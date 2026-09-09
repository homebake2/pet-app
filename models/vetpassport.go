// Package models: типы фичи «Ведпаспорт» (медкарта питомца) — 5 pet_id-scoped
// сущностей (Vaccination, Disease, VetVisit, Allergy, Medication), каждая с
// soft-delete (deleted_at) и до 10 файлов через generic-механизм файлов
// сущностей (см. handlers/files.go). Контракт — components.schemas
// GetVaccination*/GetDisease*/GetVetVisit*/GetAllergy*/GetMedication* в
// open-api/spec.json.
package models

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// Лимиты длины текстовых полей — см. соответствующие схемы spec.json.
const (
	VaccinationNameMaxLen        = 100
	DiseaseNameMaxLen            = 100
	DiseaseNoteMaxLen            = 1000
	VetVisitReasonMaxLen         = 200
	VetVisitClinicMaxLen         = 200
	VetVisitNoteMaxLen           = 1000
	AllergyAllergenMaxLen        = 100
	AllergyReactionMaxLen        = 200
	AllergyNoteMaxLen            = 1000
	MedicationNameMaxLen         = 100
	MedicationDosageMaxLen       = 100
	MedicationNoteMaxLen         = 1000
	MedicationMinPeriodicityDays = 1
	MedicationMaxPeriodicityDays = 365
	MedicationMinRepeatCount     = 1
	MedicationMaxRepeatCount     = 14
)

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
	NextEventID         uuid.NullUUID
	DeletedAt           sql.NullTime
}

// CreateVaccinationRequest — тело запроса POST /pet/{id}/vaccinations.
type CreateVaccinationRequest struct {
	Name                   string  `json:"name"`
	AdministeredDate       string  `json:"administered_date"`
	NextDate               *string `json:"next_date,omitempty"`
	AddEventOnAdministered *bool   `json:"add_event_on_administered,omitempty"`
	AddEventOnNext         *bool   `json:"add_event_on_next,omitempty"`
	// EventTime — время суток для создаваемых событий; не хранится в самой
	// прививке (см. описание GetVaccinationRequest.event_time в spec.json).
	EventTime *string `json:"event_time,omitempty"`
}

// UpdateVaccinationRequest — тело запроса PATCH /vaccinations/{id}. Nullable
// строковые поля (next_date, event_time) очищаются передачей пустой строки
// "" — тем же соглашением, что и UpdateEventRequest.Notes.
type UpdateVaccinationRequest struct {
	Name                   *string `json:"name,omitempty"`
	AdministeredDate       *string `json:"administered_date,omitempty"`
	NextDate               *string `json:"next_date,omitempty"`
	AddEventOnAdministered *bool   `json:"add_event_on_administered,omitempty"`
	AddEventOnNext         *bool   `json:"add_event_on_next,omitempty"`
	EventTime              *string `json:"event_time,omitempty"`
}

type VaccinationResponse struct {
	ID                  string  `json:"id"`
	PetID               string  `json:"pet_id"`
	Name                string  `json:"name"`
	AdministeredDate    string  `json:"administered_date"`
	NextDate            *string `json:"next_date,omitempty"`
	AdministeredEventID *string `json:"administered_event_id,omitempty"`
	NextEventID         *string `json:"next_event_id,omitempty"`
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

type MedicationDB struct {
	ID              uuid.UUID
	PetID           uuid.UUID
	Name            string
	Dosage          string
	PeriodicityDays int
	StartDate       time.Time
	RepeatCount     int
	EventTime       sql.NullString
	// EventIDs хранится как text[] (строковые UUID) в БД, см. миграцию
	// 000017_vetpassport_entities — проще читать/писать через lib/pq.
	EventIDs  []string
	Note      sql.NullString
	DeletedAt sql.NullTime
}

type CreateMedicationRequest struct {
	Name            string  `json:"name"`
	Dosage          string  `json:"dosage"`
	PeriodicityDays int     `json:"periodicity_days"`
	StartDate       string  `json:"start_date"`
	RepeatCount     int     `json:"repeat_count"`
	EventTime       *string `json:"event_time,omitempty"`
	Note            *string `json:"note,omitempty"`
}

type UpdateMedicationRequest struct {
	Name            *string `json:"name,omitempty"`
	Dosage          *string `json:"dosage,omitempty"`
	PeriodicityDays *int    `json:"periodicity_days,omitempty"`
	StartDate       *string `json:"start_date,omitempty"`
	RepeatCount     *int    `json:"repeat_count,omitempty"`
	EventTime       *string `json:"event_time,omitempty"`
	Note            *string `json:"note,omitempty"`
}

type MedicationResponse struct {
	ID              string   `json:"id"`
	PetID           string   `json:"pet_id"`
	Name            string   `json:"name"`
	Dosage          string   `json:"dosage"`
	PeriodicityDays int      `json:"periodicity_days"`
	StartDate       string   `json:"start_date"`
	RepeatCount     int      `json:"repeat_count"`
	EventTime       *string  `json:"event_time,omitempty"`
	EventIDs        []string `json:"event_ids"`
	Note            *string  `json:"note,omitempty"`
	FilesCount      int      `json:"files_count"`
}

type MedicationListResponse struct {
	Items []MedicationResponse `json:"items"`
}

// IDResponse — тело ответа 201 Created для POST /pet/{id}/vaccinations|diseases|vet-visits|allergies|medications
// (GetVaccinationIdResponseRequest / GetDiseaseIdResponseRequest / ... — все
// одной формы: {id}).
type IDResponse struct {
	ID string `json:"id"`
}
