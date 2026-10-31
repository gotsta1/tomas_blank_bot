package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	BaseURL, Token string
	HTTP           *http.Client
}

func New(token string) *Client {
	return &Client{BaseURL: "https://api.telegram.org", Token: token, HTTP: &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}

type APIError struct {
	Code        int
	Description string
	RetryAfter  int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram status %d: %s", e.Code, e.Description)
}
func (c *Client) Call(ctx context.Context, method string, in, out any) error {
	if c.Token == "" {
		return errors.New("telegram is not configured")
	}
	data, e := json.Marshal(in)
	if e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/bot"+c.Token+"/"+method, bytes.NewReader(data))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, method, out)
}

func (c *Client) Upload(ctx context.Context, method string, fields map[string]string, fieldName, fileName string, data []byte, out any) error {
	if c.Token == "" {
		return errors.New("telegram is not configured")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if e := writer.WriteField(key, value); e != nil {
			return e
		}
	}
	part, e := writer.CreateFormFile(fieldName, fileName)
	if e != nil {
		return e
	}
	if _, e = part.Write(data); e != nil {
		return e
	}
	if e = writer.Close(); e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/bot"+c.Token+"/"+method, &body)
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return c.do(req, method, out)
}

type UploadFile struct {
	FieldName string
	FileName  string
	Data      []byte
}

func (c *Client) UploadMany(ctx context.Context, method string, fields map[string]string, files []UploadFile, out any) error {
	if c.Token == "" {
		return errors.New("telegram is not configured")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if e := writer.WriteField(key, value); e != nil {
			return e
		}
	}
	for _, file := range files {
		part, e := writer.CreateFormFile(file.FieldName, file.FileName)
		if e != nil {
			return e
		}
		if _, e = part.Write(file.Data); e != nil {
			return e
		}
	}
	if e := writer.Close(); e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/bot"+c.Token+"/"+method, &body)
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return c.do(req, method, out)
}

func (c *Client) do(req *http.Request, method string, out any) error {
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return errors.New("telegram transport failed")
	}
	defer resp.Body.Close()
	var result struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Code        int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result); e != nil {
		return errors.New("invalid telegram response")
	}
	if !result.OK {
		if method == "editMessageText" && strings.Contains(result.Description, "message is not modified") {
			return nil
		}
		return &APIError{result.Code, result.Description, result.Parameters.RetryAfter}
	}
	if out != nil {
		return json.Unmarshal(result.Result, out)
	}
	return nil
}

type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}
type Message struct {
	ID          int64        `json:"message_id"`
	From        User         `json:"from"`
	Chat        Chat         `json:"chat"`
	Text        string       `json:"text"`
	Video       *MediaFile   `json:"video"`
	VideoNote   *MediaFile   `json:"video_note"`
	Photo       []MediaFile  `json:"photo"`
	RichMessage *RichMessage `json:"rich_message"`
}
type RichMessage struct {
	Blocks []RichBlock `json:"blocks"`
}
type RichBlock struct {
	Type   string      `json:"type"`
	Blocks []RichBlock `json:"blocks"`
	Photo  []MediaFile `json:"photo"`
	Video  *MediaFile  `json:"video"`
}

func (m Message) RichMediaFileIDs() []string {
	if m.RichMessage == nil {
		return nil
	}
	ids := []string{}
	var walk func([]RichBlock)
	walk = func(blocks []RichBlock) {
		for _, block := range blocks {
			switch block.Type {
			case "photo":
				if len(block.Photo) != 0 {
					ids = append(ids, block.Photo[len(block.Photo)-1].FileID)
				}
			case "video":
				if block.Video != nil {
					ids = append(ids, block.Video.FileID)
				}
			default:
				walk(block.Blocks)
			}
		}
	}
	walk(m.RichMessage.Blocks)
	return ids
}

type MediaFile struct {
	FileID string `json:"file_id"`
}
type Update struct {
	ID       int64    `json:"update_id"`
	Message  *Message `json:"message"`
	Callback *struct {
		ID      string   `json:"id"`
		From    User     `json:"from"`
		Message *Message `json:"message"`
		Data    string   `json:"data"`
	} `json:"callback_query"`
	Join *struct {
		Chat Chat `json:"chat"`
		From User `json:"from"`
	} `json:"chat_join_request"`
}
