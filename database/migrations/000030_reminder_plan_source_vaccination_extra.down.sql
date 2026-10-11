UPDATE reminder_plan SET source = 'vaccination' WHERE source = 'vaccination_extra';
ALTER TABLE reminder_plan DROP CONSTRAINT reminder_plan_source_chk;
ALTER TABLE reminder_plan ADD CONSTRAINT reminder_plan_source_chk
    CHECK (source IN ('manual', 'medication', 'vaccination'));
