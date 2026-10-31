package lava

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSubscriptionRequestAndCancellationContract(t *testing.T) {
	t.Parallel()
	var created, cancelled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "test-key" {
			t.Error("missing authentication")
		}
		switch r.Method {
		case "POST":
			if r.URL.Path != "/api/v3/invoice" {
				t.Error("incorrect API version")
			}
			var in map[string]any
			if e := json.NewDecoder(r.Body).Decode(&in); e != nil {
				t.Error(e)
			}
			if in["periodicity"] != "MONTHLY" || in["email"] != "buyer@example.com" || in["paymentProvider"] != "PAYPAL" {
				t.Errorf("bad subscription request: %#v", in)
			}
			created = true
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"id":"contract","paymentUrl":"https://checkout.example/pay"}`))
		case "DELETE":
			if r.URL.Path != "/api/v1/subscriptions" || r.URL.Query().Get("contractId") != "contract" || r.URL.Query().Get("email") != "buyer+tag@example.com" {
				t.Error("incorrect cancellation query")
			}
			cancelled = true
			w.WriteHeader(204)
		}
	}))
	defer srv.Close()
	c := New("test-key")
	c.BaseURL = srv.URL
	if _, e := c.Create(context.Background(), CreateRequest{Email: "buyer@example.com", OfferID: "offer", Currency: "USD", Periodicity: "MONTHLY", Provider: "PAYPAL"}); e != nil {
		t.Fatal(e)
	}
	if e := c.Cancel(context.Background(), "contract", "buyer+tag@example.com"); e != nil {
		t.Fatal(e)
	}
	if !created || !cancelled {
		t.Fatal("missing provider requests")
	}
}
func TestRejectsOneTimeFlow(t *testing.T) {
	t.Parallel()
	if _, e := New("test").Create(context.Background(), CreateRequest{Periodicity: "ONE_TIME"}); e == nil {
		t.Fatal("one-time checkout accepted")
	}
}

func TestReturnsTypedHTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"buyer test@example.com already has this offer","details":{"field":"email"}}`))
	}))
	defer srv.Close()
	c := New("test-key")
	c.BaseURL = srv.URL
	_, err := c.Create(context.Background(), CreateRequest{Email: "buyer@example.com", OfferID: "offer", Currency: "RUB", Periodicity: "MONTHLY"})
	var providerErr *HTTPError
	if !errors.As(err, &providerErr) || providerErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected typed 400 error, got %v", err)
	}
	if providerErr.Reason != `buyer <email> already has this offer {"field":"email"}` {
		t.Fatalf("unsafe or missing reason: %q", providerErr.Reason)
	}
}
