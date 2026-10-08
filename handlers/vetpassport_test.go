package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
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

func TestCreateVaccinationHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnedForCreate(mock)
	mock.ExpectQuery(`INSERT INTO vaccination`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("44444444-4444-4444-4444-444444444444"))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/vaccinations", models.CreateVaccinationRequest{
		Name:             "Rabies",
		AdministeredDate: "2024-01-01",
	}, true)
	CreateVaccinationHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusCreated, w.Code)
	var resp models.IDResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "44444444-4444-4444-4444-444444444444", resp.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateVaccinationHandler_ValidationError(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/vaccinations", models.CreateVaccinationRequest{
		Name:             "",
		AdministeredDate: "2024-01-01",
	}, true)
	CreateVaccinationHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateVaccinationHandler_PetNotOwned(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnError(sql.ErrNoRows)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/vaccinations", models.CreateVaccinationRequest{
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
	mock.ExpectQuery(`SELECT id, pet_id, name, administered_date, next_date, administered_event_id, next_event_id, deleted_at\s+FROM vaccination`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "name", "administered_date", "next_date", "administered_event_id", "next_event_id", "deleted_at"}))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodGet, "/pet/"+testPetID+"/vaccinations", nil, true)
	GetPetVaccinationsHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.VaccinationListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.Items)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateVaccinationHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	vaccinationID := "55555555-5555-5555-5555-555555555555"
	mock.ExpectQuery(`SELECT id, pet_id, name, administered_date, next_date, administered_event_id, next_event_id, deleted_at\s+FROM vaccination\s+WHERE id = \$1 AND deleted_at IS NULL`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "name", "administered_date", "next_date", "administered_event_id", "next_event_id", "deleted_at"}).
			AddRow(vaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), nil, nil, nil, nil))
	expectPetBelongsToUser(mock, true)
	mock.ExpectExec(`UPDATE vaccination SET`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	newName := "Rabies v2"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPatch, "/vaccinations/"+vaccinationID, models.UpdateVaccinationRequest{Name: &newName}, true)
	VaccinationByIDHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateVaccinationHandler_NotOwned(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	vaccinationID := "55555555-5555-5555-5555-555555555555"
	mock.ExpectQuery(`SELECT id, pet_id, name, administered_date, next_date, administered_event_id, next_event_id, deleted_at\s+FROM vaccination\s+WHERE id = \$1 AND deleted_at IS NULL`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "name", "administered_date", "next_date", "administered_event_id", "next_event_id", "deleted_at"}).
			AddRow(vaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), nil, nil, nil, nil))
	expectPetBelongsToUser(mock, false)

	newName := "Rabies v2"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPatch, "/vaccinations/"+vaccinationID, models.UpdateVaccinationRequest{Name: &newName}, true)
	VaccinationByIDHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteVaccinationHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	vaccinationID := "55555555-5555-5555-5555-555555555555"
	mock.ExpectQuery(`SELECT id, pet_id, name, administered_date, next_date, administered_event_id, next_event_id, deleted_at\s+FROM vaccination\s+WHERE id = \$1 AND deleted_at IS NULL`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "pet_id", "name", "administered_date", "next_date", "administered_event_id", "next_event_id", "deleted_at"}).
			AddRow(vaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), nil, nil, nil, nil))
	expectPetBelongsToUser(mock, true)
	mock.ExpectExec(`UPDATE vaccination SET deleted_at`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodDelete, "/vaccinations/"+vaccinationID, nil, true)
	VaccinationByIDHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
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

var medicationColumns = []string{"id", "pet_id", "name", "dosage", "frequency_type", "weekdays", "interval_days", "times", "start_date", "end_date", "event_ids", "note", "deleted_at", "created_at"}

// medicationSelectRe — regex-фрагмент общего SELECT для medication (см.
// database.medicationSelectColumns), общий для GetMedicationByIDForUpdate.
const medicationSelectRe = `SELECT id, pet_id, name, dosage, frequency_type, weekdays, interval_days, times, start_date, end_date, event_ids, note, deleted_at, created_at\s+FROM medication\s+WHERE id = \$1 AND deleted_at IS NULL`

func addMedicationRow(rows *sqlmock.Rows, id string, weekdays, times any, intervalDays any, startDate, endDate any, eventIDs string) *sqlmock.Rows {
	return rows.AddRow(id, testPetID, "Amoxicillin", "1 tablet", "daily", weekdays, intervalDays, times, startDate, endDate, eventIDs, nil, nil, timeParse("2024-01-01"))
}

func TestCreateMedicationHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnedForCreate(mock)
	mock.ExpectQuery(`INSERT INTO medication`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("33333333-3333-3333-3333-333333333334"))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/medications", models.CreateMedicationRequest{
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

func TestCreateMedicationHandler_AsNeeded_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnedForCreate(mock)
	mock.ExpectQuery(`INSERT INTO medication`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("33333333-3333-3333-3333-333333333338"))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/medications", models.CreateMedicationRequest{
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
	r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/medications", models.CreateMedicationRequest{
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
	r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/medications", models.CreateMedicationRequest{
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
	r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/medications", models.CreateMedicationRequest{
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

func TestCreateMedicationHandler_AsNeededWithAddEvent(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	addEvent := true
	r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/medications", models.CreateMedicationRequest{
		Name:          "Painkiller",
		Dosage:        "1 tablet",
		FrequencyType: models.MedicationFrequencyAsNeeded,
		AddEvent:      &addEvent,
	}, true)
	CreateMedicationHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateMedicationHandler_AddEventCreatesSchedule(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnedForCreate(mock)
	newID := "33333333-3333-3333-3333-333333333339"
	mock.ExpectQuery(`INSERT INTO medication`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(newID))
	mock.ExpectQuery(`INSERT INTO event`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"))
	mock.ExpectQuery(`INSERT INTO event`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"))
	mock.ExpectExec(`UPDATE medication SET event_ids`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	addEvent := true
	r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/medications", models.CreateMedicationRequest{
		Name:          "Amoxicillin",
		Dosage:        "1 tablet",
		FrequencyType: models.MedicationFrequencyDaily,
		Times:         []models.MedicationTimeSlot{{Time: "08:00"}},
		StartDate:     strPtr("2024-01-01"),
		EndDate:       strPtr("2024-01-02"),
		AddEvent:      &addEvent,
	}, true)
	CreateMedicationHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusCreated, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteMedicationHandler_NotOwned(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	medicationID := "33333333-3333-3333-3333-333333333335"
	mock.ExpectQuery(medicationSelectRe).
		WillReturnRows(addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2024-01-01"), nil, "{}"))
	expectPetBelongsToUser(mock, false)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodDelete, "/medications/"+medicationID, nil, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteMedicationHandler_HardDeletesExistingEvents(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	medicationID := "33333333-3333-3333-3333-33333333333a"
	mock.ExpectQuery(medicationSelectRe).
		WillReturnRows(addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2024-01-01"), nil, `{aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa}`))
	expectPetBelongsToUser(mock, true)
	mock.ExpectExec(`UPDATE medication SET deleted_at`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM event WHERE id = ANY`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodDelete, "/medications/"+medicationID, nil, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateMedicationEventsHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	medicationID := "33333333-3333-3333-3333-333333333336"
	mock.ExpectQuery(medicationSelectRe).
		WillReturnRows(addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2024-01-01"), timeParse("2024-01-02"), "{}"))
	expectPetBelongsToUser(mock, true)
	mock.ExpectQuery(`INSERT INTO event`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"))
	mock.ExpectQuery(`INSERT INTO event`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"))
	mock.ExpectExec(`UPDATE medication SET event_ids`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT owner_id, COUNT\(\*\) FROM file`).
		WillReturnRows(sqlmock.NewRows([]string{"owner_id", "count"}))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/medications/"+medicationID+"/events", nil, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp models.MedicationResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.EventIDs, 2)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateMedicationEventsHandler_ConflictWhenEventsExist(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	medicationID := "33333333-3333-3333-3333-33333333333b"
	mock.ExpectQuery(medicationSelectRe).
		WillReturnRows(addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2024-01-01"), nil, `{aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa}`))
	expectPetBelongsToUser(mock, true)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/medications/"+medicationID+"/events", nil, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusConflict, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateMedicationEventsHandler_AsNeededBadRequest(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	medicationID := "33333333-3333-3333-3333-33333333333c"
	rows := sqlmock.NewRows(medicationColumns).AddRow(
		medicationID, testPetID, "Painkiller", "1 tablet", "as_needed", nil, nil, nil, nil, nil, "{}", nil, nil, timeParse("2024-01-01"))
	mock.ExpectQuery(medicationSelectRe).WillReturnRows(rows)
	expectPetBelongsToUser(mock, true)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/medications/"+medicationID+"/events", nil, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteMedicationEventsHandler_HardDeletes(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	medicationID := "33333333-3333-3333-3333-333333333337"
	mock.ExpectQuery(medicationSelectRe).
		WillReturnRows(addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2024-01-01"), nil, `{aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa}`))
	expectPetBelongsToUser(mock, true)
	mock.ExpectExec(`DELETE FROM event WHERE id = ANY`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE medication SET event_ids`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodDelete, "/medications/"+medicationID+"/events", nil, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteMedicationEventsHandler_NotFoundWhenEmpty(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	medicationID := "33333333-3333-3333-3333-33333333333d"
	mock.ExpectQuery(medicationSelectRe).
		WillReturnRows(addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2024-01-01"), nil, "{}"))
	expectPetBelongsToUser(mock, true)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodDelete, "/medications/"+medicationID+"/events", nil, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateMedicationHandler_NoRegenerate_KeepsEvents(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	medicationID := "33333333-3333-3333-3333-33333333333e"
	mock.ExpectQuery(medicationSelectRe).
		WillReturnRows(addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2024-01-01"), nil, `{aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa}`))
	expectPetBelongsToUser(mock, true)
	mock.ExpectExec(`UPDATE medication SET`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	newStartDate := "2024-02-01"
	r := petRequest(t, http.MethodPatch, "/medications/"+medicationID, models.UpdateMedicationRequest{
		StartDate: models.OptionalField[string]{Set: true, Value: &newStartDate},
	}, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateMedicationHandler_RegenerateEvents(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	medicationID := "33333333-3333-3333-3333-33333333333f"
	mock.ExpectQuery(medicationSelectRe).
		WillReturnRows(addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2024-01-01"), timeParse("2024-01-02"), `{aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa}`))
	expectPetBelongsToUser(mock, true)
	mock.ExpectExec(`UPDATE medication SET`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`DELETE FROM event WHERE id = ANY`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`INSERT INTO event`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("cccccccc-cccc-cccc-cccc-cccccccccccc"))
	mock.ExpectQuery(`INSERT INTO event`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("dddddddd-dddd-dddd-dddd-dddddddddddd"))
	mock.ExpectExec(`UPDATE medication SET event_ids`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	newStartDate := "2024-03-01"
	newEndDate := "2024-03-02"
	regenerate := true
	r := petRequest(t, http.MethodPatch, "/medications/"+medicationID, models.UpdateMedicationRequest{
		StartDate:        models.OptionalField[string]{Set: true, Value: &newStartDate},
		EndDate:          models.OptionalField[string]{Set: true, Value: &newEndDate},
		RegenerateEvents: &regenerate,
	}, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateMedicationHandler_AsNeededWithoutRegenerate_BadRequest(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	medicationID := "33333333-3333-3333-3333-333333333340"
	mock.ExpectQuery(medicationSelectRe).
		WillReturnRows(addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2024-01-01"), nil, `{aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa}`))
	expectPetBelongsToUser(mock, true)

	w := httptest.NewRecorder()
	asNeeded := models.MedicationFrequencyAsNeeded
	r := petRequest(t, http.MethodPatch, "/medications/"+medicationID, models.UpdateMedicationRequest{
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

// ---------------------------------------------------------------------------
// Idempotency-Key создания сущностей ветпаспорта
// ---------------------------------------------------------------------------

const testIdempotencyKey = "6f1c2b9e-6c57-4c3b-9a55-0d6f6f1f2a10"

type vetPassportCreateCase struct {
	name    string
	table   string
	path    string
	body    any
	handler func(w http.ResponseWriter, r *http.Request, petID uuid.UUID)
}

func vetPassportCreateCases() []vetPassportCreateCase {
	return []vetPassportCreateCase{
		{"vaccination", "vaccination", "vaccinations", models.CreateVaccinationRequest{Name: "Rabies", AdministeredDate: "2024-01-01"}, CreateVaccinationHandler},
		{"disease", "disease", "diseases", models.CreateDiseaseRequest{Name: "Otitis", DiagnosedDate: "2024-01-01", Status: "active"}, CreateDiseaseHandler},
		{"vet visit", "vet_visit", "vet-visits", models.CreateVetVisitRequest{VisitDate: "2024-01-01", Reason: "Checkup"}, CreateVetVisitHandler},
		{"allergy", "allergy", "allergies", models.CreateAllergyRequest{Allergen: "Chicken", Severity: "mild"}, CreateAllergyHandler},
		{"medication", "medication", "medications", models.CreateMedicationRequest{Name: "Drug", Dosage: "1 tab", FrequencyType: "as_needed"}, CreateMedicationHandler},
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
			mock.ExpectQuery(`INSERT INTO ` + c.table + ` \(.*idempotency_key\)`).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("44444444-4444-4444-4444-444444444444"))

			w := httptest.NewRecorder()
			c.handler(w, idempotentCreateRequest(t, c), uuid.MustParse(testPetID))

			assertCreatedID(t, w, "44444444-4444-4444-4444-444444444444")
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
// индексе — это не 500, а повторный поиск и ответ ранее созданной записью.
func TestVetPassportCreate_IdempotencyKey_RaceReturnsExisting(t *testing.T) {
	for _, c := range vetPassportCreateCases() {
		t.Run(c.name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			expectPetOwnedForCreate(mock)
			lookup := `SELECT id FROM ` + c.table + ` WHERE pet_id = \$1 AND idempotency_key = \$2`
			mock.ExpectQuery(lookup).WillReturnError(sql.ErrNoRows)
			mock.ExpectQuery(`INSERT INTO ` + c.table).WillReturnError(&pq.Error{Code: "23505"})
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

// Проигравший гонку запрос прививки успел создать события — они удаляются,
// чтобы не остаться без связи с прививкой.
func TestCreateVaccinationHandler_IdempotencyRace_DeletesOrphanEvents(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnedForCreate(mock)
	lookup := `SELECT id FROM vaccination WHERE pet_id = \$1 AND idempotency_key = \$2`
	orphanEventID := "88888888-8888-8888-8888-888888888888"
	mock.ExpectQuery(lookup).WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`INSERT INTO event`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(orphanEventID))
	mock.ExpectQuery(`INSERT INTO vaccination`).WillReturnError(&pq.Error{Code: "23505"})
	mock.ExpectExec(`DELETE FROM event WHERE id = ANY`).
		WithArgs(pq.Array([]string{orphanEventID})).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(lookup).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("77777777-7777-7777-7777-777777777777"))

	addEvent := true
	eventTime := "09:00"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/vaccinations", models.CreateVaccinationRequest{
		Name: "Rabies", AdministeredDate: "2024-01-01", AddEventOnAdministered: &addEvent, EventTime: &eventTime,
	}, true)
	r.Header.Set("Idempotency-Key", testIdempotencyKey)
	CreateVaccinationHandler(w, r, uuid.MustParse(testPetID))

	assertCreatedID(t, w, "77777777-7777-7777-7777-777777777777")
	require.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Vaccination: связанные события (подпись, обновление на месте)
// ---------------------------------------------------------------------------

var vaccinationSelect = `SELECT id, pet_id, name, administered_date, next_date, administered_event_id, next_event_id, deleted_at\s+FROM vaccination\s+WHERE id = \$1 AND deleted_at IS NULL`

var vaccinationColumns = []string{"id", "pet_id", "name", "administered_date", "next_date", "administered_event_id", "next_event_id", "deleted_at"}

var eventForUpdateSelect = `SELECT id, pet_id, date_time, type, notes, value, notifications_enabled\s+FROM event\s+WHERE id = \$1 AND deleted_at IS NULL`

var eventForUpdateColumns = []string{"id", "pet_id", "date_time", "type", "notes", "value", "notifications_enabled"}

const (
	testVaccinationID       = "55555555-5555-5555-5555-555555555555"
	testAdministeredEventID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	testNextEventID         = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

func mustRFC3339(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestCreateVaccinationHandler_EventLabelDefaultPrefix(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnedForCreate(mock)
	mock.ExpectQuery(`INSERT INTO event`).
		WithArgs(uuid.MustParse(testPetID), mustRFC3339("2024-01-01T09:30:00Z"), "other", sqlmock.AnyArg(), `{"label":"Вакцинация: Rabies"}`, sqlmock.AnyArg(), false).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testAdministeredEventID))
	mock.ExpectQuery(`INSERT INTO vaccination`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testVaccinationID))

	addEvent := true
	eventTime := "09:30"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/vaccinations", models.CreateVaccinationRequest{
		Name: "Rabies", AdministeredDate: "2024-01-01", AddEventOnAdministered: &addEvent, EventTime: &eventTime,
	}, true)
	CreateVaccinationHandler(w, r, uuid.MustParse(testPetID))

	assertCreatedID(t, w, testVaccinationID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateVaccinationHandler_EventLabelFromClient(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnedForCreate(mock)
	mock.ExpectQuery(`INSERT INTO event`).
		WithArgs(uuid.MustParse(testPetID), mustRFC3339("2024-06-01T09:30:00Z"), "other", sqlmock.AnyArg(), `{"label":"Vaccination: Rabies"}`, sqlmock.AnyArg(), false).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testNextEventID))
	mock.ExpectQuery(`INSERT INTO vaccination`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testVaccinationID))

	addEvent := true
	eventTime := "09:30"
	nextDate := "2024-06-01"
	label := "Vaccination: Rabies"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/vaccinations", models.CreateVaccinationRequest{
		Name: "Rabies", AdministeredDate: "2024-01-01", NextDate: &nextDate, AddEventOnNext: &addEvent, EventTime: &eventTime, EventLabel: &label,
	}, true)
	CreateVaccinationHandler(w, r, uuid.MustParse(testPetID))

	assertCreatedID(t, w, testVaccinationID)
	require.NoError(t, mock.ExpectationsWereMet())
}

// PATCH с add_event_on_administered=true при уже существующем связанном
// событии обновляет его на месте (дата + прежнее время суток + подпись) и не
// создаёт новое.
func TestUpdateVaccinationHandler_UpdatesExistingLinkedEvent(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(vaccinationSelect).
		WillReturnRows(sqlmock.NewRows(vaccinationColumns).
			AddRow(testVaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), nil, testAdministeredEventID, nil, nil))
	expectPetBelongsToUser(mock, true)
	mock.ExpectQuery(eventForUpdateSelect).
		WithArgs(uuid.MustParse(testAdministeredEventID)).
		WillReturnRows(sqlmock.NewRows(eventForUpdateColumns).
			AddRow(testAdministeredEventID, testPetID, mustRFC3339("2024-01-01T14:45:00Z"), "other", nil, []byte(`{"label":"Вакцинация: Rabies"}`), false))
	mock.ExpectExec(`UPDATE event\s+SET date_time = \$1, value = \$2\s+WHERE id = \$3`).
		WithArgs(mustRFC3339("2024-02-10T14:45:00Z"), `{"label":"Вакцинация: Rabies v2"}`, uuid.MustParse(testAdministeredEventID)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE vaccination SET name = \$1, administered_date = \$2 WHERE id = \$3`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	name := "Rabies v2"
	date := "2024-02-10"
	addEvent := true
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPatch, "/vaccinations/"+testVaccinationID, models.UpdateVaccinationRequest{
		Name: &name, AdministeredDate: &date, AddEventOnAdministered: &addEvent,
	}, true)
	VaccinationByIDHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// Связанное событие уже удалено (например, из календаря) — создаётся новое,
// ссылка перезаписывается.
func TestUpdateVaccinationHandler_CreatesEventWhenLinkedOneDeleted(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(vaccinationSelect).
		WillReturnRows(sqlmock.NewRows(vaccinationColumns).
			AddRow(testVaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), nil, testAdministeredEventID, nil, nil))
	expectPetBelongsToUser(mock, true)
	mock.ExpectQuery(eventForUpdateSelect).WillReturnError(sql.ErrNoRows)
	newEventID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	mock.ExpectQuery(`INSERT INTO event`).
		WithArgs(uuid.MustParse(testPetID), mustRFC3339("2024-01-01T08:00:00Z"), "other", sqlmock.AnyArg(), `{"label":"Вакцинация: Rabies"}`, sqlmock.AnyArg(), false).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(newEventID))
	mock.ExpectExec(`UPDATE vaccination SET administered_event_id = \$1 WHERE id = \$2`).
		WithArgs(uuid.NullUUID{UUID: uuid.MustParse(newEventID), Valid: true}, uuid.MustParse(testVaccinationID)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	addEvent := true
	eventTime := "08:00"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPatch, "/vaccinations/"+testVaccinationID, models.UpdateVaccinationRequest{
		AddEventOnAdministered: &addEvent, EventTime: &eventTime,
	}, true)
	VaccinationByIDHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// Снятие флага мягко удаляет связанное событие и очищает ссылку.
func TestUpdateVaccinationHandler_FlagFalseDeletesLinkedEvent(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(vaccinationSelect).
		WillReturnRows(sqlmock.NewRows(vaccinationColumns).
			AddRow(testVaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), timeParse("2025-01-01"), nil, testNextEventID, nil))
	expectPetBelongsToUser(mock, true)
	mock.ExpectQuery(eventForUpdateSelect).
		WithArgs(uuid.MustParse(testNextEventID)).
		WillReturnRows(sqlmock.NewRows(eventForUpdateColumns).
			AddRow(testNextEventID, testPetID, mustRFC3339("2025-01-01T09:00:00Z"), "other", nil, []byte(`{"label":"x"}`), false))
	mock.ExpectExec(`UPDATE event SET deleted_at = \$1 WHERE id = \$2 AND deleted_at IS NULL`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE vaccination SET next_event_id = \$1 WHERE id = \$2`).
		WithArgs(uuid.NullUUID{}, uuid.MustParse(testVaccinationID)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	addEvent := false
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPatch, "/vaccinations/"+testVaccinationID, models.UpdateVaccinationRequest{AddEventOnNext: &addEvent}, true)
	VaccinationByIDHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// Флаг не передан, но дата изменилась — связанное событие переносится на
// новую дату с прежним временем суток, подпись не трогается.
func TestUpdateVaccinationHandler_DateChangeMovesLinkedEvent(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(vaccinationSelect).
		WillReturnRows(sqlmock.NewRows(vaccinationColumns).
			AddRow(testVaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), timeParse("2025-01-01"), nil, testNextEventID, nil))
	expectPetBelongsToUser(mock, true)
	mock.ExpectQuery(eventForUpdateSelect).
		WillReturnRows(sqlmock.NewRows(eventForUpdateColumns).
			AddRow(testNextEventID, testPetID, mustRFC3339("2025-01-01T09:00:00Z"), "other", nil, []byte(`{"label":"x"}`), false))
	mock.ExpectExec(`UPDATE event\s+SET date_time = \$1\s+WHERE id = \$2`).
		WithArgs(mustRFC3339("2025-03-15T09:00:00Z"), uuid.MustParse(testNextEventID)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE vaccination SET next_date = \$1 WHERE id = \$2`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	nextDate := "2025-03-15"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPatch, "/vaccinations/"+testVaccinationID, models.UpdateVaccinationRequest{NextDate: &nextDate}, true)
	VaccinationByIDHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// next_date: null (явный JSON null) очищает дату и удаляет событие-напоминание.
func TestUpdateVaccinationHandler_NullNextDateClearsDateAndEvent(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(vaccinationSelect).
		WillReturnRows(sqlmock.NewRows(vaccinationColumns).
			AddRow(testVaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), timeParse("2025-01-01"), nil, testNextEventID, nil))
	expectPetBelongsToUser(mock, true)
	mock.ExpectQuery(eventForUpdateSelect).
		WillReturnRows(sqlmock.NewRows(eventForUpdateColumns).
			AddRow(testNextEventID, testPetID, mustRFC3339("2025-01-01T09:00:00Z"), "other", nil, []byte(`{"label":"x"}`), false))
	mock.ExpectExec(`UPDATE event SET deleted_at = \$1 WHERE id = \$2 AND deleted_at IS NULL`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE vaccination SET next_date = \$1, next_event_id = \$2 WHERE id = \$3`).
		WithArgs(sql.NullTime{}, uuid.NullUUID{}, uuid.MustParse(testVaccinationID)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPatch, "/vaccinations/"+testVaccinationID, map[string]any{"next_date": nil}, true)
	VaccinationByIDHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteVaccinationHandler_SoftDeletesLinkedEvents(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(vaccinationSelect).
		WillReturnRows(sqlmock.NewRows(vaccinationColumns).
			AddRow(testVaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), timeParse("2025-01-01"), testAdministeredEventID, testNextEventID, nil))
	expectPetBelongsToUser(mock, true)
	mock.ExpectExec(`UPDATE vaccination SET deleted_at`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE event SET deleted_at = now\(\) WHERE id = ANY`).
		WithArgs(pq.Array([]string{testAdministeredEventID, testNextEventID})).
		WillReturnResult(sqlmock.NewResult(0, 2))

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodDelete, "/vaccinations/"+testVaccinationID, nil, true)
	VaccinationByIDHandler(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// event_time — местное время пояса tz, а не UTC: "09:30" в Europe/Moscow
// (UTC+3) — это 06:30Z, в America/Bogota (UTC-5) — 14:30Z. Без tz —
// прежнее поведение (UTC).
func TestCreateVaccinationHandler_EventTimeInClientTimeZone(t *testing.T) {
	cases := []struct {
		name     string
		query    string
		expected string
	}{
		{"UTC+3", "?tz=Europe/Moscow", "2024-01-01T06:30:00Z"},
		{"UTC-5", "?tz=America/Bogota", "2024-01-01T14:30:00Z"},
		{"без tz", "", "2024-01-01T09:30:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			expectPetOwnedForCreate(mock)
			mock.ExpectQuery(`INSERT INTO event`).
				WithArgs(uuid.MustParse(testPetID), mustRFC3339(tc.expected), "other", sqlmock.AnyArg(), `{"label":"Вакцинация: Rabies"}`, sqlmock.AnyArg(), false).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testAdministeredEventID))
			mock.ExpectQuery(`INSERT INTO vaccination`).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testVaccinationID))

			addEvent := true
			eventTime := "09:30"
			w := httptest.NewRecorder()
			r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/vaccinations"+tc.query, models.CreateVaccinationRequest{
				Name: "Rabies", AdministeredDate: "2024-01-01", AddEventOnAdministered: &addEvent, EventTime: &eventTime,
			}, true)
			CreateVaccinationHandler(w, r, uuid.MustParse(testPetID))

			assertCreatedID(t, w, testVaccinationID)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// Местное время после полуночи восточнее UTC попадает на предыдущие сутки
// UTC, а поздний вечер западнее UTC — на следующие: дата события не должна
// «прилипать» к дате UTC.
func TestCreateVaccinationHandler_EventTimeCrossesUTCDayBoundary(t *testing.T) {
	cases := []struct {
		name      string
		tz        string
		eventTime string
		expected  string
	}{
		{"UTC+3, 01:00", "Europe/Moscow", "01:00", "2023-12-31T22:00:00Z"},
		{"UTC-5, 22:00", "America/Bogota", "22:00", "2024-01-02T03:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			expectPetOwnedForCreate(mock)
			mock.ExpectQuery(`INSERT INTO event`).
				WithArgs(uuid.MustParse(testPetID), mustRFC3339(tc.expected), "other", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), false).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testAdministeredEventID))
			mock.ExpectQuery(`INSERT INTO vaccination`).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testVaccinationID))

			addEvent := true
			w := httptest.NewRecorder()
			r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/vaccinations?tz="+tc.tz, models.CreateVaccinationRequest{
				Name: "Rabies", AdministeredDate: "2024-01-01", AddEventOnAdministered: &addEvent, EventTime: &tc.eventTime,
			}, true)
			CreateVaccinationHandler(w, r, uuid.MustParse(testPetID))

			assertCreatedID(t, w, testVaccinationID)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCreateVaccinationHandler_InvalidTimeZone(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/vaccinations?tz=Nowhere/City", models.CreateVaccinationRequest{
		Name: "Rabies", AdministeredDate: "2024-01-01",
	}, true)
	CreateVaccinationHandler(w, r, uuid.MustParse(testPetID))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "VALIDATION_ERROR")
	require.NoError(t, mock.ExpectationsWereMet())
}

// PATCH переносит связанное событие на новую дату, сохраняя его прежнее
// время суток в поясе клиента: событие 09:00 по Москве (06:00Z) остаётся
// 09:00 по Москве; для UTC-5 то же событие 09:00 местного — это 14:00Z.
func TestUpdateVaccinationHandler_DateChangeKeepsLocalTimeOfDay(t *testing.T) {
	cases := []struct {
		name     string
		tz       string
		existing string
		expected string
	}{
		{"UTC+3", "Europe/Moscow", "2025-01-01T06:00:00Z", "2025-03-15T06:00:00Z"},
		{"UTC-5", "America/Bogota", "2025-01-01T14:00:00Z", "2025-03-15T14:00:00Z"},
		// 23:30Z 31 декабря — это 02:30 1 января по Москве: событие переносится
		// на 02:30 местного 15 марта, то есть на 23:30Z 14 марта.
		{"UTC+3, через границу суток", "Europe/Moscow", "2024-12-31T23:30:00Z", "2025-03-14T23:30:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			mock.ExpectQuery(vaccinationSelect).
				WillReturnRows(sqlmock.NewRows(vaccinationColumns).
					AddRow(testVaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), timeParse("2025-01-01"), nil, testNextEventID, nil))
			expectPetBelongsToUser(mock, true)
			mock.ExpectQuery(eventForUpdateSelect).
				WillReturnRows(sqlmock.NewRows(eventForUpdateColumns).
					AddRow(testNextEventID, testPetID, mustRFC3339(tc.existing), "other", nil, []byte(`{"label":"x"}`), false))
			mock.ExpectExec(`UPDATE event\s+SET date_time = \$1\s+WHERE id = \$2`).
				WithArgs(mustRFC3339(tc.expected), uuid.MustParse(testNextEventID)).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec(`UPDATE vaccination SET next_date = \$1 WHERE id = \$2`).
				WillReturnResult(sqlmock.NewResult(0, 1))

			nextDate := "2025-03-15"
			w := httptest.NewRecorder()
			r := petRequest(t, http.MethodPatch, "/vaccinations/"+testVaccinationID+"?tz="+tc.tz, models.UpdateVaccinationRequest{NextDate: &nextDate}, true)
			VaccinationByIDHandler(w, r)

			assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// PATCH с новым event_time трактует его как местное время пояса tz.
func TestUpdateVaccinationHandler_EventTimeInClientTimeZone(t *testing.T) {
	cases := []struct {
		name     string
		tz       string
		expected string
	}{
		{"UTC+3", "Europe/Moscow", "2025-01-01T15:15:00Z"},
		{"UTC-5", "America/Bogota", "2025-01-01T23:15:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			mock.ExpectQuery(vaccinationSelect).
				WillReturnRows(sqlmock.NewRows(vaccinationColumns).
					AddRow(testVaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), timeParse("2025-01-01"), nil, testNextEventID, nil))
			expectPetBelongsToUser(mock, true)
			mock.ExpectQuery(eventForUpdateSelect).
				WillReturnRows(sqlmock.NewRows(eventForUpdateColumns).
					AddRow(testNextEventID, testPetID, mustRFC3339("2025-01-01T09:00:00Z"), "other", nil, []byte(`{"label":"x"}`), false))
			mock.ExpectExec(`UPDATE event\s+SET date_time = \$1\s+WHERE id = \$2`).
				WithArgs(mustRFC3339(tc.expected), uuid.MustParse(testNextEventID)).
				WillReturnResult(sqlmock.NewResult(0, 1))

			eventTime := "18:15"
			w := httptest.NewRecorder()
			r := petRequest(t, http.MethodPatch, "/vaccinations/"+testVaccinationID+"?tz="+tc.tz, models.UpdateVaccinationRequest{EventTime: &eventTime}, true)
			VaccinationByIDHandler(w, r)

			assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUpdateVaccinationHandler_InvalidTimeZone(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(vaccinationSelect).
		WillReturnRows(sqlmock.NewRows(vaccinationColumns).
			AddRow(testVaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), nil, nil, nil, nil))
	expectPetBelongsToUser(mock, true)

	name := "Rabies v2"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPatch, "/vaccinations/"+testVaccinationID+"?tz=Local", models.UpdateVaccinationRequest{Name: &name}, true)
	VaccinationByIDHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "VALIDATION_ERROR")
	require.NoError(t, mock.ExpectationsWereMet())
}

// times расписания курса лекарств — местное время пояса tz: приём "08:00"
// в Europe/Moscow — это 05:00Z, в America/Bogota — 13:00Z (создание курса с
// add_event=true).
func TestCreateMedicationHandler_ScheduleTimesInClientTimeZone(t *testing.T) {
	cases := []struct {
		name     string
		query    string
		expected []string
	}{
		{"UTC+3", "?tz=Europe/Moscow", []string{"2024-01-01T05:00:00Z", "2024-01-02T05:00:00Z"}},
		{"UTC-5", "?tz=America/Bogota", []string{"2024-01-01T13:00:00Z", "2024-01-02T13:00:00Z"}},
		{"без tz", "", []string{"2024-01-01T08:00:00Z", "2024-01-02T08:00:00Z"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			expectPetOwnedForCreate(mock)
			mock.ExpectQuery(`INSERT INTO medication`).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("33333333-3333-3333-3333-333333333339"))
			for i, expected := range tc.expected {
				mock.ExpectQuery(`INSERT INTO event`).
					WithArgs(uuid.MustParse(testPetID), mustRFC3339(expected), "medication", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), false).
					WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(fmt.Sprintf("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa%d", i)))
			}
			mock.ExpectExec(`UPDATE medication SET event_ids`).
				WillReturnResult(sqlmock.NewResult(0, 1))

			addEvent := true
			w := httptest.NewRecorder()
			r := petRequest(t, http.MethodPost, "/pet/"+testPetID+"/medications"+tc.query, models.CreateMedicationRequest{
				Name:          "Amoxicillin",
				Dosage:        "1 tablet",
				FrequencyType: models.MedicationFrequencyDaily,
				Times:         []models.MedicationTimeSlot{{Time: "08:00"}},
				StartDate:     strPtr("2024-01-01"),
				EndDate:       strPtr("2024-01-02"),
				AddEvent:      &addEvent,
			}, true)
			CreateMedicationHandler(w, r, uuid.MustParse(testPetID))

			assert.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// POST /medications/{id}/events строит события по сохранённому расписанию
// в поясе tz.
func TestCreateMedicationEventsHandler_ScheduleTimesInClientTimeZone(t *testing.T) {
	cases := []struct {
		name     string
		tz       string
		expected string
	}{
		{"UTC+3", "Europe/Moscow", "2024-01-01T05:00:00Z"},
		{"UTC-5", "America/Bogota", "2024-01-01T13:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			medicationID := "33333333-3333-3333-3333-333333333336"
			mock.ExpectQuery(medicationSelectRe).
				WillReturnRows(addMedicationRow(sqlmock.NewRows(medicationColumns), medicationID, nil, `[{"time":"08:00"}]`, nil, timeParse("2024-01-01"), timeParse("2024-01-01"), "{}"))
			expectPetBelongsToUser(mock, true)
			mock.ExpectQuery(`INSERT INTO event`).
				WithArgs(uuid.MustParse(testPetID), mustRFC3339(tc.expected), "medication", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), false).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"))
			mock.ExpectExec(`UPDATE medication SET event_ids`).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery(`SELECT owner_id, COUNT\(\*\) FROM file`).
				WillReturnRows(sqlmock.NewRows([]string{"owner_id", "count"}))

			w := httptest.NewRecorder()
			r := petRequest(t, http.MethodPost, "/medications/"+medicationID+"/events?tz="+tc.tz, nil, true)
			MedicationByIDHandler(w, r)

			assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCreateMedicationEventsHandler_InvalidTimeZone(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, "/medications/33333333-3333-3333-3333-333333333336/events?tz=Nowhere/City", nil, true)
	MedicationByIDHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "VALIDATION_ERROR")
	require.NoError(t, mock.ExpectationsWereMet())
}
