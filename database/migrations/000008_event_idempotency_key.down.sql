DROP INDEX event_pet_idempotency_key_idx;
ALTER TABLE event DROP COLUMN idempotency_key;
