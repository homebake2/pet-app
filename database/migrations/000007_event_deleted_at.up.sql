-- Мягкое удаление событий, аналогично pet.deleted_at.
ALTER TABLE event ADD COLUMN deleted_at timestamptz NULL;
