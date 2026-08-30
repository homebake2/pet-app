-- Idempotency key для POST /events: защита от дублирования события при
-- повторной отправке запроса (обрыв соединения, double-tap).
ALTER TABLE event ADD COLUMN idempotency_key text NULL;

CREATE UNIQUE INDEX event_pet_idempotency_key_idx
  ON event (pet_id, idempotency_key)
  WHERE idempotency_key IS NOT NULL;
