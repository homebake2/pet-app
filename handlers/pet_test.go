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

const testUserID = "22222222-2222-2222-2222-222222222222"
const testPetID = "33333333-3333-3333-3333-333333333333"

func petRequest(t *testing.T, method, path string, body any, authed bool) *http.Request {
	t.Helper()
	r := doRequest(method, path, body)
	if authed {
		r.Header.Set("Authorization", "Bearer "+validAccessToken(t, testUserID))
	}
	return r
}

var petColumns = []string{
	"id", "name", "gender", "species", "birth_date", "color",
	"sterilized", "habitation", "notes", "deleted_at", "breed", "icon",
}

func TestPetHandler_MethodNotAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	PetHandler(w, doRequest(http.MethodDelete, "/pet", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestCreatePetHandler_Unauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	CreatePetHandler(w, doRequest(http.MethodPost, "/pet", models.CreatePetRequest{Name: "Rex", Species: "dog"}))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestCreatePetHandler_MissingFields(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{Name: "", Species: ""}, true)
	CreatePetHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreatePetHandler_InvalidIcon(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	badIcon := "NOT_AN_ICON"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{Name: "Rex", Species: "dog", Icon: &badIcon}, true)
	CreatePetHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreatePetHandler_InvalidGender(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	badGender := "not-a-gender"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{Name: "Rex", Species: "dog", Gender: &badGender}, true)
	CreatePetHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreatePetHandler_InvalidHabitation(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	badHabitation := "not-a-habitation"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{Name: "Rex", Species: "dog", Habitation: &badHabitation}, true)
	CreatePetHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreatePetHandler_InvalidBirthDate(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	badDate := "not-a-date"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{Name: "Rex", Species: "dog", BirthDate: &badDate}, true)
	CreatePetHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreatePetHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`INSERT INTO pet`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testPetID))
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, "DOG",
		))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{Name: "Rex", Species: "dog"}, true)
	CreatePetHandler(w, r)

	assert.Equal(t, http.StatusCreated, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAllPetHandler_WithoutLanguageCode(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, breed, species, icon FROM pet`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "breed", "species", "icon"}).
			AddRow(testPetID, "Rex", "Labrador", "dog", "DOG"))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet", nil, true)
	GetAllPetHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAllPetHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, breed, species, icon FROM pet`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "breed", "species", "icon"}).
			AddRow(testPetID, "Rex", "Labrador", "dog", "DOG"))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet", nil, true)
	r.Header.Set("LanguageCode", "ru")
	GetAllPetHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.PetResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
	assert.Equal(t, "Rex", resp.Items[0].Name)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPetHandler_InvalidID(t *testing.T) {
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/not-a-uuid", nil, true)
	r.Header.Set("LanguageCode", "en")
	GetPetHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetPetHandler_NotFound(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnError(sql.ErrNoRows)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/"+testPetID, nil, true)
	r.Header.Set("LanguageCode", "en")
	GetPetHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetPetHandler_SoftDeletedHiddenViaQuery(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized, habitation, notes, deleted_at, breed, icon\s+FROM pet\s+WHERE id = \$1 AND user_id = \$2 AND deleted_at IS NULL`).
		WillReturnError(sql.ErrNoRows)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/"+testPetID, nil, true)
	GetPetHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPetHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", "male", "dog", nil, nil, true, nil, nil, nil, nil, "DOG",
		))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/"+testPetID, nil, true)
	r.Header.Set("LanguageCode", "en")
	GetPetHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestUpdatePetHandler_EmptyNameRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, "DOG",
		))

	empty := ""
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, models.UpdatePetRequest{Name: &empty}, true)
	UpdatePetHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdatePetHandler_PartialUpdate(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, "DOG",
		))
	mock.ExpectExec(`UPDATE pet SET`).WillReturnResult(sqlmock.NewResult(0, 1))

	notes := "Заметка без изменения имени и вида"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, models.UpdatePetRequest{Notes: &notes}, true)
	UpdatePetHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdatePetHandler_NotFound(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnError(sql.ErrNoRows)

	name := "Rex"
	species := "dog"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, models.UpdatePetRequest{Name: &name, Species: &species}, true)
	UpdatePetHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdatePetHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, "DOG",
		))
	mock.ExpectExec(`UPDATE pet SET`).WillReturnResult(sqlmock.NewResult(0, 1))

	name := "Rexy"
	species := "dog"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, models.UpdatePetRequest{Name: &name, Species: &species}, true)
	UpdatePetHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeletePetHandler_NotFound(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnError(sql.ErrNoRows)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodDelete, "/pet/"+testPetID, nil, true)
	DeletePetHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestDeletePetHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, "DOG",
		))
	mock.ExpectExec(`UPDATE pet SET deleted_at`).WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodDelete, "/pet/"+testPetID, nil, true)
	DeletePetHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPetByIDHandler_RoutesToEvents(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT COUNT\(1\) FROM pet WHERE id = \$1 AND user_id = \$2`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT id, pet_id, date_time, type, notes, value\s+FROM event\s+WHERE pet_id = \$1\s+ORDER BY date_time`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value"}))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/"+testPetID+"/events", nil, true)
	PetByIDHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp PetEventsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.Items)
}

func TestPetByIDHandler_EventsWrongMethod(t *testing.T) {
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/events", nil, true)
	PetByIDHandler(w, r)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestPetByIDHandler_EventsInvalidID(t *testing.T) {
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/not-a-uuid/events", nil, true)
	PetByIDHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetPetEventsHandler_PetNotOwned(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT COUNT\(1\) FROM pet WHERE id = \$1 AND user_id = \$2`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/"+testPetID+"/events", nil, true)
	PetByIDHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
