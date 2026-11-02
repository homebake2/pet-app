-- Предложения с формы «Новые идеи» на сайте (POST /web/suggestions).
-- user_id — не внешний ключ: это `local`, `anonymous` или uuid аккаунта, и
-- предложение переживает удаление аккаунта.
CREATE TABLE suggestion (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    text        NOT NULL,
    category   text        NOT NULL,
    title      text        NOT NULL,
    body       text        NOT NULL DEFAULT '',
    contact    text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
