package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/gotsta1/tomas_blank_bot/backend/internal/telegram"
	"github.com/jackc/pgx/v5"
)

// sendRichMediaTx sends the collage, text and inline keyboard as one Telegram
// message. Video notes remain separate because rich messages have no video
// note block.
func (a *App) sendRichMediaTx(ctx context.Context, tx pgx.Tx, chatID int64, media []MessageMedia, text, parseMode string, keyboard map[string]any) (int64, error) {
	if len(media) == 0 {
		return 0, errors.New("rich message requires media")
	}
	var body strings.Builder
	if len(media) > 1 {
		body.WriteString("<tg-collage>")
	}
	items := make([]map[string]any, 0, len(media))
	files := make([]telegram.UploadFile, 0, len(media))
	for index, item := range media {
		if item.Type != "photo" && item.Type != "video" {
			return 0, errors.New("unsupported rich message media type")
		}
		id := "media_" + strconv.Itoa(index)
		source := item.FileID
		if source == "" {
			if len(item.Data) == 0 {
				return 0, errors.New("message media has no source data")
			}
			source = "attach://" + id
			files = append(files, telegram.UploadFile{FieldName: id, FileName: item.Name, Data: item.Data})
		}
		items = append(items, map[string]any{"id": id, "media": map[string]string{"type": item.Type, "media": source}})
		if item.Type == "photo" {
			fmt.Fprintf(&body, `<img src="tg://photo?id=%s"/>`, id)
		} else {
			fmt.Fprintf(&body, `<video src="tg://video?id=%s"></video>`, id)
		}
	}
	if len(media) > 1 {
		body.WriteString("</tg-collage>")
	}
	if parseMode != "HTML" {
		text = html.EscapeString(text)
	}
	body.WriteString("<p>")
	body.WriteString(strings.ReplaceAll(text, "\n", "<br/>"))
	body.WriteString("</p>")
	rich := map[string]any{"html": body.String(), "media": items}
	var sent telegram.Message
	if len(files) == 0 {
		if err := a.Telegram.Call(ctx, "sendRichMessage", map[string]any{"chat_id": chatID, "rich_message": rich, "reply_markup": keyboard}, &sent); err != nil {
			return 0, err
		}
	} else {
		richJSON, err := json.Marshal(rich)
		if err != nil {
			return 0, err
		}
		keyboardJSON, err := json.Marshal(keyboard)
		if err != nil {
			return 0, err
		}
		if err = a.Telegram.UploadMany(ctx, "sendRichMessage", map[string]string{
			"chat_id":      strconv.FormatInt(chatID, 10),
			"rich_message": string(richJSON),
			"reply_markup": string(keyboardJSON),
		}, files, &sent); err != nil {
			return 0, err
		}
	}
	if sent.ID == 0 {
		return 0, errors.New("telegram returned an empty rich message id")
	}
	// A rich message returns the media in its blocks. Cache new file IDs when
	// provided so later sends can reuse Telegram's copies without reuploading.
	fileIDs := sent.RichMediaFileIDs()
	if len(fileIDs) == len(media) {
		for index, item := range media {
			if item.FileID != "" || fileIDs[index] == "" {
				continue
			}
			if _, err := tx.Exec(ctx, "UPDATE bot_message_media SET telegram_file_id=$2,updated_at=now() WHERE id=$1", item.ID, fileIDs[index]); err != nil {
				return 0, err
			}
		}
	}
	return sent.ID, nil
}
