package app

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// launchMailing queues one or more independent runs. The worker snapshots
// matching users separately at each requested time.
func (a *App) launchMailing(w http.ResponseWriter, r *http.Request, id int64) error {
	if r.Method != http.MethodPost || id == 0 {
		bad(w, 405, "Method not allowed")
		return nil
	}
	var input struct {
		ScheduledAt    *time.Time  `json:"scheduled_at"`
		ScheduledTimes []time.Time `json:"scheduled_times"`
	}
	if decode(r, &input) != nil {
		bad(w, 400, "Invalid JSON")
		return nil
	}
	immediate := input.ScheduledAt == nil && len(input.ScheduledTimes) == 0
	when := time.Now()
	times := input.ScheduledTimes
	if len(times) == 0 {
		if input.ScheduledAt != nil {
			when = *input.ScheduledAt
		}
		times = []time.Time{when}
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	for i, scheduled := range times {
		if scheduled.IsZero() || (input.ScheduledAt != nil || len(input.ScheduledTimes) > 0) && !scheduled.After(time.Now()) {
			bad(w, 400, "Scheduled time must be in the future")
			return nil
		}
		if i > 0 && scheduled.Equal(times[i-1]) {
			bad(w, 400, "Duplicate scheduled time")
			return nil
		}
	}
	ctx := r.Context()
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status, audience string
	err = tx.QueryRow(ctx, "SELECT run_status,audience_status FROM broadcast_campaigns WHERE id=$1 AND mode='manual' FOR UPDATE", id).Scan(&status, &audience)
	if errors.Is(err, pgx.ErrNoRows) {
		bad(w, 404, "Not found")
		return nil
	}
	if err != nil {
		return err
	}
	if status == "sending" || status == "scheduled" && !immediate {
		bad(w, 409, "Wait for the current mailing to finish")
		return nil
	}
	if status == "scheduled" {
		var immediatePending bool
		err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM mailing_runs WHERE campaign_id=$1 AND status='scheduled' AND scheduled_at<=now())", id).Scan(&immediatePending)
		if err != nil {
			return err
		}
		if immediatePending {
			bad(w, 409, "Немедленная отправка уже ожидает запуска")
			return nil
		}
	}
	var hasMessage bool
	err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM broadcast_steps WHERE campaign_id=$1 AND kind='message' AND enabled)", id).Scan(&hasMessage)
	if err != nil {
		return err
	}
	if !hasMessage {
		bad(w, 400, "Add at least one enabled message")
		return nil
	}
	runIDs := make([]int64, 0, len(times))
	for _, scheduled := range times {
		var runID int64
		if err = tx.QueryRow(ctx, `INSERT INTO mailing_runs(campaign_id,audience_status,status,scheduled_at)
			VALUES($1,$2,'scheduled',$3) RETURNING id`, id, audience, scheduled).Scan(&runID); err != nil {
			return err
		}
		runIDs = append(runIDs, runID)
	}
	if _, err = tx.Exec(ctx, `UPDATE broadcast_campaigns SET current_run_id=$2,run_status='scheduled',
		scheduled_at=$3,started_at=NULL,completed_at=NULL,updated_at=now() WHERE id=$1`, id, runIDs[0], times[0]); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO audit_log(action,entity_id) VALUES('mailing.launch',$1)", jsonNumber(id)); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	respond(w, 200, map[string]any{"status": "scheduled", "scheduled_times": times, "run_ids": runIDs})
	return nil
}

// updateMailingSchedule replaces only runs that have not started. Running and
// completed runs remain untouched, including their delivery history.
func (a *App) updateMailingSchedule(w http.ResponseWriter, r *http.Request, id int64) error {
	if r.Method != http.MethodPut || id == 0 {
		bad(w, 405, "Method not allowed")
		return nil
	}
	var input struct {
		ExpectedRunIDs []int64 `json:"expected_run_ids"`
		Runs           []struct {
			ID          int64     `json:"id"`
			ScheduledAt time.Time `json:"scheduled_at"`
		} `json:"runs"`
	}
	if decode(r, &input) != nil {
		bad(w, 400, "Invalid JSON")
		return nil
	}
	now := time.Now()
	seenTimes := make(map[time.Time]bool, len(input.Runs))
	seenIDs := make(map[int64]bool, len(input.Runs))
	for _, run := range input.Runs {
		if !run.ScheduledAt.After(now) {
			bad(w, 400, "Scheduled time must be in the future")
			return nil
		}
		when := run.ScheduledAt.UTC()
		if seenTimes[when] || run.ID < 0 || run.ID > 0 && seenIDs[run.ID] {
			bad(w, 400, "Duplicate or invalid scheduled run")
			return nil
		}
		seenTimes[when] = true
		if run.ID > 0 {
			seenIDs[run.ID] = true
		}
	}
	ctx := r.Context()
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var audience string
	err = tx.QueryRow(ctx, "SELECT audience_status FROM broadcast_campaigns WHERE id=$1 AND mode='manual' FOR UPDATE", id).Scan(&audience)
	if errors.Is(err, pgx.ErrNoRows) {
		bad(w, 404, "Not found")
		return nil
	}
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, "SELECT id FROM mailing_runs WHERE campaign_id=$1 AND status='scheduled' AND scheduled_at>now() ORDER BY id FOR UPDATE", id)
	if err != nil {
		return err
	}
	currentIDs, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return err
	}
	sort.Slice(input.ExpectedRunIDs, func(i, j int) bool { return input.ExpectedRunIDs[i] < input.ExpectedRunIDs[j] })
	if len(currentIDs) != len(input.ExpectedRunIDs) {
		bad(w, 409, "Расписание изменилось. Закройте календарь и откройте его снова.")
		return nil
	}
	current := make(map[int64]bool, len(currentIDs))
	for i, runID := range currentIDs {
		if runID != input.ExpectedRunIDs[i] {
			bad(w, 409, "Расписание изменилось. Закройте календарь и откройте его снова.")
			return nil
		}
		current[runID] = true
	}
	for runID := range seenIDs {
		if !current[runID] {
			bad(w, 409, "Отправка уже началась или расписание изменилось. Откройте календарь снова.")
			return nil
		}
	}
	for _, runID := range currentIDs {
		if !seenIDs[runID] {
			if _, err = tx.Exec(ctx, "DELETE FROM mailing_runs WHERE id=$1", runID); err != nil {
				return err
			}
		}
	}
	for _, run := range input.Runs {
		if run.ID > 0 {
			if _, err = tx.Exec(ctx, "UPDATE mailing_runs SET scheduled_at=$2 WHERE id=$1", run.ID, run.ScheduledAt); err != nil {
				return err
			}
		} else {
			if _, err = tx.Exec(ctx, "INSERT INTO mailing_runs(campaign_id,audience_status,status,scheduled_at) VALUES($1,$2,'scheduled',$3)", id, audience, run.ScheduledAt); err != nil {
				return err
			}
		}
	}
	if err = syncMailingCampaign(ctx, tx, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO audit_log(action,entity_id) VALUES('mailing.schedule_update',$1)", jsonNumber(id)); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	respond(w, 200, map[string]any{"status": "saved"})
	return nil
}

func syncMailingCampaign(ctx context.Context, tx pgx.Tx, id int64) error {
	var lockedID int64
	if err := tx.QueryRow(ctx, "SELECT id FROM broadcast_campaigns WHERE id=$1 FOR UPDATE", id).Scan(&lockedID); err != nil {
		return err
	}
	var chosenID *int64
	var chosenStatus *string
	var scheduledAt, startedAt, completedAt *time.Time
	err := tx.QueryRow(ctx, `SELECT id,status,scheduled_at,started_at,completed_at FROM mailing_runs
		WHERE campaign_id=$1 ORDER BY CASE status WHEN 'sending' THEN 0 WHEN 'scheduled' THEN 1 ELSE 2 END,
		CASE WHEN status='scheduled' THEN scheduled_at END ASC,
		CASE WHEN status='completed' THEN id END DESC LIMIT 1`, id).Scan(&chosenID, &chosenStatus, &scheduledAt, &startedAt, &completedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	status := "draft"
	if chosenStatus != nil {
		status = *chosenStatus
	}
	_, err = tx.Exec(ctx, `UPDATE broadcast_campaigns SET current_run_id=$2,run_status=$3,scheduled_at=$4,
		started_at=$5,completed_at=$6,updated_at=now() WHERE id=$1`, id, chosenID, status, scheduledAt, startedAt, completedAt)
	return err
}

type MailingRun struct {
	ID             int64      `json:"id"`
	AudienceStatus string     `json:"audience_status"`
	Status         string     `json:"status"`
	ScheduledAt    time.Time  `json:"scheduled_at"`
	StartedAt      *time.Time `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at"`
	RecipientCount int        `json:"recipient_count"`
	CompletedCount int        `json:"completed_count"`
	FailedCount    int        `json:"failed_count"`
}

func (a *App) mailingRuns(w http.ResponseWriter, r *http.Request, campaignID int64) error {
	if r.Method != http.MethodGet || campaignID == 0 {
		bad(w, 405, "Method not allowed")
		return nil
	}
	rows, err := a.DB.Query(r.Context(), `SELECT r.id,r.audience_status,r.status,r.scheduled_at,r.started_at,r.completed_at,
		(SELECT count(*)::integer FROM mailing_deliveries d WHERE d.run_id=r.id),
		(SELECT count(*)::integer FROM mailing_deliveries d WHERE d.run_id=r.id AND d.status='completed'),
		(SELECT count(*)::integer FROM mailing_deliveries d WHERE d.run_id=r.id AND d.status='failed')
		FROM mailing_runs r WHERE r.campaign_id=$1 ORDER BY r.id DESC`, campaignID)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []MailingRun{}
	for rows.Next() {
		var item MailingRun
		if err = rows.Scan(&item.ID, &item.AudienceStatus, &item.Status, &item.ScheduledAt, &item.StartedAt, &item.CompletedAt, &item.RecipientCount, &item.CompletedCount, &item.FailedCount); err != nil {
			return err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	respond(w, 200, items)
	return nil
}

// nextMailingMessage deliberately never wraps around to the first block.
func nextMailingMessage(blocks []broadcastBlock, afterID int64) (int64, string, int, bool) {
	start := 0
	if afterID != 0 {
		start = len(blocks)
		for i, block := range blocks {
			if block.id == afterID && block.kind == "message" {
				start = i + 1
				break
			}
		}
	}
	delay := 0
	for _, block := range blocks[start:] {
		if block.kind == "delay" {
			delay += block.delay
		}
		if block.kind == "message" {
			return block.id, block.key, delay, true
		}
	}
	return 0, "", 0, false
}

func (a *App) processMailings(ctx context.Context) error {
	rows, err := a.DB.Query(ctx, "SELECT r.id FROM mailing_runs r JOIN broadcast_campaigns c ON c.id=r.campaign_id WHERE c.mode='manual' AND r.status='scheduled' AND r.scheduled_at<=now() ORDER BY r.scheduled_at LIMIT 10")
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = a.startMailing(ctx, id); err != nil {
			return err
		}
	}
	rows, err = a.DB.Query(ctx, `SELECT d.run_id,d.user_id FROM mailing_deliveries d
		JOIN mailing_runs r ON r.id=d.run_id
		WHERE r.status='sending' AND d.status='pending' AND d.next_send_at<=now()
		ORDER BY d.next_send_at LIMIT 20`)
	if err != nil {
		return err
	}
	type due struct{ runID, userID int64 }
	items := []due{}
	for rows.Next() {
		var item due
		if err = rows.Scan(&item.runID, &item.userID); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, item := range items {
		if err = a.sendMailingOne(ctx, item.runID, item.userID); err != nil {
			a.Log.Error("mailing delivery failed", "run_id", item.runID, "user_id", item.userID, "error", err)
			if _, updateErr := a.DB.Exec(ctx, `UPDATE mailing_deliveries SET attempts=attempts+1,last_error=$3,
				status=CASE WHEN attempts>=4 THEN 'failed' ELSE status END,
				next_send_at=now()+$4::interval WHERE run_id=$1 AND user_id=$2 AND status='pending'`, item.runID, item.userID, err.Error(), retryDelay(err, 5)); updateErr != nil {
				return updateErr
			}
		}
	}
	rows, err = a.DB.Query(ctx, `UPDATE mailing_runs r SET status='completed',completed_at=now()
		WHERE r.status='sending' AND NOT EXISTS(SELECT 1 FROM mailing_deliveries d WHERE d.run_id=r.id AND d.status='pending')
		RETURNING r.campaign_id`)
	if err != nil {
		return err
	}
	finishedCampaigns, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return err
	}
	seenCampaigns := make(map[int64]bool, len(finishedCampaigns))
	for _, campaignID := range finishedCampaigns {
		if seenCampaigns[campaignID] {
			continue
		}
		seenCampaigns[campaignID] = true
		tx, beginErr := a.DB.Begin(ctx)
		if beginErr != nil {
			return beginErr
		}
		if syncErr := syncMailingCampaign(ctx, tx, campaignID); syncErr != nil {
			_ = tx.Rollback(ctx)
			return syncErr
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return commitErr
		}
	}
	return nil
}

func (a *App) startMailing(ctx context.Context, runID int64) error {
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var audience string
	var id int64
	err = tx.QueryRow(ctx, "SELECT campaign_id FROM mailing_runs WHERE id=$1", runID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var lockedID int64
	err = tx.QueryRow(ctx, "SELECT id FROM broadcast_campaigns WHERE id=$1 AND mode='manual' FOR UPDATE", id).Scan(&lockedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `SELECT audience_status FROM mailing_runs WHERE id=$1 AND campaign_id=$2
		AND status='scheduled' AND scheduled_at<=now() FOR UPDATE`, runID, id).Scan(&audience)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	blocks, err := a.broadcastBlocks(ctx, id)
	if err != nil {
		return err
	}
	first, _, delay, found := nextMailingMessage(blocks, 0)
	if !found {
		return errors.New("scheduled mailing has no enabled message")
	}
	_, err = tx.Exec(ctx, `INSERT INTO mailing_deliveries(campaign_id,run_id,user_id,step_id,next_send_at)
		SELECT $1,$2,u.id,$3,now()+make_interval(secs=>$4::integer) FROM user_status u
		WHERE u.first_started_at IS NOT NULL AND NOT u.blocked AND ($5='all' OR u.status=$5)
		ON CONFLICT DO NOTHING`, id, runID, first, delay, audience)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE broadcast_campaigns SET current_run_id=$2,run_status='sending',scheduled_at=(SELECT scheduled_at FROM mailing_runs WHERE id=$2),started_at=now(),completed_at=NULL,updated_at=now() WHERE id=$1`, id, runID)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE mailing_runs SET status='sending',started_at=now() WHERE id=$1", runID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (a *App) sendMailingOne(ctx context.Context, runID, userID int64) error {
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var stepID, campaignID int64
	var blocked bool
	err = tx.QueryRow(ctx, `SELECT d.step_id,u.blocked,d.campaign_id FROM mailing_deliveries d
		JOIN users u ON u.id=d.user_id JOIN mailing_runs r ON r.id=d.run_id
		WHERE d.run_id=$1 AND d.user_id=$2 AND d.status='pending' AND d.next_send_at<=now()
		AND r.status='sending' FOR UPDATE OF d,u`, runID, userID).Scan(&stepID, &blocked, &campaignID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if blocked {
		_, err = tx.Exec(ctx, "UPDATE mailing_deliveries SET status='failed',last_error='Telegram user blocked bot' WHERE run_id=$1 AND user_id=$2", runID, userID)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	blocks, err := a.broadcastBlocks(ctx, campaignID)
	if err != nil {
		return err
	}
	var key string
	for _, b := range blocks {
		if b.id == stepID && b.kind == "message" {
			key = b.key
			break
		}
	}
	if key == "" {
		return errors.New("mailing message is missing")
	}
	if err = a.sendTx(ctx, tx, userID, key, 0); err != nil {
		return err
	}
	nextID, _, delay, found := nextMailingMessage(blocks, stepID)
	if found {
		_, err = tx.Exec(ctx, `UPDATE mailing_deliveries SET step_id=$3,next_send_at=now()+make_interval(secs=>$4::integer),
			 sent_count=sent_count+1,attempts=0,last_error=NULL WHERE run_id=$1 AND user_id=$2`, runID, userID, nextID, delay)
	} else {
		_, err = tx.Exec(ctx, `UPDATE mailing_deliveries SET step_id=NULL,status='completed',sent_count=sent_count+1,attempts=0,last_error=NULL
			WHERE run_id=$1 AND user_id=$2`, runID, userID)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
