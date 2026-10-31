INSERT INTO bot_messages(key, name, text, buttons)
VALUES (
    'payment_failed',
    'Ошибка создания оплаты',
    'Lava.top не смогла создать ссылку на оплату. Попробуйте выбрать способ оплаты ещё раз. Если ошибка повторится, обратитесь в поддержку.',
    '[{"label": "Выбрать способ оплаты", "action": "club:join", "type": "callback", "row": 0, "enabled": true}, {"label": "Поддержка", "action": "https://t.me/your_support", "type": "url", "row": 1, "enabled": true}]'
);
