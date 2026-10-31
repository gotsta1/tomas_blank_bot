CREATE TABLE bot_message_media (
    message_id BIGINT PRIMARY KEY REFERENCES bot_messages(id) ON DELETE CASCADE,
    media_type TEXT NOT NULL CHECK (media_type IN ('video', 'video_note')),
    file_name TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 52428800),
    data BYTEA NOT NULL,
    telegram_file_id TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE delivered_message_media (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    control_message_id BIGINT NOT NULL,
    media_message_id BIGINT NOT NULL,
    PRIMARY KEY (user_id, control_message_id)
);
