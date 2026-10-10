package models

import "encoding/json"

// ActivityEvent represents a single event in the activities response
type ActivityEvent struct {
	ID    string          `json:"id"`
	Date  string          `json:"date"`
	Type  string          `json:"type"`
	Notes *string         `json:"notes,omitempty"`
	Value json.RawMessage `json:"value"`
	// FilesCount — количество прикреплённых файлов события (0, если файлов
	// нет), см. «Файлы события — Backend».
	FilesCount int `json:"files_count"`
}

// ActivityDay represents events for a single day
type ActivityDay struct {
	Date   string          `json:"date"`
	Events []ActivityEvent `json:"events"`
}

// ActivitiesResponse is the main response structure for the /activities endpoint
type ActivitiesResponse struct {
	PetName string        `json:"pet_name"`
	Items   []ActivityDay `json:"items"`
}

// ActivitiesCalendarItem — один элемент items ответа GET /activities/calendar:
// количество элементов (фактов и незавершённых напоминаний по всем питомцам
// пользователя) за один календарный день диапазона, без их содержимого.
type ActivitiesCalendarItem struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
	// HasReminders — признак того, что в этот день есть хотя бы одно
	// незавершённое напоминание. Производное агрегатное значение дня, а не
	// поле конкретного элемента.
	HasReminders bool `json:"has_reminders"`
}

// ActivitiesCalendarResponse — тело ответа GET /activities/calendar.
type ActivitiesCalendarResponse struct {
	Items []ActivitiesCalendarItem `json:"items"`
}

// Значения ActivitiesDayItem.ItemType.
const (
	ActivityItemTypeEvent    = "event"
	ActivityItemTypeReminder = "reminder"
)

// ActivitiesDayItem — один элемент items ответа GET /activities/day: факт
// (item_type=event) либо незавершённое напоминание (item_type=reminder). В отличие от
// GET /activities, здесь в одном списке смешаны элементы разных питомцев
// пользователя, поэтому у элемента есть pet_id/pet_name.
type ActivitiesDayItem struct {
	ItemType string `json:"item_type"`
	ID       string `json:"id"`
	// PlanID — настройки напоминания; только у item_type=reminder.
	PlanID *string `json:"plan_id,omitempty"`
	// PlanSource, PlanSourceTitle, PlanUnclosedCount — данные настроек для
	// выбора «это / все» без отдельного запроса; только у item_type=reminder.
	PlanSource        *string         `json:"plan_source,omitempty"`
	PlanSourceTitle   *string         `json:"plan_source_title,omitempty"`
	PlanUnclosedCount *int            `json:"plan_unclosed_count,omitempty"`
	Date              string          `json:"date"`
	Type              string          `json:"type"`
	Notes             *string         `json:"notes,omitempty"`
	Value             json.RawMessage `json:"value"`
	FilesCount        int             `json:"files_count"`
	PetID             string          `json:"pet_id"`
	PetName           string          `json:"pet_name"`
}

// ActivitiesDayResponse — тело ответа GET /activities/day.
type ActivitiesDayResponse struct {
	Date  string              `json:"date"`
	Items []ActivitiesDayItem `json:"items"`
}
