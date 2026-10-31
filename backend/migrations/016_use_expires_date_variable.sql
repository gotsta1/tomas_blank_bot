UPDATE bot_messages
SET text = replace(text, '{{expires_at}}', '{{expires_date}}'),
    buttons = replace(buttons::text, '{{expires_at}}', '{{expires_date}}')::jsonb,
    version = version + 1,
    updated_at = now()
WHERE text LIKE '%{{expires_at}}%'
   OR buttons::text LIKE '%{{expires_at}}%';
