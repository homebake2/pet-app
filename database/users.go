package database

import (
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
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

// ClearRefreshTokenByIDIfMatches атомарно инвалидирует refresh_token
// пользователя, только если сохранённое значение совпадает с переданным
// (compare-and-clear одним запросом — без гонки между чтением и обновлением
// при параллельных logout/refresh с одним и тем же токеном).
// Возвращает false, если пользователь не найден или токен не совпал —
// вызывающий код не должен различать эти случаи (см. handlers/auth.go).
func ClearRefreshTokenByIDIfMatches(userID, token string) (bool, error) {
	res, err := DB.Exec(
		"UPDATE users SET refresh_token=NULL, tokens_invalidated_at=now() WHERE id=$1 AND refresh_token=$2",
		userID, token,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// RotateRefreshTokenByID атомарно заменяет refresh_token пользователя на
// новый одним UPDATE (без предварительного SELECT), исключая гонку под
// параллельными refresh-запросами с одним и тем же токеном. Возвращает
// false, если пользователь не найден или сохранённый токен не совпал со
// старым — в этом случае вызывающий код должен вернуть 401.
func RotateRefreshTokenByID(userID, oldToken, newToken string) (bool, error) {
	res, err := DB.Exec(
		"UPDATE users SET refresh_token=$1 WHERE id=$2 AND refresh_token=$3",
		newToken, userID, oldToken,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// UpdateRefreshTokenByID сохраняет новый refresh_token пользователя по id.
// Используется id, а не login, потому что login сравнивается
// регистронезависимо (см. FindUserByLoginNormalized) и не годится в качестве
// точного ключа для UPDATE.
func UpdateRefreshTokenByID(userID, token string) error {
	_, err := DB.Exec("UPDATE users SET refresh_token=$1 WHERE id=$2", token, userID)
	return err
}

// FindUserByLoginNormalized ищет пользователя по логину без учёта регистра и
// обрамляющих пробелов ("User" и " user " считаются одним и тем же логином).
func FindUserByLoginNormalized(login string) (uuid.UUID, string, error) {
	var id uuid.UUID
	var password string
	err := DB.QueryRow(
		"SELECT id, password FROM users WHERE lower(trim(login)) = lower($1)", login,
	).Scan(&id, &password)
	return id, password, err
}

// IsUniqueViolation сообщает, является ли ошибка нарушением уникального
// ограничения Postgres (код 23505), а не любой другой ошибкой INSERT.
// Используется, чтобы отличить гонку параллельной регистрации с одинаковым
// login от настоящей ошибки БД, которая должна остаться 500.
func IsUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == "23505"
	}
	return false
}

// UpsertRegistrationAttempt атомарно увеличивает счётчик регистраций с
// данного IP за текущие сутки и возвращает новое значение счётчика (см.
// database/migrations/000004_registration_rate_limit.up.sql).
func UpsertRegistrationAttempt(ip string) (int, error) {
	day := time.Now().UTC().Format("2006-01-02")
	var count int
	err := DB.QueryRow(`
		INSERT INTO registration_rate_limit (ip, day, count)
		VALUES ($1, $2, 1)
		ON CONFLICT (ip, day) DO UPDATE SET count = registration_rate_limit.count + 1
		RETURNING count
	`, ip, day).Scan(&count)
	return count, err
}

// FindUserIDAndLoginByGuestDeviceID ищет гостевого пользователя по
// device_id (см. database/migrations/000005_guest_users.up.sql).
func FindUserIDAndLoginByGuestDeviceID(deviceID string) (uuid.UUID, string, error) {
	var id uuid.UUID
	var login string
	err := DB.QueryRow(
		"SELECT id, login FROM users WHERE guest_device_id=$1", deviceID,
	).Scan(&id, &login)
	return id, login, err
}

// CreateGuestUser создаёт гостевого пользователя без пароля, привязанного к
// device_id. Идемпотентность по device_id обеспечивается вызывающим кодом
// (сначала FindUserIDAndLoginByGuestDeviceID) и частичным уникальным
// индексом idx_users_guest_device_id на случай гонки.
func CreateGuestUser(login, deviceID string) (uuid.UUID, error) {
	var id uuid.UUID
	err := DB.QueryRow(
		"INSERT INTO users (login, password, guest_device_id, is_guest) VALUES ($1, '', $2, true) RETURNING id",
		login, deviceID,
	).Scan(&id)
	return id, err
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
