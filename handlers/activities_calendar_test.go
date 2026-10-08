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
const calendarEventsQuery = `SELECT e\.date_time, e\.notifications_enabled\s+FROM event e\s+JOIN pet p ON e\.pet_id = p\.id\s+WHERE p\.user_id = \$1\s+AND p\.deleted_at IS NULL\s+AND e\.deleted_at IS NULL\s+AND e\.date_time >= \$2\s+AND e\.date_time < \$3`

// timeArgMatcher сравнивает аргумент запроса с ожидаемым моментом времени
// через time.Equal — представление location у значения не важно.
type timeArgMatcher struct{ want time.Time }

func (m timeArgMatcher) Match(v driver.Value) bool {
	got, ok := v.(time.Time)
	return ok && got.Equal(m.want)
}

func timeArg(want time.Time) sqlmock.Argument { return timeArgMatcher{want: want} }

type calendarResponse struct {
	Items []struct {
		Date             string `json:"date"`
		Count            int    `json:"count"`
		HasNotifications bool   `json:"has_notifications"`
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
	GetActivitiesCalendarHandler(w, doRequest(http.MethodGet, "/activities/calendar?from=2024-01-10&to=2024-01-01", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetActivitiesCalendarHandler_RangeTooLong(t *testing.T) {
	w := httptest.NewRecorder()
	GetActivitiesCalendarHandler(w, doRequest(http.MethodGet, "/activities/calendar?from=2023-01-01&to=2024-06-01", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetActivitiesCalendarHandler_Unauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	GetActivitiesCalendarHandler(w, doRequest(http.MethodGet, "/activities/calendar?from=2024-01-01&to=2024-01-02", nil))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetActivitiesCalendarHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(calendarEventsQuery).
		WithArgs(testUserID,
			timeArg(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
			timeArg(time.Date(2024, 1, 4, 0, 0, 0, 0, time.UTC))).
		WillReturnRows(sqlmock.NewRows([]string{"date_time", "notifications_enabled"}).
			AddRow(time.Date(2024, 1, 2, 8, 0, 0, 0, time.UTC), false).
			AddRow(time.Date(2024, 1, 2, 9, 0, 0, 0, time.UTC), true).
			AddRow(time.Date(2024, 1, 2, 23, 59, 0, 0, time.UTC), false))

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodGet, "/activities/calendar?from=2024-01-01&to=2024-01-03", nil, true)
	GetActivitiesCalendarHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp calendarResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 3)
	assert.Equal(t, "2024-01-01", resp.Items[0].Date)
	assert.Equal(t, 0, resp.Items[0].Count)
	assert.False(t, resp.Items[0].HasNotifications)
	assert.Equal(t, "2024-01-02", resp.Items[1].Date)
	assert.Equal(t, 3, resp.Items[1].Count)
	assert.True(t, resp.Items[1].HasNotifications)
	assert.Equal(t, "2024-01-03", resp.Items[2].Date)
	assert.Equal(t, 0, resp.Items[2].Count)
	assert.False(t, resp.Items[2].HasNotifications)
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
		WillReturnRows(sqlmock.NewRows([]string{"date_time", "notifications_enabled"}).
			AddRow(time.Date(2026, 9, 30, 22, 30, 0, 0, time.UTC), true).
			AddRow(time.Date(2026, 10, 31, 20, 59, 0, 0, time.UTC), false))

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodGet, "/activities/calendar?from=2026-10-01&to=2026-10-31&tz=Europe/Moscow", nil, true)
	GetActivitiesCalendarHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp calendarResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 31)
	assert.Equal(t, "2026-10-01", resp.Items[0].Date)
	assert.Equal(t, 1, resp.Items[0].Count)
	assert.True(t, resp.Items[0].HasNotifications)
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
	GetActivitiesDayHandler(w, doRequest(http.MethodGet, "/activities/day?date=not-a-date", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetActivitiesDayHandler_Unauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	GetActivitiesDayHandler(w, doRequest(http.MethodGet, "/activities/day?date=2024-01-01", nil))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetActivitiesDayHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	eventID := "44444444-4444-4444-4444-444444444444"
	eventDate := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT e\.id, e\.pet_id, e\.date_time, e\.type, e\.notes, e\.value, e\.notifications_enabled, p\.name\s+FROM event e\s+JOIN pet p ON e\.pet_id = p\.id\s+WHERE p\.user_id = \$1\s+AND p\.deleted_at IS NULL\s+AND e\.deleted_at IS NULL\s+AND e\.date_time >= \$2\s+AND e\.date_time < \$3\s+ORDER BY e\.date_time ASC`).
		WithArgs(testUserID, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value", "notifications_enabled", "name"}).
			AddRow(eventID, testPetID, eventDate, "weight", nil, []byte(`{"amount":5}`), false, "Rex"))
	mock.ExpectQuery(`SELECT owner_id, COUNT\(\*\) FROM file\s+WHERE owner_type = \$1 AND owner_id = ANY\(\$2\) AND confirmed_at IS NOT NULL\s+GROUP BY owner_id`).
		WillReturnRows(sqlmock.NewRows([]string{"owner_id", "count"}).AddRow(eventID, 2))

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodGet, "/activities/day?date=2024-01-01", nil, true)
	GetActivitiesDayHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Date  string `json:"date"`
		Items []struct {
			ID         string `json:"id"`
			FilesCount int    `json:"files_count"`
			PetID      string `json:"pet_id"`
			PetName    string `json:"pet_name"`
			Type       string `json:"type"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "2024-01-01", resp.Date)
	require.Len(t, resp.Items, 1)
	assert.Equal(t, eventID, resp.Items[0].ID)
	assert.Equal(t, 2, resp.Items[0].FilesCount)
	assert.Equal(t, testPetID, resp.Items[0].PetID)
	assert.Equal(t, "Rex", resp.Items[0].PetName)
	assert.Equal(t, "weight", resp.Items[0].Type)
	require.NoError(t, mock.ExpectationsWereMet())
}

// UTC-5: 27 сентября по Нью-Йорку — [27.09 04:00Z, 28.09 04:00Z), так что
// событие 28.09 в 01:00Z (27.09 21:00 по местному времени) остаётся в 27-м.
func TestGetActivitiesDayHandler_UsesClientTimeZoneBounds(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	eventID := "44444444-4444-4444-4444-444444444444"
	mock.ExpectQuery(`SELECT e\.id, e\.pet_id, e\.date_time, e\.type, e\.notes, e\.value, e\.notifications_enabled, p\.name\s+FROM event e\s+JOIN pet p ON e\.pet_id = p\.id`).
		WithArgs(testUserID,
			timeArg(time.Date(2026, 9, 27, 4, 0, 0, 0, time.UTC)),
			timeArg(time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC))).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value", "notifications_enabled", "name"}).
			AddRow(eventID, testPetID, time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC), "weight", nil, []byte(`{"amount":5}`), false, "Rex"))
	mock.ExpectQuery(`SELECT owner_id, COUNT\(\*\) FROM file`).
		WillReturnRows(sqlmock.NewRows([]string{"owner_id", "count"}))

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
	mock.ExpectQuery(`SELECT e\.id, e\.pet_id, e\.date_time, e\.type, e\.notes, e\.value, e\.notifications_enabled, p\.name\s+FROM event e\s+JOIN pet p ON e\.pet_id = p\.id`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value", "notifications_enabled", "name"}))

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodGet, "/activities/day?date=2024-01-01", nil, true)
	GetActivitiesDayHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Items []any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.Items)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- GET /activities/nearest ---

func TestGetActivitiesNearestHandler_MethodNotAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	GetActivitiesNearestHandler(w, doRequest(http.MethodPost, "/activities/nearest", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestGetActivitiesNearestHandler_Unauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	GetActivitiesNearestHandler(w, doRequest(http.MethodGet, "/activities/nearest", nil))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetActivitiesNearestHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	eventID := "44444444-4444-4444-4444-444444444444"
	eventDate := time.Date(2030, 1, 1, 10, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT e\.id, e\.pet_id, e\.date_time, e\.type, e\.notes, e\.value, e\.notifications_enabled, p\.name\s+FROM event e\s+JOIN pet p ON e\.pet_id = p\.id\s+WHERE p\.user_id = \$1\s+AND p\.deleted_at IS NULL\s+AND e\.deleted_at IS NULL\s+AND e\.date_time >= \$2\s+ORDER BY e\.date_time ASC, e\.id ASC\s+LIMIT 1`).
		WithArgs(testUserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value", "notifications_enabled", "name"}).
			AddRow(eventID, testPetID, eventDate, "weight", nil, []byte(`{"amount":5}`), false, "Rex"))
	mock.ExpectQuery(`SELECT owner_id, COUNT\(\*\) FROM file\s+WHERE owner_type = \$1 AND owner_id = ANY\(\$2\) AND confirmed_at IS NOT NULL\s+GROUP BY owner_id`).
		WillReturnRows(sqlmock.NewRows([]string{"owner_id", "count"}).AddRow(eventID, 1))

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodGet, "/activities/nearest", nil, true)
	GetActivitiesNearestHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Item *struct {
			ID         string `json:"id"`
			FilesCount int    `json:"files_count"`
			PetID      string `json:"pet_id"`
			PetName    string `json:"pet_name"`
			Type       string `json:"type"`
		} `json:"item"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.Item)
	assert.Equal(t, eventID, resp.Item.ID)
	assert.Equal(t, 1, resp.Item.FilesCount)
	assert.Equal(t, testPetID, resp.Item.PetID)
	assert.Equal(t, "Rex", resp.Item.PetName)
	assert.Equal(t, "weight", resp.Item.Type)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetActivitiesNearestHandler_NoUpcomingEventReturnsNullItem(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT e\.id, e\.pet_id, e\.date_time, e\.type, e\.notes, e\.value, e\.notifications_enabled, p\.name\s+FROM event e\s+JOIN pet p ON e\.pet_id = p\.id`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value", "notifications_enabled", "name"}))

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodGet, "/activities/nearest", nil, true)
	GetActivitiesNearestHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Item *json.RawMessage `json:"item"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Nil(t, resp.Item)
	require.NoError(t, mock.ExpectationsWereMet())
}
