package handlers

import (
	"database/sql"
	"myauthservice/database"
	"myauthservice/models"
	"myauthservice/openapi"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
)

// parseSingleDateParam разбирает обязательный query-параметр date
// (YYYY-MM-DD) — используется GET /activities/day. Трактовка та же, что и у
// границ from/to GET /activities: календарная дата в часовом поясе клиента
// из параметра tz (см. parseTimeZoneParam и "Просмотр календаря — Backend").
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
// количество элементов (фактов и незавершённых напоминаний) по каждому дню
// диапазона по всем не мягко удалённым питомцам пользователя и признак
// has_reminders (в этот день есть незавершённое напоминание), без
// содержимого самих элементов (см. "Просмотр календаря — Backend", раздел
// A). Параметр pet_id не принимается и не возвращает 404 — пользователь без
// питомцев/событий получает 200 с count: 0 по всем дням. День элемента — его
// календарный день в поясе tz.
func GetActivitiesCalendarHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}

	fromDate, toDate, ok := parseEventDateRange(w, r)
	if !ok {
		return
	}

	loc, ok := parseTimeZoneParam(w, r)
	if !ok {
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	start, end := localDaysBounds(fromDate, toDate, loc)
	eventCounts, err := database.CountEventsByUserIDGroupedByDay(userID, start, end, loc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка подсчёта событий")
		return
	}
	reminderCounts, err := database.CountRemindersByUserIDGroupedByDay(userID, start, end, loc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка подсчёта напоминаний")
		return
	}

	items := make([]models.ActivitiesCalendarItem, 0)
	currentDate := fromDate
	for !currentDate.After(toDate) {
		dateStr := currentDate.Format("2006-01-02")
		items = append(items, models.ActivitiesCalendarItem{
			Date:         dateStr,
			Count:        eventCounts[dateStr] + reminderCounts[dateStr],
			HasReminders: reminderCounts[dateStr] > 0,
		})
		currentDate = currentDate.AddDate(0, 0, 1)
	}

	writeJSON(w, http.StatusOK, models.ActivitiesCalendarResponse{Items: items})
}

// reminderCalendarItems превращает строки напоминаний календаря в элементы
// ответа (item_type=reminder): заметка — собственная заметка напоминания, а
// при её отсутствии — заметка настроек; files_count — число файлов настроек
// плюс собственных файлов напоминания.
func reminderCalendarItems(rows []database.ReminderCalendarRow) ([]models.ActivitiesDayItem, error) {
	planIDs := make([]uuid.UUID, len(rows))
	reminderIDs := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		planIDs[i] = row.PlanID
		reminderIDs[i] = row.ID
	}
	planFileCounts, err := database.CountFilesForOwners(reminderPlanFileOwnerType, planIDs)
	if err != nil {
		return nil, err
	}
	ownFileCounts, err := database.CountFilesForOwners(reminderFileOwnerType, reminderIDs)
	if err != nil {
		return nil, err
	}

	items := make([]models.ActivitiesDayItem, 0, len(rows))
	for _, row := range rows {
		planID := row.PlanID.String()
		planSource := row.PlanSource
		unclosed := row.PlanUnclosedCount
		var sourceTitle *string
		if row.PlanSourceTitle.Valid {
			title := row.PlanSourceTitle.String
			sourceTitle = &title
		}
		items = append(items, models.ActivitiesDayItem{
			ItemType:          models.ActivityItemTypeReminder,
			ID:                row.ID.String(),
			PlanID:            &planID,
			PlanSource:        &planSource,
			PlanSourceTitle:   sourceTitle,
			PlanUnclosedCount: &unclosed,
			Date:              row.RemindAt.UTC().Format(time.RFC3339),
			Type:              row.Type,
			Notes:             effectiveReminderNotes(row.ReminderNotes, row.PlanNotes),
			Value:             row.Value,
			FilesCount:        planFileCounts[row.PlanID] + ownFileCounts[row.ID],
			Pets:              database.EventPetRefs(row.Pets),
		})
	}
	return items, nil
}

// effectiveReminderNotes возвращает заметку напоминания: собственную, если
// она непустая, иначе заметку настроек; nil, если нет ни той, ни другой.
func effectiveReminderNotes(reminderNotes, planNotes sql.NullString) *string {
	if reminderNotes.Valid && reminderNotes.String != "" {
		notes := reminderNotes.String
		return &notes
	}
	if planNotes.Valid {
		notes := planNotes.String
		return &notes
	}
	return nil
}

// GetActivitiesDayHandler обрабатывает GET /activities/day — все факты и все
// незавершённые напоминания всех не мягко удалённых питомцев пользователя за
// один календарный день в часовом поясе tz, отсортированные по моменту (для
// напоминания — remind_at) по возрастанию, при равенстве — по id (см.
// "Просмотр календаря — Backend", раздел B). Параметр pet_id не принимается и
// не возвращает 404.
func GetActivitiesDayHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}

	date, ok := parseSingleDateParam(w, r)
	if !ok {
		return
	}

	loc, ok := parseTimeZoneParam(w, r)
	if !ok {
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	start, end := localDaysBounds(date, date, loc)
	eventsFull, err := database.GetEventsByUserIDInRange(userID, start, end)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения событий")
		return
	}

	eventIDs := make([]uuid.UUID, len(eventsFull))
	for i, e := range eventsFull {
		eventIDs[i] = e.Event.ID
	}
	filesCounts, err := database.CountFilesForOwners(eventFileOwnerType, eventIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения количества файлов событий")
		return
	}

	items := make([]models.ActivitiesDayItem, 0, len(eventsFull))
	for _, e := range eventsFull {
		var notes *string
		if e.Event.Notes.Valid {
			notes = &e.Event.Notes.String
		}
		items = append(items, models.ActivitiesDayItem{
			ItemType:   models.ActivityItemTypeEvent,
			ID:         e.Event.ID.String(),
			Date:       e.Event.Date.UTC().Format(time.RFC3339),
			Type:       e.Event.Type,
			Notes:      notes,
			Value:      e.Event.Value,
			FilesCount: filesCounts[e.Event.ID],
			Pets:       database.EventPetRefs(e.Pets),
		})
	}

	reminderRows, err := database.GetRemindersByUserIDInRange(userID, start, end)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения напоминаний")
		return
	}
	reminderItems, err := reminderCalendarItems(reminderRows)
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения количества файлов напоминаний")
		return
	}
	items = append(items, reminderItems...)

	// Моменты у фактов и напоминаний отдаются одним форматом RFC3339 (UTC,
	// секундная точность), поэтому сортировка по строке эквивалентна
	// сортировке по времени.
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Date != items[j].Date {
			return items[i].Date < items[j].Date
		}
		return items[i].ID < items[j].ID
	})

	writeJSON(w, http.StatusOK, models.ActivitiesDayResponse{
		Date:  date.Format("2006-01-02"),
		Items: items,
	})
}
