-- Поддержка гостевых пользователей (POST /auth/guest, см. handlers/auth.go).
-- guest_device_id уникален только среди гостевых строк — частичный индекс
-- гарантирует, что обычные пользователи (guest_device_id IS NULL) никогда
-- не конфликтуют друг с другом.
ALTER TABLE users ADD COLUMN guest_device_id text;
ALTER TABLE users ADD COLUMN is_guest boolean NOT NULL DEFAULT false;

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_guest_device_id
    ON users (guest_device_id)
    WHERE guest_device_id IS NOT NULL;
