package database

import (
	"database/sql"
	"fmt"
	"log"
	"myauthservice/models"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// InsertPet создаёт нового питомца, подставляя NULL для отсутствующих
// необязательных полей запроса.
func InsertPet(userID string, req models.CreatePetRequest) (uuid.UUID, error) {
	query := `
        INSERT INTO pet (
            user_id, breed, name, species, birth_date, gender, color, sterilized, habitation, notes, icon, deleted_at
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
        ) RETURNING id
    `

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

	var sterilized sql.NullBool
	if req.Sterilized != nil {
		sterilized = sql.NullBool{Bool: *req.Sterilized, Valid: true}
	} else {
		sterilized = sql.NullBool{Valid: false}
	}

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

	deletedAt := sql.NullTime{Valid: false}

	var newID uuid.UUID

	err := DB.QueryRow(query, userID, req.Breed, req.Name, req.Species, birthDate, gender, req.Color, sterilized, habitat, req.Notes, icon, deletedAt).Scan(&newID)
	if err != nil {
		log.Println("InsertPet error:", err)
		return uuid.Nil, err
	}
	return newID, nil
}

// GetPetsByUserID - функция получения питомцев по user_id
func GetPetsByUserID(userID string) ([]models.PetDB, error) {
	var pets []models.PetDB

	rows, err := DB.Query(`
	SELECT id, name, breed, species, icon
	FROM pet
	WHERE user_id = $1
	  AND deleted_at IS NULL
`, userID)
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

// GetPetByIDAndUserID - получить питомца по id и user_id
// Мягко удалённые питомцы (deleted_at заполнен) скрыты — обрабатываются как несуществующие.
func GetPetByIDAndUserID(petID uuid.UUID, userID string) (*models.PetIdResponse, error) {
	query := `
	SELECT id, name, gender, species, birth_date, color, sterilized,
	       habitation, notes, deleted_at, breed, icon
	FROM pet
	WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
	`

	var petDB models.PetIdDB

	err := DB.QueryRow(query, petID, userID).Scan(
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

	pet := models.PetIdResponse{
		ID:         petDB.ID.String(),
		Name:       petDB.Name,
		Species:    petDB.Species,
		Sterilized: petDB.Sterilized.Bool,
		IsDeleted:  petDB.DeletedAt.Valid,
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

// UpdatePet обновляет только те поля питомца, что переданы в запросе.
func UpdatePet(petID uuid.UUID, userID string, req models.UpdatePetRequest) error {
	setParts := []string{}
	args := []any{}
	argID := 1

	add := func(field string, value any) {
		setParts = append(setParts, field+" = $"+strconv.Itoa(argID))
		args = append(args, value)
		argID++
	}

	if req.Name != nil {
		add("name", *req.Name)
	}

	if req.Gender != nil {
		add("gender", *req.Gender)
	}

	if req.Species != nil {
		add("species", *req.Species)
	}

	if req.BirthDate != nil {
		t, err := time.Parse("2006-01-02", *req.BirthDate)
		if err != nil {
			return err
		}
		add("birth_date", t)
	}

	if req.Color != nil {
		add("color", *req.Color)
	}

	if req.Sterilized != nil {
		add("sterilized", *req.Sterilized)
	}

	if req.Habitation != nil {
		add("habitation", *req.Habitation)
	}

	if req.Notes != nil {
		add("notes", *req.Notes)
	}

	if req.Breed != nil {
		add("breed", *req.Breed)
	}

	if req.Icon != nil {
		add("icon", *req.Icon)
	}

	// is_deleted (через deleted_at)
	if req.IsDeleted != nil {
		if *req.IsDeleted {
			now := time.Now().UTC()
			add("deleted_at", now)
		} else {
			setParts = append(setParts, "deleted_at = NULL")
		}
	}

	if len(setParts) == 0 {
		return nil // нечего обновлять
	}

	query := fmt.Sprintf(`
		UPDATE pet
		SET %s
		WHERE id = $%d AND user_id = $%d
	`, strings.Join(setParts, ", "), argID, argID+1)

	args = append(args, petID, userID)

	_, err := DB.Exec(query, args...)
	return err
}

func DeletePet(petID uuid.UUID, userID string) error {
	query := `
		UPDATE pet
		SET deleted_at = $1
		WHERE id = $2 AND user_id = $3
	`

	now := time.Now().UTC()

	result, err := DB.Exec(query, now, petID, userID)
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

// GetPetIdDBByIDAndUserID - получить питомца по id и user_id (проверка принадлежности к пользователю)
func GetPetIdDBByIDAndUserID(petID uuid.UUID, userID string) (*models.PetIdDB, error) {
	query := `
	SELECT id, name, gender, species, birth_date, color, sterilized,
	       habitation, notes, deleted_at, breed, icon
	FROM pet
	WHERE id = $1 AND user_id = $2
	`

	var petDB models.PetIdDB

	err := DB.QueryRow(query, petID, userID).Scan(
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

// CheckPetBelongsToUser - проверка принадлежности питомца пользователю
func CheckPetBelongsToUser(petID uuid.UUID, userID string) (bool, error) {
	query := `SELECT COUNT(1) FROM pet WHERE id = $1 AND user_id = $2`
	var count int
	err := DB.QueryRow(query, petID, userID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count == 1, nil
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

// GetPetNameByID - получить имя питомца по ID
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
