package handlers

import (
	"database/sql"
	"encoding/json"
	"myauthservice/models"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

var fileColumns = []string{
	"id", "owner_type", "owner_id", "user_id", "object_key", "content_type", "position", "confirmed_at", "created_at",
}

// expectNoPetPhoto mocks the file-table lookup that GetPetHandler/GetAllPetHandler
// perform to fill photo_url/photo_file_id, reporting no confirmed photo.
func expectNoPetPhoto(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT id, owner_type, owner_id, user_id, object_key, content_type, position, confirmed_at, created_at\s+FROM file\s+WHERE owner_type = \$1 AND owner_id = \$2 AND confirmed_at IS NOT NULL`).
		WillReturnError(sql.ErrNoRows)
}

// expectNoPetPhotos mocks the batched file-table lookup GetAllPetHandler
// performs for the pet list, reporting no confirmed photos for any pet.
func expectNoPetPhotos(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT id, owner_type, owner_id, user_id, object_key, content_type, position, confirmed_at, created_at\s+FROM file\s+WHERE owner_type = \$1 AND owner_id = ANY\(\$2\) AND confirmed_at IS NOT NULL`).
		WillReturnRows(sqlmock.NewRows(fileColumns))
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

func TestCreatePetHandler_BirthDateTooOldRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	tooOld := "1400-01-01"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{Name: "Rex", Species: "dog", BirthDate: &tooOld}, true)
	CreatePetHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreatePetHandler_NameTooLongRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	longName := strings.Repeat("a", 101)
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{Name: longName, Species: "dog"}, true)
	CreatePetHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreatePetHandler_NotesTooLongRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	longNotes := strings.Repeat("a", 1001)
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{Name: "Rex", Species: "dog", Notes: &longNotes}, true)
	CreatePetHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreatePetHandler_BreedTooLongRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	longBreed := strings.Repeat("a", 101)
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{Name: "Rex", Species: "dog", Breed: &longBreed}, true)
	CreatePetHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreatePetHandler_ColorTooLongRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	longColor := strings.Repeat("a", 101)
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{Name: "Rex", Species: "dog", Color: &longColor}, true)
	CreatePetHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreatePetHandler_InvalidIdempotencyKeyRejected(t *testing.T) {
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{Name: "Rex", Species: "dog"}, false)
	r.Header.Set("Idempotency-Key", "not-a-uuid")
	CreatePetHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreatePetHandler_IdempotencyKeyReplaysExistingPet(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	idempotencyKey := "44444444-4444-4444-4444-444444444444"

	mock.ExpectExec(`INSERT INTO pet_idempotency_key`).
		WithArgs(testUserID, idempotencyKey).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT pet_id FROM pet_idempotency_key`).
		WithArgs(testUserID, idempotencyKey).
		WillReturnRows(sqlmock.NewRows([]string{"pet_id"}).AddRow(testPetID))
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, "DOG",
		))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{Name: "Rex", Species: "dog"}, true)
	r.Header.Set("Idempotency-Key", idempotencyKey)
	CreatePetHandler(w, r)

	assert.Equal(t, http.StatusCreated, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
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
	expectNoPetPhotos(mock)

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
	expectNoPetPhotos(mock)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet", nil, true)
	r.Header.Set("LanguageCode", "ru")
	GetAllPetHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.PetResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
	assert.Equal(t, "Rex", resp.Items[0].Name)
	assert.Nil(t, resp.Items[0].PhotoURL)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetAllPetHandler_WithPhoto verifies photo_url is populated for a pet
// with a confirmed pet_photo file (batched lookup, see «Фотография питомца
// — Backend», раздел «Чтение»).
func TestGetAllPetHandler_WithPhoto(t *testing.T) {
	mock := setupMockDB(t)
	setupFakeStorage(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, breed, species, icon FROM pet`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "breed", "species", "icon"}).
			AddRow(testPetID, "Rex", "Labrador", "dog", "DOG"))
	fileID := "55555555-5555-5555-5555-555555555555"
	mock.ExpectQuery(`SELECT id, owner_type, owner_id, user_id, object_key, content_type, position, confirmed_at, created_at\s+FROM file\s+WHERE owner_type = \$1 AND owner_id = ANY\(\$2\) AND confirmed_at IS NOT NULL`).
		WillReturnRows(sqlmock.NewRows(fileColumns).AddRow(
			fileID, "pet_photo", testPetID, testUserID, "pet_photo/"+testPetID+"/"+fileID, "image/jpeg", nil, time.Now(), time.Now(),
		))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet", nil, true)
	GetAllPetHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.PetResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
	require.NotNil(t, resp.Items[0].PhotoURL)
	assert.Equal(t, fakePresignedGetURL, *resp.Items[0].PhotoURL)
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
	expectNoPetPhoto(mock)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/"+testPetID, nil, true)
	r.Header.Set("LanguageCode", "en")
	GetPetHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.PetIdResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Nil(t, resp.PhotoURL)
	assert.Nil(t, resp.PhotoFileID)
}

// TestGetPetHandler_WithPhoto verifies photo_url/photo_file_id are populated
// for a pet with a confirmed pet_photo file (see «Фотография питомца —
// Backend», раздел «Чтение»).
func TestGetPetHandler_WithPhoto(t *testing.T) {
	mock := setupMockDB(t)
	setupFakeStorage(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", "male", "dog", nil, nil, true, nil, nil, nil, nil, "DOG",
		))
	fileID := "55555555-5555-5555-5555-555555555555"
	mock.ExpectQuery(`SELECT id, owner_type, owner_id, user_id, object_key, content_type, position, confirmed_at, created_at\s+FROM file\s+WHERE owner_type = \$1 AND owner_id = \$2 AND confirmed_at IS NOT NULL`).
		WillReturnRows(sqlmock.NewRows(fileColumns).AddRow(
			fileID, "pet_photo", testPetID, testUserID, "pet_photo/"+testPetID+"/"+fileID, "image/jpeg", nil, time.Now(), time.Now(),
		))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/"+testPetID, nil, true)
	GetPetHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.PetIdResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.PhotoURL)
	assert.Equal(t, fakePresignedGetURL, *resp.PhotoURL)
	require.NotNil(t, resp.PhotoFileID)
	assert.Equal(t, fileID, *resp.PhotoFileID)
	require.NoError(t, mock.ExpectationsWereMet())
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

func TestUpdatePetHandler_NameTooLongRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, "DOG",
		))

	longName := strings.Repeat("a", 101)
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, models.UpdatePetRequest{Name: &longName}, true)
	UpdatePetHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdatePetHandler_NotesTooLongRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, "DOG",
		))

	longNotes := strings.Repeat("a", 1001)
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, models.UpdatePetRequest{Notes: &longNotes}, true)
	UpdatePetHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdatePetHandler_BirthDateTooOldRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, "DOG",
		))

	tooOld := "1400-01-01"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, models.UpdatePetRequest{BirthDate: &tooOld}, true)
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
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized, habitation, notes, deleted_at, breed, icon\s+FROM pet\s+WHERE id = \$1 AND user_id = \$2 AND deleted_at IS NULL`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, "DOG",
		))
	mock.ExpectQuery(`SELECT id, pet_id, date_time, type, notes, value\s+FROM event\s+WHERE pet_id = \$1\s+AND deleted_at IS NULL\s+ORDER BY date_time DESC\s+LIMIT \$2 OFFSET \$3`).
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
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized, habitation, notes, deleted_at, breed, icon\s+FROM pet\s+WHERE id = \$1 AND user_id = \$2 AND deleted_at IS NULL`).
		WillReturnError(sql.ErrNoRows)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/"+testPetID+"/events", nil, true)
	PetByIDHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetPetEventsHandler_SoftDeletedPetReturns404(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	// resolveOwnedPet скрывает мягко удалённых питомцев тем же условием
	// deleted_at IS NULL, что и GET /pet/{id} — запрос к таблице событий не
	// выполняется вовсе.
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized, habitation, notes, deleted_at, breed, icon\s+FROM pet\s+WHERE id = \$1 AND user_id = \$2 AND deleted_at IS NULL`).
		WillReturnError(sql.ErrNoRows)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/"+testPetID+"/events", nil, true)
	PetByIDHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPetEventsHandler_PaginationDefaults(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized, habitation, notes, deleted_at, breed, icon\s+FROM pet\s+WHERE id = \$1 AND user_id = \$2 AND deleted_at IS NULL`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, "DOG",
		))
	mock.ExpectQuery(`SELECT id, pet_id, date_time, type, notes, value\s+FROM event\s+WHERE pet_id = \$1\s+AND deleted_at IS NULL\s+ORDER BY date_time DESC\s+LIMIT \$2 OFFSET \$3`).
		WithArgs(sqlmock.AnyArg(), 50, 0).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value"}))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/"+testPetID+"/events", nil, true)
	PetByIDHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPetEventsHandler_LimitClampedTo200(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized, habitation, notes, deleted_at, breed, icon\s+FROM pet\s+WHERE id = \$1 AND user_id = \$2 AND deleted_at IS NULL`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, "DOG",
		))
	mock.ExpectQuery(`SELECT id, pet_id, date_time, type, notes, value\s+FROM event\s+WHERE pet_id = \$1\s+AND deleted_at IS NULL\s+ORDER BY date_time DESC\s+LIMIT \$2 OFFSET \$3`).
		WithArgs(sqlmock.AnyArg(), 200, 5).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value"}))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/"+testPetID+"/events?limit=1000&offset=5", nil, true)
	PetByIDHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPetEventsHandler_InvalidLimitRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/"+testPetID+"/events?limit=-1", nil, true)
	PetByIDHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetPetEventsHandler_NonIntegerOffsetRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/"+testPetID+"/events?offset=abc", nil, true)
	PetByIDHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
