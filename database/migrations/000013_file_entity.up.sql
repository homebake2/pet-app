-- Единая таблица `file` для generic-механизма файлов сущностей (см.
-- «Общие требования: Файлы сущностей»). Строка с confirmed_at IS NULL —
-- незавершённая загрузка (presigned PUT URL выдан, байты ещё не
-- подтверждены) и не должна учитываться при чтении/подсчёте кардинальности.
CREATE TABLE file (
    id            uuid PRIMARY KEY,
    owner_type    text NOT NULL,
    owner_id      uuid NOT NULL,
    user_id       uuid NOT NULL,
    object_key    text NOT NULL,
    content_type  text NOT NULL,
    position      integer NULL,
    confirmed_at  timestamptz NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- Ускоряет как поиск подтверждённого файла(ов) владельца при чтении
-- (photo_url и т.п.), так и подсчёт кардинальности при подтверждении.
CREATE INDEX file_owner_confirmed_idx ON file (owner_type, owner_id) WHERE confirmed_at IS NOT NULL;
