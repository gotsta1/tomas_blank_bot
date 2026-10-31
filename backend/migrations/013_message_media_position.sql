ALTER TABLE bot_message_media
    ADD COLUMN media_position TEXT NOT NULL DEFAULT 'before'
    CHECK (media_position IN ('before', 'after'));
