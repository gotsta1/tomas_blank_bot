UPDATE bot_messages
SET text = '<b>🗃 Подписка:</b>

🟢 Подписка активна
Действует до: {{expires_date}}
Дней до окончания доступа: {{days_left}}
Автопродление: {{auto_renew}}',
    parse_mode = 'HTML',
    version = version + 1,
    updated_at = now()
WHERE key = 'subscription_status_active';
