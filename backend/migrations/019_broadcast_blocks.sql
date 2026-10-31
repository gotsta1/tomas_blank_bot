ALTER TABLE broadcast_steps DROP CONSTRAINT broadcast_steps_delay_seconds_check;
ALTER TABLE broadcast_steps ALTER COLUMN message_id DROP NOT NULL;
ALTER TABLE broadcast_steps ADD COLUMN kind TEXT NOT NULL DEFAULT 'message';

CREATE TEMP TABLE broadcast_legacy_order ON COMMIT DROP AS
SELECT id, delay_seconds, row_number() OVER (ORDER BY position, id)::integer AS ordinal
FROM broadcast_steps;

UPDATE broadcast_steps AS step
SET position = old.ordinal * 2, delay_seconds = 0
FROM broadcast_legacy_order AS old
WHERE step.id = old.id;

INSERT INTO broadcast_steps(message_id, position, delay_seconds, enabled, kind)
SELECT NULL, ordinal * 2 - 1, delay_seconds, true, 'delay'
FROM broadcast_legacy_order;

ALTER TABLE broadcast_steps ADD CONSTRAINT broadcast_steps_kind_check CHECK (
  (kind = 'message' AND message_id IS NOT NULL AND delay_seconds = 0)
  OR (kind = 'delay' AND message_id IS NULL AND delay_seconds BETWEEN 60 AND 31536000)
);
