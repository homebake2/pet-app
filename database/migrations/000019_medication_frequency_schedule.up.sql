-- Лекарства: замена модели "фиксированный интервал + repeat_count + одно
-- event_time" на модель частотных паттернов Apple Health "Здоровье"
-- (frequency_type/weekdays/interval_days/times/start_date/end_date), см.
-- artifacts/PET/pages/vetpassport/lekarstva-backend.md. Нет продовых данных
-- (фича не в проде) — данные не переносятся, только пересоздание колонок.

ALTER TABLE medication
  DROP COLUMN periodicity_days,
  DROP COLUMN repeat_count,
  DROP COLUMN event_time,
  DROP COLUMN start_date;

ALTER TABLE medication
  ADD COLUMN frequency_type text NOT NULL DEFAULT 'as_needed',
  ADD COLUMN weekdays        jsonb NULL,
  ADD COLUMN interval_days   integer NULL,
  ADD COLUMN times           jsonb NULL,
  ADD COLUMN start_date      date NULL,
  ADD COLUMN end_date        date NULL;

ALTER TABLE medication
  ALTER COLUMN frequency_type DROP DEFAULT;

-- event_ids уже text[] NOT NULL DEFAULT '{}' без ограничения длины на уровне
-- БД (проверка "не более 60" — только в приложении, см. 000017), потолок
-- не менялся при вводе 000017, поэтому раньше был неограничен на уровне БД —
-- ничего дополнительно менять на уровне схемы не требуется.
