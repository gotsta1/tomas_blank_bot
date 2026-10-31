UPDATE bot_messages
SET text = '<b>THOMAS CLUB</b>

Закрытое пространство для тех, кто решил не откладывать себя на потом.

Тренировки, питание, восстановление и сильное окружение — всё, чтобы двигаться к результату.',
    parse_mode = 'HTML',
    buttons = '[{"row":0,"type":"callback","label":"🗃 Что внутри","action":"club:about","enabled":true},{"row":1,"type":"callback","label":"🔥 Вступить в клуб","action":"club:join","enabled":true}]'::jsonb,
    version = version + 1,
    updated_at = now()
WHERE key = 'start';

UPDATE bot_messages
SET text = '<b>ЧТО ТЕБЯ ЖДЁТ ВНУТРИ:</b>

🏋🏼 Тренировки и рабочие схемы
🥩 Питание без лишней теории
🧬 Восстановление и здоровье
🤝 Закрытое сообщество людей, которые делают

Если решил меняться — заходи.',
    parse_mode = 'HTML',
    buttons = '[{"row":0,"type":"callback","label":"🔥 Выбрать подписку","action":"club:join","enabled":true}]'::jsonb,
    version = version + 1,
    updated_at = now()
WHERE key = 'about_club';

UPDATE bot_messages
SET text = '<b>СРОК ПОДПИСКИ:</b>

Выбери период. После его окончания подписка продлится автоматически — отключить продление можно в любой момент.',
    parse_mode = 'HTML',
    buttons = '[{"row":0,"type":"callback","label":"⚡ 1 месяц","action":"plan:monthly","enabled":true},{"row":1,"type":"callback","label":"🔥 3 месяца","action":"plan:90d","enabled":true},{"row":2,"type":"callback","label":"💪 6 месяцев","action":"plan:180d","enabled":true},{"row":3,"type":"callback","label":"🏆 12 месяцев","action":"plan:year","enabled":true},{"row":4,"type":"callback","label":"← Назад","action":"nav:start","enabled":true}]'::jsonb,
    version = version + 1,
    updated_at = now()
WHERE key = 'choose_plan';

UPDATE bot_messages
SET text = '<b>СПОСОБ ОПЛАТЫ:</b>',
    parse_mode = 'HTML',
    buttons = '[{"row":0,"type":"callback","label":"🇷🇺 Картой РФ","action":"pay:region:ru","enabled":true},{"row":1,"type":"callback","label":"🌎 Зарубежной картой","action":"pay:region:foreign","enabled":true},{"row":2,"type":"callback","label":"← Назад","action":"nav:plan","enabled":true}]'::jsonb,
    version = version + 1,
    updated_at = now()
WHERE key = 'choose_payment_region';

UPDATE bot_messages
SET text = '<b>СПОСОБ ОПЛАТЫ:</b>

Выбери удобный вариант:',
    parse_mode = 'HTML',
    buttons = '[{"row":0,"type":"callback","label":"⚡ СБП","action":"pay:ru:sbp","enabled":true},{"row":1,"type":"callback","label":"💳 Картой","action":"pay:ru:card","enabled":true},{"row":2,"type":"callback","label":"← Назад","action":"nav:region","enabled":true}]'::jsonb,
    version = version + 1,
    updated_at = now()
WHERE key = 'choose_ru_payment';

UPDATE bot_messages
SET text = '<b>СПОСОБ ОПЛАТЫ:</b>

Оплата пройдёт в долларах. Если карта в другой валюте, банк автоматически пересчитает сумму.',
    parse_mode = 'HTML',
    buttons = '[{"row":0,"type":"callback","label":"🅿️ PayPal","action":"pay:foreign:paypal","enabled":true},{"row":1,"type":"callback","label":"💳 Картой","action":"pay:foreign:card","enabled":true},{"row":2,"type":"callback","label":" Apple Pay","action":"pay:foreign:applepay","enabled":true},{"row":3,"type":"callback","label":"← Назад","action":"nav:region","enabled":true}]'::jsonb,
    version = version + 1,
    updated_at = now()
WHERE key = 'choose_foreign_payment';

UPDATE bot_messages
SET text = '<b>КУДА ОТПРАВИТЬ ЧЕК?</b>

Отправь свой email одним сообщением.

На эту почту Lava.top оформит подписку и будет присылать чеки и уведомления о продлении.',
    parse_mode = 'HTML',
    buttons = '[{"row":0,"type":"callback","label":"← Назад","action":"club:join","enabled":true}]'::jsonb,
    version = version + 1,
    updated_at = now()
WHERE key = 'email_request';

UPDATE bot_messages
SET text = 'Не похоже на email 🤔

Проверь адрес и отправь ещё раз, например: name@gmail.com',
    parse_mode = '',
    version = version + 1,
    updated_at = now()
WHERE key = 'email_invalid';

UPDATE bot_messages
SET text = '⏳ Создаю ссылку на оплату…',
    parse_mode = '',
    version = version + 1,
    updated_at = now()
WHERE key = 'payment_generating';

UPDATE bot_messages
SET text = '<b>ВСЁ ГОТОВО К ОПЛАТЕ:</b>

Тариф: {{plan_name}}
Период: {{period}}
Автопродление: включено

Нажми кнопку ниже — сумма и доступные способы оплаты будут на странице Lava.top.',
    parse_mode = 'HTML',
    buttons = '[{"row":0,"type":"url","label":"💳 Оплатить {{price}}","action":"{{payment_url}}","enabled":true},{"row":1,"type":"callback","label":"✅ Я оплатил","action":"pay:check","enabled":true}]'::jsonb,
    version = version + 1,
    updated_at = now()
WHERE key = 'payment_created';

UPDATE bot_messages
SET text = '⏳ Платёж пока не найден.

Если только что оплатил — подожди несколько секунд и проверь ещё раз.',
    parse_mode = '',
    buttons = '[{"row":0,"type":"callback","label":"🔄 Проверить оплату","action":"pay:check","enabled":true}]'::jsonb,
    version = version + 1,
    updated_at = now()
WHERE key = 'payment_pending';

UPDATE bot_messages
SET text = '<b>ПРОВЕРЯЮ ПЛАТЁЖ</b>

Lava.top ещё обрабатывает запрос. Не создавай новую подписку — попробуй проверить статус чуть позже.',
    parse_mode = 'HTML',
    buttons = (
        SELECT jsonb_agg(
            CASE WHEN button->>'type' = 'url'
                THEN jsonb_set(button, '{label}', '"💬 Написать в поддержку"'::jsonb)
                ELSE button
            END
            ORDER BY position
        )
        FROM jsonb_array_elements(buttons) WITH ORDINALITY AS items(button, position)
    ),
    version = version + 1,
    updated_at = now()
WHERE key = 'payment_uncertain';

UPDATE bot_messages
SET text = '<b>НЕ УДАЛОСЬ СОЗДАТЬ ОПЛАТУ</b>

Попробуй ещё раз. Если ошибка повторится — напиши в поддержку.',
    parse_mode = 'HTML',
    buttons = (
        SELECT jsonb_agg(
            CASE
                WHEN button->>'type' = 'callback' THEN jsonb_set(button, '{label}', '"🔄 Попробовать снова"'::jsonb)
                WHEN button->>'type' = 'url' THEN jsonb_set(button, '{label}', '"💬 Написать в поддержку"'::jsonb)
                ELSE button
            END
            ORDER BY position
        )
        FROM jsonb_array_elements(buttons) WITH ORDINALITY AS items(button, position)
    ),
    version = version + 1,
    updated_at = now()
WHERE key = 'payment_failed';

UPDATE bot_messages
SET text = '<b>ОПЛАТА ПРОШЛА ✅</b>

Подписка действует до: {{expires_at}}

Отправь заявку в закрытый канал — бот проверит доступ и пропустит тебя.',
    parse_mode = 'HTML',
    buttons = '[{"row":0,"type":"url","label":"🔥 Вступить в закрытый канал","action":"{{invite_url}}","enabled":true}]'::jsonb,
    version = version + 1,
    updated_at = now()
WHERE key = 'payment_success';

UPDATE bot_messages
SET text = '<b>🗃 ПОДПИСКА</b>

🟢 Подписка активна
Действует до: {{expires_date}}
Дней до окончания доступа: {{days_left}}
Автопродление: {{auto_renew}}',
    parse_mode = 'HTML',
    buttons = '[{"row":0,"type":"url","label":"🔥 Вступить в канал","action":"{{invite_url}}","enabled":true},{"row":1,"type":"callback","label":"✖ Отключить автопродление","action":"subscription:cancel","enabled":true}]'::jsonb,
    version = version + 1,
    updated_at = now()
WHERE key = 'subscription_status_active';

UPDATE bot_messages
SET text = '<b>🗃 ПОДПИСКА</b>

🔴 Срок подписки закончился.

Выбери новый тариф, чтобы вернуть доступ в клуб.',
    parse_mode = 'HTML',
    buttons = '[{"row":0,"type":"callback","label":"🔥 Возобновить подписку","action":"club:join","enabled":true}]'::jsonb,
    version = version + 1,
    updated_at = now()
WHERE key = 'subscription_status_expired';

UPDATE bot_messages
SET text = '<b>🗃 ПОДПИСКА</b>

Подписки пока нет.

Выбери тариф и заходи в закрытый клуб.',
    parse_mode = 'HTML',
    buttons = '[{"row":0,"type":"callback","label":"🔥 Выбрать подписку","action":"club:join","enabled":true}]'::jsonb,
    version = version + 1,
    updated_at = now()
WHERE key = 'subscription_status_new';

UPDATE bot_messages
SET text = '<b>ОТКЛЮЧИТЬ АВТОПРОДЛЕНИЕ?</b>

Новых списаний не будет. Доступ сохранится до конца уже оплаченного периода.',
    parse_mode = 'HTML',
    buttons = '[{"row":0,"type":"callback","label":"Да, отключить","action":"subscription:cancel:confirm","enabled":true},{"row":1,"type":"callback","label":"← Назад","action":"subscription:status","enabled":true}]'::jsonb,
    version = version + 1,
    updated_at = now()
WHERE key = 'subscription_cancel_confirm';

UPDATE bot_messages
SET text = '<b>АВТОПРОДЛЕНИЕ ОТКЛЮЧЕНО</b>

Новых списаний не будет. Доступ в клуб сохранится до {{expires_at}}.',
    parse_mode = 'HTML',
    buttons = '[{"row":0,"type":"callback","label":"🗃 Статус подписки","action":"subscription:status","enabled":true}]'::jsonb,
    version = version + 1,
    updated_at = now()
WHERE key = 'subscription_cancelled';

UPDATE bot_messages
SET text = '<b>НУЖНА ПОМОЩЬ?</b>

Напиши в поддержку — разберёмся с оплатой, подпиской или доступом.',
    parse_mode = 'HTML',
    buttons = (
        SELECT jsonb_agg(jsonb_set(button, '{label}', '"💬 Написать в поддержку"'::jsonb) ORDER BY position)
        FROM jsonb_array_elements(buttons) WITH ORDINALITY AS items(button, position)
    ),
    version = version + 1,
    updated_at = now()
WHERE key = 'help';

UPDATE bot_messages
SET text = '<b>ЧТО-ТО ПОШЛО НЕ ТАК</b>

Попробуй ещё раз. Если ошибка повторится — напиши в поддержку.',
    parse_mode = 'HTML',
    buttons = (
        SELECT jsonb_agg(jsonb_set(button, '{label}', '"💬 Написать в поддержку"'::jsonb) ORDER BY position)
        FROM jsonb_array_elements(buttons) WITH ORDINALITY AS items(button, position)
    ),
    version = version + 1,
    updated_at = now()
WHERE key = 'error';

UPDATE bot_messages
SET text = '<b>ПОРА ЗАХОДИТЬ В КЛУБ</b>

Внутри — материалы, разборы и окружение для тех, кто решил делать, а не откладывать.

Посмотри, что тебя ждёт 👇🏼',
    parse_mode = 'HTML',
    buttons = '[{"row":0,"type":"callback","label":"🗃 Что внутри","action":"club:about","enabled":true}]'::jsonb,
    version = version + 1,
    updated_at = now()
WHERE key = 'broadcast_intro';
