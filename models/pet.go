package models

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

var allowedEventTypes = map[string]bool{
	"weight":     true,
	"urine":      true,
	"defecation": true,
	"vomit":      true,
	"diarrhea":   true,
	"other":      true,
}

func IsValidEventType(eventType string) bool {
	return allowedEventTypes[eventType]
}

type CreateEventRequest struct {
	PetID string  `json:"pet_id"`          // обязательный
	Date  string  `json:"date"`            // обязательный
	Type  string  `json:"type"`            // обязательный
	Notes *string `json:"notes,omitempty"` // необязательный
	Value string  `json:"value"`           // обязательный
}

type UpdateEventRequest struct {
	PetID string  `json:"pet_id"`          // обязательный
	Date  *string `json:"date,omitempty"`  // необязательный
	Type  *string `json:"type,omitempty"`  // необязательный
	Notes *string `json:"notes,omitempty"` // необязательный
	Value *string `json:"value,omitempty"` // необязательный
}

type EventResponse struct {
	ID      string  `json:"id"`
	Date    string  `json:"date"`
	Type    string  `json:"type"`
	Value   string  `json:"value"`
	Notes   *string `json:"notes,omitempty"`
	PetID   string  `json:"pet_id"`
	PetName string  `json:"pet_name"`
}

type EventDB struct {
	ID        uuid.UUID
	PetID     uuid.UUID
	Date      time.Time
	Type      string
	Notes     sql.NullString
	Value     string
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

type CreatePetRequest struct {
	Name       string  `json:"name"`                 // обязательное
	Gender     *string `json:"gender,omitempty"`     // перечисление
	Species    string  `json:"species"`              // обязательное
	BirthDate  *string `json:"birth_date,omitempty"` // дата в формате "YYYY-MM-DD"
	Color      *string `json:"color,omitempty"`
	Sterilized *bool   `json:"sterilized,omitempty"`
	Habitation *string `json:"habilitation,omitempty"` // enum: indoor, outside, both
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
	Habitation *string `json:"habilitation,omitempty"`
	Notes      *string `json:"notes,omitempty"`
	IsDeleted  bool    `json:"is_deleted"`
	Breed      *string `json:"breed,omitempty"`
	// Icon обязателен по спеке (GetPetProfileResponse.icon) — всегда заполняется,
	// при отсутствии значения в БД используется дефолт "OTHER" (см. database.GetPetByIDAndProfileID).
	Icon string `json:"icon"`
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

type UpdatePetRequest struct {
	Name       *string `json:"name,omitempty"`
	Gender     *string `json:"gender,omitempty"`
	Species    *string `json:"species,omitempty"`
	BirthDate  *string `json:"birth_date,omitempty"`
	Color      *string `json:"color,omitempty"`
	Sterilized *bool   `json:"sterilized,omitempty"`
	Habitation *string `json:"habilitation,omitempty"`
	Notes      *string `json:"notes,omitempty"`
	IsDeleted  *bool   `json:"is_deleted,omitempty"`
	Breed      *string `json:"breed,omitempty"`
	Icon       *string `json:"icon,omitempty"`
}
