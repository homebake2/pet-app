-- Схема для myauthservice (pet-app)

CREATE TABLE IF NOT EXISTS users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    login         text UNIQUE NOT NULL,
    password      text NOT NULL,
    refresh_token text
);

CREATE TABLE IF NOT EXISTS profile (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    first_name  text NOT NULL,
    middle_name text,
    last_name   text,
    email       text,
    phone       text,
    UNIQUE (user_id)
);

CREATE TABLE IF NOT EXISTS pet (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id  uuid NOT NULL REFERENCES profile(id) ON DELETE CASCADE,
    breed       text,
    name        text NOT NULL,
    species     text NOT NULL,
    birth_date  date,
    gender      text,
    color       text,
    sterilized  boolean,
    habitation  text,
    notes       text,
    icon        text,
    deleted_at  timestamptz
);

CREATE TABLE IF NOT EXISTS event (
    id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    pet_id    uuid NOT NULL REFERENCES pet(id) ON DELETE CASCADE,
    date_time timestamptz NOT NULL,
    type      text NOT NULL,
    notes     text,
    value     text NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_pet_profile_id ON pet(profile_id);
CREATE INDEX IF NOT EXISTS idx_event_pet_id ON event(pet_id);
