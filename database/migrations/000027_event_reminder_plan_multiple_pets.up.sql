-- Несколько питомцев в событии и в настройках напоминания: запись одна, а
-- связь с питомцами хранится в таблицах event_pet и reminder_plan_pet. Владелец
-- записи — пользователь (user_id), а не питомец. Столбцы event.pet_id и
-- reminder_plan.pet_id удаляются: существующие связи переносятся в новые
-- таблицы перед удалением столбцов.
--
-- position задаёт порядок питомцев записи в ответах: порядок передачи при
-- создании, затем порядок привязки.

-- event: владелец и связь с питомцами.
ALTER TABLE event ADD COLUMN user_id uuid REFERENCES users(id);
UPDATE event SET user_id = pet.user_id FROM pet WHERE pet.id = event.pet_id;
ALTER TABLE event ALTER COLUMN user_id SET NOT NULL;

CREATE TABLE event_pet (
    event_id uuid    NOT NULL REFERENCES event(id) ON DELETE CASCADE,
    pet_id   uuid    NOT NULL REFERENCES pet(id) ON DELETE CASCADE,
    position integer NOT NULL DEFAULT 0,
    PRIMARY KEY (event_id, pet_id)
);
INSERT INTO event_pet (event_id, pet_id, position) SELECT id, pet_id, 1 FROM event;

-- Выборки «по питомцу» идут через связь.
CREATE INDEX event_pet_pet_id_idx ON event_pet (pet_id, event_id);

-- Ключ идемпотентности теперь уникален в пределах пользователя. Если один
-- ключ использовался у разных питомцев одного пользователя, ключ остаётся
-- только у самого раннего события.
DROP INDEX IF EXISTS event_pet_idempotency_key_idx;
UPDATE event SET idempotency_key = NULL
 WHERE id IN (
    SELECT id FROM (
        SELECT id, row_number() OVER (PARTITION BY user_id, idempotency_key ORDER BY date_time, id) AS rn
        FROM event
        WHERE idempotency_key IS NOT NULL
    ) ranked
    WHERE rn > 1
 );
CREATE UNIQUE INDEX event_user_idempotency_key_idx
  ON event (user_id, idempotency_key)
  WHERE idempotency_key IS NOT NULL;

-- Агрегация графиков и календарь пользователя.
DROP INDEX IF EXISTS event_pet_type_date_idx;
ALTER TABLE event DROP COLUMN pet_id;
CREATE INDEX event_type_date_idx ON event (type, date_time) WHERE deleted_at IS NULL;
CREATE INDEX event_user_date_idx ON event (user_id, date_time) WHERE deleted_at IS NULL;

-- reminder_plan: владелец и связь с питомцами.
ALTER TABLE reminder_plan ADD COLUMN user_id uuid REFERENCES users(id);
UPDATE reminder_plan SET user_id = pet.user_id FROM pet WHERE pet.id = reminder_plan.pet_id;
ALTER TABLE reminder_plan ALTER COLUMN user_id SET NOT NULL;

CREATE TABLE reminder_plan_pet (
    plan_id  uuid    NOT NULL REFERENCES reminder_plan(id) ON DELETE CASCADE,
    pet_id   uuid    NOT NULL REFERENCES pet(id) ON DELETE CASCADE,
    position integer NOT NULL DEFAULT 0,
    PRIMARY KEY (plan_id, pet_id)
);
INSERT INTO reminder_plan_pet (plan_id, pet_id, position) SELECT id, pet_id, 1 FROM reminder_plan;

CREATE INDEX reminder_plan_pet_pet_id_idx ON reminder_plan_pet (pet_id, plan_id);

DROP INDEX IF EXISTS reminder_plan_pet_id_idx;
ALTER TABLE reminder_plan DROP COLUMN pet_id;
CREATE INDEX reminder_plan_user_id_idx ON reminder_plan (user_id);
