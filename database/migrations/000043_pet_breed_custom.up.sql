-- Собственное название породы: хранится только при breed = 'other'. Остальное
-- значение breed — идентификатор породы из клиентского справочника.
ALTER TABLE pet ADD COLUMN breed_custom text;

ALTER TABLE pet ADD CONSTRAINT pet_breed_custom_only_for_other
    CHECK (breed_custom IS NULL OR breed = 'other');
