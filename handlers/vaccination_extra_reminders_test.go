package handlers

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"myauthservice/models"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Дополнительные напоминания вакцинации, next_time, completed_at,
// skip_administered_event — см. «Вакцинации — Backend».

const (
	testExtraPlanID1 = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	testExtraPlanID2 = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
)

// expectDeleteReminderPlan — ожидание жёсткого удаления настроек без
// файлов (database.DeleteReminderPlansWith).
func expectDeleteReminderPlan(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT id FROM reminder WHERE plan_id = ANY`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(`DELETE FROM file WHERE owner_type = \$1 AND owner_id = ANY\(\$2\) RETURNING object_key`).
		WithArgs(reminderPlanFileOwnerType, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"object_key"}))
	mock.ExpectExec(`DELETE FROM reminder_plan WHERE id = ANY`).WillReturnResult(sqlmock.NewResult(0, 1))
}

// expectNewReminderPlanInsert — ожидание вставки настроек с одним
// напоминанием на момент remindAt (UTC).
func expectNewReminderPlanInsert(mock sqlmock.Sqlmock, remindAt string) {
	mock.ExpectExec(`INSERT INTO reminder_plan \(`).
		WithArgs(sqlmock.AnyArg(), uuid.MustParse(testUserID), "vaccination", uuid.MustParse(testVaccinationID), "other",
			`{"label":"Вакцинация: Rabies"}`, sqlmock.AnyArg(), "once", sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "UTC").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO reminder_plan_pet`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO reminder \(id, plan_id, remind_at, notes\)`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), mustRFC3339(remindAt), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

// Создание: дополнительные напоминания создаются отдельными разовыми
// настройками независимо от add_reminder_on_next; next_time сохраняется в
// прививке.
func TestCreateVaccinationHandler_ExtraRemindersCreatePlans(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnedForCreate(mock)
	mock.ExpectBegin()
	expectVaccinationFactInsert(mock)
	mock.ExpectQuery(`INSERT INTO vaccination`).
		WithArgs(testPetID, "Rabies", sqlmock.AnyArg(), "00:00", sqlmock.AnyArg(), "10:00", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testVaccinationID))
	expectNewReminderPlanInsert(mock, "2098-01-01T08:00:00Z")
	expectNewReminderPlanInsert(mock, "2098-01-01T09:00:00Z")
	mock.ExpectCommit()

	nextDate := "2098-01-02"
	nextTime := "10:00"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, vaccinationsPath+"?tz=UTC", models.CreateVaccinationRequest{
		Name: "Rabies", AdministeredDate: "2024-01-01", NextDate: &nextDate, NextTime: &nextTime,
		ExtraReminders: []models.VaccinationExtraReminderRequest{
			{Date: "2098-01-01", Time: "08:00"},
			{Date: "2098-01-01", Time: "09:00"},
		},
	}, true)
	CreateVaccinationHandler(w, r, uuid.MustParse(testPetID))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.JSONEq(t, `{"id":"`+testVaccinationID+`","administered_event_id":"`+testAdministeredEventID+`","next_plan_id":null}`, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// skip_administered_event=true: факт не создаётся, administered_event_id
// остаётся пустым.
func TestCreateVaccinationHandler_SkipAdministeredEvent(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnedForCreate(mock)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO vaccination`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testVaccinationID))
	mock.ExpectCommit()

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPost, vaccinationsPath+"?tz=UTC", models.CreateVaccinationRequest{
		Name: "Rabies", AdministeredDate: "2098-01-01", SkipAdministeredEvent: true,
	}, true)
	CreateVaccinationHandler(w, r, uuid.MustParse(testPetID))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.JSONEq(t, `{"id":"`+testVaccinationID+`","administered_event_id":null,"next_plan_id":null}`, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// Невалидные дополнительные напоминания отклоняются до обращения к БД.
func TestCreateVaccinationHandler_ExtraRemindersInvalid(t *testing.T) {
	nextDate := "2098-01-02"
	valid := models.VaccinationExtraReminderRequest{Date: "2098-01-01", Time: "08:00"}
	six := make([]models.VaccinationExtraReminderRequest, 6)
	for i := range six {
		six[i] = models.VaccinationExtraReminderRequest{Date: "2098-01-01", Time: "0" + string(rune('1'+i)) + ":00"}
	}
	planID := testExtraPlanID1
	cases := map[string]models.CreateVaccinationRequest{
		"без next_date": {
			Name: "Rabies", AdministeredDate: "2024-01-01",
			ExtraReminders: []models.VaccinationExtraReminderRequest{valid},
		},
		"более пяти": {
			Name: "Rabies", AdministeredDate: "2024-01-01", NextDate: &nextDate, ExtraReminders: six,
		},
		"некорректная дата": {
			Name: "Rabies", AdministeredDate: "2024-01-01", NextDate: &nextDate,
			ExtraReminders: []models.VaccinationExtraReminderRequest{{Date: "01.01.2098", Time: "08:00"}},
		},
		"некорректное время": {
			Name: "Rabies", AdministeredDate: "2024-01-01", NextDate: &nextDate,
			ExtraReminders: []models.VaccinationExtraReminderRequest{{Date: "2098-01-01", Time: "8am"}},
		},
		"plan_id при создании": {
			Name: "Rabies", AdministeredDate: "2024-01-01", NextDate: &nextDate,
			ExtraReminders: []models.VaccinationExtraReminderRequest{{PlanID: &planID, Date: "2098-01-01", Time: "08:00"}},
		},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)

			w := httptest.NewRecorder()
			CreateVaccinationHandler(w, petRequest(t, http.MethodPost, vaccinationsPath+"?tz=UTC", req, true), uuid.MustParse(testPetID))

			assert.Equal(t, http.StatusBadRequest, w.Code)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// Моменты дополнительных напоминаний: строго в будущем, не позже next_date +
// next_time и различны между собой и с основным напоминанием.
func TestCreateVaccinationHandler_ExtraReminderMomentsRejected(t *testing.T) {
	nextDate := "2098-01-02"
	nextTime := "10:00"
	addReminder := true
	cases := map[string]models.CreateVaccinationRequest{
		"в прошлом": {
			ExtraReminders: []models.VaccinationExtraReminderRequest{{Date: "2019-01-01", Time: "08:00"}},
		},
		"позже следующей вакцинации": {
			ExtraReminders: []models.VaccinationExtraReminderRequest{{Date: "2098-01-02", Time: "10:01"}},
		},
		"совпадают между собой": {
			ExtraReminders: []models.VaccinationExtraReminderRequest{
				{Date: "2098-01-01", Time: "08:00"}, {Date: "2098-01-01", Time: "08:00"},
			},
		},
		"совпадает с основным": {
			AddReminderOnNext: &addReminder,
			ExtraReminders:    []models.VaccinationExtraReminderRequest{{Date: "2098-01-02", Time: "10:00"}},
		},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			expectPetOwnedForCreate(mock)

			req.Name, req.AdministeredDate, req.NextDate, req.NextTime = "Rabies", "2024-01-01", &nextDate, &nextTime
			w := httptest.NewRecorder()
			CreateVaccinationHandler(w, petRequest(t, http.MethodPost, vaccinationsPath+"?tz=UTC", req, true), uuid.MustParse(testPetID))

			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// Список: next_time, completed_at и extra_reminders (по возрастанию момента;
// пустой массив, если дополнительных напоминаний нет).
func TestGetPetVaccinationsHandler_ReturnsExtrasAndCompletion(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, gender, species, birth_date, color, sterilized`).
		WillReturnRows(sqlmock.NewRows(petColumns).AddRow(testPetID, "Rex", nil, "dog", nil, nil, false, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil))
	expectNoWeightEvent(mock)
	completedID := "66666666-6666-6666-6666-666666666666"
	mock.ExpectQuery(`FROM vaccination\s+WHERE pet_id = \$1 AND deleted_at IS NULL\s+ORDER BY next_date ASC NULLS LAST`).
		WillReturnRows(sqlmock.NewRows(vaccinationColumns).
			AddRow(testVaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), "00:00", timeParse("2098-01-02"), "10:00", nil, nil, testNextPlanID, nil).
			AddRow(completedID, testPetID, "Distemper", timeParse("2023-01-01"), "00:00", nil, nil, mustRFC3339("2024-03-01T08:00:00Z"), nil, nil, nil))
	mock.ExpectQuery(`SELECT owner_id, COUNT\(\*\) FROM file`).
		WillReturnRows(sqlmock.NewRows([]string{"owner_id", "count"}))
	expectVaccinationExtras(mock,
		vaccinationExtraPlanRow(testExtraPlanID2, "2098-01-02", "09:00"),
		vaccinationExtraPlanRow(testExtraPlanID1, "2098-01-01", "08:30"),
	)

	w := httptest.NewRecorder()
	GetPetVaccinationsHandler(w, petRequest(t, http.MethodGet, vaccinationsPath, nil, true), uuid.MustParse(testPetID))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp models.VaccinationListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 2)

	active := resp.Items[0]
	require.NotNil(t, active.NextTime)
	assert.Equal(t, "10:00", *active.NextTime)
	assert.Nil(t, active.CompletedAt)
	assert.Equal(t, []models.VaccinationExtraReminderResponse{
		{PlanID: testExtraPlanID1, Date: "2098-01-01", Time: "08:30"},
		{PlanID: testExtraPlanID2, Date: "2098-01-02", Time: "09:00"},
	}, active.ExtraReminders)

	completed := resp.Items[1]
	assert.Nil(t, completed.NextDate)
	assert.Nil(t, completed.NextTime)
	require.NotNil(t, completed.CompletedAt)
	assert.Equal(t, "2024-03-01T08:00:00Z", *completed.CompletedAt)
	assert.NotNil(t, completed.ExtraReminders)
	assert.Empty(t, completed.ExtraReminders)
	require.NoError(t, mock.ExpectationsWereMet())
}

// activeVaccinationRow — прививка с next_date 2098-01-02 10:00 и
// напоминанием на неё (next_plan_id).
func activeVaccinationRow() []driver.Value {
	return []driver.Value{testVaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), "00:00", timeParse("2098-01-02"), "10:00", nil, nil, testNextPlanID, nil}
}

// expectActiveVaccinationPatchPrelude — предварительное чтение, проверка
// владения и блокировка строки прививки.
func expectVaccinationPatchPrelude(mock sqlmock.Sqlmock, row []driver.Value) {
	mock.ExpectQuery(vaccinationSelect).WillReturnRows(sqlmock.NewRows(vaccinationColumns).AddRow(row...))
	expectPetBelongsToUser(mock, true)
	mock.ExpectBegin()
	mock.ExpectQuery(vaccinationSelect).WillReturnRows(sqlmock.NewRows(vaccinationColumns).AddRow(row...))
}

// next_date: null жёстко удаляет напоминание на следующую дату и все
// дополнительные, очищает next_date и next_time.
func TestUpdateVaccinationHandler_NextDateNullDeletesAllReminders(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectVaccinationPatchPrelude(mock, activeVaccinationRow())
	expectVaccinationExtras(mock, vaccinationExtraPlanRow(testExtraPlanID1, "2098-01-01", "08:00"))
	// Настройки напоминания на следующую дату.
	mock.ExpectQuery(`FROM reminder_plan p`).
		WillReturnRows(sqlmock.NewRows(reminderPlanSelectColumns).AddRow(
			testNextPlanID, testUserID, "vaccination", testVaccinationID, "other", []byte(`{"label":"Вакцинация: Rabies"}`), nil, "once", nil, nil,
			"{10:00}", timeParse("2098-01-02"), nil, "UTC", timeParse("2024-01-01"), "Rabies"))
	mock.ExpectQuery(`SELECT link\.plan_id, pet\.id, pet\.name, pet\.species\s+FROM reminder_plan_pet link`).
		WillReturnRows(sqlmock.NewRows([]string{"plan_id", "id", "name", "species"}).AddRow(testNextPlanID, testPetID, "Rex", "DOG"))
	expectDeleteReminderPlan(mock)
	expectDeleteReminderPlan(mock)
	mock.ExpectExec(`UPDATE vaccination SET next_date = \$1, next_time = \$2::time, next_plan_id = \$3 WHERE id = \$4`).
		WithArgs(nil, nil, nil, testVaccinationID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPatch, "/vaccinations/"+testVaccinationID+"?tz=UTC", map[string]any{"next_date": nil}, true)
	VaccinationByIDHandler(w, r)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// Непустой extra_reminders вместе с next_date: null — 400.
func TestUpdateVaccinationHandler_ExtrasWithNullNextDateRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectVaccinationPatchPrelude(mock, activeVaccinationRow())
	expectVaccinationExtras(mock)
	mock.ExpectQuery(`FROM reminder_plan p`).WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPatch, "/vaccinations/"+testVaccinationID+"?tz=UTC", map[string]any{
		"next_date":       nil,
		"extra_reminders": []map[string]string{{"date": "2098-01-01", "time": "08:00"}},
	}, true)
	VaccinationByIDHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// plan_id, не принадлежащий дополнительным напоминаниям этой вакцинации (в
// том числе равный next_plan_id), — 400, ничего не меняется.
func TestUpdateVaccinationHandler_ForeignExtraPlanIDRejected(t *testing.T) {
	for _, planID := range []string{testNextPlanID, testExtraPlanID2} {
		t.Run(planID, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			expectVaccinationPatchPrelude(mock, activeVaccinationRow())
			expectVaccinationExtras(mock, vaccinationExtraPlanRow(testExtraPlanID1, "2098-01-01", "08:00"))
			mock.ExpectQuery(`FROM reminder_plan p`).WillReturnError(sql.ErrNoRows)
			mock.ExpectRollback()

			w := httptest.NewRecorder()
			r := petRequest(t, http.MethodPatch, "/vaccinations/"+testVaccinationID+"?tz=UTC", models.UpdateVaccinationRequest{
				ExtraReminders: &[]models.VaccinationExtraReminderRequest{{PlanID: &planID, Date: "2098-01-01", Time: "08:00"}},
			}, true)
			VaccinationByIDHandler(w, r)

			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// Перенос next_date раньше существующего дополнительного напоминания — 400 и
// откат: проверка инварианта «все дополнительные не позже».
func TestUpdateVaccinationHandler_MovingNextDateBeforeExtraRejected(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectVaccinationPatchPrelude(mock, activeVaccinationRow())
	expectVaccinationExtras(mock, vaccinationExtraPlanRow(testExtraPlanID1, "2098-01-05", "08:00"))
	// Основное напоминание не создавалось (next_plan_id не найден).
	mock.ExpectQuery(`FROM reminder_plan p`).WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	nextDate := "2098-01-03"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPatch, "/vaccinations/"+testVaccinationID+"?tz=UTC", models.UpdateVaccinationRequest{
		NextDate: &nextDate,
	}, true)
	VaccinationByIDHandler(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// Заданная next_date на завершённой вакцинации очищает completed_at и
// записывает next_time (по умолчанию — полночь).
func TestUpdateVaccinationHandler_NextDateOnCompletedClearsCompletedAt(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	completed := []driver.Value{testVaccinationID, testPetID, "Rabies", timeParse("2024-01-01"), "00:00", nil, nil, mustRFC3339("2024-03-01T08:00:00Z"), nil, nil, nil}
	expectVaccinationPatchPrelude(mock, completed)
	expectVaccinationExtras(mock)
	mock.ExpectExec(`UPDATE vaccination SET next_date = \$1, next_time = \$2::time, completed_at = \$3 WHERE id = \$4`).
		WithArgs(sqlmock.AnyArg(), "00:00", nil, testVaccinationID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	nextDate := "2098-01-02"
	w := httptest.NewRecorder()
	r := petRequest(t, http.MethodPatch, "/vaccinations/"+testVaccinationID+"?tz=UTC", models.UpdateVaccinationRequest{
		NextDate: &nextDate,
	}, true)
	VaccinationByIDHandler(w, r)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// Удаление вакцинации жёстко удаляет и дополнительные напоминания.
func TestDeleteVaccinationHandler_DeletesExtraReminders(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(vaccinationSelect).
		WillReturnRows(sqlmock.NewRows(vaccinationColumns).AddRow(activeVaccinationRow()...))
	expectPetBelongsToUser(mock, true)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE vaccination SET deleted_at`).WillReturnResult(sqlmock.NewResult(0, 1))
	expectDeleteReminderPlan(mock) // напоминание на следующую дату
	expectVaccinationExtras(mock,
		vaccinationExtraPlanRow(testExtraPlanID1, "2098-01-01", "08:00"),
		vaccinationExtraPlanRow(testExtraPlanID2, "2098-01-01", "09:00"),
	)
	expectDeleteReminderPlan(mock) // оба дополнительных одним запросом
	mock.ExpectCommit()

	w := httptest.NewRecorder()
	VaccinationByIDHandler(w, petRequest(t, http.MethodDelete, "/vaccinations/"+testVaccinationID, nil, true))

	assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// Импорт: next_time, completed_at и extra_reminder_plan_local_ids
// валидируются до обращения к БД.
func TestImportLocalDataHandler_VaccinationExtrasRejected(t *testing.T) {
	oncePlan := func(localID string) models.ImportReminderPlan {
		plan := validImportReminderPlan(localID, "local-1")
		plan.FrequencyType = "once"
		plan.Type = "other"
		plan.Value = eventValue(`{"label":"Прививка"}`)
		return plan
	}
	base := func() models.ImportVaccination {
		return models.ImportVaccination{
			LocalID: "vac-1", PetLocalID: "local-1", Name: "Бешенство", AdministeredDate: "2024-01-01",
			NextDate: strPtr("2024-02-01"),
		}
	}
	mutate := func(f func(v *models.ImportVaccination)) models.ImportVaccination {
		v := base()
		f(&v)
		return v
	}
	cases := []struct {
		name string
		plan []models.ImportReminderPlan
		vac  models.ImportVaccination
	}{
		{"next_time без next_date", nil, mutate(func(v *models.ImportVaccination) {
			v.NextDate, v.NextTime = nil, strPtr("10:00")
		})},
		{"некорректный next_time", nil, mutate(func(v *models.ImportVaccination) { v.NextTime = strPtr("10h") })},
		{"completed_at вместе с next_date", nil, mutate(func(v *models.ImportVaccination) {
			v.CompletedAt = strPtr("2024-03-01T08:00:00Z")
		})},
		{"extra без next_date", []models.ImportReminderPlan{oncePlan("p1")}, mutate(func(v *models.ImportVaccination) {
			v.NextDate, v.ExtraReminderPlanLocalIDs = nil, []string{"p1"}
		})},
		{"extra более пяти", nil, mutate(func(v *models.ImportVaccination) {
			v.ExtraReminderPlanLocalIDs = []string{"a", "b", "c", "d", "e", "f"}
		})},
		{"extra повторяются", []models.ImportReminderPlan{oncePlan("p1")}, mutate(func(v *models.ImportVaccination) {
			v.ExtraReminderPlanLocalIDs = []string{"p1", "p1"}
		})},
		{"extra включает next_reminder_plan_local_id", []models.ImportReminderPlan{oncePlan("p1")}, mutate(func(v *models.ImportVaccination) {
			v.NextReminderPlanLocalID, v.ExtraReminderPlanLocalIDs = strPtr("p1"), []string{"p1"}
		})},
		{"extra не найден в reminder_plans", []models.ImportReminderPlan{oncePlan("p1")}, mutate(func(v *models.ImportVaccination) {
			v.ExtraReminderPlanLocalIDs = []string{"nope"}
		})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectTokensValid(mock, testUserID)
			mock.ExpectExec(`INSERT INTO import_local_data_idempotency_key`).
				WithArgs(testUserID, testImportIdempotencyKey).
				WillReturnResult(sqlmock.NewResult(0, 1))

			plans := c.plan
			if plans == nil {
				plans = []models.ImportReminderPlan{}
			}
			w := httptest.NewRecorder()
			ImportLocalDataHandler(w, importRequest(t, models.ImportLocalDataRequest{
				Pets:          []models.ImportLocalDataPet{validImportPet("local-1")},
				Events:        []models.ImportLocalDataEvent{},
				ReminderPlans: plans,
				Vaccinations:  []models.ImportVaccination{c.vac},
			}, true, testImportIdempotencyKey))

			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
