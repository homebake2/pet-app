package handlers

import (
	"database/sql"
	"encoding/json"
	"myauthservice/eventreg"
	"myauthservice/models"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func statsCountsPath(query string) string {
	return "/events/stats/counts?pet_id=" + testPetID + "&" + query
}

func TestGetEventStatsCountsHandler_MethodNotAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	GetEventStatsCountsHandler(w, eventRequest(t, http.MethodPost, statsCountsPath("from=2024-01-01&to=2024-01-02&tz=UTC"), nil, false))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestGetEventStatsCountsHandler_Unauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	GetEventStatsCountsHandler(w, eventRequest(t, http.MethodGet, statsCountsPath("from=2024-01-01&to=2024-01-02&tz=UTC"), nil, false))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetEventStatsCountsHandler_BadRequest(t *testing.T) {
	cases := map[string]string{
		"нет from":    "to=2024-01-02&tz=UTC",
		"нет to":      "from=2024-01-01&tz=UTC",
		"нет tz":      "from=2024-01-01&to=2024-01-02",
		"плохой tz":   "from=2024-01-01&to=2024-01-02&tz=Nowhere/City",
		"from > to":   "from=2024-02-01&to=2024-01-01&tz=UTC",
		"длинный":     "from=2024-01-01&to=2025-06-01&tz=UTC",
		"плохая дата": "from=01.01.2024&to=2024-01-02&tz=UTC",
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			mock := setupMockDB(t)
			w := httptest.NewRecorder()
			GetEventStatsCountsHandler(w, eventRequest(t, http.MethodGet, statsCountsPath(query), nil, true))
			assert.Equal(t, http.StatusBadRequest, w.Code)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestGetEventStatsCountsHandler_PetNotFound(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnError(sql.ErrNoRows)

	w := httptest.NewRecorder()
	GetEventStatsCountsHandler(w, eventRequest(t, http.MethodGet, statsCountsPath("from=2024-01-01&to=2024-01-02&tz=UTC"), nil, true))

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetEventStatsCountsHandler_QueryError(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPet(mock)
	mock.ExpectQuery(`SELECT e\.type, count\(\*\)`).WillReturnError(assertError)

	w := httptest.NewRecorder()
	GetEventStatsCountsHandler(w, eventRequest(t, http.MethodGet, statsCountsPath("from=2024-01-01&to=2024-01-02&tz=UTC"), nil, true))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Один запрос GROUP BY по типам: границы периода — локальные сутки пояса
// клиента (UTC+3: начало 2024-01-01 — 21:00 UTC предыдущего дня), типы —
// только агрегируемые из реестра.
func TestGetEventStatsCountsHandler_CountsByType(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPet(mock)
	mock.ExpectQuery(`SELECT e\.type, count\(\*\)`).
		WithArgs(
			uuid.MustParse(testPetID),
			pq.Array(eventreg.AggregatableTypes()),
			mustRFC3339("2023-12-31T21:00:00Z"),
			mustRFC3339("2024-01-02T21:00:00Z"),
			testUserID,
		).
		WillReturnRows(sqlmock.NewRows([]string{"type", "count"}).
			AddRow("water", 5).
			AddRow("weight", 2))

	w := httptest.NewRecorder()
	GetEventStatsCountsHandler(w, eventRequest(t, http.MethodGet, statsCountsPath("from=2024-01-01&to=2024-01-02&tz=Europe/Moscow"), nil, true))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp models.EventStatsCountsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, []models.EventStatsTypeCount{
		{Type: "water", Count: 5},
		{Type: "weight", Count: 2},
	}, resp.Items)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Нет событий — пустой массив items, а не null.
func TestGetEventStatsCountsHandler_EmptyItems(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPet(mock)
	mock.ExpectQuery(`SELECT e\.type, count\(\*\)`).
		WillReturnRows(sqlmock.NewRows([]string{"type", "count"}))

	w := httptest.NewRecorder()
	GetEventStatsCountsHandler(w, eventRequest(t, http.MethodGet, statsCountsPath("from=2024-01-01&to=2024-01-02&tz=UTC"), nil, true))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{"items":[]}`, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}
