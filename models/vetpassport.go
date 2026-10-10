// Package models: типы фичи «Ведпаспорт» (медкарта питомца) — 5 pet_id-scoped
// сущностей (Vaccination, Disease, VetVisit, Allergy, Medication), каждая с
// soft-delete (deleted_at) и до 10 файлов через generic-механизм файлов
// сущностей (см. handlers/files.go). Контракт — components.schemas
// GetVaccination*/GetDisease*/GetVetVisit*/GetAllergy*/GetMedication* в
// open-api/spec.json.
package models

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Лимиты длины текстовых полей — см. соответствующие схемы spec.json.
const (
	VaccinationNameMaxLen = 100
	// VaccinationEventLabelMaxLen — лимит event_label в символах (рунах).
	VaccinationEventLabelMaxLen = 150
	DiseaseNameMaxLen           = 100
	DiseaseNoteMaxLen           = 1000
	VetVisitReasonMaxLen        = 200
	VetVisitClinicMaxLen        = 200
	VetVisitNoteMaxLen          = 1000
	AllergyAllergenMaxLen       = 100
	AllergyReactionMaxLen       = 200
	AllergyNoteMaxLen           = 1000
	MedicationNameMaxLen        = 100
	MedicationDosageMaxLen      = 100
	MedicationNoteMaxLen        = 1000
	MedicationDoseNoteMaxLen    = 100
	MedicationMinIntervalDays   = 2
	MedicationMaxIntervalDays   = 365
	MedicationMinWeekday        = 1
	MedicationMaxWeekday        = 7
	MedicationMinTimesCount     = 1
	MedicationMaxTimesCount     = 4
	// MedicationScheduleMaxLookaheadDays — предохранитель от бесконечного
	// перебора дат в расчёте next_dose: теоретический случай, недостижимый на
	// валидных weekdays/interval_days.
	MedicationScheduleMaxLookaheadDays = 730
)

// MedicationFrequency* — значения MedicationFrequencyTypeEnum.
const (
	MedicationFrequencyDaily        = "daily"
	MedicationFrequencySpecificDays = "specific_days"
	MedicationFrequencyEveryNDays   = "every_n_days"
	MedicationFrequencyAsNeeded     = "as_needed"
)

var allowedMedicationFrequencyTypes = map[string]bool{
	MedicationFrequencyDaily:        true,
	MedicationFrequencySpecificDays: true,
	MedicationFrequencyEveryNDays:   true,
	MedicationFrequencyAsNeeded:     true,
}

// IsValidMedicationFrequencyType проверяет значение MedicationFrequencyTypeEnum.
func IsValidMedicationFrequencyType(v string) bool {
	return allowedMedicationFrequencyTypes[v]
}

var allowedBodyConditions = map[string]bool{
	"underweight": true,
	"thin":        true,
	"normal":      true,
	"overweight":  true,
	"obese":       true,
}

// IsValidBodyCondition проверяет значение PetBodyConditionEnum (pet.body_condition).
func IsValidBodyCondition(v string) bool {
	return allowedBodyConditions[v]
}

var allowedDiseaseStatuses = map[string]bool{
	"active": true,
	"cured":  true,
}

// IsValidDiseaseStatus проверяет значение DiseaseStatusEnum.
func IsValidDiseaseStatus(v string) bool {
	return allowedDiseaseStatuses[v]
}

var allowedAllergySeverities = map[string]bool{
	"mild":     true,
	"moderate": true,
	"severe":   true,
}

// IsValidAllergySeverity проверяет значение AllergySeverityEnum.
func IsValidAllergySeverity(v string) bool {
	return allowedAllergySeverities[v]
}

// ---------------------------------------------------------------------------
// Vaccination
// ---------------------------------------------------------------------------

type VaccinationDB struct {
	ID                  uuid.UUID
	PetID               uuid.UUID
	Name                string
	AdministeredDate    time.Time
	NextDate            sql.NullTime
	AdministeredEventID uuid.NullUUID
	// NextPlanID — настройки напоминания на дату следующей вакцинации;
	// NULL, если напоминания нет либо его настроек уже нет.
	NextPlanID uuid.NullUUID
	DeletedAt  sql.NullTime
}

// CreateVaccinationRequest — тело запроса POST /pet/{id}/vaccinations.
type CreateVaccinationRequest struct {
	Name              string  `json:"name"`
	AdministeredDate  string  `json:"administered_date"`
	NextDate          *string `json:"next_date,omitempty"`
	AddReminderOnNext *bool   `json:"add_reminder_on_next,omitempty"`
	// AdministeredTime — время суток факта на дату введения (факт создаётся
	// всегда); NextTime — время суток напоминания на next_date. Не
	// хранятся в самой прививке (см. описание GetVaccinationRequest в
	// spec.json).
	AdministeredTime *string `json:"administered_time,omitempty"`
	NextTime         *string `json:"next_time,omitempty"`
	// EventLabel — опциональная, уже локализованная клиентом подпись
	// создаваемых записей (value.label, обрезается сервером до 50 рун). Если
	// не передана — "Вакцинация: " + name. Не хранится в самой прививке.
	EventLabel *string `json:"event_label,omitempty"`
}

// UpdateVaccinationRequest — тело запроса PATCH /vaccinations/{id}. Nullable
// строковые поля (next_date, next_time) очищаются передачей пустой строки
// "" — тем же соглашением, что и UpdateEventRequest.Notes.
type UpdateVaccinationRequest struct {
	Name              *string `json:"name,omitempty"`
	AdministeredDate  *string `json:"administered_date,omitempty"`
	NextDate          *string `json:"next_date,omitempty"`
	AddReminderOnNext *bool   `json:"add_reminder_on_next,omitempty"`
	AdministeredTime  *string `json:"administered_time,omitempty"`
	NextTime          *string `json:"next_time,omitempty"`
	// EventLabel — см. CreateVaccinationRequest.EventLabel.
	EventLabel *string `json:"event_label,omitempty"`
}

type VaccinationResponse struct {
	ID                  string  `json:"id"`
	PetID               string  `json:"pet_id"`
	Name                string  `json:"name"`
	AdministeredDate    string  `json:"administered_date"`
	NextDate            *string `json:"next_date,omitempty"`
	AdministeredEventID *string `json:"administered_event_id,omitempty"`
	NextPlanID          *string `json:"next_plan_id,omitempty"`
	FilesCount          int     `json:"files_count"`
}

type VaccinationListResponse struct {
	Items []VaccinationResponse `json:"items"`
}

// ---------------------------------------------------------------------------
// Disease
// ---------------------------------------------------------------------------

type DiseaseDB struct {
	ID            uuid.UUID
	PetID         uuid.UUID
	Name          string
	DiagnosedDate time.Time
	Status        string
	Note          sql.NullString
	DeletedAt     sql.NullTime
}

type CreateDiseaseRequest struct {
	Name          string  `json:"name"`
	DiagnosedDate string  `json:"diagnosed_date"`
	Status        string  `json:"status"`
	Note          *string `json:"note,omitempty"`
}

type UpdateDiseaseRequest struct {
	Name          *string `json:"name,omitempty"`
	DiagnosedDate *string `json:"diagnosed_date,omitempty"`
	Status        *string `json:"status,omitempty"`
	Note          *string `json:"note,omitempty"`
}

type DiseaseResponse struct {
	ID            string  `json:"id"`
	PetID         string  `json:"pet_id"`
	Name          string  `json:"name"`
	DiagnosedDate string  `json:"diagnosed_date"`
	Status        string  `json:"status"`
	Note          *string `json:"note,omitempty"`
	FilesCount    int     `json:"files_count"`
}

type DiseaseListResponse struct {
	Items []DiseaseResponse `json:"items"`
}

// ---------------------------------------------------------------------------
// VetVisit
// ---------------------------------------------------------------------------

type VetVisitDB struct {
	ID        uuid.UUID
	PetID     uuid.UUID
	VisitDate time.Time
	Reason    string
	Clinic    sql.NullString
	Note      sql.NullString
	DeletedAt sql.NullTime
}

type CreateVetVisitRequest struct {
	VisitDate string  `json:"visit_date"`
	Reason    string  `json:"reason"`
	Clinic    *string `json:"clinic,omitempty"`
	Note      *string `json:"note,omitempty"`
}

type UpdateVetVisitRequest struct {
	VisitDate *string `json:"visit_date,omitempty"`
	Reason    *string `json:"reason,omitempty"`
	Clinic    *string `json:"clinic,omitempty"`
	Note      *string `json:"note,omitempty"`
}

type VetVisitResponse struct {
	ID         string  `json:"id"`
	PetID      string  `json:"pet_id"`
	VisitDate  string  `json:"visit_date"`
	Reason     string  `json:"reason"`
	Clinic     *string `json:"clinic,omitempty"`
	Note       *string `json:"note,omitempty"`
	FilesCount int     `json:"files_count"`
}

type VetVisitListResponse struct {
	Items []VetVisitResponse `json:"items"`
}

// ---------------------------------------------------------------------------
// Allergy
// ---------------------------------------------------------------------------

type AllergyDB struct {
	ID           uuid.UUID
	PetID        uuid.UUID
	Allergen     string
	Reaction     sql.NullString
	DetectedDate sql.NullTime
	Severity     string
	Note         sql.NullString
	DeletedAt    sql.NullTime
}

type CreateAllergyRequest struct {
	Allergen     string  `json:"allergen"`
	Reaction     *string `json:"reaction,omitempty"`
	DetectedDate *string `json:"detected_date,omitempty"`
	Severity     string  `json:"severity"`
	Note         *string `json:"note,omitempty"`
}

type UpdateAllergyRequest struct {
	Allergen     *string `json:"allergen,omitempty"`
	Reaction     *string `json:"reaction,omitempty"`
	DetectedDate *string `json:"detected_date,omitempty"`
	Severity     *string `json:"severity,omitempty"`
	Note         *string `json:"note,omitempty"`
}

type AllergyResponse struct {
	ID           string  `json:"id"`
	PetID        string  `json:"pet_id"`
	Allergen     string  `json:"allergen"`
	Reaction     *string `json:"reaction,omitempty"`
	DetectedDate *string `json:"detected_date,omitempty"`
	Severity     string  `json:"severity"`
	Note         *string `json:"note,omitempty"`
	FilesCount   int     `json:"files_count"`
}

type AllergyListResponse struct {
	Items []AllergyResponse `json:"items"`
}

// ---------------------------------------------------------------------------
// Medication
// ---------------------------------------------------------------------------

// MedicationTimeSlot — элемент times: время приёма в день + опциональная
// собственная доза (иначе используется medication.dosage), см.
// MedicationTimeSlot в open-api/spec.json.
type MedicationTimeSlot struct {
	Time     string  `json:"time"`
	DoseNote *string `json:"dose_note,omitempty"`
}

// OptionalField различает три состояния nullable-поля PATCH-запроса:
// поле отсутствует в теле (Set=false), поле передано как JSON null
// (Set=true, Value=nil), поле передано со значением (Set=true, Value=&v).
// Обычный *T (без обёртки) через encoding/json не различает "поле
// отсутствует" и "поле явно null" — оба декодируются в nil. Эта разница
// нужна PATCH /medications/{id}: правила согласования полей расписания с
// frequency_type требуют знать, было ли поле явно передано в запросе (в
// том числе как null, чтобы обнулить его), а не только его итоговое
// значение.
type OptionalField[T any] struct {
	Set   bool
	Value *T
}

func (o *OptionalField[T]) UnmarshalJSON(data []byte) error {
	o.Set = true
	if string(data) == "null" {
		o.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}

// MarshalJSON — используется только тестовыми хелперами, которые строят
// UpdateMedicationRequest в Go и сериализуют его как настоящий клиент (см.
// handlers/vetpassport_test.go); реальный HTTP-запрос сервер только
// декодирует. В паре с тегом `omitzero` на полях UpdateMedicationRequest
// (Set=false — нулевое значение структуры) это позволяет тестам передавать
// "поле не задано" через zero-value и "поле явно null" через
// {Set:true, Value:nil}.
func (o OptionalField[T]) MarshalJSON() ([]byte, error) {
	if !o.Set || o.Value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(o.Value)
}

type MedicationDB struct {
	ID            uuid.UUID
	PetID         uuid.UUID
	Name          string
	Dosage        string
	FrequencyType string
	// Weekdays/Times — nil означает NULL в БД (jsonb), не пустой массив.
	Weekdays     []int
	IntervalDays sql.NullInt64
	Times        []MedicationTimeSlot
	StartDate    sql.NullTime
	EndDate      sql.NullTime
	// ReminderPlanID — настройки напоминания набора приёмов; NULL, если
	// набора нет либо его настроек уже нет.
	ReminderPlanID uuid.NullUUID
	Note           sql.NullString
	DeletedAt      sql.NullTime
	// CreatedAt — вторичный ключ сортировки списка (по next_dose возр., затем
	// created_at убыв.), см. GetPetMedicationsHandler.
	CreatedAt time.Time
}

// CreateMedicationRequest — тело запроса POST /pet/{id}/medications
// (GetMedicationRequest в spec.json). Для создания различать "поле
// отсутствует" и "поле null" не требуется — оба значат "не задано" (см.
// правила согласования полей расписания с frequency_type).
type CreateMedicationRequest struct {
	Name          string               `json:"name"`
	Dosage        string               `json:"dosage"`
	FrequencyType string               `json:"frequency_type"`
	Weekdays      []int                `json:"weekdays,omitempty"`
	IntervalDays  *int                 `json:"interval_days,omitempty"`
	Times         []MedicationTimeSlot `json:"times,omitempty"`
	StartDate     *string              `json:"start_date,omitempty"`
	EndDate       *string              `json:"end_date,omitempty"`
	Note          *string              `json:"note,omitempty"`
	AddReminders  *bool                `json:"add_reminders,omitempty"`
}

// UpdateMedicationRequest — тело запроса PATCH /medications/{id}
// (UpdateMedicationRequest в spec.json). Поля расписания используют
// OptionalField, чтобы отличить "не менять" от "явно обнулить" —
// см. "Лекарства — Backend", раздел «Редактирование».
type UpdateMedicationRequest struct {
	Name          *string `json:"name,omitempty"`
	Dosage        *string `json:"dosage,omitempty"`
	FrequencyType *string `json:"frequency_type,omitempty"`

	Weekdays     OptionalField[[]int]                `json:"weekdays,omitzero"`
	IntervalDays OptionalField[int]                  `json:"interval_days,omitzero"`
	Times        OptionalField[[]MedicationTimeSlot] `json:"times,omitzero"`
	StartDate    OptionalField[string]               `json:"start_date,omitzero"`
	EndDate      OptionalField[string]               `json:"end_date,omitzero"`

	// Note — "" очищает поле (то же соглашение, что у остальных сущностей
	// ветпаспорта), отсутствие поля не изменяет его.
	Note *string `json:"note,omitempty"`

	RegenerateReminders *bool `json:"regenerate_reminders,omitempty"`
}

type MedicationResponse struct {
	ID            string                `json:"id"`
	PetID         string                `json:"pet_id"`
	Name          string                `json:"name"`
	Dosage        string                `json:"dosage"`
	FrequencyType string                `json:"frequency_type"`
	Weekdays      *[]int                `json:"weekdays,omitempty"`
	IntervalDays  *int                  `json:"interval_days,omitempty"`
	Times         *[]MedicationTimeSlot `json:"times,omitempty"`
	StartDate     *string               `json:"start_date,omitempty"`
	EndDate       *string               `json:"end_date,omitempty"`
	NextDose      *string               `json:"next_dose,omitempty"`
	// ReminderPlanID — настройки напоминания набора; null, если набора нет.
	ReminderPlanID *string `json:"reminder_plan_id"`
	Note           *string `json:"note,omitempty"`
	FilesCount     int     `json:"files_count"`
}

type MedicationListResponse struct {
	Items []MedicationResponse `json:"items"`
}

// VaccinationLinkIDs — id связанных с прививкой записей: факт на дату
// введения и настройки напоминания на следующую дату (null — записи нет).
type VaccinationLinkIDs struct {
	AdministeredEventID *string `json:"administered_event_id"`
	NextPlanID          *string `json:"next_plan_id"`
}

// VaccinationCreatedResponse — тело ответа 201 Created для POST
// /pet/{id}/vaccinations: id прививки и id созданных ею записей. Повтор
// запроса с тем же Idempotency-Key возвращает только id (связанные записи
// клиент получит из списка прививок).
type VaccinationCreatedResponse struct {
	ID string `json:"id"`
	VaccinationLinkIDs
}

// IDResponse — тело ответа 201 Created для POST /pet/{id}/vaccinations|diseases|vet-visits|allergies|medications
// (GetVaccinationIdResponseRequest / GetDiseaseIdResponseRequest / ... — все
// одной формы: {id}).
type IDResponse struct {
	ID string `json:"id"`
}
