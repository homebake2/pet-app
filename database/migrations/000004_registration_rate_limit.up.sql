-- Ограничение частоты авто-регистрации по IP: не более 3 регистраций в сутки
-- с одного адреса (см. handlers/auth.go, authenticateOrRegister).
CREATE TABLE IF NOT EXISTS registration_rate_limit (
    ip    inet NOT NULL,
    day   date NOT NULL,
    count integer NOT NULL DEFAULT 0,
    PRIMARY KEY (ip, day)
);
