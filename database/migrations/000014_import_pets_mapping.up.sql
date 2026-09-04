-- Хранит сопоставление local_id -> серверный id питомца для завершённого
-- переноса POST /import/local-data (см. "Импорт локальных данных — Backend",
-- раздел 3, поле pets ответа), чтобы повторный запрос с тем же
-- Idempotency-Key возвращал то же сопоставление, а не только счётчики.
ALTER TABLE import_local_data_idempotency_key
  ADD COLUMN pets_mapping jsonb;
