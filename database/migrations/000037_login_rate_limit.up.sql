-- Ограничение неудачных попыток входа: счётчик неверных паролей по ключу
-- (логин или IP) в скользящем окне (см. handlers/login_rate_limit.go).
CREATE TABLE IF NOT EXISTS login_rate_limit (
    key          text        PRIMARY KEY,
    window_start timestamptz NOT NULL DEFAULT now(),
    failures     integer     NOT NULL DEFAULT 0
);
