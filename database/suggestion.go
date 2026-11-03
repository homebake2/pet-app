package database

// CreateSuggestion сохраняет предложение с сайта и возвращает его id. Вместе с
// предложением записывается текущая версия документа personal_data — версия,
// на которую согласился отправитель.
func CreateSuggestion(userID, category, title, body, contact string) (string, error) {
	var id string
	err := DB.QueryRow(
		`INSERT INTO suggestion (user_id, category, title, body, contact, consent_version)
		 VALUES ($1, $2, $3, $4, $5, (SELECT version FROM consent_document WHERE doc_type = 'personal_data'))
		 RETURNING id`,
		userID, category, title, body, contact,
	).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, nil
}
