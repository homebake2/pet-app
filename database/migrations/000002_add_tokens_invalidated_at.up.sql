-- Позволяет серверно инвалидировать уже выданные access-токены
-- при logout и удалении аккаунта (см. requireUserID в handlers/response.go).
ALTER TABLE users ADD COLUMN tokens_invalidated_at timestamptz;
