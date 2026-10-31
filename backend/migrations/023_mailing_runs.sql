CREATE TABLE mailing_runs (
  id BIGSERIAL PRIMARY KEY,
  campaign_id BIGINT NOT NULL REFERENCES broadcast_campaigns(id) ON DELETE CASCADE,
  audience_status TEXT NOT NULL CHECK (audience_status IN ('all','new','active','expired')),
  status TEXT NOT NULL CHECK (status IN ('scheduled', 'sending', 'completed')),
  scheduled_at TIMESTAMPTZ NOT NULL,
  started_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX mailing_runs_campaign_order ON mailing_runs(campaign_id, id DESC);

ALTER TABLE broadcast_campaigns ADD COLUMN current_run_id BIGINT REFERENCES mailing_runs(id) ON DELETE SET NULL;

-- Preserve the already scheduled, sending and completed mailings as run #1.
INSERT INTO mailing_runs(campaign_id,audience_status,status,scheduled_at,started_at,completed_at,created_at)
SELECT id,audience_status,run_status,COALESCE(scheduled_at,created_at),started_at,completed_at,created_at
FROM broadcast_campaigns WHERE mode='manual' AND run_status<>'draft';

UPDATE broadcast_campaigns c SET current_run_id=r.id
FROM mailing_runs r WHERE r.campaign_id=c.id;

ALTER TABLE mailing_deliveries ADD COLUMN run_id BIGINT REFERENCES mailing_runs(id) ON DELETE CASCADE;
UPDATE mailing_deliveries d SET run_id=r.id
FROM mailing_runs r WHERE r.campaign_id=d.campaign_id;
ALTER TABLE mailing_deliveries ALTER COLUMN run_id SET NOT NULL;
ALTER TABLE mailing_deliveries DROP CONSTRAINT mailing_deliveries_pkey;
ALTER TABLE mailing_deliveries ADD PRIMARY KEY(run_id,user_id);
CREATE INDEX mailing_deliveries_run_status ON mailing_deliveries(run_id,status);
