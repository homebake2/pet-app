-- Нормализация login: сравнение/поиск регистронезависимы и с обрезкой
-- пробелов, поэтому "user" и "User" должны считаться одним и тем же логином.
-- Уникальный индекс закрепляет это на уровне схемы, а не только в коде.
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_login_lower_trim ON users (lower(trim(login)));
