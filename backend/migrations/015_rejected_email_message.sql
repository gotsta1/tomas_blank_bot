INSERT INTO bot_messages(key, name, text, parse_mode, buttons)
VALUES (
    'email_rejected',
    'Email отклонён платёжной системой',
    '<b>ЭТУ ПОЧТУ НЕЛЬЗЯ ИСПОЛЬЗОВАТЬ</b>

Lava.top не приняла этот email. Отправь другой адрес одним сообщением.',
    'HTML',
    '[{"label": "← Назад", "action": "club:join", "type": "callback", "row": 0, "enabled": true}]'
)
ON CONFLICT (key) DO NOTHING;
