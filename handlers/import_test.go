package handlers

import (
	"encoding/json"
	"myauthservice/models"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testImportIdempotencyKey = "77777777-7777-4777-7777-777777777777"

func importRequest(t *testing.T, body any, authed bool, idempotencyKey string) *http.Request {
	t.Helper()
	r := doRequest(http.MethodPost, "/import/local-data", body)
	if authed {
		r.Header.Set("Authorization", "Bearer "+validAccessToken(t, testUserID))
	}
	if idempotencyKey != "" {
		r.Header.Set("Idempotency-Key", idempotencyKey)
	}
	return r
}

func validImportPet(localID string) models.ImportLocalDataPet {
	return models.ImportLocalDataPet{
		LocalID: localID,
		Name:    "Барсик",
		Species: "cat",
	}
}

func validImportEvent(localID, petLocalID string) models.ImportLocalDataEvent {
	return models.ImportLocalDataEvent{
		LocalID:    localID,
		PetLocalID: petLocalID,
		Date:       "2024-01-01T12:00:00Z",
		Type:       "weight",
		Value:      eventValue(`{"amount":4.2}`),
	}
}

func TestImportLocalDataHandler_MethodNotAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	ImportLocalDataHandler(w, doRequest(http.MethodGet, "/import/local-data", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestImportLocalDataHandler_Unauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	r := importRequest(t, models.ImportLocalDataRequest{Pets: []models.ImportLocalDataPet{}, Events: []models.ImportLocalDataEvent{}}, false, testImportIdempotencyKey)
	ImportLocalDataHandler(w, r)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestImportLocalDataHandler_MissingIdempotencyKey(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := importRequest(t, models.ImportLocalDataRequest{Pets: []models.ImportLocalDataPet{}, Events: []models.ImportLocalDataEvent{}}, true, "")
	ImportLocalDataHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImportLocalDataHandler_MalformedIdempotencyKey(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := importRequest(t, models.ImportLocalDataRequest{Pets: []models.ImportLocalDataPet{}, Events: []models.ImportLocalDataEvent{}}, true, "not-a-uuid")
	ImportLocalDataHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImportLocalDataHandler_MalformedJSON(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectExec(`INSERT INTO import_local_data_idempotency_key`).
		WithArgs(testUserID, testImportIdempotencyKey).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/import/local-data", strings.NewReader("not-json"))
	r.Header.Set("Authorization", "Bearer "+validAccessToken(t, testUserID))
	r.Header.Set("Idempotency-Key", testImportIdempotencyKey)
	ImportLocalDataHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errResp struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Equal(t, "BAD_REQUEST", errResp.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImportLocalDataHandler_NullPetsRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectExec(`INSERT INTO import_local_data_idempotency_key`).
		WithArgs(testUserID, testImportIdempotencyKey).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	r := importRequest(t, models.ImportLocalDataRequest{Events: []models.ImportLocalDataEvent{}}, true, testImportIdempotencyKey)
	ImportLocalDataHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImportLocalDataHandler_InvalidPetRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectExec(`INSERT INTO import_local_data_idempotency_key`).
		WithArgs(testUserID, testImportIdempotencyKey).
		WillReturnResult(sqlmock.NewResult(0, 1))

	badPet := models.ImportLocalDataPet{LocalID: "local-1", Name: "", Species: "cat"}
	w := httptest.NewRecorder()
	r := importRequest(t, models.ImportLocalDataRequest{
		Pets:   []models.ImportLocalDataPet{badPet},
		Events: []models.ImportLocalDataEvent{},
	}, true, testImportIdempotencyKey)
	ImportLocalDataHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImportLocalDataHandler_InvalidEventRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectExec(`INSERT INTO import_local_data_idempotency_key`).
		WithArgs(testUserID, testImportIdempotencyKey).
		WillReturnResult(sqlmock.NewResult(0, 1))

	pet := validImportPet("local-1")
	badEvent := models.ImportLocalDataEvent{LocalID: "event-1", PetLocalID: "local-1", Date: "2024-01-01T12:00:00Z", Type: "not-a-type", Value: eventValue(`{"amount":4.2}`)}
	w := httptest.NewRecorder()
	r := importRequest(t, models.ImportLocalDataRequest{
		Pets:   []models.ImportLocalDataPet{pet},
		Events: []models.ImportLocalDataEvent{badEvent},
	}, true, testImportIdempotencyKey)
	ImportLocalDataHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Импорт обязан использовать тот же валидатор значения, что и POST /events:
// ослабленного правила для переноса локальных данных быть не должно.
func TestImportLocalDataHandler_InvalidEventValueRejected(t *testing.T) {
	cases := []struct {
		name  string
		event models.ImportLocalDataEvent
	}{
		{"вес вне диапазона", models.ImportLocalDataEvent{LocalID: "event-1", PetLocalID: "local-1", Date: "2024-01-01T12:00:00Z", Type: "weight", Value: eventValue(`{"amount":500}`)}},
		{"лишнее поле", models.ImportLocalDataEvent{LocalID: "event-1", PetLocalID: "local-1", Date: "2024-01-01T12:00:00Z", Type: "weight", Value: eventValue(`{"amount":5,"unit":"g"}`)}},
		{"нет обязательного поля", models.ImportLocalDataEvent{LocalID: "event-1", PetLocalID: "local-1", Date: "2024-01-01T12:00:00Z", Type: "temperature", Value: eventValue(`{"amount":38}`)}},
		{"строка вместо объекта", models.ImportLocalDataEvent{LocalID: "event-1", PetLocalID: "local-1", Date: "2024-01-01T12:00:00Z", Type: "weight", Value: eventValue(`"4.2"`)}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			mock.ExpectExec(`INSERT INTO import_local_data_idempotency_key`).
				WithArgs(testUserID, testImportIdempotencyKey).
				WillReturnResult(sqlmock.NewResult(0, 1))

			w := httptest.NewRecorder()
			r := importRequest(t, models.ImportLocalDataRequest{
				Pets:   []models.ImportLocalDataPet{validImportPet("local-1")},
				Events: []models.ImportLocalDataEvent{c.event},
			}, true, testImportIdempotencyKey)
			ImportLocalDataHandler(w, r)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestImportLocalDataHandler_EventPetLocalIDMismatchRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectExec(`INSERT INTO import_local_data_idempotency_key`).
		WithArgs(testUserID, testImportIdempotencyKey).
		WillReturnResult(sqlmock.NewResult(0, 1))

	pet := validImportPet("local-1")
	event := validImportEvent("event-1", "does-not-exist")
	w := httptest.NewRecorder()
	r := importRequest(t, models.ImportLocalDataRequest{
		Pets:   []models.ImportLocalDataPet{pet},
		Events: []models.ImportLocalDataEvent{event},
	}, true, testImportIdempotencyKey)
	ImportLocalDataHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Дублирующийся local_id питомца делает отображение local_id -> id
// неоднозначным для поля pets ответа — запрос должен быть отклонён (см.
// "Импорт локальных данных — Backend", раздел 3, шаг 4).
func TestImportLocalDataHandler_DuplicatePetLocalIDRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectExec(`INSERT INTO import_local_data_idempotency_key`).
		WithArgs(testUserID, testImportIdempotencyKey).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	r := importRequest(t, models.ImportLocalDataRequest{
		Pets:   []models.ImportLocalDataPet{validImportPet("dup"), validImportPet("dup")},
		Events: []models.ImportLocalDataEvent{},
	}, true, testImportIdempotencyKey)
	ImportLocalDataHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Дублирующийся local_id события делает отображение local_id -> id
// неоднозначным для поля events ответа — запрос должен быть отклонён.
func TestImportLocalDataHandler_DuplicateEventLocalIDRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectExec(`INSERT INTO import_local_data_idempotency_key`).
		WithArgs(testUserID, testImportIdempotencyKey).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	r := importRequest(t, models.ImportLocalDataRequest{
		Pets: []models.ImportLocalDataPet{validImportPet("local-1")},
		Events: []models.ImportLocalDataEvent{
			validImportEvent("dup-event", "local-1"),
			validImportEvent("dup-event", "local-1"),
		},
	}, true, testImportIdempotencyKey)
	ImportLocalDataHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImportLocalDataHandler_InvalidProfileRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectExec(`INSERT INTO import_local_data_idempotency_key`).
		WithArgs(testUserID, testImportIdempotencyKey).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	r := importRequest(t, models.ImportLocalDataRequest{
		Pets:    []models.ImportLocalDataPet{},
		Events:  []models.ImportLocalDataEvent{},
		Profile: &models.ImportLocalDataProfile{FirstName: ""},
	}, true, testImportIdempotencyKey)
	ImportLocalDataHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImportLocalDataHandler_IdempotencyKeyReplaysStoredResult(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectExec(`INSERT INTO import_local_data_idempotency_key`).
		WithArgs(testUserID, testImportIdempotencyKey).
		WillReturnResult(sqlmock.NewResult(0, 0))
	const storedEventID = "66666666-6666-6666-6666-666666666666"
	mock.ExpectQuery(`SELECT pets_imported, events_imported, profile_imported`).
		WithArgs(testUserID, testImportIdempotencyKey).
		WillReturnRows(sqlmock.NewRows([]string{"pets_imported", "events_imported", "profile_imported", "pets_mapping", "events_mapping"}).
			AddRow(2, 1, true, `[{"local_id":"whatever","id":"`+testPetID+`"}]`, `[{"local_id":"event-1","id":"`+storedEventID+`"}]`))

	w := httptest.NewRecorder()
	// Тело повторного запроса умышленно отличается от первого раза — должен
	// вернуться ранее сохранённый результат, без повторной валидации/записи.
	r := importRequest(t, models.ImportLocalDataRequest{
		Pets:   []models.ImportLocalDataPet{validImportPet("whatever")},
		Events: []models.ImportLocalDataEvent{},
	}, true, testImportIdempotencyKey)
	ImportLocalDataHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.ImportLocalDataResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, models.ImportLocalDataResponse{
		PetsImported: 2, EventsImported: 1, ProfileImported: true,
		Pets:   []models.ImportedPet{{LocalID: "whatever", ID: testPetID}},
		Events: []models.ImportedEvent{{LocalID: "event-1", ID: storedEventID}},
	}, resp)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImportLocalDataHandler_SuccessWithoutProfile(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectExec(`INSERT INTO import_local_data_idempotency_key`).
		WithArgs(testUserID, testImportIdempotencyKey).
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO pet`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testPetID))
	mock.ExpectQuery(`INSERT INTO event`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("66666666-6666-6666-6666-666666666666"))
	mock.ExpectCommit()

	const insertedEventID = "66666666-6666-6666-6666-666666666666"
	mock.ExpectExec(`UPDATE import_local_data_idempotency_key SET`).
		WithArgs(1, 1, false, `[{"local_id":"local-1","id":"`+testPetID+`"}]`, `[{"local_id":"event-1","id":"`+insertedEventID+`"}]`, testUserID, testImportIdempotencyKey).
		WillReturnResult(sqlmock.NewResult(0, 1))

	pet := validImportPet("local-1")
	event := validImportEvent("event-1", "local-1")
	w := httptest.NewRecorder()
	r := importRequest(t, models.ImportLocalDataRequest{
		Pets:   []models.ImportLocalDataPet{pet},
		Events: []models.ImportLocalDataEvent{event},
	}, true, testImportIdempotencyKey)
	ImportLocalDataHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.ImportLocalDataResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, models.ImportLocalDataResponse{
		PetsImported: 1, EventsImported: 1, ProfileImported: false,
		Pets:   []models.ImportedPet{{LocalID: "local-1", ID: testPetID}},
		Events: []models.ImportedEvent{{LocalID: "event-1", ID: insertedEventID}},
	}, resp)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImportLocalDataHandler_SuccessWithProfile(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectExec(`INSERT INTO import_local_data_idempotency_key`).
		WithArgs(testUserID, testImportIdempotencyKey).
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO pet`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testPetID))
	mock.ExpectQuery(`INSERT INTO event`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("66666666-6666-6666-6666-666666666666"))
	mock.ExpectExec(`INSERT INTO profile`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	const insertedEventID = "66666666-6666-6666-6666-666666666666"
	mock.ExpectExec(`UPDATE import_local_data_idempotency_key SET`).
		WithArgs(1, 1, true, `[{"local_id":"local-1","id":"`+testPetID+`"}]`, `[{"local_id":"event-1","id":"`+insertedEventID+`"}]`, testUserID, testImportIdempotencyKey).
		WillReturnResult(sqlmock.NewResult(0, 1))

	pet := validImportPet("local-1")
	event := validImportEvent("event-1", "local-1")
	w := httptest.NewRecorder()
	r := importRequest(t, models.ImportLocalDataRequest{
		Pets:    []models.ImportLocalDataPet{pet},
		Events:  []models.ImportLocalDataEvent{event},
		Profile: &models.ImportLocalDataProfile{FirstName: "Иван"},
	}, true, testImportIdempotencyKey)
	ImportLocalDataHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.ImportLocalDataResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, models.ImportLocalDataResponse{
		PetsImported: 1, EventsImported: 1, ProfileImported: true,
		Pets:   []models.ImportedPet{{LocalID: "local-1", ID: testPetID}},
		Events: []models.ImportedEvent{{LocalID: "event-1", ID: insertedEventID}},
	}, resp)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestImportLocalDataHandler_DBErrorRollsBackAndReturns500(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectExec(`INSERT INTO import_local_data_idempotency_key`).
		WithArgs(testUserID, testImportIdempotencyKey).
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO pet`).
		WillReturnError(assertError)
	mock.ExpectRollback()

	pet := validImportPet("local-1")
	w := httptest.NewRecorder()
	r := importRequest(t, models.ImportLocalDataRequest{
		Pets:   []models.ImportLocalDataPet{pet},
		Events: []models.ImportLocalDataEvent{},
	}, true, testImportIdempotencyKey)
	ImportLocalDataHandler(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}
