UPDATE bot_messages
SET buttons = (
        SELECT jsonb_agg(
            CASE
                WHEN button->>'action' = 'nav:start'
                    THEN jsonb_set(button, '{action}', '"nav:plan"')
                ELSE button
            END
            ORDER BY position
        )
        FROM jsonb_array_elements(buttons) WITH ORDINALITY AS items(button, position)
    ),
    version = version + 1,
    updated_at = now()
WHERE key = 'choose_payment_region';

UPDATE bot_messages
SET buttons = (
        SELECT jsonb_agg(
            CASE
                WHEN button->>'action' = 'club:join'
                    THEN jsonb_set(button, '{action}', '"nav:region"')
                ELSE button
            END
            ORDER BY position
        )
        FROM jsonb_array_elements(buttons) WITH ORDINALITY AS items(button, position)
    ),
    version = version + 1,
    updated_at = now()
WHERE key IN ('choose_ru_payment', 'choose_foreign_payment');
