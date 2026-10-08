-- Баннеры: основная таблица содержит только поля, не зависящие от языка.
-- Переводимые поля вынесены в *_translation по общему шаблону локализации контента.
CREATE TABLE banner (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    layout      text NOT NULL CHECK (layout IN ('sheet', 'cover', 'dialog', 'carousel', 'poster')),
    tone        text CHECK (tone IS NULL OR tone IN ('info', 'attention', 'negative', 'positive', 'promo')),
    is_show     boolean NOT NULL DEFAULT true,
    starts_at   timestamptz,
    ends_at     timestamptz,
    priority    integer NOT NULL DEFAULT 0,
    frequency   text NOT NULL CHECK (frequency IN ('once', 'every_launch')),
    cta_action  text CHECK (cta_action IS NULL OR cta_action IN ('url', 'deeplink', 'store')),
    cta_target  text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT banner_window_check CHECK (starts_at IS NULL OR ends_at IS NULL OR ends_at > starts_at)
);

CREATE TABLE banner_translation (
    banner_id        uuid NOT NULL REFERENCES banner(id) ON DELETE CASCADE,
    locale           text NOT NULL CHECK (locale IN ('ru', 'en')),
    overline         text,
    title            text,
    body             text,
    badge            text,
    image_url        text,
    image_blurhash   text,
    cta_label        text,
    secondary_label  text,
    PRIMARY KEY (banner_id, locale)
);

CREATE TABLE banner_slide (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    banner_id  uuid NOT NULL REFERENCES banner(id) ON DELETE CASCADE,
    position   integer NOT NULL CHECK (position >= 1),
    CONSTRAINT banner_slide_position_unique UNIQUE (banner_id, position)
);

CREATE TABLE banner_slide_translation (
    slide_id        uuid NOT NULL REFERENCES banner_slide(id) ON DELETE CASCADE,
    locale          text NOT NULL CHECK (locale IN ('ru', 'en')),
    title           text,
    body            text,
    image_url       text,
    image_blurhash  text,
    PRIMARY KEY (slide_id, locale)
);

-- Одно правило таргетинга каждого типа на баннер. type намеренно без CHECK:
-- неизвестный тип делает баннер невалидным на чтении (его исключает код),
-- а новый тип правила не требует миграции.
CREATE TABLE banner_targeting (
    banner_id  uuid NOT NULL REFERENCES banner(id) ON DELETE CASCADE,
    type       text NOT NULL,
    params     jsonb NOT NULL,
    PRIMARY KEY (banner_id, type)
);

-- Персональное ограничение показа: если у баннера есть хотя бы одна строка,
-- он отдаётся только перечисленным пользователям.
CREATE TABLE banner_user (
    banner_id  uuid NOT NULL REFERENCES banner(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (banner_id, user_id)
);

CREATE INDEX banner_user_user_id_idx ON banner_user (user_id);
