package handlers

import (
	"database/sql"
	"encoding/json"
	"myauthservice/models"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func profileRequest(t *testing.T, method string, body any, authed bool) *http.Request {
	t.Helper()
	r := doRequest(method, "/profile", body)
	if authed {
		r.Header.Set("Authorization", "Bearer "+validAccessToken(t, testProfileID))
	}
	return r
}

func TestProfileHandler_MethodNotAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	ProfileHandler(w, profileRequest(t, http.MethodDelete, nil, false))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestCreateProfileHandler_Unauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	CreateProfileHandler(w, profileRequest(t, http.MethodPost, models.Profile{FirstName: "Ann"}, false))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestCreateProfileHandler_MissingFirstName(t *testing.T) {
	w := httptest.NewRecorder()
	r := profileRequest(t, http.MethodPost, models.Profile{FirstName: "  "}, true)
	CreateProfileHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateProfileHandler_InsertsWhenMissing(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM profile WHERE user_id=\$1`).
		WithArgs(testProfileID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(`INSERT INTO profile`).WillReturnResult(sqlmock.NewResult(1, 1))

	w := httptest.NewRecorder()
	r := profileRequest(t, http.MethodPost, models.Profile{FirstName: "Ann"}, true)
	CreateProfileHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateProfileHandler_UpdatesWhenExists(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM profile WHERE user_id=\$1`).
		WithArgs(testProfileID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectExec(`UPDATE profile`).WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	r := profileRequest(t, http.MethodPost, models.Profile{FirstName: "Ann"}, true)
	CreateProfileHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateProfileHandler_DBErrorOnCount(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM profile WHERE user_id=\$1`).
		WillReturnError(assertError)

	w := httptest.NewRecorder()
	r := profileRequest(t, http.MethodPost, models.Profile{FirstName: "Ann"}, true)
	CreateProfileHandler(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestUpdateProfileHandler_Unauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	UpdateProfileHandler(w, profileRequest(t, http.MethodPut, map[string]string{"first_name": "Ann"}, false))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestUpdateProfileHandler_MissingFirstName(t *testing.T) {
	w := httptest.NewRecorder()
	r := profileRequest(t, http.MethodPut, map[string]string{"last_name": "Smith"}, true)
	UpdateProfileHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateProfileHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectExec(`UPDATE profile SET first_name=\$1 WHERE user_id=\$2`).
		WithArgs("Ann", testProfileID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	r := profileRequest(t, http.MethodPut, map[string]string{"first_name": "Ann"}, true)
	UpdateProfileHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateProfileHandler_MultipleFields(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectExec(`UPDATE profile SET first_name=\$1, last_name=\$2 WHERE user_id=\$3`).
		WithArgs("Ann", "Smith", testProfileID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	r := profileRequest(t, http.MethodPut, map[string]string{"first_name": "Ann", "last_name": "Smith"}, true)
	UpdateProfileHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetProfileHandler_NotFound(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(`SELECT user_id, first_name, middle_name, last_name, email, phone FROM profile WHERE user_id=\$1`).
		WithArgs(testProfileID).
		WillReturnError(sql.ErrNoRows)

	w := httptest.NewRecorder()
	r := profileRequest(t, http.MethodGet, nil, true)
	GetProfileHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetProfileHandler_DBError(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(`SELECT user_id, first_name, middle_name, last_name, email, phone FROM profile WHERE user_id=\$1`).
		WillReturnError(assertError)

	w := httptest.NewRecorder()
	r := profileRequest(t, http.MethodGet, nil, true)
	GetProfileHandler(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetProfileHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(`SELECT user_id, first_name, middle_name, last_name, email, phone FROM profile WHERE user_id=\$1`).
		WithArgs(testProfileID).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "first_name", "middle_name", "last_name", "email", "phone"}).
			AddRow(testProfileID, "Ann", nil, nil, nil, nil))
	mock.ExpectQuery(`SELECT login FROM users WHERE id=\$1`).
		WithArgs(testProfileID).
		WillReturnRows(sqlmock.NewRows([]string{"login"}).AddRow("ann-login"))

	w := httptest.NewRecorder()
	r := profileRequest(t, http.MethodGet, nil, true)
	GetProfileHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.ProfileResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Ann", resp.FirstName)
	assert.Equal(t, "ann-login", resp.Login)
	require.NoError(t, mock.ExpectationsWereMet())
}
