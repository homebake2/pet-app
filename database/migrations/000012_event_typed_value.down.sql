-- Симметрична up и так же разрушительна: данные не восстанавливаются.
-- Откат не предназначен для прода — он возвращает структуру, а не события.
DELETE FROM event;

DROP INDEX IF EXISTS event_pet_type_date_idx;

ALTER TABLE event DROP COLUMN value;
ALTER TABLE event ADD COLUMN value text NOT NULL;
