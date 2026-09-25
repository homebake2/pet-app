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
	// NotificationsEnabled — необязательный, по умолчанию false. true
	// допустим только для события с датой строго в будущем (см. «Добавление
	// события — Backend», «Модель значения события и реестр метрик»).
	NotificationsEnabled *bool `json:"notifications_enabled,omitempty"`
}

// UpdateEventRequest — тело запроса PATCH /events/{id}. Value заменяется
// целиком; слияние вложенных полей объекта не поддерживается.
type UpdateEventRequest struct {
	PetID string           `json:"pet_id"`          // обязательный
	Date  *string          `json:"date,omitempty"`  // необязательный
	Type  *string          `json:"type,omitempty"`  // необязательный
	Notes *string          `json:"notes,omitempty"` // необязательный
	Value *json.RawMessage `json:"value,omitempty"` // необязательный
	// NotificationsEnabled — независимое от других полей необязательное
	// булево значение (см. «Редактирование события — Backend»). Итоговое
	// сочетание (переданное значение либо уже сохранённое) проверяется
	// против итоговой даты события.
	NotificationsEnabled *bool `json:"notifications_enabled,omitempty"`
}

type EventResponse struct {
	ID      string          `json:"id"`
	Date    string          `json:"date"`
	Type    string          `json:"type"`
	Value   json.RawMessage `json:"value"`
	Notes   *string         `json:"notes,omitempty"`
	PetID   string          `json:"pet_id"`
	PetName string          `json:"pet_name"`
	// NotificationsEnabled — сохранённое значение столбца
	// event.notifications_enabled, отдаётся как есть без дополнительной
	// валидации на чтении (см. «Модель значения события и реестр метрик»).
	NotificationsEnabled bool `json:"notifications_enabled"`
	// Files — прикреплённые файлы события (фото и документы), в порядке
	// position, не более 10 элементов (см. «Файлы события — Backend»).
	Files []EventFileItem `json:"files"`
}

type EventDB struct {
	ID                   uuid.UUID
	PetID                uuid.UUID
	Date                 time.Time
	Type                 string
	Notes                sql.NullString
	Value                json.RawMessage
	NotificationsEnabled bool
	DeletedAt            sql.NullTime
}

// knownSpeciesValues — закрытый набор значений species, распознаваемых для
// целей применимости типа события к виду питомца (см. eventreg). Поле
// species само по себе остаётся свободным текстом при создании/обновлении
// питомца — этот набор используется только справочно (раньше был закрытым
// перечислением поля icon, которое было удалено из API целиком).
var knownSpeciesValues = map[string]bool{
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

// IsKnownSpeciesValue сообщает, входит ли значение species в закрытый
// справочник видов, используемый для проверки применимости типа события
// (см. eventreg.IsApplicableToSpecies). Не является ограничением на
// произвольный ввод поля species при создании/обновлении питомца.
func IsKnownSpeciesValue(species string) bool {
	return knownSpeciesValues[species]
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
	// Weight — вес питомца в кг (0.001–400), опционально; НЕ сохраняется как
	// поле питомца. При передаче сервер создаёт событие типа weight со
	// значением amount=weight (см. «Вес питомца — Backend»).
	Weight *float64 `json:"weight,omitempty"`
	// BodyCondition — кондиция тела питомца (PetBodyConditionEnum), опционально
	// (см. «Ведпаспорт — Backend»). Хранится как поле pet.body_condition.
	BodyCondition *string `json:"body_condition,omitempty"`
}

type PetItem struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Breed   string `json:"breed"`
	Species string `json:"species"`
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
	// PhotoURL — presigned GET URL на текущую фотографию питомца; null, если
	// фотографии нет. PhotoFileID — id соответствующей строки file, нужен
	// клиенту для DELETE /files/{file_id}. Оба вычисляются на чтении join'ом
	// к таблице file, сама таблица pet полем для фотографии не дополняется
	// (см. «Фотография питомца — Backend»).
	PhotoURL    *string `json:"photo_url"`
	PhotoFileID *string `json:"photo_file_id"`
	// Weight — текущий вес питомца в кг, вычисляется как amount последнего по
	// date_time события типа weight; null, если таких событий нет. Не
	// хранится как отдельное поле питомца (см. «Вес питомца — Backend»).
	Weight *float64 `json:"weight"`
	// BodyCondition — кондиция тела питомца (PetBodyConditionEnum); null, если
	// не задана (см. «Ведпаспорт — Backend»).
	BodyCondition *string `json:"body_condition,omitempty"`
}

type PetIdDB struct {
	ID            uuid.UUID
	Name          string
	Gender        sql.NullString
	Species       string
	BirthDate     sql.NullTime
	Color         sql.NullString
	Sterilized    sql.NullBool
	Habitation    sql.NullString
	Notes         sql.NullString
	DeletedAt     sql.NullTime
	Breed         sql.NullString
	BodyCondition sql.NullString
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
	// Weight — вес питомца в кг (0.001–400), опционально и nullable.
	// Отсутствие ключа и явный null равнозначны: событие weight не
	// создаётся. Очистки веса через этот эндпоинт нет — только передача
	// нового числа создаёт новое событие weight (см. «Вес питомца — Backend»).
	Weight *float64 `json:"weight,omitempty"`
	// BodyCondition — кондиция тела питомца (PetBodyConditionEnum), nullable:
	// передача пустой строки "" очищает поле (то же соглашение, что и у
	// UpdateEventRequest.Notes), передача значения — обновляет его.
	BodyCondition *string `json:"body_condition,omitempty"`
}
