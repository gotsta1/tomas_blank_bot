//go:build integration

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gotsta1/tomas_blank_bot/backend/internal/config"
	"github.com/gotsta1/tomas_blank_bot/backend/internal/lava"
	"github.com/gotsta1/tomas_blank_bot/backend/internal/telegram"
	"github.com/gotsta1/tomas_blank_bot/backend/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func testApp(t *testing.T) *App {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	base, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	var database string
	if e = base.QueryRow(ctx, "SELECT current_database()").Scan(&database); e != nil {
		t.Fatal(e)
	}
	if !strings.HasSuffix(database, "_test") {
		base.Close()
		t.Fatal("test database name must end in _test")
	}
	schema := fmt.Sprintf("test_%d", time.Now().UnixNano())
	if _, e = base.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	pc, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	pc.ConnConfig.RuntimeParams["search_path"] = schema
	pool, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { pool.Close(); _, _ = base.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); base.Close() })
	if e = migrations.Run(ctx, pool); e != nil {
		t.Fatal(e)
	}
	if e = migrations.Run(ctx, pool); e != nil {
		t.Fatal("migration replay", e)
	}
	c := config.Config{Location: time.UTC, BaseURL: "https://club.example", LavaKey: "test", LavaSecret: strings.Repeat("x", 32), TelegramSecret: strings.Repeat("y", 32), OfferID: "offer", ForeignCurrency: "USD", ChannelID: -100123, PlanName: "Club", PlanPrice: "990 RUB", PlanPeriod: "Monthly"}
	return New(pool, c, slog.New(slog.NewTextHandler(io.Discard, nil)))
}
func execTest(t *testing.T, a *App, sql string, args ...any) {
	t.Helper()
	if _, e := a.DB.Exec(context.Background(), sql, args...); e != nil {
		t.Fatal(e)
	}
}
func seedUser(t *testing.T, a *App) int64 {
	t.Helper()
	var uid int64
	if e := a.DB.QueryRow(context.Background(), "INSERT INTO users(telegram_user_id,email,selected_periodicity,first_started_at) VALUES(12345,'buyer@example.com','MONTHLY',now()) RETURNING id").Scan(&uid); e != nil {
		t.Fatal(e)
	}
	return uid
}
func seedSubscription(t *testing.T, a *App, uid int64) {
	t.Helper()
	execTest(t, a, "INSERT INTO subscriptions(user_id,contract_id,offer_id,email,periodicity) VALUES($1,'root','offer','buyer@example.com','MONTHLY')", uid)
}
func snapshotServer(t *testing.T, a *App, initial lava.Subscription) func(lava.Subscription) {
	t.Helper()
	var mu sync.Mutex
	snapshot := initial
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "test" {
			t.Error("missing API auth")
		}
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "DELETE" {
			if r.URL.Query().Get("contractId") != "root" || r.URL.Query().Get("email") != "buyer@example.com" {
				t.Error("invalid cancellation")
			}
			w.WriteHeader(204)
			return
		}
		_ = json.NewEncoder(w).Encode(snapshot)
	}))
	t.Cleanup(srv.Close)
	a.Lava.BaseURL = srv.URL
	return func(s lava.Subscription) { mu.Lock(); snapshot = s; mu.Unlock() }
}
func paidSnapshot() lava.Subscription {
	expires := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	return lava.Subscription{ID: "root", Status: "COMPLETED", DateTime: time.Now().UTC().Add(-time.Hour).Truncate(time.Second), SubscriptionStatus: "ACTIVE", Receipt: lava.Receipt{Amount: "990.25", Currency: "RUB"}, ExpiredAt: &expires}
}
func state(t *testing.T, a *App, uid int64) (string, bool, time.Time) {
	t.Helper()
	var status string
	var renew bool
	var expiry *time.Time
	if e := a.DB.QueryRow(context.Background(), "SELECT status,auto_renew,expires_at FROM user_status WHERE id=$1", uid).Scan(&status, &renew, &expiry); e != nil {
		t.Fatal(e)
	}
	if expiry == nil {
		return status, renew, time.Time{}
	}
	return status, renew, *expiry
}
func TestPaymentReplayRenewalAndCancellation(t *testing.T) {
	a := testApp(t)
	uid := seedUser(t, a)
	seedSubscription(t, a, uid)
	s := paidSnapshot()
	set := snapshotServer(t, a, s)
	errs := make(chan error, 6)
	for range 6 {
		go func() { errs <- a.SyncSubscription(context.Background(), "root") }()
	}
	for range 6 {
		if e := <-errs; e != nil {
			t.Fatal(e)
		}
	}
	var count int
	if e := a.DB.QueryRow(context.Background(), "SELECT count(*) FROM payments").Scan(&count); e != nil || count != 1 {
		t.Fatalf("duplicate payment: count=%d err=%v", count, e)
	}
	status, renew, expiry := state(t, a, uid)
	if status != "active" || !renew || !expiry.Equal(*s.ExpiredAt) {
		t.Fatalf("invalid access: %s %v %v", status, renew, expiry)
	}
	next := s.ExpiredAt.AddDate(0, 1, 0)
	s.ExpiredAt = &next
	s.RecurrentPayments = []lava.Payment{{ID: "renewal", Status: "COMPLETED", DateTime: time.Now().UTC(), Amount: "990.25", Currency: "RUB"}}
	set(s)
	if e := a.processLava(context.Background(), lava.Event{Type: "subscription.recurring.payment.success", ContractID: "renewal", ParentID: "root"}); e != nil {
		t.Fatal(e)
	}
	if e := a.cancelSubscription(context.Background(), uid); e != nil {
		t.Fatal(e)
	}
	// An old success webhook must not undo cancellation or shorten access.
	stale := paidSnapshot()
	set(stale)
	if e := a.processLava(context.Background(), lava.Event{Type: "payment.success", ContractID: "root"}); e != nil {
		t.Fatal(e)
	}
	status, renew, expiry = state(t, a, uid)
	if status != "active" || renew || !expiry.Equal(next) {
		t.Fatalf("cancellation lost paid access: %s %v %v", status, renew, expiry)
	}
}

func TestCancelSubscriptionNormalizesEmailCase(t *testing.T) {
	a := testApp(t)
	uid := seedUser(t, a)
	seedSubscription(t, a, uid)
	s := paidSnapshot()
	snapshotServer(t, a, s)
	if err := a.SyncSubscription(context.Background(), "root"); err != nil {
		t.Fatal(err)
	}
	execTest(t, a, "UPDATE subscriptions SET email='Buyer@Example.com' WHERE contract_id='root'")
	if err := a.cancelSubscription(context.Background(), uid); err != nil {
		t.Fatal(err)
	}
	_, renew, _ := state(t, a, uid)
	if renew {
		t.Fatal("auto-renewal remained enabled")
	}
}
func TestCancelSubscriptionFailureShowsRetry(t *testing.T) {
	a := testApp(t)
	uid := seedUser(t, a)
	seedSubscription(t, a, uid)
	snapshotServer(t, a, paidSnapshot())
	if err := a.SyncSubscription(context.Background(), "root"); err != nil {
		t.Fatal(err)
	}
	// Lava rejects cancellation while its status endpoint still reports ACTIVE.
	lavaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			respond(w, http.StatusBadRequest, map[string]any{"message": "Invalid email for subscription cancelling"})
			return
		}
		_ = json.NewEncoder(w).Encode(paidSnapshot())
	}))
	t.Cleanup(lavaServer.Close)
	a.Lava.BaseURL = lavaServer.URL
	var notification string
	telegramServer(t, a, func(method string, body map[string]any) (int, any) {
		if method == "editMessageText" {
			notification, _ = body["text"].(string)
		}
		return 200, map[string]any{"ok": true, "result": true}
	})
	var update telegram.Update
	if err := json.Unmarshal([]byte(`{"update_id":1,"callback_query":{"id":"cancel","from":{"id":12345},"message":{"message_id":1,"chat":{"id":12345,"type":"private"}},"data":"subscription:cancel:confirm"}}`), &update); err != nil {
		t.Fatal(err)
	}
	if err := a.processTelegram(context.Background(), update); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(notification, "Попробуй ещё раз") {
		t.Fatalf("missing cancellation failure message: %q", notification)
	}
	_, renew, _ := state(t, a, uid)
	if !renew {
		t.Fatal("rejected cancellation disabled auto-renewal")
	}
}
func TestPendingNeverGrantsAccess(t *testing.T) {
	a := testApp(t)
	uid := seedUser(t, a)
	seedSubscription(t, a, uid)
	snapshotServer(t, a, lava.Subscription{ID: "root", Status: "IN_PROGRESS"})
	if e := a.SyncSubscription(context.Background(), "root"); e != ErrPending {
		t.Fatalf("expected pending, got %v", e)
	}
	status, _, _ := state(t, a, uid)
	if status != "new" {
		t.Fatalf("access granted: %s", status)
	}
}

func TestFreeActiveSubscriptionDerivesMissingExpiry(t *testing.T) {
	a := testApp(t)
	uid := seedUser(t, a)
	seedSubscription(t, a, uid)
	started := time.Date(2026, time.September, 20, 20, 47, 14, 0, time.UTC)
	snapshotServer(t, a, lava.Subscription{
		ID:                 "root",
		Status:             "COMPLETED",
		SubscriptionStatus: "ACTIVE",
		Periodicity:        "period_year",
		DateTime:           started,
		Receipt:            lava.Receipt{Amount: "0.0", Currency: "USD"},
	})
	if err := a.SyncSubscription(context.Background(), "root"); err != nil {
		t.Fatal(err)
	}
	status, renew, expiry := state(t, a, uid)
	if status != "active" || !renew || !expiry.Equal(started.AddDate(1, 0, 0)) {
		t.Fatalf("invalid free access: status=%s renew=%v expiry=%v", status, renew, expiry)
	}
}

func TestFailedRenewalKeepsPaidAccess(t *testing.T) {
	a := testApp(t)
	uid := seedUser(t, a)
	seedSubscription(t, a, uid)
	s := paidSnapshot()
	s.SubscriptionStatus = "FAILED"
	snapshotServer(t, a, s)
	if e := a.processLava(context.Background(), lava.Event{Type: "subscription.recurring.payment.failed", ContractID: "renewal", ParentID: "root"}); e != nil {
		t.Fatal(e)
	}
	status, renew, _ := state(t, a, uid)
	if status != "active" || !renew {
		t.Fatalf("failed attempt revoked paid access or recurring state: %s %v", status, renew)
	}
}
func TestConcurrentCheckoutCreatesOneInvoice(t *testing.T) {
	a := testApp(t)
	uid := seedUser(t, a)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var in lava.CreateRequest
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Periodicity != "MONTHLY" || in.Email != "buyer@example.com" {
			t.Error("invalid subscription request")
		}
		respond(w, 201, lava.Checkout{ID: "root", PaymentURL: "https://pay.example/checkout"})
	}))
	defer srv.Close()
	a.Lava.BaseURL = srv.URL
	errs := make(chan error, 4)
	for range 4 {
		go func() { _, e := a.checkout(context.Background(), uid, "pay:ru:card"); errs <- e }()
	}
	for range 4 {
		if e := <-errs; e != nil {
			t.Fatal(e)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("created %d invoices", calls.Load())
	}
}
func TestCheckoutTransportFailureIsNotRetried(t *testing.T) {
	a := testApp(t)
	uid := seedUser(t, a)
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "uncertain", 500) }))
	defer srv.Close()
	a.Lava.BaseURL = srv.URL
	for range 2 {
		key, e := a.checkout(context.Background(), uid, "pay:ru:card")
		if e != nil || key != "payment_uncertain" {
			t.Fatalf("%s %v", key, e)
		}
	}
	if calls != 1 {
		t.Fatalf("unsafe retry: %d", calls)
	}
}

func TestCheckoutRejectionAllowsRetry(t *testing.T) {
	a := testApp(t)
	uid := seedUser(t, a)
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "rejected", http.StatusBadRequest)
			return
		}
		respond(w, http.StatusCreated, lava.Checkout{ID: "root", PaymentURL: "https://pay.example/checkout"})
	}))
	defer srv.Close()
	a.Lava.BaseURL = srv.URL
	key, err := a.checkout(context.Background(), uid, "pay:ru:card")
	if err != nil || key != "payment_failed" {
		t.Fatalf("first checkout: key=%s err=%v", key, err)
	}
	key, err = a.checkout(context.Background(), uid, "pay:ru:card")
	if err != nil || key != "payment_created" {
		t.Fatalf("retry checkout: key=%s err=%v", key, err)
	}
	if calls != 2 {
		t.Fatalf("expected one safe retry, got %d calls", calls)
	}
}

func TestOldCheckoutReusesPayableInvoiceAndReplacesFailedOne(t *testing.T) {
	a := testApp(t)
	uid := seedUser(t, a)
	var creates int
	status := "NEW"
	lookupUnavailable := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if lookupUnavailable {
				http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
				return
			}
			respond(w, http.StatusOK, lava.Invoice{ID: "contract-1", Status: status})
			return
		}
		creates++
		respond(w, http.StatusCreated, lava.Checkout{ID: fmt.Sprintf("contract-%d", creates), PaymentURL: fmt.Sprintf("https://pay.example/%d", creates)})
	}))
	defer srv.Close()
	a.Lava.BaseURL = srv.URL
	if key, err := a.checkout(context.Background(), uid, "pay:ru:card"); err != nil || key != "payment_created" {
		t.Fatalf("initial checkout: key=%q err=%v", key, err)
	}
	execTest(t, a, "UPDATE checkout_attempts SET created_at=now()-interval '1 day' WHERE user_id=$1", uid)
	if key, err := a.checkout(context.Background(), uid, "pay:ru:card"); err != nil || key != "payment_created" || creates != 1 {
		t.Fatalf("payable checkout was replaced: key=%q err=%v creates=%d", key, err, creates)
	}
	lookupUnavailable = true
	if key, err := a.checkout(context.Background(), uid, "pay:ru:card"); err != nil || key != "payment_created" || creates != 1 {
		t.Fatalf("provider outage replaced an existing checkout: key=%q err=%v creates=%d", key, err, creates)
	}
	lookupUnavailable = false
	status = "FAILED"
	if key, err := a.checkout(context.Background(), uid, "pay:ru:card"); err != nil || key != "payment_created" || creates != 2 {
		t.Fatalf("failed checkout was not replaced: key=%q err=%v creates=%d", key, err, creates)
	}
	var oldState, newState, newURL string
	if err := a.DB.QueryRow(context.Background(), "SELECT state FROM checkout_attempts WHERE external_id='contract-1'").Scan(&oldState); err != nil {
		t.Fatal(err)
	}
	if err := a.DB.QueryRow(context.Background(), "SELECT state,payment_url FROM checkout_attempts WHERE external_id='contract-2'").Scan(&newState, &newURL); err != nil {
		t.Fatal(err)
	}
	if oldState != "failed" || newState != "ready" || newURL != "https://pay.example/2" {
		t.Fatalf("old=%q new=%q url=%q", oldState, newState, newURL)
	}
}

func TestCheckoutRejectedEmailRequestsReplacement(t *testing.T) {
	for _, reason := range []string{"Incorrect email to purchase", "Provided string value does not match email format"} {
		t.Run(reason, func(t *testing.T) {
			a := testApp(t)
			uid := seedUser(t, a)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": reason})
			}))
			defer srv.Close()
			a.Lava.BaseURL = srv.URL

			key, err := a.checkout(context.Background(), uid, "pay:ru:card")
			if err != nil || key != "email_rejected" {
				t.Fatalf("checkout: key=%q err=%v", key, err)
			}
			var email *string
			var pendingFlow, state string
			if err = a.DB.QueryRow(context.Background(), "SELECT email,pending_flow FROM users WHERE id=$1", uid).Scan(&email, &pendingFlow); err != nil {
				t.Fatal(err)
			}
			if err = a.DB.QueryRow(context.Background(), "SELECT state FROM checkout_attempts WHERE user_id=$1 ORDER BY id DESC LIMIT 1", uid).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if email != nil || pendingFlow != "pay:ru:card" || state != "failed" {
				t.Fatalf("email=%v pending_flow=%q state=%q", email, pendingFlow, state)
			}
		})
	}
}

func TestCheckoutUsesSelectedPeriodicity(t *testing.T) {
	a := testApp(t)
	uid := seedUser(t, a)
	execTest(t, a, "UPDATE users SET selected_periodicity='PERIOD_YEAR' WHERE id=$1", uid)
	var request lava.CreateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		respond(w, http.StatusCreated, lava.Checkout{
			ID:         "year-contract",
			PaymentURL: "https://pay.example/year",
			AmountTotal: lava.AmountTotal{
				Amount:   json.Number("24.000000"),
				Currency: "USD",
			},
		})
	}))
	defer srv.Close()
	a.Lava.BaseURL = srv.URL
	key, err := a.checkout(context.Background(), uid, "pay:foreign:paypal")
	if err != nil || key != "payment_created" {
		t.Fatalf("key=%q err=%v", key, err)
	}
	if request.Email != "buyer@example.com" || request.Periodicity != "PERIOD_YEAR" || request.Provider != "PAYPAL" {
		t.Fatalf("unexpected Lava request: %#v", request)
	}
	var attemptPeriod, subscriptionPeriod, amount, currency string
	if err := a.DB.QueryRow(
		context.Background(),
		"SELECT periodicity,price_amount::text,price_currency FROM checkout_attempts WHERE user_id=$1",
		uid,
	).Scan(&attemptPeriod, &amount, &currency); err != nil {
		t.Fatal(err)
	}
	if err := a.DB.QueryRow(context.Background(), "SELECT periodicity FROM subscriptions WHERE user_id=$1", uid).Scan(&subscriptionPeriod); err != nil {
		t.Fatal(err)
	}
	if attemptPeriod != "PERIOD_YEAR" || subscriptionPeriod != "PERIOD_YEAR" || amount != "24.000000" || currency != "USD" {
		t.Fatalf("attempt=%q subscription=%q amount=%q currency=%q", attemptPeriod, subscriptionPeriod, amount, currency)
	}
}

func TestWebhookAuthAndDedup(t *testing.T) {
	a := testApp(t)
	h := a.Handler()
	payload := `{"update_id":123,"message":{"text":"/start"}}`
	for _, auth := range []string{"wrong", a.Config.TelegramSecret, a.Config.TelegramSecret} {
		r := httptest.NewRequest("POST", "/webhooks/telegram", strings.NewReader(payload))
		r.Header.Set("X-Telegram-Bot-Api-Secret-Token", auth)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 200
		if auth == "wrong" {
			want = 401
		}
		if w.Code != want {
			t.Fatalf("webhook status %d", w.Code)
		}
	}
	var count int
	if e := a.DB.QueryRow(context.Background(), "SELECT count(*) FROM webhook_events").Scan(&count); e != nil || count != 1 {
		t.Fatalf("dedup count %d %v", count, e)
	}
}
func telegramServer(t *testing.T, a *App, handler func(string, map[string]any) (int, any)) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		parts := strings.Split(r.URL.Path, "/")
		code, result := handler(parts[len(parts)-1], body)
		respond(w, code, result)
	}))
	t.Cleanup(srv.Close)
	a.Telegram = telegram.New("test")
	a.Telegram.BaseURL = srv.URL
}
func TestExpirationRecoversUnbanAndProtectsRenewal(t *testing.T) {
	a := testApp(t)
	expired := paidSnapshot()
	past := time.Now().Add(-time.Minute).UTC()
	expired.ExpiredAt = &past
	snapshotServer(t, a, expired)

	uid := seedUser(t, a)
	seedSubscription(t, a, uid)
	execTest(t, a, "UPDATE subscriptions SET expires_at=now()-interval '1 minute'")
	execTest(t, a, "INSERT INTO channel_access(user_id,state) VALUES($1,'member')", uid)
	var methods []string
	fail := true
	telegramServer(t, a, func(method string, _ map[string]any) (int, any) {
		methods = append(methods, method)
		if method == "unbanChatMember" && fail {
			fail = false
			return 500, map[string]any{"ok": false, "error_code": 500}
		}
		return 200, map[string]any{"ok": true, "result": true}
	})
	if e := a.removeMember(context.Background(), uid); e == nil {
		t.Fatal("expected failed unban")
	}
	var s string
	if e := a.DB.QueryRow(context.Background(), "SELECT state FROM channel_access WHERE user_id=$1", uid).Scan(&s); e != nil || s != "removing" {
		t.Fatalf("lost removal stage %s %v", s, e)
	}
	execTest(t, a, "UPDATE subscriptions SET expires_at=now()+interval '1 day'")
	if e := a.removeMember(context.Background(), uid); e != nil {
		t.Fatal(e)
	}
	if strings.Join(methods, ",") != "banChatMember,unbanChatMember,unbanChatMember" {
		t.Fatalf("renewed user banned: %v", methods)
	}
}
func TestBroadcastStopsAfterPaymentAndCycles(t *testing.T) {
	a := testApp(t)
	uid := seedUser(t, a)
	seedSubscription(t, a, uid)
	var campaignID int64
	if e := a.DB.QueryRow(context.Background(), "INSERT INTO broadcast_campaigns(name,audience_status,enabled) VALUES('test','new',true) RETURNING id").Scan(&campaignID); e != nil {
		t.Fatal(e)
	}
	execTest(t, a, "INSERT INTO broadcast_steps(campaign_id,kind,position,delay_seconds) VALUES($1,'delay',1,60)", campaignID)
	execTest(t, a, "INSERT INTO broadcast_steps(campaign_id,kind,message_id,position,delay_seconds) SELECT $1,'message',id,2,0 FROM bot_messages WHERE key='broadcast_intro'", campaignID)
	var sent int
	telegramServer(t, a, func(method string, _ map[string]any) (int, any) {
		if method == "sendMessage" {
			sent++
		}
		return 200, map[string]any{"ok": true, "result": true}
	})
	if e := a.broadcast(context.Background()); e != nil {
		t.Fatal(e)
	}
	execTest(t, a, "UPDATE broadcast_user_state SET next_send_at=now()-interval '1 second'")
	if e := a.broadcast(context.Background()); e != nil {
		t.Fatal(e)
	}
	if sent != 1 {
		t.Fatalf("sent %d", sent)
	}
	execTest(t, a, "UPDATE broadcast_user_state SET next_send_at=now()-interval '1 second'")
	execTest(t, a, "UPDATE subscriptions SET expires_at=now()+interval '1 day'")
	if e := a.broadcastOne(context.Background(), campaignID, uid); e != nil {
		t.Fatal(e)
	}
	if sent != 1 {
		t.Fatal("broadcast sent to active user")
	}
}

func TestManualMailingProgressCountsMessagesAndRecipients(t *testing.T) {
	a := testApp(t)
	firstUser := seedUser(t, a)
	var secondUser, campaignID, runID int64
	if err := a.DB.QueryRow(context.Background(), "INSERT INTO users(telegram_user_id,first_started_at) VALUES(23456,now()) RETURNING id").Scan(&secondUser); err != nil {
		t.Fatal(err)
	}
	if err := a.DB.QueryRow(context.Background(), "INSERT INTO broadcast_campaigns(name,audience_status,mode,run_status) VALUES('progress','all','manual','sending') RETURNING id").Scan(&campaignID); err != nil {
		t.Fatal(err)
	}
	execTest(t, a, `INSERT INTO broadcast_steps(campaign_id,kind,message_id,position,delay_seconds)
		SELECT $1,'message',id,1,0 FROM bot_messages WHERE key='broadcast_intro'`, campaignID)
	execTest(t, a, `INSERT INTO broadcast_steps(campaign_id,kind,message_id,position,delay_seconds)
		SELECT $1,'message',id,2,0 FROM bot_messages WHERE key='broadcast_intro'`, campaignID)
	if err := a.DB.QueryRow(context.Background(), "INSERT INTO mailing_runs(campaign_id,audience_status,status,scheduled_at,started_at) VALUES($1,'all','sending',now(),now()) RETURNING id", campaignID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	execTest(t, a, "UPDATE broadcast_campaigns SET current_run_id=$2 WHERE id=$1", campaignID, runID)
	execTest(t, a, "INSERT INTO mailing_deliveries(campaign_id,run_id,user_id,next_send_at,sent_count) VALUES($1,$2,$3,now(),1)", campaignID, runID, firstUser)
	execTest(t, a, "INSERT INTO mailing_deliveries(campaign_id,run_id,user_id,next_send_at,status,sent_count) VALUES($1,$2,$3,now(),'completed',2)", campaignID, runID, secondUser)
	w := httptest.NewRecorder()
	if err := a.broadcastCampaigns(w, httptest.NewRequest("GET", "/api/mailings/1", nil), campaignID, "manual"); err != nil {
		t.Fatal(err)
	}
	var campaign BroadcastCampaign
	if err := json.Unmarshal(w.Body.Bytes(), &campaign); err != nil {
		t.Fatal(err)
	}
	if campaign.RecipientCount != 2 || campaign.MessageCount != 2 || campaign.SentCount != 3 || campaign.CompletedCount != 1 {
		t.Fatalf("mailing progress: %+v", campaign)
	}
}

func TestManualMailingSendsOnceAndCompletes(t *testing.T) {
	a := testApp(t)
	uid := seedUser(t, a)
	var id int64
	if err := a.DB.QueryRow(context.Background(), `INSERT INTO broadcast_campaigns(name,audience_status,mode)
		VALUES('one shot','new','manual') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	execTest(t, a, `INSERT INTO broadcast_steps(campaign_id,kind,message_id,position,delay_seconds)
		SELECT $1,'message',id,1,0 FROM bot_messages WHERE key='broadcast_intro'`, id)
	execTest(t, a, "INSERT INTO broadcast_steps(campaign_id,kind,position,delay_seconds) VALUES($1,'delay',2,5)", id)
	execTest(t, a, `INSERT INTO broadcast_steps(campaign_id,kind,message_id,position,delay_seconds)
		SELECT $1,'message',id,3,0 FROM bot_messages WHERE key='broadcast_intro'`, id)
	sent := 0
	telegramServer(t, a, func(method string, _ map[string]any) (int, any) {
		if method == "sendMessage" {
			sent++
		}
		return 200, map[string]any{"ok": true, "result": true}
	})
	call := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/mailings/1/send", strings.NewReader(`{"scheduled_at":null}`))
		w := httptest.NewRecorder()
		if err := a.launchMailing(w, r, id); err != nil {
			t.Fatal(err)
		}
		return w
	}
	if w := call(); w.Code != 200 {
		t.Fatalf("launch: %d %s", w.Code, w.Body)
	}
	if w := call(); w.Code != 409 {
		t.Fatalf("relaunch: %d %s", w.Code, w.Body)
	}
	if err := a.processMailings(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Fatalf("first message count: %d", sent)
	}
	var status string
	var recipients int
	if err := a.DB.QueryRow(context.Background(), "SELECT run_status,(SELECT count(*) FROM mailing_deliveries WHERE campaign_id=$1) FROM broadcast_campaigns WHERE id=$1", id).Scan(&status, &recipients); err != nil {
		t.Fatal(err)
	}
	if status != "sending" || recipients != 1 {
		t.Fatalf("status=%s recipients=%d", status, recipients)
	}
	// A user who starts after the snapshot must not join this run.
	execTest(t, a, "INSERT INTO users(telegram_user_id,first_started_at) VALUES(23456,now())")
	execTest(t, a, "UPDATE mailing_deliveries SET next_send_at=now()-interval '1 second' WHERE campaign_id=$1 AND user_id=$2", id, uid)
	if err := a.processMailings(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.processMailings(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sent != 2 {
		t.Fatalf("expected exactly two messages, got %d", sent)
	}
	if err := a.DB.QueryRow(context.Background(), "SELECT run_status FROM broadcast_campaigns WHERE id=$1", id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "completed" {
		t.Fatalf("status=%s", status)
	}
	if w := call(); w.Code != 200 {
		t.Fatalf("repeat launch: %d %s", w.Code, w.Body)
	}
	if err := a.processMailings(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sent != 4 {
		t.Fatalf("repeat first message count: %d", sent)
	}
	execTest(t, a, `UPDATE mailing_deliveries SET next_send_at=now()-interval '1 second'
		WHERE run_id=(SELECT current_run_id FROM broadcast_campaigns WHERE id=$1)`, id)
	if err := a.processMailings(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.processMailings(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sent != 6 {
		t.Fatalf("repeat total message count: %d", sent)
	}
	rows, err := a.DB.Query(context.Background(), `SELECT r.status,count(d.user_id) FROM mailing_runs r
		LEFT JOIN mailing_deliveries d ON d.run_id=r.id WHERE r.campaign_id=$1 GROUP BY r.id ORDER BY r.id`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	counts := []int{}
	for rows.Next() {
		var runStatus string
		var count int
		if err = rows.Scan(&runStatus, &count); err != nil {
			t.Fatal(err)
		}
		if runStatus != "completed" {
			t.Fatalf("run status=%s", runStatus)
		}
		counts = append(counts, count)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(counts, []int{1, 2}) {
		t.Fatalf("run recipient history=%v", counts)
	}
}

func TestManualMailingWaitsForSchedule(t *testing.T) {
	a := testApp(t)
	var id int64
	if err := a.DB.QueryRow(context.Background(), `INSERT INTO broadcast_campaigns(name,audience_status,mode)
		VALUES('scheduled','all','manual') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	execTest(t, a, `INSERT INTO broadcast_steps(campaign_id,kind,message_id,position,delay_seconds)
		SELECT $1,'message',id,1,0 FROM bot_messages WHERE key='broadcast_intro'`, id)
	when := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	r := httptest.NewRequest("POST", "/api/mailings/1/send", strings.NewReader(fmt.Sprintf(`{"scheduled_at":%q}`, when)))
	w := httptest.NewRecorder()
	if err := a.launchMailing(w, r, id); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 {
		t.Fatalf("schedule: %d %s", w.Code, w.Body)
	}
	if err := a.processMailings(context.Background()); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := a.DB.QueryRow(context.Background(), "SELECT run_status FROM broadcast_campaigns WHERE id=$1", id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "scheduled" {
		t.Fatalf("started early: %s", status)
	}
	execTest(t, a, "UPDATE mailing_runs SET scheduled_at=now()-interval '1 second' WHERE campaign_id=$1", id)
	if err := a.processMailings(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.DB.QueryRow(context.Background(), "SELECT run_status FROM broadcast_campaigns WHERE id=$1", id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "completed" {
		t.Fatalf("empty scheduled mailing: %s", status)
	}
}

func TestManualMailingMultipleScheduledDates(t *testing.T) {
	a := testApp(t)
	seedUser(t, a)
	var id int64
	if err := a.DB.QueryRow(context.Background(), `INSERT INTO broadcast_campaigns(name,audience_status,mode)
		VALUES('multiple dates','all','manual') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	execTest(t, a, `INSERT INTO broadcast_steps(campaign_id,kind,message_id,position,delay_seconds)
		SELECT $1,'message',id,1,0 FROM bot_messages WHERE key='broadcast_intro'`, id)
	sent := 0
	telegramServer(t, a, func(method string, _ map[string]any) (int, any) {
		if method == "sendMessage" {
			sent++
		}
		return 200, map[string]any{"ok": true, "result": true}
	})
	first := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	second := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	r := httptest.NewRequest("POST", "/api/mailings/1/send", strings.NewReader(fmt.Sprintf(`{"scheduled_times":[%q,%q]}`, second, first)))
	w := httptest.NewRecorder()
	if err := a.launchMailing(w, r, id); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 {
		t.Fatalf("schedule: %d %s", w.Code, w.Body)
	}
	var count int
	if err := a.DB.QueryRow(context.Background(), "SELECT count(*) FROM mailing_runs WHERE campaign_id=$1", id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("scheduled runs=%d", count)
	}
	if err := a.processMailings(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sent != 0 {
		t.Fatalf("sent early: %d", sent)
	}
	execTest(t, a, `UPDATE mailing_runs SET scheduled_at=now()-interval '1 second'
		WHERE id=(SELECT id FROM mailing_runs WHERE campaign_id=$1 ORDER BY scheduled_at LIMIT 1)`, id)
	if err := a.processMailings(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Fatalf("first run sent=%d", sent)
	}
	var status string
	if err := a.DB.QueryRow(context.Background(), "SELECT run_status FROM broadcast_campaigns WHERE id=$1", id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "scheduled" {
		t.Fatalf("status with second run queued=%s", status)
	}
	execTest(t, a, "UPDATE mailing_runs SET scheduled_at=now()-interval '1 second' WHERE campaign_id=$1 AND status='scheduled'", id)
	if err := a.processMailings(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sent != 2 {
		t.Fatalf("both runs sent=%d", sent)
	}
	if err := a.DB.QueryRow(context.Background(), "SELECT run_status FROM broadcast_campaigns WHERE id=$1", id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "completed" {
		t.Fatalf("final status=%s", status)
	}
}

func TestManualMailingScheduleCanBeEditedBeforeSend(t *testing.T) {
	a := testApp(t)
	var id int64
	if err := a.DB.QueryRow(context.Background(), `INSERT INTO broadcast_campaigns(name,audience_status,mode)
		VALUES('editable schedule','all','manual') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	execTest(t, a, `INSERT INTO broadcast_steps(campaign_id,kind,message_id,position,delay_seconds)
		SELECT $1,'message',id,1,0 FROM bot_messages WHERE key='broadcast_intro'`, id)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		w := httptest.NewRecorder()
		var err error
		if method == "POST" {
			err = a.launchMailing(w, r, id)
		} else {
			err = a.updateMailingSchedule(w, r, id)
		}
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	first := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	second := first.Add(time.Hour)
	third := second.Add(time.Hour)
	if w := call("POST", "/api/mailings/1/send", fmt.Sprintf(`{"scheduled_at":%q}`, first.Format(time.RFC3339))); w.Code != 200 {
		t.Fatalf("launch: %d %s", w.Code, w.Body)
	}
	var firstID int64
	if err := a.DB.QueryRow(context.Background(), "SELECT id FROM mailing_runs WHERE campaign_id=$1", id).Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	edit := fmt.Sprintf(`{"expected_run_ids":[%d],"runs":[{"id":%d,"scheduled_at":%q},{"scheduled_at":%q}]}`, firstID, firstID, second.Format(time.RFC3339), third.Format(time.RFC3339))
	if w := call("PUT", "/api/mailings/1/schedule", edit); w.Code != 200 {
		t.Fatalf("edit: %d %s", w.Code, w.Body)
	}
	var count int
	var nearest time.Time
	if err := a.DB.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM mailing_runs WHERE campaign_id=$1),scheduled_at
		FROM broadcast_campaigns WHERE id=$1`, id).Scan(&count, &nearest); err != nil {
		t.Fatal(err)
	}
	if count != 2 || !nearest.Equal(second) {
		t.Fatalf("edited schedule: count=%d nearest=%s", count, nearest)
	}
	settings := BroadcastCampaign{Name: "editable schedule", AudienceStatus: "new"}
	w := httptest.NewRecorder()
	if err := a.broadcastCampaigns(w, httptest.NewRequest("PUT", "/api/mailings/1", bytes.NewReader(marshal(settings))), id, "manual"); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 {
		t.Fatalf("edit scheduled audience: %d %s", w.Code, w.Body)
	}
	var updated int
	if err := a.DB.QueryRow(context.Background(), "SELECT count(*) FROM mailing_runs WHERE campaign_id=$1 AND status='scheduled' AND audience_status='new'", id).Scan(&updated); err != nil || updated != 2 {
		t.Fatalf("scheduled audiences updated=%d err=%v", updated, err)
	}
	stale := fmt.Sprintf(`{"expected_run_ids":[],"runs":[]}`)
	if w := call("PUT", "/api/mailings/1/schedule", stale); w.Code != 409 {
		t.Fatalf("stale edit: %d %s", w.Code, w.Body)
	}
	var secondID int64
	if err := a.DB.QueryRow(context.Background(), "SELECT id FROM mailing_runs WHERE campaign_id=$1 AND id<>$2", id, firstID).Scan(&secondID); err != nil {
		t.Fatal(err)
	}
	remove := fmt.Sprintf(`{"expected_run_ids":[%d,%d],"runs":[{"id":%d,"scheduled_at":%q}]}`, firstID, secondID, secondID, third.Format(time.RFC3339))
	if w := call("PUT", "/api/mailings/1/schedule", remove); w.Code != 200 {
		t.Fatalf("remove: %d %s", w.Code, w.Body)
	}
	if err := a.DB.QueryRow(context.Background(), "SELECT count(*) FROM mailing_runs WHERE campaign_id=$1", id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("remaining runs=%d", count)
	}
	removeAll := fmt.Sprintf(`{"expected_run_ids":[%d],"runs":[]}`, secondID)
	if w := call("PUT", "/api/mailings/1/schedule", removeAll); w.Code != 200 {
		t.Fatalf("remove all: %d %s", w.Code, w.Body)
	}
	var status string
	if err := a.DB.QueryRow(context.Background(), "SELECT run_status FROM broadcast_campaigns WHERE id=$1", id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "draft" {
		t.Fatalf("status after removing schedule=%s", status)
	}
}

func TestScheduledMailingStepsCanBeEditedUntilSending(t *testing.T) {
	a := testApp(t)
	var campaignID int64
	if err := a.DB.QueryRow(context.Background(), `INSERT INTO broadcast_campaigns(name,audience_status,mode)
		VALUES('scheduled edits','all','manual') RETURNING id`).Scan(&campaignID); err != nil {
		t.Fatal(err)
	}
	callStep := func(method string, id int64, body Broadcast) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/api/mailings/1/steps", bytes.NewReader(marshal(body)))
		if err := a.broadcasts(w, r, campaignID, id, "manual"); err != nil {
			t.Fatal(err)
		}
		return w
	}
	message := Broadcast{Kind: "message", Enabled: true, Message: Content{Name: "Шаг", Text: "Первый текст", Buttons: []Button{}}}
	w := callStep("POST", 0, message)
	if w.Code != 200 {
		t.Fatalf("create message: %d %s", w.Code, w.Body)
	}
	var created struct {
		ID        int64
		MessageID int64 `json:"message_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	w = httptest.NewRecorder()
	if err := a.launchMailing(w, httptest.NewRequest("POST", "/api/mailings/1/send", strings.NewReader(fmt.Sprintf(`{"scheduled_at":%q}`, when))), campaignID); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 {
		t.Fatalf("schedule: %d %s", w.Code, w.Body)
	}
	message.Message.Version = 1
	message.Message.Text = "Текст перед запуском"
	if w = callStep("PUT", created.ID, message); w.Code != 200 {
		t.Fatalf("edit scheduled message: %d %s", w.Code, w.Body)
	}
	if w = callStep("POST", 0, Broadcast{Kind: "delay", Delay: 30}); w.Code != 200 {
		t.Fatalf("add scheduled delay: %d %s", w.Code, w.Body)
	}
	execTest(t, a, "UPDATE broadcast_campaigns SET run_status='sending' WHERE id=$1", campaignID)
	settings := BroadcastCampaign{Name: "scheduled edits", AudienceStatus: "new"}
	w = httptest.NewRecorder()
	if err := a.broadcastCampaigns(w, httptest.NewRequest("PUT", "/api/mailings/1", bytes.NewReader(marshal(settings))), campaignID, "manual"); err != nil {
		t.Fatal(err)
	}
	if w.Code != 409 {
		t.Fatalf("changed audience after sending started: %d %s", w.Code, w.Body)
	}
	message.Message.Version = 2
	message.Message.Text = "Текст во время отправки"
	if w = callStep("PUT", created.ID, message); w.Code != 409 {
		t.Fatalf("edited sending message: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "Рассылка уже отправляется") {
		t.Fatalf("missing sending explanation: %s", w.Body)
	}
	content := Content{Name: "Шаг", Text: "Прямое изменение", Version: 2, Buttons: []Button{}}
	w = httptest.NewRecorder()
	if err := a.messages(w, httptest.NewRequest("PUT", "/api/messages/1", bytes.NewReader(marshal(content))), created.MessageID); err != nil {
		t.Fatal(err)
	}
	if w.Code != 409 {
		t.Fatalf("edited sending message directly: %d %s", w.Code, w.Body)
	}
}

func TestManualMailingSendNowKeepsScheduleAndAllowsRename(t *testing.T) {
	a := testApp(t)
	seedUser(t, a)
	var id int64
	if err := a.DB.QueryRow(context.Background(), `INSERT INTO broadcast_campaigns(name,audience_status,mode)
		VALUES('before','all','manual') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	execTest(t, a, `INSERT INTO broadcast_steps(campaign_id,kind,message_id,position,delay_seconds)
		SELECT $1,'message',id,1,0 FROM bot_messages WHERE key='broadcast_intro'`, id)
	when := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	launch := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/mailings/1/send", strings.NewReader(body))
		w := httptest.NewRecorder()
		if err := a.launchMailing(w, r, id); err != nil {
			t.Fatal(err)
		}
		return w
	}
	if w := launch(fmt.Sprintf(`{"scheduled_at":%q}`, when.Format(time.RFC3339))); w.Code != 200 {
		t.Fatalf("schedule: %d %s", w.Code, w.Body)
	}
	rename := func(audience string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", "/api/mailings/1", strings.NewReader(fmt.Sprintf(`{"name":"after","audience_status":%q}`, audience)))
		w := httptest.NewRecorder()
		if err := a.broadcastCampaigns(w, r, id, "manual"); err != nil {
			t.Fatal(err)
		}
		return w
	}
	if w := rename("new"); w.Code != 200 {
		t.Fatalf("audience change before sending: %d %s", w.Code, w.Body)
	}
	if w := rename("all"); w.Code != 200 {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	if w := launch(`{"scheduled_at":null}`); w.Code != 200 {
		t.Fatalf("send now: %d %s", w.Code, w.Body)
	}
	if w := launch(`{"scheduled_at":null}`); w.Code != 409 {
		t.Fatalf("duplicate send now: %d %s", w.Code, w.Body)
	}
	sent := 0
	telegramServer(t, a, func(method string, _ map[string]any) (int, any) {
		if method == "sendMessage" {
			sent++
		}
		return 200, map[string]any{"ok": true, "result": true}
	})
	if err := a.processMailings(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Fatalf("immediate messages=%d", sent)
	}
	var name, status string
	var next time.Time
	if err := a.DB.QueryRow(context.Background(), "SELECT name,run_status,scheduled_at FROM broadcast_campaigns WHERE id=$1", id).Scan(&name, &status, &next); err != nil {
		t.Fatal(err)
	}
	if name != "after" || status != "scheduled" || !next.Equal(when) {
		t.Fatalf("after immediate send: name=%s status=%s next=%s", name, status, next)
	}
	var scheduled, completed int
	if err := a.DB.QueryRow(context.Background(), `SELECT count(*) FILTER (WHERE status='scheduled'),count(*) FILTER (WHERE status='completed')
		FROM mailing_runs WHERE campaign_id=$1`, id).Scan(&scheduled, &completed); err != nil {
		t.Fatal(err)
	}
	if scheduled != 1 || completed != 1 {
		t.Fatalf("run statuses: scheduled=%d completed=%d", scheduled, completed)
	}
}

func TestBroadcastOwnMessageLifecycle(t *testing.T) {
	a := testApp(t)
	var campaignID int64
	if e := a.DB.QueryRow(context.Background(), "INSERT INTO broadcast_campaigns(name,audience_status,enabled) VALUES('test','new',true) RETURNING id").Scan(&campaignID); e != nil {
		t.Fatal(e)
	}
	call := func(method string, id int64, body any) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/broadcasts", bytes.NewReader(marshal(body)))
		w := httptest.NewRecorder()
		if err := a.broadcasts(w, r, campaignID, id, "auto"); err != nil {
			t.Fatal(err)
		}
		return w
	}
	b := Broadcast{Kind: "message", Enabled: true, Message: Content{Name: "Первый шаг", Text: "Привет", ParseMode: "HTML", Buttons: []Button{}}}
	w := call("POST", 0, b)
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var created struct{ ID, MessageID int64 }
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	// Response uses snake case; verify the dedicated message through the database.
	var messageID int64
	var key string
	if err := a.DB.QueryRow(context.Background(), "SELECT m.id,m.key FROM broadcast_steps b JOIN bot_messages m ON m.id=b.message_id WHERE b.id=$1", created.ID).Scan(&messageID, &key); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "broadcast_step_") {
		t.Fatalf("message key = %q", key)
	}
	b.Message.Version = 1
	b.Message.Text = "Изменённый текст"
	w = call("PUT", created.ID, b)
	if w.Code != 200 {
		t.Fatalf("update: %d %s", w.Code, w.Body)
	}
	var text string
	if err := a.DB.QueryRow(context.Background(), "SELECT text FROM bot_messages WHERE id=$1", messageID).Scan(&text); err != nil || text != b.Message.Text {
		t.Fatalf("message text: %q, %v", text, err)
	}
	w = call("POST", 0, Broadcast{Kind: "delay", Delay: 1})
	if w.Code != 200 {
		t.Fatalf("create delay: %d %s", w.Code, w.Body)
	}
	var pause struct{ ID int64 }
	if err := json.Unmarshal(w.Body.Bytes(), &pause); err != nil {
		t.Fatal(err)
	}
	w = call("PUT", 0, map[string]any{"order": []int64{pause.ID, created.ID}})
	if w.Code != 200 {
		t.Fatalf("reorder: %d %s", w.Code, w.Body)
	}
	var firstKind string
	if err := a.DB.QueryRow(context.Background(), "SELECT kind FROM broadcast_steps ORDER BY position LIMIT 1").Scan(&firstKind); err != nil || firstKind != "delay" {
		t.Fatalf("first block: %q, %v", firstKind, err)
	}
	w = call("DELETE", pause.ID, nil)
	if w.Code != 200 {
		t.Fatalf("delete delay: %d %s", w.Code, w.Body)
	}
	w = call("DELETE", created.ID, nil)
	if w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	var count int
	if err := a.DB.QueryRow(context.Background(), "SELECT count(*) FROM bot_messages WHERE id=$1", messageID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphaned message count: %d, %v", count, err)
	}
	var legacyID int64
	if err := a.DB.QueryRow(context.Background(), "INSERT INTO broadcast_steps(campaign_id,kind,message_id,position,delay_seconds) SELECT $1,'message',id,2,0 FROM bot_messages WHERE key='broadcast_intro' RETURNING id", campaignID).Scan(&legacyID); err != nil {
		t.Fatal(err)
	}
	if w = call("PUT", legacyID, b); w.Code != 409 {
		t.Fatalf("legacy template edited: %d %s", w.Code, w.Body)
	}
	if w = call("DELETE", legacyID, nil); w.Code != 200 {
		t.Fatalf("delete legacy step: %d %s", w.Code, w.Body)
	}
	if err := a.DB.QueryRow(context.Background(), "SELECT count(*) FROM bot_messages WHERE key='broadcast_intro'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("legacy template count: %d, %v", count, err)
	}
}
func TestBroadcastCampaignAudienceAndIsolation(t *testing.T) {
	a := testApp(t)
	uid := seedUser(t, a)
	var first, second int64
	for index, target := range []*int64{&first, &second} {
		name := fmt.Sprintf("Рассылка %d", index+1)
		if err := a.DB.QueryRow(context.Background(), "INSERT INTO broadcast_campaigns(name,audience_status,enabled) VALUES($1,'new',true) RETURNING id", name).Scan(target); err != nil {
			t.Fatal(err)
		}
		execTest(t, a, "INSERT INTO broadcast_steps(campaign_id,kind,message_id,position,delay_seconds) SELECT $1,'message',id,1,0 FROM bot_messages WHERE key='broadcast_intro'", *target)
	}
	if err := a.broadcast(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := a.DB.QueryRow(context.Background(), "SELECT count(*) FROM broadcast_user_state WHERE user_id=$1", uid).Scan(&count); err != nil || count != 2 {
		t.Fatalf("independent states: %d, %v", count, err)
	}
	execTest(t, a, "UPDATE broadcast_campaigns SET audience_status='active' WHERE id=$1", second)
	if err := a.broadcast(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.DB.QueryRow(context.Background(), "SELECT count(*) FROM broadcast_user_state WHERE user_id=$1", uid).Scan(&count); err != nil || count != 1 {
		t.Fatalf("filtered states: %d, %v", count, err)
	}
	var stepID int64
	if err := a.DB.QueryRow(context.Background(), "SELECT id FROM broadcast_steps WHERE campaign_id=$1", first).Scan(&stepID); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("DELETE", "/api/broadcasts", nil)
	w := httptest.NewRecorder()
	if err := a.broadcasts(w, r, second, stepID, "auto"); err != nil {
		t.Fatal(err)
	}
	if w.Code != 404 {
		t.Fatalf("cross-campaign delete: %d %s", w.Code, w.Body.String())
	}
	if err := a.DB.QueryRow(context.Background(), "SELECT count(*) FROM broadcast_steps WHERE id=$1", stepID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("step deleted across campaign: %d, %v", count, err)
	}
}
func TestAdminAuthenticationAndOptimisticUpdate(t *testing.T) {
	a := testApp(t)
	hash, e := bcrypt.GenerateFromPassword([]byte("test-password-long"), bcrypt.MinCost)
	if e != nil {
		t.Fatal(e)
	}
	a.Config.AdminPasswordHash = string(hash)
	h := a.Handler()
	call := func(method, path, body string, cookie *http.Cookie, origin bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if origin {
			r.Header.Set("Origin", a.Config.BaseURL)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("GET", "/api/messages", "", nil, false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := call("POST", "/api/login", `{"password":"test-password-long"}`, nil, true)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	cookie := w.Result().Cookies()[0]
	w = call("GET", "/api/messages", "", cookie, false)
	var messages []Content
	if e = json.Unmarshal(w.Body.Bytes(), &messages); e != nil || len(messages) == 0 {
		t.Fatalf("messages: %s %v", w.Body, e)
	}
	c := messages[0]
	c.Text = "Updated"
	body := string(marshal(c))
	if w = call("PUT", "/api/messages/"+jsonNumber(c.ID), body, cookie, false); w.Code != 403 {
		t.Fatal("CSRF accepted")
	}
	if w = call("PUT", "/api/messages/"+jsonNumber(c.ID), body, cookie, true); w.Code != 200 {
		t.Fatalf("update: %d %s", w.Code, w.Body)
	}
	if w = call("PUT", "/api/messages/"+jsonNumber(c.ID), body, cookie, true); w.Code != 409 {
		t.Fatalf("lost update accepted: %d", w.Code)
	}
	if w = call("GET", "/api/users?status=active&auto_renew=true", "", cookie, false); w.Code != 200 {
		t.Fatalf("users: %d %s", w.Code, w.Body)
	}
}

func TestTerminatedAccessCannotBeRevivedByStaleSnapshot(t *testing.T) {
	a := testApp(t)
	uid := seedUser(t, a)
	seedSubscription(t, a, uid)
	s := paidSnapshot()
	set := snapshotServer(t, a, s)
	if e := a.SyncSubscription(context.Background(), "root"); e != nil {
		t.Fatal(e)
	}
	terminated := time.Now().Add(-time.Minute).UTC()
	s.TerminatedAt = &terminated
	set(s)
	if e := a.SyncSubscription(context.Background(), "root"); e != nil {
		t.Fatal(e)
	}
	s.TerminatedAt = nil
	set(s)
	if e := a.SyncSubscription(context.Background(), "root"); e != nil {
		t.Fatal(e)
	}
	status, renew, _ := state(t, a, uid)
	if status != "expired" || renew {
		t.Fatalf("revived terminated subscription: %s %v", status, renew)
	}
}

func TestBotNavigationAndPaidCheck(t *testing.T) {
	a := testApp(t)
	var methods, texts []string
	telegramServer(t, a, func(method string, body map[string]any) (int, any) {
		methods = append(methods, method)
		if text, ok := body["text"].(string); ok {
			texts = append(texts, text)
		}
		return 200, map[string]any{"ok": true, "result": true}
	})
	if e := a.processTelegram(context.Background(), telegram.Update{ID: 1, Message: &telegram.Message{ID: 1, From: telegram.User{ID: 12345, FirstName: "Test"}, Chat: telegram.Chat{ID: 12345, Type: "private"}, Text: "/start"}}); e != nil {
		t.Fatal(e)
	}
	var update telegram.Update
	for _, action := range []string{"club:join", "plan:90d", "pay:region:ru", "club:join", "pay:check"} {
		payload := fmt.Sprintf(`{"update_id":2,"callback_query":{"id":"callback","from":{"id":12345},"message":{"message_id":1,"chat":{"id":12345,"type":"private"}},"data":%q}}`, action)
		if e := json.Unmarshal([]byte(payload), &update); e != nil {
			t.Fatal(e)
		}
		if e := a.processTelegram(context.Background(), update); e != nil {
			t.Fatal(e)
		}
	}
	if methods[0] != "sendMessage" {
		t.Fatal(methods)
	}
	for _, method := range methods[1:] {
		if method == "sendMessage" {
			t.Fatal("navigation created another message")
		}
	}
	if !strings.Contains(texts[len(texts)-1], "Платёж пока не найден") {
		t.Fatal("payment button granted access")
	}
	var status string
	if e := a.DB.QueryRow(context.Background(), "SELECT status FROM user_status WHERE telegram_user_id=12345").Scan(&status); e != nil || status != "new" {
		t.Fatalf("%s %v", status, e)
	}
}

func TestRapidDuplicateStartSendsOnce(t *testing.T) {
	a := testApp(t)
	sends := 0
	telegramServer(t, a, func(method string, _ map[string]any) (int, any) {
		if method == "sendMessage" {
			sends++
		}
		return 200, map[string]any{"ok": true, "result": true}
	})
	for i := int64(1); i <= 2; i++ {
		update := telegram.Update{ID: i, Message: &telegram.Message{
			ID: i, From: telegram.User{ID: 12345}, Chat: telegram.Chat{ID: 12345, Type: "private"}, Text: "/start",
		}}
		payload, err := json.Marshal(update)
		if err != nil {
			t.Fatal(err)
		}
		execTest(t, a, "INSERT INTO webhook_events(provider,dedup_key,payload) VALUES('telegram',$1,$2)", fmt.Sprint(i), payload)
		if err = a.processTelegram(context.Background(), update); err != nil {
			t.Fatal(err)
		}
		execTest(t, a, "UPDATE webhook_events SET processed_at=now() WHERE dedup_key=$1", fmt.Sprint(i))
	}
	if sends != 1 {
		t.Fatalf("rapid /start sent %d responses", sends)
	}
}

func TestPaidCheckQueuesOneConfirmation(t *testing.T) {
	a := testApp(t)
	uid := seedUser(t, a)
	seedSubscription(t, a, uid)
	snapshotServer(t, a, paidSnapshot())
	sends := 0
	telegramServer(t, a, func(method string, body map[string]any) (int, any) {
		if method == "sendMessage" || method == "editMessageText" {
			if text, ok := body["text"].(string); ok && strings.Contains(text, "ОПЛАТА ПРОШЛА") {
				sends++
			}
		}
		return 200, map[string]any{"ok": true, "result": true}
	})
	var update telegram.Update
	if err := json.Unmarshal([]byte(`{"update_id":1,"callback_query":{"id":"check","from":{"id":12345},"message":{"message_id":1,"chat":{"id":12345,"type":"private"}},"data":"pay:check"}}`), &update); err != nil {
		t.Fatal(err)
	}
	if err := a.processTelegram(context.Background(), update); err != nil {
		t.Fatal(err)
	}
	if err := a.outbox(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sends != 1 {
		t.Fatalf("payment confirmation sent %d times", sends)
	}
}

func TestPaymentBackNavigationReturnsOneStep(t *testing.T) {
	a := testApp(t)
	texts := []string{}
	telegramServer(t, a, func(_ string, body map[string]any) (int, any) {
		if text, ok := body["text"].(string); ok {
			texts = append(texts, text)
		}
		return 200, map[string]any{"ok": true, "result": true}
	})
	message := telegram.Message{
		ID:   1,
		From: telegram.User{ID: 24680, FirstName: "Test"},
		Chat: telegram.Chat{ID: 24680, Type: "private"},
		Text: "/start",
	}
	if err := a.processTelegram(context.Background(), telegram.Update{ID: 1, Message: &message}); err != nil {
		t.Fatal(err)
	}
	actions := []string{
		"club:join",
		"plan:90d",
		"nav:plan",
		"plan:90d",
		"pay:region:ru",
		"nav:region",
	}
	for index, action := range actions {
		payload := fmt.Sprintf(
			`{"update_id":%d,"callback_query":{"id":"callback-%d","from":{"id":24680},"message":{"message_id":1,"chat":{"id":24680,"type":"private"}},"data":%q}}`,
			index+2,
			index+2,
			action,
		)
		var update telegram.Update
		if err := json.Unmarshal([]byte(payload), &update); err != nil {
			t.Fatal(err)
		}
		if err := a.processTelegram(context.Background(), update); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(texts[3], "Выбери срок доступа") {
		t.Fatalf("region back skipped the plan screen: %q", texts[3])
	}
	if !strings.Contains(texts[len(texts)-1], "СПОСОБ ОПЛАТЫ") {
		t.Fatalf("payment method back skipped the region screen: %q", texts[len(texts)-1])
	}
	var periodicity string
	if err := a.DB.QueryRow(
		context.Background(),
		"SELECT selected_periodicity FROM users WHERE telegram_user_id=$1",
		int64(24680),
	).Scan(&periodicity); err != nil {
		t.Fatal(err)
	}
	if periodicity != "PERIOD_90_DAYS" {
		t.Fatalf("selected periodicity was lost: %q", periodicity)
	}
}

func TestPlanSelectionAndEmailFlow(t *testing.T) {
	a := testApp(t)
	type telegramCall struct {
		method string
		body   map[string]any
	}
	calls := []telegramCall{}
	telegramServer(t, a, func(method string, body map[string]any) (int, any) {
		calls = append(calls, telegramCall{method: method, body: body})
		result := any(true)
		if method == "sendMessage" {
			result = map[string]any{"message_id": 777}
		}
		return 200, map[string]any{"ok": true, "result": result}
	})
	lavaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request lava.CreateRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Email != "buyer@example.com" || request.Periodicity != "PERIOD_180_DAYS" || request.Provider != "PAYPAL" {
			t.Errorf("unexpected request: %#v", request)
		}
		respond(w, http.StatusCreated, lava.Checkout{ID: "email-flow", PaymentURL: "https://pay.example/email-flow"})
	}))
	defer lavaServer.Close()
	a.Lava.BaseURL = lavaServer.URL
	message := telegram.Message{ID: 1, From: telegram.User{ID: 987654321, FirstName: "Test"}, Chat: telegram.Chat{ID: 987654321, Type: "private"}, Text: "/start"}
	if err := a.processTelegram(context.Background(), telegram.Update{ID: 1, Message: &message}); err != nil {
		t.Fatal(err)
	}
	for index, action := range []string{"plan:180d", "pay:region:foreign", "pay:foreign:paypal"} {
		payload := fmt.Sprintf(`{"update_id":%d,"callback_query":{"id":"callback-%d","from":{"id":987654321},"message":{"message_id":1,"chat":{"id":987654321,"type":"private"}},"data":%q}}`, index+2, index+2, action)
		var update telegram.Update
		if err := json.Unmarshal([]byte(payload), &update); err != nil {
			t.Fatal(err)
		}
		if err := a.processTelegram(context.Background(), update); err != nil {
			t.Fatal(err)
		}
	}
	var attempts int
	if err := a.DB.QueryRow(context.Background(), "SELECT count(*) FROM checkout_attempts").Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("checkout created before email: attempts=%d err=%v", attempts, err)
	}
	emailMessage := telegram.Message{ID: 2, From: telegram.User{ID: 987654321, FirstName: "Test"}, Chat: telegram.Chat{ID: 987654321, Type: "private"}, Text: "buyer@example.com"}
	if err := a.processTelegram(context.Background(), telegram.Update{ID: 5, Message: &emailMessage}); err != nil {
		t.Fatal(err)
	}
	var email, periodicity, attemptPeriod string
	if err := a.DB.QueryRow(context.Background(), "SELECT email,selected_periodicity FROM users WHERE telegram_user_id=$1", int64(987654321)).Scan(&email, &periodicity); err != nil {
		t.Fatal(err)
	}
	if err := a.DB.QueryRow(context.Background(), "SELECT periodicity FROM checkout_attempts WHERE user_id=(SELECT id FROM users WHERE telegram_user_id=$1)", int64(987654321)).Scan(&attemptPeriod); err != nil {
		t.Fatal(err)
	}
	if email != "buyer@example.com" || periodicity != "PERIOD_180_DAYS" || attemptPeriod != "PERIOD_180_DAYS" {
		t.Fatalf("email=%q periodicity=%q attempt=%q", email, periodicity, attemptPeriod)
	}
	if len(calls) < 3 {
		t.Fatalf("too few Telegram calls: %#v", calls)
	}
	last := calls[len(calls)-3:]
	if last[0].method != "sendMessage" || last[1].method != "deleteMessage" || last[2].method != "sendMessage" {
		t.Fatalf("unexpected payment message lifecycle: %#v", last)
	}
	if last[0].body["text"] != "⏳ Создаю ссылку на оплату…" || last[1].body["message_id"] != float64(777) {
		t.Fatalf("temporary payment message was not deleted: %#v", last)
	}
}

func TestMessageMediaUploadSendAndCleanup(t *testing.T) {
	a := testApp(t)
	var startID int64
	if err := a.DB.QueryRow(context.Background(), "SELECT id FROM bot_messages WHERE key='start'").Scan(&startID); err != nil {
		t.Fatal(err)
	}
	upload := func(mediaType, mediaPosition string) int64 {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		if err := writer.WriteField("media_type", mediaType); err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteField("media_position", mediaPosition); err != nil {
			t.Fatal(err)
		}
		extension := ".mp4"
		mediaData := []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0, 'i', 's', 'o', 'm', 'm', 'p', '4', '2'}
		if mediaType == "photo" {
			extension = ".jpg"
			mediaData = []byte{0xff, 0xd8, 0xff, 0xe0, 0, 16, 'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 1, 0, 1, 0, 0, 0xff, 0xd9}
		}
		part, err := writer.CreateFormFile("file", mediaType+extension)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write(mediaData); err != nil {
			t.Fatal(err)
		}
		if err = writer.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/messages/%d/media", startID), &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		response := httptest.NewRecorder()
		if err = a.messageMedia(response, req, startID, 0); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK {
			t.Fatalf("upload status=%d body=%s", response.Code, response.Body.String())
		}
		var uploaded struct {
			ID int64 `json:"id"`
		}
		if err = json.Unmarshal(response.Body.Bytes(), &uploaded); err != nil || uploaded.ID == 0 {
			t.Fatalf("invalid upload response: %s err=%v", response.Body.String(), err)
		}
		return uploaded.ID
	}

	var methods []string
	var mediaMessageSequence int64 = 699
	var lastControlMessageID int64
	var richTypes []string
	var richHTML, controlKeyboard string
	richRejectOnce := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		methods = append(methods, method)
		switch method {
		case "sendRichMessage":
			var rich struct {
				HTML  string `json:"html"`
				Media []struct {
					ID    string `json:"id"`
					Media struct {
						Type string `json:"type"`
					} `json:"media"`
				} `json:"media"`
			}
			if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
				}
				if err := json.Unmarshal([]byte(r.FormValue("rich_message")), &rich); err != nil {
					t.Error(err)
				}
				controlKeyboard = r.FormValue("reply_markup")
			} else {
				var payload struct {
					Rich     json.RawMessage `json:"rich_message"`
					Keyboard json.RawMessage `json:"reply_markup"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if err := json.Unmarshal(payload.Rich, &rich); err != nil {
					t.Error(err)
				}
				controlKeyboard = string(payload.Keyboard)
			}
			richTypes = richTypes[:0]
			richHTML = rich.HTML
			blocks := make([]map[string]any, 0, len(rich.Media))
			for _, item := range rich.Media {
				richTypes = append(richTypes, item.Media.Type)
				block := map[string]any{"type": item.Media.Type}
				if item.Media.Type == "photo" {
					block["photo"] = []map[string]any{{"file_id": "photo-file-id"}}
				} else {
					block["video"] = map[string]any{"file_id": "video-file-id"}
				}
				blocks = append(blocks, block)
			}
			if richRejectOnce {
				richRejectOnce = false
				respond(w, http.StatusBadRequest, map[string]any{"ok": false, "error_code": 400, "description": "Bad Request: rich message is not supported"})
				return
			}
			lastControlMessageID = atomic.AddInt64(&mediaMessageSequence, 1)
			respond(w, http.StatusOK, map[string]any{"ok": true, "result": map[string]any{"message_id": lastControlMessageID, "rich_message": map[string]any{"blocks": []map[string]any{{"type": "collage", "blocks": blocks}}}}})
		case "sendVideo", "sendVideoNote", "sendPhoto":
			field := "video"
			var mediaResult any = map[string]any{"file_id": "video-file-id"}
			if method == "sendVideoNote" {
				field = "video_note"
				mediaResult = map[string]any{"file_id": "note-file-id"}
			} else if method == "sendPhoto" {
				field = "photo"
				mediaResult = []map[string]any{{"file_id": "photo-file-id"}}
			}
			if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
				}
				file, _, err := r.FormFile(field)
				if err != nil {
					t.Error(err)
				} else {
					_ = file.Close()
				}
			} else {
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
			}
			respond(w, http.StatusOK, map[string]any{"ok": true, "result": map[string]any{"message_id": atomic.AddInt64(&mediaMessageSequence, 1), field: mediaResult}})
		case "sendMessage":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			keyboard, err := json.Marshal(payload["reply_markup"])
			if err != nil {
				t.Error(err)
			}
			controlKeyboard = string(keyboard)
			lastControlMessageID = 701
			respond(w, http.StatusOK, map[string]any{"ok": true, "result": map[string]any{"message_id": 701}})
		default:
			respond(w, http.StatusOK, map[string]any{"ok": true, "result": true})
		}
	}))
	t.Cleanup(srv.Close)
	a.Telegram = telegram.New("test")
	a.Telegram.BaseURL = srv.URL

	videoID := upload("video", "before")
	secondVideoID := upload("video", "before")
	photoID := upload("photo", "before")
	beforeNoteID := upload("video_note", "before")
	orderBody, err := json.Marshal(map[string]any{"order": []int64{photoID, secondVideoID, videoID}})
	if err != nil {
		t.Fatal(err)
	}
	orderRequest := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/messages/%d/media", startID), bytes.NewReader(orderBody))
	orderResponse := httptest.NewRecorder()
	if err = a.messageMedia(orderResponse, orderRequest, startID, 0); err != nil || orderResponse.Code != http.StatusOK {
		t.Fatalf("reorder status=%d body=%s err=%v", orderResponse.Code, orderResponse.Body.String(), err)
	}
	message := telegram.Message{ID: 1, From: telegram.User{ID: 70701, FirstName: "Video"}, Chat: telegram.Chat{ID: 70701, Type: "private"}, Text: "/start"}
	if err := a.processTelegram(context.Background(), telegram.Update{ID: 1, Message: &message}); err != nil {
		t.Fatal(err)
	}
	if len(methods) < 2 || methods[0] != "sendVideoNote" || methods[1] != "sendRichMessage" {
		t.Fatalf("before-note must precede the complete album and text: %#v", methods)
	}
	var fileID string
	if err := a.DB.QueryRow(context.Background(), "SELECT telegram_file_id FROM bot_message_media WHERE id=$1", videoID).Scan(&fileID); err != nil || fileID != "video-file-id" {
		t.Fatalf("video file id=%q err=%v", fileID, err)
	}
	var sentMedia int
	if err := a.DB.QueryRow(context.Background(), "SELECT count(*) FROM delivered_message_media").Scan(&sentMedia); err != nil || sentMedia != 2 {
		t.Fatalf("multiple media deliveries=%d err=%v", sentMedia, err)
	}
	if slices.Contains(methods, "sendMediaGroup") || strings.Join(richTypes, ",") != "photo,video,video" {
		t.Fatalf("rich collage order is wrong: methods=%#v types=%#v", methods, richTypes)
	}
	if !strings.Contains(richHTML, "<tg-collage>") || !strings.Contains(richHTML, "<p>") || !strings.Contains(controlKeyboard, "callback_data") {
		t.Fatalf("media, text and controls were not combined: html=%q keyboard=%q", richHTML, controlKeyboard)
	}
	mediaRefreshStart := len(methods)
	refreshCallback := fmt.Sprintf(`{"update_id":2,"callback_query":{"id":"media-refresh","from":{"id":70701},"message":{"message_id":%d,"chat":{"id":70701,"type":"private"}},"data":"nav:start"}}`, lastControlMessageID)
	var refreshUpdate telegram.Update
	if err := json.Unmarshal([]byte(refreshCallback), &refreshUpdate); err != nil {
		t.Fatal(err)
	}
	if err := a.processTelegram(context.Background(), refreshUpdate); err != nil {
		t.Fatal(err)
	}
	refreshMethods := methods[mediaRefreshStart:]
	if len(refreshMethods) < 4 || refreshMethods[len(refreshMethods)-2] != "sendVideoNote" || refreshMethods[len(refreshMethods)-1] != "sendRichMessage" {
		t.Fatalf("media callback must recreate content followed by its controls: %#v", refreshMethods)
	}
	if !strings.Contains(controlKeyboard, "callback_data") {
		t.Fatalf("media callback lost its inline keyboard: %s", controlKeyboard)
	}
	callback := fmt.Sprintf(`{"update_id":2,"callback_query":{"id":"media-cleanup","from":{"id":70701},"message":{"message_id":%d,"chat":{"id":70701,"type":"private"}},"data":"club:join"}}`, lastControlMessageID)
	var update telegram.Update
	if err := json.Unmarshal([]byte(callback), &update); err != nil {
		t.Fatal(err)
	}
	if err := a.processTelegram(context.Background(), update); err != nil {
		t.Fatal(err)
	}
	var deliveries int
	if err := a.DB.QueryRow(context.Background(), "SELECT count(*) FROM delivered_message_media").Scan(&deliveries); err != nil || deliveries != 0 {
		t.Fatalf("media delivery was not cleaned up: count=%d err=%v", deliveries, err)
	}
	if !slices.Contains(methods, "sendRichMessage") || strings.Count(strings.Join(methods, ","), "deleteMessage") < 3 {
		t.Fatalf("unexpected methods: %#v", methods)
	}
	deleteBeforeNoteRequest := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/messages/%d/media/%d", startID, beforeNoteID), nil)
	deleteBeforeNoteResponse := httptest.NewRecorder()
	if err := a.messageMedia(deleteBeforeNoteResponse, deleteBeforeNoteRequest, startID, beforeNoteID); err != nil || deleteBeforeNoteResponse.Code != http.StatusOK {
		t.Fatalf("delete before-note status=%d body=%s err=%v", deleteBeforeNoteResponse.Code, deleteBeforeNoteResponse.Body.String(), err)
	}

	deleteVideoRequest := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/messages/%d/media/%d", startID, videoID), nil)
	deleteVideoResponse := httptest.NewRecorder()
	if err := a.messageMedia(deleteVideoResponse, deleteVideoRequest, startID, videoID); err != nil || deleteVideoResponse.Code != http.StatusOK {
		t.Fatalf("delete video status=%d body=%s err=%v", deleteVideoResponse.Code, deleteVideoResponse.Body.String(), err)
	}
	for _, mediaID := range []int64{secondVideoID, photoID} {
		request := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/messages/%d/media/%d", startID, mediaID), nil)
		response := httptest.NewRecorder()
		if err := a.messageMedia(response, request, startID, mediaID); err != nil || response.Code != http.StatusOK {
			t.Fatalf("delete media %d status=%d body=%s err=%v", mediaID, response.Code, response.Body.String(), err)
		}
	}
	noteID := upload("video_note", "after")
	noteMethodStart := len(methods)
	note := telegram.Message{ID: 1, From: telegram.User{ID: 70702, FirstName: "Note"}, Chat: telegram.Chat{ID: 70702, Type: "private"}, Text: "/start"}
	if err := a.processTelegram(context.Background(), telegram.Update{ID: 3, Message: &note}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(methods, "sendVideoNote") {
		t.Fatalf("video note was not sent: %#v", methods)
	}
	noteMethods := methods[noteMethodStart:]
	if len(noteMethods) < 2 || noteMethods[0] != "sendMessage" || noteMethods[1] != "sendVideoNote" {
		t.Fatalf("video note order is wrong: %#v", noteMethods)
	}
	var notePosition string
	if err := a.DB.QueryRow(context.Background(), "SELECT media_position FROM bot_message_media WHERE id=$1", noteID).Scan(&notePosition); err != nil || notePosition != "after" {
		t.Fatalf("video note position=%q err=%v", notePosition, err)
	}
	photoAfterNoteID := upload("photo", "before")
	combinedMethodStart := len(methods)
	combined := telegram.Message{ID: 2, From: telegram.User{ID: 70703, FirstName: "Combined"}, Chat: telegram.Chat{ID: 70703, Type: "private"}, Text: "/start"}
	if err := a.processTelegram(context.Background(), telegram.Update{ID: 4, Message: &combined}); err != nil {
		t.Fatal(err)
	}
	combinedMethods := methods[combinedMethodStart:]
	if len(combinedMethods) != 2 || combinedMethods[0] != "sendRichMessage" || combinedMethods[1] != "sendVideoNote" {
		t.Fatalf("single photo, text and buttons must precede after-note: %#v", combinedMethods)
	}
	if len(richTypes) != 1 || richTypes[0] != "photo" || !strings.Contains(controlKeyboard, "callback_data") {
		t.Fatalf("single photo and controls were not combined: types=%#v keyboard=%q", richTypes, controlKeyboard)
	}
	richRejectOnce = true
	fallbackMethodStart := len(methods)
	fallback := telegram.Message{ID: 3, From: telegram.User{ID: 70704, FirstName: "Fallback"}, Chat: telegram.Chat{ID: 70704, Type: "private"}, Text: "/start"}
	if err := a.processTelegram(context.Background(), telegram.Update{ID: 5, Message: &fallback}); err != nil {
		t.Fatal(err)
	}
	fallbackMethods := methods[fallbackMethodStart:]
	if strings.Join(fallbackMethods, ",") != "sendRichMessage,sendPhoto,editMessageReplyMarkup,sendVideoNote" {
		t.Fatalf("unsupported rich message must fall back without retrying the whole flow: %#v", fallbackMethods)
	}
	photoAfterNoteRequest := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/messages/%d/media/%d", startID, photoAfterNoteID), nil)
	photoAfterNoteResponse := httptest.NewRecorder()
	if err := a.messageMedia(photoAfterNoteResponse, photoAfterNoteRequest, startID, photoAfterNoteID); err != nil || photoAfterNoteResponse.Code != http.StatusOK {
		t.Fatalf("delete photo status=%d body=%s err=%v", photoAfterNoteResponse.Code, photoAfterNoteResponse.Body.String(), err)
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/messages/%d/media/%d", startID, noteID), nil)
	deleteResponse := httptest.NewRecorder()
	if err := a.messageMedia(deleteResponse, deleteRequest, startID, noteID); err != nil {
		t.Fatal(err)
	}
	var deleted struct {
		OK      bool `json:"ok"`
		Version int  `json:"version"`
	}
	if err := json.Unmarshal(deleteResponse.Body.Bytes(), &deleted); err != nil {
		t.Fatal(err)
	}
	if deleteResponse.Code != http.StatusOK || !deleted.OK || deleted.Version == 0 {
		t.Fatalf("delete response status=%d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
	var mediaCount int
	if err := a.DB.QueryRow(context.Background(), "SELECT count(*) FROM bot_message_media WHERE message_id=$1", startID).Scan(&mediaCount); err != nil || mediaCount != 0 {
		t.Fatalf("media was not deleted: count=%d err=%v", mediaCount, err)
	}
}

func TestActiveSubscriberCannotCreateSecondCheckout(t *testing.T) {
	a := testApp(t)
	uid := seedUser(t, a)
	seedSubscription(t, a, uid)
	execTest(t, a, "UPDATE subscriptions SET expires_at=now()+interval '1 day'")
	key, err := a.checkout(context.Background(), uid, "pay:ru:card")
	if err != nil || key != "subscription_status_active" {
		t.Fatalf("key=%q err=%v", key, err)
	}
	var attempts int
	if err := a.DB.QueryRow(context.Background(), "SELECT count(*) FROM checkout_attempts WHERE user_id=$1", uid).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}

func TestJoinRequestRequiresOwnedActiveSubscription(t *testing.T) {
	a := testApp(t)
	uid := seedUser(t, a)
	seedSubscription(t, a, uid)
	var methods []string
	telegramServer(t, a, func(method string, _ map[string]any) (int, any) {
		methods = append(methods, method)
		return 200, map[string]any{"ok": true, "result": true}
	})
	if e := a.join(context.Background(), 12345, a.Config.ChannelID); e != nil {
		t.Fatal(e)
	}
	execTest(t, a, "UPDATE subscriptions SET expires_at=now()+interval '1 day'")
	if e := a.join(context.Background(), 99999, a.Config.ChannelID); e != nil {
		t.Fatal(e)
	}
	if e := a.join(context.Background(), 12345, a.Config.ChannelID); e != nil {
		t.Fatal(e)
	}
	if strings.Join(methods, ",") != "declineChatJoinRequest,declineChatJoinRequest,approveChatJoinRequest" {
		t.Fatal(methods)
	}
}

func TestCancellationStatusWithoutTimestampStaysCancelled(t *testing.T) {
	a := testApp(t)
	uid := seedUser(t, a)
	seedSubscription(t, a, uid)
	s := paidSnapshot()
	s.SubscriptionStatus = "CANCELLED"
	set := snapshotServer(t, a, s)
	if e := a.SyncSubscription(context.Background(), "root"); e != nil {
		t.Fatal(e)
	}
	s.SubscriptionStatus = "ACTIVE"
	set(s)
	if e := a.SyncSubscription(context.Background(), "root"); e != nil {
		t.Fatal(e)
	}
	status, renew, _ := state(t, a, uid)
	if status != "active" || renew {
		t.Fatalf("lost cancellation without timestamp: %s %v", status, renew)
	}
}

func TestExpiryChecksProviderBeforeRemoving(t *testing.T) {
	for _, scenario := range []string{"renewed", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			a := testApp(t)
			uid := seedUser(t, a)
			seedSubscription(t, a, uid)
			execTest(t, a, "UPDATE subscriptions SET expires_at=now()-interval '1 minute'")
			execTest(t, a, "INSERT INTO channel_access(user_id,state) VALUES($1,'member')", uid)
			var telegramCalls int
			telegramServer(t, a, func(method string, _ map[string]any) (int, any) {
				telegramCalls++
				return 200, map[string]any{"ok": true, "result": true}
			})
			if scenario == "renewed" {
				snapshotServer(t, a, paidSnapshot())
			} else {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "unavailable", 503) }))
				defer srv.Close()
				a.Lava.BaseURL = srv.URL
			}
			err := a.removeMember(context.Background(), uid)
			if scenario == "renewed" && err != nil {
				t.Fatal(err)
			}
			if scenario == "unavailable" && err == nil {
				t.Fatal("expected provider failure")
			}
			if telegramCalls != 0 {
				t.Fatal("removed user without verifying expiry")
			}
			var access string
			if err := a.DB.QueryRow(context.Background(), "SELECT state FROM channel_access WHERE user_id=$1", uid).Scan(&access); err != nil || access != "member" {
				t.Fatalf("unexpected channel state %s %v", access, err)
			}
		})
	}
}
func TestCheckoutInvalidFlowDoesNotReserveAttempt(t *testing.T) {
	a := testApp(t)
	uid := seedUser(t, a)
	if _, err := a.checkout(context.Background(), uid, "unsupported"); err == nil {
		t.Fatal("invalid flow accepted")
	}
	var count int
	if err := a.DB.QueryRow(context.Background(), "SELECT count(*) FROM checkout_attempts").Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid flow reserved a checkout: %d %v", count, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestCheckoutSavesProviderResponseAfterContextCancellation(t *testing.T) {
	a := testApp(t)
	uid := seedUser(t, a)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.Lava.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		cancel()
		return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"root","paymentUrl":"https://pay.example/checkout"}`)), Request: r}, nil
	})}
	key, err := a.checkout(ctx, uid, "pay:ru:card")
	if err != nil || key != "payment_created" {
		t.Fatalf("provider response lost: %s %v", key, err)
	}
	var state, id string
	if err = a.DB.QueryRow(context.Background(), "SELECT state,external_id FROM checkout_attempts WHERE user_id=$1", uid).Scan(&state, &id); err != nil || state != "ready" || id != "root" {
		t.Fatalf("checkout not persisted: %s %s %v", state, id, err)
	}
	var count int
	if err = a.DB.QueryRow(context.Background(), "SELECT count(*) FROM subscriptions WHERE user_id=$1 AND contract_id='root'", uid).Scan(&count); err != nil || count != 1 {
		t.Fatalf("subscription not persisted: %d %v", count, err)
	}
}
