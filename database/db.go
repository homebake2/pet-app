package database

import (
	"database/sql"
	"fmt"
	"log"
	"myauthservice/models"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

var DB *sql.DB

func InitDB() {
	var err error
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		log.Fatal("DATABASE_URL не задан")
	}

	DB, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Ошибка подключения к базе: %v", err)
	}
	if err = DB.Ping(); err != nil {
		log.Fatalf("Не удалось подключиться к базе: %v", err)
	}

	if err := RunMigrations(); err != nil {
		log.Fatalf("Ошибка применения миграций: %v", err)
	}
}

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

// Получить id профиля по user_id из profile
func GetProfileIDByUserID(userID string) (uuid.UUID, error) {

	// например, SQL-запрос к базе
	var profileID uuid.UUID
	err := DB.QueryRow("SELECT id FROM profile WHERE user_id = $1", userID).Scan(&profileID)
	if err != nil {
		return uuid.Nil, err
	}
	return profileID, nil
}

func InsertPet(profileID uuid.UUID, req models.CreatePetRequest) (uuid.UUID, error) {
	// подготовка SQL-запроса с учетом необязательных полей
	query := `
        INSERT INTO pet (
            profile_id, breed, name, species, birth_date, gender, color, sterilized, habitation, notes, icon, deleted_at
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
        ) RETURNING id
    `

	// использует параметры из req, заменяя отсутствующие — на NULL
	var birthDate sql.NullTime
	if req.BirthDate != nil {
		parsedDate, err := time.Parse("2006-01-02", *req.BirthDate)
		if err != nil {
			return uuid.Nil, err
		}
		birthDate = sql.NullTime{Time: parsedDate, Valid: true}
	} else {
		birthDate = sql.NullTime{Valid: false}
	}

	var gender sql.NullString
	if req.Gender != nil {
		gender = sql.NullString{String: *req.Gender, Valid: true}
	} else {
		gender = sql.NullString{Valid: false}
	}

	// аналогично для других необязательных полей

	var sterilized sql.NullBool
	if req.Sterilized != nil {
		sterilized = sql.NullBool{Bool: *req.Sterilized, Valid: true}
	} else {
		sterilized = sql.NullBool{Valid: false}
	}

	// и так далее для остальных необязательных полей

	var habitat sql.NullString
	if req.Habitation != nil {
		habitat = sql.NullString{String: *req.Habitation, Valid: true}
	} else {
		habitat = sql.NullString{Valid: false}
	}

	var icon sql.NullString
	if req.Icon != nil {
		icon = sql.NullString{String: *req.Icon, Valid: true}
	} else {
		icon = sql.NullString{Valid: false}
	}

	var deletedAt sql.NullTime
	if req.IsDeleted != nil && *req.IsDeleted {
		deletedAt = sql.NullTime{Time: time.Now().UTC(), Valid: true}
	} else {
		deletedAt = sql.NullTime{Valid: false}
	}

	// Тут можно подготовить все параметры и выполнить запрос
	// После вставки вернете id нового питомца.

	var newID uuid.UUID

	err := DB.QueryRow(query, profileID, req.Breed, req.Name, req.Species, birthDate, gender, req.Color, sterilized, habitat, req.Notes, icon, deletedAt).Scan(&newID)
	if err != nil {
		log.Println("InsertPet error:", err)
		return uuid.Nil, err
	}
	return newID, nil

}

// GetPetsByProfileID - функция получения питомцев по profile_id
func GetPetsByProfileID(profileID uuid.UUID) ([]models.PetDB, error) {
	var pets []models.PetDB

	// SQL запрос для получения питомцев
	rows, err := DB.Query(`
	SELECT id, name, breed, species, icon
	FROM pet
	WHERE profile_id = $1
	  AND deleted_at IS NULL
`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var pet models.PetDB
		if err := rows.Scan(&pet.ID, &pet.Name, &pet.Breed, &pet.Species, &pet.Icon); err != nil {
			return nil, err
		}
		pets = append(pets, pet)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return pets, nil
}

// GetPetByIDAndProfileID - получить питомца по id и profile_id
func GetPetByIDAndProfileID(petID, profileID uuid.UUID) (*models.PetIdResponse, error) {
	query := `
	SELECT id, name, gender, species, birth_date, color, sterilized,
	       habitation, notes, deleted_at, breed, icon
	FROM pet
	WHERE id = $1 AND profile_id = $2
	`

	var petDB models.PetIdDB

	err := DB.QueryRow(query, petID, profileID).Scan(
		&petDB.ID,
		&petDB.Name,
		&petDB.Gender,
		&petDB.Species,
		&petDB.BirthDate,
		&petDB.Color,
		&petDB.Sterilized,
		&petDB.Habitation,
		&petDB.Notes,
		&petDB.DeletedAt,
		&petDB.Breed,
		&petDB.Icon,
	)

	if err != nil {
		return nil, err
	}

	// Маппинг в response
	pet := models.PetIdResponse{
		ID:         petDB.ID.String(),
		Name:       petDB.Name,
		Species:    petDB.Species,
		Sterilized: petDB.Sterilized,
		IsDeleted:  petDB.DeletedAt.Valid, // 👈 логика soft delete
	}

	if petDB.Gender.Valid {
		pet.Gender = &petDB.Gender.String
	}

	if petDB.BirthDate.Valid {
		str := petDB.BirthDate.Time.Format("2006-01-02")
		pet.BirthDate = &str
	}

	if petDB.Color.Valid {
		pet.Color = &petDB.Color.String
	}

	if petDB.Habitation.Valid {
		pet.Habitation = &petDB.Habitation.String
	}

	if petDB.Notes.Valid {
		pet.Notes = &petDB.Notes.String
	}

	if petDB.Breed.Valid {
		pet.Breed = &petDB.Breed.String
	}

	pet.Icon = "OTHER"
	if petDB.Icon.Valid && petDB.Icon.String != "" {
		pet.Icon = petDB.Icon.String
	}

	return &pet, nil
}

func UpdatePet(petID, profileID uuid.UUID, req models.UpdatePetRequest) error {
	setParts := []string{}
	args := []any{}
	argID := 1

	// helper для добавления полей
	add := func(field string, value any, condition bool) {
		log.Println("TRY ADD:", field, value, condition)
		if condition {
			setParts = append(setParts, field+" = $"+strconv.Itoa(argID))
			args = append(args, value)
			argID++
		}
	}

	// name
	if req.Name != nil {
		add("name", *req.Name, true)
	}

	// gender
	if req.Gender != nil {
		add("gender", *req.Gender, true)
	}

	// species
	if req.Species != nil {
		add("species", *req.Species, true)
	}

	// birth_date
	if req.BirthDate != nil {
		t, err := time.Parse("2006-01-02", *req.BirthDate)
		if err != nil {
			return err
		}
		add("birth_date", t, true)
	}

	// color
	if req.Color != nil {
		add("color", *req.Color, true)
	}

	// sterilized
	if req.Sterilized != nil {
		add("sterilized", *req.Sterilized, true)
	}

	// habitation
	if req.Habitation != nil {
		add("habitation", *req.Habitation, true)
	}

	// notes
	if req.Notes != nil {
		add("notes", *req.Notes, true)
	}

	// breed
	if req.Breed != nil {
		add("breed", *req.Breed, true)
	}
	// icon
	if req.Icon != nil {
		add("icon", *req.Icon, true)
	}

	// is_deleted (через deleted_at)
	if req.IsDeleted != nil {
		if req.IsDeleted != nil {
			if *req.IsDeleted {
				now := time.Now().UTC()
				add("deleted_at", now, true)
			} else {
				setParts = append(setParts, "deleted_at = NULL")
			}
		}
	}
	if len(setParts) == 0 {
		log.Printf("зашли в условие setParts = 0 ")
		return nil // нечего обновлять
	}

	query := fmt.Sprintf(`
		UPDATE pet
		SET %s
		WHERE id = $%d AND profile_id = $%d
	`, strings.Join(setParts, ", "), argID, argID+1)

	args = append(args, petID, profileID)

	_, err := DB.Exec(query, args...)
	return err
}

func DeletePet(petID, profileID uuid.UUID) error {
	query := `
		UPDATE pet
		SET deleted_at = $1
		WHERE id = $2 AND profile_id = $3
	`

	now := time.Now().UTC()

	result, err := DB.Exec(query, now, petID, profileID)
	if err != nil {
		log.Println("DeletePet error:", err)
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

// InsertEvent - функция создания нового ивента для питомца
func InsertEvent(petID uuid.UUID, req models.CreateEventRequest) (uuid.UUID, error) {
	query := `
        INSERT INTO event (
            pet_id, date_time, type, notes, value
        ) VALUES (
            $1, $2, $3, $4, $5
        ) RETURNING id
    `

	// Парсинг даты из запроса
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

	var eventID uuid.UUID
	err = DB.QueryRow(query, petID, dateTime, req.Type, notes, req.Value).Scan(&eventID)
	if err != nil {
		log.Println("InsertEvent error:", err)
		return uuid.Nil, err
	}

	return eventID, nil
}

// GetPetIdDBByIDAndProfileID - получить питомца по id и profile_id (проверка принадлежности к пользователю)
func GetPetIdDBByIDAndProfileID(petID, profileID uuid.UUID) (*models.PetIdDB, error) {
	query := `
	SELECT id, name, gender, species, birth_date, color, sterilized,
	       habitation, notes, deleted_at, breed, icon
	FROM pet
	WHERE id = $1 AND profile_id = $2
	`

	var petDB models.PetIdDB

	err := DB.QueryRow(query, petID, profileID).Scan(
		&petDB.ID,
		&petDB.Name,
		&petDB.Gender,
		&petDB.Species,
		&petDB.BirthDate,
		&petDB.Color,
		&petDB.Sterilized,
		&petDB.Habitation,
		&petDB.Notes,
		&petDB.DeletedAt,
		&petDB.Breed,
		&petDB.Icon,
	)

	if err != nil {
		return nil, err
	}

	return &petDB, nil
}

// GetEventByID - получить событие по ID и информацию о питомце
func GetEventByID(eventID uuid.UUID) (*models.EventDB, uuid.UUID, string, error) {
	query := `
	SELECT e.id, e.pet_id, e.date_time, e.type, e.notes, e.value, p.name
	FROM event e
	JOIN pet p ON e.pet_id = p.id
	WHERE e.id = $1
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

// CheckPetBelongsToProfile - проверка принадлежности питомца профилю
func CheckPetBelongsToProfile(petID, profileID uuid.UUID) (bool, error) {
	query := `SELECT COUNT(1) FROM pet WHERE id = $1 AND profile_id = $2`
	var count int
	err := DB.QueryRow(query, petID, profileID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count == 1, nil
}

// UpdateEvent - функция обновления события
func UpdateEvent(eventID uuid.UUID, req models.UpdateEventRequest, dateTime *time.Time, eventType *string, notes *string, value *string) error {
	setParts := []string{}
	args := []any{}
	argID := 1

	// Helper для добавления полей
	add := func(field string, value any) {
		setParts = append(setParts, field+" = $"+strconv.Itoa(argID))
		args = append(args, value)
		argID++
	}

	// Обновление полей, если они переданы
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
		add("value", *value)
	}

	// Если нет полей для обновления
	if len(setParts) == 0 {
		return nil
	}

	// Добавляем условие WHERE
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
	WHERE id = $1
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

// GetPetById - получить питомца по ID
func GetPetById(petID uuid.UUID) (*models.PetIdDB, error) {
	query := `
	SELECT id, name, gender, species, birth_date, color, sterilized,
	       habitation, notes, deleted_at, breed, icon
	FROM pet
	WHERE id = $1
	`

	var petDB models.PetIdDB
	err := DB.QueryRow(query, petID).Scan(
		&petDB.ID,
		&petDB.Name,
		&petDB.Gender,
		&petDB.Species,
		&petDB.BirthDate,
		&petDB.Color,
		&petDB.Sterilized,
		&petDB.Habitation,
		&petDB.Notes,
		&petDB.DeletedAt,
		&petDB.Breed,
		&petDB.Icon,
	)

	if err != nil {
		return nil, err
	}

	return &petDB, nil
}

// DeleteEvent - функция удаления события
func DeleteEvent(eventID uuid.UUID) error {
	query := `DELETE FROM event WHERE id = $1`
	result, err := DB.Exec(query, eventID)
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

// GetEventsByPetIDAndDateRange returns all events for a specific pet within a date range
func GetEventsByPetIDAndDateRange(petID uuid.UUID, fromDate, toDate time.Time) ([]models.EventDB, error) {
	query := `
	SELECT id, pet_id, date_time, type, notes, value
	FROM event
	WHERE pet_id = $1
	AND date_time >= $2
	AND date_time <= $3
	ORDER BY date_time
	`
	rows, err := DB.Query(query, petID, fromDate, toDate.Add(24*time.Hour-1*time.Second)) // Include entire toDate
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

// GetEventsByPetID returns all events for a pet, ordered by date
func GetEventsByPetID(petID uuid.UUID) ([]models.EventDB, error) {
	query := `
	SELECT id, pet_id, date_time, type, notes, value
	FROM event
	WHERE pet_id = $1
	ORDER BY date_time
	`
	rows, err := DB.Query(query, petID)
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

// GetPetNameByID returns the name of a pet by its ID
func GetPetNameByID(petID uuid.UUID) (string, error) {
	query := `SELECT name FROM pet WHERE id = $1`
	var name string
	err := DB.QueryRow(query, petID).Scan(&name)
	if err != nil {
		log.Println("GetPetNameByID error:", err)
		return "", err
	}
	return name, nil
}
