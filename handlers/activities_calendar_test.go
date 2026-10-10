package handlers

import (
	"database/sql/driver"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// calendarEventsQuery — выборка моментов событий для GET /activities/calendar
// (группировка по дню клиента выполняется в Go).
const calendarEventsQuery = `SELECT e\.date_time\s+FROM event e\s+WHERE e\.user_id = \$1\s+AND e\.deleted_at IS NULL\s+AND e\.date_time >= \$2\s+AND e\.date_time < \$3\s+AND EXISTS \(`

// timeArgMatcher сравнивает аргумент запроса с ожидаемым моментом времени
// через time.Equal — представление location у значения не важно.
type timeArgMatcher struct{ want time.Time }

func (m timeArgMatcher) Match(v driver.Value) bool {
	got, ok := v.(time.Time)
	return ok && got.Equal(m.want)
}

func timeArg(want time.Time) sqlmock.Argument { return timeArgMatcher{want: want} }

// calendarRemindersQuery — выборка моментов незавершённых напоминаний для
// GET /activities/calendar.
const calendarRemindersQuery = `SELECT r\.remind_at\s+FROM reminder r\s+JOIN reminder_plan p ON p\.id = r\.plan_id\s+WHERE p\.user_id = \$1 AND r\.closed_at IS NULL AND EXISTS \(`

// reminderCalendarQuery — выборка незавершённых напоминаний с данными
// настроек (GET /activities/day).
const reminderCalendarQuery = `SELECT r\.id, r\.plan_id, r\.remind_at, r\.notes, p\.notes, p\.type, p\.value,\s+p\.source, COALESCE\(med\.name, vac\.name\),\s+\(SELECT COUNT\(\*\) FROM reminder o WHERE o\.plan_id = p\.id AND o\.closed_at IS NULL\)\s+FROM reminder r`

var reminderCalendarColumns = []string{"id", "plan_id", "remind_at", "notes", "notes", "type", "value", "source", "source_title", "unclosed"}

type calendarResponse struct {
	Items []struct {
		Date         string `json:"date"`
		Count        int    `json:"count"`
		HasReminders bool   `json:"has_reminders"`
	} `json:"items"`
}

// --- GET /activities/calendar ---

func TestGetActivitiesCalendarHandler_MethodNotAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	GetActivitiesCalendarHandler(w, doRequest(http.MethodPost, "/activities/calendar", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestGetActivitiesCalendarHandler_MissingParams(t *testing.T) {
	w := httptest.NewRecorder()
	GetActivitiesCalendarHandler(w, doRequest(http.MethodGet, "/activities/calendar", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetActivitiesCalendarHandler_FromAfterTo(t *testing.T) {
	w := httptest.NewRecorder()
	GetActivitiesCalendarHandler(w, doRequest(http.MethodGet, "/activities/calendar?from=2024-01-10&to=2024-01-01&tz=UTC", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetActivitiesCalendarHandler_RangeTooLong(t *testing.T) {
	w := httptest.NewRecorder()
	GetActivitiesCalendarHandler(w, doRequest(http.MethodGet, "/activities/calendar?from=2023-01-01&to=2024-06-01&tz=UTC", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetActivitiesCalendarHandler_Unauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	GetActivitiesCalendarHandler(w, doRequest(http.MethodGet, "/activities/calendar?from=2024-01-01&to=2024-01-02&tz=UTC", nil))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// tz обязателен: без него 400 VALIDATION_ERROR, значения по умолчанию нет.
func TestGetActivitiesCalendarHandler_MissingTimeZone(t *testing.T) {
	w := httptest.NewRecorder()
	GetActivitiesCalendarHandler(w, doRequest(http.MethodGet, "/activities/calendar?from=2024-01-01&to=2024-01-02", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "VALIDATION_ERROR")
}

func TestGetActivitiesCalendarHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(calendarEventsQuery).
		WithArgs(testUserID,
			timeArg(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
			timeArg(time.Date(2024, 1, 4, 0, 0, 0, 0, time.UTC))).
		WillReturnRows(sqlmock.NewRows([]string{"date_time"}).
			AddRow(time.Date(2024, 1, 2, 8, 0, 0, 0, time.UTC)).
			AddRow(time.Date(2024, 1, 2, 23, 59, 0, 0, time.UTC)).
			AddRow(time.Date(2024, 1, 3, 10, 0, 0, 0, time.UTC)))
	mock.ExpectQuery(calendarRemindersQuery).
		WithArgs(testUserID,
			timeArg(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
			timeArg(time.Date(2024, 1, 4, 0, 0, 0, 0, time.UTC))).
		WillReturnRows(sqlmock.NewRows([]string{"remind_at"}).
			AddRow(time.Date(2024, 1, 2, 9, 0, 0, 0, time.UTC)).
			AddRow(time.Date(2024, 1, 3, 12, 0, 0, 0, time.UTC)))

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodGet, "/activities/calendar?from=2024-01-01&to=2024-01-03&tz=UTC", nil, true)
	GetActivitiesCalendarHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp calendarResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 3)
	// День без элементов: count=0, has_reminders=false.
	assert.Equal(t, "2024-01-01", resp.Items[0].Date)
	assert.Equal(t, 0, resp.Items[0].Count)
	assert.False(t, resp.Items[0].HasReminders)
	// Два факта и одно напоминание: count включает оба вида элементов.
	assert.Equal(t, "2024-01-02", resp.Items[1].Date)
	assert.Equal(t, 3, resp.Items[1].Count)
	assert.True(t, resp.Items[1].HasReminders)
	assert.Equal(t, "2024-01-03", resp.Items[2].Date)
	assert.Equal(t, 2, resp.Items[2].Count)
	assert.True(t, resp.Items[2].HasReminders)
	require.NoError(t, mock.ExpectationsWereMet())
}

// День только с фактами: has_reminders=false.
func TestGetActivitiesCalendarHandler_FactsOnlyHasNoReminders(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(calendarEventsQuery).
		WillReturnRows(sqlmock.NewRows([]string{"date_time"}).AddRow(time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC)))
	mock.ExpectQuery(calendarRemindersQuery).
		WillReturnRows(sqlmock.NewRows([]string{"remind_at"}))

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodGet, "/activities/calendar?from=2024-01-01&to=2024-01-01&tz=UTC", nil, true)
	GetActivitiesCalendarHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp calendarResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
	assert.Equal(t, 1, resp.Items[0].Count)
	assert.False(t, resp.Items[0].HasReminders)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetActivitiesCalendarHandler_InvalidTimeZone(t *testing.T) {
	for _, tz := range []string{"Mars/Olympus", "Local", "UTC+3"} {
		w := httptest.NewRecorder()
		GetActivitiesCalendarHandler(w, doRequest(http.MethodGet, "/activities/calendar?from=2024-01-01&to=2024-01-02&tz="+url.QueryEscape(tz), nil))
		assert.Equal(t, http.StatusBadRequest, w.Code, tz)
		assert.Contains(t, w.Body.String(), "VALIDATION_ERROR", tz)
	}
}

// Месяц в UTC+3: границы — полночь 1-го и полночь 1-го следующего месяца по
// Москве, а событие 30 сентября в 22:30Z попадает в 1 октября — именно тот
// день, который пользователь видит в сетке.
func TestGetActivitiesCalendarHandler_GroupsByClientTimeZone(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(calendarEventsQuery).
		WithArgs(testUserID,
			timeArg(time.Date(2026, 9, 30, 21, 0, 0, 0, time.UTC)),
			timeArg(time.Date(2026, 10, 31, 21, 0, 0, 0, time.UTC))).
		WillReturnRows(sqlmock.NewRows([]string{"date_time"}).
			AddRow(time.Date(2026, 9, 30, 22, 30, 0, 0, time.UTC)).
			AddRow(time.Date(2026, 10, 31, 20, 59, 0, 0, time.UTC)))
	mock.ExpectQuery(calendarRemindersQuery).
		WithArgs(testUserID,
			timeArg(time.Date(2026, 9, 30, 21, 0, 0, 0, time.UTC)),
			timeArg(time.Date(2026, 10, 31, 21, 0, 0, 0, time.UTC))).
		WillReturnRows(sqlmock.NewRows([]string{"remind_at"}).
			AddRow(time.Date(2026, 9, 30, 22, 30, 0, 0, time.UTC)))

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodGet, "/activities/calendar?from=2026-10-01&to=2026-10-31&tz=Europe/Moscow", nil, true)
	GetActivitiesCalendarHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp calendarResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 31)
	assert.Equal(t, "2026-10-01", resp.Items[0].Date)
	assert.Equal(t, 2, resp.Items[0].Count)
	assert.True(t, resp.Items[0].HasReminders)
	assert.Equal(t, "2026-10-31", resp.Items[30].Date)
	assert.Equal(t, 1, resp.Items[30].Count)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- GET /activities/day ---

func TestGetActivitiesDayHandler_MethodNotAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	GetActivitiesDayHandler(w, doRequest(http.MethodPost, "/activities/day", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestGetActivitiesDayHandler_MissingParam(t *testing.T) {
	w := httptest.NewRecorder()
	GetActivitiesDayHandler(w, doRequest(http.MethodGet, "/activities/day", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetActivitiesDayHandler_InvalidDate(t *testing.T) {
	w := httptest.NewRecorder()
	GetActivitiesDayHandler(w, doRequest(http.MethodGet, "/activities/day?date=not-a-date&tz=UTC", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetActivitiesDayHandler_Unauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	GetActivitiesDayHandler(w, doRequest(http.MethodGet, "/activities/day?date=2024-01-01&tz=UTC", nil))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetActivitiesDayHandler_MissingTimeZone(t *testing.T) {
	w := httptest.NewRecorder()
	GetActivitiesDayHandler(w, doRequest(http.MethodGet, "/activities/day?date=2024-01-01", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "VALIDATION_ERROR")
}

func TestGetActivitiesDayHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	eventID := "44444444-4444-4444-4444-444444444444"
	eventDate := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT e\.id, e\.user_id, e\.date_time, e\.type, e\.notes, e\.value\s+FROM event e\s+WHERE e\.user_id = \$1\s+AND e\.deleted_at IS NULL\s+AND e\.date_time >= \$2\s+AND e\.date_time < \$3\s+AND EXISTS`).
		WithArgs(testUserID, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(eventRow(eventID, "weight", `{"amount":5}`, eventDate))
	expectEventPets(mock, eventID, petRex)
	mock.ExpectQuery(`SELECT owner_id, COUNT\(\*\) FROM file\s+WHERE owner_type = \$1 AND owner_id = ANY\(\$2\) AND confirmed_at IS NOT NULL\s+GROUP BY owner_id`).
		WillReturnRows(sqlmock.NewRows([]string{"owner_id", "count"}).AddRow(eventID, 2))
	// Незавершённое напоминание, момент которого уже наступил, остаётся в
	// календаре наравне с будущими; собственная заметка напоминания
	// показывается вместо заметки настроек.
	reminderID := "55555555-5555-4555-8555-555555555555"
	planID := "66666666-6666-4666-8666-666666666666"
	mock.ExpectQuery(reminderCalendarQuery).
		WithArgs(testUserID, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows(reminderCalendarColumns).
			AddRow(reminderID, planID, time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC), "2 пипетки", "1 таблетка", "medication", []byte(`{"name":"Нурофен"}`), "medication", "Нурофен", 4))
	mock.ExpectQuery(`SELECT link\.plan_id, pet\.id, pet\.name, pet\.species\s+FROM reminder_plan_pet link`).
		WillReturnRows(sqlmock.NewRows([]string{"plan_id", "id", "name", "species"}).
			AddRow(planID, testPetID, "Rex", "DOG").AddRow(planID, testPetID2, "Tom", "CAT"))
	mock.ExpectQuery(`SELECT owner_id, COUNT\(\*\) FROM file\s+WHERE owner_type = \$1 AND owner_id = ANY\(\$2\) AND confirmed_at IS NOT NULL\s+GROUP BY owner_id`).
		WithArgs("reminder_plan_file", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"owner_id", "count"}).AddRow(planID, 1))
	mock.ExpectQuery(`SELECT owner_id, COUNT\(\*\) FROM file\s+WHERE owner_type = \$1 AND owner_id = ANY\(\$2\) AND confirmed_at IS NOT NULL\s+GROUP BY owner_id`).
		WithArgs("reminder_file", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"owner_id", "count"}).AddRow(reminderID, 2))

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodGet, "/activities/day?date=2024-01-01&tz=UTC", nil, true)
	GetActivitiesDayHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Date  string `json:"date"`
		Items []struct {
			ItemType   string  `json:"item_type"`
			ID         string  `json:"id"`
			PlanID     *string `json:"plan_id"`
			PlanSource *string `json:"plan_source"`
			PlanTitle  *string `json:"plan_source_title"`
			Unclosed   *int    `json:"plan_unclosed_count"`
			Date       string  `json:"date"`
			Notes      *string `json:"notes"`
			FilesCount int     `json:"files_count"`
			Pets       []struct {
				PetID   string `json:"pet_id"`
				PetName string `json:"pet_name"`
			} `json:"pets"`
			Type string `json:"type"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "2024-01-01", resp.Date)
	require.Len(t, resp.Items, 2)
	// Элементы отсортированы по моменту: напоминание 08:00, затем факт 10:00.
	assert.Equal(t, "reminder", resp.Items[0].ItemType)
	assert.Equal(t, reminderID, resp.Items[0].ID)
	require.NotNil(t, resp.Items[0].PlanID)
	assert.Equal(t, planID, *resp.Items[0].PlanID)
	require.NotNil(t, resp.Items[0].PlanSource)
	assert.Equal(t, "medication", *resp.Items[0].PlanSource)
	require.NotNil(t, resp.Items[0].PlanTitle)
	assert.Equal(t, "Нурофен", *resp.Items[0].PlanTitle)
	require.NotNil(t, resp.Items[0].Unclosed)
	assert.Equal(t, 4, *resp.Items[0].Unclosed)
	require.NotNil(t, resp.Items[0].Notes)
	assert.Equal(t, "2 пипетки", *resp.Items[0].Notes)
	assert.Equal(t, 3, resp.Items[0].FilesCount)
	assert.Equal(t, "medication", resp.Items[0].Type)

	assert.Equal(t, "event", resp.Items[1].ItemType)
	assert.Equal(t, eventID, resp.Items[1].ID)
	assert.Nil(t, resp.Items[1].PlanID)
	assert.Nil(t, resp.Items[1].PlanSource)
	assert.Nil(t, resp.Items[1].Unclosed)
	assert.Equal(t, 2, resp.Items[1].FilesCount)
	require.Len(t, resp.Items[1].Pets, 1)
	assert.Equal(t, testPetID, resp.Items[1].Pets[0].PetID)
	assert.Equal(t, "Rex", resp.Items[1].Pets[0].PetName)
	// Напоминание общих настроек отдаётся один раз со всеми питомцами.
	require.Len(t, resp.Items[0].Pets, 2)
	assert.Equal(t, "Tom", resp.Items[0].Pets[1].PetName)
	assert.Equal(t, "weight", resp.Items[1].Type)
	require.NoError(t, mock.ExpectationsWereMet())
}

// UTC-5: 27 сентября по Нью-Йорку — [27.09 04:00Z, 28.09 04:00Z), так что
// событие 28.09 в 01:00Z (27.09 21:00 по местному времени) остаётся в 27-м.
func TestGetActivitiesDayHandler_UsesClientTimeZoneBounds(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	eventID := "44444444-4444-4444-4444-444444444444"
	mock.ExpectQuery(`SELECT e\.id, e\.user_id, e\.date_time, e\.type, e\.notes, e\.value\s+FROM event e\s+WHERE e\.user_id = \$1`).
		WithArgs(testUserID,
			timeArg(time.Date(2026, 9, 27, 4, 0, 0, 0, time.UTC)),
			timeArg(time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC))).
		WillReturnRows(eventRow(eventID, "weight", `{"amount":5}`, time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC)))
	expectEventPets(mock, eventID, petRex)
	mock.ExpectQuery(`SELECT owner_id, COUNT\(\*\) FROM file`).
		WillReturnRows(sqlmock.NewRows([]string{"owner_id", "count"}))
	mock.ExpectQuery(reminderCalendarQuery).
		WithArgs(testUserID,
			timeArg(time.Date(2026, 9, 27, 4, 0, 0, 0, time.UTC)),
			timeArg(time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC))).
		WillReturnRows(sqlmock.NewRows(reminderCalendarColumns))

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodGet, "/activities/day?date=2026-09-27&tz=America/New_York", nil, true)
	GetActivitiesDayHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Date  string `json:"date"`
		Items []struct {
			ID   string `json:"id"`
			Date string `json:"date"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "2026-09-27", resp.Date)
	require.Len(t, resp.Items, 1)
	assert.Equal(t, "2026-09-28T01:00:00Z", resp.Items[0].Date)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetActivitiesDayHandler_InvalidTimeZone(t *testing.T) {
	w := httptest.NewRecorder()
	GetActivitiesDayHandler(w, doRequest(http.MethodGet, "/activities/day?date=2024-01-01&tz=Nowhere/City", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetActivitiesDayHandler_EmptyResultNo404(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT e\.id, e\.user_id, e\.date_time, e\.type, e\.notes, e\.value\s+FROM event e\s+WHERE e\.user_id = \$1`).
		WillReturnRows(sqlmock.NewRows(eventColumnNames))
	mock.ExpectQuery(reminderCalendarQuery).
		WillReturnRows(sqlmock.NewRows(reminderCalendarColumns))

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodGet, "/activities/day?date=2024-01-01&tz=UTC", nil, true)
	GetActivitiesDayHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Items []any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.Items)
	require.NoError(t, mock.ExpectationsWereMet())
}
