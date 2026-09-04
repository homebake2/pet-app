package models

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Лимиты длины текстовых полей питомца при создании (POST /pet) и
// обновлении (PUT /pet/{id}) — общие для обоих эндпоинтов.
const (
	PetNameMaxLen  = 100
	PetNotesMaxLen = 1000
	PetBreedMaxLen = 100
	PetColorMaxLen = 100
)

// CreateEventRequest — тело запроса POST /events. Value — типизированный
// объект, форма которого определяется Type (см. пакет eventreg); хранится и
// передаётся как сырой JSON, чтобы одна и та же структура обслуживала все 14
// типов события без ветвления по типу в моделях.
type CreateEventRequest struct {
	PetID string          `json:"pet_id"`          // обязательный
	Date  string          `json:"date"`            // обязательный
	Type  string          `json:"type"`            // обязательный
	Notes *string         `json:"notes,omitempty"` // необязательный
	Value json.RawMessage `json:"value"`           // обязательный
}

// UpdateEventRequest — тело запроса PATCH /events/{id}. Value заменяется
// целиком; слияние вложенных полей объекта не поддерживается.
type UpdateEventRequest struct {
	PetID string           `json:"pet_id"`          // обязательный
	Date  *string          `json:"date,omitempty"`  // необязательный
	Type  *string          `json:"type,omitempty"`  // необязательный
	Notes *string          `json:"notes,omitempty"` // необязательный
	Value *json.RawMessage `json:"value,omitempty"` // необязательный
}

type EventResponse struct {
	ID      string          `json:"id"`
	Date    string          `json:"date"`
	Type    string          `json:"type"`
	Value   json.RawMessage `json:"value"`
	Notes   *string         `json:"notes,omitempty"`
	PetID   string          `json:"pet_id"`
	PetName string          `json:"pet_name"`
}

type EventDB struct {
	ID        uuid.UUID
	PetID     uuid.UUID
	Date      time.Time
	Type      string
	Notes     sql.NullString
	Value     json.RawMessage
	DeletedAt sql.NullTime
}

var allowedIcons = map[string]bool{
	"DOG":           true,
	"CAT":           true,
	"HAMSTER":       true,
	"GUINEA_PIG":    true,
	"RABBIT":        true,
	"PARROT":        true,
	"CANARY":        true,
	"FISH":          true,
	"TURTLE":        true,
	"RAT":           true,
	"MOUSE":         true,
	"FERRET":        true,
	"HEDGEHOG":      true,
	"CHINCHILLA":    true,
	"MINI_PIG":      true,
	"MINI_GOAT":     true,
	"CHICKEN":       true,
	"DUCK":          true,
	"PIGEON":        true,
	"IGUANA":        true,
	"GECKO":         true,
	"BEARDED_AGAMA": true,
	"SNAKE":         true,
	"PYTHON":        true,
	"FROG":          true,
	"AXOLOTL":       true,
	"TARANTULA":     true,
	"HERMIT_CRAB":   true,
	"ANT_FARM":      true,
	"SNAIL":         true,
	"OTHER":         true,
}

func IsValidIcon(icon string) bool {
	return allowedIcons[icon]
}

var allowedGenders = map[string]bool{
	"male":   true,
	"female": true,
	"other":  true,
}

func IsValidGender(gender string) bool {
	return allowedGenders[gender]
}

var allowedHabitations = map[string]bool{
	"indoor":  true,
	"outside": true,
	"both":    true,
}

func IsValidHabitation(habitation string) bool {
	return allowedHabitations[habitation]
}

// CreatePetRequest — тело запроса POST /pet.
type CreatePetRequest struct {
	Name       string  `json:"name"`                 // обязательное
	Gender     *string `json:"gender,omitempty"`     // перечисление
	Species    string  `json:"species"`              // обязательное
	BirthDate  *string `json:"birth_date,omitempty"` // дата в формате "YYYY-MM-DD"
	Color      *string `json:"color,omitempty"`
	Sterilized *bool   `json:"sterilized,omitempty"`
	Habitation *string `json:"habitation,omitempty"` // enum: indoor, outside, both
	Notes      *string `json:"notes,omitempty"`
	Breed      *string `json:"breed,omitempty"`
	Icon       *string `json:"icon,omitempty"` // enum: DOG, CAT, HAMSTER, GUINEA_PIG, RABBIT, PARROT, CANARY, FISH, TURTLE, RAT, MOUSE, FERRET, HEDGEHOG, CHINCHILLA, MINI_PIG, MINI_GOAT, CHICKEN, DUCK, PIGEON, IGUANA, GECKO, BEARDED_AGAMA, SNAKE, PYTHON, FROG, AXOLOTL, TARANTULA, HERMIT_CRAB, ANT_FARM, SNAIL, OTHER
}

type PetItem struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Breed   string `json:"breed"`
	Species string `json:"species"`
	Icon    string `json:"icon"`
	// PhotoURL — presigned GET URL на текущую фотографию питомца (owner_type
	// = "pet_photo" в generic-механизме файлов сущностей); null, если
	// фотографии нет. См. «Фотография питомца — Backend».
	PhotoURL *string `json:"photo_url"`
}

type PetResponse struct {
	Items []PetItem `json:"items"`
}

type PetDB struct {
	ID      uuid.UUID      `db:"id"`
	Name    string         `db:"name"`
	Breed   sql.NullString `db:"breed"`
	Species string         `db:"species"`
	Icon    sql.NullString `db:"icon"`
}

type PetIdResponse struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Gender     *string `json:"gender,omitempty"`
	Species    string  `json:"species"`
	BirthDate  *string `json:"birth_date,omitempty"`
	Color      *string `json:"color,omitempty"`
	Sterilized bool    `json:"sterilized"`
	Habitation *string `json:"habitation,omitempty"`
	Notes      *string `json:"notes,omitempty"`
	IsDeleted  bool    `json:"is_deleted"`
	Breed      *string `json:"breed,omitempty"`
	// Icon обязателен по спеке (GetPetProfileResponse.icon) — всегда заполняется,
	// при отсутствии значения в БД используется дефолт "OTHER" (см. database.GetPetByIDAndProfileID).
	Icon string `json:"icon"`
	// PhotoURL — presigned GET URL на текущую фотографию питомца; null, если
	// фотографии нет. PhotoFileID — id соответствующей строки file, нужен
	// клиенту для DELETE /files/{file_id}. Оба вычисляются на чтении join'ом
	// к таблице file, сама таблица pet полем для фотографии не дополняется
	// (см. «Фотография питомца — Backend»).
	PhotoURL    *string `json:"photo_url"`
	PhotoFileID *string `json:"photo_file_id"`
}

type PetIdDB struct {
	ID         uuid.UUID
	Name       string
	Gender     sql.NullString
	Species    string
	BirthDate  sql.NullTime
	Color      sql.NullString
	Sterilized sql.NullBool
	Habitation sql.NullString
	Notes      sql.NullString
	DeletedAt  sql.NullTime
	Breed      sql.NullString
	Icon       sql.NullString
}

// UpdatePetRequest — тело запроса PUT /pet/{id}.
type UpdatePetRequest struct {
	Name       *string `json:"name,omitempty"`
	Gender     *string `json:"gender,omitempty"`
	Species    *string `json:"species,omitempty"`
	BirthDate  *string `json:"birth_date,omitempty"`
	Color      *string `json:"color,omitempty"`
	Sterilized *bool   `json:"sterilized,omitempty"`
	Habitation *string `json:"habitation,omitempty"`
	Notes      *string `json:"notes,omitempty"`
	IsDeleted  *bool   `json:"is_deleted,omitempty"`
	Breed      *string `json:"breed,omitempty"`
	Icon       *string `json:"icon,omitempty"`
}
