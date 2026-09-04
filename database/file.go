package database

import (
	"database/sql"
	"log"
	"myauthservice/models"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// InsertUnconfirmedFile создаёт неподтверждённую строку file (confirmed_at
// = NULL) — шаг (e) сценария «Основной сценарий: загрузка/замена файла».
// Такая строка не видна при чтении файлов сущности до подтверждения (см.
// ConfirmFile), но уже находима по id для complete/delete. filename — NULL,
// если клиент не передал его в POST /files/upload-url (см. «Общие
// требования: Файлы сущностей», раздел «Модель данных»).
func InsertUnconfirmedFile(id uuid.UUID, ownerType string, ownerID uuid.UUID, userID string, objectKey string, contentType string, filename *string) error {
	var filenameArg sql.NullString
	if filename != nil {
		filenameArg = sql.NullString{String: *filename, Valid: true}
	}
	_, err := DB.Exec(`
		INSERT INTO file (id, owner_type, owner_id, user_id, object_key, content_type, filename, position, confirmed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULL, NULL)
	`, id, ownerType, ownerID, userID, objectKey, contentType, filenameArg)
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
		SELECT id, owner_type, owner_id, user_id, object_key, content_type, filename, position, confirmed_at, created_at
		FROM file
		WHERE id = $1
	`, id).Scan(&f.ID, &f.OwnerType, &f.OwnerID, &f.UserID, &f.ObjectKey, &f.ContentType, &f.Filename, &f.Position, &f.ConfirmedAt, &f.CreatedAt)
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

// ConfirmFileUpToN подтверждает загрузку (confirmed_at = now()) для файла id
// при кардинальности «до N» (см. «Общие требования: Файлы сущностей»,
// раздел «Подтверждение загрузки», шаг d): если число уже подтверждённых
// строк для пары (ownerType, ownerID) равно limit — подтверждение НЕ
// выполняется, limitReached=true (вызывающий код обязан ответить 409).
// Иначе строке присваивается position = (максимальный существующий position
// среди уже подтверждённых строк этой пары) + 1, либо 0, если подтверждённых
// строк ещё нет; значения position не переиспользуются и не компактируются
// при последующих удалениях. pg_advisory_xact_lock сериализует конкурентные
// complete для одной и той же пары (ownerType, ownerID) — без него два
// параллельных первых-в-галерее complete могли бы оба увидеть "подтверждённых
// строк нет" и присвоить одинаковый position 0 (SELECT ... FOR UPDATE не
// защищает от этого, если строк для блокировки ещё не существует).
func ConfirmFileUpToN(id uuid.UUID, ownerType string, ownerID uuid.UUID, limit int) (limitReached bool, err error) {
	tx, err := DB.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, ownerType+"|"+ownerID.String()); err != nil {
		return false, err
	}

	var count int
	var maxPosition sql.NullInt32
	if err := tx.QueryRow(`
		SELECT COUNT(*), MAX(position) FROM file
		WHERE owner_type = $1 AND owner_id = $2 AND confirmed_at IS NOT NULL
	`, ownerType, ownerID).Scan(&count, &maxPosition); err != nil {
		return false, err
	}

	if count >= limit {
		return true, nil
	}

	newPosition := int32(0)
	if maxPosition.Valid {
		newPosition = maxPosition.Int32 + 1
	}

	if _, err := tx.Exec(`UPDATE file SET confirmed_at = now(), position = $1 WHERE id = $2`, newPosition, id); err != nil {
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}

	return false, nil
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
		SELECT id, owner_type, owner_id, user_id, object_key, content_type, filename, position, confirmed_at, created_at
		FROM file
		WHERE owner_type = $1 AND owner_id = $2 AND confirmed_at IS NOT NULL
	`, ownerType, ownerID).Scan(&f.ID, &f.OwnerType, &f.OwnerID, &f.UserID, &f.ObjectKey, &f.ContentType, &f.Filename, &f.Position, &f.ConfirmedAt, &f.CreatedAt)
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
		SELECT id, owner_type, owner_id, user_id, object_key, content_type, filename, position, confirmed_at, created_at
		FROM file
		WHERE owner_type = $1 AND owner_id = ANY($2) AND confirmed_at IS NOT NULL
	`, ownerType, pq.Array(ownerIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var f models.FileDB
		if err := rows.Scan(&f.ID, &f.OwnerType, &f.OwnerID, &f.UserID, &f.ObjectKey, &f.ContentType, &f.Filename, &f.Position, &f.ConfirmedAt, &f.CreatedAt); err != nil {
			return nil, err
		}
		result[f.OwnerID] = f
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

// GetFilesForOwner возвращает все подтверждённые строки file для пары
// (ownerType, ownerID), упорядоченные по position по возрастанию — используется
// полной формой чтения (например, поле `files` у события, см. «Файлы события
// — Backend», раздел «Чтение»). Пустой срез (не nil), если файлов нет.
func GetFilesForOwner(ownerType string, ownerID uuid.UUID) ([]models.FileDB, error) {
	rows, err := DB.Query(`
		SELECT id, owner_type, owner_id, user_id, object_key, content_type, filename, position, confirmed_at, created_at
		FROM file
		WHERE owner_type = $1 AND owner_id = $2 AND confirmed_at IS NOT NULL
		ORDER BY position ASC
	`, ownerType, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	files := []models.FileDB{}
	for rows.Next() {
		var f models.FileDB
		if err := rows.Scan(&f.ID, &f.OwnerType, &f.OwnerID, &f.UserID, &f.ObjectKey, &f.ContentType, &f.Filename, &f.Position, &f.ConfirmedAt, &f.CreatedAt); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return files, nil
}

// CountFilesForOwner возвращает количество подтверждённых файлов для пары
// (ownerType, ownerID) — используется полем `files_count` у одного события
// (см. «Файлы события — Backend»). Без выборки самих строк.
func CountFilesForOwner(ownerType string, ownerID uuid.UUID) (int, error) {
	var count int
	err := DB.QueryRow(`
		SELECT COUNT(*) FROM file
		WHERE owner_type = $1 AND owner_id = $2 AND confirmed_at IS NOT NULL
	`, ownerType, ownerID).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// CountFilesForOwners — батч-версия CountFilesForOwner для списков событий
// (GET /activities, GET /activities/day, GET /pet/{id}/events): один запрос
// под все ownerID вместо N+1. Owner без подтверждённых файлов просто
// отсутствует в результирующей map — вызывающий код трактует это как 0.
func CountFilesForOwners(ownerType string, ownerIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	result := make(map[uuid.UUID]int, len(ownerIDs))
	if len(ownerIDs) == 0 {
		return result, nil
	}

	rows, err := DB.Query(`
		SELECT owner_id, COUNT(*) FROM file
		WHERE owner_type = $1 AND owner_id = ANY($2) AND confirmed_at IS NOT NULL
		GROUP BY owner_id
	`, ownerType, pq.Array(ownerIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var ownerID uuid.UUID
		var count int
		if err := rows.Scan(&ownerID, &count); err != nil {
			return nil, err
		}
		result[ownerID] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}
