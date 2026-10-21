package handlers

import (
	"database/sql"
	"encoding/json"
	"myauthservice/openapi"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testTransferID   = "77777777-7777-4777-8777-777777777777"
	testTransferCode = "ABCDE23456"
	otherTestUserID  = "44444444-4444-4444-8444-444444444444"
)

func transferRequest(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	return petRequest(t, method, path, body, true)
}

func serveTransfer(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	mux := NewMux()
	mux.ServeHTTP(w, transferRequest(t, method, path, body))
	return w
}

func expectNoCodeFailures(mock sqlmock.Sqlmock, count int) {
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM pet_transfer_code_failure`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(count))
}

func expectExpireByCode(mock sqlmock.Sqlmock) {
	mock.ExpectExec(`UPDATE pet_transfer SET status = 'expired', resolved_at = now\(\)\s+WHERE code = \$1`).
		WithArgs(testTransferCode).
		WillReturnResult(sqlmock.NewResult(0, 0))
}

var pendingTransferColumns = []string{"id", "pet_id", "from_user_id", "expires_at"}

func TestNormalizePetTransferCode(t *testing.T) {
	cases := []struct {
		input string
		want  string
		ok    bool
	}{
		{"ABCDE23456", "ABCDE23456", true},
		{"abcde-23456", "ABCDE23456", true},
		{"  abcde 23456 ", "ABCDE23456", true},
		{"ABCDE2345", "", false},
		{"ABCDE234567", "", false},
		{"ABCDE2345O", "", false},
		{"ABCDE2345I", "", false},
		{"ABCDE2345L", "", false},
		{"ABCDE2345" + "0", "", false},
		{"ABCDE2345" + "1", "", false},
		{"", "", false},
		{"----", "", false},
	}
	for _, tc := range cases {
		got, ok := normalizePetTransferCode(tc.input)
		assert.Equal(t, tc.ok, ok, tc.input)
		assert.Equal(t, tc.want, got, tc.input)
	}
}

func TestGeneratePetTransferCode(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		code, err := generatePetTransferCode()
		require.NoError(t, err)
		require.Len(t, code, petTransferCodeLength)
		for _, r := range code {
			require.Truef(t, strings.ContainsRune(petTransferCodeAlphabet, r), "символ %q вне алфавита", r)
		}
		normalized, ok := normalizePetTransferCode(code)
		require.True(t, ok)
		require.Equal(t, code, normalized)
		seen[code] = true
	}
	assert.Greater(t, len(seen), 190)
	assert.NotContains(t, petTransferCodeAlphabet, "0")
	assert.NotContains(t, petTransferCodeAlphabet, "O")
	assert.NotContains(t, petTransferCodeAlphabet, "1")
	assert.NotContains(t, petTransferCodeAlphabet, "I")
	assert.NotContains(t, petTransferCodeAlphabet, "L")
}

func TestPetTransfer_Unauthorized(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/pet/" + testPetID + "/transfer"},
		{http.MethodGet, "/pet/" + testPetID + "/transfer"},
		{http.MethodDelete, "/pet/" + testPetID + "/transfer"},
		{http.MethodPost, "/pet-transfers/preview"},
		{http.MethodPost, "/pet-transfers/accept"},
		{http.MethodPost, "/pet-transfers/decline"},
	} {
		w := httptest.NewRecorder()
		NewMux().ServeHTTP(w, doRequest(tc.method, tc.path, map[string]string{"code": testTransferCode}))
		assert.Equal(t, http.StatusUnauthorized, w.Code, tc.method+" "+tc.path)
	}
}

func TestPetTransfer_InvalidPetID(t *testing.T) {
	w := httptest.NewRecorder()
	NewMux().ServeHTTP(w, doRequest(http.MethodPost, "/pet/not-a-uuid/transfer", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPetTransfer_MethodNotAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	NewMux().ServeHTTP(w, doRequest(http.MethodPut, "/pet/"+testPetID+"/transfer", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)

	w = httptest.NewRecorder()
	NewMux().ServeHTTP(w, doRequest(http.MethodGet, "/pet-transfers/accept", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestCreatePetTransfer_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetRole(mock, "owner")
	expiresAt := time.Now().UTC().Add(7 * 24 * time.Hour).Truncate(time.Second)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE pet_transfer SET status = 'expired'`).
		WithArgs(testPetID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT name, species FROM pet\s+WHERE id = \$1 AND user_id = \$2 AND deleted_at IS NULL\s+FOR UPDATE`).
		WithArgs(testPetID, testUserID).
		WillReturnRows(sqlmock.NewRows([]string{"name", "species"}).AddRow("Rex", "dog"))
	mock.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM pet_transfer WHERE pet_id = \$1 AND status = 'pending'\)`).
		WithArgs(testPetID).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM pet_access_invite WHERE code`).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(`INSERT INTO pet_transfer`).
		WithArgs(testPetID, "Rex", "dog", testUserID, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "code", "expires_at"}).AddRow(testTransferID, testTransferCode, expiresAt))
	mock.ExpectCommit()

	w := serveTransfer(t, http.MethodPost, "/pet/"+testPetID+"/transfer", nil)

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var body openapi.PetTransferResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, testTransferID, body.Id.String())
	assert.Equal(t, testTransferCode, body.Code)
	assert.True(t, body.ExpiresAt.Equal(expiresAt))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreatePetTransfer_PetNotFound(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetRole(mock, "owner")
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE pet_transfer SET status = 'expired'`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT name, species FROM pet`).WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	w := serveTransfer(t, http.MethodPost, "/pet/"+testPetID+"/transfer", nil)

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreatePetTransfer_AlreadyPending(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetRole(mock, "owner")
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE pet_transfer SET status = 'expired'`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT name, species FROM pet`).
		WillReturnRows(sqlmock.NewRows([]string{"name", "species"}).AddRow("Rex", "dog"))
	mock.ExpectQuery(`SELECT EXISTS`).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()

	w := serveTransfer(t, http.MethodPost, "/pet/"+testPetID+"/transfer", nil)

	require.Equal(t, http.StatusConflict, w.Code)
	var body openapi.GetErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, openapi.TRANSFERALREADYPENDING, body.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPetTransfer_NotFound(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetRole(mock, "owner")
	mock.ExpectExec(`UPDATE pet_transfer SET status = 'expired'`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT t\.id, t\.code, t\.expires_at\s+FROM pet_transfer t`).WillReturnError(sql.ErrNoRows)

	w := serveTransfer(t, http.MethodGet, "/pet/"+testPetID+"/transfer", nil)

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPetTransfer_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetRole(mock, "owner")
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	mock.ExpectExec(`UPDATE pet_transfer SET status = 'expired'`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT t\.id, t\.code, t\.expires_at\s+FROM pet_transfer t`).
		WithArgs(testPetID, testUserID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "code", "expires_at"}).AddRow(testTransferID, testTransferCode, expiresAt))

	w := serveTransfer(t, http.MethodGet, "/pet/"+testPetID+"/transfer", nil)

	require.Equal(t, http.StatusOK, w.Code)
	var body openapi.PetTransferResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, testTransferCode, body.Code)
}

func TestCancelPetTransfer(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mock := setupMockDB(t)
		expectTokensValid(mock, testUserID)
		expectPetRole(mock, "owner")
		mock.ExpectExec(`UPDATE pet_transfer SET status = 'expired'`).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(`UPDATE pet_transfer t SET status = 'cancelled'`).
			WithArgs(testPetID, testUserID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		w := serveTransfer(t, http.MethodDelete, "/pet/"+testPetID+"/transfer", nil)

		assert.Equal(t, http.StatusNoContent, w.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no active transfer", func(t *testing.T) {
		mock := setupMockDB(t)
		expectTokensValid(mock, testUserID)
		expectPetRole(mock, "owner")
		mock.ExpectExec(`UPDATE pet_transfer SET status = 'expired'`).WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(`UPDATE pet_transfer t SET status = 'cancelled'`).WillReturnResult(sqlmock.NewResult(0, 0))

		w := serveTransfer(t, http.MethodDelete, "/pet/"+testPetID+"/transfer", nil)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

func TestPetTransferCode_ValidationError(t *testing.T) {
	for _, path := range []string{"/pet-transfers/preview", "/pet-transfers/accept", "/pet-transfers/decline"} {
		for _, code := range []string{"", "short", "ABCDE2345O"} {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)

			w := serveTransfer(t, http.MethodPost, path, map[string]string{"code": code})

			assert.Equal(t, http.StatusBadRequest, w.Code, path+" "+code)
			require.NoError(t, mock.ExpectationsWereMet())
		}
	}
}

func TestPetTransferCode_RateLimited(t *testing.T) {
	for _, path := range []string{"/pet-transfers/preview", "/pet-transfers/accept", "/pet-transfers/decline"} {
		mock := setupMockDB(t)
		expectTokensValid(mock, testUserID)
		expectNoCodeFailures(mock, 10)

		w := serveTransfer(t, http.MethodPost, path, map[string]string{"code": testTransferCode})

		require.Equal(t, http.StatusTooManyRequests, w.Code, path)
		var body openapi.GetErrorResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		assert.Equal(t, openapi.RATELIMITED, body.Code)
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestPetTransferPreview_InvalidCodeRecordsFailure(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectNoCodeFailures(mock, 9)
	expectExpireByCode(mock)
	mock.ExpectQuery(`SELECT t\.id, t\.pet_id, t\.from_user_id, t\.expires_at`).WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(`WITH purge AS \(\s+DELETE FROM pet_transfer_code_failure`).
		WithArgs(testUserID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Код вводится в «человеческом» виде: нормализуется сервером.
	w := serveTransfer(t, http.MethodPost, "/pet-transfers/preview", map[string]string{"code": "abcde-23456"})

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPetTransferPreview_OwnCode(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectNoCodeFailures(mock, 0)
	expectExpireByCode(mock)
	mock.ExpectQuery(`SELECT t\.id, t\.pet_id, t\.from_user_id, t\.expires_at`).
		WithArgs(testTransferCode).
		WillReturnRows(sqlmock.NewRows(pendingTransferColumns).AddRow(testTransferID, testPetID, testUserID, time.Now().Add(time.Hour)))

	w := serveTransfer(t, http.MethodPost, "/pet-transfers/preview", map[string]string{"code": testTransferCode})

	require.Equal(t, http.StatusConflict, w.Code)
	var body openapi.GetErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, openapi.TRANSFEROWNPET, body.Code)
	// Собственный код не считается неудачной попыткой подбора.
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPetTransferPreview_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectNoCodeFailures(mock, 0)
	expectExpireByCode(mock)
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	mock.ExpectQuery(`SELECT t\.id, t\.pet_id, t\.from_user_id, t\.expires_at`).
		WithArgs(testTransferCode).
		WillReturnRows(sqlmock.NewRows(pendingTransferColumns).AddRow(testTransferID, testPetID, otherTestUserID, expiresAt))
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WithArgs(testPetID, otherTestUserID).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", "male", "dog", time.Date(2020, 5, 17, 0, 0, 0, 0, time.UTC), nil, false, nil, nil, nil, "Corgi", nil, nil, nil, nil, nil, nil, nil, nil, nil, "owner", false, testUserID, "Owner"))
	expectNoWeightEvent(mock)
	mock.ExpectQuery(`SELECT COALESCE\(NULLIF\(trim\(pr\.first_name\), ''\), u\.login\)`).
		WithArgs(otherTestUserID).
		WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("Анна"))
	expectNoPetPhoto(mock)

	w := serveTransfer(t, http.MethodPost, "/pet-transfers/preview", map[string]string{"code": testTransferCode})

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "Анна", body["sender_name"])
	pet := body["pet"].(map[string]any)
	assert.Equal(t, "Rex", pet["name"])
	assert.Equal(t, "dog", pet["species"])
	assert.Equal(t, "Corgi", pet["breed"])
	assert.Equal(t, "male", pet["gender"])
	assert.Equal(t, "2020-05-17", pet["birth_date"])
	assert.Nil(t, pet["photo_url"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPetTransferDecline_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectNoCodeFailures(mock, 0)
	mock.ExpectBegin()
	expectExpireByCode(mock)
	mock.ExpectQuery(`SELECT t\.id, t\.pet_id, t\.from_user_id, t\.expires_at[\s\S]+FOR UPDATE OF t, p`).
		WillReturnRows(sqlmock.NewRows(pendingTransferColumns).AddRow(testTransferID, testPetID, otherTestUserID, time.Now().Add(time.Hour)))
	mock.ExpectExec(`UPDATE pet_transfer SET status = 'declined', to_user_id = \$2`).
		WithArgs(testTransferID, testUserID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	w := serveTransfer(t, http.MethodPost, "/pet-transfers/decline", map[string]string{"code": testTransferCode})

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPetTransferDecline_OwnCode(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectNoCodeFailures(mock, 0)
	mock.ExpectBegin()
	expectExpireByCode(mock)
	mock.ExpectQuery(`SELECT t\.id, t\.pet_id, t\.from_user_id, t\.expires_at`).
		WillReturnRows(sqlmock.NewRows(pendingTransferColumns).AddRow(testTransferID, testPetID, testUserID, time.Now().Add(time.Hour)))
	mock.ExpectRollback()

	w := serveTransfer(t, http.MethodPost, "/pet-transfers/decline", map[string]string{"code": testTransferCode})

	assert.Equal(t, http.StatusConflict, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPetTransferAccept_NotFound(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectNoCodeFailures(mock, 0)
	mock.ExpectBegin()
	expectExpireByCode(mock)
	mock.ExpectQuery(`SELECT t\.id, t\.pet_id, t\.from_user_id, t\.expires_at`).WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()
	mock.ExpectExec(`WITH purge AS`).WillReturnResult(sqlmock.NewResult(0, 1))

	w := serveTransfer(t, http.MethodPost, "/pet-transfers/accept", map[string]string{"code": testTransferCode})

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPetTransferAccept_SuccessWithoutEvents(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectNoCodeFailures(mock, 0)
	mock.ExpectBegin()
	expectExpireByCode(mock)
	mock.ExpectQuery(`SELECT t\.id, t\.pet_id, t\.from_user_id, t\.expires_at[\s\S]+FOR UPDATE OF t, p`).
		WillReturnRows(sqlmock.NewRows(pendingTransferColumns).AddRow(testTransferID, testPetID, otherTestUserID, time.Now().Add(time.Hour)))
	mock.ExpectExec(`UPDATE pet SET user_id = \$1 WHERE id = \$2`).
		WithArgs(testUserID, testPetID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectEndPetAccessOnTransfer(mock)
	mock.ExpectQuery(`SELECT e\.id, e\.deleted_at IS NOT NULL`).
		WithArgs(testPetID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "deleted", "other"}))
	mock.ExpectQuery(`DELETE FROM reminder_plan_pet WHERE pet_id = \$1 RETURNING plan_id`).
		WithArgs(testPetID).
		WillReturnRows(sqlmock.NewRows([]string{"plan_id"}))
	// Без событий их файлы не обновляются; файлы питомца и записей Ветпаспорта
	// передаются одним запросом.
	mock.ExpectExec(`UPDATE file f SET user_id = \$1\s+FROM \(SELECT 'pet_photo'::text AS owner_type, \$2::uuid AS owner_id[\s\S]+'vaccination_file'[\s\S]+'medication_file'`).
		WithArgs(testUserID, testPetID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	for _, table := range []string{"vaccination", "disease", "vet_visit", "allergy", "medication"} {
		mock.ExpectExec(`UPDATE `+table+` SET user_id = \$1 WHERE pet_id = \$2`).
			WithArgs(testUserID, testPetID).
			WillReturnResult(sqlmock.NewResult(0, 0))
	}
	mock.ExpectExec(`UPDATE pet_transfer SET status = 'accepted', to_user_id = \$2`).
		WithArgs(testTransferID, testUserID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	w := serveTransfer(t, http.MethodPost, "/pet-transfers/accept", map[string]string{"code": testTransferCode})

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body openapi.PetTransferAcceptResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, testPetID, body.PetId.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdatePetHandler_SoftDeleteCancelsTransfer(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "owner", false, testUserID, "Owner"))
	expectNoWeightEvent(mock)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE pet_transfer SET status = 'cancelled'`).
		WithArgs(testPetID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE pet\s+SET deleted_at`).WillReturnResult(sqlmock.NewResult(0, 1))
	expectEndPetAccessOnDelete(mock)
	mock.ExpectCommit()

	w := serveTransfer(t, http.MethodPut, "/pet/"+testPetID, map[string]any{"is_deleted": true})

	assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}
