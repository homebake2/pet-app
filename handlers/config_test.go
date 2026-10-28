package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const appConfigQuery = `SELECT about_me_url FROM app_config WHERE id`

func TestGetConfigHandler_OKWithoutAuthorization(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(appConfigQuery).
		WillReturnRows(sqlmock.NewRows([]string{"about_me_url"}).AddRow("https://example.com"))

	w := httptest.NewRecorder()
	GetConfigHandler(w, httptest.NewRequest(http.MethodGet, "/config", nil))

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, map[string]any{"aboutMeUrl": "https://example.com"}, body)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetConfigHandler_IgnoresInvalidAuthorization(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(appConfigQuery).
		WillReturnRows(sqlmock.NewRows([]string{"about_me_url"}).AddRow("https://example.com"))

	r := httptest.NewRequest(http.MethodGet, "/config", nil)
	r.Header.Set("Authorization", "Bearer garbage")
	w := httptest.NewRecorder()
	GetConfigHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
}

func assertConfig500(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	require.Equal(t, http.StatusInternalServerError, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "INTERNAL_ERROR", body["code"])
	assert.NotEmpty(t, body["message"])
}

func TestGetConfigHandler_NoRow(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(appConfigQuery).WillReturnError(sql.ErrNoRows)

	w := httptest.NewRecorder()
	GetConfigHandler(w, httptest.NewRequest(http.MethodGet, "/config", nil))

	assertConfig500(t, w)
}

func TestGetConfigHandler_DBError(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(appConfigQuery).WillReturnError(assertError)

	w := httptest.NewRecorder()
	GetConfigHandler(w, httptest.NewRequest(http.MethodGet, "/config", nil))

	assertConfig500(t, w)
}

func TestGetConfigHandler_MethodNotAllowed(t *testing.T) {
	setupMockDB(t)

	w := httptest.NewRecorder()
	GetConfigHandler(w, httptest.NewRequest(http.MethodPost, "/config", nil))

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
