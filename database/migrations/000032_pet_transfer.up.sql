-- История передач питомцев между пользователями (см. database/pet_transfer.go).
-- pet_id без внешнего ключа: запись истории переживает физическое удаление
-- питомца при удалении аккаунта. Ссылки на пользователей обнуляются при
-- удалении аккаунта участника, снимок питомца (pet_name, pet_species) остаётся.
CREATE TABLE pet_transfer (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    pet_id       uuid        NOT NULL,
    pet_name     text        NOT NULL,
    pet_species  text        NOT NULL,
    from_user_id uuid        NULL REFERENCES users(id) ON DELETE SET NULL,
    to_user_id   uuid        NULL REFERENCES users(id) ON DELETE SET NULL,
    code         text        NOT NULL,
    status       text        NOT NULL CHECK (status IN ('pending', 'accepted', 'declined', 'cancelled', 'expired')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    resolved_at  timestamptz NULL
);

-- У питомца не более одной активной передачи.
CREATE UNIQUE INDEX pet_transfer_pending_pet_idx ON pet_transfer (pet_id) WHERE status = 'pending';

-- Коды активных передач не повторяются.
CREATE UNIQUE INDEX pet_transfer_pending_code_idx ON pet_transfer (code) WHERE status = 'pending';

-- Неудачные обращения к эндпоинтам с кодом передачи: учёт для ограничения
-- подбора кода (не более 10 в час на пользователя).
CREATE TABLE pet_transfer_code_failure (
    id         bigserial   PRIMARY KEY,
    user_id    uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX pet_transfer_code_failure_user_idx ON pet_transfer_code_failure (user_id, created_at);
