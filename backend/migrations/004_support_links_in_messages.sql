-- Support links are content, so administrators manage them with their message
-- buttons. Replace the former environment-backed placeholder with an editable
-- HTTPS value while preserving button order and all other fields.
UPDATE bot_messages AS message
SET buttons = (
        SELECT jsonb_agg(
            CASE
                WHEN button->>'action' = '{{support_url}}'
                    THEN jsonb_set(button, '{action}', to_jsonb('https://t.me/your_support'::text))
                ELSE button
            END
            ORDER BY ordinal
        )
        FROM jsonb_array_elements(message.buttons) WITH ORDINALITY AS entry(button, ordinal)
    ),
    version = version + 1,
    updated_at = now()
WHERE message.buttons @> '[{"action":"{{support_url}}"}]'::jsonb;
