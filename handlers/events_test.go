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
			AddRow(testPetID, testPetID, eventDate, "weight", nil, "5kg"))

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
	body := models.CreateEventRequest{PetID: testPetID, Date: "2024-01-01T10:00:00Z", Type: "bogus", Value: "1"}
	r := eventRequest(t, http.MethodPost, "/events", body, true)
	CreateEventHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateEventHandler_InvalidPetID(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	body := models.CreateEventRequest{PetID: "not-a-uuid", Date: "2024-01-01T10:00:00Z", Type: "weight", Value: "1"}
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
		{"weight not numeric", "weight", "4.5kg"},
		{"weight out of range", "weight", "500"},
		{"weight below min", "weight", "0"},
		{"status not in enum", "urine", "a lot"},
		{"other too long", "other", strings.Repeat("x", 51)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)

			body := models.CreateEventRequest{PetID: testPetID, Date: "2024-01-01T10:00:00Z", Type: c.typ, Value: c.value}
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
		{"weight valid", "weight", "4.5"},
		{"weight max boundary", "weight", "400"},
		{"status normal", "urine", "normal"},
		{"status abnormal", "vomit", "abnormal"},
		{"other valid", "other", "рвота после еды"},
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
					AddRow(eventID, testPetID, time.Now(), c.typ, nil, c.value, "Rex"))

			body := models.CreateEventRequest{PetID: testPetID, Date: "2024-01-01T10:00:00Z", Type: c.typ, Value: c.value}
			w := httptest.NewRecorder()
			r := eventRequest(t, http.MethodPost, "/events", body, true)
			CreateEventHandler(w, r)
			assert.Equal(t, http.StatusCreated, w.Code, c.name)
		})
	}
}

func TestCreateEventHandler_InvalidIdempotencyKey(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	body := models.CreateEventRequest{PetID: testPetID, Date: "2024-01-01T10:00:00Z", Type: "weight", Value: "5"}
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
			AddRow(existingEventID, testPetID, time.Now(), "weight", nil, "5", "Rex"))

	key := "11111111-1111-4111-8111-111111111111"
	body := models.CreateEventRequest{PetID: testPetID, Date: "2024-01-01T10:00:00Z", Type: "weight", Value: "5"}
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

	body := models.CreateEventRequest{PetID: testPetID, Date: "2024-01-01T10:00:00Z", Type: "weight", Value: "1"}
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

	body := models.CreateEventRequest{PetID: testPetID, Date: "2024-01-01T10:00:00Z", Type: "weight", Value: "1"}
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
			AddRow(eventID, testPetID, time.Now(), "weight", nil, "5", "Rex"))

	body := models.CreateEventRequest{PetID: testPetID, Date: "2024-01-01T10:00:00Z", Type: "weight", Value: "5"}
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
			AddRow(eventID, testPetID, time.Now(), "weight", nil, "5kg", "Rex"))
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
			AddRow(eventID, testPetID, time.Now(), "weight", nil, "5kg", "Rex"))
	mock.ExpectQuery(`SELECT COUNT\(1\) FROM pet WHERE id = \$1 AND user_id = \$2`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodGet, "/events/"+eventID, nil, true)
	GetEventHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.EventResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "weight", resp.Type)
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
			AddRow(eventID, testPetID, time.Now(), "weight", nil, "5kg", "Rex"))
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
			AddRow(eventID, testPetID, time.Now(), "weight", nil, "5kg"))

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
			AddRow(eventID, testPetID, time.Now(), "weight", nil, "5kg"))

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
			AddRow(eventID, testPetID, time.Now(), "weight", nil, "5"))

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
			AddRow(eventID, testPetID, time.Now(), "weight", nil, "5"))
	mock.ExpectQuery(`SELECT COUNT\(1\) FROM pet WHERE id = \$1 AND user_id = \$2`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, "DOG",
		))

	newValue := "not-a-number"
	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodPatch, "/events/"+eventID, models.UpdateEventRequest{PetID: testPetID, Value: &newValue}, true)
	UpdateEventHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateEventHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	eventID := "44444444-4444-4444-4444-444444444444"
	mock.ExpectQuery(`SELECT id, pet_id, date_time, type, notes, value\s+FROM event\s+WHERE id = \$1`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "date_time", "type", "notes", "value"}).
			AddRow(eventID, testPetID, time.Now(), "weight", nil, "5kg"))
	mock.ExpectQuery(`SELECT COUNT\(1\) FROM pet WHERE id = \$1 AND user_id = \$2`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, "DOG",
		))
	mock.ExpectExec(`UPDATE event SET`).WillReturnResult(sqlmock.NewResult(0, 1))

	newValue := "6"
	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodPatch, "/events/"+eventID, models.UpdateEventRequest{PetID: testPetID, Value: &newValue}, true)
	UpdateEventHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}
