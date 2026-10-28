CREATE TABLE app_config (
    id boolean PRIMARY KEY DEFAULT true CHECK (id),
    about_me_url text NOT NULL CHECK (about_me_url LIKE 'https://%'),
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO app_config (id, about_me_url) VALUES (true, 'https://example.com');
