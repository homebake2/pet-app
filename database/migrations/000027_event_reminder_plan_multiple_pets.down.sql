-- Возврат к одному питомцу у записи: у записи с несколькими питомцами
-- остаётся первый по position (остальные связи теряются).

ALTER TABLE reminder_plan ADD COLUMN pet_id uuid REFERENCES pet(id) ON DELETE CASCADE;
UPDATE reminder_plan SET pet_id = (
    SELECT rpp.pet_id FROM reminder_plan_pet rpp
    WHERE rpp.plan_id = reminder_plan.id
    ORDER BY rpp.position, rpp.pet_id
    LIMIT 1
);
DELETE FROM reminder_plan WHERE pet_id IS NULL;
ALTER TABLE reminder_plan ALTER COLUMN pet_id SET NOT NULL;
CREATE INDEX reminder_plan_pet_id_idx ON reminder_plan (pet_id);
DROP INDEX IF EXISTS reminder_plan_user_id_idx;
DROP TABLE reminder_plan_pet;
ALTER TABLE reminder_plan DROP COLUMN user_id;

ALTER TABLE event ADD COLUMN pet_id uuid REFERENCES pet(id) ON DELETE CASCADE;
UPDATE event SET pet_id = (
    SELECT ep.pet_id FROM event_pet ep
    WHERE ep.event_id = event.id
    ORDER BY ep.position, ep.pet_id
    LIMIT 1
);
DELETE FROM event WHERE pet_id IS NULL;
ALTER TABLE event ALTER COLUMN pet_id SET NOT NULL;
CREATE INDEX IF NOT EXISTS idx_event_pet_id ON event (pet_id);
DROP INDEX IF EXISTS event_user_date_idx;
DROP INDEX IF EXISTS event_type_date_idx;
DROP INDEX IF EXISTS event_user_idempotency_key_idx;
DROP TABLE event_pet;
ALTER TABLE event DROP COLUMN user_id;

CREATE UNIQUE INDEX event_pet_idempotency_key_idx
  ON event (pet_id, idempotency_key)
  WHERE idempotency_key IS NOT NULL;
CREATE INDEX event_pet_type_date_idx
  ON event (pet_id, type, date_time)
  WHERE deleted_at IS NULL;
