ALTER TABLE import_local_data_idempotency_key
    DROP COLUMN reminder_plans_mapping,
    DROP COLUMN reminder_plans_imported;

ALTER TABLE vaccination DROP COLUMN next_plan_id;
ALTER TABLE vaccination ADD COLUMN next_event_id uuid REFERENCES event(id) ON DELETE SET NULL;

ALTER TABLE medication DROP COLUMN reminder_plan_id;
ALTER TABLE medication ADD COLUMN event_ids text[] NOT NULL DEFAULT '{}';

ALTER TABLE event ADD COLUMN notifications_enabled boolean NOT NULL DEFAULT false;

DROP TABLE reminder;
DROP TABLE reminder_plan;
