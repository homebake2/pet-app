package database

import "database/sql"

// ConsentDocument — текущая версия документа и признак того, что именно эту
// версию принял пользователь.
type ConsentDocument struct {
	DocType  string
	Version  int
	Accepted bool
}

// ConsentAcceptance — принимаемая версия документа.
type ConsentAcceptance struct {
	DocType string
	Version int
}

// GetAuthState возвращает момент инвалидации токенов пользователя и признак
// того, что принятые версии совпадают с текущими по всем документам. Один
// запрос на оба признака, потому что вызывается на каждый защищённый запрос.
func GetAuthState(userID string) (invalidatedAt sql.NullTime, consentAccepted bool, err error) {
	err = DB.QueryRow(
		`SELECT u.tokens_invalidated_at,
		        NOT EXISTS (
		            SELECT 1 FROM consent_document d
		            WHERE NOT EXISTS (
		                SELECT 1 FROM user_consent c
		                WHERE c.user_id = u.id AND c.doc_type = d.doc_type AND c.version = d.version
		            )
		        )
		 FROM users u WHERE u.id=$1`,
		userID,
	).Scan(&invalidatedAt, &consentAccepted)
	return invalidatedAt, consentAccepted, err
}

// GetConsentDocuments возвращает текущие версии документов и статус их
// принятия пользователем.
func GetConsentDocuments(userID string) ([]ConsentDocument, error) {
	rows, err := DB.Query(
		`SELECT d.doc_type, d.version,
		        EXISTS (
		            SELECT 1 FROM user_consent c
		            WHERE c.user_id=$1 AND c.doc_type = d.doc_type AND c.version = d.version
		        )
		 FROM consent_document d
		 ORDER BY d.doc_type`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []ConsentDocument
	for rows.Next() {
		var doc ConsentDocument
		if err := rows.Scan(&doc.DocType, &doc.Version, &doc.Accepted); err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

// AcceptConsents записывает принятие переданных версий, если все они
// совпадают с текущими. Возвращает false, если хотя бы одна версия устарела
// или документ неизвестен; в этом случае ничего не записывается. Повторное
// принятие той же версии не создаёт дубликатов.
func AcceptConsents(userID string, accepted []ConsentAcceptance) (bool, error) {
	matched := false
	err := RunInTx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT doc_type, version FROM consent_document FOR SHARE`)
		if err != nil {
			return err
		}
		current := map[string]int{}
		for rows.Next() {
			var docType string
			var version int
			if err := rows.Scan(&docType, &version); err != nil {
				rows.Close()
				return err
			}
			current[docType] = version
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		if len(accepted) != len(current) {
			return nil
		}
		for _, a := range accepted {
			if v, ok := current[a.DocType]; !ok || v != a.Version {
				return nil
			}
		}
		for _, a := range accepted {
			if _, err := tx.Exec(
				`INSERT INTO user_consent (user_id, doc_type, version) VALUES ($1, $2, $3)
				 ON CONFLICT DO NOTHING`,
				userID, a.DocType, a.Version,
			); err != nil {
				return err
			}
		}
		matched = true
		return nil
	})
	return matched, err
}

// ConsentDocumentText — актуальный текст документа на одном языке.
type ConsentDocumentText struct {
	DocType string
	Version int
	Text    string
}

// GetConsentDocumentTexts возвращает актуальные версии и тексты всех
// документов на языке language.
func GetConsentDocumentTexts(language string) ([]ConsentDocumentText, error) {
	rows, err := DB.Query(
		`SELECT d.doc_type, d.version, t.text
		 FROM consent_document d
		 JOIN consent_document_text t ON t.doc_type = d.doc_type AND t.language = $1
		 ORDER BY d.doc_type`,
		language,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []ConsentDocumentText
	for rows.Next() {
		var doc ConsentDocumentText
		if err := rows.Scan(&doc.DocType, &doc.Version, &doc.Text); err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

// GetCurrentConsentVersion возвращает текущую версию документа.
func GetCurrentConsentVersion(docType string) (int, error) {
	var version int
	err := DB.QueryRow(`SELECT version FROM consent_document WHERE doc_type=$1`, docType).Scan(&version)
	return version, err
}
