-- Расширение import_local_data_idempotency_key для переноса ветпаспорта
-- (см. 000014_import_pets_mapping.up.sql / 000016_import_events_mapping.up.sql):
-- по одной паре "количество"/"сопоставление local_id -> серверный id" на
-- каждую из 5 новых сущностей, симметрично pets_imported/pets_mapping.
ALTER TABLE import_local_data_idempotency_key
  ADD COLUMN vaccinations_imported integer,
  ADD COLUMN diseases_imported     integer,
  ADD COLUMN vet_visits_imported   integer,
  ADD COLUMN allergies_imported    integer,
  ADD COLUMN medications_imported  integer,
  ADD COLUMN vaccinations_mapping  jsonb,
  ADD COLUMN diseases_mapping      jsonb,
  ADD COLUMN vet_visits_mapping    jsonb,
  ADD COLUMN allergies_mapping     jsonb,
  ADD COLUMN medications_mapping   jsonb;
