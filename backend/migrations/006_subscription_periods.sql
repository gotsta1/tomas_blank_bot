ALTER TABLE users
    ADD COLUMN selected_periodicity TEXT
    CHECK (selected_periodicity IN ('MONTHLY', 'PERIOD_90_DAYS', 'PERIOD_180_DAYS', 'PERIOD_YEAR'));

ALTER TABLE checkout_attempts ADD COLUMN periodicity TEXT;
UPDATE checkout_attempts SET periodicity = 'MONTHLY' WHERE periodicity IS NULL;
ALTER TABLE checkout_attempts
    ALTER COLUMN periodicity SET NOT NULL,
    ADD CONSTRAINT checkout_periodicity CHECK (periodicity IN ('MONTHLY', 'PERIOD_90_DAYS', 'PERIOD_180_DAYS', 'PERIOD_YEAR'));

ALTER TABLE subscriptions ADD COLUMN periodicity TEXT;
UPDATE subscriptions SET periodicity = 'MONTHLY' WHERE periodicity IS NULL;
ALTER TABLE subscriptions
    ALTER COLUMN periodicity SET NOT NULL,
    ADD CONSTRAINT subscription_periodicity CHECK (periodicity IN ('MONTHLY', 'PERIOD_90_DAYS', 'PERIOD_180_DAYS', 'PERIOD_YEAR'));

INSERT INTO bot_messages(key, name, text, buttons)
VALUES (
    'choose_plan',
    'Выбор тарифа',
    'Выберите срок подписки. После окончания выбранного периода Lava.top автоматически выполнит следующее списание.',
    '[{"label":"1 месяц","action":"plan:monthly","type":"callback","row":0,"enabled":true},{"label":"3 месяца","action":"plan:90d","type":"callback","row":1,"enabled":true},{"label":"6 месяцев","action":"plan:180d","type":"callback","row":2,"enabled":true},{"label":"12 месяцев","action":"plan:year","type":"callback","row":3,"enabled":true},{"label":"Назад","action":"nav:start","type":"callback","row":4,"enabled":true}]'
);

UPDATE bot_messages
SET text = 'Тариф: {{plan_name}}
Период: {{period}}
Автопродление: включено

Точная сумма и доступные способы показаны на странице Lava.top.',
    version = version + 1,
    updated_at = now()
WHERE key = 'payment_created';
