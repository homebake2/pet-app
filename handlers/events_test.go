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
	"github.com/google/uuid"
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
		// tz обязателен: без него 400 даже при остальных валидных параметрах.
		"/activities?from=2024-01-01&to=2024-01-02&pet_id=" + testPetID,
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
	path := "/activities?from=2024-01-01&to=2024-01-02&tz=UTC&pet_id=" + testPetID
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
	mock.ExpectQuery(`SELECT e.id, e.user_id, e.date_time, e.type, e.notes, e.value\s+FROM event e\s+JOIN event_pet ep ON ep.event_id = e.id\s+WHERE ep.pet_id = \$1`).
		WillReturnRows(sqlmock.NewRows(eventColumnNames).
			AddRow(testEventID, testUserID, eventDate, "weight", nil, []byte(`{"amount":5}`)))
	expectEventPets(mock, testEventID, testPet{ID: testPetID, Name: "Rex", Species: "DOG"})
	mock.ExpectQuery(`SELECT owner_id, COUNT\(\*\) FROM file\s+WHERE owner_type = \$1 AND owner_id = ANY\(\$2\) AND confirmed_at IS NOT NULL\s+GROUP BY owner_id`).
		WillReturnRows(sqlmock.NewRows([]string{"owner_id", "count"}))

	w := httptest.NewRecorder()
	path := "/activities?from=2024-01-01&to=2024-01-02&tz=UTC&pet_id=" + testPetID
	GetActivitiesHandler(w, eventRequest(t, http.MethodGet, path, nil, true))

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.ActivitiesResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Rex", resp.PetName)
	require.Len(t, resp.Items, 2)
	require.Len(t, resp.Items[0].Events, 1)
	assert.Equal(t, "weight", resp.Items[0].Events[0].Type)
	require.Len(t, resp.Items[0].Events[0].Pets, 1)
	assert.Equal(t, models.EventPetRef{PetID: testPetID, PetName: "Rex"}, resp.Items[0].Events[0].Pets[0])
	require.NoError(t, mock.ExpectationsWereMet())
}

// UTC+3: событие 1 января в 22:30Z — это уже 2 января по Москве, и в ответе
// оно должно оказаться под 2024-01-02; границы запроса — полночь по Москве.
func TestGetActivitiesHandler_GroupsByClientTimeZone(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT COUNT\(1\) FROM pet WHERE id = \$1 AND user_id = \$2`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`SELECT name FROM pet WHERE id = \$1`).
		WillReturnRows(sqlmock.NewRows([]string{"name"}).AddRow("Rex"))
	mock.ExpectQuery(`SELECT e.id, e.user_id, e.date_time, e.type, e.notes, e.value\s+FROM event e\s+JOIN event_pet ep ON ep.event_id = e.id\s+WHERE ep.pet_id = \$1`).
		WithArgs(sqlmock.AnyArg(),
			timeArg(time.Date(2023, 12, 31, 21, 0, 0, 0, time.UTC)),
			timeArg(time.Date(2024, 1, 2, 21, 0, 0, 0, time.UTC))).
		WillReturnRows(sqlmock.NewRows(eventColumnNames).
			AddRow(testEventID, testUserID, time.Date(2024, 1, 1, 22, 30, 0, 0, time.UTC), "weight", nil, []byte(`{"amount":5}`)))
	expectEventPets(mock, testEventID, testPet{ID: testPetID, Name: "Rex", Species: "DOG"})
	mock.ExpectQuery(`SELECT owner_id, COUNT\(\*\) FROM file`).
		WillReturnRows(sqlmock.NewRows([]string{"owner_id", "count"}))

	w := httptest.NewRecorder()
	path := "/activities?from=2024-01-01&to=2024-01-02&tz=Europe/Moscow&pet_id=" + testPetID
	GetActivitiesHandler(w, eventRequest(t, http.MethodGet, path, nil, true))

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.ActivitiesResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 2)
	assert.Equal(t, "2024-01-01", resp.Items[0].Date)
	assert.Empty(t, resp.Items[0].Events)
	assert.Equal(t, "2024-01-02", resp.Items[1].Date)
	require.Len(t, resp.Items[1].Events, 1)
	assert.Equal(t, "2024-01-01T22:30:00Z", resp.Items[1].Events[0].Date)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetActivitiesHandler_InvalidTimeZone(t *testing.T) {
	w := httptest.NewRecorder()
	path := "/activities?from=2024-01-01&to=2024-01-02&tz=Bad/Zone&pet_id=" + testPetID
	GetActivitiesHandler(w, eventRequest(t, http.MethodGet, path, nil, true))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func createBody(petIDs []string, eventType, value string) models.CreateEventRequest {
	return models.CreateEventRequest{PetIDs: petIDs, Date: "2024-01-01T10:00:00Z", Type: eventType, Value: eventValue(value)}
}

func postEvent(t *testing.T, body models.CreateEventRequest) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	CreateEventHandler(w, eventRequest(t, http.MethodPost, "/events", body, true))
	return w
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
	w := postEvent(t, models.CreateEventRequest{})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateEventHandler_InvalidType(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	w := postEvent(t, createBody([]string{testPetID}, "bogus", `{"amount":5}`))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// pet_ids: не UUID, повторы, больше 10 элементов, пустой массив — 400 до
// обращения к БД.
func TestCreateEventHandler_InvalidPetIDs(t *testing.T) {
	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = uuid.NewString()
	}
	cases := map[string][]string{
		"не UUID":   {"not-a-uuid"},
		"повторы":   {testPetID, testPetID},
		"более 10":  eleven,
		"пустой":    {},
		"nil-набор": nil,
	}
	for name, ids := range cases {
		t.Run(name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			w := postEvent(t, createBody(ids, "weight", `{"amount":5}`))
			assert.Equal(t, http.StatusBadRequest, w.Code)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
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
			w := postEvent(t, createBody([]string{testPetID}, c.typ, c.value))
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
			// Вид вне справочника трактуется как OTHER — применим ко всем
			// типам и значениям словарей.
			anyPet := testPet{ID: testPetID, Name: "Rex", Species: "dog"}
			expectOwnedPets(mock, anyPet)
			expectEventCreated(mock, testEventID, c.typ, c.value, anyPet)

			w := postEvent(t, createBody([]string{testPetID}, c.typ, c.value))
			assert.Equal(t, http.StatusCreated, w.Code, c.name)

			var resp models.EventResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.JSONEq(t, c.value, string(resp.Value))
			assert.Equal(t, petRefsOf(petRex), resp.Pets)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCreateEventHandler_InvalidIdempotencyKey(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodPost, "/events", createBody([]string{testPetID}, "weight", `{"amount":5}`), true)
	r.Header.Set("Idempotency-Key", "not-a-uuid")
	CreateEventHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// Дедупликация по (user_id, idempotency_key): повтор возвращает ранее
// созданное событие со всеми питомцами и ничего не вставляет.
func TestCreateEventHandler_IdempotencyKey_ReturnsExisting(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPets(mock, petRex, petTom)
	existingEventID := "55555555-5555-5555-5555-555555555555"
	mock.ExpectQuery(eventIdempotencySQL).
		WithArgs(testUserID, "11111111-1111-4111-8111-111111111111").
		WillReturnRows(eventRow(existingEventID, "feeding", `{"amount":5,"unit":"g","food":"dry"}`, time.Now()))
	expectEventPets(mock, existingEventID, petRex, petTom)
	expectEventFilesEmpty(mock)

	w := httptest.NewRecorder()
	r := eventRequest(t, http.MethodPost, "/events", createBody(petIDsOfTest(petRex, petTom), "feeding", `{"amount":5,"unit":"g","food":"dry"}`), true)
	r.Header.Set("Idempotency-Key", "11111111-1111-4111-8111-111111111111")
	CreateEventHandler(w, r)

	assert.Equal(t, http.StatusCreated, w.Code)
	var resp models.EventResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, existingEventID, resp.ID)
	assert.Equal(t, petRefsOf(petRex, petTom), resp.Pets)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateEventHandler_PetNotFound(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPets(mock)

	w := postEvent(t, createBody([]string{testPetID}, "weight", `{"amount":5}`))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// Один из нескольких питомцев чужой — 404 без раскрытия, какой именно.
func TestCreateEventHandler_OneOfPetsNotOwned_NotFound(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPets(mock, petRex)

	w := postEvent(t, createBody([]string{testPetID, testOtherPet}, "feeding", `{"amount":5,"unit":"g","food":"dry"}`))
	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateEventHandler_PetDeleted(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	deleted := petTom
	deleted.Deleted = true
	expectOwnedPets(mock, petRex, deleted)

	w := postEvent(t, createBody(petIDsOfTest(petRex, petTom), "feeding", `{"amount":5,"unit":"g","food":"dry"}`))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// heat_cycle неприменим к FISH.
func TestCreateEventHandler_TypeNotApplicableToPetSpecies(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPets(mock, petNemo)

	w := postEvent(t, createBody([]string{testPetID3}, "heat_cycle", `{"phase":"started"}`))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Пересечение допустимых типов: heat_cycle применим к DOG и CAT, но не к
// FISH — с рыбкой в наборе тип недоступен.
func TestCreateEventHandler_MultiplePets_TypeIntersection(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPets(mock, petRex, petNemo)

	w := postEvent(t, createBody(petIDsOfTest(petRex, petNemo), "heat_cycle", `{"phase":"started"}`))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateEventHandler_MultiplePets_IntersectionSuccess(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPets(mock, petRex, petTom)
	expectEventCreated(mock, testEventID, "heat_cycle", `{"phase":"started"}`, petRex, petTom)

	w := postEvent(t, createBody(petIDsOfTest(petRex, petTom), "heat_cycle", `{"phase":"started"}`))
	assert.Equal(t, http.StatusCreated, w.Code)
	var resp models.EventResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, petRefsOf(petRex, petTom), resp.Pets)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Измерительные типы (weight, temperature, water_quality) допускают ровно
// одного питомца.
func TestCreateEventHandler_MeasurementTypeWithMultiplePets(t *testing.T) {
	cases := map[string]string{
		"weight":        `{"amount":5}`,
		"temperature":   `{"amount":38.5,"kind":"body"}`,
		"water_quality": `{"ph":7}`,
	}
	for eventType, value := range cases {
		t.Run(eventType, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			expectOwnedPets(mock, petRex, petTom)

			w := postEvent(t, createBody(petIDsOfTest(petRex, petTom), eventType, value))
			assert.Equal(t, http.StatusBadRequest, w.Code)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// Значение вложенного словаря должно быть применимо к виду каждого питомца:
// water_change применим к FISH, но не к DOG.
func TestCreateEventHandler_NestedValueMustApplyToEveryPet(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPets(mock, petNemo, petRex)

	w := postEvent(t, createBody(petIDsOfTest(petNemo, petRex), "hygiene", `{"procedure":"water_change"}`))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateEventHandler_NestedValueApplicableToSinglePet(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPets(mock, petNemo)
	expectEventCreated(mock, testEventID, "hygiene", `{"procedure":"water_change"}`, petNemo)

	w := postEvent(t, createBody([]string{testPetID3}, "hygiene", `{"procedure":"water_change"}`))
	assert.Equal(t, http.StatusCreated, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Питомец со species вне закрытого справочника трактуется как OTHER —
// применим к любому типу, в том числе к water_quality.
func TestCreateEventHandler_UnknownSpeciesDefaultsToOtherApplicability(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	odd := testPet{ID: testPetID, Name: "Барсик", Species: "необычный питомец"}
	expectOwnedPets(mock, odd)
	expectEventCreated(mock, testEventID, "water_quality", `{"ph":7}`, odd)

	w := postEvent(t, createBody([]string{testPetID}, "water_quality", `{"ph":7}`))
	assert.Equal(t, http.StatusCreated, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateEventHandler_FeedingFoodNotApplicableToPetSpecies(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPets(mock, petRex)

	w := postEvent(t, createBody([]string{testPetID}, "feeding", `{"amount":10,"unit":"g","food":"live_prey"}`))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateEventHandler_ActivityKindNotApplicableToPetSpecies(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPets(mock, petNemo)

	w := postEvent(t, createBody([]string{testPetID3}, "activity", `{"duration_min":10,"kind":"swim"}`))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateEventHandler_Success_MultiplePets(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPets(mock, petRex, petTom, petNemo)
	expectEventCreated(mock, testEventID, "feeding", `{"amount":5,"unit":"g","food":"dry"}`, petRex, petTom, petNemo)

	w := postEvent(t, createBody(petIDsOfTest(allPets3...), "feeding", `{"amount":5,"unit":"g","food":"dry"}`))
	assert.Equal(t, http.StatusCreated, w.Code)
	var resp models.EventResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, petRefsOf(allPets3...), resp.Pets)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetEventHandler_InvalidID(t *testing.T) {
	w := httptest.NewRecorder()
	GetEventHandler(w, eventRequest(t, http.MethodGet, "/events/not-a-uuid", nil, true))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// Чужое событие (user_id не совпадает) неотличимо от несуществующего.
func TestGetEventHandler_NotFoundOrNotOwned(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(eventForUserQuery).WillReturnError(sql.ErrNoRows)

	w := httptest.NewRecorder()
	GetEventHandler(w, eventRequest(t, http.MethodGet, "/events/"+testEventID, nil, true))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// Запись без видимых питомцев нигде не показывается.
func TestGetEventHandler_NoVisiblePets_NotFound(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	deleted := petRex
	deleted.Deleted = true
	expectEventForUser(mock, testEventID, "weight", `{"amount":5}`, deleted)

	w := httptest.NewRecorder()
	GetEventHandler(w, eventRequest(t, http.MethodGet, "/events/"+testEventID, nil, true))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// Запись остаётся видимой, пока есть хотя бы один видимый питомец; в списке
// питомцев отдаются только видимые.
func TestGetEventHandler_ReturnsOnlyVisiblePets(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	deleted := petTom
	deleted.Deleted = true
	expectEventForUser(mock, testEventID, "feeding", `{"amount":5,"unit":"g","food":"dry"}`, petRex, deleted)
	expectEventFilesEmpty(mock)

	w := httptest.NewRecorder()
	GetEventHandler(w, eventRequest(t, http.MethodGet, "/events/"+testEventID, nil, true))

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.EventResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, petRefsOf(petRex), resp.Pets)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetEventHandler_WithFiles(t *testing.T) {
	mock := setupMockDB(t)
	setupFakeStorage(t)
	expectTokensValid(mock, testUserID)
	expectEventForUser(mock, testEventID, "weight", `{"amount":5}`, petRex)
	docFileID := "99999999-9999-9999-9999-999999999999"
	docFilename := "analysis.pdf"
	mock.ExpectQuery(filesByOwnerSQL).
		WillReturnRows(sqlmock.NewRows(fileRowColumns).AddRow(
			docFileID, "event_file", testEventID, testUserID, "event_file/"+testEventID+"/"+docFileID, "application/pdf", docFilename, 0, time.Now(), time.Now(),
		))

	w := httptest.NewRecorder()
	GetEventHandler(w, eventRequest(t, http.MethodGet, "/events/"+testEventID, nil, true))

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.EventResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Files, 1)
	assert.Equal(t, docFileID, resp.Files[0].FileID)
	assert.Equal(t, fakePresignedGetURL, resp.Files[0].URL)
	require.NotNil(t, resp.Files[0].Filename)
	assert.Equal(t, docFilename, *resp.Files[0].Filename)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteEventHandler_NotFound(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(eventForUserQuery).WillReturnError(sql.ErrNoRows)

	w := httptest.NewRecorder()
	DeleteEventHandler(w, eventRequest(t, http.MethodDelete, "/events/"+testEventID, nil, true))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// Событие удаляется целиком, в том числе когда все его питомцы мягко удалены.
func TestDeleteEventHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	deleted := petRex
	deleted.Deleted = true
	expectEventForUser(mock, testEventID, "weight", `{"amount":5}`, deleted)
	mock.ExpectExec(`UPDATE event SET deleted_at = \$1 WHERE id = \$2 AND deleted_at IS NULL`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	DeleteEventHandler(w, eventRequest(t, http.MethodDelete, "/events/"+testEventID, nil, true))

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func patchEvent(t *testing.T, body models.UpdateEventRequest) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	UpdateEventHandler(w, eventRequest(t, http.MethodPatch, "/events/"+testEventID, body, true))
	return w
}

func petIDsPtr(pets ...testPet) *[]string {
	ids := petIDsOfTest(pets...)
	return &ids
}

func TestUpdateEventHandler_NotFound(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(eventForUserQuery).WillReturnError(sql.ErrNoRows)

	w := patchEvent(t, models.UpdateEventRequest{PetIDs: petIDsPtr(petRex)})
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdateEventHandler_NoVisiblePets_NotFound(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	deleted := petRex
	deleted.Deleted = true
	expectEventForUser(mock, testEventID, "weight", `{"amount":5}`, deleted)

	notes := "x"
	w := patchEvent(t, models.UpdateEventRequest{Notes: &notes})
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdateEventHandler_NoFieldsToUpdate(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectEventForUser(mock, testEventID, "weight", `{"amount":5}`, petRex)

	w := patchEvent(t, models.UpdateEventRequest{})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateEventHandler_TypeWithoutValue(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectEventForUser(mock, testEventID, "weight", `{"amount":5}`, petRex)

	newType := "other"
	w := patchEvent(t, models.UpdateEventRequest{Type: &newType})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateEventHandler_ValueInvalidForCurrentType(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectEventForUser(mock, testEventID, "weight", `{"amount":5}`, petRex)

	w := patchEvent(t, models.UpdateEventRequest{Value: eventValuePtr(`{"amount":500}`)})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// Новый type проверяется по видам питомцев события.
func TestUpdateEventHandler_TypeNotApplicableToPetSpecies(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectEventForUser(mock, testEventID, "weight", `{"amount":5}`, petNemo)

	newType := "heat_cycle"
	w := patchEvent(t, models.UpdateEventRequest{Type: &newType, Value: eventValuePtr(`{"phase":"started"}`)})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Смена type на измерительный у события с несколькими питомцами — 400.
func TestUpdateEventHandler_MeasurementTypeWithMultiplePets(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectEventForUser(mock, testEventID, "feeding", `{"amount":5,"unit":"g","food":"dry"}`, petRex, petTom)

	newType := "weight"
	w := patchEvent(t, models.UpdateEventRequest{Type: &newType, Value: eventValuePtr(`{"amount":5}`)})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Если ни type, ни value, ни набор питомцев не меняются, применимость
// сохранённого типа не перепроверяется.
func TestUpdateEventHandler_TypeUnchangedApplicabilityNotChecked(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectEventForUser(mock, testEventID, "heat_cycle", `{"phase":"started"}`, petNemo)
	mock.ExpectBegin()
	expectEventForUser(mock, testEventID, "heat_cycle", `{"phase":"started"}`, petNemo)
	mock.ExpectExec(`UPDATE event SET`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	notes := "заметка"
	w := patchEvent(t, models.UpdateEventRequest{Notes: &notes})
	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateEventHandler_HygieneProcedureNotApplicableToPetSpecies(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectEventForUser(mock, testEventID, "hygiene", `{"procedure":"water_change"}`, petNemo)

	w := patchEvent(t, models.UpdateEventRequest{Value: eventValuePtr(`{"procedure":"brushing"}`)})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateEventHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectEventForUser(mock, testEventID, "weight", `{"amount":5}`, petRex)
	mock.ExpectBegin()
	expectEventForUser(mock, testEventID, "weight", `{"amount":5}`, petRex)
	mock.ExpectExec(`UPDATE event SET`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	w := patchEvent(t, models.UpdateEventRequest{Value: eventValuePtr(`{"amount":6}`)})
	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Привязка питомца: проверки по итоговому набору (в том числе для
// сохранённых type/value), затем в одной транзакции — связи.
func TestUpdateEventHandler_AttachPet(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectEventForUser(mock, testEventID, "feeding", `{"amount":5,"unit":"g","food":"dry"}`, petRex)
	expectOwnedPets(mock, petRex, petTom)
	mock.ExpectBegin()
	expectEventForUser(mock, testEventID, "feeding", `{"amount":5,"unit":"g","food":"dry"}`, petRex)
	mock.ExpectExec(`DELETE FROM event_pet link`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`INSERT INTO event_pet`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	w := patchEvent(t, models.UpdateEventRequest{PetIDs: petIDsPtr(petRex, petTom)})
	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Отвязка: питомец убирается из набора, событие остаётся у остальных.
func TestUpdateEventHandler_DetachPet(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectEventForUser(mock, testEventID, "feeding", `{"amount":5,"unit":"g","food":"dry"}`, petRex, petTom)
	expectOwnedPets(mock, petTom)
	mock.ExpectBegin()
	expectEventForUser(mock, testEventID, "feeding", `{"amount":5,"unit":"g","food":"dry"}`, petRex, petTom)
	mock.ExpectExec(`DELETE FROM event_pet link`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO event_pet`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	w := patchEvent(t, models.UpdateEventRequest{PetIDs: petIDsPtr(petTom)})
	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Привязка питомца, к виду которого сохранённый тип неприменим, — 400.
func TestUpdateEventHandler_AttachPet_StoredTypeNotApplicable(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectEventForUser(mock, testEventID, "heat_cycle", `{"phase":"started"}`, petRex)
	expectOwnedPets(mock, petRex, petNemo)

	w := patchEvent(t, models.UpdateEventRequest{PetIDs: petIDsPtr(petRex, petNemo)})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Привязка второго питомца к событию с измерительным типом — 400.
func TestUpdateEventHandler_AttachPet_MeasurementType(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectEventForUser(mock, testEventID, "weight", `{"amount":5}`, petRex)
	expectOwnedPets(mock, petRex, petTom)

	w := patchEvent(t, models.UpdateEventRequest{PetIDs: petIDsPtr(petRex, petTom)})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Пустой набор, повторы, потолок 10 — 400; событие не остаётся без питомцев.
func TestUpdateEventHandler_InvalidPetIDs(t *testing.T) {
	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = uuid.NewString()
	}
	cases := map[string][]string{
		"пустой":   {},
		"повторы":  {testPetID, testPetID},
		"более 10": eleven,
		"не UUID":  {"nope"},
	}
	for name, ids := range cases {
		t.Run(name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			expectEventForUser(mock, testEventID, "weight", `{"amount":5}`, petRex)

			ids := ids
			w := patchEvent(t, models.UpdateEventRequest{PetIDs: &ids})
			assert.Equal(t, http.StatusBadRequest, w.Code)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUpdateEventHandler_AttachPet_NotOwned_NotFound(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectEventForUser(mock, testEventID, "feeding", `{"amount":5,"unit":"g","food":"dry"}`, petRex)
	expectOwnedPets(mock, petRex)

	ids := []string{testPetID, testOtherPet}
	w := patchEvent(t, models.UpdateEventRequest{PetIDs: &ids})
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdateEventHandler_AttachPet_Deleted(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectEventForUser(mock, testEventID, "feeding", `{"amount":5,"unit":"g","food":"dry"}`, petRex)
	deleted := petTom
	deleted.Deleted = true
	expectOwnedPets(mock, petRex, deleted)

	w := patchEvent(t, models.UpdateEventRequest{PetIDs: petIDsPtr(petRex, petTom)})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// --- Событие — только факт: дата не позднее now()+5 минут ---

func TestCreateEventHandler_FutureDateRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	body := createBody([]string{testPetID}, "weight", `{"amount":5}`)
	body.Date = time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)
	w := postEvent(t, body)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateEventHandler_DateWithinToleranceAccepted(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPets(mock, petRex)
	expectEventCreated(mock, testEventID, "weight", `{"amount":5}`, petRex)

	body := createBody([]string{testPetID}, "weight", `{"amount":5}`)
	body.Date = time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339)
	w := postEvent(t, body)

	assert.Equal(t, http.StatusCreated, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateEventHandler_ResponseHasNoNotificationsField(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectOwnedPets(mock, petRex)
	expectEventCreated(mock, testEventID, "weight", `{"amount":5}`, petRex)

	w := postEvent(t, createBody([]string{testPetID}, "weight", `{"amount":5}`))

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.NotContains(t, w.Body.String(), "notifications_enabled")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateEventHandler_MoveToFutureDateRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectEventForUser(mock, testEventID, "weight", `{"amount":5}`, petRex)

	futureDate := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	w := patchEvent(t, models.UpdateEventRequest{Date: &futureDate})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Если date в запросе нет, сохранённая дата не перепроверяется.
func TestUpdateEventHandler_StoredDateNotRecheckedWhenDateNotSent(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	future := time.Now().Add(48 * time.Hour)
	mock.ExpectQuery(eventForUserQuery).WillReturnRows(eventRow(testEventID, "weight", `{"amount":5}`, future))
	expectEventPets(mock, testEventID, petRex)
	mock.ExpectBegin()
	expectEventForUser(mock, testEventID, "weight", `{"amount":5}`, petRex)
	mock.ExpectExec(`UPDATE event SET`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	w := patchEvent(t, models.UpdateEventRequest{Value: eventValuePtr(`{"amount":6}`)})
	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}
