package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthHandler_OK(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(`SELECT 1`).WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))

	w := httptest.NewRecorder()
	HealthHandler(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"status":"ok"}`, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHealthHandler_DBDownReturns503WithoutDetails(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(`SELECT 1`).WillReturnError(assertError)

	w := httptest.NewRecorder()
	HealthHandler(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.JSONEq(t, `{"status":"unavailable"}`, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHealthHandler_MethodNotAllowed(t *testing.T) {
	setupMockDB(t)

	w := httptest.NewRecorder()
	HealthHandler(w, httptest.NewRequest(http.MethodPost, "/healthz", nil))

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
