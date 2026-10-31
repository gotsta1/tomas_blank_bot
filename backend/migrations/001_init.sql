CREATE TABLE users (
 id BIGSERIAL PRIMARY KEY, telegram_user_id BIGINT UNIQUE NOT NULL,
 username TEXT NOT NULL DEFAULT '', first_name TEXT NOT NULL DEFAULT '', last_name TEXT NOT NULL DEFAULT '',
 email TEXT, pending_flow TEXT NOT NULL DEFAULT '', blocked BOOLEAN NOT NULL DEFAULT false,
 first_started_at TIMESTAMPTZ, last_activity_at TIMESTAMPTZ NOT NULL DEFAULT now(), created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE checkout_attempts (
 id BIGSERIAL PRIMARY KEY, user_id BIGINT NOT NULL REFERENCES users(id), flow TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('creating','ready','unknown','completed','failed')),
 external_id TEXT UNIQUE, payment_url TEXT, email TEXT NOT NULL, offer_id TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX one_pending_checkout ON checkout_attempts(user_id) WHERE state IN ('creating','ready','unknown');
CREATE TABLE subscriptions (
 id BIGSERIAL PRIMARY KEY, user_id BIGINT NOT NULL REFERENCES users(id), contract_id TEXT UNIQUE NOT NULL,
 offer_id TEXT NOT NULL, email TEXT NOT NULL, provider_status TEXT NOT NULL DEFAULT 'NEW', auto_renew BOOLEAN NOT NULL DEFAULT false,
 started_at TIMESTAMPTZ, expires_at TIMESTAMPTZ, cancelled_at TIMESTAMPTZ, terminated_at TIMESTAMPTZ,
 synced_at TIMESTAMPTZ NOT NULL DEFAULT now(), created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX subscriptions_access ON subscriptions(user_id, expires_at DESC);
CREATE TABLE payments (
 external_id TEXT PRIMARY KEY, subscription_id BIGINT NOT NULL REFERENCES subscriptions(id),
 amount NUMERIC(20,6) NOT NULL, currency TEXT NOT NULL, paid_at TIMESTAMPTZ NOT NULL,
 payment_type TEXT NOT NULL, raw_payload JSONB NOT NULL
);
CREATE TABLE webhook_events (
 id BIGSERIAL PRIMARY KEY, provider TEXT NOT NULL, dedup_key TEXT NOT NULL, payload JSONB NOT NULL,
 processed_at TIMESTAMPTZ, attempts INTEGER NOT NULL DEFAULT 0, next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 last_error TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(provider,dedup_key)
);
CREATE INDEX webhook_pending ON webhook_events(next_attempt_at,id) WHERE processed_at IS NULL;
CREATE TABLE bot_messages (
 id BIGSERIAL PRIMARY KEY, key TEXT UNIQUE NOT NULL, name TEXT NOT NULL, text TEXT NOT NULL,
 parse_mode TEXT NOT NULL DEFAULT '' CHECK(parse_mode IN ('','HTML')),
 buttons JSONB NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(buttons)='array'),
 version INTEGER NOT NULL DEFAULT 1, updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE broadcast_steps (
 id BIGSERIAL PRIMARY KEY, message_id BIGINT NOT NULL REFERENCES bot_messages(id), position INTEGER NOT NULL,
 delay_seconds INTEGER NOT NULL CHECK(delay_seconds BETWEEN 60 AND 31536000), enabled BOOLEAN NOT NULL DEFAULT true
);
CREATE TABLE broadcast_user_state (
 user_id BIGINT PRIMARY KEY REFERENCES users(id), step_id BIGINT REFERENCES broadcast_steps(id) ON DELETE SET NULL,
 last_sent_at TIMESTAMPTZ, next_send_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE channel_access (
 user_id BIGINT PRIMARY KEY REFERENCES users(id), state TEXT NOT NULL DEFAULT 'unknown' CHECK(state IN ('unknown','member','removing','removed')),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE outbox_jobs (
 id BIGSERIAL PRIMARY KEY, user_id BIGINT NOT NULL REFERENCES users(id), message_key TEXT NOT NULL,
 dedup_key TEXT UNIQUE NOT NULL, done_at TIMESTAMPTZ, attempts INTEGER NOT NULL DEFAULT 0,
 next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(), last_error TEXT
);
CREATE TABLE admin_sessions (token_hash TEXT PRIMARY KEY, expires_at TIMESTAMPTZ NOT NULL);
CREATE TABLE login_attempts (bucket TEXT PRIMARY KEY, attempts INTEGER NOT NULL, window_start TIMESTAMPTZ NOT NULL);
CREATE TABLE audit_log (id BIGSERIAL PRIMARY KEY, action TEXT NOT NULL, entity_id TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE VIEW user_status AS
SELECT u.*, CASE WHEN COALESCE(s.expires_at > now(),false) THEN 'active' WHEN s.expires_at IS NOT NULL THEN 'expired' ELSE 'new' END AS status,
 s.expires_at, COALESCE(s.auto_renew,false) AS auto_renew,
 GREATEST(0,ceil(extract(epoch FROM (s.expires_at-now()))/86400))::integer AS days_left
FROM users u LEFT JOIN LATERAL (
 SELECT expires_at,auto_renew FROM subscriptions WHERE user_id=u.id AND expires_at IS NOT NULL ORDER BY expires_at DESC LIMIT 1
) s ON true;
