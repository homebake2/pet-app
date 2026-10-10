//go:build integration

// Ведпаспорт (медкарта питомца): интеграционные тесты для 5 новых
// pet_id-scoped сущностей (Vaccination, Disease, VetVisit, Allergy,
// Medication) — happy-path CRUD, ownership (чужой pet_id/сущность -> 404) и
// базовая валидация, поверх реального HTTP (handlers.NewMux()) и Postgres,
// со сверкой каждого запроса/ответа с open-api/spec.json (см. doRequest в
// helpers_test.go).
package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

type idResponse struct {
	ID string `json:"id"`
}

func TestVaccination_CRUDHappyPath(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/vaccinations?tz=UTC", map[string]any{
		"name":              "Rabies",
		"administered_date": "2024-01-01",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, createResp.status, "%s", createResp.body)
	var created idResponse
	createResp.decode(t, &created)
	require.NotEmpty(t, created.ID)

	list := doRequest(t, http.MethodGet, "/pet/"+petID+"/vaccinations", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, list.status)
	var listBody struct {
		Items []struct {
			ID               string `json:"id"`
			Name             string `json:"name"`
			AdministeredDate string `json:"administered_date"`
			FilesCount       int    `json:"files_count"`
		} `json:"items"`
	}
	list.decode(t, &listBody)
	require.Len(t, listBody.Items, 1)
	require.Equal(t, created.ID, listBody.Items[0].ID)
	require.Equal(t, "Rabies", listBody.Items[0].Name)
	require.Equal(t, 0, listBody.Items[0].FilesCount)

	patch := doRequest(t, http.MethodPatch, "/vaccinations/"+created.ID+"?tz=UTC", map[string]any{
		"name": "Rabies (updated)",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, patch.status, "%s", patch.body)

	list2 := doRequest(t, http.MethodGet, "/pet/"+petID+"/vaccinations", nil, tokens.AccessToken)
	list2.decode(t, &listBody)
	require.Equal(t, "Rabies (updated)", listBody.Items[0].Name)

	del := doRequest(t, http.MethodDelete, "/vaccinations/"+created.ID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, del.status)

	list3 := doRequest(t, http.MethodGet, "/pet/"+petID+"/vaccinations", nil, tokens.AccessToken)
	list3.decode(t, &listBody)
	require.Empty(t, listBody.Items)
}

func TestVaccination_OwnershipEnforced(t *testing.T) {
	resetDB(t)
	owner := registerUser(t, uniqueLogin(t), "correct-password")
	stranger := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, owner.AccessToken, "Барсик")

	// Чужой pet_id -> 404 при создании.
	createOnForeignPet := doRequest(t, http.MethodPost, "/pet/"+petID+"/vaccinations?tz=UTC", map[string]any{
		"name":              "Rabies",
		"administered_date": "2024-01-01",
	}, stranger.AccessToken)
	require.Equal(t, http.StatusNotFound, createOnForeignPet.status)

	createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/vaccinations?tz=UTC", map[string]any{
		"name":              "Rabies",
		"administered_date": "2024-01-01",
	}, owner.AccessToken)
	require.Equal(t, http.StatusCreated, createResp.status)
	var created idResponse
	createResp.decode(t, &created)

	// Чужая сущность -> 404 при PATCH/DELETE.
	patch := doRequest(t, http.MethodPatch, "/vaccinations/"+created.ID+"?tz=UTC", map[string]any{"name": "Hacked"}, stranger.AccessToken)
	require.Equal(t, http.StatusNotFound, patch.status)
	del := doRequest(t, http.MethodDelete, "/vaccinations/"+created.ID, nil, stranger.AccessToken)
	require.Equal(t, http.StatusNotFound, del.status)
}

func TestVaccination_ValidationErrors(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	resp := doRequest(t, http.MethodPost, "/pet/"+petID+"/vaccinations?tz=UTC", map[string]any{
		"name":              "",
		"administered_date": "2024-01-01",
	}, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, resp.status)

	// tz обязателен у создания и редактирования.
	noTZ := doUnvalidatedRequest(t, http.MethodPost, "/pet/"+petID+"/vaccinations", map[string]any{
		"name":              "Rabies",
		"administered_date": "2024-01-01",
	}, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, noTZ.status)

	// Факт на дату введения не может быть в будущем; напоминание на
	// следующую дату — в прошлом. Ни одна запись при 400 не создаётся.
	futureFact := doRequest(t, http.MethodPost, "/pet/"+petID+"/vaccinations?tz=UTC", map[string]any{
		"name":              "Rabies",
		"administered_date": futureDate(10),
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, futureFact.status, "%s", futureFact.body)
	pastReminder := doRequest(t, http.MethodPost, "/pet/"+petID+"/vaccinations?tz=UTC", map[string]any{
		"name":                 "Rabies",
		"administered_date":    "2023-01-01",
		"next_date":            "2024-01-01",
		"add_reminder_on_next": true,
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, pastReminder.status, "%s", pastReminder.body)

	list := doRequest(t, http.MethodGet, "/pet/"+petID+"/vaccinations", nil, tokens.AccessToken)
	var listBody struct {
		Items []idResponse `json:"items"`
	}
	list.decode(t, &listBody)
	require.Empty(t, listBody.Items, "прививка не должна остаться после 400")
}

// Факт на дату введения и напоминание на следующую дату: факт попадает в
// события питомца, напоминание — в настройки (source=vaccination) и
// календарь, но не в события.
func TestVaccination_AutoCreatesFactAndReminder(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	nextDate := futureDate(30)

	createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/vaccinations?tz=UTC", map[string]any{
		"name":                 "Rabies",
		"administered_date":    "2024-01-01",
		"next_date":            nextDate,
		"add_reminder_on_next": true,
		"administered_time":    "09:30",
		"next_time":            "09:30",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, createResp.status, "%s", createResp.body)
	var created struct {
		ID                  string  `json:"id"`
		AdministeredEventID *string `json:"administered_event_id"`
		NextPlanID          *string `json:"next_plan_id"`
	}
	createResp.decode(t, &created)
	require.NotNil(t, created.AdministeredEventID)
	require.NotNil(t, created.NextPlanID)

	list := doRequest(t, http.MethodGet, "/pet/"+petID+"/vaccinations", nil, tokens.AccessToken)
	var listBody struct {
		Items []struct {
			AdministeredEventID *string `json:"administered_event_id"`
			NextPlanID          *string `json:"next_plan_id"`
		} `json:"items"`
	}
	list.decode(t, &listBody)
	require.Len(t, listBody.Items, 1)
	require.Equal(t, *created.AdministeredEventID, *listBody.Items[0].AdministeredEventID)
	require.Equal(t, *created.NextPlanID, *listBody.Items[0].NextPlanID)

	// В событиях питомца — только факт.
	events := listPetEvents(t, tokens.AccessToken, petID)
	require.Len(t, events, 1)
	require.Equal(t, "2024-01-01T09:30:00Z", events[0].Date)

	plan := getReminderPlan(t, tokens.AccessToken, *created.NextPlanID)
	require.Equal(t, "vaccination", plan.Source)
	require.NotNil(t, plan.SourceID)
	require.Equal(t, created.ID, *plan.SourceID)
	require.NotNil(t, plan.SourceTitle)
	require.Equal(t, "Rabies", *plan.SourceTitle)
	require.Equal(t, "once", plan.FrequencyType)
	require.Len(t, plan.Reminders, 1)
	require.Equal(t, nextDate+"T09:30:00Z", plan.Reminders[0].RemindAt)
}

func TestDisease_CRUDHappyPath(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/diseases", map[string]any{
		"name":           "Diabetes",
		"diagnosed_date": "2024-01-01",
		"status":         "active",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, createResp.status, "%s", createResp.body)
	var created idResponse
	createResp.decode(t, &created)

	patch := doRequest(t, http.MethodPatch, "/diseases/"+created.ID, map[string]any{"status": "cured"}, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, patch.status)

	list := doRequest(t, http.MethodGet, "/pet/"+petID+"/diseases", nil, tokens.AccessToken)
	var listBody struct {
		Items []struct {
			Status string `json:"status"`
		} `json:"items"`
	}
	list.decode(t, &listBody)
	require.Len(t, listBody.Items, 1)
	require.Equal(t, "cured", listBody.Items[0].Status)

	del := doRequest(t, http.MethodDelete, "/diseases/"+created.ID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, del.status)
}

// Некорректное значение enum (status: "not-a-status") здесь намеренно не
// проверяется через doRequest: сам запрос был бы отклонён проверкой
// гармонизации со спекой (см. UpdateVaccinationRequest.status в
// GetDiseaseRequest, enum DiseaseStatusEnum), а не хендлером — аналогично
// комментарию про Idempotency-Key в import_test.go. Это покрыто unit-тестом
// TestCreateDiseaseHandler_InvalidStatus (handlers/vetpassport_test.go);
// здесь проверяется только валидация, не завязанная на enum в спеке (пустое
// обязательное строковое поле name — минимальная длина в спеке не задана).
func TestDisease_EmptyNameRejected(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	resp := doRequest(t, http.MethodPost, "/pet/"+petID+"/diseases", map[string]any{
		"name":           "",
		"diagnosed_date": "2024-01-01",
		"status":         "active",
	}, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, resp.status)
}

func TestVetVisit_CRUDHappyPath(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/vet-visits", map[string]any{
		"visit_date": "2024-01-01",
		"reason":     "Annual checkup",
		"clinic":     "VetClinic",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, createResp.status, "%s", createResp.body)
	var created idResponse
	createResp.decode(t, &created)

	patch := doRequest(t, http.MethodPatch, "/vet-visits/"+created.ID, map[string]any{"reason": "Follow-up"}, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, patch.status)

	del := doRequest(t, http.MethodDelete, "/vet-visits/"+created.ID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, del.status)

	list := doRequest(t, http.MethodGet, "/pet/"+petID+"/vet-visits", nil, tokens.AccessToken)
	var listBody struct {
		Items []struct{ ID string } `json:"items"`
	}
	list.decode(t, &listBody)
	require.Empty(t, listBody.Items)
}

func TestVetVisit_MissingReasonRejected(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	resp := doRequest(t, http.MethodPost, "/pet/"+petID+"/vet-visits", map[string]any{
		"visit_date": "2024-01-01",
		"reason":     "",
	}, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, resp.status)
}

func TestAllergy_CRUDHappyPath(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/allergies", map[string]any{
		"allergen": "Pollen",
		"severity": "mild",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, createResp.status, "%s", createResp.body)
	var created idResponse
	createResp.decode(t, &created)

	patch := doRequest(t, http.MethodPatch, "/allergies/"+created.ID, map[string]any{"severity": "severe"}, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, patch.status)

	list := doRequest(t, http.MethodGet, "/pet/"+petID+"/allergies", nil, tokens.AccessToken)
	var listBody struct {
		Items []struct {
			Severity string `json:"severity"`
		} `json:"items"`
	}
	list.decode(t, &listBody)
	require.Len(t, listBody.Items, 1)
	require.Equal(t, "severe", listBody.Items[0].Severity)

	del := doRequest(t, http.MethodDelete, "/allergies/"+created.ID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, del.status)
}

// Некорректный severity не проверяется здесь по той же причине, что и
// status у disease выше — покрыто unit-тестом
// TestCreateAllergyHandler_InvalidSeverity.
func TestAllergy_EmptyAllergenRejected(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	resp := doRequest(t, http.MethodPost, "/pet/"+petID+"/allergies", map[string]any{
		"allergen": "",
		"severity": "mild",
	}, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, resp.status)
}

func TestMedication_CRUDAndRemindersLifecycle(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	start, end := futureDate(1), futureDate(3)
	createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/medications?tz=UTC", map[string]any{
		"name":           "Amoxicillin",
		"dosage":         "1 tablet",
		"frequency_type": "daily",
		"times":          []map[string]any{{"time": "08:00"}},
		"start_date":     start,
		"end_date":       end,
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, createResp.status, "%s", createResp.body)
	var created idResponse
	createResp.decode(t, &created)

	// Без add_reminders набора нет.
	medications := listMedications(t, tokens.AccessToken, petID)
	require.Len(t, medications, 1)
	require.Nil(t, medications[0].ReminderPlanID)

	// POST /medications/{id}/reminders создаёт набор: 3 дня x 1 время/день
	// = 3 напоминания в настройках с источником medication.
	remindersResp := doRequest(t, http.MethodPost, "/medications/"+created.ID+"/reminders?tz=UTC", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, remindersResp.status, "%s", remindersResp.body)
	var medication medicationBody
	remindersResp.decode(t, &medication)
	require.NotNil(t, medication.ReminderPlanID)

	plan := getReminderPlan(t, tokens.AccessToken, *medication.ReminderPlanID)
	require.Equal(t, "medication", plan.Source)
	require.Equal(t, "medication", plan.Type)
	require.NotNil(t, plan.Notes)
	require.Equal(t, "1 tablet", *plan.Notes)
	require.NotNil(t, plan.SourceTitle)
	require.Equal(t, "Amoxicillin", *plan.SourceTitle)
	require.Len(t, plan.Reminders, 3)

	// Повторный вызов, пока набор есть, — конфликт.
	conflictResp := doRequest(t, http.MethodPost, "/medications/"+created.ID+"/reminders?tz=UTC", nil, tokens.AccessToken)
	require.Equal(t, http.StatusConflict, conflictResp.status)

	// Напоминания не попадают в события питомца (они — не факты).
	require.Empty(t, listPetEvents(t, tokens.AccessToken, petID))

	// DELETE /medications/{id}/reminders жёстко удаляет настройки и
	// напоминания; ссылка на чтении становится null.
	deleteReminders := doRequest(t, http.MethodDelete, "/medications/"+created.ID+"/reminders", nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, deleteReminders.status)
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder`))

	// Повторное удаление, когда набора уже нет, — 404.
	deleteAgain := doRequest(t, http.MethodDelete, "/medications/"+created.ID+"/reminders", nil, tokens.AccessToken)
	require.Equal(t, http.StatusNotFound, deleteAgain.status)

	medications = listMedications(t, tokens.AccessToken, petID)
	require.Len(t, medications, 1)
	require.Nil(t, medications[0].ReminderPlanID)

	del := doRequest(t, http.MethodDelete, "/medications/"+created.ID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, del.status)
}

func TestMedication_AsNeeded_NoSchedule(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/medications?tz=UTC", map[string]any{
		"name":           "Painkiller",
		"dosage":         "1 tablet",
		"frequency_type": "as_needed",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, createResp.status, "%s", createResp.body)
	var created idResponse
	createResp.decode(t, &created)

	// Нет расписания у as_needed — 400.
	remindersResp := doRequest(t, http.MethodPost, "/medications/"+created.ID+"/reminders?tz=UTC", nil, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, remindersResp.status)

	medications := listMedications(t, tokens.AccessToken, petID)
	require.Len(t, medications, 1)
	require.Nil(t, medications[0].NextDose)
}

// tz обязателен у GET списка, POST, PATCH и POST .../reminders.
func TestMedication_TimeZoneRequired(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	body := map[string]any{"name": "Painkiller", "dosage": "1 tablet", "frequency_type": "as_needed"}
	require.Equal(t, http.StatusBadRequest, doUnvalidatedRequest(t, http.MethodPost, "/pet/"+petID+"/medications", body, tokens.AccessToken).status)
	require.Equal(t, http.StatusBadRequest, doUnvalidatedRequest(t, http.MethodGet, "/pet/"+petID+"/medications", nil, tokens.AccessToken).status)

	createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/medications?tz=UTC", body, tokens.AccessToken)
	require.Equal(t, http.StatusCreated, createResp.status)
	var created idResponse
	createResp.decode(t, &created)
	require.Equal(t, http.StatusBadRequest, doUnvalidatedRequest(t, http.MethodPatch, "/medications/"+created.ID, map[string]any{"note": "x"}, tokens.AccessToken).status)
	require.Equal(t, http.StatusBadRequest, doUnvalidatedRequest(t, http.MethodPost, "/medications/"+created.ID+"/reminders", nil, tokens.AccessToken).status)
}

// add_reminders=true при создании: набор создаётся сразу; PATCH без
// regenerate_reminders меняет поля лекарства, но не набор; с
// regenerate_reminders=true будущие напоминания пересоздаются по новому
// расписанию; name/dosage применяются к настройкам набора.
func TestMedication_PatchRegenerateReminders(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	start := futureDate(1)
	createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/medications?tz=UTC", map[string]any{
		"name":           "Amoxicillin",
		"dosage":         "1 tablet",
		"frequency_type": "daily",
		"times":          []map[string]any{{"time": "08:00", "dose_note": "2 пипетки"}},
		"start_date":     start,
		"end_date":       futureDate(2),
		"add_reminders":  true,
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, createResp.status, "%s", createResp.body)
	var created idResponse
	createResp.decode(t, &created)

	medications := listMedications(t, tokens.AccessToken, petID)
	require.Len(t, medications, 1)
	require.NotNil(t, medications[0].ReminderPlanID)
	planID := *medications[0].ReminderPlanID
	plan := getReminderPlan(t, tokens.AccessToken, planID)
	require.Len(t, plan.Reminders, 2)

	// Доза по времени приёма — заметка напоминания, а не настроек.
	reminder := getReminder(t, tokens.AccessToken, plan.Reminders[0].ID)
	require.NotNil(t, reminder.Notes)
	require.Equal(t, "2 пипетки", *reminder.Notes)
	require.Equal(t, 2, reminder.PlanUnclosedCount)

	// regenerate_reminders=false (по умолчанию) — поля расписания лекарства
	// меняются, но набор остаётся прежним.
	patchNoRegen := doRequest(t, http.MethodPatch, "/medications/"+created.ID+"?tz=UTC", map[string]any{
		"end_date": futureDate(5),
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, patchNoRegen.status, "%s", patchNoRegen.body)
	plan = getReminderPlan(t, tokens.AccessToken, planID)
	require.Len(t, plan.Reminders, 2)
	medications = listMedications(t, tokens.AccessToken, petID)
	require.Equal(t, futureDate(5), *medications[0].EndDate)

	// name/dosage применяются к настройкам набора без пересоздания.
	patchName := doRequest(t, http.MethodPatch, "/medications/"+created.ID+"?tz=UTC", map[string]any{
		"name":   "Amoxicillin Forte",
		"dosage": "2 tablets",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, patchName.status, "%s", patchName.body)
	plan = getReminderPlan(t, tokens.AccessToken, planID)
	require.Equal(t, "Amoxicillin Forte", plan.Value.Name)
	require.Equal(t, "2 tablets", *plan.Notes)
	require.Len(t, plan.Reminders, 2)

	// regenerate_reminders=true — будущие напоминания пересоздаются по
	// обновлённому расписанию (4 дня x 1 время = 4 напоминания).
	patchRegen := doRequest(t, http.MethodPatch, "/medications/"+created.ID+"?tz=UTC", map[string]any{
		"end_date":             futureDate(4),
		"regenerate_reminders": true,
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, patchRegen.status, "%s", patchRegen.body)
	plan = getReminderPlan(t, tokens.AccessToken, planID)
	require.Len(t, plan.Reminders, 4)
	require.NotNil(t, plan.EndDate)
	require.Equal(t, futureDate(4), *plan.EndDate)

	// Переход в as_needed при наличии набора без regenerate — 400; с ним —
	// набор удаляется.
	asNeeded := map[string]any{"frequency_type": "as_needed", "weekdays": nil, "interval_days": nil, "times": nil, "start_date": nil, "end_date": nil}
	rejected := doRequest(t, http.MethodPatch, "/medications/"+created.ID+"?tz=UTC", asNeeded, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, rejected.status)
	asNeeded["regenerate_reminders"] = true
	accepted := doRequest(t, http.MethodPatch, "/medications/"+created.ID+"?tz=UTC", asNeeded, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, accepted.status, "%s", accepted.body)
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
	medications = listMedications(t, tokens.AccessToken, petID)
	require.Nil(t, medications[0].ReminderPlanID)
}

// interval_days/weekdays вне допустимого диапазона не проверяются здесь:
// диапазон задан прямо в спеке (minimum/maximum) и покрыт unit-тестами
// (TestCreateMedicationHandler_EveryNDaysIntervalOutOfRange и др.).
func TestMedication_EmptyNameRejected(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	resp := doRequest(t, http.MethodPost, "/pet/"+petID+"/medications?tz=UTC", map[string]any{
		"name":           "",
		"dosage":         "1 tablet",
		"frequency_type": "daily",
		"times":          []map[string]any{{"time": "08:00"}},
		"start_date":     "2024-01-01",
	}, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, resp.status)
}

func TestMedication_FrequencyFieldConsistencyRejected(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	// weekdays недопустим при frequency_type=daily.
	resp := doRequest(t, http.MethodPost, "/pet/"+petID+"/medications?tz=UTC", map[string]any{
		"name":           "Amoxicillin",
		"dosage":         "1 tablet",
		"frequency_type": "daily",
		"weekdays":       []int{1, 2},
		"times":          []map[string]any{{"time": "08:00"}},
		"start_date":     "2024-01-01",
	}, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, resp.status)
}

func TestMedication_OwnershipEnforcedOnReminders(t *testing.T) {
	resetDB(t)
	owner := registerUser(t, uniqueLogin(t), "correct-password")
	stranger := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, owner.AccessToken, "Барсик")

	createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/medications?tz=UTC", map[string]any{
		"name":           "Amoxicillin",
		"dosage":         "1 tablet",
		"frequency_type": "daily",
		"times":          []map[string]any{{"time": "08:00"}},
		"start_date":     futureDate(1),
		"end_date":       futureDate(3),
	}, owner.AccessToken)
	require.Equal(t, http.StatusCreated, createResp.status)
	var created idResponse
	createResp.decode(t, &created)

	resp := doRequest(t, http.MethodPost, "/medications/"+created.ID+"/reminders?tz=UTC", nil, stranger.AccessToken)
	require.Equal(t, http.StatusNotFound, resp.status)

	del := doRequest(t, http.MethodDelete, "/medications/"+created.ID+"/reminders", nil, stranger.AccessToken)
	require.Equal(t, http.StatusNotFound, del.status)
}

// PetBodyCondition проверяет паритет body_condition в существующих
// POST/PUT /pet эндпоинтах (см. корневой CLAUDE.md, "Ведпаспорт — Backend").
func TestPet_BodyConditionRoundTrips(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	createResp := doRequest(t, http.MethodPost, "/pet", map[string]any{
		"name":           "Барсик",
		"species":        "cat",
		"body_condition": "normal",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, createResp.status, "%s", createResp.body)
	var pet struct {
		ID            string  `json:"id"`
		BodyCondition *string `json:"body_condition"`
	}
	createResp.decode(t, &pet)
	require.NotNil(t, pet.BodyCondition)
	require.Equal(t, "normal", *pet.BodyCondition)

	update := doRequest(t, http.MethodPut, "/pet/"+pet.ID, map[string]any{
		"body_condition": "overweight",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, update.status, "%s", update.body)

	get := doRequest(t, http.MethodGet, "/pet/"+pet.ID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, get.status)
	get.decode(t, &pet)
	require.NotNil(t, pet.BodyCondition)
	require.Equal(t, "overweight", *pet.BodyCondition)
}

// Idempotency-Key на создании сущностей ветпаспорта: повтор с тем же ключом
// возвращает id ранее созданной записи и не создаёт дубликат (ни самой
// записи, ни связанных фактов и напоминаний); тот же ключ у другого питомца —
// это другая запись (уникальность на пару pet_id + ключ).
func TestVetPassport_IdempotencyKeyDeduplicatesCreate(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	otherPetID := createPet(t, tokens.AccessToken, "Мурка")

	cases := []struct {
		resource string
		query    string
		body     map[string]any
	}{
		{"vaccinations", "?tz=UTC", map[string]any{"name": "Rabies", "administered_date": "2024-01-01", "administered_time": "09:00"}},
		{"diseases", "", map[string]any{"name": "Otitis", "diagnosed_date": "2024-01-01", "status": "active"}},
		{"vet-visits", "", map[string]any{"visit_date": "2024-01-01", "reason": "Checkup"}},
		{"allergies", "", map[string]any{"allergen": "Chicken", "severity": "mild"}},
		{"medications", "?tz=UTC", map[string]any{"name": "Drug", "dosage": "1 tab", "frequency_type": "as_needed"}},
	}

	for _, c := range cases {
		t.Run(c.resource, func(t *testing.T) {
			headers := map[string]string{"Idempotency-Key": "6f1c2b9e-6c57-4c3b-9a55-0d6f6f1f2a10"}
			path := "/pet/" + petID + "/" + c.resource

			first := doRequest(t, http.MethodPost, path+c.query, c.body, tokens.AccessToken, headers)
			require.Equalf(t, http.StatusCreated, first.status, "%s", first.body)
			var firstID idResponse
			first.decode(t, &firstID)

			replay := doRequest(t, http.MethodPost, path+c.query, c.body, tokens.AccessToken, headers)
			require.Equalf(t, http.StatusCreated, replay.status, "%s", replay.body)
			var replayID idResponse
			replay.decode(t, &replayID)
			require.Equal(t, firstID.ID, replayID.ID)

			listPath := path
			if c.query != "" && c.resource == "medications" {
				listPath += c.query
			}
			list := doRequest(t, http.MethodGet, listPath, nil, tokens.AccessToken)
			var listBody struct {
				Items []idResponse `json:"items"`
			}
			list.decode(t, &listBody)
			require.Len(t, listBody.Items, 1, "повтор с тем же ключом не должен создавать дубликат")

			other := doRequest(t, http.MethodPost, "/pet/"+otherPetID+"/"+c.resource+c.query, c.body, tokens.AccessToken, headers)
			require.Equalf(t, http.StatusCreated, other.status, "%s", other.body)
			var otherID idResponse
			other.decode(t, &otherID)
			require.NotEqual(t, firstID.ID, otherID.ID)

			// Удаление не освобождает ключ: повтор возвращает прежний id.
			del := doRequest(t, http.MethodDelete, "/"+c.resource+"/"+firstID.ID, nil, tokens.AccessToken)
			require.Equalf(t, http.StatusNoContent, del.status, "%s", del.body)
			afterDelete := doRequest(t, http.MethodPost, path+c.query, c.body, tokens.AccessToken, headers)
			require.Equalf(t, http.StatusCreated, afterDelete.status, "%s", afterDelete.body)
			var afterDeleteID idResponse
			afterDelete.decode(t, &afterDeleteID)
			require.Equal(t, firstID.ID, afterDeleteID.ID)
		})
	}

	// Повтор создания прививки не размножил связанные факты: у питомца было
	// создано ровно одно событие (и оно удалено вместе с прививкой).
	require.Empty(t, listPetEvents(t, tokens.AccessToken, petID))
}

type petEventItem struct {
	ID    string `json:"id"`
	Date  string `json:"date"`
	Type  string `json:"type"`
	Value struct {
		Label string `json:"label"`
	} `json:"value"`
}

func listPetEvents(t *testing.T, token, petID string) []petEventItem {
	t.Helper()
	resp := doRequest(t, http.MethodGet, "/pet/"+petID+"/events", nil, token)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	var body struct {
		Items []petEventItem `json:"items"`
	}
	resp.decode(t, &body)
	return body.Items
}

type medicationBody struct {
	ID             string  `json:"id"`
	ReminderPlanID *string `json:"reminder_plan_id"`
	NextDose       *string `json:"next_dose"`
	EndDate        *string `json:"end_date"`
}

func listMedications(t *testing.T, token, petID string) []medicationBody {
	t.Helper()
	resp := doRequest(t, http.MethodGet, "/pet/"+petID+"/medications?tz=UTC", nil, token)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	var body struct {
		Items []medicationBody `json:"items"`
	}
	resp.decode(t, &body)
	return body.Items
}

// Связанный факт прививки: подпись с префиксом, PATCH обновляет его на месте
// (а не создаёт новый), удаление прививки удаляет его.
// Напоминание на следующую дату — настройки (не событие): включается и
// выключается флагом, очищается вместе с next_date.
func TestVaccination_LinkedRecordsLifecycle(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/vaccinations?tz=UTC", map[string]any{
		"name":              "Rabies",
		"administered_date": "2024-01-01",
		"administered_time": "14:45",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, createResp.status, "%s", createResp.body)
	var created idResponse
	createResp.decode(t, &created)

	events := listPetEvents(t, tokens.AccessToken, petID)
	require.Len(t, events, 1)
	eventID := events[0].ID
	require.Equal(t, "other", events[0].Type)
	require.Equal(t, "Вакцинация: Rabies", events[0].Value.Label)
	require.Equal(t, "2024-01-01T14:45:00Z", events[0].Date)

	// Повторный PATCH: тот же факт, новая дата, прежнее время
	// суток, локализованная клиентом подпись.
	patch := doRequest(t, http.MethodPatch, "/vaccinations/"+created.ID+"?tz=UTC", map[string]any{
		"name":              "Rabies v2",
		"administered_date": "2024-02-10",
		"event_label":       "Vaccination: Rabies v2",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, patch.status, "%s", patch.body)

	events = listPetEvents(t, tokens.AccessToken, petID)
	require.Len(t, events, 1, "PATCH не должен создавать второй факт")
	require.Equal(t, eventID, events[0].ID)
	require.Equal(t, "Vaccination: Rabies v2", events[0].Value.Label)
	require.Equal(t, "2024-02-10T14:45:00Z", events[0].Date)

	// Перенос факта в будущее — 400, факт не меняется.
	futureMove := doRequest(t, http.MethodPatch, "/vaccinations/"+created.ID+"?tz=UTC", map[string]any{
		"administered_date": futureDate(5),
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, futureMove.status, "%s", futureMove.body)

	// Включение напоминания на next_date создаёт настройки, а не событие.
	nextDate := futureDate(40)
	var linked struct {
		AdministeredEventID *string `json:"administered_event_id"`
		NextPlanID          *string `json:"next_plan_id"`
	}
	patch = doRequest(t, http.MethodPatch, "/vaccinations/"+created.ID+"?tz=UTC", map[string]any{
		"next_date":            nextDate,
		"add_reminder_on_next": true,
		"next_time":            "10:00",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, patch.status, "%s", patch.body)
	patch.decode(t, &linked)
	require.NotNil(t, linked.NextPlanID)
	require.Len(t, listPetEvents(t, tokens.AccessToken, petID), 1)
	plan := getReminderPlan(t, tokens.AccessToken, *linked.NextPlanID)
	require.Len(t, plan.Reminders, 1)
	require.Equal(t, nextDate+"T10:00:00Z", plan.Reminders[0].RemindAt)

	// Перенос next_date переносит напоминание на месте (то же время суток).
	moved := futureDate(50)
	patch = doRequest(t, http.MethodPatch, "/vaccinations/"+created.ID+"?tz=UTC", map[string]any{
		"next_date": moved,
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, patch.status, "%s", patch.body)
	plan = getReminderPlan(t, tokens.AccessToken, *linked.NextPlanID)
	require.Len(t, plan.Reminders, 1)
	require.Equal(t, moved+"T10:00:00Z", plan.Reminders[0].RemindAt)

	// next_date: null очищает дату и жёстко удаляет напоминание на неё.
	patch = doRequest(t, http.MethodPatch, "/vaccinations/"+created.ID+"?tz=UTC", map[string]any{
		"next_date": nil,
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, patch.status, "%s", patch.body)
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))

	list := doRequest(t, http.MethodGet, "/pet/"+petID+"/vaccinations", nil, tokens.AccessToken)
	var listBody struct {
		Items []struct {
			NextDate            *string `json:"next_date"`
			AdministeredEventID *string `json:"administered_event_id"`
			NextPlanID          *string `json:"next_plan_id"`
		} `json:"items"`
	}
	list.decode(t, &listBody)
	require.Len(t, listBody.Items, 1)
	require.Nil(t, listBody.Items[0].NextDate)
	require.Nil(t, listBody.Items[0].NextPlanID)
	require.NotNil(t, listBody.Items[0].AdministeredEventID)
	require.Equal(t, eventID, *listBody.Items[0].AdministeredEventID)

	// Факт удалён из календаря — PATCH с administered_time создаёт его заново.
	del := doRequest(t, http.MethodDelete, "/events/"+eventID, nil, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, del.status, "%s", del.body)
	patch = doRequest(t, http.MethodPatch, "/vaccinations/"+created.ID+"?tz=UTC", map[string]any{
		"administered_time": "08:00",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, patch.status, "%s", patch.body)
	events = listPetEvents(t, tokens.AccessToken, petID)
	require.Len(t, events, 1)
	require.NotEqual(t, eventID, events[0].ID)

	// Удаление прививки удаляет связанный факт.
	del = doRequest(t, http.MethodDelete, "/vaccinations/"+created.ID, nil, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, del.status, "%s", del.body)
	require.Empty(t, listPetEvents(t, tokens.AccessToken, petID))
}

// administered_time/next_time прививки и times курса лекарств — местное время пояса tz:
// факт и напоминания создаются/переносятся на соответствующий момент, а не на
// то же время суток в UTC.
func TestVetPassport_LinkedRecordsUseClientTimeZone(t *testing.T) {
	cases := []struct {
		tz               string
		vaccinationFact  string
		vaccinationMoved string
		medicationOffset string
	}{
		{"Europe/Moscow", "2024-01-01T06:30:00Z", "2024-02-10T06:30:00Z", "05:00:00Z"},
		{"America/Bogota", "2024-01-01T14:30:00Z", "2024-02-10T14:30:00Z", "13:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.tz, func(t *testing.T) {
			resetDB(t)
			tokens := registerUser(t, uniqueLogin(t), "correct-password")
			petID := createPet(t, tokens.AccessToken, "Барсик")

			createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/vaccinations?tz="+tc.tz, map[string]any{
				"name":              "Rabies",
				"administered_date": "2024-01-01",
				"administered_time": "09:30",
			}, tokens.AccessToken)
			require.Equalf(t, http.StatusCreated, createResp.status, "%s", createResp.body)
			var vaccination idResponse
			createResp.decode(t, &vaccination)

			events := listPetEvents(t, tokens.AccessToken, petID)
			require.Len(t, events, 1)
			require.Equal(t, tc.vaccinationFact, events[0].Date)

			// Перенос даты без administered_time сохраняет местное время суток.
			patch := doRequest(t, http.MethodPatch, "/vaccinations/"+vaccination.ID+"?tz="+tc.tz, map[string]any{
				"administered_date": "2024-02-10",
			}, tokens.AccessToken)
			require.Equalf(t, http.StatusOK, patch.status, "%s", patch.body)
			events = listPetEvents(t, tokens.AccessToken, petID)
			require.Len(t, events, 1)
			require.Equal(t, tc.vaccinationMoved, events[0].Date)

			del := doRequest(t, http.MethodDelete, "/vaccinations/"+vaccination.ID, nil, tokens.AccessToken)
			require.Equalf(t, http.StatusNoContent, del.status, "%s", del.body)

			day := futureDate(2)
			medResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/medications?tz="+tc.tz, map[string]any{
				"name":           "Amoxicillin",
				"dosage":         "1 tablet",
				"frequency_type": "daily",
				"times":          []map[string]any{{"time": "08:00"}},
				"start_date":     day,
				"end_date":       day,
				"add_reminders":  true,
			}, tokens.AccessToken)
			require.Equalf(t, http.StatusCreated, medResp.status, "%s", medResp.body)

			medications := listMedications(t, tokens.AccessToken, petID)
			require.Len(t, medications, 1)
			require.NotNil(t, medications[0].ReminderPlanID)
			plan := getReminderPlan(t, tokens.AccessToken, *medications[0].ReminderPlanID)
			require.Len(t, plan.Reminders, 1)
			require.Equal(t, day+"T"+tc.medicationOffset, plan.Reminders[0].RemindAt)

			list := doRequest(t, http.MethodGet, "/pet/"+petID+"/medications?tz="+tc.tz, nil, tokens.AccessToken)
			require.Equalf(t, http.StatusOK, list.status, "%s", list.body)
		})
	}

	t.Run("неизвестный пояс", func(t *testing.T) {
		resetDB(t)
		tokens := registerUser(t, uniqueLogin(t), "correct-password")
		petID := createPet(t, tokens.AccessToken, "Барсик")
		resp := doRequest(t, http.MethodPost, "/pet/"+petID+"/vaccinations?tz=Nowhere/City", map[string]any{
			"name":              "Rabies",
			"administered_date": "2024-01-01",
		}, tokens.AccessToken)
		require.Equal(t, http.StatusBadRequest, resp.status)
	})
}
