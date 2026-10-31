package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/gotsta1/tomas_blank_bot/backend/internal/telegram"
	"github.com/jackc/pgx/v5"
)

func (a *App) processTelegram(ctx context.Context, u telegram.Update) error {
	if u.Join != nil {
		return a.join(ctx, u.Join.From.ID, u.Join.Chat.ID)
	}
	var from telegram.User
	var messageID int64
	var action, text string
	if u.Callback != nil {
		if u.Callback.Message == nil || u.Callback.Message.Chat.Type != "private" || u.Callback.Message.Chat.ID != u.Callback.From.ID {
			return nil
		}
		from = u.Callback.From
		messageID = u.Callback.Message.ID
		action = u.Callback.Data
		// Acknowledgement may already have expired after queue retry; business processing still proceeds.
		answer := map[string]any{"callback_query_id": u.Callback.ID}
		if action == "pay:check" {
			answer["text"] = "Проверяем оплату…"
		}
		if e := a.Telegram.Call(ctx, "answerCallbackQuery", answer, nil); e != nil {
			a.Log.Debug("callback acknowledgement failed", "error", e)
		}
	} else if u.Message != nil {
		if u.Message.Chat.Type != "private" {
			return nil
		}
		from = u.Message.From
		text = strings.TrimSpace(u.Message.Text)
		if text == "/start" {
			duplicate, err := a.rapidDuplicateStart(ctx, u.ID, from.ID)
			if err != nil {
				return err
			}
			if duplicate {
				return nil
			}
		}
	} else {
		return nil
	}
	var uid int64
	e := a.DB.QueryRow(ctx, `INSERT INTO users(telegram_user_id,username,first_name,last_name,first_started_at) VALUES($1,$2,$3,$4,CASE WHEN $5 THEN now() ELSE NULL END) ON CONFLICT(telegram_user_id) DO UPDATE SET username=excluded.username,first_name=excluded.first_name,last_name=excluded.last_name,last_activity_at=now(),blocked=false,first_started_at=COALESCE(users.first_started_at,excluded.first_started_at) RETURNING id`, from.ID, from.Username, from.FirstName, from.LastName, strings.HasPrefix(text, "/start")).Scan(&uid)
	if e != nil {
		return e
	}
	key := ""
	var flow string
	if action == "" {
		command := ""
		if fields := strings.Fields(text); len(fields) > 0 {
			command = strings.Split(fields[0], "@")[0]
		}
		switch command {
		case "/start":
			key = "start"
			_, e = a.DB.Exec(ctx, "UPDATE users SET pending_flow='',selected_periodicity=NULL WHERE id=$1", uid)
		case "/buy":
			action = "club:join"
		case "/status":
			action = "subscription:status"
		case "/help":
			key = "help"
		default:
			var pending string
			if e = a.DB.QueryRow(ctx, "SELECT pending_flow FROM users WHERE id=$1", uid).Scan(&pending); e != nil {
				return e
			}
			if pending == "" {
				key = "help"
				break
			}
			address, err := mail.ParseAddress(text)
			if err != nil || address.Address != text || len(text) > 254 {
				key = "email_invalid"
				break
			}
			if _, e = a.DB.Exec(ctx, "UPDATE users SET email=$2,pending_flow='' WHERE id=$1", uid, text); e != nil {
				return e
			}
			flow = pending
		}
		if e != nil {
			return e
		}
	}
	if action != "" {
		if periodicity, ok := periodicityFromAction(action); ok {
			_, e = a.DB.Exec(ctx, "UPDATE users SET selected_periodicity=$2 WHERE id=$1", uid, periodicity)
			if e != nil {
				return e
			}
			key = "choose_payment_region"
			action = ""
		}
	}
	if action != "" {
		switch action {
		case "nav:start":
			key = "start"
		case "nav:plan":
			key = "choose_plan"
			_, e = a.DB.Exec(ctx, "UPDATE users SET pending_flow='',selected_periodicity=NULL WHERE id=$1", uid)
		case "nav:region":
			key = "choose_payment_region"
			_, e = a.DB.Exec(ctx, "UPDATE users SET pending_flow='' WHERE id=$1", uid)
		case "club:about":
			key = "about_club"
		case "club:join":
			key = "choose_plan"
			var active bool
			if e = a.DB.QueryRow(ctx, "SELECT status='active' FROM user_status WHERE id=$1", uid).Scan(&active); e != nil {
				return e
			}
			if active {
				key = "subscription_status_active"
			}
			_, e = a.DB.Exec(ctx, "UPDATE users SET pending_flow='',selected_periodicity=NULL WHERE id=$1", uid)
		case "pay:region:ru":
			key = "choose_ru_payment"
		case "pay:region:foreign":
			key = "choose_foreign_payment"
		case "pay:ru:sbp", "pay:ru:card", "pay:foreign:paypal", "pay:foreign:card", "pay:foreign:applepay":
			flow = action
		case "pay:check":
			var contract string
			e = a.DB.QueryRow(ctx, "SELECT contract_id FROM subscriptions WHERE user_id=$1 ORDER BY created_at DESC LIMIT 1", uid).Scan(&contract)
			if e == nil {
				e = a.SyncSubscription(ctx, contract)
			}
			if e != nil && !errors.Is(e, ErrPending) && !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
			var status string
			if e = a.DB.QueryRow(ctx, "SELECT status FROM user_status WHERE id=$1", uid).Scan(&status); e != nil {
				return e
			}
			if status == "active" {
				// SyncSubscription queues the one success notification in the outbox.
				// Sending it here too duplicates the confirmation.
				return nil
			}
			key = "payment_pending"
		case "subscription:status":
			var status string
			if e = a.DB.QueryRow(ctx, "SELECT status FROM user_status WHERE id=$1", uid).Scan(&status); e != nil {
				return e
			}
			key = "subscription_status_" + status
		case "subscription:cancel":
			key = "subscription_cancel_confirm"
		case "subscription:cancel:confirm":
			var activeRenewal bool
			if e = a.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM subscriptions WHERE user_id=$1 AND auto_renew AND expires_at>now())", uid).Scan(&activeRenewal); e != nil {
				return e
			}
			if !activeRenewal {
				return nil
			}
			if e = a.cancelSubscription(ctx, uid); e != nil {
				a.Log.Error("subscription cancellation failed", "user_id", uid, "error", e)
				key = "subscription_cancel_failed"
				e = nil
				break
			}
			key = "subscription_cancelled"
		default:
			return nil
		}
		if e != nil && !errors.Is(e, ErrPending) && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
	}
	if flow != "" {
		var hasEmail bool
		if e = a.DB.QueryRow(ctx, "SELECT email IS NOT NULL FROM users WHERE id=$1", uid).Scan(&hasEmail); e != nil {
			return e
		}
		if !hasEmail {
			if _, e = a.DB.Exec(ctx, "UPDATE users SET pending_flow=$2 WHERE id=$1", uid, flow); e != nil {
				return e
			}
			key = "email_request"
		} else {
			var temporaryMessageID int64
			temporaryMessageID, e = a.sendTemporary(ctx, uid, "payment_generating")
			if e != nil {
				return e
			}
			key, e = a.checkout(ctx, uid, flow)
			a.deleteTemporary(ctx, from.ID, temporaryMessageID)
			if e != nil {
				return e
			}
		}
	}
	if key == "" {
		return nil
	}
	return a.send(ctx, uid, key, messageID)
}

func (a *App) rapidDuplicateStart(ctx context.Context, updateID, telegramUserID int64) (bool, error) {
	var duplicate bool
	err := a.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM webhook_events current_event
			JOIN webhook_events previous ON previous.provider='telegram'
				AND previous.id<current_event.id
				AND previous.processed_at IS NOT NULL
				AND previous.created_at>=current_event.created_at-interval '2 seconds'
				AND previous.payload #>> '{message,text}'='/start'
				AND previous.payload #>> '{message,from,id}'=$2
			WHERE current_event.provider='telegram' AND current_event.dedup_key=$1
		)
	`, strconv.FormatInt(updateID, 10), strconv.FormatInt(telegramUserID, 10)).Scan(&duplicate)
	return duplicate, err
}

func (a *App) sendTemporary(ctx context.Context, uid int64, key string) (int64, error) {
	tx, e := a.DB.Begin(ctx)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", uid); e != nil {
		return 0, e
	}
	var sent telegram.Message
	if e = a.sendTxResult(ctx, tx, uid, key, 0, &sent); e != nil {
		return 0, e
	}
	if sent.ID == 0 {
		return 0, errors.New("telegram returned an empty message id")
	}
	if e = tx.Commit(ctx); e != nil {
		return 0, e
	}
	return sent.ID, nil
}

func (a *App) deleteTemporary(ctx context.Context, chatID, messageID int64) {
	e := a.Telegram.Call(ctx, "deleteMessage", map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
	}, nil)
	if e != nil {
		a.Log.Warn("temporary message deletion failed", "message_id", messageID, "error", e)
	}
}

func (a *App) send(ctx context.Context, uid int64, key string, messageID int64) error {
	tx, e := a.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", uid); e != nil {
		return e
	}
	if e = a.sendTx(ctx, tx, uid, key, messageID); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (a *App) sendTx(ctx context.Context, tx pgx.Tx, uid int64, key string, messageID int64) error {
	return a.sendTxResult(ctx, tx, uid, key, messageID, nil)
}

func (a *App) sendTxResult(
	ctx context.Context,
	tx pgx.Tx,
	uid int64,
	key string,
	messageID int64,
	result *telegram.Message,
) error {
	c, e := loadContent(ctx, tx, key)
	if e != nil {
		return e
	}
	var telegramID int64
	var expires *time.Time
	var days *int
	var renew bool
	if e = tx.QueryRow(ctx, "SELECT telegram_user_id,expires_at,days_left,auto_renew FROM user_status WHERE id=$1", uid).Scan(&telegramID, &expires, &days, &renew); e != nil {
		return e
	}
	vars := map[string]string{"plan_name": a.Config.PlanName, "price": a.Config.PlanPrice, "period": a.Config.PlanPeriod}
	if expires != nil {
		expiresDate := expires.In(a.Config.Location).Format("02.01.2006")
		vars["expires_date"] = expiresDate
	}
	if days != nil {
		vars["days_left"] = jsonNumber(int64(*days))
	}
	renewKey := "renew_off"
	if renew {
		renewKey = "renew_on"
	}
	renewContent, e := loadContent(ctx, tx, renewKey)
	if e != nil {
		return e
	}
	vars["auto_renew"] = renewContent.Text
	var paymentURL, periodicity, priceAmount, priceCurrency *string
	e = tx.QueryRow(
		ctx,
		`SELECT payment_url,periodicity,price_amount::text,price_currency
		 FROM checkout_attempts
		 WHERE user_id=$1 AND state='ready'
		 ORDER BY id DESC LIMIT 1`,
		uid,
	).Scan(&paymentURL, &periodicity, &priceAmount, &priceCurrency)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	if paymentURL != nil {
		vars["payment_url"] = *paymentURL
	}
	if periodicity != nil {
		vars["period"] = periodicityLabel(*periodicity)
	}
	if priceAmount != nil && priceCurrency != nil {
		vars["price"] = formatPrice(*priceAmount, *priceCurrency)
	}
	if expires != nil && expires.After(time.Now()) {
		var link string
		e = tx.QueryRow(ctx, "SELECT value FROM runtime_settings WHERE key='invite_url'").Scan(&link)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		vars["invite_url"] = link
	}
	hadPreviousMedia := false
	if messageID != 0 {
		rows, queryErr := tx.Query(ctx, "SELECT media_message_id FROM delivered_message_media WHERE user_id=$1 AND control_message_id=$2", uid, messageID)
		if queryErr != nil {
			return queryErr
		}
		var previousMediaIDs []int64
		for rows.Next() {
			var previousMediaID int64
			if e = rows.Scan(&previousMediaID); e != nil {
				rows.Close()
				return e
			}
			previousMediaIDs = append(previousMediaIDs, previousMediaID)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		hadPreviousMedia = len(previousMediaIDs) != 0
		for _, previousMediaID := range previousMediaIDs {
			deleteErr := a.Telegram.Call(ctx, "deleteMessage", map[string]any{"chat_id": telegramID, "message_id": previousMediaID}, nil)
			var apiErr *telegram.APIError
			if deleteErr != nil && (!errors.As(deleteErr, &apiErr) || apiErr.Code != 400) {
				return deleteErr
			}
		}
		if _, e = tx.Exec(ctx, "DELETE FROM delivered_message_media WHERE user_id=$1 AND control_message_id=$2", uid, messageID); e != nil {
			return e
		}
	}
	// Media is sent as new Telegram messages, while editing keeps the original
	// message at its old place in the chat. Replace that control message too, so
	// its inline keyboard stays beside the newly sent content.
	if messageID != 0 && (hadPreviousMedia || len(c.Media) != 0) {
		deleteErr := a.Telegram.Call(ctx, "deleteMessage", map[string]any{"chat_id": telegramID, "message_id": messageID}, nil)
		var apiErr *telegram.APIError
		if deleteErr != nil && (!errors.As(deleteErr, &apiErr) || apiErr.Code != 400) {
			return deleteErr
		}
		messageID = 0
	}
	mediaMessageIDs := []int64{}
	renderedText := render(c.Text, vars, c.ParseMode == "HTML")
	sendAttachedMedia := func(media []MessageMedia, caption string) error {
		var sentIDs []int64
		sentIDs, e = a.sendPositionMediaTx(ctx, tx, telegramID, media, caption, c.ParseMode)
		if e == nil {
			mediaMessageIDs = append(mediaMessageIDs, sentIDs...)
			return nil
		}
		var apiErr *telegram.APIError
		if errors.As(e, &apiErr) && apiErr.Code == 403 {
			_, err := tx.Exec(ctx, "UPDATE users SET blocked=true WHERE id=$1", uid)
			return err
		}
		return e
	}
	beforeNotes := make([]MessageMedia, 0, 1)
	afterNotes := make([]MessageMedia, 0, 1)
	regularMedia := make([]MessageMedia, 0, len(c.Media))
	for _, media := range c.Media {
		if media.Type != "video_note" {
			regularMedia = append(regularMedia, media)
		} else if media.Position == "after" {
			afterNotes = append(afterNotes, media)
		} else {
			beforeNotes = append(beforeNotes, media)
		}
	}
	var controlMessageID int64
	if len(beforeNotes) != 0 {
		if e = sendAttachedMedia(beforeNotes, ""); e != nil {
			return e
		}
	}
	keyboard := map[string]any{"inline_keyboard": renderKeyboard(c.Buttons, vars, renew)}
	var sent telegram.Message
	if len(regularMedia) != 0 {
		firstRegularIndex := len(mediaMessageIDs)
		controlMessageID, e = a.sendRichMediaTx(ctx, tx, telegramID, regularMedia, renderedText, c.ParseMode, keyboard)
		var richError *telegram.APIError
		if errors.As(e, &richError) && richError.Code == 400 {
			// Older Bot API servers or an unsupported rich formatting tag can
			// reject this request before sending anything. Keep the bot usable.
			if e = sendAttachedMedia(regularMedia, renderedText); e != nil {
				return e
			}
			controlMessageID = mediaMessageIDs[firstRegularIndex]
			e = a.Telegram.Call(ctx, "editMessageReplyMarkup", map[string]any{
				"chat_id": telegramID, "message_id": controlMessageID, "reply_markup": keyboard,
			}, nil)
			var unchanged *telegram.APIError
			if errors.As(e, &unchanged) && unchanged.Code == 400 && strings.Contains(strings.ToLower(unchanged.Description), "message is not modified") {
				e = nil
			}
		} else if e == nil {
			mediaMessageIDs = append(mediaMessageIDs, controlMessageID)
		}
		sent.ID = controlMessageID
	} else {
		payload := map[string]any{"chat_id": telegramID, "text": renderedText, "reply_markup": keyboard}
		if c.ParseMode != "" {
			payload["parse_mode"] = c.ParseMode
		}
		method := "sendMessage"
		if messageID != 0 {
			method = "editMessageText"
			payload["message_id"] = messageID
		}
		var output any
		if method == "sendMessage" && (result != nil || len(c.Media) != 0) {
			output = &sent
		}
		e = a.Telegram.Call(ctx, method, payload, output)
		if method == "editMessageText" {
			controlMessageID = messageID
		} else {
			controlMessageID = sent.ID
		}
	}
	var apiErr *telegram.APIError
	if errors.As(e, &apiErr) && apiErr.Code == 403 {
		_, err := tx.Exec(ctx, "UPDATE users SET blocked=true WHERE id=$1", uid)
		return err
	}
	if e != nil {
		return e
	}
	if len(afterNotes) != 0 {
		if e = sendAttachedMedia(afterNotes, ""); e != nil {
			return e
		}
	}
	if result != nil && sent.ID != 0 {
		*result = sent
	}
	if len(mediaMessageIDs) != 0 {
		if controlMessageID == 0 {
			return errors.New("telegram returned an empty control message id")
		}
		for _, mediaMessageID := range mediaMessageIDs {
			if _, e = tx.Exec(ctx, `
				INSERT INTO delivered_message_media(user_id,control_message_id,media_message_id)
				VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, uid, controlMessageID, mediaMessageID); e != nil {
				return e
			}
		}
	}
	return e
}

func (a *App) sendPositionMediaTx(ctx context.Context, tx pgx.Tx, telegramID int64, media []MessageMedia, caption, parseMode string) ([]int64, error) {
	if len(media) == 0 {
		return nil, nil
	}
	var note *MessageMedia
	regular := make([]MessageMedia, 0, len(media))
	for i := range media {
		if media[i].Type == "video_note" {
			note = &media[i]
		} else {
			regular = append(regular, media[i])
		}
	}
	ids := []int64{}
	sendNote := func() error {
		if note == nil {
			return nil
		}
		id, e := a.sendMediaTx(ctx, tx, telegramID, *note, "", "")
		if e == nil {
			ids = append(ids, id)
		}
		return e
	}
	// A video note is a separate Telegram message. Its position is relative to
	// the complete message, while photos and videos remain one album.
	noteFirst := note != nil && note.Position != "after"
	if noteFirst {
		if e := sendNote(); e != nil {
			return nil, e
		}
	}
	if len(regular) == 1 {
		id, e := a.sendMediaTx(ctx, tx, telegramID, regular[0], caption, parseMode)
		if e != nil {
			return nil, e
		}
		ids = append(ids, id)
	} else if len(regular) > 1 {
		albumIDs, e := a.sendMediaGroupTx(ctx, tx, telegramID, regular, caption, parseMode)
		if e != nil {
			return nil, e
		}
		ids = append(ids, albumIDs...)
	}
	if !noteFirst {
		if e := sendNote(); e != nil {
			return nil, e
		}
	}
	return ids, nil
}

func (a *App) sendMediaGroupTx(ctx context.Context, tx pgx.Tx, telegramID int64, media []MessageMedia, caption, parseMode string) ([]int64, error) {
	items := make([]map[string]string, 0, len(media))
	files := []telegram.UploadFile{}
	for index, item := range media {
		source := item.FileID
		if source == "" {
			if len(item.Data) == 0 {
				return nil, errors.New("message media has no source data")
			}
			field := "media_" + strconv.Itoa(index)
			source = "attach://" + field
			files = append(files, telegram.UploadFile{FieldName: field, FileName: item.Name, Data: item.Data})
		}
		mediaItem := map[string]string{"type": item.Type, "media": source}
		if index == 0 && caption != "" {
			mediaItem["caption"] = caption
			if parseMode != "" {
				mediaItem["parse_mode"] = parseMode
			}
		}
		items = append(items, mediaItem)
	}
	var sent []telegram.Message
	if len(files) == 0 {
		if e := a.Telegram.Call(ctx, "sendMediaGroup", map[string]any{"chat_id": telegramID, "media": items}, &sent); e != nil {
			return nil, e
		}
	} else {
		encoded, e := json.Marshal(items)
		if e != nil {
			return nil, e
		}
		if e = a.Telegram.UploadMany(ctx, "sendMediaGroup", map[string]string{
			"chat_id": strconv.FormatInt(telegramID, 10),
			"media":   string(encoded),
		}, files, &sent); e != nil {
			return nil, e
		}
	}
	if len(sent) != len(media) {
		return nil, errors.New("telegram returned incomplete media group")
	}
	ids := make([]int64, 0, len(sent))
	for index, message := range sent {
		fileID := telegramMessageFileID(message)
		if message.ID == 0 || fileID == "" {
			return nil, errors.New("telegram returned incomplete media information")
		}
		ids = append(ids, message.ID)
		if _, e := tx.Exec(ctx, "UPDATE bot_message_media SET telegram_file_id=$2,updated_at=now() WHERE id=$1", media[index].ID, fileID); e != nil {
			return nil, e
		}
	}
	return ids, nil
}

func telegramMessageFileID(message telegram.Message) string {
	if message.Video != nil {
		return message.Video.FileID
	}
	if message.VideoNote != nil {
		return message.VideoNote.FileID
	}
	if len(message.Photo) != 0 {
		return message.Photo[len(message.Photo)-1].FileID
	}
	return ""
}

func (a *App) sendMediaTx(ctx context.Context, tx pgx.Tx, telegramID int64, media MessageMedia, caption, parseMode string) (int64, error) {
	method := "sendVideo"
	field := "video"
	if media.Type == "photo" {
		method = "sendPhoto"
		field = "photo"
	} else if media.Type == "video_note" {
		method = "sendVideoNote"
		field = "video_note"
	}
	var sent telegram.Message
	payload := map[string]any{"chat_id": telegramID, field: media.FileID}
	fields := map[string]string{"chat_id": strconv.FormatInt(telegramID, 10)}
	if caption != "" && media.Type != "video_note" {
		payload["caption"] = caption
		fields["caption"] = caption
		if parseMode != "" {
			payload["parse_mode"] = parseMode
			fields["parse_mode"] = parseMode
		}
	}
	if media.FileID != "" {
		e := a.Telegram.Call(ctx, method, payload, &sent)
		return sent.ID, e
	}
	if len(media.Data) == 0 {
		return 0, errors.New("message media has no source data")
	}
	e := a.Telegram.Upload(
		ctx,
		method,
		fields,
		field,
		media.Name,
		media.Data,
		&sent,
	)
	if e != nil {
		return 0, e
	}
	fileID := telegramMessageFileID(sent)
	if sent.ID == 0 || fileID == "" {
		return 0, errors.New("telegram returned incomplete media information")
	}
	if _, e = tx.Exec(ctx, "UPDATE bot_message_media SET telegram_file_id=$2,updated_at=now() WHERE id=$1", media.ID, fileID); e != nil {
		return 0, e
	}
	return sent.ID, nil
}

func formatPrice(amount, currency string) string {
	if strings.Contains(amount, ".") {
		amount = strings.TrimRight(strings.TrimRight(amount, "0"), ".")
	}
	if amount == "" {
		amount = "0"
	}
	return amount + " " + currency
}
func (a *App) join(ctx context.Context, telegramID, channelID int64) error {
	if channelID != a.Config.ChannelID {
		return nil
	}
	tx, e := a.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var uid int64
	e = tx.QueryRow(ctx, "SELECT id FROM users WHERE telegram_user_id=$1 FOR UPDATE", telegramID).Scan(&uid)
	if errors.Is(e, pgx.ErrNoRows) {
		return a.Telegram.Call(ctx, "declineChatJoinRequest", map[string]any{"chat_id": channelID, "user_id": telegramID}, nil)
	}
	if e != nil {
		return e
	}
	var active bool
	if e = tx.QueryRow(ctx, "SELECT status='active' FROM user_status WHERE id=$1", uid).Scan(&active); e != nil {
		return e
	}
	method := "declineChatJoinRequest"
	if active {
		method = "approveChatJoinRequest"
	}
	if e = a.Telegram.Call(ctx, method, map[string]any{"chat_id": channelID, "user_id": telegramID}, nil); e != nil {
		if !active {
			return e
		}
		var member struct {
			Status string `json:"status"`
		}
		if err := a.Telegram.Call(ctx, "getChatMember", map[string]any{"chat_id": channelID, "user_id": telegramID}, &member); err != nil || (member.Status != "member" && member.Status != "administrator" && member.Status != "creator") {
			return e
		}
	}
	if active {
		if _, e = tx.Exec(ctx, "INSERT INTO channel_access(user_id,state) VALUES($1,'member') ON CONFLICT(user_id) DO UPDATE SET state='member',updated_at=now()", uid); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
