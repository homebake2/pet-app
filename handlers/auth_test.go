package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"myauthservice/models"
	"myauthservice/openapi"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
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

// expectRegistrationAllowed мокает атомарный upsert счётчика rate-limit на
// регистрацию, возвращая count в пределах разрешённого лимита.
func expectRegistrationAllowed(mock sqlmock.Sqlmock, count int) {
	mock.ExpectQuery(`INSERT INTO registration_rate_limit`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(count))
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

func TestRegisterHandler_LoginTooShort(t *testing.T) {
	w := httptest.NewRecorder()
	RegisterHandler(w, doRequest(http.MethodPost, "/auth/register", models.User{Login: "j", Password: "password"}))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var body openapi.GetErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, openapi.VALIDATIONERROR, body.Code)
}

// Login с пробелами по краям должен обрезаться перед проверкой минимальной
// длины: " j " после trim — 1 символ, короче минимума.
func TestRegisterHandler_LoginTooShortAfterTrim(t *testing.T) {
	w := httptest.NewRecorder()
	RegisterHandler(w, doRequest(http.MethodPost, "/auth/register", models.User{Login: " j ", Password: "password"}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRegisterHandler_PasswordTooShort(t *testing.T) {
	w := httptest.NewRecorder()
	RegisterHandler(w, doRequest(http.MethodPost, "/auth/register", models.User{Login: "john", Password: "short"}))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var body openapi.GetErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, openapi.VALIDATIONERROR, body.Code)
}

func TestRegisterHandler_LookupDBError(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(`SELECT id, password FROM users WHERE lower\(trim\(login\)\) = lower\(\$1\)`).
		WithArgs("john").
		WillReturnError(assertError)

	w := httptest.NewRecorder()
	RegisterHandler(w, doRequest(http.MethodPost, "/auth/register", models.User{Login: "john", Password: "password"}))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestRegisterHandler_InsertError(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(`SELECT id, password FROM users WHERE lower\(trim\(login\)\) = lower\(\$1\)`).
		WithArgs("john").
		WillReturnError(sql.ErrNoRows)
	expectRegistrationAllowed(mock, 1)
	mock.ExpectQuery(`INSERT INTO users \(login, password\) VALUES \(\$1, \$2\) RETURNING id`).
		WithArgs("john", sqlmock.AnyArg()).
		WillReturnError(assertError)

	w := httptest.NewRecorder()
	RegisterHandler(w, doRequest(http.MethodPost, "/auth/register", models.User{Login: "john", Password: "password"}))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// Регистрация и вход объединены: если пользователя с таким login нет — он создаётся.
func TestRegisterHandler_CreatesNewUserWithHashedPassword(t *testing.T) {
	mock := setupMockDB(t)
	newID := "11111111-1111-1111-1111-111111111111"
	mock.ExpectQuery(`SELECT id, password FROM users WHERE lower\(trim\(login\)\) = lower\(\$1\)`).
		WithArgs("john").
		WillReturnError(sql.ErrNoRows)
	expectRegistrationAllowed(mock, 1)
	mock.ExpectQuery(`INSERT INTO users \(login, password\) VALUES \(\$1, \$2\) RETURNING id`).
		WithArgs("john", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(newID))
	mock.ExpectExec(`UPDATE users SET refresh_token=\$1 WHERE id=\$2`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	RegisterHandler(w, doRequest(http.MethodPost, "/auth/register", models.User{Login: "john", Password: "password"}))

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.AuthResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.AccessToken)
	assert.NotEmpty(t, resp.RefreshToken)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Login нормализуется: обрезаются пробелы, сравнение регистронезависимо.
func TestRegisterHandler_NormalizesLoginTrimAndCase(t *testing.T) {
	mock := setupMockDB(t)
	userID := "11111111-1111-1111-1111-111111111111"
	mock.ExpectQuery(`SELECT id, password FROM users WHERE lower\(trim\(login\)\) = lower\(\$1\)`).
		WithArgs("User").
		WillReturnRows(sqlmock.NewRows([]string{"id", "password"}).AddRow(userID, hashPassword(t, "password")))
	mock.ExpectExec(`UPDATE users SET refresh_token=\$1 WHERE id=\$2`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	RegisterHandler(w, doRequest(http.MethodPost, "/auth/register", models.User{Login: "  User  ", Password: "password"}))

	assert.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Превышение лимита регистраций с одного IP должно вернуть 429 RATE_LIMITED
// и не создавать пользователя.
func TestRegisterHandler_RateLimited(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(`SELECT id, password FROM users WHERE lower\(trim\(login\)\) = lower\(\$1\)`).
		WithArgs("john").
		WillReturnError(sql.ErrNoRows)
	expectRegistrationAllowed(mock, 4)

	w := httptest.NewRecorder()
	RegisterHandler(w, doRequest(http.MethodPost, "/auth/register", models.User{Login: "john", Password: "password"}))

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	var body openapi.GetErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, openapi.RATELIMITED, body.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Ошибка БД на шаге rate-limit должна fail-closed: 500, регистрация не идёт дальше.
func TestRegisterHandler_RateLimitDBErrorFailsClosed(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(`SELECT id, password FROM users WHERE lower\(trim\(login\)\) = lower\(\$1\)`).
		WithArgs("john").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`INSERT INTO registration_rate_limit`).
		WillReturnError(assertError)

	w := httptest.NewRecorder()
	RegisterHandler(w, doRequest(http.MethodPost, "/auth/register", models.User{Login: "john", Password: "password"}))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Гонка параллельной регистрации: INSERT падает с unique_violation (23505) —
// сервер не должен отвечать 500, а обработать это как "пользователь уже
// существует" (повторный поиск + сверка пароля).
func TestRegisterHandler_ConcurrentRegistrationUniqueViolationFallsBackToLogin(t *testing.T) {
	mock := setupMockDB(t)
	userID := "11111111-1111-1111-1111-111111111111"
	mock.ExpectQuery(`SELECT id, password FROM users WHERE lower\(trim\(login\)\) = lower\(\$1\)`).
		WithArgs("john").
		WillReturnError(sql.ErrNoRows)
	expectRegistrationAllowed(mock, 1)
	mock.ExpectQuery(`INSERT INTO users \(login, password\) VALUES \(\$1, \$2\) RETURNING id`).
		WithArgs("john", sqlmock.AnyArg()).
		WillReturnError(&pq.Error{Code: "23505"})
	mock.ExpectQuery(`SELECT id, password FROM users WHERE lower\(trim\(login\)\) = lower\(\$1\)`).
		WithArgs("john").
		WillReturnRows(sqlmock.NewRows([]string{"id", "password"}).AddRow(userID, hashPassword(t, "password")))
	mock.ExpectExec(`UPDATE users SET refresh_token=\$1 WHERE id=\$2`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	RegisterHandler(w, doRequest(http.MethodPost, "/auth/register", models.User{Login: "john", Password: "password"}))

	assert.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Любая другая ошибка INSERT (не unique_violation) остаётся обычной ошибкой БД (500).
func TestRegisterHandler_InsertNonUniqueViolationErrorStays500(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(`SELECT id, password FROM users WHERE lower\(trim\(login\)\) = lower\(\$1\)`).
		WithArgs("john").
		WillReturnError(sql.ErrNoRows)
	expectRegistrationAllowed(mock, 1)
	mock.ExpectQuery(`INSERT INTO users \(login, password\) VALUES \(\$1, \$2\) RETURNING id`).
		WithArgs("john", sqlmock.AnyArg()).
		WillReturnError(&pq.Error{Code: "23503"})

	w := httptest.NewRecorder()
	RegisterHandler(w, doRequest(http.MethodPost, "/auth/register", models.User{Login: "john", Password: "password"}))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// /auth/login тоже автоматически регистрирует пользователя, если его ещё нет.
func TestLoginHandler_AutoRegistersUnknownUser(t *testing.T) {
	mock := setupMockDB(t)
	newID := "11111111-1111-1111-1111-111111111111"
	mock.ExpectQuery(`SELECT id, password FROM users WHERE lower\(trim\(login\)\) = lower\(\$1\)`).
		WithArgs("john").
		WillReturnError(sql.ErrNoRows)
	expectRegistrationAllowed(mock, 1)
	mock.ExpectQuery(`INSERT INTO users \(login, password\) VALUES \(\$1, \$2\) RETURNING id`).
		WithArgs("john", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(newID))
	mock.ExpectExec(`UPDATE users SET refresh_token=\$1 WHERE id=\$2`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	LoginHandler(w, doRequest(http.MethodPost, "/auth/login", models.User{Login: "john", Password: "password"}))

	assert.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLoginHandler_SaveRefreshTokenError(t *testing.T) {
	mock := setupMockDB(t)
	userID := "11111111-1111-1111-1111-111111111111"
	mock.ExpectQuery(`SELECT id, password FROM users WHERE lower\(trim\(login\)\) = lower\(\$1\)`).
		WithArgs("john").
		WillReturnRows(sqlmock.NewRows([]string{"id", "password"}).AddRow(userID, hashPassword(t, "password")))
	mock.ExpectExec(`UPDATE users SET refresh_token=\$1 WHERE id=\$2`).
		WillReturnError(assertError)

	w := httptest.NewRecorder()
	LoginHandler(w, doRequest(http.MethodPost, "/auth/login", models.User{Login: "john", Password: "password"}))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
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
	mock.ExpectQuery(`SELECT id, password FROM users WHERE lower\(trim\(login\)\) = lower\(\$1\)`).
		WithArgs("john").
		WillReturnRows(sqlmock.NewRows([]string{"id", "password"}).AddRow(userID, hashPassword(t, "correctpw")))

	w := httptest.NewRecorder()
	LoginHandler(w, doRequest(http.MethodPost, "/auth/login", models.User{Login: "john", Password: "wrongpass"}))

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestLoginHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	userID := "11111111-1111-1111-1111-111111111111"
	mock.ExpectQuery(`SELECT id, password FROM users WHERE lower\(trim\(login\)\) = lower\(\$1\)`).
		WithArgs("john").
		WillReturnRows(sqlmock.NewRows([]string{"id", "password"}).AddRow(userID, hashPassword(t, "password")))
	mock.ExpectExec(`UPDATE users SET refresh_token=\$1 WHERE id=\$2`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	LoginHandler(w, doRequest(http.MethodPost, "/auth/login", models.User{Login: "john", Password: "password"}))

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

// Атомарная ротация: если ни один пользователь/токен не совпал (пользователь
// не найден, токен отозван или устарел), UPDATE затрагивает 0 строк — и
// сервер должен вернуть 401, не выполняя отдельный SELECT для различения причин.
func TestRefreshTokenHandler_NoRowsAffectedReturnsUnauthorized(t *testing.T) {
	mock := setupMockDB(t)
	token := validRefreshToken(t, "john", "11111111-1111-1111-1111-111111111111")
	mock.ExpectExec(`UPDATE users SET refresh_token=\$1 WHERE id=\$2 AND refresh_token=\$3`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	w := httptest.NewRecorder()
	RefreshTokenHandler(w, doRequest(http.MethodPost, "/auth/refresh", map[string]string{"refresh_token": token}))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshTokenHandler_RotateDBError(t *testing.T) {
	mock := setupMockDB(t)
	token := validRefreshToken(t, "john", "11111111-1111-1111-1111-111111111111")
	mock.ExpectExec(`UPDATE users SET refresh_token=\$1 WHERE id=\$2 AND refresh_token=\$3`).
		WillReturnError(assertError)

	w := httptest.NewRecorder()
	RefreshTokenHandler(w, doRequest(http.MethodPost, "/auth/refresh", map[string]string{"refresh_token": token}))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshTokenHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	token := validRefreshToken(t, "john", "11111111-1111-1111-1111-111111111111")
	mock.ExpectExec(`UPDATE users SET refresh_token=\$1 WHERE id=\$2 AND refresh_token=\$3`).
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

	var body openapi.GetErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, unauthorizedLogoutMessage, body.Message)
}

// Атомарный compare-and-clear: 0 затронутых строк (пользователь не найден
// ИЛИ токен не совпал) должен вернуть тот же самый унифицированный ответ,
// что и невалидный JWT — клиенту причина не раскрывается.
func TestLogoutHandler_NoRowsAffectedReturnsUnifiedUnauthorized(t *testing.T) {
	mock := setupMockDB(t)
	token := validRefreshToken(t, "john", "11111111-1111-1111-1111-111111111111")
	mock.ExpectExec(`UPDATE users SET refresh_token=NULL, tokens_invalidated_at=now\(\) WHERE id=\$1 AND refresh_token=\$2`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	w := httptest.NewRecorder()
	LogoutHandler(w, doRequest(http.MethodPost, "/auth/logout", map[string]string{"refresh_token": token}))
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var body openapi.GetErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, unauthorizedLogoutMessage, body.Message)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLogoutHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	token := validRefreshToken(t, "john", "11111111-1111-1111-1111-111111111111")
	mock.ExpectExec(`UPDATE users SET refresh_token=NULL, tokens_invalidated_at=now\(\) WHERE id=\$1 AND refresh_token=\$2`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	LogoutHandler(w, doRequest(http.MethodPost, "/auth/logout", map[string]string{"refresh_token": token}))

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestLogoutHandler_ClearError(t *testing.T) {
	mock := setupMockDB(t)
	token := validRefreshToken(t, "john", "11111111-1111-1111-1111-111111111111")
	mock.ExpectExec(`UPDATE users SET refresh_token=NULL, tokens_invalidated_at=now\(\) WHERE id=\$1 AND refresh_token=\$2`).
		WillReturnError(assertError)

	w := httptest.NewRecorder()
	LogoutHandler(w, doRequest(http.MethodPost, "/auth/logout", map[string]string{"refresh_token": token}))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGuestHandler_MethodNotAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	GuestHandler(w, doRequest(http.MethodGet, "/auth/guest", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestGuestHandler_MissingDeviceID(t *testing.T) {
	w := httptest.NewRecorder()
	GuestHandler(w, doRequest(http.MethodPost, "/auth/guest", openapi.GetGuestRequest{DeviceId: "  "}))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var body openapi.GetErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, openapi.VALIDATIONERROR, body.Code)
}

// Новый device_id создаёт нового гостевого пользователя.
func TestGuestHandler_CreatesNewGuestUser(t *testing.T) {
	mock := setupMockDB(t)
	newID := "11111111-1111-1111-1111-111111111111"
	mock.ExpectQuery(`SELECT id, login FROM users WHERE guest_device_id=\$1`).
		WithArgs("device-1").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`INSERT INTO users \(login, password, guest_device_id, is_guest\) VALUES \(\$1, '', \$2, true\) RETURNING id`).
		WithArgs(sqlmock.AnyArg(), "device-1").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(newID))
	mock.ExpectExec(`UPDATE users SET refresh_token=\$1 WHERE id=\$2`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	GuestHandler(w, doRequest(http.MethodPost, "/auth/guest", openapi.GetGuestRequest{DeviceId: "device-1"}))

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.AuthResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.AccessToken)
	assert.NotEmpty(t, resp.RefreshToken)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Повторный вызов с тем же device_id идемпотентен — новая запись не
// создаётся, используется id уже существующего гостевого пользователя.
func TestGuestHandler_ReusesExistingGuestUser(t *testing.T) {
	mock := setupMockDB(t)
	existingID := "22222222-2222-2222-2222-222222222222"
	mock.ExpectQuery(`SELECT id, login FROM users WHERE guest_device_id=\$1`).
		WithArgs("device-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "login"}).AddRow(existingID, "guest_abc123"))
	mock.ExpectExec(`UPDATE users SET refresh_token=\$1 WHERE id=\$2`).
		WithArgs(sqlmock.AnyArg(), existingID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	GuestHandler(w, doRequest(http.MethodPost, "/auth/guest", openapi.GetGuestRequest{DeviceId: "device-1"}))

	assert.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGuestHandler_LookupDBError(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(`SELECT id, login FROM users WHERE guest_device_id=\$1`).
		WithArgs("device-1").
		WillReturnError(assertError)

	w := httptest.NewRecorder()
	GuestHandler(w, doRequest(http.MethodPost, "/auth/guest", openapi.GetGuestRequest{DeviceId: "device-1"}))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}
