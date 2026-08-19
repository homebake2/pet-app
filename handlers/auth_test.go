package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"myauthservice/models"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func hashPassword(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	require.NoError(t, err)
	return string(hash)
}

func doRequest(method, path string, body any) *http.Request {
	var reader *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	return httptest.NewRequest(method, path, reader)
}

func TestRegisterHandler_MethodNotAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	RegisterHandler(w, doRequest(http.MethodGet, "/auth/register", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestRegisterHandler_BadBody(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader([]byte("{invalid")))
	RegisterHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRegisterHandler_MissingFields(t *testing.T) {
	w := httptest.NewRecorder()
	RegisterHandler(w, doRequest(http.MethodPost, "/auth/register", models.User{Login: "", Password: ""}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRegisterHandler_LookupDBError(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(`SELECT id, password FROM users WHERE login=\$1`).
		WithArgs("john").
		WillReturnError(assertError)

	w := httptest.NewRecorder()
	RegisterHandler(w, doRequest(http.MethodPost, "/auth/register", models.User{Login: "john", Password: "pw"}))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRegisterHandler_InsertError(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(`SELECT id, password FROM users WHERE login=\$1`).
		WithArgs("john").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`INSERT INTO users \(login, password\) VALUES \(\$1, \$2\) RETURNING id`).
		WithArgs("john", sqlmock.AnyArg()).
		WillReturnError(assertError)

	w := httptest.NewRecorder()
	RegisterHandler(w, doRequest(http.MethodPost, "/auth/register", models.User{Login: "john", Password: "pw"}))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// Регистрация и вход объединены: если пользователя с таким login нет — он создаётся.
func TestRegisterHandler_CreatesNewUserWithHashedPassword(t *testing.T) {
	mock := setupMockDB(t)
	newID := "11111111-1111-1111-1111-111111111111"
	mock.ExpectQuery(`SELECT id, password FROM users WHERE login=\$1`).
		WithArgs("john").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`INSERT INTO users \(login, password\) VALUES \(\$1, \$2\) RETURNING id`).
		WithArgs("john", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(newID))
	mock.ExpectExec(`UPDATE users SET refresh_token=\$1 WHERE login=\$2`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	RegisterHandler(w, doRequest(http.MethodPost, "/auth/register", models.User{Login: "john", Password: "pw"}))

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.AuthResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.AccessToken)
	assert.NotEmpty(t, resp.RefreshToken)
	require.NoError(t, mock.ExpectationsWereMet())
}

// /auth/login тоже автоматически регистрирует пользователя, если его ещё нет.
func TestLoginHandler_AutoRegistersUnknownUser(t *testing.T) {
	mock := setupMockDB(t)
	newID := "11111111-1111-1111-1111-111111111111"
	mock.ExpectQuery(`SELECT id, password FROM users WHERE login=\$1`).
		WithArgs("john").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`INSERT INTO users \(login, password\) VALUES \(\$1, \$2\) RETURNING id`).
		WithArgs("john", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(newID))
	mock.ExpectExec(`UPDATE users SET refresh_token=\$1 WHERE login=\$2`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	LoginHandler(w, doRequest(http.MethodPost, "/auth/login", models.User{Login: "john", Password: "pw"}))

	assert.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLoginHandler_MethodNotAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	LoginHandler(w, doRequest(http.MethodGet, "/auth/login", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestLoginHandler_MissingFields(t *testing.T) {
	w := httptest.NewRecorder()
	LoginHandler(w, doRequest(http.MethodPost, "/auth/login", models.User{}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestLoginHandler_WrongPassword(t *testing.T) {
	mock := setupMockDB(t)
	userID := "11111111-1111-1111-1111-111111111111"
	mock.ExpectQuery(`SELECT id, password FROM users WHERE login=\$1`).
		WithArgs("john").
		WillReturnRows(sqlmock.NewRows([]string{"id", "password"}).AddRow(userID, hashPassword(t, "correct")))

	w := httptest.NewRecorder()
	LoginHandler(w, doRequest(http.MethodPost, "/auth/login", models.User{Login: "john", Password: "wrong"}))

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestLoginHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	userID := "11111111-1111-1111-1111-111111111111"
	mock.ExpectQuery(`SELECT id, password FROM users WHERE login=\$1`).
		WithArgs("john").
		WillReturnRows(sqlmock.NewRows([]string{"id", "password"}).AddRow(userID, hashPassword(t, "pw")))
	mock.ExpectExec(`UPDATE users SET refresh_token=\$1 WHERE login=\$2`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	LoginHandler(w, doRequest(http.MethodPost, "/auth/login", models.User{Login: "john", Password: "pw"}))

	assert.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshTokenHandler_MissingToken(t *testing.T) {
	w := httptest.NewRecorder()
	RefreshTokenHandler(w, doRequest(http.MethodPost, "/auth/refresh", map[string]string{"refresh_token": ""}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRefreshTokenHandler_InvalidToken(t *testing.T) {
	w := httptest.NewRecorder()
	RefreshTokenHandler(w, doRequest(http.MethodPost, "/auth/refresh", map[string]string{"refresh_token": "garbage"}))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRefreshTokenHandler_WrongTokenType(t *testing.T) {
	token := validAccessToken(t, "11111111-1111-1111-1111-111111111111")
	w := httptest.NewRecorder()
	RefreshTokenHandler(w, doRequest(http.MethodPost, "/auth/refresh", map[string]string{"refresh_token": token}))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRefreshTokenHandler_UserNotFound(t *testing.T) {
	mock := setupMockDB(t)
	token := validRefreshToken(t, "john", "11111111-1111-1111-1111-111111111111")
	mock.ExpectQuery(`SELECT refresh_token FROM users WHERE login=\$1`).
		WithArgs("john").
		WillReturnError(sql.ErrNoRows)

	w := httptest.NewRecorder()
	RefreshTokenHandler(w, doRequest(http.MethodPost, "/auth/refresh", map[string]string{"refresh_token": token}))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRefreshTokenHandler_StaleToken(t *testing.T) {
	mock := setupMockDB(t)
	token := validRefreshToken(t, "john", "11111111-1111-1111-1111-111111111111")
	mock.ExpectQuery(`SELECT refresh_token FROM users WHERE login=\$1`).
		WithArgs("john").
		WillReturnRows(sqlmock.NewRows([]string{"refresh_token"}).AddRow("some-other-token"))

	w := httptest.NewRecorder()
	RefreshTokenHandler(w, doRequest(http.MethodPost, "/auth/refresh", map[string]string{"refresh_token": token}))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRefreshTokenHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	token := validRefreshToken(t, "john", "11111111-1111-1111-1111-111111111111")
	mock.ExpectQuery(`SELECT refresh_token FROM users WHERE login=\$1`).
		WithArgs("john").
		WillReturnRows(sqlmock.NewRows([]string{"refresh_token"}).AddRow(token))
	mock.ExpectExec(`UPDATE users SET refresh_token=\$1 WHERE login=\$2`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	RefreshTokenHandler(w, doRequest(http.MethodPost, "/auth/refresh", map[string]string{"refresh_token": token}))

	assert.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLogoutHandler_MissingToken(t *testing.T) {
	w := httptest.NewRecorder()
	LogoutHandler(w, doRequest(http.MethodPost, "/auth/logout", map[string]string{"refresh_token": ""}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestLogoutHandler_InvalidToken(t *testing.T) {
	w := httptest.NewRecorder()
	LogoutHandler(w, doRequest(http.MethodPost, "/auth/logout", map[string]string{"refresh_token": "garbage"}))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestLogoutHandler_StaleToken(t *testing.T) {
	mock := setupMockDB(t)
	token := validRefreshToken(t, "john", "11111111-1111-1111-1111-111111111111")
	mock.ExpectQuery(`SELECT refresh_token FROM users WHERE login=\$1`).
		WithArgs("john").
		WillReturnRows(sqlmock.NewRows([]string{"refresh_token"}).AddRow("other"))

	w := httptest.NewRecorder()
	LogoutHandler(w, doRequest(http.MethodPost, "/auth/logout", map[string]string{"refresh_token": token}))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestLogoutHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	token := validRefreshToken(t, "john", "11111111-1111-1111-1111-111111111111")
	mock.ExpectQuery(`SELECT refresh_token FROM users WHERE login=\$1`).
		WithArgs("john").
		WillReturnRows(sqlmock.NewRows([]string{"refresh_token"}).AddRow(token))
	mock.ExpectExec(`UPDATE users SET refresh_token=NULL WHERE login=\$1`).
		WithArgs("john").
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	LogoutHandler(w, doRequest(http.MethodPost, "/auth/logout", map[string]string{"refresh_token": token}))

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLogoutHandler_ClearError(t *testing.T) {
	mock := setupMockDB(t)
	token := validRefreshToken(t, "john", "11111111-1111-1111-1111-111111111111")
	mock.ExpectQuery(`SELECT refresh_token FROM users WHERE login=\$1`).
		WithArgs("john").
		WillReturnRows(sqlmock.NewRows([]string{"refresh_token"}).AddRow(token))
	mock.ExpectExec(`UPDATE users SET refresh_token=NULL WHERE login=\$1`).
		WithArgs("john").
		WillReturnError(assertError)

	w := httptest.NewRecorder()
	LogoutHandler(w, doRequest(http.MethodPost, "/auth/logout", map[string]string{"refresh_token": token}))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
