package database

// CreateSuggestion сохраняет предложение с сайта и возвращает его id.
func CreateSuggestion(userID, category, title, body, contact string) (string, error) {
	var id string
	err := DB.QueryRow(
		`INSERT INTO suggestion (user_id, category, title, body, contact)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id`,
		userID, category, title, body, contact,
	).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, nil
}
