-- Согласие на обработку персональных данных и принятие условий использования.
-- consent_document хранит текущие версии документов; версию повышают вручную
-- (UPDATE или новая миграция), после чего у всех пользователей снова
-- запрашивается принятие. user_consent — журнал принятий, только добавление.
CREATE TABLE consent_document (
    doc_type text    PRIMARY KEY CHECK (doc_type IN ('terms', 'personal_data')),
    version  integer NOT NULL CHECK (version >= 1)
);

INSERT INTO consent_document (doc_type, version) VALUES ('terms', 1), ('personal_data', 1);

CREATE TABLE user_consent (
    user_id     uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    doc_type    text        NOT NULL REFERENCES consent_document(doc_type),
    version     integer     NOT NULL CHECK (version >= 1),
    accepted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, doc_type, version)
);

-- Версия документа personal_data, действовавшая при отправке предложения.
-- Предложения, созданные до появления согласия, получают 0.
ALTER TABLE suggestion ADD COLUMN consent_version integer NOT NULL DEFAULT 0;
