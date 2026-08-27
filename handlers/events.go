package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"myauthservice/database"
	"myauthservice/models"
	"myauthservice/openapi"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	maxActivitiesRange = 366 * 24 * time.Hour
	maxEventFieldLen   = 500
)

// GetActivitiesHandler handles GET requests to /activities endpoint
func GetActivitiesHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Обработчик /activities GET вызван")

	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}

	fromStr := r.URL.Query().Get("from")
	if fromStr == "" {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Отсутствует обязательный параметр from")
		return
	}

	toStr := r.URL.Query().Get("to")
	if toStr == "" {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Отсутствует обязательный параметр to")
		return
	}

	petIDStr := r.URL.Query().Get("pet_id")
	if petIDStr == "" {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Отсутствует обязательный параметр pet_id")
		return
	}

	fromDate, err := time.Parse("2006-01-02", fromStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный формат даты from (ожидается YYYY-MM-DD)")
		return
	}

	toDate, err := time.Parse("2006-01-02", toStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный формат даты to (ожидается YYYY-MM-DD)")
		return
	}

	if toDate.Sub(fromDate) > maxActivitiesRange {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Диапазон дат не должен превышать 366 дней")
		return
	}

	petID, err := uuid.Parse(petIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный формат pet_id")
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	profileID, err := database.GetProfileIDByUserID(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения профиля пользователя")
		return
	}

	belongs, err := database.CheckPetBelongsToProfile(petID, profileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки принадлежности питомца")
		return
	}

	if !belongs {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Питомец не найден")
		return
	}

	petName, err := database.GetPetNameByID(petID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения имени питомца")
		return
	}

	eventsDB, err := database.GetEventsByPetIDAndDateRange(petID, fromDate, toDate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения событий")
		return
	}

	eventsByDay := make(map[string][]models.ActivityEvent)
	for _, eventDB := range eventsDB {
		dateStr := eventDB.Date.Format("2006-01-02")
		var notes *string
		if eventDB.Notes.Valid {
			notes = new(string)
			*notes = eventDB.Notes.String
		}
		activityEvent := models.ActivityEvent{
			ID:    eventDB.ID.String(),
			Date:  eventDB.Date.Format(time.RFC3339),
			Type:  eventDB.Type,
			Notes: notes,
			Value: eventDB.Value,
		}
		eventsByDay[dateStr] = append(eventsByDay[dateStr], activityEvent)
	}

	var items []models.ActivityDay
	currentDate := fromDate
	for currentDate.Before(toDate.AddDate(0, 0, 1)) {
		dateStr := currentDate.Format("2006-01-02")
		events, exists := eventsByDay[dateStr]
		if !exists {
			events = []models.ActivityEvent{}
		}
		items = append(items, models.ActivityDay{
			Date:   dateStr,
			Events: events,
		})
		currentDate = currentDate.AddDate(0, 0, 1)
	}

	response := models.ActivitiesResponse{
		PetName: petName,
		Items:   items,
	}

	writeJSON(w, http.StatusOK, response)
}

// PetEventItem represents a single event as returned by GET /pet/{id}/events (GetEventResponse).
type PetEventItem struct {
	ID    string  `json:"id"`
	Date  string  `json:"date"`
	Type  string  `json:"type"`
	Notes *string `json:"notes,omitempty"`
	Value string  `json:"value"`
}

// PetEventsResponse is the response body for GET /pet/{id}/events.
type PetEventsResponse struct {
	Items []PetEventItem `json:"items"`
}

// GET /pet/{id}/events возвращает плоский список всех событий питомца без
// фильтра по датам и без группировки по дням (в отличие от /activities) —
// задел на будущий сценарий вида «вся история питомца». На момент написания
// клиентом не используется.
func GetPetEventsHandler(w http.ResponseWriter, r *http.Request, petID uuid.UUID) {
	log.Println("Обработчик /pet/{id}/events GET вызван")

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	profileID, err := database.GetProfileIDByUserID(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения профиля")
		return
	}

	belongs, err := database.CheckPetBelongsToProfile(petID, profileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки принадлежности питомца")
		return
	}
	if !belongs {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Питомец не найден")
		return
	}

	eventsDB, err := database.GetEventsByPetID(petID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения событий")
		return
	}

	items := make([]PetEventItem, 0, len(eventsDB))
	for _, eventDB := range eventsDB {
		var notes *string
		if eventDB.Notes.Valid {
			notes = &eventDB.Notes.String
		}
		items = append(items, PetEventItem{
			ID:    eventDB.ID.String(),
			Date:  eventDB.Date.Format("2006-01-02"),
			Type:  eventDB.Type,
			Notes: notes,
			Value: eventDB.Value,
		})
	}

	writeJSON(w, http.StatusOK, PetEventsResponse{Items: items})
}

func EventIDResonseHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		GetEventHandler(w, r)
	case http.MethodPatch:
		UpdateEventHandler(w, r)
	case http.MethodDelete:
		DeleteEventHandler(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
	}
}

func parseEventIDFromPath(r *http.Request) (uuid.UUID, error) {
	pathParts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pathParts) < 2 || pathParts[0] != "events" {
		return uuid.Nil, errors.New("некорректный путь")
	}
	return uuid.Parse(pathParts[1])
}

// DELETE /events/{id}
func DeleteEventHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Обработчик /events/{id} DELETE вызван")

	eventID, err := parseEventIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный ID события")
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	profileID, err := database.GetProfileIDByUserID(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения профиля")
		return
	}

	_, petID, _, err := database.GetEventByID(eventID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Событие не найдено")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения события")
		return
	}

	belongs, err := database.CheckPetBelongsToProfile(petID, profileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки принадлежности питомца")
		return
	}

	if !belongs {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Событие не найдено")
		return
	}

	if err := database.DeleteEvent(eventID); err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Событие не найдено")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка удаления события")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GET /events/{id}
func GetEventHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Обработчик /events/{id} GET вызван")

	eventID, err := parseEventIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный ID события")
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	profileID, err := database.GetProfileIDByUserID(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения профиля")
		return
	}

	eventDB, petID, petName, err := database.GetEventByID(eventID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Событие не найдено")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения события")
		return
	}

	belongs, err := database.CheckPetBelongsToProfile(petID, profileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки прав доступа")
		return
	}

	if !belongs {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Событие не найдено")
		return
	}

	var notes *string
	if eventDB.Notes.Valid {
		notes = &eventDB.Notes.String
	}

	response := models.EventResponse{
		ID:      eventDB.ID.String(),
		Date:    eventDB.Date.Format(time.RFC3339),
		Type:    eventDB.Type,
		Value:   eventDB.Value,
		Notes:   notes,
		PetID:   petID.String(),
		PetName: petName,
	}

	writeJSON(w, http.StatusOK, response)
}

// POST /events
func CreateEventHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Обработчик /events POST вызван")
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	profileID, err := database.GetProfileIDByUserID(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения профиля")
		return
	}

	var req models.CreateEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}

	if req.PetID == "" || req.Date == "" || req.Type == "" || req.Value == "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Обязательные поля не заполнены")
		return
	}

	if len(req.Value) > maxEventFieldLen || (req.Notes != nil && len(*req.Notes) > maxEventFieldLen) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле notes/value не должно превышать 500 символов")
		return
	}

	if !models.IsValidEventType(req.Type) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректное значение type")
		return
	}

	if _, err := time.Parse(time.RFC3339, req.Date); err != nil {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный формат даты")
		return
	}

	petID, err := uuid.Parse(req.PetID)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный ID питомца")
		return
	}

	petDB, err := database.GetPetIdDBByIDAndProfileID(petID, profileID)
	if err != nil {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Питомец "+req.PetID+" не найден")
		return
	}

	if petDB.DeletedAt.Valid {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Невозможно создать запись о событии, так как питомец удален")
		return
	}

	eventID, err := database.InsertEvent(petID, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка при создании события")
		return
	}

	createdEvent, petIDOfEvent, petName, err := database.GetEventByID(eventID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Событие создано, но не удалось получить его данные")
		return
	}

	var notes *string
	if createdEvent.Notes.Valid {
		notes = &createdEvent.Notes.String
	}

	writeJSON(w, http.StatusCreated, models.EventResponse{
		ID:      createdEvent.ID.String(),
		Date:    createdEvent.Date.Format(time.RFC3339),
		Type:    createdEvent.Type,
		Value:   createdEvent.Value,
		Notes:   notes,
		PetID:   petIDOfEvent.String(),
		PetName: petName,
	})
}

// PATCH /events/{id}
func UpdateEventHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Обработчик /events/{id} PATCH вызван")

	eventID, err := parseEventIDFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректный ID события")
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	profileID, err := database.GetProfileIDByUserID(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения профиля")
		return
	}

	eventDB, err := database.GetEventByIDForUpdate(eventID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Данное мероприятие не существует или не найдено")
			return
		}
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения события")
		return
	}

	var req models.UpdateEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Некорректное тело запроса")
		return
	}

	if req.PetID == "" {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Обязательное поле pet_id не заполнено")
		return
	}

	if req.Date == nil && req.Type == nil && req.Notes == nil && req.Value == nil {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Необходимо указать хотя бы одно поле для обновления")
		return
	}

	if (req.Value != nil && len(*req.Value) > maxEventFieldLen) || (req.Notes != nil && len(*req.Notes) > maxEventFieldLen) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Поле notes/value не должно превышать 500 символов")
		return
	}

	reqPetID, err := uuid.Parse(req.PetID)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный ID питомца")
		return
	}

	if eventDB.PetID != reqPetID {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Данное мероприятие не существует или не найдено")
		return
	}

	belongs, err := database.CheckPetBelongsToProfile(reqPetID, profileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка проверки прав доступа")
		return
	}

	if !belongs {
		writeError(w, http.StatusNotFound, openapi.NOTFOUND, "Питомец "+req.PetID+" не найден")
		return
	}

	petDB, err := database.GetPetById(reqPetID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения питомца")
		return
	}

	if petDB.DeletedAt.Valid {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Невозможно редактировать запись о событии, так как питомец удален")
		return
	}

	if req.Type != nil && !models.IsValidEventType(*req.Type) {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректное значение type")
		return
	}

	var dateTime *time.Time
	if req.Date != nil {
		parsedDate, err := time.Parse(time.RFC3339, *req.Date)
		if err != nil {
			writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный формат даты")
			return
		}
		dateTime = &parsedDate
	}

	if err := database.UpdateEvent(eventID, req, dateTime, req.Type, req.Notes, req.Value); err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка при обновлении события")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
