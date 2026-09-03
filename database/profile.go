package database

import (
	"database/sql"
	"log"
	"myauthservice/models"

	"github.com/google/uuid"
)

// Получить профиль по user_id
func GetProfileByUserID(userID string) (*models.Profile, error) {
	query := `SELECT user_id, first_name, middle_name, last_name, email, phone FROM profile WHERE user_id=$1`
	var profile models.Profile
	err := DB.QueryRow(query, userID).Scan(
		&profile.UserID,
		&profile.FirstName,
		&profile.MiddleName,
		&profile.LastName,
		&profile.Email,
		&profile.Phone,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // не нашли профиль
		}
		log.Printf("Error get profile: %v", err)
		return nil, err
	}
	return &profile, nil
}

// Получить id профиля по user_id из profile
func GetProfileIDByUserID(userID string) (uuid.UUID, error) {
	var profileID uuid.UUID
	err := DB.QueryRow("SELECT id FROM profile WHERE user_id = $1", userID).Scan(&profileID)
	if err != nil {
		return uuid.Nil, err
	}
	return profileID, nil
}

// UpsertProfileWith создаёт либо полностью перезаписывает профиль
// пользователя (та же семантика, что у POST /profile) через произвольный
// dbExecutor — используется как обычным CreateProfileHandler (DB), так и
// внутри транзакции переноса локальных данных (см. ImportLocalData).
func UpsertProfileWith(exec dbExecutor, userID string, input models.Profile) error {
	const queryUpsert = `
		INSERT INTO profile (user_id, first_name, middle_name, last_name, email, phone)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id) DO UPDATE
		SET first_name=EXCLUDED.first_name,
			middle_name=EXCLUDED.middle_name,
			last_name=EXCLUDED.last_name,
			email=EXCLUDED.email,
			phone=EXCLUDED.phone
	`
	_, err := exec.Exec(queryUpsert, userID, input.FirstName, input.MiddleName, input.LastName, input.Email, input.Phone)
	return err
}
