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
	"sterilized", "habitation", "notes", "deleted_at", "breed", "body_condition",
	"microchipped", "microchip_number", "ringed", "ring_number",
	"uv_lamp_required", "water_type", "enclosure_volume_l", "group_size", "access", "shared", "owner_id", "owner_name",
}

var fileColumns = []string{
	"id", "owner_type", "owner_id", "user_id", "object_key", "content_type", "filename", "position", "confirmed_at", "created_at",
}

// expectNoPetPhoto mocks the file-table lookup that GetPetHandler/GetAllPetHandler
// perform to fill photo_url/photo_file_id, reporting no confirmed photo.
func expectNoPetPhoto(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT id, owner_type, owner_id, user_id, object_key, content_type, filename, position, confirmed_at, created_at\s+FROM file\s+WHERE owner_type = \$1 AND owner_id = \$2 AND confirmed_at IS NOT NULL`).
		WillReturnError(sql.ErrNoRows)
}

// expectNoPetPhotos mocks the batched file-table lookup GetAllPetHandler
// performs for the pet list, reporting no confirmed photos for any pet.
func expectNoPetPhotos(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT id, owner_type, owner_id, user_id, object_key, content_type, filename, position, confirmed_at, created_at\s+FROM file\s+WHERE owner_type = \$1 AND owner_id = ANY\(\$2\) AND confirmed_at IS NOT NULL`).
		WillReturnRows(sqlmock.NewRows(fileColumns))
}

// expectNoWeightEvent mocks the database.GetLatestPetWeight lookup that
// database.GetPetByIDAndUserID performs on every successful read to compute
// the computed, never-stored weight field, reporting no weight-type events
// for the pet yet (weight: null in the response).
func expectNoWeightEvent(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT \(e\.value->>'amount'\)::float8\s+FROM event e\s+JOIN event_pet ep ON ep\.event_id = e\.id\s+WHERE ep\.pet_id = \$1 AND e\.type = 'weight' AND e\.deleted_at IS NULL`).
		WillReturnError(sql.ErrNoRows)
}

// expectWeightEvent mocks the same lookup as expectNoWeightEvent, but
// reporting a latest weight-event amount (see «Вес питомца — Backend»).
func expectWeightEvent(mock sqlmock.Sqlmock, amount float64) {
	mock.ExpectQuery(`SELECT \(e\.value->>'amount'\)::float8\s+FROM event e\s+JOIN event_pet ep ON ep\.event_id = e\.id\s+WHERE ep\.pet_id = \$1 AND e\.type = 'weight' AND e\.deleted_at IS NULL`).
		WillReturnRows(sqlmock.NewRows([]string{"amount"}).AddRow(amount))
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

func TestCreatePetHandler_InvalidWaterTypeRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	bad := "swamp"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{Name: "Rex", Species: "fish", WaterType: &bad}, true)
	CreatePetHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreatePetHandler_InvalidEnclosureVolumeLRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	tooBig := 5001.0
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{Name: "Rex", Species: "fish", EnclosureVolumeL: &tooBig}, true)
	CreatePetHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreatePetHandler_InvalidGroupSizeRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	tooMany := 10001
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{Name: "Rex", Species: "snail", GroupSize: &tooMany}, true)
	CreatePetHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreatePetHandler_MicrochipNumberTooLongRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	tooLong := strings.Repeat("a", 51)
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{Name: "Rex", Species: "dog", MicrochipNumber: &tooLong}, true)
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
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "owner", false, testUserID, "Owner"))
	expectNoWeightEvent(mock)

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
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "owner", false, testUserID, "Owner"))
	expectNoWeightEvent(mock)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{Name: "Rex", Species: "dog"}, true)
	CreatePetHandler(w, r)

	assert.Equal(t, http.StatusCreated, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreatePetHandler_InvalidWeightRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	badWeight := 500.0
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{Name: "Rex", Species: "dog", Weight: &badWeight}, true)
	CreatePetHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestCreatePetHandler_WithWeight_CreatesWeightEvent проверяет, что POST /pet
// с полем weight создаёт событие типа weight для нового питомца и
// возвращает его в поле weight ответа, вычисленном тем же путём, что и
// GET /pet/{id} (см. «Вес питомца — Backend»).
func TestCreatePetHandler_WithWeight_CreatesWeightEvent(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`INSERT INTO pet`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testPetID))
	eventID := "44444444-4444-4444-4444-444444444444"
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO event \(`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(eventID))
	mock.ExpectExec(`INSERT INTO event_pet`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "owner", false, testUserID, "Owner"))
	expectWeightEvent(mock, 5.5)

	weight := 5.5
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{Name: "Rex", Species: "dog", Weight: &weight}, true)
	CreatePetHandler(w, r)

	assert.Equal(t, http.StatusCreated, w.Code)
	var resp models.PetIdResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.Weight)
	assert.Equal(t, 5.5, *resp.Weight)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestCreatePetHandler_WeightEventInsertFailureDoesNotFailCreate проверяет,
// что сбой создания события веса не должен приводить к ошибке создания
// питомца — это best-effort побочный эффект (см. «Вес питомца — Backend»,
// шаг 4: pet creation must NOT roll back if this event insert fails).
func TestCreatePetHandler_WeightEventInsertFailureDoesNotFailCreate(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`INSERT INTO pet`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testPetID))
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO event \(`).
		WillReturnError(assertError)
	mock.ExpectRollback()
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "owner", false, testUserID, "Owner"))
	expectNoWeightEvent(mock)

	weight := 5.5
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet", models.CreatePetRequest{Name: "Rex", Species: "dog", Weight: &weight}, true)
	CreatePetHandler(w, r)

	assert.Equal(t, http.StatusCreated, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetAllPetHandler_WithoutLanguageCode(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.breed, p\.species, CASE WHEN p\.user_id`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "breed", "species", "access", "owner_id"}).
			AddRow(testPetID, "Rex", "Labrador", "dog", "owner", testUserID))
	mock.ExpectQuery(`FROM pet_access_notice`).WillReturnRows(sqlmock.NewRows(accessNoticeColumns))
	expectOwnerNames(mock, map[string]string{testUserID: "Owner"})
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
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.breed, p\.species, CASE WHEN p\.user_id`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "breed", "species", "access", "owner_id"}).
			AddRow(testPetID, "Rex", "Labrador", "dog", "owner", testUserID))
	mock.ExpectQuery(`FROM pet_access_notice`).WillReturnRows(sqlmock.NewRows(accessNoticeColumns))
	expectOwnerNames(mock, map[string]string{testUserID: "Owner"})
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
	assert.Equal(t, models.PetOwnerRef{ID: testUserID, Name: "Owner"}, resp.Items[0].Owner)
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
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.breed, p\.species, CASE WHEN p\.user_id`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "breed", "species", "access", "owner_id"}).
			AddRow(testPetID, "Rex", "Labrador", "dog", "owner", testUserID))
	mock.ExpectQuery(`FROM pet_access_notice`).WillReturnRows(sqlmock.NewRows(accessNoticeColumns))
	expectOwnerNames(mock, map[string]string{testUserID: "Owner"})
	fileID := "55555555-5555-5555-5555-555555555555"
	mock.ExpectQuery(`SELECT id, owner_type, owner_id, user_id, object_key, content_type, filename, position, confirmed_at, created_at\s+FROM file\s+WHERE owner_type = \$1 AND owner_id = ANY\(\$2\) AND confirmed_at IS NOT NULL`).
		WillReturnRows(sqlmock.NewRows(fileColumns).AddRow(
			fileID, "pet_photo", testPetID, testUserID, "pet_photo/"+testPetID+"/"+fileID, "image/jpeg", nil, nil, time.Now(), time.Now(),
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
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
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
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
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
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", "male", "dog", nil, nil, true, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "owner", false, testUserID, "Owner"))
	expectNoWeightEvent(mock)
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
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", "male", "dog", nil, nil, true, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "owner", false, testUserID, "Owner"))
	expectNoWeightEvent(mock)
	fileID := "55555555-5555-5555-5555-555555555555"
	mock.ExpectQuery(`SELECT id, owner_type, owner_id, user_id, object_key, content_type, filename, position, confirmed_at, created_at\s+FROM file\s+WHERE owner_type = \$1 AND owner_id = \$2 AND confirmed_at IS NOT NULL`).
		WillReturnRows(sqlmock.NewRows(fileColumns).AddRow(
			fileID, "pet_photo", testPetID, testUserID, "pet_photo/"+testPetID+"/"+fileID, "image/jpeg", nil, nil, time.Now(), time.Now(),
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

// TestGetPetHandler_WithWeight проверяет, что GET /pet/{id} вычисляет weight
// как amount последнего события типа weight — независимо от того, где это
// событие было создано (POST /pet, PUT /pet/{id} или POST /events; см. «Вес
// питомца — Backend»).
func TestGetPetHandler_WithWeight(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", "male", "dog", nil, nil, true, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "owner", false, testUserID, "Owner"))
	expectWeightEvent(mock, 12.3)
	expectNoPetPhoto(mock)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/"+testPetID, nil, true)
	GetPetHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.PetIdResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.Weight)
	assert.Equal(t, 12.3, *resp.Weight)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdatePetHandler_EmptyNameRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "owner", false, testUserID, "Owner"))
	expectNoWeightEvent(mock)

	empty := ""
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, models.UpdatePetRequest{Name: &empty}, true)
	UpdatePetHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdatePetHandler_NameTooLongRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "owner", false, testUserID, "Owner"))
	expectNoWeightEvent(mock)

	longName := strings.Repeat("a", 101)
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, models.UpdatePetRequest{Name: &longName}, true)
	UpdatePetHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdatePetHandler_NotesTooLongRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "owner", false, testUserID, "Owner"))
	expectNoWeightEvent(mock)

	longNotes := strings.Repeat("a", 1001)
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, models.UpdatePetRequest{Notes: &longNotes}, true)
	UpdatePetHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdatePetHandler_BirthDateTooOldRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "owner", false, testUserID, "Owner"))
	expectNoWeightEvent(mock)

	tooOld := "1400-01-01"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, models.UpdatePetRequest{BirthDate: &tooOld}, true)
	UpdatePetHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdatePetHandler_PartialUpdate(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "owner", false, testUserID, "Owner"))
	expectNoWeightEvent(mock)
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
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
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
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "owner", false, testUserID, "Owner"))
	expectNoWeightEvent(mock)
	mock.ExpectExec(`UPDATE pet SET`).WillReturnResult(sqlmock.NewResult(0, 1))

	name := "Rexy"
	species := "dog"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, models.UpdatePetRequest{Name: &name, Species: &species}, true)
	UpdatePetHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdatePetHandler_InvalidWeightRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "owner", false, testUserID, "Owner"))
	expectNoWeightEvent(mock)

	badWeight := 0.0
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, models.UpdatePetRequest{Weight: &badWeight}, true)
	UpdatePetHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdatePetHandler_InvalidGroupSizeRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "owner", false, testUserID, "Owner"))
	expectNoWeightEvent(mock)

	tooFew := 0
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, models.UpdatePetRequest{GroupSize: &tooFew}, true)
	UpdatePetHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestUpdatePetHandler_ExplicitNullClearsWaterType проверяет, что явный
// null у water_type (в отличие от отсутствия ключа) доходит до
// database.UpdatePet как признак очистки поля — построена на сыром JSON-теле
// запроса (map), а не на models.UpdatePetRequest{}, поскольку сериализация
// структуры с omitempty никогда не порождает буквальный null (см.
// «Профильные поля питомца по видам», «Редактирование питомца — Backend»).
func TestUpdatePetHandler_ExplicitNullClearsWaterType(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "freshwater", nil, nil, "owner", false, testUserID, "Owner"))
	expectNoWeightEvent(mock)
	mock.ExpectExec(`UPDATE pet SET water_type = \$1`).WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, map[string]any{"water_type": nil}, true)
	UpdatePetHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestUpdatePetHandler_AbsentWaterTypeDoesNotChangeIt проверяет, что
// отсутствие ключа water_type в теле запроса не добавляет его в SET (в
// отличие от явного null выше) — обновляется только переданное поле.
func TestUpdatePetHandler_AbsentWaterTypeDoesNotChangeIt(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "freshwater", nil, nil, "owner", false, testUserID, "Owner"))
	expectNoWeightEvent(mock)
	mock.ExpectExec(`UPDATE pet SET notes = \$1\s+WHERE`).WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, map[string]any{"notes": "hello"}, true)
	UpdatePetHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestUpdatePetHandler_WithWeight_CreatesWeightEvent проверяет, что PUT
// /pet/{id} с непустым weight создаёт НОВОЕ событие типа weight, не
// изменяя pet напрямую (см. «Вес питомца — Backend»).
func TestUpdatePetHandler_WithWeight_CreatesWeightEvent(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "owner", false, testUserID, "Owner"))
	expectNoWeightEvent(mock)
	mock.ExpectExec(`UPDATE pet SET`).WillReturnResult(sqlmock.NewResult(0, 1))
	eventID := "44444444-4444-4444-4444-444444444444"
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO event \(`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(eventID))
	mock.ExpectExec(`INSERT INTO event_pet`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	weight := 7.2
	name := "Rexy"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, models.UpdatePetRequest{Name: &name, Weight: &weight}, true)
	UpdatePetHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestUpdatePetHandler_OmittedAndNullWeightAreEquivalent проверяет, что
// отсутствие ключа weight и явный null не создают событие weight (см. «Вес
// питомца — Backend»: очистки веса через этот эндпоинт нет).
func TestUpdatePetHandler_OmittedAndNullWeightAreEquivalent(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT p\.id, p\.name, p\.gender, p\.species, p\.birth_date, p\.color, p\.sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "owner", false, testUserID, "Owner"))
	expectNoWeightEvent(mock)
	mock.ExpectExec(`UPDATE pet SET`).WillReturnResult(sqlmock.NewResult(0, 1))

	name := "Rexy"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPut, "/pet/"+testPetID, models.UpdatePetRequest{Name: &name}, true)
	UpdatePetHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	// Никакого INSERT INTO event не ожидается — ExpectationsWereMet проверит,
	// что все замоканные вызовы (без лишнего INSERT INTO event) исчерпаны.
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeletePetHandler_NotFound(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetRoleMissing(mock)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodDelete, "/pet/"+testPetID, nil, true)
	DeletePetHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestDeletePetHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetRole(mock, "owner")
	// Мягкое удаление питомца, жёсткое удаление его связей с настройками
	// напоминаний и настроек, оставшихся без питомцев (с напоминаниями и
	// файлами), — в одной транзакции.
	// Активная передача питомца отменяется в той же транзакции.
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE pet_transfer SET status = 'cancelled'`).
		WithArgs(testPetID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE pet\s+SET deleted_at`).WillReturnResult(sqlmock.NewResult(0, 1))
	expectEndPetAccessOnDelete(mock)
	mock.ExpectQuery(`DELETE FROM reminder_plan_pet WHERE pet_id = \$1 RETURNING plan_id`).
		WillReturnRows(sqlmock.NewRows([]string{"plan_id"}).AddRow("66666666-6666-4666-8666-666666666666"))
	mock.ExpectQuery(`SELECT p\.id FROM reminder_plan p\s+WHERE p\.id = ANY\(\$1\)\s+AND NOT EXISTS`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("66666666-6666-4666-8666-666666666666"))
	mock.ExpectQuery(`SELECT id FROM reminder WHERE plan_id = ANY`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("55555555-5555-4555-8555-555555555555"))
	mock.ExpectQuery(`DELETE FROM file WHERE owner_type = \$1 AND owner_id = ANY\(\$2\) RETURNING object_key`).
		WithArgs("reminder_file", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"object_key"}))
	mock.ExpectQuery(`DELETE FROM file WHERE owner_type = \$1 AND owner_id = ANY\(\$2\) RETURNING object_key`).
		WithArgs("reminder_plan_file", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"object_key"}))
	mock.ExpectExec(`DELETE FROM reminder_plan WHERE id = ANY`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodDelete, "/pet/"+testPetID, nil, true)
	DeletePetHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPetByIDHandler_RoutesToEvents(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetRole(mock, "owner")
	mock.ExpectQuery(`SELECT e\.id, e\.user_id, e\.date_time, e\.type, e\.notes, e\.value\s+FROM event e\s+JOIN event_pet ep ON ep\.event_id = e\.id\s+WHERE ep\.pet_id = \$1\s+AND e\.deleted_at IS NULL[\s\S]*ORDER BY e\.date_time DESC[\s\S]*LIMIT \$3 OFFSET \$4`).
		WillReturnRows(sqlmock.NewRows(eventColumnNames))
	mock.ExpectQuery(`SELECT COUNT\(\*\)\s+FROM event e\s+JOIN event_pet ep ON ep\.event_id = e\.id\s+WHERE ep\.pet_id = \$1\s+AND e\.deleted_at IS NULL`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	expectAvailableFilters(mock, 0, 0)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/"+testPetID+"/events", nil, true)
	PetByIDHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp PetEventsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.Items)
	assert.Equal(t, 0, resp.Total)
	require.NotNil(t, resp.AvailableFilters)
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
	expectPetRoleMissing(mock)

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
	expectPetRoleMissing(mock)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/"+testPetID+"/events", nil, true)
	PetByIDHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPetEventsHandler_PaginationDefaults(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetRole(mock, "owner")
	mock.ExpectQuery(`SELECT e\.id, e\.user_id, e\.date_time, e\.type, e\.notes, e\.value\s+FROM event e\s+JOIN event_pet ep ON ep\.event_id = e\.id\s+WHERE ep\.pet_id = \$1\s+AND e\.deleted_at IS NULL[\s\S]*ORDER BY e\.date_time DESC[\s\S]*LIMIT \$3 OFFSET \$4`).
		WithArgs(sqlmock.AnyArg(), testUserID, 50, 0).
		WillReturnRows(sqlmock.NewRows(eventColumnNames))
	mock.ExpectQuery(`SELECT COUNT\(\*\)\s+FROM event e\s+JOIN event_pet ep ON ep\.event_id = e\.id\s+WHERE ep\.pet_id = \$1\s+AND e\.deleted_at IS NULL`).
		WithArgs(sqlmock.AnyArg(), testUserID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	expectAvailableFilters(mock, 0, 0)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/"+testPetID+"/events", nil, true)
	PetByIDHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPetEventsHandler_LimitClampedTo200(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetRole(mock, "owner")
	mock.ExpectQuery(`SELECT e\.id, e\.user_id, e\.date_time, e\.type, e\.notes, e\.value\s+FROM event e\s+JOIN event_pet ep ON ep\.event_id = e\.id\s+WHERE ep\.pet_id = \$1\s+AND e\.deleted_at IS NULL[\s\S]*ORDER BY e\.date_time DESC[\s\S]*LIMIT \$3 OFFSET \$4`).
		WithArgs(sqlmock.AnyArg(), testUserID, 200, 5).
		WillReturnRows(sqlmock.NewRows(eventColumnNames))
	mock.ExpectQuery(`SELECT COUNT\(\*\)\s+FROM event e\s+JOIN event_pet ep ON ep\.event_id = e\.id\s+WHERE ep\.pet_id = \$1\s+AND e\.deleted_at IS NULL`).
		WithArgs(sqlmock.AnyArg(), testUserID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

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

// TestGetPetEventsHandler_SearchFiltersAndReturnsTotal проверяет, что при
// непустом search в обе SQL-выборки (список и COUNT) подставляется
// дополнительное WHERE-условие по notes/value->>'label'/value->>'name' и что
// total в ответе берётся из результата COUNT-запроса, а не из длины items.
func TestGetPetEventsHandler_SearchFiltersAndReturnsTotal(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetRole(mock, "owner")
	mock.ExpectQuery(`(?s)SELECT e\.id, e\.user_id, e\.date_time, e\.type, e\.notes, e\.value\s+FROM event e\s+JOIN event_pet ep ON ep\.event_id = e\.id\s+WHERE ep\.pet_id = \$1\s+AND e\.deleted_at IS NULL[\s\S]*AND \(\s*e\.notes ILIKE \$5.*e\.value ->> 'label' ILIKE \$5.*e\.value ->> 'name' ILIKE \$5.*\)[\s\S]*ORDER BY e\.date_time DESC.*LIMIT \$3 OFFSET \$4`).
		WithArgs(sqlmock.AnyArg(), testUserID, 50, 0, "%вакцина%").
		WillReturnRows(sqlmock.NewRows(eventColumnNames).
			AddRow(testEventID, testUserID, time.Now(), "other", sql.NullString{String: "вакцина от бешенства", Valid: true}, []byte(`{"label":"вакцина"}`)))
	expectEventPets(mock, testEventID, petRex)
	mock.ExpectQuery(`(?s)SELECT COUNT\(\*\)\s+FROM event e\s+JOIN event_pet ep ON ep\.event_id = e\.id\s+WHERE ep\.pet_id = \$1\s+AND e\.deleted_at IS NULL[\s\S]*AND \(\s*e\.notes ILIKE \$3.*e\.value ->> 'label' ILIKE \$3.*e\.value ->> 'name' ILIKE \$3.*\)`).
		WithArgs(sqlmock.AnyArg(), testUserID, "%вакцина%").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))
	expectNoSharedPets(mock)
	mock.ExpectQuery(`SELECT owner_id, COUNT\(\*\) FROM file`).
		WillReturnRows(sqlmock.NewRows([]string{"owner_id", "count"}))
	expectAvailableFilters(mock, 0, 0)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/"+testPetID+"/events?search=%D0%B2%D0%B0%D0%BA%D1%86%D0%B8%D0%BD%D0%B0", nil, true)
	PetByIDHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp PetEventsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.Items, 1)
	assert.Equal(t, 7, resp.Total)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetPetEventsHandler_EmptySearchOmitsFilter проверяет, что пустая
// строка search (?search=) трактуется как отсутствие фильтра — тот же SQL,
// что и без параметра вовсе.
func TestGetPetEventsHandler_EmptySearchOmitsFilter(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetRole(mock, "owner")
	mock.ExpectQuery(`SELECT e\.id, e\.user_id, e\.date_time, e\.type, e\.notes, e\.value\s+FROM event e\s+JOIN event_pet ep ON ep\.event_id = e\.id\s+WHERE ep\.pet_id = \$1\s+AND e\.deleted_at IS NULL[\s\S]*ORDER BY e\.date_time DESC[\s\S]*LIMIT \$3 OFFSET \$4`).
		WithArgs(sqlmock.AnyArg(), testUserID, 50, 0).
		WillReturnRows(sqlmock.NewRows(eventColumnNames))
	mock.ExpectQuery(`SELECT COUNT\(\*\)\s+FROM event e\s+JOIN event_pet ep ON ep\.event_id = e\.id\s+WHERE ep\.pet_id = \$1\s+AND e\.deleted_at IS NULL`).
		WithArgs(sqlmock.AnyArg(), testUserID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	expectAvailableFilters(mock, 0, 0)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/"+testPetID+"/events?search=", nil, true)
	PetByIDHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// expectAvailableFilters мокирует два запроса блока available_filters
// (итоги + разбивка по типам) с заданным числом событий и дней.
func expectAvailableFilters(mock sqlmock.Sqlmock, total, days int, typeCounts ...any) {
	mock.ExpectQuery(`SELECT COUNT\(\*\), COUNT\(DISTINCT \(e\.date_time AT TIME ZONE \$2\)::date\)\s+FROM event e\s+JOIN event_pet ep ON ep\.event_id = e\.id\s+WHERE ep\.pet_id = \$1\s+AND e\.deleted_at IS NULL`).
		WillReturnRows(sqlmock.NewRows([]string{"count", "days"}).AddRow(total, days))
	rows := sqlmock.NewRows([]string{"type", "count"})
	for i := 0; i+1 < len(typeCounts); i += 2 {
		rows.AddRow(typeCounts[i], typeCounts[i+1])
	}
	mock.ExpectQuery(`SELECT e\.type, COUNT\(\*\)\s+FROM event e\s+JOIN event_pet ep ON ep\.event_id = e\.id\s+WHERE ep\.pet_id = \$1\s+AND e\.deleted_at IS NULL[\s\S]*GROUP BY e\.type`).
		WillReturnRows(rows)
}

// Фильтры types/from/to/tz: в обе выборки попадают условия в порядке
// search, types, from, to; границы периода переводятся в моменты времени в
// поясе tz (конец — начало суток после to).
func TestGetPetEventsHandler_TypesAndPeriodFilters(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetRole(mock, "owner")
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.FixedZone("", 3*3600))
	to := time.Date(2026, 10, 11, 0, 0, 0, 0, time.FixedZone("", 3*3600))
	mock.ExpectQuery(`(?s)AND e\.deleted_at IS NULL[\s\S]*AND \(\s*e\.notes ILIKE \$5.*\)\s+AND e\.type = ANY\(\$6\)\s+AND e\.date_time >= \$7\s+AND e\.date_time < \$8\s+ORDER BY e\.date_time DESC.*LIMIT \$3 OFFSET \$4`).
		WithArgs(sqlmock.AnyArg(), testUserID, 50, 10, "%кот%", sqlmock.AnyArg(), from.UTC(), to.UTC()).
		WillReturnRows(sqlmock.NewRows(eventColumnNames))
	mock.ExpectQuery(`(?s)SELECT COUNT\(\*\)\s+FROM event e.*AND \(\s*e\.notes ILIKE \$3.*\)\s+AND e\.type = ANY\(\$4\)\s+AND e\.date_time >= \$5\s+AND e\.date_time < \$6`).
		WithArgs(sqlmock.AnyArg(), testUserID, "%кот%", sqlmock.AnyArg(), from.UTC(), to.UTC()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/"+testPetID+"/events?offset=10&search=%D0%BA%D0%BE%D1%82&types=weight,activity,weight&from=2026-10-01&to=2026-10-10&tz=Europe/Moscow", nil, true)
	PetByIDHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp PetEventsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 3, resp.Total)
	// offset>0: блок available_filters не считается и не отдаётся.
	assert.Nil(t, resp.AvailableFilters)
	assert.NotContains(t, w.Body.String(), "available_filters")
	require.NoError(t, mock.ExpectationsWereMet())
}

// available_filters при offset=0 не зависит от фильтров запроса (отдельные
// запросы без условий фильтра), distinct_days считается в поясе tz.
func TestGetPetEventsHandler_AvailableFiltersIgnoreFilters(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetRole(mock, "owner")
	mock.ExpectQuery(`SELECT e\.id, e\.user_id`).
		WillReturnRows(sqlmock.NewRows(eventColumnNames))
	mock.ExpectQuery(`SELECT COUNT\(\*\)\s+FROM event e`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`SELECT COUNT\(\*\), COUNT\(DISTINCT`).
		WithArgs(sqlmock.AnyArg(), "Europe/Moscow", testUserID).
		WillReturnRows(sqlmock.NewRows([]string{"count", "days"}).AddRow(12, 4))
	mock.ExpectQuery(`SELECT e\.type, COUNT\(\*\)`).
		WithArgs(sqlmock.AnyArg(), testUserID).
		WillReturnRows(sqlmock.NewRows([]string{"type", "count"}).AddRow("weight", 9).AddRow("activity", 3))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/"+testPetID+"/events?types=feeding&tz=Europe/Moscow", nil, true)
	PetByIDHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp PetEventsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.AvailableFilters)
	assert.Equal(t, 12, resp.AvailableFilters.Total)
	assert.Equal(t, 4, resp.AvailableFilters.DistinctDays)
	assert.Equal(t, []PetEventTypeCountItem{{Type: "weight", Count: 9}, {Type: "activity", Count: 3}}, resp.AvailableFilters.Types)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Без tz distinct_days считается в UTC.
func TestGetPetEventsHandler_AvailableFiltersDefaultUTC(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetRole(mock, "owner")
	mock.ExpectQuery(`SELECT e\.id, e\.user_id`).
		WillReturnRows(sqlmock.NewRows(eventColumnNames))
	mock.ExpectQuery(`SELECT COUNT\(\*\)\s+FROM event e`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(`SELECT COUNT\(\*\), COUNT\(DISTINCT`).
		WithArgs(sqlmock.AnyArg(), "UTC", testUserID).
		WillReturnRows(sqlmock.NewRows([]string{"count", "days"}).AddRow(0, 0))
	mock.ExpectQuery(`SELECT e\.type, COUNT\(\*\)`).
		WillReturnRows(sqlmock.NewRows([]string{"type", "count"}))

	w := httptest.NewRecorder()
	PetByIDHandler(w, petRequest(t, http.MethodGet, "/pet/"+testPetID+"/events", nil, true))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"types":[]`)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPetEventsHandler_InvalidFiltersRejected(t *testing.T) {
	cases := map[string]string{
		"unknown type":          "types=weight,bogus",
		"bad from format":       "from=2026-13-01&tz=UTC",
		"bad to format":         "to=01.10.2026&tz=UTC",
		"from after to":         "from=2026-10-10&to=2026-10-01&tz=UTC",
		"from without tz":       "from=2026-10-01",
		"to without tz":         "to=2026-10-01",
		"invalid tz":            "tz=Mars/Base",
		"invalid tz with range": "from=2026-10-01&tz=Mars/Base",
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)

			w := httptest.NewRecorder()
			PetByIDHandler(w, petRequest(t, http.MethodGet, "/pet/"+testPetID+"/events?"+query, nil, true))

			assert.Equal(t, http.StatusBadRequest, w.Code)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// Пустой types и одиночный from/to без парной границы допустимы.
func TestParsePetEventsFilter_Variants(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/pet/x/events?types=&from=2026-10-01&tz=Europe/Moscow", nil)
	filter, tzName, ok := parsePetEventsFilter(httptest.NewRecorder(), r)
	require.True(t, ok)
	assert.Empty(t, filter.Types)
	require.NotNil(t, filter.From)
	assert.Nil(t, filter.To)
	assert.Equal(t, "Europe/Moscow", tzName)
	assert.Equal(t, time.Date(2026, 9, 30, 21, 0, 0, 0, time.UTC), filter.From.UTC())

	r = httptest.NewRequest(http.MethodGet, "/pet/x/events?to=2026-10-01&tz=UTC", nil)
	filter, _, ok = parsePetEventsFilter(httptest.NewRecorder(), r)
	require.True(t, ok)
	assert.Nil(t, filter.From)
	assert.Equal(t, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), filter.To.UTC())
}
