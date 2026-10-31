ALTER TABLE broadcast_steps DROP CONSTRAINT broadcast_steps_kind_check;

ALTER TABLE broadcast_steps ADD CONSTRAINT broadcast_steps_kind_check CHECK (
  (kind = 'message' AND message_id IS NOT NULL AND delay_seconds = 0)
  OR (kind = 'delay' AND message_id IS NULL AND delay_seconds BETWEEN 1 AND 31536000)
);
