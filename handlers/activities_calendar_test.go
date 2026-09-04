package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	mock.ExpectQuery(`SELECT \(e\.date_time AT TIME ZONE 'UTC'\)::date AS day, COUNT\(\*\)\s+FROM event e\s+JOIN pet p ON e\.pet_id = p\.id\s+WHERE p\.user_id = \$1\s+AND p\.deleted_at IS NULL\s+AND e\.deleted_at IS NULL\s+AND e\.date_time >= \$2\s+AND e\.date_time < \$3\s+GROUP BY day`).
		WithArgs(testUserID, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"day", "count"}).
			AddRow(time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), 3))

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodGet, "/activities/calendar?from=2024-01-01&to=2024-01-03", nil, true)
	GetActivitiesCalendarHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Items []struct {
			Date  string `json:"date"`
			Count int    `json:"count"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 3)
	assert.Equal(t, "2024-01-01", resp.Items[0].Date)
	assert.Equal(t, 0, resp.Items[0].Count)
	assert.Equal(t, "2024-01-02", resp.Items[1].Date)
	assert.Equal(t, 3, resp.Items[1].Count)
	assert.Equal(t, "2024-01-03", resp.Items[2].Date)
	assert.Equal(t, 0, resp.Items[2].Count)
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
	mock.ExpectQuery(`SELECT e\.id, e\.pet_id, e\.date_time, e\.type, e\.notes, e\.value, p\.name\s+FROM event e\s+JOIN pet p ON e\.pet_id = p\.id\s+WHERE p\.user_id = \$1\s+AND p\.deleted_at IS NULL\s+AND e\.deleted_at IS NULL\s+AND e\.date_time >= \$2\s+AND e\.date_time < \$3\s+ORDER BY e\.date_time ASC`).
		WithArgs(testUserID, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value", "name"}).
			AddRow(eventID, testPetID, eventDate, "weight", nil, []byte(`{"amount":5}`), "Rex"))
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

func TestGetActivitiesDayHandler_EmptyResultNo404(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT e\.id, e\.pet_id, e\.date_time, e\.type, e\.notes, e\.value, p\.name\s+FROM event e\s+JOIN pet p ON e\.pet_id = p\.id`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value", "name"}))

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
