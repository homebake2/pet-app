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
	return insertPetWith(DB, userID, req)
}

// insertPetWith — то же самое, что InsertPet, но принимает произвольный
// dbExecutor: используется как для обычных запросов (DB), так и внутри
// транзакции переноса локальных данных (см. ImportLocalData).
func insertPetWith(exec dbExecutor, userID string, req models.CreatePetRequest) (uuid.UUID, error) {
	query := `
        INSERT INTO pet (
            user_id, breed, name, species, birth_date, gender, color, sterilized, habitation, notes, deleted_at, body_condition,
            microchipped, microchip_number, size_category, ringed, ring_number, uv_lamp_required, water_type, enclosure_volume_l, group_size
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
            $13, $14, $15, $16, $17, $18, $19, $20, $21
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

	deletedAt := sql.NullTime{Valid: false}

	var bodyCondition sql.NullString
	if req.BodyCondition != nil {
		bodyCondition = sql.NullString{String: *req.BodyCondition, Valid: true}
	} else {
		bodyCondition = sql.NullString{Valid: false}
	}

	var microchipped sql.NullBool
	if req.Microchipped != nil {
		microchipped = sql.NullBool{Bool: *req.Microchipped, Valid: true}
	} else {
		microchipped = sql.NullBool{Valid: false}
	}

	var microchipNumber sql.NullString
	if req.MicrochipNumber != nil {
		microchipNumber = sql.NullString{String: *req.MicrochipNumber, Valid: true}
	} else {
		microchipNumber = sql.NullString{Valid: false}
	}

	var sizeCategory sql.NullString
	if req.SizeCategory != nil {
		sizeCategory = sql.NullString{String: *req.SizeCategory, Valid: true}
	} else {
		sizeCategory = sql.NullString{Valid: false}
	}

	var ringed sql.NullBool
	if req.Ringed != nil {
		ringed = sql.NullBool{Bool: *req.Ringed, Valid: true}
	} else {
		ringed = sql.NullBool{Valid: false}
	}

	var ringNumber sql.NullString
	if req.RingNumber != nil {
		ringNumber = sql.NullString{String: *req.RingNumber, Valid: true}
	} else {
		ringNumber = sql.NullString{Valid: false}
	}

	var uvLampRequired sql.NullBool
	if req.UVLampRequired != nil {
		uvLampRequired = sql.NullBool{Bool: *req.UVLampRequired, Valid: true}
	} else {
		uvLampRequired = sql.NullBool{Valid: false}
	}

	var waterType sql.NullString
	if req.WaterType != nil {
		waterType = sql.NullString{String: *req.WaterType, Valid: true}
	} else {
		waterType = sql.NullString{Valid: false}
	}

	var enclosureVolumeL sql.NullFloat64
	if req.EnclosureVolumeL != nil {
		enclosureVolumeL = sql.NullFloat64{Float64: *req.EnclosureVolumeL, Valid: true}
	} else {
		enclosureVolumeL = sql.NullFloat64{Valid: false}
	}

	var groupSize sql.NullInt64
	if req.GroupSize != nil {
		groupSize = sql.NullInt64{Int64: int64(*req.GroupSize), Valid: true}
	} else {
		groupSize = sql.NullInt64{Valid: false}
	}

	var newID uuid.UUID

	err := exec.QueryRow(
		query,
		userID, req.Breed, req.Name, req.Species, birthDate, gender, req.Color, sterilized, habitat, req.Notes, deletedAt, bodyCondition,
		microchipped, microchipNumber, sizeCategory, ringed, ringNumber, uvLampRequired, waterType, enclosureVolumeL, groupSize,
	).Scan(&newID)
	if err != nil {
		log.Println("InsertPet error:", err)
		return uuid.Nil, err
	}
	return newID, nil
}

// ReservePetIdempotencyKey резервирует пару (user_id, idempotency_key) перед
// созданием питомца. reserved=true — ключ использован этим пользователем
// впервые, можно продолжать создание питомца как обычно. reserved=false —
// ключ уже зарегистрирован (конфликт по PRIMARY KEY), см.
// GetPetIDByIdempotencyKey для определения дальнейшего шага.
func ReservePetIdempotencyKey(userID string, idempotencyKey string) (reserved bool, err error) {
	result, err := DB.Exec(`
		INSERT INTO pet_idempotency_key (user_id, idempotency_key, pet_id)
		VALUES ($1, $2, NULL)
		ON CONFLICT (user_id, idempotency_key) DO NOTHING
	`, userID, idempotencyKey)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

// GetPetIDByIdempotencyKey возвращает pet_id, ранее зарезервированный под
// (user_id, idempotency_key). hasPetID=false означает редкую гонку
// параллельных запросов с одним ключом (резервирование ещё не завершено).
func GetPetIDByIdempotencyKey(userID string, idempotencyKey string) (petID uuid.UUID, hasPetID bool, err error) {
	var petIDNull sql.NullString
	err = DB.QueryRow(`
		SELECT pet_id FROM pet_idempotency_key WHERE user_id = $1 AND idempotency_key = $2
	`, userID, idempotencyKey).Scan(&petIDNull)
	if err != nil {
		return uuid.Nil, false, err
	}
	if !petIDNull.Valid {
		return uuid.Nil, false, nil
	}
	petID, err = uuid.Parse(petIDNull.String)
	if err != nil {
		return uuid.Nil, false, err
	}
	return petID, true, nil
}

// FinalizePetIdempotencyKey связывает ранее зарезервированный ключ с id
// только что созданного питомца.
func FinalizePetIdempotencyKey(userID string, idempotencyKey string, petID uuid.UUID) error {
	_, err := DB.Exec(`
		UPDATE pet_idempotency_key SET pet_id = $1 WHERE user_id = $2 AND idempotency_key = $3
	`, petID, userID, idempotencyKey)
	return err
}

// GetPetsByUserID - функция получения питомцев по user_id
func GetPetsByUserID(userID string) ([]models.PetDB, error) {
	var pets []models.PetDB

	rows, err := DB.Query(`
	SELECT id, name, breed, species
	FROM pet
	WHERE user_id = $1
	  AND deleted_at IS NULL
	ORDER BY created_at
`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var pet models.PetDB
		if err := rows.Scan(&pet.ID, &pet.Name, &pet.Breed, &pet.Species); err != nil {
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
	       habitation, notes, deleted_at, breed, body_condition,
	       microchipped, microchip_number, size_category, ringed, ring_number,
	       uv_lamp_required, water_type, enclosure_volume_l, group_size
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
		&petDB.BodyCondition,
		&petDB.Microchipped,
		&petDB.MicrochipNumber,
		&petDB.SizeCategory,
		&petDB.Ringed,
		&petDB.RingNumber,
		&petDB.UVLampRequired,
		&petDB.WaterType,
		&petDB.EnclosureVolumeL,
		&petDB.GroupSize,
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

	if petDB.BodyCondition.Valid {
		pet.BodyCondition = &petDB.BodyCondition.String
	}

	applyPetProfileFields(&pet, &petDB)

	weight, err := GetLatestPetWeight(petID)
	if err != nil {
		return nil, err
	}
	pet.Weight = weight

	return &pet, nil
}

// applyPetProfileFields копирует профильные поля питомца по видам (см.
// «Профильные поля питомца по видам») из строки БД в ответ API. Общая для
// GetPetByIDAndUserID и любого другого места, читающего PetIdDB в
// PetIdResponse.
func applyPetProfileFields(pet *models.PetIdResponse, petDB *models.PetIdDB) {
	if petDB.Microchipped.Valid {
		pet.Microchipped = &petDB.Microchipped.Bool
	}
	if petDB.MicrochipNumber.Valid {
		pet.MicrochipNumber = &petDB.MicrochipNumber.String
	}
	if petDB.SizeCategory.Valid {
		pet.SizeCategory = &petDB.SizeCategory.String
	}
	if petDB.Ringed.Valid {
		pet.Ringed = &petDB.Ringed.Bool
	}
	if petDB.RingNumber.Valid {
		pet.RingNumber = &petDB.RingNumber.String
	}
	if petDB.UVLampRequired.Valid {
		pet.UVLampRequired = &petDB.UVLampRequired.Bool
	}
	if petDB.WaterType.Valid {
		pet.WaterType = &petDB.WaterType.String
	}
	if petDB.EnclosureVolumeL.Valid {
		pet.EnclosureVolumeL = &petDB.EnclosureVolumeL.Float64
	}
	if petDB.GroupSize.Valid {
		groupSize := int(petDB.GroupSize.Int64)
		pet.GroupSize = &groupSize
	}
}

// GetLatestPetWeight возвращает текущий вес питомца — amount последнего по
// date_time (неудалённого) события типа weight для этого питомца, либо nil,
// если таких событий нет. Вес НЕ хранится как поле pet: это единая функция
// вычисления, используемая GET /pet/{id} и read-back POST/PUT /pet, и
// отражает любое событие weight независимо от того, где оно было создано
// (POST /pet, PUT /pet/{id} или обычный POST /events) — см. «Вес питомца —
// Backend».
func GetLatestPetWeight(petID uuid.UUID) (*float64, error) {
	var amount sql.NullFloat64
	err := DB.QueryRow(`
		SELECT (value->>'amount')::float8
		FROM event
		WHERE pet_id = $1 AND type = 'weight' AND deleted_at IS NULL
		ORDER BY date_time DESC
		LIMIT 1
	`, petID).Scan(&amount)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		log.Println("GetLatestPetWeight error:", err)
		return nil, err
	}
	if !amount.Valid {
		return nil, nil
	}
	return &amount.Float64, nil
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

	if req.BodyCondition != nil {
		if *req.BodyCondition == "" {
			add("body_condition", sql.NullString{Valid: false})
		} else {
			add("body_condition", *req.BodyCondition)
		}
	}

	// Профильные поля питомца по видам (см. «Профильные поля питомца по
	// видам», «Редактирование питомца — Backend»): булевы поля — обычные
	// *bool; microchip_number/ring_number следуют соглашению body_condition
	// (пустая строка очищает); size_category/water_type/enclosure_volume_l/
	// group_size различают "ключ отсутствует" (req.* == nil, ничего не
	// делаем) от "явный null" (Clear*-флаг, заполненный
	// UpdatePetRequest.ApplyExplicitNullClears в хендлере) — очищаем поле.

	if req.Microchipped != nil {
		add("microchipped", *req.Microchipped)
	}

	if req.MicrochipNumber != nil {
		if *req.MicrochipNumber == "" {
			add("microchip_number", sql.NullString{Valid: false})
		} else {
			add("microchip_number", *req.MicrochipNumber)
		}
	}

	if req.SizeCategory != nil {
		add("size_category", *req.SizeCategory)
	} else if req.ClearSizeCategory {
		add("size_category", sql.NullString{Valid: false})
	}

	if req.Ringed != nil {
		add("ringed", *req.Ringed)
	}

	if req.RingNumber != nil {
		if *req.RingNumber == "" {
			add("ring_number", sql.NullString{Valid: false})
		} else {
			add("ring_number", *req.RingNumber)
		}
	}

	if req.UVLampRequired != nil {
		add("uv_lamp_required", *req.UVLampRequired)
	}

	if req.WaterType != nil {
		add("water_type", *req.WaterType)
	} else if req.ClearWaterType {
		add("water_type", sql.NullString{Valid: false})
	}

	if req.EnclosureVolumeL != nil {
		add("enclosure_volume_l", *req.EnclosureVolumeL)
	} else if req.ClearEnclosureVolumeL {
		add("enclosure_volume_l", sql.NullFloat64{Valid: false})
	}

	if req.GroupSize != nil {
		add("group_size", *req.GroupSize)
	} else if req.ClearGroupSize {
		add("group_size", sql.NullInt64{Valid: false})
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
	       habitation, notes, deleted_at, breed, body_condition,
	       microchipped, microchip_number, size_category, ringed, ring_number,
	       uv_lamp_required, water_type, enclosure_volume_l, group_size
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
		&petDB.BodyCondition,
		&petDB.Microchipped,
		&petDB.MicrochipNumber,
		&petDB.SizeCategory,
		&petDB.Ringed,
		&petDB.RingNumber,
		&petDB.UVLampRequired,
		&petDB.WaterType,
		&petDB.EnclosureVolumeL,
		&petDB.GroupSize,
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

// CheckPetOwnership проверяет владение питомцем по правилу, зарегистрированному
// для owner_type = "pet_photo" в реестре типов владельцев generic-механизма
// файлов сущностей (см. handlers/files.go): pet.id = petID AND
// pet.user_id = userID AND pet.deleted_at IS NULL — дословно то же правило,
// что и на остальных эндпоинтах питомца (GET/PUT/DELETE /pet/{id}).
func CheckPetOwnership(petID uuid.UUID, userID string) (bool, error) {
	query := `SELECT COUNT(1) FROM pet WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`
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
	       habitation, notes, deleted_at, breed, body_condition,
	       microchipped, microchip_number, size_category, ringed, ring_number,
	       uv_lamp_required, water_type, enclosure_volume_l, group_size
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
		&petDB.BodyCondition,
		&petDB.Microchipped,
		&petDB.MicrochipNumber,
		&petDB.SizeCategory,
		&petDB.Ringed,
		&petDB.RingNumber,
		&petDB.UVLampRequired,
		&petDB.WaterType,
		&petDB.EnclosureVolumeL,
		&petDB.GroupSize,
	)

	if err != nil {
		return nil, err
	}

	return &petDB, nil
}

// CountPetsByUserID возвращает количество не мягко удалённых питомцев
// пользователя — используется полем `pets_count` в ответах login/register/
// guest (см. GetLoginResponse).
func CountPetsByUserID(userID string) (int, error) {
	var count int
	err := DB.QueryRow(`
		SELECT COUNT(*) FROM pet WHERE user_id = $1 AND deleted_at IS NULL
	`, userID).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
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
