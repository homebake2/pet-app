package database

import (
	"log"
	"myauthservice/models"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// InsertUnconfirmedFile создаёт неподтверждённую строку file (confirmed_at
// = NULL) — шаг (e) сценария «Основной сценарий: загрузка/замена файла».
// Такая строка не видна при чтении файлов сущности до подтверждения (см.
// ConfirmFile), но уже находима по id для complete/delete.
func InsertUnconfirmedFile(id uuid.UUID, ownerType string, ownerID uuid.UUID, userID string, objectKey string, contentType string) error {
	_, err := DB.Exec(`
		INSERT INTO file (id, owner_type, owner_id, user_id, object_key, content_type, position, confirmed_at)
		VALUES ($1, $2, $3, $4, $5, $6, NULL, NULL)
	`, id, ownerType, ownerID, userID, objectKey, contentType)
	if err != nil {
		log.Println("InsertUnconfirmedFile error:", err)
		return err
	}
	return nil
}

// GetFileByID находит строку file по id независимо от confirmed_at —
// используется complete/delete, которым нужно найти запись по единственному
// параметру запроса (file_id) и повторно проверить владение.
func GetFileByID(id uuid.UUID) (*models.FileDB, error) {
	var f models.FileDB
	err := DB.QueryRow(`
		SELECT id, owner_type, owner_id, user_id, object_key, content_type, position, confirmed_at, created_at
		FROM file
		WHERE id = $1
	`, id).Scan(&f.ID, &f.OwnerType, &f.OwnerID, &f.UserID, &f.ObjectKey, &f.ContentType, &f.Position, &f.ConfirmedAt, &f.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// ConfirmFileExactlyOne подтверждает загрузку (confirmed_at = now()) для
// файла id и — для кардинальности «ровно 1» — удаляет любые другие ранее
// подтверждённые строки той же пары (ownerType, ownerID), возвращая их
// object_key для best-effort удаления в S3 на стороне вызывающего кода (см.
// «Подтверждение загрузки», шаг d).
func ConfirmFileExactlyOne(id uuid.UUID, ownerType string, ownerID uuid.UUID) (replacedObjectKeys []string, err error) {
	tx, err := DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`UPDATE file SET confirmed_at = now() WHERE id = $1`, id); err != nil {
		return nil, err
	}

	rows, err := tx.Query(`
		SELECT id, object_key FROM file
		WHERE owner_type = $1 AND owner_id = $2 AND confirmed_at IS NOT NULL AND id != $3
	`, ownerType, ownerID, id)
	if err != nil {
		return nil, err
	}
	var replacedIDs []uuid.UUID
	for rows.Next() {
		var replacedID uuid.UUID
		var objectKey string
		if err := rows.Scan(&replacedID, &objectKey); err != nil {
			rows.Close()
			return nil, err
		}
		replacedIDs = append(replacedIDs, replacedID)
		replacedObjectKeys = append(replacedObjectKeys, objectKey)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	if len(replacedIDs) > 0 {
		if _, err := tx.Exec(`DELETE FROM file WHERE id = ANY($1)`, pq.Array(replacedIDs)); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return replacedObjectKeys, nil
}

// DeleteFileByID удаляет строку file по id и возвращает её object_key для
// best-effort удаления в S3. sql.ErrNoRows — file_id не найден (в т.ч. уже
// удалён), см. «Удаление файла».
func DeleteFileByID(id uuid.UUID) (objectKey string, err error) {
	err = DB.QueryRow(`DELETE FROM file WHERE id = $1 RETURNING object_key`, id).Scan(&objectKey)
	if err != nil {
		return "", err
	}
	return objectKey, nil
}

// GetConfirmedFileForOwner ищет единственную подтверждённую строку file для
// пары (ownerType, ownerID) — используется чтением сущности с кардинальностью
// «ровно 1» (например, photo_url питомца). sql.ErrNoRows — фотографии нет.
func GetConfirmedFileForOwner(ownerType string, ownerID uuid.UUID) (*models.FileDB, error) {
	var f models.FileDB
	err := DB.QueryRow(`
		SELECT id, owner_type, owner_id, user_id, object_key, content_type, position, confirmed_at, created_at
		FROM file
		WHERE owner_type = $1 AND owner_id = $2 AND confirmed_at IS NOT NULL
	`, ownerType, ownerID).Scan(&f.ID, &f.OwnerType, &f.OwnerID, &f.UserID, &f.ObjectKey, &f.ContentType, &f.Position, &f.ConfirmedAt, &f.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// GetConfirmedFilesForOwners — батч-версия GetConfirmedFileForOwner для
// списков (например, GET /pet): один запрос под все ownerID вместо N+1.
// Возвращает только найденные строки, ключ — owner_id.
func GetConfirmedFilesForOwners(ownerType string, ownerIDs []uuid.UUID) (map[uuid.UUID]models.FileDB, error) {
	result := make(map[uuid.UUID]models.FileDB, len(ownerIDs))
	if len(ownerIDs) == 0 {
		return result, nil
	}

	rows, err := DB.Query(`
		SELECT id, owner_type, owner_id, user_id, object_key, content_type, position, confirmed_at, created_at
		FROM file
		WHERE owner_type = $1 AND owner_id = ANY($2) AND confirmed_at IS NOT NULL
	`, ownerType, pq.Array(ownerIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var f models.FileDB
		if err := rows.Scan(&f.ID, &f.OwnerType, &f.OwnerID, &f.UserID, &f.ObjectKey, &f.ContentType, &f.Position, &f.ConfirmedAt, &f.CreatedAt); err != nil {
			return nil, err
		}
		result[f.OwnerID] = f
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}
