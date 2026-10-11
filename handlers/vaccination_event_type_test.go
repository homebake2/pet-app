package handlers

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"myauthservice/database"
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

// Системный тип события vaccination: запреты POST/PATCH, поддержка
// value.vaccination_at у дополнительных напоминаний, импорт.

// vaccinationWithoutMainPlanRow — прививка с next_date 2098-01-02 10:00 без
// напоминания на следующую дату (next_plan_id пуст).
func vaccinationWithoutMainPlanRow() []driver.Value {
	return []driver.Value{testVaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), "00:00", timeParse("2098-01-02"), "10:00", nil, nil, nil, nil}
}

func TestCreateEventHandler_VaccinationTypeRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := postEvent(t, createBody([]string{testPetID}, "vaccination", `{"name":"Rabies"}`))

	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// У события типа vaccination type и value через PATCH не меняются.
func TestUpdateEventHandler_VaccinationTypeAndValueImmutable(t *testing.T) {
	newType := "other"
	cases := []struct {
		name string
		req  models.UpdateEventRequest
	}{
		{"value", models.UpdateEventRequest{Value: eventValuePtr(`{"name":"Other"}`)}},
		{"type и value", models.UpdateEventRequest{Type: &newType, Value: eventValuePtr(`{"label":"x"}`)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			expectEventForUser(mock, testEventID, "vaccination", `{"name":"Rabies"}`, petRex)

			w := patchEvent(t, c.req)

			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// Дата и заметка факта вакцинации редактируются как у обычного события.
func TestUpdateEventHandler_VaccinationNotesEditable(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectEventForUser(mock, testEventID, "vaccination", `{"name":"Rabies"}`, petRex)
	mock.ExpectBegin()
	expectEventForUser(mock, testEventID, "vaccination", `{"name":"Rabies"}`, petRex)
	mock.ExpectExec(`UPDATE event SET`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	notes := "заметка"
	w := patchEvent(t, models.UpdateEventRequest{Notes: &notes})

	assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// Другую запись нельзя перевести в тип vaccination.
func TestUpdateEventHandler_ChangeTypeToVaccinationRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectEventForUser(mock, testEventID, "other", `{"label":"x"}`, petRex)

	newType := "vaccination"
	w := patchEvent(t, models.UpdateEventRequest{Type: &newType, Value: eventValuePtr(`{"name":"Rabies"}`)})

	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// Импорт проверяет форму value типа vaccination по реестру.
func TestImportLocalDataHandler_InvalidVaccinationValueRejected(t *testing.T) {
	cases := map[string]string{
		"нет name":          `{}`,
		"пустое name":       `{"name":""}`,
		"vaccination_at":    `{"name":"Rabies","vaccination_at":"2098-01-02"}`,
		"лишнее поле label": `{"name":"Rabies","label":"x"}`,
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			mock.ExpectExec(`INSERT INTO import_local_data_idempotency_key`).
				WithArgs(testUserID, testImportIdempotencyKey).
				WillReturnResult(sqlmock.NewResult(0, 1))

			w := httptest.NewRecorder()
			r := importRequest(t, models.ImportLocalDataRequest{
				ReminderPlans: []models.ImportReminderPlan{},
				Pets:          []models.ImportLocalDataPet{validImportPet("local-1")},
				Events: []models.ImportLocalDataEvent{{
					LocalID: "event-1", PetLocalIDs: []string{"local-1"}, Date: "2024-01-01T12:00:00Z",
					Type: "vaccination", Value: eventValue(value),
				}},
			}, true, testImportIdempotencyKey)
			ImportLocalDataHandler(w, r)

			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// PATCH next_time пересчитывает value.vaccination_at дополнительных
// напоминаний в той же транзакции; их расписание не меняется.
func TestUpdateVaccinationHandler_NextTimeRecomputesExtraVaccinationAt(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectVaccinationPatchPrelude(mock, vaccinationWithoutMainPlanRow())
	expectVaccinationExtras(mock, vaccinationExtraPlanRow(testExtraPlanID1, "2098-01-01", "08:00"))
	mock.ExpectExec(`UPDATE reminder_plan SET value = \$1 WHERE id = \$2`).
		WithArgs(`{"name":"Rabies","vaccination_at":"2098-01-02T11:00:00Z"}`, testExtraPlanID1).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE vaccination SET next_time = \$1::time WHERE id = \$2`).
		WithArgs("11:00", testVaccinationID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	nextTime := "11:00"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPatch, "/vaccinations/"+testVaccinationID+"?tz=UTC", models.UpdateVaccinationRequest{NextTime: &nextTime}, true)
	VaccinationByIDHandler(w, r)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// vaccination_at записывается со смещением пояса tz запроса.
func TestUpdateVaccinationHandler_VaccinationAtUsesRequestOffset(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectVaccinationPatchPrelude(mock, vaccinationWithoutMainPlanRow())
	expectVaccinationExtras(mock, vaccinationExtraPlanRow(testExtraPlanID1, "2098-01-01", "08:00"))
	mock.ExpectExec(`UPDATE reminder_plan SET value = \$1 WHERE id = \$2`).
		WithArgs(`{"name":"Rabies","vaccination_at":"2098-01-02T11:00:00+03:00"}`, testExtraPlanID1).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE vaccination SET next_time = \$1::time WHERE id = \$2`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	nextTime := "11:00"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPatch, "/vaccinations/"+testVaccinationID+"?tz=Europe/Moscow", models.UpdateVaccinationRequest{NextTime: &nextTime}, true)
	VaccinationByIDHandler(w, r)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// Изменение name обновляет value.name дополнительных напоминаний (момент
// следующей вакцинации прежний).
func TestUpdateVaccinationHandler_RenameUpdatesExtraValue(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectVaccinationPatchPrelude(mock, vaccinationWithoutMainPlanRow())
	expectVaccinationExtras(mock, vaccinationExtraPlanRow(testExtraPlanID1, "2098-01-01", "08:00"))
	mock.ExpectExec(`UPDATE reminder_plan SET value = \$1 WHERE id = \$2`).
		WithArgs(`{"name":"Rabies v2","vaccination_at":"2098-01-02T10:00:00Z"}`, testExtraPlanID1).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE vaccination SET name = \$1 WHERE id = \$2`).
		WithArgs("Rabies v2", testVaccinationID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	newName := "Rabies v2"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPatch, "/vaccinations/"+testVaccinationID+"?tz=UTC", models.UpdateVaccinationRequest{Name: &newName}, true)
	VaccinationByIDHandler(w, r)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// Перенос напоминания на следующую вакцинацию из календаря пересчитывает
// vaccination_at дополнительных; без изменения момента запросов нет.
func TestRefreshVaccinationExtraMoments(t *testing.T) {
	vaccination := &models.VaccinationDB{
		ID:       uuid.MustParse(testVaccinationID),
		Name:     "Rabies",
		NextDate: sql.NullTime{Time: timeParse("2098-01-02"), Valid: true},
		NextTime: sql.NullString{String: "10:00", Valid: true},
	}
	extras := []models.ReminderPlanDB{{ID: uuid.MustParse(testExtraPlanID1)}, {ID: uuid.MustParse(testExtraPlanID2)}}
	utc := time.UTC

	t.Run("момент изменился", func(t *testing.T) {
		mock := setupMockDB(t)
		for _, id := range []string{testExtraPlanID1, testExtraPlanID2} {
			mock.ExpectExec(`UPDATE reminder_plan SET value = \$1 WHERE id = \$2`).
				WithArgs(`{"name":"Rabies","vaccination_at":"2098-01-03T09:30:00Z"}`, id).
				WillReturnResult(sqlmock.NewResult(0, 1))
		}
		require.NoError(t, refreshVaccinationExtraMoments(database.DB, vaccination, extras, timeParse("2098-01-03"), "09:30", utc))
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("момент прежний", func(t *testing.T) {
		mock := setupMockDB(t)
		require.NoError(t, refreshVaccinationExtraMoments(database.DB, vaccination, extras, timeParse("2098-01-02"), "10:00", utc))
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// Факт, создаваемый отметкой «выполнено» дополнительного напоминания, не
// несёт vaccination_at.
func TestFactValueFromPlanValue(t *testing.T) {
	got, err := factValueFromPlanValue("vaccination", json.RawMessage(`{"name":"Rabies","vaccination_at":"2098-01-02T10:00:00Z"}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"Rabies"}`, string(got))

	got, err = factValueFromPlanValue("other", json.RawMessage(`{"label":"x"}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"label":"x"}`, string(got))
}
