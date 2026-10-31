CREATE TABLE runtime_settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE INDEX outbox_pending ON outbox_jobs(next_attempt_at,id) WHERE done_at IS NULL;
