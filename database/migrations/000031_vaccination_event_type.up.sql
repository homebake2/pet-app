-- Системный тип события vaccination: факт на дату введения, напоминание на
-- следующую вакцинацию и дополнительные напоминания вакцинации получают
-- type = 'vaccination' и value = {name} (у дополнительных — ещё
-- vaccination_at, момент следующей вакцинации в RFC3339, UTC), вместо
-- type = 'other' со строкой value.label.

-- Факты на дату введения.
UPDATE event e
SET type = 'vaccination',
    value = jsonb_build_object('name', v.name)
FROM vaccination v
WHERE e.id = v.administered_event_id;

-- Напоминания на следующую вакцинацию.
UPDATE reminder_plan p
SET type = 'vaccination',
    value = jsonb_build_object('name', v.name)
FROM vaccination v
WHERE p.source = 'vaccination'
  AND p.source_id = v.id;

-- Дополнительные напоминания: vaccination_at — момент next_date + next_time
-- в часовом поясе настроек напоминания; при отсутствии next_date — только name.
UPDATE reminder_plan p
SET type = 'vaccination',
    value = CASE
        WHEN v.next_date IS NULL THEN jsonb_build_object('name', v.name)
        ELSE jsonb_build_object(
            'name', v.name,
            'vaccination_at', to_char(
                ((v.next_date + COALESCE(v.next_time, TIME '00:00')) AT TIME ZONE p.tz) AT TIME ZONE 'UTC',
                'YYYY-MM-DD"T"HH24:MI:SS"Z"'
            )
        )
    END
FROM vaccination v
WHERE p.source = 'vaccination_extra'
  AND p.source_id = v.id;
