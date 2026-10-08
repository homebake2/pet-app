-- Idempotency key для POST /pet/{id}/{vaccinations,diseases,vet-visits,
-- allergies,medications}: тот же механизм, что у POST /events (см.
-- 000008_event_idempotency_key) — ключ хранится прямо в строке сущности,
-- уникальность на пару (pet_id, idempotency_key), индекс частичный, чтобы
-- не конфликтовали записи без ключа.
ALTER TABLE vaccination ADD COLUMN idempotency_key text NULL;
ALTER TABLE disease     ADD COLUMN idempotency_key text NULL;
ALTER TABLE vet_visit   ADD COLUMN idempotency_key text NULL;
ALTER TABLE allergy     ADD COLUMN idempotency_key text NULL;
ALTER TABLE medication  ADD COLUMN idempotency_key text NULL;

CREATE UNIQUE INDEX vaccination_pet_idempotency_key_idx
  ON vaccination (pet_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX disease_pet_idempotency_key_idx
  ON disease (pet_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX vet_visit_pet_idempotency_key_idx
  ON vet_visit (pet_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX allergy_pet_idempotency_key_idx
  ON allergy (pet_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX medication_pet_idempotency_key_idx
  ON medication (pet_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
