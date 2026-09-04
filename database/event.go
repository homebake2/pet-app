package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"myauthservice/models"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// InsertEvent - функция создания нового ивента для питомца. idempotencyKey
// пустая строка означает "заголовок Idempotency-Key не передан" (NULL в БД).
func InsertEvent(petID uuid.UUID, req models.CreateEventRequest, idempotencyKey string) (uuid.UUID, error) {
	return insertEventWith(DB, petID, req, idempotencyKey)
}

// insertEventWith — то же самое, что InsertEvent, но принимает произвольный
// dbExecutor: используется как для обычных запросов (DB), так и внутри
// транзакции переноса локальных данных (см. ImportLocalData).
func insertEventWith(exec dbExecutor, petID uuid.UUID, req models.CreateEventRequest, idempotencyKey string) (uuid.UUID, error) {
	query := `
        INSERT INTO event (
            pet_id, date_time, type, notes, value, idempotency_key
        ) VALUES (
            $1, $2, $3, $4, $5, $6
        ) RETURNING id
    `

	dateTime, err := time.Parse(time.RFC3339, req.Date)
	if err != nil {
		return uuid.Nil, err
	}

	var notes sql.NullString
	if req.Notes != nil {
		notes = sql.NullString{String: *req.Notes, Valid: true}
	} else {
		notes = sql.NullString{Valid: false}
	}

	var key sql.NullString
	if idempotencyKey != "" {
		key = sql.NullString{String: idempotencyKey, Valid: true}
	}

	var eventID uuid.UUID
	// value передаётся строкой: столбец event.value имеет тип jsonb, а
	// []byte драйвер закодировал бы как bytea.
	err = exec.QueryRow(query, petID, dateTime, req.Type, notes, string(req.Value), key).Scan(&eventID)
	if err != nil {
		log.Println("InsertEvent error:", err)
		return uuid.Nil, err
	}

	return eventID, nil
}

// GetEventByPetIDAndIdempotencyKey ищет неудалённое событие питомца по
// ранее использованному Idempotency-Key (см. страницу "Добавление события —
// Backend"). Возвращает sql.ErrNoRows, если такого события нет.
func GetEventByPetIDAndIdempotencyKey(petID uuid.UUID, idempotencyKey string) (*models.EventDB, uuid.UUID, string, error) {
	query := `
	SELECT e.id, e.pet_id, e.date_time, e.type, e.notes, e.value, p.name
	FROM event e
	JOIN pet p ON e.pet_id = p.id
	WHERE e.pet_id = $1 AND e.idempotency_key = $2
	`

	var eventDB models.EventDB
	var petName string

	err := DB.QueryRow(query, petID, idempotencyKey).Scan(
		&eventDB.ID,
		&eventDB.PetID,
		&eventDB.Date,
		&eventDB.Type,
		&eventDB.Notes,
		&eventDB.Value,
		&petName,
	)

	if err != nil {
		return nil, uuid.Nil, "", err
	}

	return &eventDB, eventDB.PetID, petName, nil
}

// GetEventByID - получить событие по ID и информацию о питомце
func GetEventByID(eventID uuid.UUID) (*models.EventDB, uuid.UUID, string, error) {
	query := `
	SELECT e.id, e.pet_id, e.date_time, e.type, e.notes, e.value, p.name
	FROM event e
	JOIN pet p ON e.pet_id = p.id
	WHERE e.id = $1 AND e.deleted_at IS NULL
	`

	var eventDB models.EventDB
	var petName string

	err := DB.QueryRow(query, eventID).Scan(
		&eventDB.ID,
		&eventDB.PetID,
		&eventDB.Date,
		&eventDB.Type,
		&eventDB.Notes,
		&eventDB.Value,
		&petName,
	)

	if err != nil {
		return nil, uuid.Nil, "", err
	}

	return &eventDB, eventDB.PetID, petName, nil
}

// UpdateEvent - функция обновления события
func UpdateEvent(eventID uuid.UUID, req models.UpdateEventRequest, dateTime *time.Time, eventType *string, notes *string, value *json.RawMessage) error {
	setParts := []string{}
	args := []any{}
	argID := 1

	add := func(field string, value any) {
		setParts = append(setParts, field+" = $"+strconv.Itoa(argID))
		args = append(args, value)
		argID++
	}

	if dateTime != nil {
		add("date_time", *dateTime)
	}
	if eventType != nil {
		add("type", *eventType)
	}
	if notes != nil {
		if *notes == "" {
			add("notes", sql.NullString{Valid: false})
		} else {
			add("notes", sql.NullString{String: *notes, Valid: true})
		}
	}
	if value != nil {
		// value заменяется целиком (слияние вложенных полей не поддерживается).
		add("value", string(*value))
	}

	if len(setParts) == 0 {
		return nil // нечего обновлять
	}

	query := fmt.Sprintf(`
		UPDATE event
		SET %s
		WHERE id = $%d
	`, strings.Join(setParts, ", "), argID)
	args = append(args, eventID)

	_, err := DB.Exec(query, args...)
	if err != nil {
		log.Println("UpdateEvent error:", err)
		return err
	}

	return nil
}

// GetEventByIDForUpdate - получить событие по ID для обновления
func GetEventByIDForUpdate(eventID uuid.UUID) (*models.EventDB, error) {
	query := `
	SELECT id, pet_id, date_time, type, notes, value
	FROM event
	WHERE id = $1 AND deleted_at IS NULL
	`

	var eventDB models.EventDB
	err := DB.QueryRow(query, eventID).Scan(
		&eventDB.ID,
		&eventDB.PetID,
		&eventDB.Date,
		&eventDB.Type,
		&eventDB.Notes,
		&eventDB.Value,
	)

	if err != nil {
		return nil, err
	}

	return &eventDB, nil
}

// DeleteEvent - функция мягкого удаления события (устанавливает deleted_at,
// строка физически не удаляется), аналогично database.DeletePet.
func DeleteEvent(eventID uuid.UUID) error {
	query := `UPDATE event SET deleted_at = $1 WHERE id = $2 AND deleted_at IS NULL`
	now := time.Now().UTC()
	result, err := DB.Exec(query, now, eventID)
	if err != nil {
		log.Println("DeleteEvent error:", err)
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return sql.ErrNoRows
	}

	return nil
}

// GetEventsByPetIDAndDateRange - получить все неудалённые события питомца в
// заданном диапазоне дат. fromDate/toDate — календарные UTC-даты (полночь
// UTC); границы сравниваются полуоткрытым интервалом
// [fromDate, toDate+1 день) в UTC, см. страницу "Просмотр календаря — Backend".
func GetEventsByPetIDAndDateRange(petID uuid.UUID, fromDate, toDate time.Time) ([]models.EventDB, error) {
	query := `
	SELECT id, pet_id, date_time, type, notes, value
	FROM event
	WHERE pet_id = $1
	AND deleted_at IS NULL
	AND date_time >= $2
	AND date_time < $3
	ORDER BY date_time
	`
	rows, err := DB.Query(query, petID, fromDate.UTC(), toDate.UTC().AddDate(0, 0, 1))
	if err != nil {
		log.Println("GetEventsByPetIDAndDateRange error:", err)
		return nil, err
	}
	defer rows.Close()

	var events []models.EventDB
	for rows.Next() {
		var eventDB models.EventDB
		err := rows.Scan(
			&eventDB.ID,
			&eventDB.PetID,
			&eventDB.Date,
			&eventDB.Type,
			&eventDB.Notes,
			&eventDB.Value,
		)
		if err != nil {
			log.Println("GetEventsByPetIDAndDateRange scan error:", err)
			return nil, err
		}
		events = append(events, eventDB)
	}

	if err := rows.Err(); err != nil {
		log.Println("GetEventsByPetIDAndDateRange rows error:", err)
		return nil, err
	}

	return events, nil
}

// CheckEventFileOwnership проверяет владение событием по правилу,
// зарегистрированному для owner_type = "event_file" в реестре типов
// владельцев generic-механизма файлов сущностей (см. handlers/files.go,
// «Файлы события — Backend»): событие не мягко удалено и принадлежит
// (через pet_id) не мягко удалённому питомцу userID — то же правило, что и
// при создании/редактировании события (в отличие от удаления самого
// события, где мягко удалённый питомец операцию не блокирует).
func CheckEventFileOwnership(eventID uuid.UUID, userID string) (bool, error) {
	query := `
	SELECT COUNT(1) FROM event
	WHERE id = $1 AND deleted_at IS NULL
	AND EXISTS (SELECT 1 FROM pet WHERE pet.id = event.pet_id AND pet.user_id = $2 AND pet.deleted_at IS NULL)
	`
	var count int
	err := DB.QueryRow(query, eventID, userID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count == 1, nil
}

// EventWithPet — строка события вместе с именем его питомца, используется
// эндпоинтами, отдающими события разных питомцев одним списком
// (GET /activities/day, см. «Просмотр календаря — Backend»).
type EventWithPet struct {
	Event   models.EventDB
	PetName string
}

// CountEventsByUserIDGroupedByDay возвращает количество неудалённых событий
// всех неудалённых питомцев userID, попадающих в полуоткрытый интервал
// [fromDate, toDate+1 день) в UTC, сгруппированное по календарному дню (UTC)
// — см. «Просмотр календаря — Backend», GET /activities/calendar. Дни без
// событий отсутствуют в результирующей map — вызывающий код достраивает
// диапазон нулями.
func CountEventsByUserIDGroupedByDay(userID string, fromDate, toDate time.Time) (map[string]int, error) {
	query := `
	SELECT (e.date_time AT TIME ZONE 'UTC')::date AS day, COUNT(*)
	FROM event e
	JOIN pet p ON e.pet_id = p.id
	WHERE p.user_id = $1
	AND p.deleted_at IS NULL
	AND e.deleted_at IS NULL
	AND e.date_time >= $2
	AND e.date_time < $3
	GROUP BY day
	`
	rows, err := DB.Query(query, userID, fromDate.UTC(), toDate.UTC().AddDate(0, 0, 1))
	if err != nil {
		log.Println("CountEventsByUserIDGroupedByDay error:", err)
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]int)
	for rows.Next() {
		var day time.Time
		var count int
		if err := rows.Scan(&day, &count); err != nil {
			return nil, err
		}
		result[day.Format("2006-01-02")] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

// GetEventsByUserIDAndDate возвращает все неудалённые события всех
// неудалённых питомцев userID, чей календарный день (UTC) — dayStart
// (полуоткрытый интервал [dayStart, dayStart+1 день) в UTC), отсортированные
// по date_time по возрастанию — см. «Просмотр календаря — Backend»,
// GET /activities/day.
func GetEventsByUserIDAndDate(userID string, dayStart time.Time) ([]EventWithPet, error) {
	query := `
	SELECT e.id, e.pet_id, e.date_time, e.type, e.notes, e.value, p.name
	FROM event e
	JOIN pet p ON e.pet_id = p.id
	WHERE p.user_id = $1
	AND p.deleted_at IS NULL
	AND e.deleted_at IS NULL
	AND e.date_time >= $2
	AND e.date_time < $3
	ORDER BY e.date_time ASC
	`
	rows, err := DB.Query(query, userID, dayStart.UTC(), dayStart.UTC().AddDate(0, 0, 1))
	if err != nil {
		log.Println("GetEventsByUserIDAndDate error:", err)
		return nil, err
	}
	defer rows.Close()

	var events []EventWithPet
	for rows.Next() {
		var e EventWithPet
		if err := rows.Scan(&e.Event.ID, &e.Event.PetID, &e.Event.Date, &e.Event.Type, &e.Event.Notes, &e.Event.Value, &e.PetName); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return events, nil
}

// GetEventsByPetID - получить события питомца, отсортированные по date_time
// по убыванию (сначала последние), с пагинацией limit/offset.
func GetEventsByPetID(petID uuid.UUID, limit, offset int) ([]models.EventDB, error) {
	query := `
	SELECT id, pet_id, date_time, type, notes, value
	FROM event
	WHERE pet_id = $1
	AND deleted_at IS NULL
	ORDER BY date_time DESC
	LIMIT $2 OFFSET $3
	`
	rows, err := DB.Query(query, petID, limit, offset)
	if err != nil {
		log.Println("GetEventsByPetID error:", err)
		return nil, err
	}
	defer rows.Close()

	var events []models.EventDB
	for rows.Next() {
		var eventDB models.EventDB
		if err := rows.Scan(&eventDB.ID, &eventDB.PetID, &eventDB.Date, &eventDB.Type, &eventDB.Notes, &eventDB.Value); err != nil {
			return nil, err
		}
		events = append(events, eventDB)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return events, nil
}
