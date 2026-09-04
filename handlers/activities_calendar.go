package handlers

import (
	"myauthservice/database"
	"myauthservice/models"
	"myauthservice/openapi"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// parseSingleDateParam разбирает обязательный query-параметр date
// (YYYY-MM-DD) — используется GET /activities/day. Трактовка та же, что и у
// границ from/to GET /activities: календарная дата в UTC (см. "Просмотр
// календаря — Backend", раздел "Граница дня").
func parseSingleDateParam(w http.ResponseWriter, r *http.Request) (date time.Time, ok bool) {
	dateStr := r.URL.Query().Get("date")
	if dateStr == "" {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Отсутствует обязательный параметр date")
		return time.Time{}, false
	}

	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "Некорректный формат даты date (ожидается YYYY-MM-DD)")
		return time.Time{}, false
	}

	return date, true
}

// GetActivitiesCalendarHandler обрабатывает GET /activities/calendar —
// количество событий по каждому дню диапазона по всем не мягко удалённым
// питомцам пользователя, без содержимого самих событий (см. "Просмотр
// календаря — Backend", раздел A). Параметр pet_id не принимается и не
// возвращает 404 — пользователь без питомцев/событий получает 200 с
// count: 0 по всем дням.
func GetActivitiesCalendarHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}

	fromDate, toDate, ok := parseEventDateRange(w, r)
	if !ok {
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	counts, err := database.CountEventsByUserIDGroupedByDay(userID, fromDate, toDate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка подсчёта событий")
		return
	}

	items := make([]models.ActivitiesCalendarItem, 0)
	currentDate := fromDate
	for !currentDate.After(toDate) {
		dateStr := currentDate.Format("2006-01-02")
		items = append(items, models.ActivitiesCalendarItem{
			Date:  dateStr,
			Count: counts[dateStr],
		})
		currentDate = currentDate.AddDate(0, 0, 1)
	}

	writeJSON(w, http.StatusOK, models.ActivitiesCalendarResponse{Items: items})
}

// GetActivitiesDayHandler обрабатывает GET /activities/day — все события всех
// не мягко удалённых питомцев пользователя за один календарный день (UTC),
// отсортированные по date_time по возрастанию (см. "Просмотр календаря —
// Backend", раздел B). Параметр pet_id не принимается и не возвращает 404.
func GetActivitiesDayHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}

	date, ok := parseSingleDateParam(w, r)
	if !ok {
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	eventsWithPet, err := database.GetEventsByUserIDAndDate(userID, date)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения событий")
		return
	}

	eventIDs := make([]uuid.UUID, len(eventsWithPet))
	for i, e := range eventsWithPet {
		eventIDs[i] = e.Event.ID
	}
	filesCounts, err := database.CountFilesForOwners(eventFileOwnerType, eventIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения количества файлов событий")
		return
	}

	items := make([]models.ActivitiesDayEventItem, 0, len(eventsWithPet))
	for _, e := range eventsWithPet {
		var notes *string
		if e.Event.Notes.Valid {
			notes = &e.Event.Notes.String
		}
		items = append(items, models.ActivitiesDayEventItem{
			ID:         e.Event.ID.String(),
			Date:       e.Event.Date.UTC().Format(time.RFC3339),
			Type:       e.Event.Type,
			Notes:      notes,
			Value:      e.Event.Value,
			FilesCount: filesCounts[e.Event.ID],
			PetID:      e.Event.PetID.String(),
			PetName:    e.PetName,
		})
	}

	writeJSON(w, http.StatusOK, models.ActivitiesDayResponse{
		Date:  date.Format("2006-01-02"),
		Items: items,
	})
}
