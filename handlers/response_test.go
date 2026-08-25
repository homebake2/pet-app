package handlers

import (
	"database/sql"
	"encoding/json"
	"myauthservice/openapi"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusCreated, map[string]string{"foo": "bar"})

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "bar", body["foo"])
}

func TestWriteJSON_NilBody(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusNoContent, nil)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.Bytes())
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, openapi.VALIDATIONERROR, "oops")

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var body openapi.GetErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, openapi.VALIDATIONERROR, body.Code)
	assert.Equal(t, "oops", body.Message)
}

func TestRequireUserID(t *testing.T) {
	tests := []struct {
		name       string
		authHeader string
		wantOK     bool
		wantStatus int
	}{
		{"missing header", "", false, http.StatusUnauthorized},
		{"missing bearer prefix", "sometoken", false, http.StatusUnauthorized},
		{"empty token after bearer", "Bearer ", false, http.StatusUnauthorized},
		{"invalid token", "Bearer garbage", false, http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.authHeader != "" {
				r.Header.Set("Authorization", tt.authHeader)
			}

			id, ok := requireUserID(w, r)
			assert.Equal(t, tt.wantOK, ok)
			if !tt.wantOK {
				assert.Equal(t, tt.wantStatus, w.Code)
				assert.Empty(t, id)
			}
		})
	}
}

func TestRequireUserID_Valid(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, "user-42")

	token := validAccessToken(t, "user-42")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer "+token)

	id, ok := requireUserID(w, r)
	require.True(t, ok)
	assert.Equal(t, "user-42", id)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRequireUserID_UserNotFound(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(`SELECT tokens_invalidated_at FROM users WHERE id=\$1`).
		WithArgs("user-42").
		WillReturnError(sql.ErrNoRows)

	token := validAccessToken(t, "user-42")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer "+token)

	id, ok := requireUserID(w, r)
	assert.False(t, ok)
	assert.Empty(t, id)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireUserID_RevokedToken(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(`SELECT tokens_invalidated_at FROM users WHERE id=\$1`).
		WithArgs("user-42").
		WillReturnRows(sqlmock.NewRows([]string{"tokens_invalidated_at"}).AddRow(time.Now().Add(time.Hour)))

	token := validAccessToken(t, "user-42")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer "+token)

	id, ok := requireUserID(w, r)
	assert.False(t, ok)
	assert.Empty(t, id)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
