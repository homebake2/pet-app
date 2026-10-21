package handlers

import (
	"database/sql"
	"encoding/json"
	"myauthservice/models"
	"myauthservice/openapi"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func serveAccess(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	NewMux().ServeHTTP(w, petRequest(t, method, path, body, true))
	return w
}

func errorCodeFrom(t *testing.T, w *httptest.ResponseRecorder) openapi.ErrorCodeEnum {
	t.Helper()
	var body openapi.GetErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body.Code
}

func TestPetAccess_Unauthorized(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/pet/" + testPetID + "/access-invites"},
		{http.MethodGet, "/pet/" + testPetID + "/access"},
		{http.MethodPut, "/pet/" + testPetID + "/access/" + otherTestUserID},
		{http.MethodDelete, "/pet/" + testPetID + "/access/" + otherTestUserID},
		{http.MethodDelete, "/pet/" + testPetID + "/access/me"},
		{http.MethodDelete, "/pet/" + testPetID + "/access-invites/" + testTransferID},
		{http.MethodPost, "/pet-access-invites/preview"},
		{http.MethodPost, "/pet-access-invites/accept"},
		{http.MethodPost, "/pet-access-invites/decline"},
		{http.MethodDelete, "/pet-access-notices/" + testPetID},
	} {
		w := httptest.NewRecorder()
		NewMux().ServeHTTP(w, doRequest(tc.method, tc.path, map[string]string{"code": testTransferCode}))
		assert.Equal(t, http.StatusUnauthorized, w.Code, tc.method+" "+tc.path)
	}
}

func TestPetAccess_InvalidIDs(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/pet/not-a-uuid/access-invites"},
		{http.MethodGet, "/pet/not-a-uuid/access"},
		{http.MethodPut, "/pet/" + testPetID + "/access/not-a-uuid"},
		{http.MethodDelete, "/pet/" + testPetID + "/access-invites/not-a-uuid"},
		{http.MethodDelete, "/pet-access-notices/not-a-uuid"},
	} {
		w := httptest.NewRecorder()
		NewMux().ServeHTTP(w, doRequest(tc.method, tc.path, nil))
		assert.Equal(t, http.StatusBadRequest, w.Code, tc.method+" "+tc.path)
	}
}

// Участник (любая роль) не выполняет операции владельца: 403, а не 404.
func TestPetAccess_OwnerOnlyEndpointsForbiddenForMember(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/pet/" + testPetID + "/access-invites", map[string]string{"role": "view"}},
		{http.MethodGet, "/pet/" + testPetID + "/access", nil},
		{http.MethodPut, "/pet/" + testPetID + "/access/" + otherTestUserID, map[string]string{"role": "full"}},
		{http.MethodDelete, "/pet/" + testPetID + "/access/" + otherTestUserID, nil},
		{http.MethodDelete, "/pet/" + testPetID + "/access-invites/" + testTransferID, nil},
		{http.MethodPost, "/pet/" + testPetID + "/transfer", nil},
		{http.MethodGet, "/pet/" + testPetID + "/transfer", nil},
		{http.MethodDelete, "/pet/" + testPetID + "/transfer", nil},
		{http.MethodDelete, "/pet/" + testPetID, nil},
	} {
		for _, role := range []string{"full", "view"} {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			expectPetRole(mock, role)

			w := serveAccess(t, tc.method, tc.path, tc.body)

			assert.Equal(t, http.StatusForbidden, w.Code, tc.method+" "+tc.path+" "+role)
			assert.Equal(t, openapi.FORBIDDEN, errorCodeFrom(t, w))
			require.NoError(t, mock.ExpectationsWereMet())
		}
	}
}

// Чужой питомец без доступа неотличим от несуществующего.
func TestPetAccess_OwnerOnlyEndpointsNotFoundWithoutAccess(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetRoleMissing(mock)

	w := serveAccess(t, http.MethodGet, "/pet/"+testPetID+"/access", nil)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestPetAccess_CreateInviteValidation(t *testing.T) {
	for name, body := range map[string]any{
		"роль owner":          map[string]any{"role": "owner"},
		"нет роли":            map[string]any{},
		"pet_ids без питомца": map[string]any{"role": "view", "pet_ids": []string{testPetID2}},
		"дубликаты":           map[string]any{"role": "view", "pet_ids": []string{testPetID, testPetID}},
		"не uuid":             map[string]any{"role": "view", "pet_ids": []string{"x"}},
	} {
		mock := setupMockDB(t)
		expectTokensValid(mock, testUserID)
		expectPetRole(mock, "owner")

		w := serveAccess(t, http.MethodPost, "/pet/"+testPetID+"/access-invites", body)

		assert.Equal(t, http.StatusBadRequest, w.Code, name)
		assert.Equal(t, openapi.VALIDATIONERROR, errorCodeFrom(t, w), name)
	}
}

func TestPetAccess_LeaveByOwnerForbidden(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetRole(mock, "owner")

	w := serveAccess(t, http.MethodDelete, "/pet/"+testPetID+"/access/me", nil)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestPetAccess_LeaveByMember(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetRole(mock, "view")
	mock.ExpectExec(`DELETE FROM pet_access WHERE pet_id = \$1 AND user_id = \$2`).
		WithArgs(testPetID, testUserID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := serveAccess(t, http.MethodDelete, "/pet/"+testPetID+"/access/me", nil)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPetAccess_RevokeCreatesNotice(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetRole(mock, "owner")
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM pet_access WHERE pet_id = \$1 AND user_id = \$2`).
		WithArgs(testPetID, otherTestUserID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO pet_access_notice`).
		WithArgs(testPetID, otherTestUserID, "revoked").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	w := serveAccess(t, http.MethodDelete, "/pet/"+testPetID+"/access/"+otherTestUserID, nil)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPetAccess_RevokeUnknownMember(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetRole(mock, "owner")
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM pet_access WHERE pet_id = \$1 AND user_id = \$2`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	w := serveAccess(t, http.MethodDelete, "/pet/"+testPetID+"/access/"+otherTestUserID, nil)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestPetAccess_UpdateRole(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetRole(mock, "owner")
	mock.ExpectExec(`UPDATE pet_access SET role = \$3, updated_at = now\(\) WHERE pet_id = \$1 AND user_id = \$2`).
		WithArgs(testPetID, otherTestUserID, "full").
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := serveAccess(t, http.MethodPut, "/pet/"+testPetID+"/access/"+otherTestUserID, map[string]string{"role": "full"})

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPetAccess_UpdateRoleInvalid(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetRole(mock, "owner")

	w := serveAccess(t, http.MethodPut, "/pet/"+testPetID+"/access/"+otherTestUserID, map[string]string{"role": "owner"})

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPetAccessNotice_DeleteIsIdempotent(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectExec(`DELETE FROM pet_access_notice WHERE user_id = \$1 AND pet_id = \$2`).
		WithArgs(testUserID, testPetID).
		WillReturnResult(sqlmock.NewResult(0, 0))

	w := serveAccess(t, http.MethodDelete, "/pet-access-notices/"+testPetID, nil)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Подбор кода приглашения ограничен общим со передачами счётчиком.
func TestPetAccessInvite_CodeRateLimited(t *testing.T) {
	for _, path := range []string{"/pet-access-invites/preview", "/pet-access-invites/accept", "/pet-access-invites/decline"} {
		mock := setupMockDB(t)
		expectTokensValid(mock, testUserID)
		expectNoCodeFailures(mock, 10)

		w := serveAccess(t, http.MethodPost, path, map[string]string{"code": testTransferCode})

		assert.Equal(t, http.StatusTooManyRequests, w.Code, path)
		assert.Equal(t, openapi.RATELIMITED, errorCodeFrom(t, w))
		require.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestPetAccessInvite_InvalidCodeFormat(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := serveAccess(t, http.MethodPost, "/pet-access-invites/preview", map[string]string{"code": "bad"})

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, openapi.VALIDATIONERROR, errorCodeFrom(t, w))
}

// Недействительный код: единый 404 и учёт неудачной попытки.
func TestPetAccessInvite_UnknownCodeRecordsFailure(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectNoCodeFailures(mock, 0)
	mock.ExpectExec(`UPDATE pet_access_invite SET status = 'expired'`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT id, from_user_id::text, role, expires_at FROM pet_access_invite`).
		WithArgs(testTransferCode).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(`WITH purge AS`).WillReturnResult(sqlmock.NewResult(0, 1))

	w := serveAccess(t, http.MethodPost, "/pet-access-invites/preview", map[string]string{"code": testTransferCode})

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Роль view: профиль питомца изменять нельзя, запись события и удаление —
// тоже; чтение остаётся доступным.
func TestPetAccess_ViewRoleCannotWritePet(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "view", true, testUserID, "Owner"))

	expectNoWeightEvent(mock)

	w := serveAccess(t, http.MethodPut, "/pet/"+testPetID, map[string]any{"name": "New"})

	assert.Equal(t, http.StatusForbidden, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPetAccess_FullRoleCannotDeletePetViaUpdate(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "full", true, testUserID, "Owner"))
	expectNoWeightEvent(mock)

	w := serveAccess(t, http.MethodPut, "/pet/"+testPetID, map[string]any{"is_deleted": true})

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestPetAccess_GetPetReturnsAccessAndShared(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "view", true, testUserID, "Owner"))
	expectNoWeightEvent(mock)
	expectNoPetPhoto(mock)

	w := serveAccess(t, http.MethodGet, "/pet/"+testPetID, nil)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var pet models.PetIdResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &pet))
	assert.Equal(t, "view", pet.Access)
	assert.True(t, pet.Shared)
	assert.Equal(t, models.PetOwnerRef{ID: testUserID, Name: "Owner"}, pet.Owner)
}

// Запись события с питомцем, доступным только для просмотра, — 403; с
// питомцами разных владельцев — 400.
func TestPetAccess_CreateEventPetSetRules(t *testing.T) {
	viewPet := petRex
	viewPet.Role = "view"
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPets(mock, viewPet)
	w := postEvent(t, createBody(petIDsOfTest(petRex), "weight", `{"amount":5}`))
	assert.Equal(t, http.StatusForbidden, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())

	foreign := petTom
	foreign.OwnerID = otherTestUserID
	foreign.Role = "full"
	mock = setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPets(mock, petRex, foreign)
	w = postEvent(t, createBody(petIDsOfTest(petRex, foreign), "feeding", `{"amount":5,"unit":"g","food":"dry"}`))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, openapi.VALIDATIONERROR, errorCodeFrom(t, w))
}

func TestPetAccess_UpdateAndDeleteEventForbiddenForView(t *testing.T) {
	viewPet := petRex
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectEventForUser(mock, testEventID, "weight", `{"amount":5}`, viewPet)
	expectWritable(mock, false)
	notes := "x"
	w := patchEvent(t, models.UpdateEventRequest{Notes: &notes})
	assert.Equal(t, http.StatusForbidden, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())

	mock = setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(eventForDeleteQuery).WillReturnRows(eventRow(testEventID, "weight", `{"amount":5}`, time.Now().Add(-time.Hour)))
	expectEventPets(mock, testEventID, viewPet)
	expectWritable(mock, false)
	w = httptest.NewRecorder()
	DeleteEventHandler(w, eventRequest(t, http.MethodDelete, "/events/"+testEventID, nil, true))
	assert.Equal(t, http.StatusForbidden, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}
