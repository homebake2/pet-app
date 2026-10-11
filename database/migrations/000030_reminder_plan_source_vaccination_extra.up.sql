-- Настройки напоминания: источник vaccination_extra — дополнительные
-- напоминания вакцинации. Основное напоминание на следующую вакцинацию
-- остаётся source = 'vaccination' (его id = vaccination.next_plan_id);
-- раньше дополнительные отличались только по id != next_plan_id.
ALTER TABLE reminder_plan DROP CONSTRAINT reminder_plan_source_chk;
ALTER TABLE reminder_plan ADD CONSTRAINT reminder_plan_source_chk
    CHECK (source IN ('manual', 'medication', 'vaccination', 'vaccination_extra'));

UPDATE reminder_plan p
SET source = 'vaccination_extra'
FROM vaccination v
WHERE p.source = 'vaccination'
  AND v.id = p.source_id
  AND p.id IS DISTINCT FROM v.next_plan_id;
