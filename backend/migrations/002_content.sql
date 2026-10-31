INSERT INTO bot_messages(key,name,text,buttons) VALUES
('start','Приветствие','Добро пожаловать в клуб!','[{"label": "Вступить в клуб", "action": "club:join", "type": "callback", "row": 0, "enabled": true}, {"label": "Подробнее о клубе", "action": "club:about", "type": "callback", "row": 1, "enabled": true}]'),
('about_club','О клубе','Закрытое сообщество, полезные материалы и общение. Присоединяйтесь!','[{"label": "Вступить в клуб", "action": "club:join", "type": "callback", "row": 0, "enabled": true}]'),
('choose_payment_region','Регион оплаты','Выберите карту для оплаты подписки.','[{"label": "Российская карта", "action": "pay:region:ru", "type": "callback", "row": 0, "enabled": true}, {"label": "Зарубежная карта", "action": "pay:region:foreign", "type": "callback", "row": 1, "enabled": true}, {"label": "Назад", "action": "nav:start", "type": "callback", "row": 2, "enabled": true}]'),
('choose_ru_payment','Оплата в рублях','Выберите способ. Доступность СБП и автопродления зависит от вариантов на странице Lava.top.','[{"label": "СБП", "action": "pay:ru:sbp", "type": "callback", "row": 0, "enabled": true}, {"label": "Картой", "action": "pay:ru:card", "type": "callback", "row": 1, "enabled": true}, {"label": "Назад", "action": "club:join", "type": "callback", "row": 2, "enabled": true}]'),
('choose_foreign_payment','Оплата зарубежной картой','Выберите способ. Apple Pay доступен только если показан на странице оплаты Lava.top.','[{"label": "PayPal", "action": "pay:foreign:paypal", "type": "callback", "row": 0, "enabled": true}, {"label": "Картой", "action": "pay:foreign:card", "type": "callback", "row": 1, "enabled": true}, {"label": "Apple Pay", "action": "pay:foreign:applepay", "type": "callback", "row": 2, "enabled": true}, {"label": "Назад", "action": "club:join", "type": "callback", "row": 3, "enabled": true}]'),
('email_request','Запрос email','Отправьте настоящий email для оформления подписки и получения чека.','[{"label": "Назад", "action": "club:join", "type": "callback", "row": 0, "enabled": true}]'),
('email_invalid','Некорректный email','Не удалось распознать email. Отправьте адрес в формате name@example.com.','[]'),
('payment_generating','Создание оплаты','Генерируем ссылку на оплату...','[]'),
('payment_created','Ссылка на оплату','Тариф: {{plan_name}}
Стоимость: {{price}}
Период: {{period}}
Автопродление: включено

Точная сумма и доступные способы показаны на странице Lava.top.','[{"label": "Оплатить", "action": "{{payment_url}}", "type": "url", "row": 0, "enabled": true}, {"label": "Оплатил", "action": "pay:check", "type": "callback", "row": 1, "enabled": true}]'),
('payment_pending','Ожидание оплаты','Подождите, оплата ещё не поступила.','[{"label": "Проверить ещё раз", "action": "pay:check", "type": "callback", "row": 0, "enabled": true}]'),
('payment_uncertain','Проверка создания оплаты','Запрос на оплату уже обрабатывается. Не создавайте повторную подписку. Если ссылка не появилась, обратитесь в поддержку.','[{"label": "Поддержка", "action": "{{support_url}}", "type": "url", "row": 0, "enabled": true}]'),
('payment_success','Оплата подтверждена','Подписка оплачена до {{expires_at}}. Отправьте заявку на вступление — бот проверит ваш доступ.','[{"label": "Вступить в приватный канал", "action": "{{invite_url}}", "type": "url", "row": 0, "enabled": true}]'),
('subscription_status_active','Активная подписка','Статус: активна
Подписка действует до: {{expires_at}}
Осталось дней: {{days_left}}
Автопродление: {{auto_renew}}','[{"label": "Вступить в канал", "action": "{{invite_url}}", "type": "url", "row": 0, "enabled": true}, {"label": "Отключить автопродление", "action": "subscription:cancel", "type": "callback", "row": 1, "enabled": true}]'),
('subscription_status_expired','Подписка истекла','Статус: подписка истекла.','[{"label": "Купить подписку", "action": "club:join", "type": "callback", "row": 0, "enabled": true}]'),
('subscription_status_new','Нет подписки','У вас пока нет оплаченной подписки.','[{"label": "Купить подписку", "action": "club:join", "type": "callback", "row": 0, "enabled": true}]'),
('subscription_cancel_confirm','Подтверждение отмены','Вы уверены, что хотите отключить автопродление? Доступ сохранится до конца оплаченного периода.','[{"label": "Да, отключить", "action": "subscription:cancel:confirm", "type": "callback", "row": 0, "enabled": true}, {"label": "Назад", "action": "subscription:status", "type": "callback", "row": 1, "enabled": true}]'),
('subscription_cancelled','Автопродление отключено','Автопродление отключено. Оплаченный доступ действует до {{expires_at}}.','[{"label": "Статус", "action": "subscription:status", "type": "callback", "row": 0, "enabled": true}]'),
('help','Помощь','Если возникли вопросы, напишите в поддержку.','[{"label": "Поддержка", "action": "{{support_url}}", "type": "url", "row": 0, "enabled": true}]'),
('error','Ошибка','Не удалось выполнить действие. Попробуйте позже или обратитесь в поддержку.','[{"label": "Поддержка", "action": "{{support_url}}", "type": "url", "row": 0, "enabled": true}]'),
('broadcast_intro','Рассылка: приглашение','В клубе вас ждут новые материалы. Посмотрите подробности и присоединяйтесь.','[{"label": "Подробнее о клубе", "action": "club:about", "type": "callback", "row": 0, "enabled": true}]'),
('command_start','Меню: start','Начать заново','[]'),
('command_buy','Меню: buy','Купить подписку','[]'),
('command_status','Меню: status','Моя подписка','[]'),
('command_help','Меню: help','Поддержка','[]'),
('renew_on','Автопродление: включено','включено','[]'),
('renew_off','Автопродление: отключено','отключено','[]');
