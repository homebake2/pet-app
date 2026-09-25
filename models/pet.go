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
	PetNameMaxLen            = 100
	PetNotesMaxLen           = 1000
	PetBreedMaxLen           = 100
	PetColorMaxLen           = 100
	PetMicrochipNumberMaxLen = 50
	PetRingNumberMaxLen      = 50
)

// Диапазоны значений профильных числовых полей питомца по видам (см.
// «Профильные поля питомца по видам»).
const (
	PetEnclosureVolumeLMin = 0.1
	PetEnclosureVolumeLMax = 5000
	PetGroupSizeMin        = 1
	PetGroupSizeMax        = 10000
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

var allowedSizeCategories = map[string]bool{
	"small":  true,
	"medium": true,
	"large":  true,
}

// IsValidSizeCategory проверяет значение профильного поля size_category
// (см. «Профильные поля питомца по видам», «Справочник значений»).
func IsValidSizeCategory(sizeCategory string) bool {
	return allowedSizeCategories[sizeCategory]
}

var allowedWaterTypes = map[string]bool{
	"freshwater": true,
	"saltwater":  true,
}

// IsValidWaterType проверяет значение профильного поля water_type (см.
// «Профильные поля питомца по видам», «Справочник значений»).
func IsValidWaterType(waterType string) bool {
	return allowedWaterTypes[waterType]
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

	// Профильные поля питомца по видам — все опциональны для любого species,
	// применимость к группе вида — только клиентское правило видимости формы
	// (см. «Профильные поля питомца по видам»).
	Microchipped     *bool    `json:"microchipped,omitempty"`
	MicrochipNumber  *string  `json:"microchip_number,omitempty"`
	SizeCategory     *string  `json:"size_category,omitempty"`
	Ringed           *bool    `json:"ringed,omitempty"`
	RingNumber       *string  `json:"ring_number,omitempty"`
	UVLampRequired   *bool    `json:"uv_lamp_required,omitempty"`
	WaterType        *string  `json:"water_type,omitempty"`
	EnclosureVolumeL *float64 `json:"enclosure_volume_l,omitempty"`
	GroupSize        *int     `json:"group_size,omitempty"`
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

	// Профильные поля питомца по видам; null, если не заданы (см. «Профильные
	// поля питомца по видам»).
	Microchipped     *bool    `json:"microchipped,omitempty"`
	MicrochipNumber  *string  `json:"microchip_number,omitempty"`
	SizeCategory     *string  `json:"size_category,omitempty"`
	Ringed           *bool    `json:"ringed,omitempty"`
	RingNumber       *string  `json:"ring_number,omitempty"`
	UVLampRequired   *bool    `json:"uv_lamp_required,omitempty"`
	WaterType        *string  `json:"water_type,omitempty"`
	EnclosureVolumeL *float64 `json:"enclosure_volume_l,omitempty"`
	GroupSize        *int     `json:"group_size,omitempty"`
}

type PetIdDB struct {
	ID               uuid.UUID
	Name             string
	Gender           sql.NullString
	Species          string
	BirthDate        sql.NullTime
	Color            sql.NullString
	Sterilized       sql.NullBool
	Habitation       sql.NullString
	Notes            sql.NullString
	DeletedAt        sql.NullTime
	Breed            sql.NullString
	BodyCondition    sql.NullString
	Microchipped     sql.NullBool
	MicrochipNumber  sql.NullString
	SizeCategory     sql.NullString
	Ringed           sql.NullBool
	RingNumber       sql.NullString
	UVLampRequired   sql.NullBool
	WaterType        sql.NullString
	EnclosureVolumeL sql.NullFloat64
	GroupSize        sql.NullInt64
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

	// Профильные поля питомца по видам (см. «Профильные поля питомца по
	// видам», «Редактирование питомца — Backend»). Булевы поля — обычные
	// *bool (отсутствие ключа не меняет значение). Строковые поля
	// (MicrochipNumber, RingNumber) следуют соглашению BodyCondition: пустая
	// строка "" очищает поле.
	Microchipped    *bool   `json:"microchipped,omitempty"`
	MicrochipNumber *string `json:"microchip_number,omitempty"`
	Ringed          *bool   `json:"ringed,omitempty"`
	RingNumber      *string `json:"ring_number,omitempty"`
	UVLampRequired  *bool   `json:"uv_lamp_required,omitempty"`

	// SizeCategory, WaterType, EnclosureVolumeL, GroupSize — nullable
	// enum/числовые поля, где "отсутствие ключа" и "явный null" должны
	// различаться (отсутствие ключа не меняет значение, явный null очищает
	// поле) — в отличие от BodyCondition, тип не позволяет использовать
	// пустую строку как признак очистки. Значение по этим четырём полям
	// заполняется при декодировании тела запроса как обычно (nil как для
	// absent, так и для null); отдельно, до вызова database.UpdatePet,
	// обработчик (см. handlers.UpdatePetHandler) разбирает тело запроса как
	// map[string]json.RawMessage и заполняет соответствующий Clear*-флаг,
	// если ключ присутствует и его значение — буквально "null". Флаги не
	// участвуют в JSON (де)сериализации.
	SizeCategory          *string  `json:"size_category,omitempty"`
	ClearSizeCategory     bool     `json:"-"`
	WaterType             *string  `json:"water_type,omitempty"`
	ClearWaterType        bool     `json:"-"`
	EnclosureVolumeL      *float64 `json:"enclosure_volume_l,omitempty"`
	ClearEnclosureVolumeL bool     `json:"-"`
	GroupSize             *int     `json:"group_size,omitempty"`
	ClearGroupSize        bool     `json:"-"`
}

// ApplyExplicitNullClears разбирает сырое тело запроса PUT /pet/{id} и
// заставляет Clear*-флаги для тех из четырёх nullable enum/числовых
// профильных полей (size_category, water_type, enclosure_volume_l,
// group_size), что присутствуют в теле запроса как явный JSON null —
// в отличие от отсутствия ключа, которое не должно менять сохранённое
// значение (см. «Редактирование питомца — Backend»). Вызывается один раз
// сразу после json.Unmarshal(body, &req) с тем же телом запроса.
func (r *UpdatePetRequest) ApplyExplicitNullClears(rawBody []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &raw); err != nil {
		return err
	}

	isExplicitNull := func(key string) bool {
		value, present := raw[key]
		return present && string(value) == "null"
	}

	r.ClearSizeCategory = isExplicitNull("size_category")
	r.ClearWaterType = isExplicitNull("water_type")
	r.ClearEnclosureVolumeL = isExplicitNull("enclosure_volume_l")
	r.ClearGroupSize = isExplicitNull("group_size")

	return nil
}
