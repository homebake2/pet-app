-- Напоминания: событие (event) становится только фактом, запланированные
-- события живут в двух новых сущностях — настройки напоминания
-- (reminder_plan) и напоминания по моментам расписания (reminder).
-- Лекарства и вакцинации ссылаются на настройки напоминания вместо
-- событий. Продуктивных данных нет, поэтому данные старых форм
-- (event.notifications_enabled, medication.event_ids, vaccination.next_event_id)
-- не переносятся в новые сущности, а удаляются.

CREATE TABLE reminder_plan (
    id             uuid PRIMARY KEY,
    pet_id         uuid NOT NULL REFERENCES pet(id) ON DELETE CASCADE,
    source         text NOT NULL,
    source_id      uuid NULL,
    type           text NOT NULL,
    value          jsonb NOT NULL,
    notes          text NULL,
    frequency_type text NOT NULL,
    weekdays       jsonb NULL,
    interval_days  integer NULL,
    times          text[] NOT NULL,
    start_date     date NOT NULL,
    end_date       date NULL,
    tz             text NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT reminder_plan_source_chk CHECK (source IN ('manual', 'medication', 'vaccination')),
    CONSTRAINT reminder_plan_frequency_chk CHECK (frequency_type IN ('once', 'daily', 'specific_days', 'every_n_days'))
);

CREATE INDEX reminder_plan_pet_id_idx ON reminder_plan (pet_id);

CREATE TABLE reminder (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id       uuid NOT NULL REFERENCES reminder_plan(id) ON DELETE CASCADE,
    remind_at     timestamptz NOT NULL,
    notes         text NULL,
    closed_at     timestamptz NULL,
    close_reason  text NULL,
    fact_event_id uuid NULL,
    CONSTRAINT reminder_close_reason_chk CHECK (close_reason IN ('done', 'skipped', 'deleted', 'replaced')),
    -- closed_at и close_reason задаются только вместе; fact_event_id — только при done.
    CONSTRAINT reminder_closed_pair_chk CHECK ((closed_at IS NULL) = (close_reason IS NULL)),
    CONSTRAINT reminder_fact_chk CHECK (fact_event_id IS NULL OR close_reason = 'done')
);

-- Момент расписания уникален в пределах настроек: по закрытой строке
-- пересоздание расписания определяет, что момент уже был закрыт.
CREATE UNIQUE INDEX reminder_plan_remind_at_idx ON reminder (plan_id, remind_at);
-- Выборки календаря и ближайших напоминаний работают только с
-- незавершёнными напоминаниями.
CREATE INDEX reminder_open_remind_at_idx ON reminder (remind_at) WHERE closed_at IS NULL;

-- Данные старых форм: наборы напоминаний лекарств были служебными строками
-- event (удалялись физически), события-напоминания вакцинаций и события с
-- включёнными уведомлениями в будущем — запланированные события старой
-- модели, которые теперь не являются фактами.
DELETE FROM event WHERE id::text IN (SELECT unnest(event_ids) FROM medication);
UPDATE event SET deleted_at = now()
 WHERE deleted_at IS NULL
   AND (id IN (SELECT next_event_id FROM vaccination WHERE next_event_id IS NOT NULL)
        OR (notifications_enabled AND date_time > now()));

ALTER TABLE event DROP COLUMN notifications_enabled;

ALTER TABLE medication DROP COLUMN event_ids;
-- Ссылка обнуляется, когда настройки удаляются (все напоминания закрыты,
-- удалён весь набор или питомец): на чтении «набора нет» — это null.
ALTER TABLE medication
    ADD COLUMN reminder_plan_id uuid NULL REFERENCES reminder_plan(id) ON DELETE SET NULL;

ALTER TABLE vaccination DROP COLUMN next_event_id;
ALTER TABLE vaccination
    ADD COLUMN next_plan_id uuid NULL REFERENCES reminder_plan(id) ON DELETE SET NULL;

-- Сопоставление local_id -> серверный id настроек напоминаний (и их
-- напоминаний) для повторного ответа POST /import/local-data с тем же
-- Idempotency-Key.
ALTER TABLE import_local_data_idempotency_key
    ADD COLUMN reminder_plans_imported integer,
    ADD COLUMN reminder_plans_mapping  jsonb;
