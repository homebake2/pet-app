package handlers

import (
	"database/sql"
	"encoding/json"
	"myauthservice/models"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
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
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized, habitation, notes, deleted_at, breed, icon, body_condition\s+FROM pet\s+WHERE id = \$1 AND user_id = \$2`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, "DOG", nil,
		))
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
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized, habitation, notes, deleted_at, breed, icon, body_condition\s+FROM pet\s+WHERE id = \$1 AND user_id = \$2`).
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
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized, habitation, notes, deleted_at, breed, icon, body_condition\s+FROM pet\s+WHERE id = \$1 AND user_id = \$2 AND deleted_at IS NULL`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(
			testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, "DOG", nil,
		))
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
