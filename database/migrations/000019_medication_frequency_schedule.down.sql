ALTER TABLE medication
  DROP COLUMN frequency_type,
  DROP COLUMN weekdays,
  DROP COLUMN interval_days,
  DROP COLUMN times,
  DROP COLUMN start_date,
  DROP COLUMN end_date;

ALTER TABLE medication
  ADD COLUMN periodicity_days integer NOT NULL DEFAULT 1,
  ADD COLUMN start_date       date NOT NULL DEFAULT CURRENT_DATE,
  ADD COLUMN repeat_count     integer NOT NULL DEFAULT 1,
  ADD COLUMN event_time       text NULL;

ALTER TABLE medication
  ALTER COLUMN periodicity_days DROP DEFAULT,
  ALTER COLUMN start_date DROP DEFAULT,
  ALTER COLUMN repeat_count DROP DEFAULT;
