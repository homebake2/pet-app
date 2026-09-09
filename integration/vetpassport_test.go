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
	require.Equalf(t, http.StatusNoContent, patch.status, "%s", patch.body)

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
		"name":             "Amoxicillin",
		"dosage":           "1 tablet",
		"periodicity_days": 1,
		"start_date":       "2024-01-01",
		"repeat_count":     3,
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, createResp.status, "%s", createResp.body)
	var created idResponse
	createResp.decode(t, &created)

	// POST /medications/{id}/events создаёт расписание приёма.
	eventsResp := doRequest(t, http.MethodPost, "/medications/"+created.ID+"/events", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, eventsResp.status, "%s", eventsResp.body)
	var medication struct {
		EventIDs []string `json:"event_ids"`
	}
	eventsResp.decode(t, &medication)
	require.Len(t, medication.EventIDs, 3)

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

// repeat_count вне диапазона 1-14 не проверяется здесь: диапазон задан прямо
// в спеке (minimum/maximum), запрос был бы отклонён проверкой гармонизации
// раньше хендлера — покрыто unit-тестом
// TestCreateMedicationHandler_InvalidRepeatCount.
func TestMedication_EmptyNameRejected(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	resp := doRequest(t, http.MethodPost, "/pet/"+petID+"/medications", map[string]any{
		"name":             "",
		"dosage":           "1 tablet",
		"periodicity_days": 1,
		"start_date":       "2024-01-01",
		"repeat_count":     3,
	}, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, resp.status)
}

func TestMedication_OwnershipEnforcedOnEvents(t *testing.T) {
	resetDB(t)
	owner := registerUser(t, uniqueLogin(t), "correct-password")
	stranger := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, owner.AccessToken, "Барсик")

	createResp := doRequest(t, http.MethodPost, "/pet/"+petID+"/medications", map[string]any{
		"name":             "Amoxicillin",
		"dosage":           "1 tablet",
		"periodicity_days": 1,
		"start_date":       "2024-01-01",
		"repeat_count":     3,
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
