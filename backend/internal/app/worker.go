package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gotsta1/tomas_blank_bot/backend/internal/lava"
	"github.com/gotsta1/tomas_blank_bot/backend/internal/telegram"
	"github.com/jackc/pgx/v5"
)

// RunWorker elects one leader using a PostgreSQL session lock. This intentionally
// bounds concurrency and avoids parallel Telegram operations for the same user.
func (a *App) RunWorker(ctx context.Context) {
	for ctx.Err() == nil {
		a.workerSession(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}
func (a *App) workerSession(ctx context.Context) {
	conn, e := a.DB.Acquire(ctx)
	if e != nil {
		a.Log.Error("worker database unavailable", "error", e)
		return
	}
	defer conn.Release()
	var leader bool
	if e = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(901121)").Scan(&leader); e != nil || !leader {
		return
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock(901121)")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastSchedule := time.Time{}
	a.Log.Info("worker leader started")
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Exit on lock connection loss rather than continuing without leadership.
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			e = conn.Ping(pingCtx)
			cancel()
			if e != nil {
				return
			}
			for i := 0; i < 20; i++ {
				did, err := a.nextEvent(ctx)
				if err != nil {
					a.Log.Error("event loop failed", "error", err)
					break
				}
				if !did {
					break
				}
			}
			if time.Since(lastSchedule) >= 30*time.Second {
				a.schedule(ctx)
				lastSchedule = time.Now()
			} else {
				// Dispatch due messages every second without rescanning audiences.
				op, cancel := context.WithTimeout(ctx, 60*time.Second)
				err := a.sendDueBroadcasts(op)
				cancel()
				if err != nil {
					a.Log.Error("broadcast dispatch failed", "error", err)
				}
			}
			op, cancel := context.WithTimeout(ctx, 60*time.Second)
			if err := a.processMailings(op); err != nil {
				a.Log.Error("mailing dispatch failed", "error", err)
			}
			cancel()
		}
	}
}
func (a *App) nextEvent(ctx context.Context) (bool, error) {
	var id int64
	var provider string
	var raw []byte
	var attempts int
	e := a.DB.QueryRow(ctx, "SELECT id,provider,payload,attempts FROM webhook_events WHERE processed_at IS NULL AND next_attempt_at<=now() ORDER BY id LIMIT 1").Scan(&id, &provider, &raw, &attempts)
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	operation, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if provider == "lava" {
		var event lava.Event
		e = json.Unmarshal(raw, &event)
		if e == nil {
			e = a.processLava(operation, event)
		}
	} else {
		var update telegram.Update
		e = json.Unmarshal(raw, &update)
		if e == nil {
			e = a.processTelegram(operation, update)
		}
	}
	if e != nil {
		a.Log.Error("event processing failed", "event_id", id, "provider", provider, "attempt", attempts+1, "error", e)
		_, err := a.DB.Exec(ctx, "UPDATE webhook_events SET attempts=attempts+1,last_error=$2,next_attempt_at=now()+$3::interval WHERE id=$1", id, e.Error(), retryDelay(e, attempts))
		return true, err
	}
	_, e = a.DB.Exec(ctx, "UPDATE webhook_events SET processed_at=now(),last_error=NULL WHERE id=$1", id)
	return true, e
}
func retryDelay(err error, attempt int) string {
	seconds := min(3600, 5*(1<<min(attempt, 9)))
	var api *telegram.APIError
	if errors.As(err, &api) {
		seconds = max(seconds, api.RetryAfter)
	}
	return fmt.Sprintf("%d seconds", seconds)
}
func (a *App) schedule(ctx context.Context) {
	for _, job := range []struct {
		name string
		run  func(context.Context) error
	}{{"reconcile", a.reconcile}, {"outbox", a.outbox}, {"expiration", a.expire}, {"broadcast", a.broadcast}} {
		op, cancel := context.WithTimeout(ctx, 60*time.Second)
		e := job.run(op)
		cancel()
		if e != nil {
			a.Log.Error("scheduled job failed", "job", job.name, "error", e)
		}
	}
	_, e := a.DB.Exec(ctx, "DELETE FROM admin_sessions WHERE expires_at<now()")
	if e != nil {
		a.Log.Error("session cleanup failed", "error", e)
	}
}
func (a *App) reconcile(ctx context.Context) error {
	rows, e := a.DB.Query(ctx, "SELECT contract_id FROM subscriptions WHERE synced_at<now()-interval '5 minutes' AND (expires_at IS NULL OR expires_at>now()-interval '7 days') ORDER BY synced_at LIMIT 10")
	if e != nil {
		return e
	}
	ids, e := pgx.CollectRows(rows, pgx.RowTo[string])
	if e != nil {
		return e
	}
	for _, id := range ids {
		e = a.SyncSubscription(ctx, id)
		if errors.Is(e, ErrPending) {
			_, e = a.DB.Exec(ctx, "UPDATE subscriptions SET synced_at=now() WHERE contract_id=$1", id)
		}
		if e != nil {
			a.Log.Error("subscription reconciliation failed", "contract_id", id, "error", e)
		}
	}
	return nil
}
func (a *App) outbox(ctx context.Context) error {
	rows, e := a.DB.Query(ctx, "SELECT id,user_id,message_key,attempts FROM outbox_jobs WHERE done_at IS NULL AND next_attempt_at<=now() ORDER BY id LIMIT 20")
	if e != nil {
		return e
	}
	type job struct {
		id, uid  int64
		key      string
		attempts int
	}
	items := []job{}
	for rows.Next() {
		var j job
		if e = rows.Scan(&j.id, &j.uid, &j.key, &j.attempts); e != nil {
			rows.Close()
			return e
		}
		items = append(items, j)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, j := range items {
		e = a.deliverOutbox(ctx, j.uid, j.key)
		if e == nil {
			_, e = a.DB.Exec(ctx, "UPDATE outbox_jobs SET done_at=now() WHERE id=$1", j.id)
		} else {
			_, e = a.DB.Exec(ctx, "UPDATE outbox_jobs SET attempts=attempts+1,last_error=$2,next_attempt_at=now()+$3::interval WHERE id=$1", j.id, e.Error(), retryDelay(e, j.attempts))
		}
		if e != nil {
			return e
		}
	}
	return nil
}
func (a *App) deliverOutbox(ctx context.Context, uid int64, key string) error {
	tx, e := a.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", uid); e != nil {
		return e
	}
	var active, blocked bool
	if e = tx.QueryRow(ctx, "SELECT status='active',blocked FROM user_status WHERE id=$1", uid).Scan(&active, &blocked); e != nil {
		return e
	}
	if !active || blocked {
		return nil
	}
	if e = a.sendTx(ctx, tx, uid, key, 0); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (a *App) expire(ctx context.Context) error {
	rows, e := a.DB.Query(ctx, `SELECT c.user_id FROM channel_access c JOIN user_status u ON u.id=c.user_id WHERE c.state='removing' OR (c.state='member' AND u.status<>'active') ORDER BY c.updated_at LIMIT 50`)
	if e != nil {
		return e
	}
	ids, e := pgx.CollectRows(rows, pgx.RowTo[int64])
	if e != nil {
		return e
	}
	for _, id := range ids {
		if e = a.removeMember(ctx, id); e != nil {
			a.Log.Error("channel removal failed", "user_id", id, "error", e)
		}
	}
	return nil
}
func (a *App) removeMember(ctx context.Context, uid int64) error {
	// Reconcile this particular user's contracts before a new removal. A batch
	// reconciliation elsewhere cannot guarantee the renewal webhook has arrived.
	var accessState string
	var activeNow bool
	if err := a.DB.QueryRow(ctx, "SELECT c.state,u.status='active' FROM channel_access c JOIN user_status u ON u.id=c.user_id WHERE c.user_id=$1", uid).Scan(&accessState, &activeNow); err != nil {
		return err
	}
	if accessState == "member" && !activeNow {
		if err := a.refreshUserSubscriptions(ctx, uid); err != nil {
			return fmt.Errorf("verify expiry before removal: %w", err)
		}
	}
	// Persist the removing stage before ban, allowing unban recovery after a crash.
	tx, e := a.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", uid); e != nil {
		return e
	}
	var active bool
	var telegramID int64
	var state string
	if e = tx.QueryRow(ctx, "SELECT status='active',telegram_user_id FROM user_status WHERE id=$1", uid).Scan(&active, &telegramID); e != nil {
		return e
	}
	if e = tx.QueryRow(ctx, "SELECT state FROM channel_access WHERE user_id=$1", uid).Scan(&state); e != nil {
		return e
	}
	if active && state != "removing" {
		return nil
	}
	if _, e = tx.Exec(ctx, "UPDATE channel_access SET state='removing',updated_at=now() WHERE user_id=$1", uid); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	tx, e = a.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", uid); e != nil {
		return e
	}
	if e = tx.QueryRow(ctx, "SELECT status='active' FROM user_status WHERE id=$1", uid).Scan(&active); e != nil {
		return e
	}
	payload := map[string]any{"chat_id": a.Config.ChannelID, "user_id": telegramID}
	if !active {
		if e = a.Telegram.Call(ctx, "banChatMember", payload, nil); e != nil {
			return e
		}
	}
	payload["only_if_banned"] = true
	if e = a.Telegram.Call(ctx, "unbanChatMember", payload, nil); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "UPDATE channel_access SET state='removed',updated_at=now() WHERE user_id=$1", uid); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (a *App) broadcast(ctx context.Context) error {
	// Each campaign tracks its own audience and position in the chain.
	_, e := a.DB.Exec(ctx, `DELETE FROM broadcast_user_state s USING broadcast_campaigns c,user_status u
		WHERE s.campaign_id=c.id AND s.user_id=u.id AND
		(NOT c.enabled OR c.mode<>'auto' OR u.blocked OR u.first_started_at IS NULL OR (c.audience_status<>'all' AND u.status<>c.audience_status))`)
	if e != nil {
		return e
	}
	campaignRows, e := a.DB.Query(ctx, "SELECT id,audience_status FROM broadcast_campaigns WHERE mode='auto' AND enabled ORDER BY id")
	if e != nil {
		return e
	}
	type campaign struct {
		id     int64
		status string
	}
	campaigns := []campaign{}
	for campaignRows.Next() {
		var item campaign
		if e = campaignRows.Scan(&item.id, &item.status); e != nil {
			campaignRows.Close()
			return e
		}
		campaigns = append(campaigns, item)
	}
	campaignRows.Close()
	if e = campaignRows.Err(); e != nil {
		return e
	}
	for _, campaign := range campaigns {
		blocks, blockErr := a.broadcastBlocks(ctx, campaign.id)
		if blockErr != nil {
			return blockErr
		}
		first, _, delay, found := nextBroadcastMessage(blocks, 0)
		if !found {
			if _, e = a.DB.Exec(ctx, "DELETE FROM broadcast_user_state WHERE campaign_id=$1", campaign.id); e != nil {
				return e
			}
			continue
		}
		_, e = a.DB.Exec(ctx, `INSERT INTO broadcast_user_state(campaign_id,user_id,step_id,next_send_at)
			SELECT $1,u.id,$2,now()+make_interval(secs=>$3::integer) FROM user_status u
			WHERE ($4='all' OR u.status=$4) AND NOT u.blocked AND u.first_started_at IS NOT NULL
			ON CONFLICT DO NOTHING`, campaign.id, first, delay, campaign.status)
		if e != nil {
			return e
		}
	}
	return a.sendDueBroadcasts(ctx)
}

func (a *App) sendDueBroadcasts(ctx context.Context) error {
	rows, e := a.DB.Query(ctx, "SELECT campaign_id,user_id FROM broadcast_user_state WHERE next_send_at<=now() ORDER BY next_send_at LIMIT 20")
	if e != nil {
		return e
	}
	type dueState struct{ campaignID, userID int64 }
	due := []dueState{}
	for rows.Next() {
		var item dueState
		if e = rows.Scan(&item.campaignID, &item.userID); e != nil {
			rows.Close()
			return e
		}
		due = append(due, item)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, item := range due {
		if e = a.broadcastOne(ctx, item.campaignID, item.userID); e != nil {
			a.Log.Error("broadcast failed", "campaign_id", item.campaignID, "user_id", item.userID, "error", e)
			_, err := a.DB.Exec(ctx, "UPDATE broadcast_user_state SET next_send_at=now()+$3::interval WHERE campaign_id=$1 AND user_id=$2", item.campaignID, item.userID, retryDelay(e, 5))
			if err != nil {
				return err
			}
		}
	}
	return nil
}

type broadcastBlock struct {
	id    int64
	kind  string
	key   string
	delay int
}

func (a *App) broadcastBlocks(ctx context.Context, campaignID int64) ([]broadcastBlock, error) {
	rows, err := a.DB.Query(ctx, `SELECT b.id,b.kind,COALESCE(m.key,''),b.delay_seconds FROM broadcast_steps b LEFT JOIN bot_messages m ON m.id=b.message_id WHERE b.enabled AND b.campaign_id=$1 ORDER BY b.position,b.id`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	blocks := []broadcastBlock{}
	for rows.Next() {
		var block broadcastBlock
		if err = rows.Scan(&block.id, &block.kind, &block.key, &block.delay); err != nil {
			return nil, err
		}
		blocks = append(blocks, block)
	}
	return blocks, rows.Err()
}

// nextBroadcastMessage adds only the delay blocks encountered before the next
// message. A cycle without an explicit delay waits one hour before repeating.
func nextBroadcastMessage(blocks []broadcastBlock, afterID int64) (int64, string, int, bool) {
	if len(blocks) == 0 {
		return 0, "", 0, false
	}
	start := 0
	wrapped := false
	if afterID != 0 {
		found := false
		for index, block := range blocks {
			if block.id == afterID && block.kind == "message" {
				start = (index + 1) % len(blocks)
				wrapped = start == 0
				found = true
				break
			}
		}
		if !found {
			return nextBroadcastMessage(blocks, 0)
		}
	}
	delay := 0
	for offset := range len(blocks) {
		index := (start + offset) % len(blocks)
		if offset > 0 && index == 0 {
			wrapped = true
		}
		block := blocks[index]
		if block.kind == "delay" {
			delay += block.delay
			continue
		}
		if block.kind == "message" {
			if afterID != 0 && wrapped && delay == 0 {
				delay = 3600
			}
			return block.id, block.key, delay, true
		}
	}
	return 0, "", 0, false
}
func (a *App) broadcastOne(ctx context.Context, campaignID, uid int64) error {
	tx, e := a.DB.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", uid); e != nil {
		return e
	}
	var status, audience string
	var enabled, blocked, started bool
	if e = tx.QueryRow(ctx, `SELECT u.status,u.blocked,u.first_started_at IS NOT NULL,c.audience_status,c.enabled
		FROM user_status u JOIN broadcast_campaigns c ON c.id=$2 WHERE u.id=$1`, uid, campaignID).Scan(&status, &blocked, &started, &audience, &enabled); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		return e
	}
	if !enabled || blocked || !started || (audience != "all" && status != audience) {
		_, e = tx.Exec(ctx, "DELETE FROM broadcast_user_state WHERE campaign_id=$1 AND user_id=$2", campaignID, uid)
		if e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	var current *int64
	var due bool
	if e = tx.QueryRow(ctx, "SELECT step_id,next_send_at<=now() FROM broadcast_user_state WHERE campaign_id=$1 AND user_id=$2 FOR UPDATE", campaignID, uid).Scan(&current, &due); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		return e
	}
	if !due {
		return nil
	}
	rows, e := tx.Query(ctx, "SELECT b.id,b.kind,COALESCE(m.key,''),b.delay_seconds FROM broadcast_steps b LEFT JOIN bot_messages m ON m.id=b.message_id WHERE b.enabled AND b.campaign_id=$1 ORDER BY b.position,b.id", campaignID)
	if e != nil {
		return e
	}
	steps := []broadcastBlock{}
	for rows.Next() {
		var s broadcastBlock
		if e = rows.Scan(&s.id, &s.kind, &s.key, &s.delay); e != nil {
			rows.Close()
			return e
		}
		steps = append(steps, s)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	if len(steps) == 0 {
		_, e = tx.Exec(ctx, "DELETE FROM broadcast_user_state WHERE campaign_id=$1 AND user_id=$2", campaignID, uid)
		if e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	index := -1
	for i, s := range steps {
		if current != nil && s.kind == "message" && s.id == *current {
			index = i
			break
		}
	}
	if index < 0 {
		first, _, delay, found := nextBroadcastMessage(steps, 0)
		if !found {
			_, e = tx.Exec(ctx, "DELETE FROM broadcast_user_state WHERE campaign_id=$1 AND user_id=$2", campaignID, uid)
			if e != nil {
				return e
			}
			return tx.Commit(ctx)
		}
		_, e = tx.Exec(ctx, "UPDATE broadcast_user_state SET step_id=$3,next_send_at=now()+make_interval(secs=>$4::integer) WHERE campaign_id=$1 AND user_id=$2", campaignID, uid, first, delay)
		if e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	if e = a.sendTx(ctx, tx, uid, steps[index].key, 0); e != nil {
		return e
	}
	nextID, _, delay, _ := nextBroadcastMessage(steps, steps[index].id)
	if _, e = tx.Exec(ctx, "UPDATE broadcast_user_state SET step_id=$3,last_sent_at=now(),next_send_at=now()+make_interval(secs=>$4::integer) WHERE campaign_id=$1 AND user_id=$2", campaignID, uid, nextID, delay); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// refreshUserSubscriptions fails closed for removal if any known contract cannot
// be verified. Unban recovery deliberately does not depend on provider uptime.
func (a *App) refreshUserSubscriptions(ctx context.Context, uid int64) error {
	rows, err := a.DB.Query(ctx, "SELECT contract_id FROM subscriptions WHERE user_id=$1 ORDER BY id", uid)
	if err != nil {
		return err
	}
	contracts, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, contract := range contracts {
		if err = a.SyncSubscription(ctx, contract); err != nil && !errors.Is(err, ErrPending) {
			return err
		}
	}
	return nil
}
