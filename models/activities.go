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
// количество событий (по всем питомцам пользователя) за один календарный
// день диапазона, без содержимого самих событий.
type ActivitiesCalendarItem struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// ActivitiesCalendarResponse — тело ответа GET /activities/calendar.
type ActivitiesCalendarResponse struct {
	Items []ActivitiesCalendarItem `json:"items"`
}

// ActivitiesDayEventItem — один элемент items ответа GET /activities/day:
// событие с теми же полями, что ActivityEvent, плюс pet_id/pet_name — в
// отличие от GET /activities, здесь в одном списке смешаны события разных
// питомцев пользователя (см. «Просмотр календаря — Backend»).
type ActivitiesDayEventItem struct {
	ID         string          `json:"id"`
	Date       string          `json:"date"`
	Type       string          `json:"type"`
	Notes      *string         `json:"notes,omitempty"`
	Value      json.RawMessage `json:"value"`
	FilesCount int             `json:"files_count"`
	PetID      string          `json:"pet_id"`
	PetName    string          `json:"pet_name"`
}

// ActivitiesDayResponse — тело ответа GET /activities/day.
type ActivitiesDayResponse struct {
	Date  string                   `json:"date"`
	Items []ActivitiesDayEventItem `json:"items"`
}
