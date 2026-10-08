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

	createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/vaccinations", map[string]any{
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

	patch := doRequest(t, http.MethodPatch, "/vaccinations/"+created.ID, map[string]any{
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
	createOnForeignPet := doRequest(t, http.MethodPost, "/pet/"+petID+"/vaccinations", map[string]any{
		"name":              "Rabies",
		"administered_date": "2024-01-01",
	}, stranger.AccessToken)
	require.Equal(t, http.StatusNotFound, createOnForeignPet.status)

	createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/vaccinations", map[string]any{
		"name":              "Rabies",
		"administered_date": "2024-01-01",
	}, owner.AccessToken)
	require.Equal(t, http.StatusCreated, createResp.status)
	var created idResponse
	createResp.decode(t, &created)

	// Чужая сущность -> 404 при PATCH/DELETE.
	patch := doRequest(t, http.MethodPatch, "/vaccinations/"+created.ID, map[string]any{"name": "Hacked"}, stranger.AccessToken)
	require.Equal(t, http.StatusNotFound, patch.status)
	del := doRequest(t, http.MethodDelete, "/vaccinations/"+created.ID, nil, stranger.AccessToken)
	require.Equal(t, http.StatusNotFound, del.status)
}

func TestVaccination_ValidationErrors(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	resp := doRequest(t, http.MethodPost, "/pet/"+petID+"/vaccinations", map[string]any{
		"name":              "",
		"administered_date": "2024-01-01",
	}, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, resp.status)
}

func TestVaccination_AutoCreatesLinkedEvents(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/vaccinations", map[string]any{
		"name":                      "Rabies",
		"administered_date":         "2024-01-01",
		"next_date":                 "2025-01-01",
		"add_event_on_administered": true,
		"add_event_on_next":         true,
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, createResp.status, "%s", createResp.body)
	var created idResponse
	createResp.decode(t, &created)

	list := doRequest(t, http.MethodGet, "/pet/"+petID+"/vaccinations", nil, tokens.AccessToken)
	var listBody struct {
		Items []struct {
			AdministeredEventID *string `json:"administered_event_id"`
			NextEventID         *string `json:"next_event_id"`
		} `json:"items"`
	}
	list.decode(t, &listBody)
	require.Len(t, listBody.Items, 1)
	require.NotNil(t, listBody.Items[0].AdministeredEventID)
	require.NotNil(t, listBody.Items[0].NextEventID)

	events := doRequest(t, http.MethodGet, "/pet/"+petID+"/events", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, events.status)
	var eventsBody struct {
		Items []struct{ ID string } `json:"items"`
	}
	events.decode(t, &eventsBody)
	require.Len(t, eventsBody.Items, 2)
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

func TestMedication_CRUDAndEventsLifecycle(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/medications", map[string]any{
		"name":           "Amoxicillin",
		"dosage":         "1 tablet",
		"frequency_type": "daily",
		"times":          []map[string]any{{"time": "08:00"}},
		"start_date":     "2024-01-01",
		"end_date":       "2024-01-03",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, createResp.status, "%s", createResp.body)
	var created idResponse
	createResp.decode(t, &created)

	// POST /medications/{id}/events создаёт расписание приёма: 3 дня x 1
	// время/день = 3 события.
	eventsResp := doRequest(t, http.MethodPost, "/medications/"+created.ID+"/events", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, eventsResp.status, "%s", eventsResp.body)
	var medication struct {
		EventIDs []string `json:"event_ids"`
	}
	eventsResp.decode(t, &medication)
	require.Len(t, medication.EventIDs, 3)

	// Повторный вызов, пока event_ids не пуст, — конфликт.
	conflictResp := doRequest(t, http.MethodPost, "/medications/"+created.ID+"/events", nil, tokens.AccessToken)
	require.Equal(t, http.StatusConflict, conflictResp.status)

	petEvents := doRequest(t, http.MethodGet, "/pet/"+petID+"/events", nil, tokens.AccessToken)
	var petEventsBody struct {
		Items []struct{ ID string } `json:"items"`
	}
	petEvents.decode(t, &petEventsBody)
	require.Len(t, petEventsBody.Items, 3)

	// DELETE /medications/{id}/events физически удаляет события (в отличие
	// от soft-delete везде остальным в проекте) — они пропадают из GET
	// /pet/{id}/events.
	deleteEvents := doRequest(t, http.MethodDelete, "/medications/"+created.ID+"/events", nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, deleteEvents.status)

	// Повторное удаление, когда event_ids уже пуст, — 404.
	deleteAgain := doRequest(t, http.MethodDelete, "/medications/"+created.ID+"/events", nil, tokens.AccessToken)
	require.Equal(t, http.StatusNotFound, deleteAgain.status)

	petEventsAfter := doRequest(t, http.MethodGet, "/pet/"+petID+"/events", nil, tokens.AccessToken)
	petEventsAfter.decode(t, &petEventsBody)
	require.Empty(t, petEventsBody.Items)

	list := doRequest(t, http.MethodGet, "/pet/"+petID+"/medications", nil, tokens.AccessToken)
	var listBody struct {
		Items []struct {
			EventIDs []string `json:"event_ids"`
		} `json:"items"`
	}
	list.decode(t, &listBody)
	require.Len(t, listBody.Items, 1)
	require.Empty(t, listBody.Items[0].EventIDs)

	del := doRequest(t, http.MethodDelete, "/medications/"+created.ID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, del.status)
}

func TestMedication_AsNeeded_NoSchedule(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/medications", map[string]any{
		"name":           "Painkiller",
		"dosage":         "1 tablet",
		"frequency_type": "as_needed",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, createResp.status, "%s", createResp.body)
	var created idResponse
	createResp.decode(t, &created)

	// Нет расписания у as_needed — 400.
	eventsResp := doRequest(t, http.MethodPost, "/medications/"+created.ID+"/events", nil, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, eventsResp.status)

	list := doRequest(t, http.MethodGet, "/pet/"+petID+"/medications", nil, tokens.AccessToken)
	var listBody struct {
		Items []struct {
			NextDose *string `json:"next_dose"`
		} `json:"items"`
	}
	list.decode(t, &listBody)
	require.Len(t, listBody.Items, 1)
	require.Nil(t, listBody.Items[0].NextDose)
}

func TestMedication_PatchRegenerateEvents(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/medications", map[string]any{
		"name":           "Amoxicillin",
		"dosage":         "1 tablet",
		"frequency_type": "daily",
		"times":          []map[string]any{{"time": "08:00"}},
		"start_date":     "2024-01-01",
		"end_date":       "2024-01-02",
		"add_event":      true,
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, createResp.status, "%s", createResp.body)
	var created idResponse
	createResp.decode(t, &created)

	// regenerate_events=false (по умолчанию) — поля расписания меняются, но
	// набор событий остаётся прежним.
	patchNoRegen := doRequest(t, http.MethodPatch, "/medications/"+created.ID, map[string]any{
		"end_date": "2024-01-05",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, patchNoRegen.status, "%s", patchNoRegen.body)

	list := doRequest(t, http.MethodGet, "/pet/"+petID+"/medications", nil, tokens.AccessToken)
	var listBody struct {
		Items []struct {
			EventIDs []string `json:"event_ids"`
			EndDate  *string  `json:"end_date"`
		} `json:"items"`
	}
	list.decode(t, &listBody)
	require.Len(t, listBody.Items, 1)
	require.Len(t, listBody.Items[0].EventIDs, 2)
	require.Equal(t, "2024-01-05", *listBody.Items[0].EndDate)

	// regenerate_events=true — старые события жёстко удаляются, новые
	// создаются по обновлённому расписанию (4 дня x 1 время = 4 события).
	patchRegen := doRequest(t, http.MethodPatch, "/medications/"+created.ID, map[string]any{
		"end_date":          "2024-01-04",
		"regenerate_events": true,
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, patchRegen.status, "%s", patchRegen.body)

	list2 := doRequest(t, http.MethodGet, "/pet/"+petID+"/medications", nil, tokens.AccessToken)
	list2.decode(t, &listBody)
	require.Len(t, listBody.Items[0].EventIDs, 4)

	petEvents := doRequest(t, http.MethodGet, "/pet/"+petID+"/events", nil, tokens.AccessToken)
	var petEventsBody struct {
		Items []struct{ ID string } `json:"items"`
	}
	petEvents.decode(t, &petEventsBody)
	require.Len(t, petEventsBody.Items, 4)
}

// interval_days/weekdays вне допустимого диапазона не проверяются здесь:
// диапазон задан прямо в спеке (minimum/maximum) и покрыт unit-тестами
// (TestCreateMedicationHandler_EveryNDaysIntervalOutOfRange и др.).
func TestMedication_EmptyNameRejected(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	resp := doRequest(t, http.MethodPost, "/pet/"+petID+"/medications", map[string]any{
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
	resp := doRequest(t, http.MethodPost, "/pet/"+petID+"/medications", map[string]any{
		"name":           "Amoxicillin",
		"dosage":         "1 tablet",
		"frequency_type": "daily",
		"weekdays":       []int{1, 2},
		"times":          []map[string]any{{"time": "08:00"}},
		"start_date":     "2024-01-01",
	}, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, resp.status)
}

func TestMedication_OwnershipEnforcedOnEvents(t *testing.T) {
	resetDB(t)
	owner := registerUser(t, uniqueLogin(t), "correct-password")
	stranger := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, owner.AccessToken, "Барсик")

	createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/medications", map[string]any{
		"name":           "Amoxicillin",
		"dosage":         "1 tablet",
		"frequency_type": "daily",
		"times":          []map[string]any{{"time": "08:00"}},
		"start_date":     "2024-01-01",
		"end_date":       "2024-01-03",
	}, owner.AccessToken)
	require.Equal(t, http.StatusCreated, createResp.status)
	var created idResponse
	createResp.decode(t, &created)

	resp := doRequest(t, http.MethodPost, "/medications/"+created.ID+"/events", nil, stranger.AccessToken)
	require.Equal(t, http.StatusNotFound, resp.status)

	del := doRequest(t, http.MethodDelete, "/medications/"+created.ID+"/events", nil, stranger.AccessToken)
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
// записи, ни связанных событий); тот же ключ у другого питомца — это другая
// запись (уникальность на пару pet_id + ключ).
func TestVetPassport_IdempotencyKeyDeduplicatesCreate(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	otherPetID := createPet(t, tokens.AccessToken, "Мурка")

	cases := []struct {
		resource string
		body     map[string]any
	}{
		{"vaccinations", map[string]any{"name": "Rabies", "administered_date": "2024-01-01", "add_event_on_administered": true, "event_time": "09:00"}},
		{"diseases", map[string]any{"name": "Otitis", "diagnosed_date": "2024-01-01", "status": "active"}},
		{"vet-visits", map[string]any{"visit_date": "2024-01-01", "reason": "Checkup"}},
		{"allergies", map[string]any{"allergen": "Chicken", "severity": "mild"}},
		{"medications", map[string]any{"name": "Drug", "dosage": "1 tab", "frequency_type": "as_needed"}},
	}

	for _, c := range cases {
		t.Run(c.resource, func(t *testing.T) {
			headers := map[string]string{"Idempotency-Key": "6f1c2b9e-6c57-4c3b-9a55-0d6f6f1f2a10"}
			path := "/pet/" + petID + "/" + c.resource

			first := doRequest(t, http.MethodPost, path, c.body, tokens.AccessToken, headers)
			require.Equalf(t, http.StatusCreated, first.status, "%s", first.body)
			var firstID idResponse
			first.decode(t, &firstID)

			replay := doRequest(t, http.MethodPost, path, c.body, tokens.AccessToken, headers)
			require.Equalf(t, http.StatusCreated, replay.status, "%s", replay.body)
			var replayID idResponse
			replay.decode(t, &replayID)
			require.Equal(t, firstID.ID, replayID.ID)

			list := doRequest(t, http.MethodGet, path, nil, tokens.AccessToken)
			var listBody struct {
				Items []idResponse `json:"items"`
			}
			list.decode(t, &listBody)
			require.Len(t, listBody.Items, 1, "повтор с тем же ключом не должен создавать дубликат")

			other := doRequest(t, http.MethodPost, "/pet/"+otherPetID+"/"+c.resource, c.body, tokens.AccessToken, headers)
			require.Equalf(t, http.StatusCreated, other.status, "%s", other.body)
			var otherID idResponse
			other.decode(t, &otherID)
			require.NotEqual(t, firstID.ID, otherID.ID)

			// Удаление не освобождает ключ: повтор возвращает прежний id.
			del := doRequest(t, http.MethodDelete, "/"+c.resource+"/"+firstID.ID, nil, tokens.AccessToken)
			require.Equalf(t, http.StatusNoContent, del.status, "%s", del.body)
			afterDelete := doRequest(t, http.MethodPost, path, c.body, tokens.AccessToken, headers)
			require.Equalf(t, http.StatusCreated, afterDelete.status, "%s", afterDelete.body)
			var afterDeleteID idResponse
			afterDelete.decode(t, &afterDeleteID)
			require.Equal(t, firstID.ID, afterDeleteID.ID)
		})
	}

	// Повтор создания прививки не размножил связанные события: у питомца
	// было создано ровно одно (и оно удалено вместе с прививкой).
	events := doRequest(t, http.MethodGet, "/pet/"+petID+"/events", nil, tokens.AccessToken)
	var eventsBody struct {
		Items []struct{ ID string } `json:"items"`
	}
	events.decode(t, &eventsBody)
	require.Empty(t, eventsBody.Items)
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

// Связанное событие прививки: подпись с префиксом, PATCH обновляет его на
// месте (а не создаёт новое), снятие флага и удаление прививки удаляют его.
func TestVaccination_LinkedEventLifecycle(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/vaccinations", map[string]any{
		"name":                      "Rabies",
		"administered_date":         "2024-01-01",
		"add_event_on_administered": true,
		"event_time":                "14:45",
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

	// Повторный PATCH с тем же флагом: то же событие, новая дата, прежнее
	// время суток, локализованная клиентом подпись.
	patch := doRequest(t, http.MethodPatch, "/vaccinations/"+created.ID, map[string]any{
		"name":                      "Rabies v2",
		"administered_date":         "2024-02-10",
		"add_event_on_administered": true,
		"event_label":               "Vaccination: Rabies v2",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, patch.status, "%s", patch.body)

	events = listPetEvents(t, tokens.AccessToken, petID)
	require.Len(t, events, 1, "PATCH не должен создавать второе событие")
	require.Equal(t, eventID, events[0].ID)
	require.Equal(t, "Vaccination: Rabies v2", events[0].Value.Label)
	require.Equal(t, "2024-02-10T14:45:00Z", events[0].Date)

	// Включение напоминания на next_date создаёт второе событие.
	patch = doRequest(t, http.MethodPatch, "/vaccinations/"+created.ID, map[string]any{
		"next_date":         "2025-02-10",
		"add_event_on_next": true,
		"event_time":        "10:00",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, patch.status, "%s", patch.body)
	require.Len(t, listPetEvents(t, tokens.AccessToken, petID), 2)

	// next_date: null очищает дату и удаляет напоминание на неё.
	patch = doRequest(t, http.MethodPatch, "/vaccinations/"+created.ID, map[string]any{
		"next_date": nil,
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, patch.status, "%s", patch.body)
	events = listPetEvents(t, tokens.AccessToken, petID)
	require.Len(t, events, 1)
	require.Equal(t, eventID, events[0].ID)

	list := doRequest(t, http.MethodGet, "/pet/"+petID+"/vaccinations", nil, tokens.AccessToken)
	var listBody struct {
		Items []struct {
			NextDate            *string `json:"next_date"`
			AdministeredEventID *string `json:"administered_event_id"`
			NextEventID         *string `json:"next_event_id"`
		} `json:"items"`
	}
	list.decode(t, &listBody)
	require.Len(t, listBody.Items, 1)
	require.Nil(t, listBody.Items[0].NextDate)
	require.Nil(t, listBody.Items[0].NextEventID)
	require.NotNil(t, listBody.Items[0].AdministeredEventID)
	require.Equal(t, eventID, *listBody.Items[0].AdministeredEventID)

	// Событие удалено из календаря — флаг=true создаёт его заново.
	del := doRequest(t, http.MethodDelete, "/events/"+eventID, nil, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, del.status, "%s", del.body)
	patch = doRequest(t, http.MethodPatch, "/vaccinations/"+created.ID, map[string]any{
		"add_event_on_administered": true,
		"event_time":                "08:00",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, patch.status, "%s", patch.body)
	events = listPetEvents(t, tokens.AccessToken, petID)
	require.Len(t, events, 1)
	require.NotEqual(t, eventID, events[0].ID)

	// Удаление прививки удаляет связанное событие.
	del = doRequest(t, http.MethodDelete, "/vaccinations/"+created.ID, nil, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, del.status, "%s", del.body)
	require.Empty(t, listPetEvents(t, tokens.AccessToken, petID))
}

// event_time прививки и times курса лекарств — местное время пояса tz:
// связанные события создаются/переносятся на соответствующий момент, а не на
// то же время суток в UTC.
func TestVetPassport_LinkedEventsUseClientTimeZone(t *testing.T) {
	cases := []struct {
		tz                  string
		vaccinationCreated  string
		vaccinationMoved    string
		medicationFirstDose string
	}{
		{"Europe/Moscow", "2024-01-01T06:30:00Z", "2024-02-10T06:30:00Z", "2024-01-01T05:00:00Z"},
		{"America/Bogota", "2024-01-01T14:30:00Z", "2024-02-10T14:30:00Z", "2024-01-01T13:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.tz, func(t *testing.T) {
			resetDB(t)
			tokens := registerUser(t, uniqueLogin(t), "correct-password")
			petID := createPet(t, tokens.AccessToken, "Барсик")

			createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/vaccinations?tz="+tc.tz, map[string]any{
				"name":                      "Rabies",
				"administered_date":         "2024-01-01",
				"add_event_on_administered": true,
				"event_time":                "09:30",
			}, tokens.AccessToken)
			require.Equalf(t, http.StatusCreated, createResp.status, "%s", createResp.body)
			var vaccination idResponse
			createResp.decode(t, &vaccination)

			events := listPetEvents(t, tokens.AccessToken, petID)
			require.Len(t, events, 1)
			require.Equal(t, tc.vaccinationCreated, events[0].Date)

			// Перенос даты без event_time сохраняет местное время суток.
			patch := doRequest(t, http.MethodPatch, "/vaccinations/"+vaccination.ID+"?tz="+tc.tz, map[string]any{
				"administered_date": "2024-02-10",
			}, tokens.AccessToken)
			require.Equalf(t, http.StatusOK, patch.status, "%s", patch.body)
			events = listPetEvents(t, tokens.AccessToken, petID)
			require.Len(t, events, 1)
			require.Equal(t, tc.vaccinationMoved, events[0].Date)

			del := doRequest(t, http.MethodDelete, "/vaccinations/"+vaccination.ID, nil, tokens.AccessToken)
			require.Equalf(t, http.StatusNoContent, del.status, "%s", del.body)

			medResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/medications?tz="+tc.tz, map[string]any{
				"name":           "Amoxicillin",
				"dosage":         "1 tablet",
				"frequency_type": "daily",
				"times":          []map[string]any{{"time": "08:00"}},
				"start_date":     "2024-01-01",
				"end_date":       "2024-01-01",
				"add_event":      true,
			}, tokens.AccessToken)
			require.Equalf(t, http.StatusCreated, medResp.status, "%s", medResp.body)
			var medication idResponse
			medResp.decode(t, &medication)

			events = listPetEvents(t, tokens.AccessToken, petID)
			require.Len(t, events, 1)
			require.Equal(t, "medication", events[0].Type)
			require.Equal(t, tc.medicationFirstDose, events[0].Date)

			// Пересоздание набора событий вручную даёт тот же момент.
			delEvents := doRequest(t, http.MethodDelete, "/medications/"+medication.ID+"/events", nil, tokens.AccessToken)
			require.Equalf(t, http.StatusNoContent, delEvents.status, "%s", delEvents.body)
			addEvents := doRequest(t, http.MethodPost, "/medications/"+medication.ID+"/events?tz="+tc.tz, nil, tokens.AccessToken)
			require.Equalf(t, http.StatusOK, addEvents.status, "%s", addEvents.body)
			events = listPetEvents(t, tokens.AccessToken, petID)
			require.Len(t, events, 1)
			require.Equal(t, tc.medicationFirstDose, events[0].Date)

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
