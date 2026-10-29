package handlers

import (
	"database/sql"
	"errors"
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
// количество фактов (fact_count) и незавершённых напоминаний
// (reminder_count) по каждому дню диапазона по всем не мягко удалённым
// питомцам пользователя, без
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
	var (
		eventCounts, reminderCounts map[string]int
		eventsErr, remindersErr     error
	)
	runParallel(
		func() { eventCounts, eventsErr = database.CountEventsByUserIDGroupedByDay(userID, start, end, loc) },
		func() {
			reminderCounts, remindersErr = database.CountRemindersByUserIDGroupedByDay(userID, start, end, loc)
		},
	)
	if eventsErr != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка подсчёта событий")
		return
	}
	if remindersErr != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка подсчёта напоминаний")
		return
	}

	items := make([]models.ActivitiesCalendarItem, 0)
	currentDate := fromDate
	for !currentDate.After(toDate) {
		dateStr := currentDate.Format("2006-01-02")
		items = append(items, models.ActivitiesCalendarItem{
			Date:          dateStr,
			FactCount:     eventCounts[dateStr],
			ReminderCount: reminderCounts[dateStr],
		})
		currentDate = currentDate.AddDate(0, 0, 1)
	}

	writeJSON(w, http.StatusOK, models.ActivitiesCalendarResponse{Items: items})
}

// reminderCalendarItems превращает строки напоминаний календаря в элементы
// ответа (item_type=reminder): заметка — собственная заметка напоминания, а
// при её отсутствии — заметка настроек; files_count — число файлов настроек
// плюс собственных файлов напоминания.
func reminderCalendarItems(viewerID string, rows []database.ReminderCalendarRow) ([]models.ActivitiesDayItem, error) {
	planIDs := make([]uuid.UUID, len(rows))
	reminderIDs := make([]uuid.UUID, len(rows))
	authorRefs := make([]database.AuthorRef, len(rows))
	for i, row := range rows {
		planIDs[i] = row.PlanID
		reminderIDs[i] = row.ID
		authorRefs[i] = database.AuthorRef{AuthorID: row.AuthorID, PetIDs: petIDsOf(row.Pets)}
	}
	var (
		authors                       []*models.EventAuthor
		planFileCounts, ownFileCounts map[uuid.UUID]int
		authorsErr, planErr, ownErr   error
	)
	runParallel(
		func() { authors, authorsErr = database.ResolveAuthors(viewerID, authorRefs) },
		func() { planFileCounts, planErr = database.CountFilesForOwners(reminderPlanFileOwnerType, planIDs) },
		func() { ownFileCounts, ownErr = database.CountFilesForOwners(reminderFileOwnerType, reminderIDs) },
	)
	if err := errors.Join(authorsErr, planErr, ownErr); err != nil {
		return nil, err
	}

	items := make([]models.ActivitiesDayItem, 0, len(rows))
	for i, row := range rows {
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
			Pets:              database.ActivitiesDayPetRefs(row.Pets),
			Author:            authors[i],
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
	// Факты и напоминания не зависят друг от друга — читаются одновременно.
	var eventBranch, reminderBranch dayBranch
	runParallel(
		func() { eventBranch = dayEventItems(userID, start, end) },
		func() { reminderBranch = dayReminderItems(userID, start, end) },
	)
	for _, branch := range []dayBranch{eventBranch, reminderBranch} {
		if branch.err != nil {
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, branch.message)
			return
		}
	}
	items := append(eventBranch.items, reminderBranch.items...)

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

// dayBranch — результат чтения одной части ответа GET /activities/day: элементы
// либо ошибка с сообщением для клиента.
type dayBranch struct {
	items   []models.ActivitiesDayItem
	message string
	err     error
}

// dayEventItems читает факты дня с их файлами и авторами. Количество файлов и
// авторы зависят только от прочитанных событий и запрашиваются одновременно.
func dayEventItems(userID string, start, end time.Time) dayBranch {
	eventsFull, err := database.GetEventsByUserIDInRange(userID, start, end)
	if err != nil {
		return dayBranch{message: "Ошибка получения событий", err: err}
	}

	eventIDs := make([]uuid.UUID, len(eventsFull))
	for i, e := range eventsFull {
		eventIDs[i] = e.Event.ID
	}
	var (
		filesCounts map[uuid.UUID]int
		authors     []*models.EventAuthor
		filesErr    error
		authorsErr  error
	)
	runParallel(
		func() { filesCounts, filesErr = database.CountFilesForOwners(eventFileOwnerType, eventIDs) },
		func() { authors, authorsErr = eventAuthors(userID, eventsFull) },
	)
	if filesErr != nil {
		return dayBranch{message: "Ошибка получения количества файлов событий", err: filesErr}
	}
	if authorsErr != nil {
		return dayBranch{message: "Ошибка получения авторов событий", err: authorsErr}
	}

	items := make([]models.ActivitiesDayItem, 0, len(eventsFull))
	for i, e := range eventsFull {
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
			Pets:       database.ActivitiesDayPetRefs(e.Pets),
			Author:     authors[i],
		})
	}
	return dayBranch{items: items}
}

// dayReminderItems читает незавершённые напоминания дня.
func dayReminderItems(userID string, start, end time.Time) dayBranch {
	reminderRows, err := database.GetRemindersByUserIDInRange(userID, start, end)
	if err != nil {
		return dayBranch{message: "Ошибка получения напоминаний", err: err}
	}
	items, err := reminderCalendarItems(userID, reminderRows)
	if err != nil {
		return dayBranch{message: "Ошибка получения количества файлов напоминаний", err: err}
	}
	return dayBranch{items: items}
}
