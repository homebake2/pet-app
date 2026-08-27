package database

import (
	"database/sql"
	"log"
)

// Получить login по user_id
func GetLoginByUserID(userID string) (string, error) {
	query := `SELECT login FROM users WHERE id=$1`
	var login string
	err := DB.QueryRow(query, userID).Scan(&login)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil // не нашли пользователя
		}
		log.Printf("Error get login: %v", err)
		return "", err
	}
	return login, nil
}

// ClearRefreshToken - инвалидирует refresh_token и все ранее выданные
// access-токены пользователя (logout, см. requireUserID).
func ClearRefreshToken(login string) error {
	_, err := DB.Exec("UPDATE users SET refresh_token=NULL, tokens_invalidated_at=now() WHERE login=$1", login)
	return err
}

// GetTokensInvalidatedAt возвращает момент серверной инвалидации токенов
// пользователя (NULL, если токены никогда не отзывались).
func GetTokensInvalidatedAt(userID string) (sql.NullTime, error) {
	var invalidatedAt sql.NullTime
	err := DB.QueryRow("SELECT tokens_invalidated_at FROM users WHERE id=$1", userID).Scan(&invalidatedAt)
	return invalidatedAt, err
}

// DeleteUser удаляет пользователя по id; profile/pet/event удаляются каскадно
// через ON DELETE CASCADE (см. database/migrations/000001_init_schema.up.sql).
func DeleteUser(userID string) error {
	_, err := DB.Exec("DELETE FROM users WHERE id=$1", userID)
	return err
}
