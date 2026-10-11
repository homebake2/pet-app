-- Возврат к type = 'other' со строкой value.label ("Вакцинация: " + name,
-- не длиннее 50 символов).
UPDATE event e
SET type = 'other',
    value = jsonb_build_object('label', left('Вакцинация: ' || (e.value ->> 'name'), 50))
WHERE e.type = 'vaccination';

UPDATE reminder_plan p
SET type = 'other',
    value = jsonb_build_object('label', left('Вакцинация: ' || (p.value ->> 'name'), 50))
WHERE p.type = 'vaccination';
