package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const mailingSendingError = "Рассылка уже отправляется. Изменения сейчас сохранить нельзя."

// Starting a mailing takes the same campaign row lock. Keep it until the
// message mutation commits so a save cannot race past the status check.
func lockMailingMessage(ctx context.Context, tx pgx.Tx, messageID int64) (bool, error) {
	rows, err := tx.Query(ctx, `SELECT c.run_status FROM broadcast_campaigns c
		JOIN broadcast_steps b ON b.campaign_id=c.id
		WHERE b.message_id=$1 AND c.mode='manual'
		ORDER BY c.id FOR UPDATE OF c`, messageID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	locked := false
	for rows.Next() {
		var status string
		if err = rows.Scan(&status); err != nil {
			return false, err
		}
		locked = locked || status == "sending"
	}
	return locked, rows.Err()
}

func (a *App) admin(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 || len(parts) > 5 {
		bad(w, 404, "Not found")
		return
	}
	id := int64(0)
	if len(parts) >= 3 {
		var e error
		id, e = strconv.ParseInt(parts[2], 10, 64)
		if e != nil || id < 1 {
			bad(w, 400, "Invalid id")
			return
		}
	}
	var e error
	switch parts[1] {
	case "messages":
		if len(parts) >= 4 && parts[3] == "media" && id != 0 {
			mediaID := int64(0)
			if len(parts) == 5 {
				mediaID, e = strconv.ParseInt(parts[4], 10, 64)
				if e != nil || mediaID < 1 {
					bad(w, 400, "Invalid media id")
					return
				}
			}
			e = a.messageMedia(w, r, id, mediaID)
		} else if len(parts) <= 3 {
			e = a.messages(w, r, id)
		} else {
			bad(w, 404, "Not found")
		}
	case "broadcasts", "mailings":
		mode := "auto"
		if parts[1] == "mailings" {
			mode = "manual"
		}
		if len(parts) <= 3 {
			e = a.broadcastCampaigns(w, r, id, mode)
		} else if parts[3] == "steps" {
			stepID := int64(0)
			if len(parts) == 5 {
				stepID, e = strconv.ParseInt(parts[4], 10, 64)
				if e != nil || stepID < 1 {
					bad(w, 400, "Invalid step id")
					return
				}
			}
			e = a.broadcasts(w, r, id, stepID, mode)
		} else if len(parts) == 4 && parts[3] == "send" && mode == "manual" {
			e = a.launchMailing(w, r, id)
		} else if len(parts) == 4 && parts[3] == "schedule" && mode == "manual" {
			e = a.updateMailingSchedule(w, r, id)
		} else if len(parts) == 4 && parts[3] == "runs" && mode == "manual" {
			e = a.mailingRuns(w, r, id)
		} else {
			bad(w, 404, "Not found")
		}
	case "users":
		e = a.users(w, r, id)
	default:
		bad(w, 404, "Not found")
	}
	if e != nil {
		a.Log.Error("admin request failed", "resource", parts[1], "error", e)
		bad(w, 500, "Request failed")
	}
}
func (a *App) messages(w http.ResponseWriter, r *http.Request, id int64) error {
	if r.Method == "GET" {
		rows, e := a.DB.Query(r.Context(), `
			SELECT m.id,m.key,m.name,m.text,m.parse_mode,m.buttons,m.version
			FROM bot_messages m
			WHERE ($1::bigint=0 OR m.id=$1)
			ORDER BY m.id`, id)
		if e != nil {
			return e
		}
		defer rows.Close()
		items := []Content{}
		for rows.Next() {
			var c Content
			var raw []byte
			if e = rows.Scan(&c.ID, &c.Key, &c.Name, &c.Text, &c.ParseMode, &raw, &c.Version); e != nil {
				return e
			}
			if e = json.Unmarshal(raw, &c.Buttons); e != nil {
				return e
			}
			mediaRows, mediaErr := a.DB.Query(r.Context(), `
				SELECT id,media_type,media_position,sort_order,file_name,size_bytes
				FROM bot_message_media WHERE message_id=$1
				ORDER BY media_position DESC,sort_order,id`, c.ID)
			if mediaErr != nil {
				return mediaErr
			}
			c.Media = []MessageMedia{}
			for mediaRows.Next() {
				var media MessageMedia
				if mediaErr = mediaRows.Scan(&media.ID, &media.Type, &media.Position, &media.SortOrder, &media.Name, &media.Size); mediaErr != nil {
					mediaRows.Close()
					return mediaErr
				}
				c.Media = append(c.Media, media)
			}
			mediaErr = mediaRows.Err()
			mediaRows.Close()
			if mediaErr != nil {
				return mediaErr
			}
			items = append(items, c)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if id != 0 {
			if len(items) == 0 {
				bad(w, 404, "Not found")
			} else {
				respond(w, 200, items[0])
			}
		} else {
			respond(w, 200, items)
		}
		return nil
	}
	if r.Method != "PUT" || id == 0 {
		bad(w, 405, "Method not allowed")
		return nil
	}
	var c Content
	if e := decode(r, &c); e != nil {
		bad(w, 400, "Invalid JSON")
		return nil
	}
	tx, e := a.DB.Begin(r.Context())
	if e != nil {
		return e
	}
	defer tx.Rollback(r.Context())
	var key string
	e = tx.QueryRow(r.Context(), "SELECT key FROM bot_messages WHERE id=$1", id).Scan(&key)
	if errors.Is(e, pgx.ErrNoRows) {
		bad(w, 404, "Not found")
		return nil
	}
	if e != nil {
		return e
	}
	lockedMailing, e := lockMailingMessage(r.Context(), tx, id)
	if e != nil {
		return e
	}
	if lockedMailing {
		bad(w, 409, mailingSendingError)
		return nil
	}
	if e = validateContent(c); e != nil {
		bad(w, 400, e.Error())
		return nil
	}
	if strings.HasPrefix(key, "command_") && (len(c.Text) > 256 || len(c.Buttons) != 0 || c.ParseMode != "") {
		bad(w, 400, "Command descriptions must be plain text, 1–256 bytes, without buttons")
		return nil
	}
	var hasRegularMedia bool
	if e = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM bot_message_media WHERE message_id=$1 AND media_type<>'video_note')", id).Scan(&hasRegularMedia); e != nil {
		return e
	}
	if hasRegularMedia && utf8.RuneCountInString(c.Text) > 1024 {
		bad(w, 400, "Text with photos or videos must be no longer than 1024 characters")
		return nil
	}
	result, e := tx.Exec(r.Context(), "UPDATE bot_messages SET name=$2,text=$3,parse_mode=$4,buttons=$5,version=version+1,updated_at=now() WHERE id=$1 AND version=$6", id, c.Name, c.Text, c.ParseMode, marshal(c.Buttons), c.Version)
	if e != nil {
		return e
	}
	if result.RowsAffected() == 0 {
		bad(w, 409, "Message changed. Reload before saving.")
		return nil
	}
	if _, e = tx.Exec(r.Context(), "INSERT INTO audit_log(action,entity_id) VALUES('message.update',$1)", jsonNumber(id)); e != nil {
		return e
	}
	if e = tx.Commit(r.Context()); e != nil {
		return e
	}
	respond(w, 200, map[string]bool{"ok": true})
	return nil
}

const maxMessageMediaSize = 50 << 20

func (a *App) messageMedia(w http.ResponseWriter, r *http.Request, id, mediaID int64) error {
	if r.Method == "GET" {
		if mediaID == 0 {
			bad(w, 400, "Media id is required")
			return nil
		}
		var name, mimeType string
		var data []byte
		e := a.DB.QueryRow(r.Context(), "SELECT file_name,mime_type,data FROM bot_message_media WHERE message_id=$1 AND id=$2", id, mediaID).Scan(&name, &mimeType, &data)
		if errors.Is(e, pgx.ErrNoRows) {
			bad(w, 404, "Media not found")
			return nil
		}
		if e != nil {
			return e
		}
		w.Header().Set("Content-Type", mimeType)
		w.Header().Set("Content-Disposition", "inline")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return nil
	}
	var lockedMailing bool
	if err := a.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM broadcast_steps b JOIN broadcast_campaigns c ON c.id=b.campaign_id
		WHERE b.message_id=$1 AND c.mode='manual' AND c.run_status='sending')`, id).Scan(&lockedMailing); err != nil {
		return err
	}
	if lockedMailing {
		bad(w, 409, mailingSendingError)
		return nil
	}
	if r.Method == "DELETE" {
		if mediaID == 0 {
			bad(w, 400, "Media id is required")
			return nil
		}
		tx, e := a.DB.Begin(r.Context())
		if e != nil {
			return e
		}
		defer tx.Rollback(r.Context())
		lockedMailing, e = lockMailingMessage(r.Context(), tx, id)
		if e != nil {
			return e
		}
		if lockedMailing {
			bad(w, 409, mailingSendingError)
			return nil
		}
		result, e := tx.Exec(r.Context(), "DELETE FROM bot_message_media WHERE message_id=$1 AND id=$2", id, mediaID)
		if e != nil {
			return e
		}
		if result.RowsAffected() == 0 {
			bad(w, 404, "Media not found")
			return nil
		}
		var version int
		if e = tx.QueryRow(r.Context(), "UPDATE bot_messages SET version=version+1,updated_at=now() WHERE id=$1 RETURNING version", id).Scan(&version); e != nil {
			return e
		}
		if _, e = tx.Exec(r.Context(), "INSERT INTO audit_log(action,entity_id) VALUES('message.media.delete',$1)", jsonNumber(id)); e != nil {
			return e
		}
		if e = tx.Commit(r.Context()); e != nil {
			return e
		}
		respond(w, http.StatusOK, map[string]any{"ok": true, "version": version})
		return nil
	}
	if r.Method == "PUT" {
		if mediaID == 0 {
			var input struct {
				Order []int64 `json:"order"`
			}
			if e := decode(r, &input); e != nil || len(input.Order) == 0 || len(input.Order) > 10 {
				bad(w, 400, "Invalid media order")
				return nil
			}
			tx, e := a.DB.Begin(r.Context())
			if e != nil {
				return e
			}
			defer tx.Rollback(r.Context())
			lockedMailing, e = lockMailingMessage(r.Context(), tx, id)
			if e != nil {
				return e
			}
			if lockedMailing {
				bad(w, 409, mailingSendingError)
				return nil
			}
			var regularCount int
			if e = tx.QueryRow(r.Context(), "SELECT count(*) FROM bot_message_media WHERE message_id=$1 AND media_type<>'video_note'", id).Scan(&regularCount); e != nil {
				return e
			}
			if regularCount != len(input.Order) {
				bad(w, 409, "Media list changed. Reload before reordering.")
				return nil
			}
			seen := map[int64]bool{}
			for index, orderedID := range input.Order {
				if orderedID < 1 || seen[orderedID] {
					bad(w, 400, "Invalid media order")
					return nil
				}
				seen[orderedID] = true
				result, updateErr := tx.Exec(r.Context(), `
					UPDATE bot_message_media SET sort_order=$3,media_position='before',updated_at=now()
					WHERE message_id=$1 AND id=$2 AND media_type<>'video_note'`, id, orderedID, index)
				if updateErr != nil {
					return updateErr
				}
				if result.RowsAffected() != 1 {
					bad(w, 400, "Invalid media order")
					return nil
				}
			}
			var version int
			if e = tx.QueryRow(r.Context(), "UPDATE bot_messages SET version=version+1,updated_at=now() WHERE id=$1 RETURNING version", id).Scan(&version); e != nil {
				return e
			}
			if _, e = tx.Exec(r.Context(), "INSERT INTO audit_log(action,entity_id) VALUES('message.media.reorder',$1)", jsonNumber(id)); e != nil {
				return e
			}
			if e = tx.Commit(r.Context()); e != nil {
				return e
			}
			respond(w, http.StatusOK, map[string]any{"ok": true, "version": version})
			return nil
		}
		var input struct {
			Position string `json:"position"`
		}
		if e := decode(r, &input); e != nil || input.Position != "before" && input.Position != "after" {
			bad(w, 400, "Select whether the video note goes before or after the message")
			return nil
		}
		tx, e := a.DB.Begin(r.Context())
		if e != nil {
			return e
		}
		defer tx.Rollback(r.Context())
		lockedMailing, e = lockMailingMessage(r.Context(), tx, id)
		if e != nil {
			return e
		}
		if lockedMailing {
			bad(w, 409, mailingSendingError)
			return nil
		}
		result, e := tx.Exec(r.Context(), "UPDATE bot_message_media SET media_position=$3,updated_at=now() WHERE message_id=$1 AND id=$2 AND media_type='video_note'", id, mediaID, input.Position)
		if e != nil {
			return e
		}
		if result.RowsAffected() == 0 {
			bad(w, 404, "Media not found")
			return nil
		}
		var version int
		if e = tx.QueryRow(r.Context(), "UPDATE bot_messages SET version=version+1,updated_at=now() WHERE id=$1 RETURNING version", id).Scan(&version); e != nil {
			return e
		}
		if _, e = tx.Exec(r.Context(), "INSERT INTO audit_log(action,entity_id) VALUES('message.media.update',$1)", jsonNumber(mediaID)); e != nil {
			return e
		}
		if e = tx.Commit(r.Context()); e != nil {
			return e
		}
		respond(w, http.StatusOK, map[string]any{"ok": true, "version": version})
		return nil
	}
	if r.Method != "POST" {
		bad(w, 405, "Method not allowed")
		return nil
	}
	if e := r.ParseMultipartForm(1 << 20); e != nil {
		bad(w, 400, "Invalid media upload")
		return nil
	}
	defer r.MultipartForm.RemoveAll()
	mediaType := r.FormValue("media_type")
	if mediaType != "photo" && mediaType != "video" && mediaType != "video_note" {
		bad(w, 400, "Select photo, video or video note")
		return nil
	}
	mediaPosition := "before"
	if mediaType == "video_note" {
		mediaPosition = r.FormValue("media_position")
		if mediaPosition == "" {
			mediaPosition = "before"
		}
		if mediaPosition != "before" && mediaPosition != "after" {
			bad(w, 400, "Select whether the video note goes before or after the message")
			return nil
		}
	}
	file, header, e := r.FormFile("file")
	if e != nil {
		bad(w, 400, "Select an MP4 file")
		return nil
	}
	defer file.Close()
	data, e := io.ReadAll(io.LimitReader(file, maxMessageMediaSize+1))
	if e != nil {
		return e
	}
	if len(data) == 0 || len(data) > maxMessageMediaSize || mediaType == "photo" && len(data) > 10<<20 {
		bad(w, 400, "Photo must be up to 10 MB and video up to 50 MB")
		return nil
	}
	mimeType := http.DetectContentType(data[:min(len(data), 512)])
	validMIME := mediaType == "photo" && (mimeType == "image/jpeg" || mimeType == "image/png") ||
		(mediaType == "video" || mediaType == "video_note") && mimeType == "video/mp4"
	if !validMIME {
		bad(w, 400, "Photos must be JPEG or PNG; videos must be MP4")
		return nil
	}
	name := filepath.Base(header.Filename)
	if name == "." || name == "" || len(name) > 255 {
		name = "video.mp4"
	}
	var key, messageText string
	e = a.DB.QueryRow(r.Context(), "SELECT key,text FROM bot_messages WHERE id=$1", id).Scan(&key, &messageText)
	if errors.Is(e, pgx.ErrNoRows) {
		bad(w, 404, "Message not found")
		return nil
	}
	if e != nil {
		return e
	}
	if strings.HasPrefix(key, "command_") {
		bad(w, 400, "Command descriptions cannot contain media")
		return nil
	}
	if mediaType != "video_note" && utf8.RuneCountInString(messageText) > 1024 {
		bad(w, 400, "Shorten the message text to 1024 characters before adding photos or videos")
		return nil
	}
	tx, e := a.DB.Begin(r.Context())
	if e != nil {
		return e
	}
	defer tx.Rollback(r.Context())
	lockedMailing, e = lockMailingMessage(r.Context(), tx, id)
	if e != nil {
		return e
	}
	if lockedMailing {
		bad(w, 409, mailingSendingError)
		return nil
	}
	var regularCount, noteCount int
	if e = tx.QueryRow(r.Context(), `SELECT count(*) FILTER (WHERE media_type<>'video_note'),count(*) FILTER (WHERE media_type='video_note') FROM bot_message_media WHERE message_id=$1`, id).Scan(&regularCount, &noteCount); e != nil {
		return e
	}
	if mediaType == "video_note" && noteCount >= 1 {
		bad(w, 409, "Only one video note is allowed")
		return nil
	}
	if mediaType != "video_note" && regularCount >= 10 {
		bad(w, 409, "No more than 10 media files are allowed")
		return nil
	}
	var createdID int64
	var sortOrder int
	e = tx.QueryRow(r.Context(), `
		INSERT INTO bot_message_media(message_id,media_type,media_position,sort_order,file_name,mime_type,size_bytes,data)
		VALUES($1,$2,$3,COALESCE((SELECT max(sort_order)+1 FROM bot_message_media WHERE message_id=$1),0),$4,$5,$6,$7)
		RETURNING id,sort_order`, id, mediaType, mediaPosition, name, mimeType, len(data), data).Scan(&createdID, &sortOrder)
	if e != nil {
		return e
	}
	var version int
	if e = tx.QueryRow(r.Context(), "UPDATE bot_messages SET version=version+1,updated_at=now() WHERE id=$1 RETURNING version", id).Scan(&version); e != nil {
		return e
	}
	if _, e = tx.Exec(r.Context(), "INSERT INTO audit_log(action,entity_id) VALUES('message.media.upload',$1)", jsonNumber(id)); e != nil {
		return e
	}
	if e = tx.Commit(r.Context()); e != nil {
		return e
	}
	respond(w, http.StatusOK, map[string]any{
		"ok": true, "version": version, "id": createdID,
		"media": map[string]any{
			"id": createdID, "type": mediaType, "position": mediaPosition,
			"sort_order": sortOrder, "name": name, "size": len(data),
		},
	})
	return nil
}

type BroadcastCampaign struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	AudienceStatus string     `json:"audience_status"`
	Enabled        bool       `json:"enabled"`
	BlockCount     int        `json:"block_count"`
	RunStatus      string     `json:"run_status"`
	ScheduledAt    *time.Time `json:"scheduled_at"`
	StartedAt      *time.Time `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at"`
	RecipientCount int        `json:"recipient_count"`
	CompletedCount int        `json:"completed_count"`
	FailedCount    int        `json:"failed_count"`
	SentCount      int        `json:"sent_count"`
	MessageCount   int        `json:"message_count"`
	RunCount       int        `json:"run_count"`
}

func (a *App) broadcastCampaigns(w http.ResponseWriter, r *http.Request, id int64, mode string) error {
	ctx := r.Context()
	if r.Method == http.MethodGet {
		rows, err := a.DB.Query(ctx, `SELECT c.id,c.name,c.audience_status,c.enabled,
			(SELECT count(*)::integer FROM broadcast_steps b WHERE b.campaign_id=c.id),
			c.run_status,c.scheduled_at,c.started_at,c.completed_at,
			(SELECT count(*)::integer FROM mailing_deliveries d WHERE d.run_id=c.current_run_id),
			(SELECT count(*)::integer FROM mailing_deliveries d WHERE d.run_id=c.current_run_id AND d.status='completed'),
			(SELECT count(*)::integer FROM mailing_deliveries d WHERE d.run_id=c.current_run_id AND d.status='failed'),
			(SELECT COALESCE(sum(d.sent_count),0)::integer FROM mailing_deliveries d WHERE d.run_id=c.current_run_id),
			(SELECT count(*)::integer FROM broadcast_steps b WHERE b.campaign_id=c.id AND b.kind='message' AND b.enabled),
			(SELECT count(*)::integer FROM mailing_runs r WHERE r.campaign_id=c.id)
			FROM broadcast_campaigns c
			WHERE c.mode=$2 AND ($1::bigint=0 OR c.id=$1) ORDER BY c.id DESC`, id, mode)
		if err != nil {
			return err
		}
		defer rows.Close()
		items := []BroadcastCampaign{}
		for rows.Next() {
			var item BroadcastCampaign
			if err = rows.Scan(&item.ID, &item.Name, &item.AudienceStatus, &item.Enabled, &item.BlockCount, &item.RunStatus, &item.ScheduledAt, &item.StartedAt, &item.CompletedAt, &item.RecipientCount, &item.CompletedCount, &item.FailedCount, &item.SentCount, &item.MessageCount, &item.RunCount); err != nil {
				return err
			}
			items = append(items, item)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if id != 0 {
			if len(items) == 0 {
				bad(w, 404, "Not found")
			} else {
				respond(w, 200, items[0])
			}
		} else {
			respond(w, 200, items)
		}
		return nil
	}
	if (r.Method != http.MethodPost || id != 0) && (r.Method != http.MethodPut || id == 0) && (r.Method != http.MethodDelete || id == 0) {
		bad(w, 405, "Method not allowed")
		return nil
	}
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var currentStatus, currentAudience string
	if id != 0 {
		err = tx.QueryRow(ctx, "SELECT run_status,audience_status FROM broadcast_campaigns WHERE id=$1 AND mode=$2 FOR UPDATE", id, mode).Scan(&currentStatus, &currentAudience)
		if errors.Is(err, pgx.ErrNoRows) {
			bad(w, 404, "Not found")
			return nil
		}
		if err != nil {
			return err
		}
		if mode == "manual" && (currentStatus == "scheduled" || currentStatus == "sending") && r.Method != http.MethodPut {
			bad(w, 409, mailingSendingError)
			return nil
		}
	}
	if r.Method == http.MethodDelete {
		rows, queryErr := tx.Query(ctx, `SELECT m.id FROM broadcast_steps b JOIN bot_messages m ON m.id=b.message_id
			WHERE b.campaign_id=$1 AND m.key LIKE 'broadcast_step_%'`, id)
		if queryErr != nil {
			return queryErr
		}
		messageIDs, queryErr := pgx.CollectRows(rows, pgx.RowTo[int64])
		if queryErr != nil {
			return queryErr
		}
		result, deleteErr := tx.Exec(ctx, "DELETE FROM broadcast_campaigns WHERE id=$1", id)
		if deleteErr != nil {
			return deleteErr
		}
		if result.RowsAffected() == 0 {
			bad(w, 404, "Not found")
			return nil
		}
		for _, messageID := range messageIDs {
			if _, err = tx.Exec(ctx, "DELETE FROM bot_messages WHERE id=$1 AND NOT EXISTS(SELECT 1 FROM broadcast_steps WHERE message_id=$1)", messageID); err != nil {
				return err
			}
		}
	} else {
		var item BroadcastCampaign
		if decode(r, &item) != nil {
			bad(w, 400, "Invalid JSON")
			return nil
		}
		item.Name = strings.TrimSpace(item.Name)
		if item.AudienceStatus == "" {
			item.AudienceStatus = "new"
		}
		if utf8.RuneCountInString(item.Name) < 1 || utf8.RuneCountInString(item.Name) > 200 ||
			(item.AudienceStatus != "all" && item.AudienceStatus != "new" && item.AudienceStatus != "active" && item.AudienceStatus != "expired") {
			bad(w, 400, "Invalid campaign name or audience")
			return nil
		}
		if mode == "manual" && (currentStatus == "scheduled" || currentStatus == "sending") && item.AudienceStatus != currentAudience {
			if currentStatus == "scheduled" {
				// Scheduled runs select recipients only when they start.
				if _, err = tx.Exec(ctx, "UPDATE mailing_runs SET audience_status=$2 WHERE campaign_id=$1 AND status='scheduled'", id, item.AudienceStatus); err != nil {
					return err
				}
			} else {
				bad(w, 409, "Рассылка уже отправляется. Изменить аудиторию сейчас нельзя.")
				return nil
			}
		}
		if r.Method == http.MethodPost {
			err = tx.QueryRow(ctx, "INSERT INTO broadcast_campaigns(name,audience_status,enabled,mode) VALUES($1,$2,false,$3) RETURNING id", item.Name, item.AudienceStatus, mode).Scan(&id)
			if err != nil {
				return err
			}
		} else {
			result, updateErr := tx.Exec(ctx, "UPDATE broadcast_campaigns SET name=$2,audience_status=$3,enabled=$4,updated_at=now() WHERE id=$1 AND mode=$5", id, item.Name, item.AudienceStatus, item.Enabled && mode == "auto", mode)
			if updateErr != nil {
				return updateErr
			}
			if result.RowsAffected() == 0 {
				bad(w, 404, "Not found")
				return nil
			}
		}
	}
	if _, err = tx.Exec(ctx, "INSERT INTO audit_log(action,entity_id) VALUES($1,$2)", "broadcast.campaign."+r.Method, jsonNumber(id)); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	respond(w, 200, map[string]int64{"id": id})
	return nil
}

type Broadcast struct {
	ID        int64   `json:"id"`
	Kind      string  `json:"kind"`
	MessageID int64   `json:"message_id"`
	Position  int     `json:"position"`
	Delay     int     `json:"delay_seconds"`
	Enabled   bool    `json:"enabled"`
	Message   Content `json:"message"`
}

func (a *App) broadcasts(w http.ResponseWriter, r *http.Request, campaignID, id int64, mode string) error {
	ctx := r.Context()
	if campaignID < 1 {
		bad(w, 400, "Invalid campaign id")
		return nil
	}
	var runStatus string
	if err := a.DB.QueryRow(ctx, "SELECT run_status FROM broadcast_campaigns WHERE id=$1 AND mode=$2", campaignID, mode).Scan(&runStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			bad(w, 404, "Not found")
			return nil
		}
		return err
	}
	if mode == "manual" && r.Method != http.MethodGet && runStatus == "sending" {
		bad(w, 409, mailingSendingError)
		return nil
	}
	if r.Method == http.MethodGet && id == 0 {
		rows, err := a.DB.Query(ctx, "SELECT id,kind,COALESCE(message_id,0),position,delay_seconds,enabled FROM broadcast_steps WHERE campaign_id=$1 ORDER BY position,id", campaignID)
		if err != nil {
			return err
		}
		defer rows.Close()
		items := []Broadcast{}
		for rows.Next() {
			var b Broadcast
			if err = rows.Scan(&b.ID, &b.Kind, &b.MessageID, &b.Position, &b.Delay, &b.Enabled); err != nil {
				return err
			}
			items = append(items, b)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		respond(w, 200, items)
		return nil
	}
	if r.Method == http.MethodPut && id == 0 {
		var input struct {
			Order []int64 `json:"order"`
		}
		if decode(r, &input) != nil || len(input.Order) == 0 {
			bad(w, 400, "Invalid block order")
			return nil
		}
		tx, err := a.DB.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if mode == "manual" {
			var status string
			if err = tx.QueryRow(ctx, "SELECT run_status FROM broadcast_campaigns WHERE id=$1 AND mode=$2 FOR UPDATE", campaignID, mode).Scan(&status); err != nil {
				return err
			}
			if status == "sending" {
				bad(w, 409, mailingSendingError)
				return nil
			}
		}
		if _, err = tx.Exec(ctx, "LOCK TABLE broadcast_steps IN SHARE ROW EXCLUSIVE MODE"); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, "SELECT id FROM broadcast_steps WHERE campaign_id=$1", campaignID)
		if err != nil {
			return err
		}
		existing, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return err
		}
		if len(existing) != len(input.Order) {
			bad(w, 409, "Block list changed. Reload before reordering.")
			return nil
		}
		known := make(map[int64]bool, len(existing))
		for _, item := range existing {
			known[item] = true
		}
		for index, item := range input.Order {
			if !known[item] {
				bad(w, 400, "Invalid block order")
				return nil
			}
			delete(known, item)
			if _, err = tx.Exec(ctx, "UPDATE broadcast_steps SET position=$2 WHERE id=$1 AND campaign_id=$3", item, index+1, campaignID); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, "INSERT INTO audit_log(action,entity_id) VALUES('broadcast.reorder',$1)", jsonNumber(campaignID)); err != nil {
			return err
		}
		if err = tx.Commit(ctx); err != nil {
			return err
		}
		respond(w, 200, map[string]bool{"ok": true})
		return nil
	}
	if (r.Method != http.MethodPost || id != 0) && (r.Method != http.MethodPut || id == 0) && (r.Method != http.MethodDelete || id == 0) {
		bad(w, 405, "Method not allowed")
		return nil
	}
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if mode == "manual" {
		var status string
		if err = tx.QueryRow(ctx, "SELECT run_status FROM broadcast_campaigns WHERE id=$1 AND mode=$2 FOR UPDATE", campaignID, mode).Scan(&status); err != nil {
			return err
		}
		if status == "sending" {
			bad(w, 409, mailingSendingError)
			return nil
		}
	}
	var messageID int64
	if r.Method == http.MethodDelete {
		var key, kind string
		err = tx.QueryRow(ctx, "SELECT COALESCE(b.message_id,0),COALESCE(m.key,''),b.kind FROM broadcast_steps b LEFT JOIN bot_messages m ON m.id=b.message_id WHERE b.id=$1 AND b.campaign_id=$2", id, campaignID).Scan(&messageID, &key, &kind)
		if errors.Is(err, pgx.ErrNoRows) {
			bad(w, 404, "Not found")
			return nil
		}
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "DELETE FROM broadcast_steps WHERE id=$1 AND campaign_id=$2", id, campaignID); err != nil {
			return err
		}
		if kind == "message" && strings.HasPrefix(key, "broadcast_step_") {
			if _, err = tx.Exec(ctx, "DELETE FROM bot_messages WHERE id=$1 AND NOT EXISTS(SELECT 1 FROM broadcast_steps WHERE message_id=$1)", messageID); err != nil {
				return err
			}
		}
	} else {
		var block Broadcast
		if decode(r, &block) != nil {
			bad(w, 400, "Invalid JSON")
			return nil
		}
		if block.Kind == "" {
			block.Kind = "message"
		}
		if block.Kind != "message" && block.Kind != "delay" {
			bad(w, 400, "Invalid block type")
			return nil
		}
		if block.Kind == "delay" {
			// TODO before live broadcasts: restore the 60-second minimum in the API,
			// frontend, and a new DB migration. One second is for testing only.
			if block.Delay < 1 || block.Delay > 31536000 {
				bad(w, 400, "Delay must be between one second and one year")
				return nil
			}
		} else if err = validateContent(block.Message); err != nil {
			bad(w, 400, err.Error())
			return nil
		}
		if r.Method == http.MethodPost {
			if _, err = tx.Exec(ctx, "LOCK TABLE broadcast_steps IN SHARE ROW EXCLUSIVE MODE"); err != nil {
				return err
			}
			var position int
			if err = tx.QueryRow(ctx, "SELECT COALESCE(MAX(position),0)+1 FROM broadcast_steps WHERE campaign_id=$1", campaignID).Scan(&position); err != nil {
				return err
			}
			if block.Kind == "message" {
				err = tx.QueryRow(ctx, `INSERT INTO bot_messages(key,name,text,parse_mode,buttons) VALUES('broadcast_step_' || gen_random_uuid()::text,$1,$2,$3,$4) RETURNING id`, block.Message.Name, block.Message.Text, block.Message.ParseMode, marshal(block.Message.Buttons)).Scan(&messageID)
				if err != nil {
					return err
				}
				err = tx.QueryRow(ctx, "INSERT INTO broadcast_steps(campaign_id,kind,message_id,position,delay_seconds,enabled) VALUES($1,'message',$2,$3,0,$4) RETURNING id", campaignID, messageID, position, block.Enabled).Scan(&id)
			} else {
				err = tx.QueryRow(ctx, "INSERT INTO broadcast_steps(campaign_id,kind,message_id,position,delay_seconds,enabled) VALUES($1,'delay',NULL,$2,$3,true) RETURNING id", campaignID, position, block.Delay).Scan(&id)
			}
			if err != nil {
				return err
			}
		} else if block.Kind == "delay" {
			var actualKind string
			err = tx.QueryRow(ctx, "SELECT kind FROM broadcast_steps WHERE id=$1 AND campaign_id=$2 FOR UPDATE", id, campaignID).Scan(&actualKind)
			if errors.Is(err, pgx.ErrNoRows) {
				bad(w, 404, "Not found")
				return nil
			}
			if err != nil {
				return err
			}
			if actualKind != "delay" {
				bad(w, 409, "Block type changed")
				return nil
			}
			if _, err = tx.Exec(ctx, "UPDATE broadcast_steps SET delay_seconds=$2 WHERE id=$1 AND campaign_id=$3", id, block.Delay, campaignID); err != nil {
				return err
			}
		} else {
			var key, actualKind string
			err = tx.QueryRow(ctx, "SELECT b.message_id,m.key,b.kind FROM broadcast_steps b JOIN bot_messages m ON m.id=b.message_id WHERE b.id=$1 AND b.campaign_id=$2 FOR UPDATE OF b,m", id, campaignID).Scan(&messageID, &key, &actualKind)
			if errors.Is(err, pgx.ErrNoRows) {
				bad(w, 404, "Not found")
				return nil
			}
			if err != nil {
				return err
			}
			if actualKind != "message" || !strings.HasPrefix(key, "broadcast_step_") {
				bad(w, 409, "Recreate this legacy step to edit its message")
				return nil
			}
			var hasRegularMedia bool
			if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM bot_message_media WHERE message_id=$1 AND media_type<>'video_note')", messageID).Scan(&hasRegularMedia); err != nil {
				return err
			}
			if hasRegularMedia && utf8.RuneCountInString(block.Message.Text) > 1024 {
				bad(w, 400, "Text with photos or videos must be no longer than 1024 characters")
				return nil
			}
			var result pgconn.CommandTag
			result, err = tx.Exec(ctx, "UPDATE bot_messages SET name=$2,text=$3,parse_mode=$4,buttons=$5,version=version+1,updated_at=now() WHERE id=$1 AND version=$6", messageID, block.Message.Name, block.Message.Text, block.Message.ParseMode, marshal(block.Message.Buttons), block.Message.Version)
			if err != nil {
				return err
			}
			if result.RowsAffected() == 0 {
				bad(w, 409, "Message changed. Reload before saving.")
				return nil
			}
			if _, err = tx.Exec(ctx, "UPDATE broadcast_steps SET enabled=$2 WHERE id=$1 AND campaign_id=$3", id, block.Enabled, campaignID); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec(ctx, "INSERT INTO audit_log(action,entity_id) VALUES($1,$2)", "broadcast."+r.Method, jsonNumber(id)); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	respond(w, 200, map[string]int64{"id": id, "message_id": messageID})
	return nil
}
func (a *App) users(w http.ResponseWriter, r *http.Request, id int64) error {
	if r.Method != "GET" {
		bad(w, 405, "Method not allowed")
		return nil
	}
	q := r.URL.Query()
	status := q.Get("status")
	renew := q.Get("auto_renew")
	search := q.Get("search")
	if status != "" && status != "new" && status != "active" && status != "expired" {
		bad(w, 400, "Invalid status")
		return nil
	}
	if renew != "" && renew != "true" && renew != "false" {
		bad(w, 400, "Invalid auto_renew")
		return nil
	}
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 100000 {
		bad(w, 400, "Invalid page")
		return nil
	}
	if len(search) > 100 {
		bad(w, 400, "Search too long")
		return nil
	}
	var data []byte
	e := a.DB.QueryRow(r.Context(), `WITH filtered AS (SELECT id,telegram_user_id::text,username,first_name,last_name,email,status,expires_at,auto_renew,days_left,first_started_at,last_activity_at FROM user_status WHERE ($1::bigint=0 OR id=$1) AND ($2='' OR status=$2) AND ($3='' OR auto_renew::text=$3) AND ($4='' OR telegram_user_id::text=$4 OR username ILIKE '%' || $4 || '%')), paged AS (SELECT * FROM filtered ORDER BY id DESC LIMIT 50 OFFSET $5) SELECT json_build_object('items',COALESCE((SELECT json_agg(paged) FROM paged),'[]'::json),'total',(SELECT count(*) FROM filtered),'page',$6::int)`, id, status, renew, search, (page-1)*50, page).Scan(&data)
	if e != nil {
		return e
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(data)
	return nil
}
