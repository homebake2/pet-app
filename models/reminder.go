package models

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Значения reminder_plan.source (ReminderSourceEnum).
const (
	ReminderSourceManual      = "manual"
	ReminderSourceMedication  = "medication"
	ReminderSourceVaccination = "vaccination"
)

// Значения reminder_plan.frequency_type (ReminderFrequencyTypeEnum).
const (
	ReminderFrequencyOnce         = "once"
	ReminderFrequencyDaily        = "daily"
	ReminderFrequencySpecificDays = "specific_days"
	ReminderFrequencyEveryNDays   = "every_n_days"
)

// Значения reminder.close_reason.
const (
	ReminderCloseDone     = "done"
	ReminderCloseSkipped  = "skipped"
	ReminderCloseDeleted  = "deleted"
	ReminderCloseReplaced = "replaced"
)

const (
	// ReminderMaxMomentsPerOperation — потолок числа напоминаний, которые
	// создаёт одна операция расчёта расписания (создание, пересоздание).
	ReminderMaxMomentsPerOperation = 60
	// ReminderMaxTimesCount — максимум времён суток в расписании.
	ReminderMaxTimesCount = 4
	// ReminderScheduleNoMatchLookaheadDays — защита от бесконечного
	// перебора: если без end_date в пределах этого числа дней от start_date
	// не нашлось ни одной подходящей даты, перебор прекращается.
	ReminderScheduleNoMatchLookaheadDays = 730
	// ReminderNotesMaxLen — максимальная длина notes настроек и напоминания.
	ReminderNotesMaxLen = 500
	// ReminderFilesMaxCount — суммарный лимит файлов, видимых у одного
	// напоминания (файлы настроек + собственные файлы напоминания).
	ReminderFilesMaxCount = 10
	// ReminderUpcomingMaxLimit/DefaultLimit — limit GET /reminders/upcoming.
	ReminderUpcomingMaxLimit     = 64
	ReminderUpcomingDefaultLimit = 64
	// PetReminderUpcomingMaxLimit/DefaultLimit — limit
	// GET /pet/{id}/reminders/upcoming.
	PetReminderUpcomingMaxLimit     = 10
	PetReminderUpcomingDefaultLimit = 3
)

var allowedReminderFrequencyTypes = map[string]bool{
	ReminderFrequencyOnce:         true,
	ReminderFrequencyDaily:        true,
	ReminderFrequencySpecificDays: true,
	ReminderFrequencyEveryNDays:   true,
}

// IsValidReminderFrequencyType проверяет значение ReminderFrequencyTypeEnum.
func IsValidReminderFrequencyType(v string) bool {
	return allowedReminderFrequencyTypes[v]
}

// ReminderPlanDB — строка reminder_plan (настройки напоминания).
type ReminderPlanDB struct {
	ID            uuid.UUID
	PetID         uuid.UUID
	Source        string
	SourceID      uuid.NullUUID
	Type          string
	Value         json.RawMessage
	Notes         sql.NullString
	FrequencyType string
	// Weekdays — nil означает NULL в БД.
	Weekdays     []int
	IntervalDays sql.NullInt64
	// Times — значения 'HH:mm' (либо 'HH:mm:ss', если секунды не нулевые).
	Times     []string
	StartDate time.Time
	EndDate   sql.NullTime
	TZ        string
	CreatedAt time.Time
}

// ReminderDB — строка reminder (один момент расписания).
type ReminderDB struct {
	ID          uuid.UUID
	PlanID      uuid.UUID
	RemindAt    time.Time
	Notes       sql.NullString
	ClosedAt    sql.NullTime
	CloseReason sql.NullString
	FactEventID uuid.NullUUID
}

// ReminderMoment — рассчитанный момент расписания, из которого
// материализуется строка reminder.
type ReminderMoment struct {
	RemindAt time.Time
	// Notes — собственная заметка напоминания (например, доза по времени
	// приёма лекарства); nil — заметка настроек.
	Notes *string
}

// ReminderPlanRequest — тело запроса POST /reminder-plans и поле plan
// запроса POST /reminders/{id}/detach.
type ReminderPlanRequest struct {
	ID            string          `json:"id"`
	PetID         string          `json:"pet_id"`
	Type          string          `json:"type"`
	Value         json.RawMessage `json:"value"`
	Notes         *string         `json:"notes,omitempty"`
	FrequencyType string          `json:"frequency_type"`
	Weekdays      []int           `json:"weekdays,omitempty"`
	IntervalDays  *int            `json:"interval_days,omitempty"`
	Times         []string        `json:"times"`
	StartDate     string          `json:"start_date"`
	EndDate       *string         `json:"end_date,omitempty"`
}

// UpdateReminderPlanRequest — тело запроса PATCH /reminder-plans/{id}.
// EndDate различает «поле не передано» и «передан null» (без даты
// окончания): для проверки, что поля расписания переданы целиком.
type UpdateReminderPlanRequest struct {
	Type          *string               `json:"type,omitempty"`
	Value         *json.RawMessage      `json:"value,omitempty"`
	Notes         *string               `json:"notes,omitempty"`
	FrequencyType *string               `json:"frequency_type,omitempty"`
	Weekdays      *[]int                `json:"weekdays,omitempty"`
	IntervalDays  *int                  `json:"interval_days,omitempty"`
	Times         *[]string             `json:"times,omitempty"`
	StartDate     *string               `json:"start_date,omitempty"`
	EndDate       OptionalField[string] `json:"end_date,omitzero"`
}

// ReminderRef — элемент reminders в ReminderPlanResponse.
type ReminderRef struct {
	ID       string `json:"id"`
	RemindAt string `json:"remind_at"`
}

// ReminderPlanResponse — тело ответа ReminderPlanResponse.
type ReminderPlanResponse struct {
	ID            string          `json:"id"`
	PetID         string          `json:"pet_id"`
	PetName       string          `json:"pet_name"`
	Type          string          `json:"type"`
	Value         json.RawMessage `json:"value"`
	Notes         *string         `json:"notes"`
	FrequencyType string          `json:"frequency_type"`
	Weekdays      *[]int          `json:"weekdays"`
	IntervalDays  *int            `json:"interval_days"`
	Times         []string        `json:"times"`
	StartDate     string          `json:"start_date"`
	EndDate       *string         `json:"end_date"`
	Source        string          `json:"source"`
	SourceID      *string         `json:"source_id"`
	SourceTitle   *string         `json:"source_title"`
	Reminders     []ReminderRef   `json:"reminders"`
	Files         []EventFileItem `json:"files"`
	// FutureRemindersWithFilesCount — число незавершённых напоминаний с
	// remind_at > now(), у которых есть собственные файлы.
	FutureRemindersWithFilesCount int `json:"future_reminders_with_files_count"`
}

// ReminderResponse — тело ответа GET /reminders/{id}.
type ReminderResponse struct {
	ID                string          `json:"id"`
	PlanID            string          `json:"plan_id"`
	RemindAt          string          `json:"remind_at"`
	Type              string          `json:"type"`
	Value             json.RawMessage `json:"value"`
	Notes             *string         `json:"notes"`
	PetID             string          `json:"pet_id"`
	PetName           string          `json:"pet_name"`
	PlanSource        string          `json:"plan_source"`
	PlanSourceTitle   *string         `json:"plan_source_title"`
	PlanFiles         []EventFileItem `json:"plan_files"`
	Files             []EventFileItem `json:"files"`
	PlanUnclosedCount int             `json:"plan_unclosed_count"`
}

// CompleteReminderRequest — тело запроса POST /reminders/{id}/complete.
type CompleteReminderRequest struct {
	Done *bool `json:"done"`
}

// CompleteReminderResponse — тело ответа POST /reminders/{id}/complete.
type CompleteReminderResponse struct {
	FactEventID *string `json:"fact_event_id"`
}

// DetachReminderRequest — тело запроса POST /reminders/{id}/detach: ровно
// одно из event и plan.
type DetachReminderRequest struct {
	Event *CreateEventRequest  `json:"event,omitempty"`
	Plan  *ReminderPlanRequest `json:"plan,omitempty"`
}

// DetachReminderResponse — тело ответа POST /reminders/{id}/detach.
type DetachReminderResponse struct {
	Event *EventResponse        `json:"event,omitempty"`
	Plan  *ReminderPlanResponse `json:"plan,omitempty"`
}

// UpcomingReminderItem — элемент items ответа GET /reminders/upcoming.
type UpcomingReminderItem struct {
	ID       string `json:"id"`
	PlanID   string `json:"plan_id"`
	RemindAt string `json:"remind_at"`
	PetID    string `json:"pet_id"`
	PetName  string `json:"pet_name"`
	Type     string `json:"type"`
}

// UpcomingRemindersResponse — тело ответа GET /reminders/upcoming.
type UpcomingRemindersResponse struct {
	Items []UpcomingReminderItem `json:"items"`
}

// PetUpcomingReminderItem — элемент items ответа
// GET /pet/{id}/reminders/upcoming.
type PetUpcomingReminderItem struct {
	ID       string `json:"id"`
	PlanID   string `json:"plan_id"`
	RemindAt string `json:"remind_at"`
	Type     string `json:"type"`
}

// PetUpcomingRemindersResponse — тело ответа
// GET /pet/{id}/reminders/upcoming.
type PetUpcomingRemindersResponse struct {
	Items []PetUpcomingReminderItem `json:"items"`
}
