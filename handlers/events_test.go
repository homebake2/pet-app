package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
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

// eventValue — короткая запись типизированного значения события в тестах.
func eventValue(raw string) json.RawMessage {
	return json.RawMessage(raw)
}

func eventValuePtr(raw string) *json.RawMessage {
	v := eventValue(raw)
	return &v
}

func eventRequest(t *testing.T, method, path string, body any, authed bool) *http.Request {
	t.Helper()
	r := doRequest(method, path, body)
	if authed {
		r.Header.Set("Authorization", "Bearer "+validAccessToken(t, testUserID))
	}
	return r
}

func TestGetActivitiesHandler_MethodNotAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	GetActivitiesHandler(w, eventRequest(t, http.MethodPost, "/activities", nil, false))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestGetActivitiesHandler_MissingParams(t *testing.T) {
	cases := []string{
		"/activities",
		"/activities?from=2024-01-01",
		"/activities?from=2024-01-01&to=2024-01-02",
	}
	for _, path := range cases {
		w := httptest.NewRecorder()
		GetActivitiesHandler(w, eventRequest(t, http.MethodGet, path, nil, false))
		assert.Equal(t, http.StatusBadRequest, w.Code, path)
	}
}

func TestGetActivitiesHandler_InvalidDate(t *testing.T) {
	w := httptest.NewRecorder()
	path := "/activities?from=bad&to=2024-01-02&pet_id=" + testPetID
	GetActivitiesHandler(w, eventRequest(t, http.MethodGet, path, nil, false))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetActivitiesHandler_FromAfterTo(t *testing.T) {
	w := httptest.NewRecorder()
	path := "/activities?from=2024-01-05&to=2024-01-01&pet_id=" + testPetID
	GetActivitiesHandler(w, eventRequest(t, http.MethodGet, path, nil, false))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetActivitiesHandler_InvalidPetID(t *testing.T) {
	w := httptest.NewRecorder()
	path := "/activities?from=2024-01-01&to=2024-01-02&pet_id=not-a-uuid"
	GetActivitiesHandler(w, eventRequest(t, http.MethodGet, path, nil, false))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetActivitiesHandler_PetNotOwned(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT COUNT\(1\) FROM pet WHERE id = \$1 AND user_id = \$2`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	w := httptest.NewRecorder()
	path := "/activities?from=2024-01-01&to=2024-01-02&pet_id=" + testPetID
	GetActivitiesHandler(w, eventRequest(t, http.MethodGet, path, nil, true))

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetActivitiesHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT COUNT\(1\) FROM pet WHERE id = \$1 AND user_id = \$2`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT name FROM pet WHERE id = \$1`).
		WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("Rex"))
	eventDate := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT id, pet_id, date_time, type, notes, value\s+FROM event\s+WHERE pet_id = \$1`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value"}).
			AddRow(testPetID, testPetID, eventDate, "weight", nil, []byte(`{"amount":5}`)))
	mock.ExpectQuery(`SELECT owner_id, COUNT\(\*\) FROM file\s+WHERE owner_type = \$1 AND owner_id = ANY\(\$2\) AND confirmed_at IS NOT NULL\s+GROUP BY owner_id`).
		WillReturnRows(sqlmock.NewRows([]string{"owner_id", "count"}))

	w := httptest.NewRecorder()
	path := "/activities?from=2024-01-01&to=2024-01-02&pet_id=" + testPetID
	GetActivitiesHandler(w, eventRequest(t, http.MethodGet, path, nil, true))

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.ActivitiesResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Rex", resp.PetName)
	require.Len(t, resp.Items, 2)
	require.Len(t, resp.Items[0].Events, 1)
	assert.Equal(t, "weight", resp.Items[0].Events[0].Type)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateEventHandler_MethodNotAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	CreateEventHandler(w, eventRequest(t, http.MethodGet, "/events", nil, false))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestCreateEventHandler_Unauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	CreateEventHandler(w, eventRequest(t, http.MethodPost, "/events", models.CreateEventRequest{}, false))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestCreateEventHandler_MissingFields(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodPost, "/events", models.CreateEventRequest{}, true)
	CreateEventHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateEventHandler_InvalidType(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	body := models.CreateEventRequest{PetID: testPetID, Date: "2024-01-01T10:00:00Z", Type: "bogus", Value: eventValue(`{"amount":5}`)}
	r := eventRequest(t, http.MethodPost, "/events", body, true)
	CreateEventHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateEventHandler_InvalidPetID(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	body := models.CreateEventRequest{PetID: "not-a-uuid", Date: "2024-01-01T10:00:00Z", Type: "weight", Value: eventValue(`{"amount":5}`)}
	r := eventRequest(t, http.MethodPost, "/events", body, true)
	CreateEventHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateEventHandler_InvalidValueForType(t *testing.T) {
	cases := []struct {
		name  string
		typ   string
		value string
	}{
		{"value не объект", "weight", `"4.5"`},
		{"weight без amount", "weight", `{}`},
		{"weight вне диапазона сверху", "weight", `{"amount":500}`},
		{"weight вне диапазона снизу", "weight", `{"amount":0}`},
		{"weight с лишним полем", "weight", `{"amount":5,"kind":"body"}`},
		{"temperature без kind", "temperature", `{"amount":38.5}`},
		{"temperature c неизвестным kind", "temperature", `{"amount":38.5,"kind":"tail"}`},
		{"temperature вне диапазона", "temperature", `{"amount":51,"kind":"body"}`},
		{"feeding без food", "feeding", `{"amount":100,"unit":"g"}`},
		{"feeding с неизвестной unit", "feeding", `{"amount":100,"unit":"kg","food":"dry"}`},
		{"water вне диапазона", "water", `{"amount":0}`},
		{"activity без kind", "activity", `{"duration_min":30}`},
		{"activity с distance вне диапазона", "activity", `{"duration_min":30,"kind":"walk","distance_m":100001}`},
		{"sleep вне диапазона", "sleep", `{"duration_min":0}`},
		{"medication без name", "medication", `{"dose_amount":1,"dose_unit":"mg"}`},
		{"medication доза без единицы", "medication", `{"name":"Ципровет","dose_amount":1}`},
		{"medication единица без дозы", "medication", `{"name":"Ципровет","dose_unit":"mg"}`},
		{"hygiene с неизвестной процедурой", "hygiene", `{"procedure":"walk"}`},
		{"mood без state", "mood", `{}`},
		{"status не из словаря", "urine", `{"status":"a lot"}`},
		{"status как строка вместо объекта", "urine", `"normal"`},
		{"other слишком длинный label", "other", fmt.Sprintf(`{"label":%q}`, strings.Repeat("x", 51))},
		{"other с лишним полем", "other", `{"label":"хромота","amount":1}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)

			body := models.CreateEventRequest{PetID: testPetID, Date: "2024-01-01T10:00:00Z", Type: c.typ, Value: eventValue(c.value)}
			w := httptest.NewRecorder()
			r := eventRequest(t, http.MethodPost, "/events", body, true)
			CreateEventHandler(w, r)
			assert.Equal(t, http.StatusBadRequest, w.Code, c.name)
		})
	}
}

func TestCreateEventHandler_ValidValueForType(t *testing.T) {
	cases := []struct {
		name  string
		typ   string
		value string
	}{
		{"weight", "weight", `{"amount":4.5}`},
		{"weight нижняя граница", "weight", `{"amount":0.001}`},
		{"weight верхняя граница", "weight", `{"amount":400}`},
		{"temperature тела", "temperature", `{"amount":38.5,"kind":"body"}`},
		{"temperature среды", "temperature", `{"amount":26,"kind":"environment"}`},
		{"feeding", "feeding", `{"amount":100,"unit":"g","food":"dry"}`},
		{"feeding счётный корм", "feeding", `{"amount":10,"unit":"piece","food":"insects"}`},
		{"water", "water", `{"amount":250}`},
		{"activity без дистанции", "activity", `{"duration_min":30,"kind":"walk"}`},
		{"activity с дистанцией", "activity", `{"duration_min":30,"kind":"walk","distance_m":2500}`},
		{"sleep", "sleep", `{"duration_min":480}`},
		{"medication без дозы", "medication", `{"name":"Ципровет"}`},
		{"medication с дозой", "medication", `{"name":"Ципровет","dose_amount":2.5,"dose_unit":"mg"}`},
		{"hygiene", "hygiene", `{"procedure":"bath"}`},
		{"mood", "mood", `{"state":"calm"}`},
		{"status normal", "urine", `{"status":"normal"}`},
		{"status abnormal", "vomit", `{"status":"abnormal"}`},
		{"other", "other", `{"label":"рвота после еды"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
				WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
					testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, "DOG",
				))
			eventID := "44444444-4444-4444-4444-444444444444"
			mock.ExpectQuery(`INSERT INTO event`).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(eventID))
			mock.ExpectQuery(`SELECT e.id, e.pet_id, e.date_time, e.type, e.notes, e.value, p.name`).
				WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value", "name"}).
					AddRow(eventID, testPetID, time.Now(), c.typ, nil, []byte(c.value), "Rex"))
			mock.ExpectQuery(`SELECT id, owner_type, owner_id, user_id, object_key, content_type, filename, position, confirmed_at, created_at\s+FROM file\s+WHERE owner_type = \$1 AND owner_id = \$2 AND confirmed_at IS NOT NULL\s+ORDER BY position ASC`).
				WillReturnRows(sqlmock.NewRows(fileRowColumns))

			body := models.CreateEventRequest{PetID: testPetID, Date: "2024-01-01T10:00:00Z", Type: c.typ, Value: eventValue(c.value)}
			w := httptest.NewRecorder()
			r := eventRequest(t, http.MethodPost, "/events", body, true)
			CreateEventHandler(w, r)
			assert.Equal(t, http.StatusCreated, w.Code, c.name)

			var resp models.EventResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.JSONEq(t, c.value, string(resp.Value))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCreateEventHandler_InvalidIdempotencyKey(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	body := models.CreateEventRequest{PetID: testPetID, Date: "2024-01-01T10:00:00Z", Type: "weight", Value: eventValue(`{"amount":5}`)}
	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodPost, "/events", body, true)
	r.Header.Set("Idempotency-Key", "not-a-uuid")
	CreateEventHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateEventHandler_IdempotencyKey_ReturnsExisting(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, "DOG",
		))
	existingEventID := "55555555-5555-5555-5555-555555555555"
	mock.ExpectQuery(`SELECT e.id, e.pet_id, e.date_time, e.type, e.notes, e.value, p.name\s+FROM event e\s+JOIN pet p ON e.pet_id = p.id\s+WHERE e.pet_id = \$1 AND e.idempotency_key = \$2`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value", "name"}).
			AddRow(existingEventID, testPetID, time.Now(), "weight", nil, []byte(`{"amount":5}`), "Rex"))
	mock.ExpectQuery(`SELECT id, owner_type, owner_id, user_id, object_key, content_type, filename, position, confirmed_at, created_at\s+FROM file\s+WHERE owner_type = \$1 AND owner_id = \$2 AND confirmed_at IS NOT NULL\s+ORDER BY position ASC`).
		WillReturnRows(sqlmock.NewRows(fileRowColumns))

	key := "11111111-1111-4111-8111-111111111111"
	body := models.CreateEventRequest{PetID: testPetID, Date: "2024-01-01T10:00:00Z", Type: "weight", Value: eventValue(`{"amount":5}`)}
	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodPost, "/events", body, true)
	r.Header.Set("Idempotency-Key", key)
	CreateEventHandler(w, r)

	assert.Equal(t, http.StatusCreated, w.Code)
	var resp models.EventResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, existingEventID, resp.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateEventHandler_PetNotFound(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnError(sql.ErrNoRows)

	body := models.CreateEventRequest{PetID: testPetID, Date: "2024-01-01T10:00:00Z", Type: "weight", Value: eventValue(`{"amount":5}`)}
	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodPost, "/events", body, true)
	CreateEventHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCreateEventHandler_PetDeleted(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, time.Now(), nil, "DOG",
		))

	body := models.CreateEventRequest{PetID: testPetID, Date: "2024-01-01T10:00:00Z", Type: "weight", Value: eventValue(`{"amount":5}`)}
	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodPost, "/events", body, true)
	CreateEventHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateEventHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, "DOG",
		))
	eventID := "44444444-4444-4444-4444-444444444444"
	mock.ExpectQuery(`INSERT INTO event`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(eventID))
	mock.ExpectQuery(`SELECT e.id, e.pet_id, e.date_time, e.type, e.notes, e.value, p.name`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value", "name"}).
			AddRow(eventID, testPetID, time.Now(), "weight", nil, []byte(`{"amount":5}`), "Rex"))
	mock.ExpectQuery(`SELECT id, owner_type, owner_id, user_id, object_key, content_type, filename, position, confirmed_at, created_at\s+FROM file\s+WHERE owner_type = \$1 AND owner_id = \$2 AND confirmed_at IS NOT NULL\s+ORDER BY position ASC`).
		WillReturnRows(sqlmock.NewRows(fileRowColumns))

	body := models.CreateEventRequest{PetID: testPetID, Date: "2024-01-01T10:00:00Z", Type: "weight", Value: eventValue(`{"amount":5}`)}
	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodPost, "/events", body, true)
	CreateEventHandler(w, r)

	assert.Equal(t, http.StatusCreated, w.Code)
	var resp models.EventResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "weight", resp.Type)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetEventHandler_InvalidID(t *testing.T) {
	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodGet, "/events/not-a-uuid", nil, true)
	GetEventHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetEventHandler_NotFound(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT e.id, e.pet_id, e.date_time, e.type, e.notes, e.value, p.name`).
		WillReturnError(sql.ErrNoRows)

	eventID := "44444444-4444-4444-4444-444444444444"
	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodGet, "/events/"+eventID, nil, true)
	GetEventHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetEventHandler_NotOwned(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	eventID := "44444444-4444-4444-4444-444444444444"
	mock.ExpectQuery(`SELECT e.id, e.pet_id, e.date_time, e.type, e.notes, e.value, p.name`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value", "name"}).
			AddRow(eventID, testPetID, time.Now(), "weight", nil, []byte(`{"amount":5}`), "Rex"))
	mock.ExpectQuery(`SELECT COUNT\(1\) FROM pet WHERE id = \$1 AND user_id = \$2`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodGet, "/events/"+eventID, nil, true)
	GetEventHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetEventHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	eventID := "44444444-4444-4444-4444-444444444444"
	mock.ExpectQuery(`SELECT e.id, e.pet_id, e.date_time, e.type, e.notes, e.value, p.name`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value", "name"}).
			AddRow(eventID, testPetID, time.Now(), "weight", nil, []byte(`{"amount":5}`), "Rex"))
	mock.ExpectQuery(`SELECT COUNT\(1\) FROM pet WHERE id = \$1 AND user_id = \$2`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT id, owner_type, owner_id, user_id, object_key, content_type, filename, position, confirmed_at, created_at\s+FROM file\s+WHERE owner_type = \$1 AND owner_id = \$2 AND confirmed_at IS NOT NULL\s+ORDER BY position ASC`).
		WillReturnRows(sqlmock.NewRows(fileRowColumns))

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodGet, "/events/"+eventID, nil, true)
	GetEventHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.EventResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "weight", resp.Type)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestGetEventHandler_WithFiles проверяет, что поле `files` заполняется
// подтверждёнными файлами события (owner_type = event_file), в порядке
// position, с presigned GET URL — см. «Файлы события — Backend», раздел
// «Чтение».
func TestGetEventHandler_WithFiles(t *testing.T) {
	mock := setupMockDB(t)
	setupFakeStorage(t)
	expectTokensValid(mock, testUserID)
	eventID := "44444444-4444-4444-4444-444444444444"
	mock.ExpectQuery(`SELECT e.id, e.pet_id, e.date_time, e.type, e.notes, e.value, p.name`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value", "name"}).
			AddRow(eventID, testPetID, time.Now(), "weight", nil, []byte(`{"amount":5}`), "Rex"))
	mock.ExpectQuery(`SELECT COUNT\(1\) FROM pet WHERE id = \$1 AND user_id = \$2`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	docFileID := "99999999-9999-9999-9999-999999999999"
	docFilename := "analysis.pdf"
	mock.ExpectQuery(`SELECT id, owner_type, owner_id, user_id, object_key, content_type, filename, position, confirmed_at, created_at\s+FROM file\s+WHERE owner_type = \$1 AND owner_id = \$2 AND confirmed_at IS NOT NULL\s+ORDER BY position ASC`).
		WillReturnRows(sqlmock.NewRows(fileRowColumns).AddRow(
			docFileID, "event_file", eventID, testUserID, "event_file/"+eventID+"/"+docFileID, "application/pdf", docFilename, 0, time.Now(), time.Now(),
		))

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodGet, "/events/"+eventID, nil, true)
	GetEventHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.EventResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Files, 1)
	assert.Equal(t, docFileID, resp.Files[0].FileID)
	assert.Equal(t, fakePresignedGetURL, resp.Files[0].URL)
	assert.Equal(t, "application/pdf", resp.Files[0].ContentType)
	require.NotNil(t, resp.Files[0].Filename)
	assert.Equal(t, docFilename, *resp.Files[0].Filename)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteEventHandler_NotFound(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	eventID := "44444444-4444-4444-4444-444444444444"
	mock.ExpectQuery(`SELECT e.id, e.pet_id, e.date_time, e.type, e.notes, e.value, p.name`).
		WillReturnError(sql.ErrNoRows)

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodDelete, "/events/"+eventID, nil, true)
	DeleteEventHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestDeleteEventHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	eventID := "44444444-4444-4444-4444-444444444444"
	mock.ExpectQuery(`SELECT e.id, e.pet_id, e.date_time, e.type, e.notes, e.value, p.name`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value", "name"}).
			AddRow(eventID, testPetID, time.Now(), "weight", nil, []byte(`{"amount":5}`), "Rex"))
	mock.ExpectQuery(`SELECT COUNT\(1\) FROM pet WHERE id = \$1 AND user_id = \$2`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectExec(`UPDATE event SET deleted_at = \$1 WHERE id = \$2 AND deleted_at IS NULL`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodDelete, "/events/"+eventID, nil, true)
	DeleteEventHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateEventHandler_MissingPetID(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	eventID := "44444444-4444-4444-4444-444444444444"
	mock.ExpectQuery(`SELECT id, pet_id, date_time, type, notes, value\s+FROM event\s+WHERE id = \$1`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value"}).
			AddRow(eventID, testPetID, time.Now(), "weight", nil, []byte(`{"amount":5}`)))

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodPatch, "/events/"+eventID, models.UpdateEventRequest{}, true)
	UpdateEventHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateEventHandler_NoFieldsToUpdate(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	eventID := "44444444-4444-4444-4444-444444444444"
	mock.ExpectQuery(`SELECT id, pet_id, date_time, type, notes, value\s+FROM event\s+WHERE id = \$1`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value"}).
			AddRow(eventID, testPetID, time.Now(), "weight", nil, []byte(`{"amount":5}`)))

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodPatch, "/events/"+eventID, models.UpdateEventRequest{PetID: testPetID}, true)
	UpdateEventHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateEventHandler_TypeWithoutValue(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	eventID := "44444444-4444-4444-4444-444444444444"
	mock.ExpectQuery(`SELECT id, pet_id, date_time, type, notes, value\s+FROM event\s+WHERE id = \$1`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value"}).
			AddRow(eventID, testPetID, time.Now(), "weight", nil, []byte(`{"amount":5}`)))

	newType := "other"
	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodPatch, "/events/"+eventID, models.UpdateEventRequest{PetID: testPetID, Type: &newType}, true)
	UpdateEventHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateEventHandler_ValueInvalidForCurrentType(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	eventID := "44444444-4444-4444-4444-444444444444"
	mock.ExpectQuery(`SELECT id, pet_id, date_time, type, notes, value\s+FROM event\s+WHERE id = \$1`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value"}).
			AddRow(eventID, testPetID, time.Now(), "weight", nil, []byte(`{"amount":5}`)))
	mock.ExpectQuery(`SELECT COUNT\(1\) FROM pet WHERE id = \$1 AND user_id = \$2`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, "DOG",
		))

	newValue := eventValuePtr(`{"amount":500}`)
	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodPatch, "/events/"+eventID, models.UpdateEventRequest{PetID: testPetID, Value: newValue}, true)
	UpdateEventHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateEventHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	eventID := "44444444-4444-4444-4444-444444444444"
	mock.ExpectQuery(`SELECT id, pet_id, date_time, type, notes, value\s+FROM event\s+WHERE id = \$1`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value"}).
			AddRow(eventID, testPetID, time.Now(), "weight", nil, []byte(`{"amount":5}`)))
	mock.ExpectQuery(`SELECT COUNT\(1\) FROM pet WHERE id = \$1 AND user_id = \$2`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, "DOG",
		))
	mock.ExpectExec(`UPDATE event SET`).WillReturnResult(sqlmock.NewResult(0, 1))

	newValue := eventValuePtr(`{"amount":6}`)
	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodPatch, "/events/"+eventID, models.UpdateEventRequest{PetID: testPetID, Value: newValue}, true)
	UpdateEventHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}
