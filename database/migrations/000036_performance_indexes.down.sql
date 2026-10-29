DROP INDEX IF EXISTS vaccination_administered_event_id_idx;
DROP INDEX IF EXISTS vaccination_next_plan_id_idx;
DROP INDEX IF EXISTS medication_reminder_plan_id_idx;
DROP INDEX IF EXISTS file_object_key_idx;
DROP INDEX IF EXISTS file_owner_idx;
CREATE INDEX file_owner_confirmed_idx ON file (owner_type, owner_id) WHERE confirmed_at IS NOT NULL;
