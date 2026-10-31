ALTER TABLE broadcast_campaigns ADD COLUMN mode TEXT NOT NULL DEFAULT 'auto'
  CHECK (mode IN ('auto', 'manual'));
ALTER TABLE broadcast_campaigns ADD COLUMN run_status TEXT NOT NULL DEFAULT 'draft'
  CHECK (run_status IN ('draft', 'scheduled', 'sending', 'completed'));
ALTER TABLE broadcast_campaigns ADD COLUMN scheduled_at TIMESTAMPTZ;
ALTER TABLE broadcast_campaigns ADD COLUMN started_at TIMESTAMPTZ;
ALTER TABLE broadcast_campaigns ADD COLUMN completed_at TIMESTAMPTZ;

CREATE TABLE mailing_deliveries (
  campaign_id BIGINT NOT NULL REFERENCES broadcast_campaigns(id) ON DELETE CASCADE,
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  step_id BIGINT REFERENCES broadcast_steps(id) ON DELETE SET NULL,
  next_send_at TIMESTAMPTZ NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'completed', 'failed')),
  sent_count INTEGER NOT NULL DEFAULT 0,
  attempts INTEGER NOT NULL DEFAULT 0,
  last_error TEXT,
  PRIMARY KEY(campaign_id, user_id)
);
CREATE INDEX mailing_deliveries_due ON mailing_deliveries(next_send_at, campaign_id, user_id)
  WHERE status='pending';
