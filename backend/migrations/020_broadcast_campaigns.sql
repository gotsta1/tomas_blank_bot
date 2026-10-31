CREATE TABLE broadcast_campaigns (
  id BIGSERIAL PRIMARY KEY,
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  audience_status TEXT NOT NULL CHECK (audience_status IN ('all', 'new', 'active', 'expired')),
  enabled BOOLEAN NOT NULL DEFAULT false,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Preserve a previously configured single chain, including each user's place.
INSERT INTO broadcast_campaigns(name, audience_status, enabled)
SELECT 'Основная рассылка', 'new', true
WHERE EXISTS (SELECT 1 FROM broadcast_steps)
   OR EXISTS (SELECT 1 FROM broadcast_user_state);

ALTER TABLE broadcast_steps ADD COLUMN campaign_id BIGINT REFERENCES broadcast_campaigns(id) ON DELETE CASCADE;
UPDATE broadcast_steps SET campaign_id = (SELECT id FROM broadcast_campaigns LIMIT 1);
ALTER TABLE broadcast_steps ALTER COLUMN campaign_id SET NOT NULL;
CREATE INDEX broadcast_steps_campaign_order ON broadcast_steps(campaign_id, position, id);

ALTER TABLE broadcast_user_state ADD COLUMN campaign_id BIGINT REFERENCES broadcast_campaigns(id) ON DELETE CASCADE;
UPDATE broadcast_user_state SET campaign_id = (SELECT id FROM broadcast_campaigns LIMIT 1);
ALTER TABLE broadcast_user_state ALTER COLUMN campaign_id SET NOT NULL;
ALTER TABLE broadcast_user_state DROP CONSTRAINT broadcast_user_state_pkey;
ALTER TABLE broadcast_user_state ADD PRIMARY KEY(campaign_id, user_id);
CREATE INDEX broadcast_user_state_due ON broadcast_user_state(next_send_at, campaign_id, user_id);
