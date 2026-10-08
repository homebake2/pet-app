-- created_at пользователя: момент регистрации. Для уже существующих строк
-- значением станет момент применения миграции (реальная дата регистрации неизвестна).
ALTER TABLE users ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();
