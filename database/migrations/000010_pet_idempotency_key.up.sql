-- Idempotency key для POST /pet: защита от дублирования питомца при
-- повторной отправке запроса (обрыв соединения после успешного создания).
CREATE TABLE pet_idempotency_key (
  user_id         uuid NOT NULL,
  idempotency_key uuid NOT NULL,
  pet_id          uuid,
  created_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, idempotency_key)
);
