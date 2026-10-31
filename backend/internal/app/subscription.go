package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gotsta1/tomas_blank_bot/backend/internal/lava"
	"github.com/jackc/pgx/v5"
)

var ErrPending = errors.New("subscription payment is pending")

// SyncSubscription always reads the provider after acquiring the user's lock.
// The webhook is a reconciliation trigger, never an instruction to add days.
func (a *App) SyncSubscription(ctx context.Context, contract string) error {
	tx, e := a.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var uid, sid int64
	e = tx.QueryRow(ctx, "SELECT user_id,id FROM subscriptions WHERE contract_id=$1", contract).Scan(&uid, &sid)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", uid); e != nil {
		return e
	}
	snapshot, e := a.Lava.Subscription(ctx, contract)
	if e != nil {
		return e
	}
	if snapshot.ID != contract {
		return errors.New("provider subscription identity mismatch")
	}
	var payments []lava.Payment
	if snapshot.Status == "COMPLETED" {
		payments = append(payments, lava.Payment{ID: contract, DateTime: snapshot.DateTime, Status: snapshot.Status, Amount: snapshot.Receipt.Amount, Currency: snapshot.Receipt.Currency})
	}
	for _, p := range snapshot.RecurrentPayments {
		if p.Status == "COMPLETED" {
			payments = append(payments, p)
		}
	}
	var alreadyPaid bool
	if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM payments WHERE subscription_id=$1)", sid).Scan(&alreadyPaid); e != nil {
		return e
	}
	if len(payments) == 0 && !alreadyPaid {
		return ErrPending
	}
	expires, e := subscriptionExpiry(snapshot)
	if e != nil {
		return e
	}
	if snapshot.TerminatedAt != nil && snapshot.TerminatedAt.Before(expires) {
		expires = *snapshot.TerminatedAt
	}
	newPayment := false
	for _, p := range payments {
		if p.ID == "" || p.DateTime.IsZero() || p.Amount.String() == "" || (p.Currency != "RUB" && p.Currency != "USD" && p.Currency != "EUR") {
			return errors.New("incomplete payment from provider")
		}
		res, err := tx.Exec(ctx, `INSERT INTO payments(external_id,subscription_id,amount,currency,paid_at,payment_type,raw_payload) VALUES($1,$2,$3::numeric,$4,$5,$6,$7) ON CONFLICT(external_id) DO NOTHING`, p.ID, sid, p.Amount.String(), p.Currency, p.DateTime, paymentType(p.ID, contract), marshal(p))
		if err != nil {
			return err
		}
		newPayment = newPayment || res.RowsAffected() > 0
		var owner int64
		if err = tx.QueryRow(ctx, "SELECT subscription_id FROM payments WHERE external_id=$1", p.ID).Scan(&owner); err != nil {
			return err
		}
		if owner != sid {
			return errors.New("payment ownership conflict")
		}
	}
	// Cancellation remains sticky for this contract. A new purchase gets a new contract.
	_, e = tx.Exec(ctx, `UPDATE subscriptions SET provider_status=$2,auto_renew=($2 IN ('ACTIVE','FAILED') AND cancelled_at IS NULL AND $4::timestamptz IS NULL AND $5::timestamptz IS NULL AND terminated_at IS NULL), started_at=COALESCE(started_at,$6), expires_at=CASE WHEN COALESCE($5::timestamptz,terminated_at) IS NOT NULL THEN LEAST($3,COALESCE($5::timestamptz,terminated_at)) ELSE GREATEST(expires_at,$3) END,cancelled_at=COALESCE(cancelled_at,$4,CASE WHEN $2='CANCELLED' THEN now() END),terminated_at=COALESCE($5,terminated_at),synced_at=now() WHERE id=$1`, sid, snapshot.SubscriptionStatus, expires, snapshot.CancelledAt, snapshot.TerminatedAt, snapshot.DateTime)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "UPDATE checkout_attempts SET state='completed' WHERE external_id=$1", contract); e != nil {
		return e
	}
	if expires.After(time.Now()) {
		if _, e = tx.Exec(ctx, "DELETE FROM broadcast_user_state WHERE user_id=$1 AND campaign_id IN (SELECT id FROM broadcast_campaigns WHERE audience_status IN ('new','expired'))", uid); e != nil {
			return e
		}
	}
	if newPayment && expires.After(time.Now()) {
		_, e = tx.Exec(ctx, "INSERT INTO outbox_jobs(user_id,message_key,dedup_key) VALUES($1,'payment_success',$2) ON CONFLICT DO NOTHING", uid, fmt.Sprintf("payment:%d:%s", sid, expires.UTC().Format(time.RFC3339Nano)))
		if e != nil {
			return e
		}
	}
	if _, e = tx.Exec(ctx, "INSERT INTO audit_log(action,entity_id) VALUES('subscription.sync',$1)", contract); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func subscriptionExpiry(snapshot lava.Subscription) (time.Time, error) {
	if snapshot.ExpiredAt != nil && !snapshot.ExpiredAt.IsZero() {
		return *snapshot.ExpiredAt, nil
	}
	amount, err := snapshot.Receipt.Amount.Float64()
	if err != nil || amount != 0 || snapshot.Status != "COMPLETED" || snapshot.SubscriptionStatus != "ACTIVE" || snapshot.DateTime.IsZero() {
		return time.Time{}, errors.New("provider has not supplied subscription expiry")
	}
	switch strings.ToUpper(snapshot.Periodicity) {
	case "MONTHLY":
		return snapshot.DateTime.AddDate(0, 1, 0), nil
	case "PERIOD_90", "PERIOD_90_DAYS":
		return snapshot.DateTime.Add(90 * 24 * time.Hour), nil
	case "PERIOD_180", "PERIOD_180_DAYS":
		return snapshot.DateTime.Add(180 * 24 * time.Hour), nil
	case "PERIOD_YEAR":
		return snapshot.DateTime.AddDate(1, 0, 0), nil
	default:
		return time.Time{}, errors.New("provider has not supplied subscription expiry")
	}
}

func paymentType(id, parent string) string {
	if id == parent {
		return "first"
	}
	return "recurring"
}

func periodicityFromAction(action string) (string, bool) {
	switch action {
	case "plan:monthly":
		return "MONTHLY", true
	case "plan:90d":
		return "PERIOD_90_DAYS", true
	case "plan:180d":
		return "PERIOD_180_DAYS", true
	case "plan:year":
		return "PERIOD_YEAR", true
	default:
		return "", false
	}
}

func validPeriodicity(periodicity string) bool {
	switch periodicity {
	case "MONTHLY", "PERIOD_90_DAYS", "PERIOD_180_DAYS", "PERIOD_YEAR":
		return true
	default:
		return false
	}
}

func periodicityLabel(periodicity string) string {
	switch periodicity {
	case "MONTHLY":
		return "1 месяц"
	case "PERIOD_90_DAYS":
		return "3 месяца"
	case "PERIOD_180_DAYS":
		return "6 месяцев"
	case "PERIOD_YEAR":
		return "12 месяцев"
	default:
		return periodicity
	}
}

func (a *App) processLava(ctx context.Context, event lava.Event) error {
	if event.EnvelopeType != "" {
		a.Log.Warn("financial event requires review", "event_type", event.EnvelopeType, "event_id", event.EventID)
		return errors.New("refund or chargeback requires operator review")
	}
	switch event.Type {
	case "payment.success", "payment.failed", "subscription.recurring.payment.success", "subscription.recurring.payment.failed", "subscription.cancelled":
	default:
		a.Log.Warn("ignored unknown lava event", "event_type", event.Type)
		return nil
	}
	root := event.ParentID
	if root == "" {
		root = event.ContractID
	}
	if root == "" {
		return errors.New("missing contract id")
	}
	var known bool
	if e := a.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM subscriptions WHERE contract_id=$1)", root).Scan(&known); e != nil {
		return e
	}
	if !known { // Cancellation may name a renewal contract; resolve only via the provider.
		invoice, e := a.Lava.Invoice(ctx, root)
		if e != nil {
			return e
		}
		if invoice.ParentInvoice != nil {
			root = invoice.ParentInvoice.ID
		}
	}
	e := a.SyncSubscription(ctx, root)
	if errors.Is(e, ErrPending) && (event.Type == "payment.failed" || event.Type == "subscription.recurring.payment.failed") {
		return nil
	}
	return e
}
func (a *App) cancelSubscription(ctx context.Context, uid int64) error {
	tx, e := a.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", uid); e != nil {
		return e
	}
	rows, e := tx.Query(ctx, "SELECT contract_id,email FROM subscriptions WHERE user_id=$1 AND auto_renew AND expires_at>now()", uid)
	if e != nil {
		return e
	}
	type sub struct{ id, email string }
	items := []sub{}
	for rows.Next() {
		var s sub
		if e = rows.Scan(&s.id, &s.email); e != nil {
			rows.Close()
			return e
		}
		items = append(items, s)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, s := range items {
		// Lava stores the checkout email in lowercase and compares it when
		// cancelling. Preserve the address, but normalize its case for the API.
		if e = a.Lava.Cancel(ctx, s.id, strings.ToLower(s.email)); e != nil { // A previous request may have succeeded before its response was lost.
			current, err := a.Lava.Subscription(ctx, s.id)
			if err != nil || current.CancelledAt == nil {
				return e
			}
		}
		if _, e = tx.Exec(ctx, "UPDATE subscriptions SET auto_renew=false,cancelled_at=COALESCE(cancelled_at,now()),provider_status='CANCELLED' WHERE contract_id=$1", s.id); e != nil {
			return e
		}
	}
	if _, e = tx.Exec(ctx, "INSERT INTO audit_log(action,entity_id) VALUES('subscription.cancel',$1)", strconv.FormatInt(uid, 10)); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (a *App) checkout(ctx context.Context, uid int64, flow string) (string, error) {
	currency := "RUB"
	provider := "SMART_GLOCAL"
	switch flow {
	case "pay:ru:card", "pay:ru:sbp":
	case "pay:foreign:card", "pay:foreign:applepay":
		currency = a.Config.ForeignCurrency
		provider = ""
	case "pay:foreign:paypal":
		currency = a.Config.ForeignCurrency
		provider = "PAYPAL"
	default:
		return "", errors.New("invalid checkout flow")
	}
	if a.Config.OfferID == "" || a.Config.LavaKey == "" {
		return "", errors.New("lava offer not configured")
	}
	tx, e := a.DB.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	var email, periodicity *string
	if e = tx.QueryRow(ctx, "SELECT email,selected_periodicity FROM users WHERE id=$1 FOR UPDATE", uid).Scan(&email, &periodicity); e != nil {
		return "", e
	}
	if periodicity == nil || !validPeriodicity(*periodicity) {
		return "choose_plan", nil
	}
	if email == nil {
		return "email_request", nil
	}
	var active bool
	if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM subscriptions WHERE user_id=$1 AND expires_at>now())", uid).Scan(&active); e != nil {
		return "", e
	}
	if active {
		return "subscription_status_active", nil
	}
	var existing, existingContract string
	var existingCreated time.Time
	e = tx.QueryRow(ctx, `SELECT state,COALESCE(external_id,''),created_at
		FROM checkout_attempts WHERE user_id=$1 AND state IN ('creating','ready','unknown')`, uid).Scan(&existing, &existingContract, &existingCreated)
	if e == nil {
		if existing == "ready" {
			// A fresh invoice is reused. For an older one, ask Lava whether it
			// has definitively failed before permitting a replacement. Never
			// expire a payable invoice based on local time alone.
			if time.Since(existingCreated) < 15*time.Minute {
				return "payment_created", nil
			}
			invoice, lookupErr := a.Lava.Invoice(ctx, existingContract)
			if lookupErr != nil {
				a.Log.Warn("existing invoice status unavailable", "contract_id", existingContract, "error", lookupErr)
				return "payment_created", nil
			}
			if invoice.ID != existingContract {
				return "payment_uncertain", nil
			}
			switch strings.ToUpper(invoice.Status) {
			case "NEW", "IN_PROGRESS":
				return "payment_created", nil
			case "COMPLETED":
				return "payment_pending", nil
			case "FAILED", "CANCELLED":
				if _, e = tx.Exec(ctx, `UPDATE checkout_attempts SET state='failed'
					WHERE user_id=$1 AND external_id=$2 AND state='ready'`, uid, existingContract); e != nil {
					return "", e
				}
			default:
				return "payment_uncertain", nil
			}
		} else {
			return "payment_uncertain", nil
		}
	}
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return "", e
	}
	var id int64
	e = tx.QueryRow(ctx, "INSERT INTO checkout_attempts(user_id,flow,state,email,offer_id,periodicity) VALUES($1,$2,'creating',$3,$4,$5) RETURNING id", uid, flow, *email, a.Config.OfferID, *periodicity).Scan(&id)
	if e != nil {
		return "", e
	}
	if e = tx.Commit(ctx); e != nil {
		return "", e
	}
	invoice, e := a.Lava.Create(ctx, lava.CreateRequest{Email: *email, OfferID: a.Config.OfferID, Currency: currency, Periodicity: *periodicity, Provider: provider})
	// Save the provider's result even if the caller was cancelled after Lava
	// created the contract. This bounded cleanup must not create another invoice.
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if e != nil {
		var providerErr *lava.HTTPError
		if errors.As(e, &providerErr) && providerErr.StatusCode >= 400 && providerErr.StatusCode < 500 {
			_, _ = a.DB.Exec(persistCtx, "UPDATE checkout_attempts SET state='failed' WHERE id=$1", id)
			a.Log.Warn("invoice creation rejected", "checkout_id", id, "status", providerErr.StatusCode, "reason", providerErr.Reason)
			reason := strings.ToLower(providerErr.Reason)
			if strings.Contains(reason, "incorrect email") || strings.Contains(reason, "email format") {
				if _, updateErr := a.DB.Exec(
					persistCtx,
					"UPDATE users SET email=NULL,pending_flow=$2 WHERE id=$1",
					uid,
					flow,
				); updateErr != nil {
					return "", updateErr
				}
				return "email_rejected", nil
			}
			return "payment_failed", nil
		}
		_, _ = a.DB.Exec(persistCtx, "UPDATE checkout_attempts SET state='unknown' WHERE id=$1", id)
		a.Log.Error("invoice creation uncertain", "checkout_id", id, "error", e)
		return "payment_uncertain", nil
	}
	tx, e = a.DB.Begin(persistCtx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(persistCtx)
	var priceAmount any
	priceCurrency := invoice.AmountTotal.Currency
	if invoice.AmountTotal.Amount != "" {
		priceAmount = invoice.AmountTotal.Amount.String()
		if priceCurrency == "" {
			priceCurrency = currency
		}
	}
	if _, e = tx.Exec(
		persistCtx,
		`UPDATE checkout_attempts
		 SET state='ready',external_id=$2,payment_url=$3,price_amount=$4::numeric,price_currency=NULLIF($5,'')
		 WHERE id=$1`,
		id,
		invoice.ID,
		invoice.PaymentURL,
		priceAmount,
		priceCurrency,
	); e != nil {
		return "", e
	}
	if _, e = tx.Exec(persistCtx, "INSERT INTO subscriptions(user_id,contract_id,offer_id,email,periodicity) VALUES($1,$2,$3,$4,$5)", uid, invoice.ID, a.Config.OfferID, *email, *periodicity); e != nil {
		return "", e
	}
	if e = tx.Commit(persistCtx); e != nil {
		return "", e
	}
	return "payment_created", nil
}
