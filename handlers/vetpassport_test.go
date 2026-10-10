package handlers

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"myauthservice/models"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// timeParse парсит календарную дату "YYYY-MM-DD" для использования в
// AddRow(...) sqlmock — паника при ошибке приемлема в тестовом хелпере.
func timeParse(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

// expectPetOwnedForCreate mocks database.GetPetIdDBByIDAndUserID (used by
// resolvePetForVetPassportCreate) reporting an existing, non-deleted pet
// owned by testUserID.
func expectPetOwnedForCreate(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil))
}

func expectPetBelongsToUser(mock sqlmock.Sqlmock, belongs bool) {
	count := 0
	if belongs {
		count = 1
	}
	mock.ExpectQuery(`SELECT COUNT\(1\) FROM pet WHERE id = \$1 AND user_id = \$2`).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(count))
}

// ---------------------------------------------------------------------------
// Vaccination
// ---------------------------------------------------------------------------

const vaccinationsPath = "/pet/" + testPetID + "/vaccinations"

func TestCreateVaccinationHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnedForCreate(mock)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO vaccination`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("44444444-4444-4444-4444-444444444444"))
	mock.ExpectCommit()

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, vaccinationsPath+"?tz=UTC", models.CreateVaccinationRequest{
		Name:             "Rabies",
		AdministeredDate: "2024-01-01",
	}, true)
	CreateVaccinationHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusCreated, w.Code)
	var resp models.VaccinationCreatedResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "44444444-4444-4444-4444-444444444444", resp.ID)
	assert.Nil(t, resp.AdministeredEventID)
	assert.Nil(t, resp.NextPlanID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateVaccinationHandler_ValidationError(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, vaccinationsPath+"?tz=UTC", models.CreateVaccinationRequest{
		Name:             "",
		AdministeredDate: "2024-01-01",
	}, true)
	CreateVaccinationHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// tz обязателен при создании и редактировании: без него 400, значения по
// умолчанию нет.
func TestCreateVaccinationHandler_MissingTimeZone(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, vaccinationsPath, models.CreateVaccinationRequest{
		Name:             "Rabies",
		AdministeredDate: "2024-01-01",
	}, true)
	CreateVaccinationHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "VALIDATION_ERROR")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateVaccinationHandler_PetNotOwned(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnError(sql.ErrNoRows)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, vaccinationsPath+"?tz=UTC", models.CreateVaccinationRequest{
		Name:             "Rabies",
		AdministeredDate: "2024-01-01",
	}, true)
	CreateVaccinationHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPetVaccinationsHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil))
	expectNoWeightEvent(mock)
	mock.ExpectQuery(`SELECT id, pet_id, name, administered_date, next_date, administered_event_id, next_plan_id, deleted_at\s+FROM vaccination`).
		WillReturnRows(sqlmock.NewRows(vaccinationColumns))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, vaccinationsPath, nil, true)
	GetPetVaccinationsHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.VaccinationListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.Items)
	require.NoError(t, mock.ExpectationsWereMet())
}

var vaccinationSelect = `SELECT id, pet_id, name, administered_date, next_date, administered_event_id, next_plan_id, deleted_at\s+FROM vaccination\s+WHERE id = \$1 AND deleted_at IS NULL`

var vaccinationColumns = []string{"id", "pet_id", "name", "administered_date", "next_date", "administered_event_id", "next_plan_id", "deleted_at"}

const (
	testVaccinationID       = "55555555-5555-5555-5555-555555555555"
	testAdministeredEventID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	testNextPlanID          = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

func TestUpdateVaccinationHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(vaccinationSelect).
		WillReturnRows(sqlmock.NewRows(vaccinationColumns).
			AddRow(testVaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), nil, nil, nil, nil))
	expectPetBelongsToUser(mock, true)
	mock.ExpectBegin()
	mock.ExpectQuery(vaccinationSelect).
		WillReturnRows(sqlmock.NewRows(vaccinationColumns).
			AddRow(testVaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), nil, nil, nil, nil))
	mock.ExpectExec(`UPDATE vaccination SET`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	newName := "Rabies v2"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPatch, "/vaccinations/"+testVaccinationID+"?tz=UTC", models.UpdateVaccinationRequest{Name: &newName}, true)
	VaccinationByIDHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"administered_event_id":null,"next_plan_id":null}`, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateVaccinationHandler_MissingTimeZone(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(vaccinationSelect).
		WillReturnRows(sqlmock.NewRows(vaccinationColumns).
			AddRow(testVaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), nil, nil, nil, nil))
	expectPetBelongsToUser(mock, true)

	newName := "Rabies v2"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPatch, "/vaccinations/"+testVaccinationID, models.UpdateVaccinationRequest{Name: &newName}, true)
	VaccinationByIDHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateVaccinationHandler_InvalidTimeZone(t *testing.T) {
	for _, tz := range []string{"Local", "Nowhere/City"} {
		t.Run(tz, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			mock.ExpectQuery(vaccinationSelect).
				WillReturnRows(sqlmock.NewRows(vaccinationColumns).
					AddRow(testVaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), nil, nil, nil, nil))
			expectPetBelongsToUser(mock, true)

			name := "Rabies v2"
			w := httptest.NewRecorder()
			r := petRequest(t, http.MethodPatch, "/vaccinations/"+testVaccinationID+"?tz="+tz, models.UpdateVaccinationRequest{Name: &name}, true)
			VaccinationByIDHandler(w, r)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUpdateVaccinationHandler_NotOwned(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(vaccinationSelect).
		WillReturnRows(sqlmock.NewRows(vaccinationColumns).
			AddRow(testVaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), nil, nil, nil, nil))
	expectPetBelongsToUser(mock, false)

	newName := "Rabies v2"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPatch, "/vaccinations/"+testVaccinationID+"?tz=UTC", models.UpdateVaccinationRequest{Name: &newName}, true)
	VaccinationByIDHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Факт на дату введения не может быть в будущем (правило факта): 400, ни
// прививка, ни факт не создаются (транзакция откатывается).
func TestCreateVaccinationHandler_FutureAdministeredFactRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnedForCreate(mock)
	mock.ExpectBegin()
	mock.ExpectRollback()

	addEvent := true
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, vaccinationsPath+"?tz=UTC", models.CreateVaccinationRequest{
		Name: "Rabies", AdministeredDate: "2098-01-01", AddEventOnAdministered: &addEvent,
	}, true)
	CreateVaccinationHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Напоминание на next_date должно быть строго в будущем: 400.
func TestCreateVaccinationHandler_PastReminderRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnedForCreate(mock)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO vaccination`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testVaccinationID))
	mock.ExpectRollback()

	addReminder := true
	nextDate := "2019-01-01"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, vaccinationsPath+"?tz=UTC", models.CreateVaccinationRequest{
		Name: "Rabies", AdministeredDate: "2018-01-01", NextDate: &nextDate, AddReminderOnNext: &addReminder,
	}, true)
	CreateVaccinationHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// add_reminder_on_next=true с будущей next_date: создаются настройки
// напоминания (source=vaccination, разовое расписание) с одним напоминанием,
// id настроек записывается в прививку и возвращается как next_plan_id.
func TestCreateVaccinationHandler_ReminderCreatesPlan(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnedForCreate(mock)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO vaccination`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testVaccinationID))
	mock.ExpectExec(`INSERT INTO reminder_plan`).
		WithArgs(sqlmock.AnyArg(), uuid.MustParse(testPetID), "vaccination", uuid.MustParse(testVaccinationID), "other",
			`{"label":"Вакцинация: Rabies"}`, sqlmock.AnyArg(), "once", sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "Europe/Moscow").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO reminder \(id, plan_id, remind_at, notes\)`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), mustRFC3339("2098-02-01T06:30:00Z"), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE vaccination SET administered_event_id = \$1, next_plan_id = \$2 WHERE id = \$3`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	addReminder := true
	nextDate := "2098-02-01"
	eventTime := "09:30"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, vaccinationsPath+"?tz=Europe/Moscow", models.CreateVaccinationRequest{
		Name: "Rabies", AdministeredDate: "2024-01-01", NextDate: &nextDate, AddReminderOnNext: &addReminder, EventTime: &eventTime,
	}, true)
	CreateVaccinationHandler(w, r, uuid.MustParse(testPetID))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var resp models.VaccinationCreatedResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, testVaccinationID, resp.ID)
	assert.NotNil(t, resp.NextPlanID)
	assert.Nil(t, resp.AdministeredEventID)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Факт на дату введения создаётся в поясе клиента: 09:30 по Москве — 06:30Z.
func TestCreateVaccinationHandler_FactInClientTimeZone(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnedForCreate(mock)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO event`).
		WithArgs(uuid.MustParse(testPetID), mustRFC3339("2024-01-01T06:30:00Z"), "other", sqlmock.AnyArg(), `{"label":"Vaccination: Rabies"}`, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testAdministeredEventID))
	mock.ExpectQuery(`INSERT INTO vaccination`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testVaccinationID))
	mock.ExpectCommit()

	addEvent := true
	eventTime := "09:30"
	label := "Vaccination: Rabies"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, vaccinationsPath+"?tz=Europe/Moscow", models.CreateVaccinationRequest{
		Name: "Rabies", AdministeredDate: "2024-01-01", AddEventOnAdministered: &addEvent, EventTime: &eventTime, EventLabel: &label,
	}, true)
	CreateVaccinationHandler(w, r, uuid.MustParse(testPetID))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.JSONEq(t, `{"id":"`+testVaccinationID+`","administered_event_id":"`+testAdministeredEventID+`","next_plan_id":null}`, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// Удаление прививки в одной транзакции мягко удаляет её, связанный факт и
// жёстко — настройки напоминания.
func TestDeleteVaccinationHandler_DeletesLinkedRecords(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(vaccinationSelect).
		WillReturnRows(sqlmock.NewRows(vaccinationColumns).
			AddRow(testVaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), timeParse("2025-01-01"), testAdministeredEventID, testNextPlanID, nil))
	expectPetBelongsToUser(mock, true)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE vaccination SET deleted_at`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE event SET deleted_at`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT id FROM reminder WHERE plan_id = ANY`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(`DELETE FROM file WHERE owner_type = \$1 AND owner_id = ANY\(\$2\) RETURNING object_key`).
		WithArgs(reminderPlanFileOwnerType, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"object_key"}))
	mock.ExpectExec(`DELETE FROM reminder_plan WHERE id = ANY`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodDelete, "/vaccinations/"+testVaccinationID, nil, true)
	VaccinationByIDHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteVaccinationHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(vaccinationSelect).
		WillReturnRows(sqlmock.NewRows(vaccinationColumns).
			AddRow(testVaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), nil, nil, nil, nil))
	expectPetBelongsToUser(mock, true)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE vaccination SET deleted_at`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodDelete, "/vaccinations/"+testVaccinationID, nil, true)
	VaccinationByIDHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func mustRFC3339(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// ---------------------------------------------------------------------------
// Disease
// ---------------------------------------------------------------------------

func TestCreateDiseaseHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnedForCreate(mock)
	mock.ExpectQuery(`INSERT INTO disease`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("66666666-6666-6666-6666-666666666666"))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/diseases", models.CreateDiseaseRequest{
		Name:          "Diabetes",
		DiagnosedDate: "2024-01-01",
		Status:        "active",
	}, true)
	CreateDiseaseHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusCreated, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateDiseaseHandler_InvalidStatus(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/diseases", models.CreateDiseaseRequest{
		Name:          "Diabetes",
		DiagnosedDate: "2024-01-01",
		Status:        "not-a-status",
	}, true)
	CreateDiseaseHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteDiseaseHandler_NotOwned(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	diseaseID := "77777777-7777-7777-7777-777777777777"
	mock.ExpectQuery(`SELECT id, pet_id, name, diagnosed_date, status, note, deleted_at\s+FROM disease\s+WHERE id = \$1 AND deleted_at IS NULL`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "name", "diagnosed_date", "status", "note", "deleted_at"}).
			AddRow(diseaseID, testPetID, "Diabetes", timeParse("2024-01-01"), "active", nil, nil))
	expectPetBelongsToUser(mock, false)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodDelete, "/diseases/"+diseaseID, nil, true)
	DiseaseByIDHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// VetVisit
// ---------------------------------------------------------------------------

func TestCreateVetVisitHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnedForCreate(mock)
	mock.ExpectQuery(`INSERT INTO vet_visit`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("88888888-8888-8888-8888-888888888888"))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/vet-visits", models.CreateVetVisitRequest{
		VisitDate: "2024-01-01",
		Reason:    "Annual checkup",
	}, true)
	CreateVetVisitHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusCreated, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateVetVisitHandler_MissingReason(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/vet-visits", models.CreateVetVisitRequest{
		VisitDate: "2024-01-01",
		Reason:    "",
	}, true)
	CreateVetVisitHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateVetVisitHandler_NotOwned(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	visitID := "99999999-9999-9999-9999-999999999999"
	mock.ExpectQuery(`SELECT id, pet_id, visit_date, reason, clinic, note, deleted_at\s+FROM vet_visit\s+WHERE id = \$1 AND deleted_at IS NULL`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "visit_date", "reason", "clinic", "note", "deleted_at"}).
			AddRow(visitID, testPetID, timeParse("2024-01-01"), "Checkup", nil, nil, nil))
	expectPetBelongsToUser(mock, false)

	newReason := "Follow-up"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPatch, "/vet-visits/"+visitID, models.UpdateVetVisitRequest{Reason: &newReason}, true)
	VetVisitByIDHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Allergy
// ---------------------------------------------------------------------------

func TestCreateAllergyHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnedForCreate(mock)
	mock.ExpectQuery(`INSERT INTO allergy`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("11111111-1111-1111-1111-111111111112"))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/allergies", models.CreateAllergyRequest{
		Allergen: "Pollen",
		Severity: "mild",
	}, true)
	CreateAllergyHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusCreated, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateAllergyHandler_InvalidSeverity(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/allergies", models.CreateAllergyRequest{
		Allergen: "Pollen",
		Severity: "not-a-severity",
	}, true)
	CreateAllergyHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteAllergyHandler_NotOwned(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	allergyID := "22222222-2222-2222-2222-222222222223"
	mock.ExpectQuery(`SELECT id, pet_id, allergen, reaction, detected_date, severity, note, deleted_at\s+FROM allergy\s+WHERE id = \$1 AND deleted_at IS NULL`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "allergen", "reaction", "detected_date", "severity", "note", "deleted_at"}).
			AddRow(allergyID, testPetID, "Pollen", nil, nil, "mild", nil, nil))
	expectPetBelongsToUser(mock, false)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodDelete, "/allergies/"+allergyID, nil, true)
	AllergyByIDHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Medication
// ---------------------------------------------------------------------------

var medicationColumns = []string{"id", "pet_id", "name", "dosage", "frequency_type", "weekdays", "interval_days", "times", "start_date", "end_date", "reminder_plan_id", "note", "deleted_at", "created_at"}

// medicationSelectRe — regex-фрагмент общего SELECT для medication (см.
// database.medicationSelectColumns), общий для GetMedicationByIDForUpdate.
const medicationSelectRe = `SELECT id, pet_id, name, dosage, frequency_type, weekdays, interval_days, times, start_date, end_date, reminder_plan_id, note, deleted_at, created_at\s+FROM medication\s+WHERE id = \$1 AND deleted_at IS NULL`

const medicationsPath = "/pet/" + testPetID + "/medications"

func addMedicationRow(rows *sqlmock.Rows, id string, weekdays, times any, intervalDays any, startDate, endDate any, reminderPlanID any) *sqlmock.Rows {
	return rows.AddRow(id, testPetID, "Amoxicillin", "1 tablet", "daily", weekdays, intervalDays, times, startDate, endDate, reminderPlanID, nil, nil, timeParse("2024-01-01"))
}

func TestCreateMedicationHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnedForCreate(mock)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO medication`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("33333333-3333-3333-3333-333333333334"))
	mock.ExpectCommit()

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, medicationsPath+"?tz=UTC", models.CreateMedicationRequest{
		Name:          "Amoxicillin",
		Dosage:        "1 tablet",
		FrequencyType: models.MedicationFrequencyDaily,
		Times:         []models.MedicationTimeSlot{{Time: "08:00"}},
		StartDate:     strPtr("2024-01-01"),
	}, true)
	CreateMedicationHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusCreated, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateMedicationHandler_MissingTimeZone(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, medicationsPath, models.CreateMedicationRequest{
		Name:          "Painkiller",
		Dosage:        "1 tablet",
		FrequencyType: models.MedicationFrequencyAsNeeded,
	}, true)
	CreateMedicationHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPetMedicationsHandler_MissingTimeZone(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, medicationsPath, nil, true)
	GetPetMedicationsHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateMedicationHandler_AsNeeded_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnedForCreate(mock)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO medication`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("33333333-3333-3333-3333-333333333338"))
	mock.ExpectCommit()

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, medicationsPath+"?tz=UTC", models.CreateMedicationRequest{
		Name:          "Painkiller",
		Dosage:        "1 tablet",
		FrequencyType: models.MedicationFrequencyAsNeeded,
	}, true)
	CreateMedicationHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusCreated, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateMedicationHandler_WeekdaysNotAllowedForDaily(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, medicationsPath+"?tz=UTC", models.CreateMedicationRequest{
		Name:          "Amoxicillin",
		Dosage:        "1 tablet",
		FrequencyType: models.MedicationFrequencyDaily,
		Weekdays:      []int{1, 2},
		Times:         []models.MedicationTimeSlot{{Time: "08:00"}},
		StartDate:     strPtr("2024-01-01"),
	}, true)
	CreateMedicationHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateMedicationHandler_SpecificDaysMissingWeekdays(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, medicationsPath+"?tz=UTC", models.CreateMedicationRequest{
		Name:          "Amoxicillin",
		Dosage:        "1 tablet",
		FrequencyType: models.MedicationFrequencySpecificDays,
		Times:         []models.MedicationTimeSlot{{Time: "08:00"}},
		StartDate:     strPtr("2024-01-01"),
	}, true)
	CreateMedicationHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateMedicationHandler_EveryNDaysIntervalOutOfRange(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	interval := 400
	r := petRequest(t, http.MethodPost, medicationsPath+"?tz=UTC", models.CreateMedicationRequest{
		Name:          "Amoxicillin",
		Dosage:        "1 tablet",
		FrequencyType: models.MedicationFrequencyEveryNDays,
		IntervalDays:  &interval,
		Times:         []models.MedicationTimeSlot{{Time: "08:00"}},
		StartDate:     strPtr("2024-01-01"),
	}, true)
	CreateMedicationHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateMedicationHandler_AsNeededWithAddReminders(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	addReminders := true
	r := petRequest(t, http.MethodPost, medicationsPath+"?tz=UTC", models.CreateMedicationRequest{
		Name:          "Painkiller",
		Dosage:        "1 tablet",
		FrequencyType: models.MedicationFrequencyAsNeeded,
		AddReminders:  &addReminders,
	}, true)
	CreateMedicationHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// add_reminders=true: в одной транзакции создаются лекарство, настройки
// напоминания (source=medication, type=medication, notes=dosage) и
// напоминания по будущим моментам расписания; время приёма трактуется как
// местное время tz, dose_note становится заметкой напоминания.
func TestCreateMedicationHandler_AddRemindersCreatesPlanAndReminders(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnedForCreate(mock)
	newID := "33333333-3333-3333-3333-333333333339"
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO medication`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(newID))
	mock.ExpectExec(`INSERT INTO reminder_plan`).
		WithArgs(sqlmock.AnyArg(), uuid.MustParse(testPetID), "medication", uuid.MustParse(newID), "medication",
			`{"name":"Amoxicillin"}`, "1 tablet", "daily", sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "Europe/Moscow").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO reminder \(id, plan_id, remind_at, notes\)`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), mustRFC3339("2098-01-01T05:00:00Z"), "2 пипетки").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO reminder \(id, plan_id, remind_at, notes\)`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), mustRFC3339("2098-01-02T05:00:00Z"), "2 пипетки").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE medication SET reminder_plan_id = \$1 WHERE id = \$2`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	w := httptest.NewRecorder()
	addReminders := true
	doseNote := "2 пипетки"
	r := petRequest(t, http.MethodPost, medicationsPath+"?tz=Europe/Moscow", models.CreateMedicationRequest{
		Name:          "Amoxicillin",
		Dosage:        "1 tablet",
		FrequencyType: models.MedicationFrequencyDaily,
		Times:         []models.MedicationTimeSlot{{Time: "08:00", DoseNote: &doseNote}},
		StartDate:     strPtr("2098-01-01"),
		EndDate:       strPtr("2098-01-02"),
		AddReminders:  &addReminders,
	}, true)
	CreateMedicationHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// Если расписание не даёт ни одного будущего момента (курс закончился),
// набор не создаётся, лекарство сохраняется без напоминаний — не ошибка.
func TestCreateMedicationHandler_AddRemindersWithoutFutureMomentsSavesMedicationOnly(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnedForCreate(mock)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO medication`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("33333333-3333-3333-3333-333333333339"))
	mock.ExpectCommit()

	w := httptest.NewRecorder()
	addReminders := true
	r := petRequest(t, http.MethodPost, medicationsPath+"?tz=UTC", models.CreateMedicationRequest{
		Name:          "Amoxicillin",
		Dosage:        "1 tablet",
		FrequencyType: models.MedicationFrequencyDaily,
		Times:         []models.MedicationTimeSlot{{Time: "08:00"}},
		StartDate:     strPtr("2020-01-01"),
		EndDate:       strPtr("2020-01-02"),
		AddReminders:  &addReminders,
	}, true)
	CreateMedicationHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteMedicationHandler_NotOwned(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	medicationID := "33333333-3333-3333-3333-333333333335"
	mock.ExpectQuery(medicationSelectRe).
		WillReturnRows(addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2024-01-01"), nil, nil))
	expectPetBelongsToUser(mock, false)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodDelete, "/medications/"+medicationID, nil, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Удаление лекарства мягко удаляет запись и жёстко — настройки набора
// напоминаний со всеми напоминаниями, в одной транзакции.
func TestDeleteMedicationHandler_HardDeletesReminderPlan(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	medicationID := "33333333-3333-3333-3333-333333333336"
	mock.ExpectQuery(medicationSelectRe).
		WillReturnRows(addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2024-01-01"), nil, testNextPlanID))
	expectPetBelongsToUser(mock, true)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE medication SET deleted_at`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT id FROM reminder WHERE plan_id = ANY`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(`DELETE FROM file WHERE owner_type = \$1 AND owner_id = ANY\(\$2\) RETURNING object_key`).
		WillReturnRows(sqlmock.NewRows([]string{"object_key"}))
	mock.ExpectExec(`DELETE FROM reminder_plan WHERE id = ANY`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodDelete, "/medications/"+medicationID, nil, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// POST /medications/{id}/reminders: набор уже есть — 409.
func TestCreateMedicationRemindersHandler_ConflictWhenPlanExists(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	medicationID := "33333333-3333-3333-3333-333333333337"
	mock.ExpectQuery(medicationSelectRe).
		WillReturnRows(addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2024-01-01"), nil, testNextPlanID))
	expectPetBelongsToUser(mock, true)
	mock.ExpectBegin()
	mock.ExpectQuery(medicationSelectRe).
		WillReturnRows(addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2024-01-01"), nil, testNextPlanID))
	mock.ExpectRollback()

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/medications/"+medicationID+"/reminders?tz=UTC", nil, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusConflict, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateMedicationRemindersHandler_MissingTimeZone(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/medications/33333333-3333-3333-3333-333333333337/reminders", nil, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateMedicationRemindersHandler_AsNeededBadRequest(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	medicationID := "33333333-3333-3333-3333-333333333337"
	asNeededRow := func() *sqlmock.Rows {
		return sqlmock.NewRows(medicationColumns).
			AddRow(medicationID, testPetID, "Painkiller", "1 tab", "as_needed", nil, nil, nil, nil, nil, nil, nil, nil, timeParse("2024-01-01"))
	}
	mock.ExpectQuery(medicationSelectRe).WillReturnRows(asNeededRow())
	expectPetBelongsToUser(mock, true)
	mock.ExpectBegin()
	mock.ExpectQuery(medicationSelectRe).WillReturnRows(asNeededRow())
	mock.ExpectRollback()

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/medications/"+medicationID+"/reminders?tz=UTC", nil, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// Расписание не даёт ни одного будущего момента — явный запрос набора
// отклоняется (в отличие от создания лекарства).
func TestCreateMedicationRemindersHandler_NoFutureMomentsBadRequest(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	medicationID := "33333333-3333-3333-3333-333333333337"
	row := func() *sqlmock.Rows {
		return addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2020-01-01"), timeParse("2020-01-02"), nil)
	}
	mock.ExpectQuery(medicationSelectRe).WillReturnRows(row())
	expectPetBelongsToUser(mock, true)
	mock.ExpectBegin()
	mock.ExpectQuery(medicationSelectRe).WillReturnRows(row())
	mock.ExpectRollback()

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/medications/"+medicationID+"/reminders?tz=UTC", nil, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateMedicationRemindersHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	medicationID := "33333333-3333-3333-3333-333333333337"
	row := func() *sqlmock.Rows {
		return addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2098-01-01"), timeParse("2098-01-02"), nil)
	}
	mock.ExpectQuery(medicationSelectRe).WillReturnRows(row())
	expectPetBelongsToUser(mock, true)
	mock.ExpectBegin()
	mock.ExpectQuery(medicationSelectRe).WillReturnRows(row())
	mock.ExpectExec(`INSERT INTO reminder_plan`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO reminder \(id, plan_id, remind_at, notes\)`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), mustRFC3339("2098-01-01T08:00:00Z"), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO reminder \(id, plan_id, remind_at, notes\)`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), mustRFC3339("2098-01-02T08:00:00Z"), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE medication SET reminder_plan_id`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectQuery(medicationSelectRe).WillReturnRows(row())
	mock.ExpectQuery(`SELECT owner_id, COUNT\(\*\) FROM file`).
		WillReturnRows(sqlmock.NewRows([]string{"owner_id", "count"}))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/medications/"+medicationID+"/reminders?tz=UTC", nil, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp models.MedicationResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, medicationID, resp.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

// DELETE /medications/{id}/reminders: набора нет — 404.
func TestDeleteMedicationRemindersHandler_NotFoundWhenNoPlan(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	medicationID := "33333333-3333-3333-3333-333333333337"
	mock.ExpectQuery(medicationSelectRe).
		WillReturnRows(addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2024-01-01"), nil, nil))
	expectPetBelongsToUser(mock, true)
	mock.ExpectBegin()
	mock.ExpectQuery(medicationSelectRe).
		WillReturnRows(addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2024-01-01"), nil, nil))
	mock.ExpectRollback()

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodDelete, "/medications/"+medicationID+"/reminders", nil, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteMedicationRemindersHandler_HardDeletes(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	medicationID := "33333333-3333-3333-3333-333333333337"
	row := func() *sqlmock.Rows {
		return addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2024-01-01"), nil, testNextPlanID)
	}
	mock.ExpectQuery(medicationSelectRe).WillReturnRows(row())
	expectPetBelongsToUser(mock, true)
	mock.ExpectBegin()
	mock.ExpectQuery(medicationSelectRe).WillReturnRows(row())
	mock.ExpectQuery(`SELECT id FROM reminder WHERE plan_id = ANY`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(`DELETE FROM file WHERE owner_type = \$1 AND owner_id = ANY\(\$2\) RETURNING object_key`).
		WillReturnRows(sqlmock.NewRows([]string{"object_key"}))
	mock.ExpectExec(`DELETE FROM reminder_plan WHERE id = ANY`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodDelete, "/medications/"+medicationID+"/reminders", nil, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// PATCH без regenerate_reminders: поля лекарства обновляются, набор
// напоминаний (настройки и напоминания) не пересоздаётся.
func TestUpdateMedicationHandler_NoRegenerate_KeepsReminders(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	medicationID := "33333333-3333-3333-3333-333333333340"
	row := func() *sqlmock.Rows {
		return addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2024-01-01"), nil, testNextPlanID)
	}
	mock.ExpectQuery(medicationSelectRe).WillReturnRows(row())
	expectPetBelongsToUser(mock, true)
	mock.ExpectBegin()
	mock.ExpectQuery(medicationSelectRe).WillReturnRows(row())
	mock.ExpectExec(`UPDATE medication SET`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`FROM reminder_plan p`).
		WillReturnRows(sqlmock.NewRows(reminderPlanSelectColumns).AddRow(reminderPlanRow(medicationID)...))
	mock.ExpectExec(`UPDATE reminder_plan SET`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	w := httptest.NewRecorder()
	newTimes := []models.MedicationTimeSlot{{Time: "09:00"}}
	name := "Amoxicillin 2"
	r := petRequest(t, http.MethodPatch, "/medications/"+medicationID+"?tz=UTC", models.UpdateMedicationRequest{
		Name:  &name,
		Times: models.OptionalField[[]models.MedicationTimeSlot]{Set: true, Value: &newTimes},
	}, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// Колонки и значения запроса настроек напоминания (reminderPlanColumns +
// имя питомца + название источника).
var reminderPlanSelectColumns = []string{
	"id", "pet_id", "source", "source_id", "type", "value", "notes", "frequency_type", "weekdays", "interval_days",
	"times", "start_date", "end_date", "tz", "created_at", "pet_name", "source_title",
}

func reminderPlanRow(medicationID string) []driver.Value {
	return []driver.Value{
		testNextPlanID, testPetID, "medication", medicationID, "medication", []byte(`{"name":"Amoxicillin"}`), "1 tablet", "daily", nil, nil,
		"{08:00}", timeParse("2024-01-01"), nil, "UTC", time.Now(), "Rex", "Amoxicillin",
	}
}

// regenerate_reminders=true: будущие незавершённые напоминания набора
// удаляются, создаются новые по новому расписанию, расписание настроек
// обновляется.
func TestUpdateMedicationHandler_RegenerateReminders(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	medicationID := "33333333-3333-3333-3333-333333333341"
	row := func() *sqlmock.Rows {
		return addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2098-01-01"), timeParse("2098-01-02"), testNextPlanID)
	}
	mock.ExpectQuery(medicationSelectRe).WillReturnRows(row())
	expectPetBelongsToUser(mock, true)
	mock.ExpectBegin()
	mock.ExpectQuery(medicationSelectRe).WillReturnRows(row())
	mock.ExpectExec(`UPDATE medication SET`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`FROM reminder_plan p`).
		WillReturnRows(sqlmock.NewRows(reminderPlanSelectColumns).AddRow(reminderPlanRow(medicationID)...))
	mock.ExpectQuery(`SELECT remind_at FROM reminder WHERE plan_id = \$1 AND closed_at IS NOT NULL`).
		WillReturnRows(sqlmock.NewRows([]string{"remind_at"}))
	mock.ExpectQuery(`DELETE FROM reminder WHERE plan_id = \$1 AND closed_at IS NULL AND remind_at > \$2 RETURNING id`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectExec(`INSERT INTO reminder \(id, plan_id, remind_at, notes\)`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), mustRFC3339("2098-01-01T09:00:00Z"), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO reminder \(id, plan_id, remind_at, notes\)`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), mustRFC3339("2098-01-02T09:00:00Z"), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE reminder_plan SET`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	w := httptest.NewRecorder()
	regenerate := true
	newTimes := []models.MedicationTimeSlot{{Time: "09:00"}}
	r := petRequest(t, http.MethodPatch, "/medications/"+medicationID+"?tz=UTC", models.UpdateMedicationRequest{
		Times:               models.OptionalField[[]models.MedicationTimeSlot]{Set: true, Value: &newTimes},
		RegenerateReminders: &regenerate,
	}, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// Переход в as_needed при наличии набора напоминаний без
// regenerate_reminders=true — 400.
func TestUpdateMedicationHandler_AsNeededWithoutRegenerate_BadRequest(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	medicationID := "33333333-3333-3333-3333-333333333342"
	mock.ExpectQuery(medicationSelectRe).
		WillReturnRows(addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2024-01-01"), nil, testNextPlanID))
	expectPetBelongsToUser(mock, true)

	w := httptest.NewRecorder()
	asNeeded := models.MedicationFrequencyAsNeeded
	r := petRequest(t, http.MethodPatch, "/medications/"+medicationID+"?tz=UTC", models.UpdateMedicationRequest{
		FrequencyType: &asNeeded,
		Weekdays:      models.OptionalField[[]int]{Set: true, Value: nil},
		IntervalDays:  models.OptionalField[int]{Set: true, Value: nil},
		Times:         models.OptionalField[[]models.MedicationTimeSlot]{Set: true, Value: nil},
		StartDate:     models.OptionalField[string]{Set: true, Value: nil},
	}, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateMedicationHandler_MissingTimeZone(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	medicationID := "33333333-3333-3333-3333-333333333342"
	mock.ExpectQuery(medicationSelectRe).
		WillReturnRows(addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2024-01-01"), nil, nil))
	expectPetBelongsToUser(mock, true)

	w := httptest.NewRecorder()
	note := "x"
	r := petRequest(t, http.MethodPatch, "/medications/"+medicationID, models.UpdateMedicationRequest{Note: &note}, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Idempotency-Key создания сущностей ветпаспорта
// ---------------------------------------------------------------------------

const testIdempotencyKey = "6f1c2b9e-6c57-4c3b-9a55-0d6f6f1f2a10"

type vetPassportCreateCase struct {
	name  string
	table string
	path  string
	body  any
	// transactional — создание выполняется в транзакции (прививка и
	// лекарство: вместе с ними создаются связанные записи).
	transactional bool
	handler       func(w http.ResponseWriter, r *http.Request, petID uuid.UUID)
}

func vetPassportCreateCases() []vetPassportCreateCase {
	return []vetPassportCreateCase{
		{"vaccination", "vaccination", "vaccinations?tz=UTC", models.CreateVaccinationRequest{Name: "Rabies", AdministeredDate: "2024-01-01"}, true, CreateVaccinationHandler},
		{"disease", "disease", "diseases", models.CreateDiseaseRequest{Name: "Otitis", DiagnosedDate: "2024-01-01", Status: "active"}, false, CreateDiseaseHandler},
		{"vet visit", "vet_visit", "vet-visits", models.CreateVetVisitRequest{VisitDate: "2024-01-01", Reason: "Checkup"}, false, CreateVetVisitHandler},
		{"allergy", "allergy", "allergies", models.CreateAllergyRequest{Allergen: "Chicken", Severity: "mild"}, false, CreateAllergyHandler},
		{"medication", "medication", "medications?tz=UTC", models.CreateMedicationRequest{Name: "Drug", Dosage: "1 tab", FrequencyType: "as_needed"}, true, CreateMedicationHandler},
	}
}

func idempotentCreateRequest(t *testing.T, c vetPassportCreateCase) *http.Request {
	t.Helper()
	r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/"+c.path, c.body, true)
	r.Header.Set("Idempotency-Key", testIdempotencyKey)
	return r
}

func assertCreatedID(t *testing.T, w *httptest.ResponseRecorder, wantID string) {
	t.Helper()
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var resp models.IDResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, wantID, resp.ID)
}

// Первый запрос с ключом: записи ещё нет — вставка сохраняет ключ в строке.
func TestVetPassportCreate_IdempotencyKey_FirstRequestStoresKey(t *testing.T) {
	for _, c := range vetPassportCreateCases() {
		t.Run(c.name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			expectPetOwnedForCreate(mock)
			mock.ExpectQuery(`SELECT id FROM `+c.table+` WHERE pet_id = \$1 AND idempotency_key = \$2`).
				WithArgs(uuid.MustParse(testPetID), testIdempotencyKey).
				WillReturnError(sql.ErrNoRows)
			if c.transactional {
				mock.ExpectBegin()
			}
			mock.ExpectQuery(`INSERT INTO ` + c.table + ` \(.*idempotency_key\)`).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("44444444-4444-4444-4444-444444444444"))
			if c.transactional {
				mock.ExpectCommit()
			}

			w := httptest.NewRecorder()
			c.handler(w, idempotentCreateRequest(t, c), uuid.MustParse(testPetID))

			require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			var resp models.IDResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, "44444444-4444-4444-4444-444444444444", resp.ID)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// Повтор с тем же ключом (обрыв соединения после успешного создания):
// возвращается id ранее созданной записи, новая строка НЕ вставляется.
func TestVetPassportCreate_IdempotencyKey_ReplayReturnsExisting(t *testing.T) {
	for _, c := range vetPassportCreateCases() {
		t.Run(c.name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			expectPetOwnedForCreate(mock)
			mock.ExpectQuery(`SELECT id FROM `+c.table+` WHERE pet_id = \$1 AND idempotency_key = \$2`).
				WithArgs(uuid.MustParse(testPetID), testIdempotencyKey).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("77777777-7777-7777-7777-777777777777"))

			w := httptest.NewRecorder()
			c.handler(w, idempotentCreateRequest(t, c), uuid.MustParse(testPetID))

			assertCreatedID(t, w, "77777777-7777-7777-7777-777777777777")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// Гонка параллельных запросов с одним ключом: вставка падает на уникальном
// индексе — это не 500, а откат транзакции (проигравший не оставляет после
// себя ни фактов, ни напоминаний), повторный поиск и ответ ранее созданной
// записью.
func TestVetPassportCreate_IdempotencyKey_RaceReturnsExisting(t *testing.T) {
	for _, c := range vetPassportCreateCases() {
		t.Run(c.name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			expectPetOwnedForCreate(mock)
			lookup := `SELECT id FROM ` + c.table + ` WHERE pet_id = \$1 AND idempotency_key = \$2`
			mock.ExpectQuery(lookup).WillReturnError(sql.ErrNoRows)
			if c.transactional {
				mock.ExpectBegin()
			}
			mock.ExpectQuery(`INSERT INTO ` + c.table).WillReturnError(&pq.Error{Code: "23505"})
			if c.transactional {
				mock.ExpectRollback()
			}
			mock.ExpectQuery(lookup).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("77777777-7777-7777-7777-777777777777"))

			w := httptest.NewRecorder()
			c.handler(w, idempotentCreateRequest(t, c), uuid.MustParse(testPetID))

			assertCreatedID(t, w, "77777777-7777-7777-7777-777777777777")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestVetPassportCreate_IdempotencyKey_Invalid(t *testing.T) {
	for _, c := range vetPassportCreateCases() {
		t.Run(c.name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)

			w := httptest.NewRecorder()
			r := idempotentCreateRequest(t, c)
			r.Header.Set("Idempotency-Key", "not-a-uuid")
			c.handler(w, r, uuid.MustParse(testPetID))

			assert.Equal(t, http.StatusBadRequest, w.Code)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// Время приёма и даты расписания трактуются в поясе клиента: 08:00 по
// Москве — 05:00Z, по Боготе — 13:00Z.
func TestCreateMedicationHandler_ScheduleTimesInClientTimeZone(t *testing.T) {
	cases := []struct {
		name     string
		tz       string
		expected string
	}{
		{"UTC+3", "Europe/Moscow", "2098-01-01T05:00:00Z"},
		{"UTC-5", "America/Bogota", "2098-01-01T13:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			expectPetOwnedForCreate(mock)
			mock.ExpectBegin()
			mock.ExpectQuery(`INSERT INTO medication`).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("33333333-3333-3333-3333-333333333339"))
			mock.ExpectExec(`INSERT INTO reminder_plan`).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec(`INSERT INTO reminder \(id, plan_id, remind_at, notes\)`).
				WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), mustRFC3339(tc.expected), sqlmock.AnyArg()).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec(`UPDATE medication SET reminder_plan_id`).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()

			addReminders := true
			w := httptest.NewRecorder()
			r := petRequest(t, http.MethodPost, medicationsPath+"?tz="+tc.tz, models.CreateMedicationRequest{
				Name: "Amoxicillin", Dosage: "1 tablet", FrequencyType: models.MedicationFrequencyDaily,
				Times:     []models.MedicationTimeSlot{{Time: "08:00"}},
				StartDate: strPtr("2098-01-01"), EndDate: strPtr("2098-01-01"), AddReminders: &addReminders,
			}, true)
			CreateMedicationHandler(w, r, uuid.MustParse(testPetID))

			assert.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
