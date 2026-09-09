-- Ветпаспорт (медкарта питомца): 5 новых pet_id-scoped сущностей с
-- soft-delete через deleted_at (см. artifacts, фича "Ведпаспорт"), плюс
-- поле pet.body_condition (не отдельная сущность).

ALTER TABLE pet
  ADD COLUMN body_condition text;

CREATE TABLE IF NOT EXISTS vaccination (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    pet_id                 uuid NOT NULL REFERENCES pet(id) ON DELETE CASCADE,
    name                   text NOT NULL,
    administered_date      date NOT NULL,
    next_date              date,
    administered_event_id  uuid REFERENCES event(id) ON DELETE SET NULL,
    next_event_id          uuid REFERENCES event(id) ON DELETE SET NULL,
    deleted_at             timestamptz,
    created_at             timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS disease (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    pet_id          uuid NOT NULL REFERENCES pet(id) ON DELETE CASCADE,
    name            text NOT NULL,
    diagnosed_date  date NOT NULL,
    status          text NOT NULL,
    note            text,
    deleted_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS vet_visit (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    pet_id      uuid NOT NULL REFERENCES pet(id) ON DELETE CASCADE,
    visit_date  date NOT NULL,
    reason      text NOT NULL,
    clinic      text,
    note        text,
    deleted_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS allergy (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    pet_id         uuid NOT NULL REFERENCES pet(id) ON DELETE CASCADE,
    allergen       text NOT NULL,
    reaction       text,
    detected_date  date,
    severity       text NOT NULL,
    note           text,
    deleted_at     timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS medication (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    pet_id            uuid NOT NULL REFERENCES pet(id) ON DELETE CASCADE,
    name              text NOT NULL,
    dosage            text NOT NULL,
    periodicity_days  integer NOT NULL,
    start_date        date NOT NULL,
    repeat_count      integer NOT NULL,
    event_time        text,
    -- event_ids хранится как text[] (строковые UUID), а не uuid[] — так его
    -- проще читать/писать через lib/pq (pq.Array с []string), без отдельного
    -- Valuer/Scanner для uuid.UUID.
    event_ids         text[] NOT NULL DEFAULT '{}',
    note              text,
    deleted_at        timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_vaccination_pet_id ON vaccination(pet_id);
CREATE INDEX IF NOT EXISTS idx_disease_pet_id ON disease(pet_id);
CREATE INDEX IF NOT EXISTS idx_vet_visit_pet_id ON vet_visit(pet_id);
CREATE INDEX IF NOT EXISTS idx_allergy_pet_id ON allergy(pet_id);
CREATE INDEX IF NOT EXISTS idx_medication_pet_id ON medication(pet_id);
