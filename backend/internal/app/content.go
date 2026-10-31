package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

type Button struct {
	Label   string `json:"label"`
	Type    string `json:"type"`
	Action  string `json:"action"`
	Row     int    `json:"row"`
	Enabled bool   `json:"enabled"`
}
type Content struct {
	ID        int64          `json:"id"`
	Key       string         `json:"key"`
	Name      string         `json:"name"`
	Text      string         `json:"text"`
	ParseMode string         `json:"parse_mode"`
	Buttons   []Button       `json:"buttons"`
	Version   int            `json:"version"`
	Media     []MessageMedia `json:"media"`
}

type MessageMedia struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Position  string `json:"position"`
	SortOrder int    `json:"sort_order"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	MIME      string `json:"-"`
	FileID    string `json:"-"`
	Data      []byte `json:"-"`
}

var actions = map[string]bool{
	"club:join":                   true,
	"club:about":                  true,
	"plan:monthly":                true,
	"plan:90d":                    true,
	"plan:180d":                   true,
	"plan:year":                   true,
	"pay:region:ru":               true,
	"pay:region:foreign":          true,
	"pay:ru:sbp":                  true,
	"pay:ru:card":                 true,
	"pay:foreign:paypal":          true,
	"pay:foreign:card":            true,
	"pay:foreign:applepay":        true,
	"pay:check":                   true,
	"nav:start":                   true,
	"nav:plan":                    true,
	"nav:region":                  true,
	"subscription:status":         true,
	"subscription:cancel":         true,
	"subscription:cancel:confirm": true,
}
var variables = map[string]bool{"plan_name": true, "price": true, "period": true, "expires_date": true, "days_left": true, "auto_renew": true, "payment_url": true, "invite_url": true}
var variablePattern = regexp.MustCompile(`\{\{([a-z_]+)\}\}`)

func validateTemplate(s string) error {
	for _, match := range variablePattern.FindAllStringSubmatch(s, -1) {
		if !variables[match[1]] {
			return errors.New("unknown template variable")
		}
	}
	rest := variablePattern.ReplaceAllString(s, "")
	if strings.Contains(rest, "{{") || strings.Contains(rest, "}}") {
		return errors.New("invalid template syntax")
	}
	return nil
}
func validateContent(c Content) error {
	if len(c.Name) == 0 || len(c.Name) > 200 || utf8.RuneCountInString(c.Text) > 3500 || len(c.Text) == 0 {
		return errors.New("name or text length is invalid")
	}
	if c.ParseMode != "" && c.ParseMode != "HTML" {
		return errors.New("parse_mode must be empty or HTML")
	}
	if e := validateTemplate(c.Text); e != nil {
		return e
	}
	if len(c.Buttons) > 30 {
		return errors.New("too many buttons")
	}
	for _, b := range c.Buttons {
		if b.Label == "" || utf8.RuneCountInString(b.Label) > 64 || b.Row < 0 || b.Row > 20 {
			return errors.New("invalid button")
		}
		if e := validateTemplate(b.Label); e != nil {
			return e
		}
		switch b.Type {
		case "callback":
			if !actions[b.Action] {
				return errors.New("unknown action")
			}
		case "url":
			if b.Action != "{{payment_url}}" && b.Action != "{{invite_url}}" {
				u, e := url.Parse(b.Action)
				if e != nil || u.Scheme != "https" || u.Host == "" || strings.Contains(b.Action, "{{") {
					return errors.New("button URL must be HTTPS or a supported URL variable")
				}
			}
		default:
			return errors.New("unknown button type")
		}
	}
	return nil
}
func jsonNumber(i int64) string { return strconv.FormatInt(i, 10) }
func loadContent(ctx context.Context, tx pgx.Tx, key string) (Content, error) {
	var c Content
	var raw []byte
	e := tx.QueryRow(ctx, `
		SELECT m.id,m.key,m.name,m.text,m.parse_mode,m.buttons,m.version
		FROM bot_messages m
		WHERE m.key=$1`, key).Scan(
		&c.ID, &c.Key, &c.Name, &c.Text, &c.ParseMode, &raw, &c.Version,
	)
	if e != nil {
		return c, e
	}
	if e = json.Unmarshal(raw, &c.Buttons); e != nil {
		return c, e
	}
	rows, e := tx.Query(ctx, `
		SELECT id,media_type,media_position,sort_order,file_name,size_bytes,mime_type,telegram_file_id,
		       CASE WHEN telegram_file_id='' THEN data ELSE NULL END
		FROM bot_message_media
		WHERE message_id=$1
		ORDER BY media_position DESC,sort_order,id`, c.ID)
	if e != nil {
		return c, e
	}
	defer rows.Close()
	c.Media = []MessageMedia{}
	for rows.Next() {
		var media MessageMedia
		if e = rows.Scan(&media.ID, &media.Type, &media.Position, &media.SortOrder, &media.Name, &media.Size, &media.MIME, &media.FileID, &media.Data); e != nil {
			return c, e
		}
		c.Media = append(c.Media, media)
	}
	e = rows.Err()
	return c, e
}
func render(s string, vars map[string]string, escape bool) string {
	return variablePattern.ReplaceAllStringFunc(s, func(k string) string {
		v := vars[k[2:len(k)-2]]
		if escape {
			return html.EscapeString(v)
		}
		return v
	})
}
func renderKeyboard(buttons []Button, vars map[string]string, autoRenew bool) [][]map[string]string {
	sorted := append([]Button(nil), buttons...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Row < sorted[j].Row })
	rows := [][]map[string]string{}
	last := -1
	for _, b := range sorted {
		if !b.Enabled || b.Action == "subscription:cancel" && !autoRenew {
			continue
		}
		action := render(b.Action, vars, false)
		if action == "" {
			continue
		}
		if b.Row != last {
			rows = append(rows, []map[string]string{})
			last = b.Row
		}
		key := "callback_data"
		if b.Type == "url" {
			key = "url"
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], map[string]string{"text": render(b.Label, vars, false), key: action})
	}
	return rows
}
func marshal(v any) []byte { var b bytes.Buffer; _ = json.NewEncoder(&b).Encode(v); return b.Bytes() }
