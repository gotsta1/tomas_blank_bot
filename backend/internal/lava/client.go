// Package lava implements the public API contract captured in docs/lava-openapi.yaml.
package lava

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Client struct {
	BaseURL, Key string
	HTTP         *http.Client
}

// HTTPError means Lava returned a definite non-successful HTTP response.
// Callers can distinguish it from a transport failure, where it may be
// unknown whether the provider created an invoice.
type HTTPError struct {
	Method     string
	StatusCode int
	Reason     string
}

func (e *HTTPError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("lava %s status %d: %s", e.Method, e.StatusCode, e.Reason)
	}
	return fmt.Sprintf("lava %s status %d", e.Method, e.StatusCode)
}

var emailPattern = regexp.MustCompile(`[[:alnum:]._%+\-]+@[[:alnum:].\-]+\.[[:alpha:]]{2,}`)

func safeErrorReason(r io.Reader) string {
	var body struct {
		Error   string          `json:"error"`
		Message string          `json:"message"`
		Details json.RawMessage `json:"details"`
	}
	if err := json.NewDecoder(io.LimitReader(r, 16<<10)).Decode(&body); err != nil {
		return ""
	}
	parts := []string{body.Error, body.Message}
	if len(body.Details) > 0 && string(body.Details) != "null" && string(body.Details) != "{}" {
		parts = append(parts, string(body.Details))
	}
	reason := strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
	reason = emailPattern.ReplaceAllString(reason, "<email>")
	if len(reason) > 500 {
		reason = reason[:500]
	}
	return reason
}

func New(key string) *Client {
	return &Client{BaseURL: "https://gate.lava.top", Key: key, HTTP: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}

type CreateRequest struct {
	Email       string `json:"email"`
	OfferID     string `json:"offerId"`
	Currency    string `json:"currency"`
	Periodicity string `json:"periodicity"`
	Provider    string `json:"paymentProvider,omitempty"`
}
type Checkout struct {
	ID          string      `json:"id"`
	PaymentURL  string      `json:"paymentUrl"`
	Status      string      `json:"status"`
	AmountTotal AmountTotal `json:"amountTotal"`
}
type AmountTotal struct {
	Amount   json.Number `json:"amount"`
	Currency string      `json:"currency"`
}
type Receipt struct {
	Amount   json.Number `json:"amount"`
	Currency string      `json:"currency"`
}
type Payment struct {
	ID       string      `json:"id"`
	DateTime time.Time   `json:"datetime"`
	Status   string      `json:"status"`
	Amount   json.Number `json:"amount"`
	Currency string      `json:"currency"`
}
type Subscription struct {
	ID                 string     `json:"id"`
	DateTime           time.Time  `json:"datetime"`
	Status             string     `json:"status"`
	SubscriptionStatus string     `json:"subscriptionStatus"`
	Periodicity        string     `json:"periodicity"`
	Receipt            Receipt    `json:"receipt"`
	ExpiredAt          *time.Time `json:"expiredAt"`
	CancelledAt        *time.Time `json:"cancelledAt"`
	TerminatedAt       *time.Time `json:"terminatedAt"`
	RecurrentPayments  []Payment  `json:"recurrentPayments"`
}
type Invoice struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	Status        string `json:"status"`
	ParentInvoice *struct {
		ID string `json:"id"`
	} `json:"parentInvoice"`
}
type Event struct {
	Type         string     `json:"eventType"`
	ContractID   string     `json:"contractId"`
	ParentID     string     `json:"parentContractId"`
	Timestamp    time.Time  `json:"timestamp"`
	CancelledAt  *time.Time `json:"cancelledAt"`
	WillExpireAt *time.Time `json:"willExpireAt"`
	EventID      string     `json:"event_id"`
	EnvelopeType string     `json:"event_type"`
}

func (c *Client) Create(ctx context.Context, in CreateRequest) (Checkout, error) {
	var out Checkout
	if in.Periodicity == "" || in.Periodicity == "ONE_TIME" {
		return out, errors.New("recurring periodicity required")
	}
	err := c.call(ctx, http.MethodPost, "/api/v3/invoice", in, &out)
	if err == nil {
		u, e := url.Parse(out.PaymentURL)
		if out.ID == "" || e != nil || u.Scheme != "https" || u.Host == "" {
			err = errors.New("invalid checkout response")
		}
	}
	return out, err
}
func (c *Client) Subscription(ctx context.Context, id string) (Subscription, error) {
	var out Subscription
	err := c.call(ctx, http.MethodGet, "/api/v1/subscriptions/"+url.PathEscape(id), nil, &out)
	return out, err
}
func (c *Client) Invoice(ctx context.Context, id string) (Invoice, error) {
	var out Invoice
	err := c.call(ctx, http.MethodGet, "/api/v2/invoices/"+url.PathEscape(id), nil, &out)
	return out, err
}
func (c *Client) Cancel(ctx context.Context, id, email string) error {
	return c.call(ctx, http.MethodDelete, "/api/v1/subscriptions?"+url.Values{"contractId": {id}, "email": {email}}.Encode(), nil, nil)
}
func (c *Client) call(ctx context.Context, method, path string, in, out any) error {
	if c.Key == "" {
		return errors.New("lava integration is not configured")
	}
	var body io.Reader
	if in != nil {
		b, e := json.Marshal(in)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, body)
	if e != nil {
		return e
	}
	req.Header.Set("X-Api-Key", c.Key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return fmt.Errorf("lava %s transport failed", method)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{Method: method, StatusCode: resp.StatusCode, Reason: safeErrorReason(resp.Body)}
	}
	if out != nil {
		if e := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out); e != nil {
			return fmt.Errorf("decode lava response: %w", e)
		}
	}
	return nil
}
