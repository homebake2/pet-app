-- Idempotency key для POST /import/local-data: защита от повторного
-- переноса локальных данных при повторной отправке запроса. В отличие от
-- pet_idempotency_key хранит не id одной сущности, а весь результат
-- переноса (см. "Импорт локальных данных — Backend", раздел 6) — колонки
-- *_imported остаются NULL до завершения переноса.
CREATE TABLE import_local_data_idempotency_key (
  user_id          uuid NOT NULL,
  idempotency_key  uuid NOT NULL,
  pets_imported    integer,
  events_imported  integer,
  profile_imported boolean,
  created_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, idempotency_key)
);
