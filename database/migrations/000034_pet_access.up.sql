-- Доступ к питомцу (см. database/pet_access.go): владелец выдаёт другому
-- пользователю роль full (полный доступ) или view (только просмотр). Владелец
-- питомца в pet_access не хранится — его роль определяется pet.user_id.

-- Действующие доступы. Первичный ключ — у участника не более одной роли у
-- питомца.
CREATE TABLE pet_access (
    pet_id     uuid        NOT NULL REFERENCES pet(id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       text        NOT NULL CHECK (role IN ('full', 'view')),
    granted_by uuid        NULL REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (pet_id, user_id)
);

CREATE INDEX pet_access_user_idx ON pet_access (user_id);

-- Приглашения к доступу и их история. Ссылки на пользователей обнуляются при
-- удалении аккаунта, запись истории остаётся.
CREATE TABLE pet_access_invite (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    from_user_id uuid        NULL REFERENCES users(id) ON DELETE SET NULL,
    to_user_id   uuid        NULL REFERENCES users(id) ON DELETE SET NULL,
    role         text        NOT NULL CHECK (role IN ('full', 'view')),
    code         text        NOT NULL,
    status       text        NOT NULL CHECK (status IN ('pending', 'accepted', 'declined', 'cancelled', 'expired')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    resolved_at  timestamptz NULL
);

-- Коды активных приглашений не повторяются (коды активных передач проверяет
-- генерация).
CREATE UNIQUE INDEX pet_access_invite_pending_code_idx ON pet_access_invite (code) WHERE status = 'pending';

CREATE INDEX pet_access_invite_from_user_idx ON pet_access_invite (from_user_id) WHERE status = 'pending';

-- Питомцы приглашения. pet_id без внешнего ключа: история переживает
-- физическое удаление питомца; снимок имени и вида остаётся.
CREATE TABLE pet_access_invite_pet (
    invite_id   uuid NOT NULL REFERENCES pet_access_invite(id) ON DELETE CASCADE,
    pet_id      uuid NOT NULL,
    pet_name    text NOT NULL,
    pet_species text NOT NULL,
    PRIMARY KEY (invite_id, pet_id)
);

CREATE INDEX pet_access_invite_pet_pet_idx ON pet_access_invite_pet (pet_id);

-- Уведомления о потере доступа: не более одного на пару «пользователь —
-- питомец», новое заменяет прежнее.
CREATE TABLE pet_access_notice (
    user_id     uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    pet_id      uuid        NOT NULL,
    pet_name    text        NOT NULL,
    pet_species text        NOT NULL,
    reason      text        NOT NULL CHECK (reason IN ('revoked', 'pet_deleted', 'pet_transferred')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, pet_id)
);

-- Автор записи Ветпаспорта: пользователь, создавший её. Для событий и
-- настроек напоминаний автор — event.user_id и reminder_plan.user_id. Если
-- аккаунт автора удалён, автором считается владелец питомца.
ALTER TABLE vaccination ADD COLUMN user_id uuid NULL REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE disease     ADD COLUMN user_id uuid NULL REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE vet_visit   ADD COLUMN user_id uuid NULL REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE allergy     ADD COLUMN user_id uuid NULL REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE medication  ADD COLUMN user_id uuid NULL REFERENCES users(id) ON DELETE SET NULL;

UPDATE vaccination v SET user_id = p.user_id FROM pet p WHERE p.id = v.pet_id;
UPDATE disease d     SET user_id = p.user_id FROM pet p WHERE p.id = d.pet_id;
UPDATE vet_visit t   SET user_id = p.user_id FROM pet p WHERE p.id = t.pet_id;
UPDATE allergy a     SET user_id = p.user_id FROM pet p WHERE p.id = a.pet_id;
UPDATE medication m  SET user_id = p.user_id FROM pet p WHERE p.id = m.pet_id;
