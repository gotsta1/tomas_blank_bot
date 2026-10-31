INSERT INTO bot_messages(key, name, text, parse_mode, buttons)
VALUES (
    'subscription_cancel_failed',
    'Ошибка отключения автопродления',
    '<b>НЕ УДАЛОСЬ ОТКЛЮЧИТЬ АВТОПРОДЛЕНИЕ</b>

Попробуй ещё раз. Если не получится — напиши в поддержку.',
    'HTML',
    jsonb_build_array(jsonb_build_object(
        'row', 0, 'type', 'callback', 'label', '🔄 Попробовать снова',
        'action', 'subscription:cancel:confirm', 'enabled', true
    )) || COALESCE((
        SELECT jsonb_agg(jsonb_set(button, '{row}', '1'::jsonb))
        FROM bot_messages help,
             jsonb_array_elements(help.buttons) AS button
        WHERE help.key = 'help' AND button->>'type' = 'url'
    ), '[]'::jsonb)
);
