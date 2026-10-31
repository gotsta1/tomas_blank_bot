ALTER TABLE checkout_attempts
    ADD COLUMN price_amount NUMERIC(20, 6),
    ADD COLUMN price_currency TEXT
        CHECK (price_currency IS NULL OR price_currency ~ '^[A-Z]{3}$');

UPDATE bot_messages
SET buttons = (
        SELECT jsonb_agg(
            CASE
                WHEN button->>'action' = '{{payment_url}}'
                    THEN jsonb_set(button, '{label}', '"Оплатить {{price}}"')
                ELSE button
            END
            ORDER BY position
        )
        FROM jsonb_array_elements(buttons) WITH ORDINALITY AS items(button, position)
    ),
    version = version + 1,
    updated_at = now()
WHERE key = 'payment_created';
