-- Хранит сопоставление local_id -> серверный id события для завершённого
-- переноса POST /import/local-data (см. "Импорт локальных данных — Backend",
-- раздел 3, поле events ответа), симметрично pets_mapping (см.
-- 000014_import_pets_mapping.up.sql), чтобы повторный запрос с тем же
-- Idempotency-Key возвращал то же сопоставление, а не только счётчики.
ALTER TABLE import_local_data_idempotency_key
  ADD COLUMN events_mapping jsonb;
