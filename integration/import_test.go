//go:build integration

package integration

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestImportLocalData_HappyPath(t *testing.T) {
	resetDB(t)

	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	idempotencyKey := uuid.NewString()
	body := map[string]any{
		"profile": map[string]any{
			"first_name": "Иван",
			"email":      "ivan@example.com",
		},
		"pets": []map[string]any{
			{"local_id": "local-cat", "name": "Барсик", "species": "cat"},
			{"local_id": "local-dog", "name": "Рекс", "species": "dog"},
		},
		"events": []map[string]any{
			{
				"local_id":     "local-event-1",
				"pet_local_id": "local-cat",
				"date":         time.Now().UTC().Format(time.RFC3339),
				"type":         "weight",
				"value":        map[string]any{"amount": 4.2},
			},
		},
	}

	resp := doRequest(t, http.MethodPost, "/import/local-data", body, tokens.AccessToken,
		map[string]string{"Idempotency-Key": idempotencyKey})
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)

	var result struct {
		PetsImported    int  `json:"pets_imported"`
		EventsImported  int  `json:"events_imported"`
		ProfileImported bool `json:"profile_imported"`
		Pets            []struct {
			LocalID string `json:"local_id"`
			ID      string `json:"id"`
		} `json:"pets"`
		Events []struct {
			LocalID string `json:"local_id"`
			ID      string `json:"id"`
		} `json:"events"`
	}
	resp.decode(t, &result)
	require.Equal(t, 2, result.PetsImported)
	require.Equal(t, 1, result.EventsImported)
	require.True(t, result.ProfileImported)

	// Поле events сопоставляет local_id клиента с новым серверным id,
	// симметрично pets (см. "Импорт локальных данных — Backend", раздел 3).
	require.Len(t, result.Events, 1)
	require.Equal(t, "local-event-1", result.Events[0].LocalID)
	require.NotEmpty(t, result.Events[0].ID)
	require.Len(t, result.Pets, 2)

	// Перенесённые данные действительно доступны через обычные эндпоинты, с
	// новыми серверными id (local_id нигде не сохраняется/не возвращается).
	list := doRequest(t, http.MethodGet, "/pet", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, list.status)
	var listBody struct {
		Items []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	list.decode(t, &listBody)
	require.Len(t, listBody.Items, 2)
	for _, item := range listBody.Items {
		require.NotEqual(t, "local-cat", item.ID)
		require.NotEqual(t, "local-dog", item.ID)
	}

	profileResp := doRequest(t, http.MethodGet, "/profile", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, profileResp.status)
	var profileBody struct {
		FirstName string `json:"first_name"`
	}
	profileResp.decode(t, &profileBody)
	require.Equal(t, "Иван", profileBody.FirstName)
}

// TestImportLocalData_MedicationWithEventLocalIDs проверяет перенос
// medications[] с новой моделью частот (frequency_type/weekdays/
// interval_days/times/start_date/end_date) и связывание event_local_ids с
// уже перенесёнными events[] без пересчёта расписания на сервере — см.
// "Импорт локальных данных — Backend", шаг 9.
func TestImportLocalData_MedicationWithEventLocalIDs(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	idempotencyKey := uuid.NewString()
	body := map[string]any{
		"pets": []map[string]any{
			{"local_id": "local-cat", "name": "Барсик", "species": "cat"},
		},
		"events": []map[string]any{
			{
				"local_id":     "local-event-1",
				"pet_local_id": "local-cat",
				"date":         "2024-01-01T08:00:00Z",
				"type":         "medication",
				"value":        map[string]any{"name": "Amoxicillin"},
			},
		},
		"medications": []map[string]any{
			{
				"local_id":        "local-med-1",
				"pet_local_id":    "local-cat",
				"name":            "Amoxicillin",
				"dosage":          "1 tablet",
				"frequency_type":  "daily",
				"times":           []map[string]any{{"time": "08:00"}},
				"start_date":      "2024-01-01",
				"event_local_ids": []string{"local-event-1"},
			},
		},
	}

	resp := doRequest(t, http.MethodPost, "/import/local-data", body, tokens.AccessToken,
		map[string]string{"Idempotency-Key": idempotencyKey})
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)

	var result struct {
		MedicationsImported int `json:"medications_imported"`
		Medications         []struct {
			LocalID string `json:"local_id"`
			ID      string `json:"id"`
		} `json:"medications"`
		Events []struct {
			LocalID string `json:"local_id"`
			ID      string `json:"id"`
		} `json:"events"`
	}
	resp.decode(t, &result)
	require.Equal(t, 1, result.MedicationsImported)
	require.Len(t, result.Medications, 1)
	require.Equal(t, "local-med-1", result.Medications[0].LocalID)
	require.NotEmpty(t, result.Medications[0].ID)

	pets := doRequest(t, http.MethodGet, "/pet", nil, tokens.AccessToken)
	var petsBody struct {
		Items []struct{ ID string } `json:"items"`
	}
	pets.decode(t, &petsBody)
	require.Len(t, petsBody.Items, 1)

	medList := doRequest(t, http.MethodGet, "/pet/"+petsBody.Items[0].ID+"/medications", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, medList.status)
	var medListBody struct {
		Items []struct {
			FrequencyType string   `json:"frequency_type"`
			EventIDs      []string `json:"event_ids"`
		} `json:"items"`
	}
	medList.decode(t, &medListBody)
	require.Len(t, medListBody.Items, 1)
	require.Equal(t, "daily", medListBody.Items[0].FrequencyType)
	require.Equal(t, []string{result.Events[0].ID}, medListBody.Items[0].EventIDs)
}

func TestImportLocalData_WithoutProfile(t *testing.T) {
	resetDB(t)

	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	idempotencyKey := uuid.NewString()
	body := map[string]any{
		"pets":   []map[string]any{{"local_id": "local-cat", "name": "Барсик", "species": "cat"}},
		"events": []map[string]any{},
	}

	resp := doRequest(t, http.MethodPost, "/import/local-data", body, tokens.AccessToken,
		map[string]string{"Idempotency-Key": idempotencyKey})
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)

	var result struct {
		PetsImported    int  `json:"pets_imported"`
		EventsImported  int  `json:"events_imported"`
		ProfileImported bool `json:"profile_imported"`
	}
	resp.decode(t, &result)
	require.Equal(t, 1, result.PetsImported)
	require.Equal(t, 0, result.EventsImported)
	require.False(t, result.ProfileImported)

	// GET /profile по-прежнему 404 — перенос без profile не создаёт запись.
	profileResp := doRequest(t, http.MethodGet, "/profile", nil, tokens.AccessToken)
	require.Equal(t, http.StatusNotFound, profileResp.status)
}

func TestImportLocalData_IdempotencyKeyReplayDoesNotDuplicate(t *testing.T) {
	resetDB(t)

	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	idempotencyKey := uuid.NewString()
	body := map[string]any{
		"pets":   []map[string]any{{"local_id": "local-cat", "name": "Барсик", "species": "cat"}},
		"events": []map[string]any{},
	}

	first := doRequest(t, http.MethodPost, "/import/local-data", body, tokens.AccessToken,
		map[string]string{"Idempotency-Key": idempotencyKey})
	require.Equalf(t, http.StatusOK, first.status, "%s", first.body)

	// Повторный запрос с тем же ключом, но другим (валидным) телом — должен
	// вернуть ранее сохранённый результат и не создать новых строк.
	secondBody := map[string]any{
		"pets":   []map[string]any{{"local_id": "local-other", "name": "Другой", "species": "dog"}},
		"events": []map[string]any{},
	}
	second := doRequest(t, http.MethodPost, "/import/local-data", secondBody, tokens.AccessToken,
		map[string]string{"Idempotency-Key": idempotencyKey})
	require.Equalf(t, http.StatusOK, second.status, "%s", second.body)
	require.JSONEq(t, string(first.body), string(second.body))

	list := doRequest(t, http.MethodGet, "/pet", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, list.status)
	var listBody struct {
		Items []struct{ ID string } `json:"items"`
	}
	list.decode(t, &listBody)
	require.Len(t, listBody.Items, 1)
}

// Отсутствие обязательного заголовка Idempotency-Key покрыто unit-тестом
// TestImportLocalDataHandler_MissingIdempotencyKey (handlers/import_test.go),
// а не здесь: doRequest сверяет каждый запрос с open-api/spec.json, где этот
// заголовок объявлен обязательным параметром — намеренно невалидный по
// спеке запрос сломал бы саму проверку гармонизации запроса со спекой,
// а не только проверял бы обработку сервером.

func TestImportLocalData_InvalidPetRejectedAndNothingPersisted(t *testing.T) {
	resetDB(t)

	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	idempotencyKey := uuid.NewString()
	body := map[string]any{
		"pets":   []map[string]any{{"local_id": "local-cat", "name": "", "species": "cat"}},
		"events": []map[string]any{},
	}
	resp := doRequest(t, http.MethodPost, "/import/local-data", body, tokens.AccessToken,
		map[string]string{"Idempotency-Key": idempotencyKey})
	require.Equal(t, http.StatusBadRequest, resp.status)
	var errBody struct {
		Code string `json:"code"`
	}
	resp.decode(t, &errBody)
	require.Equal(t, "VALIDATION_ERROR", errBody.Code)

	list := doRequest(t, http.MethodGet, "/pet", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, list.status)
	var listBody struct {
		Items []struct{ ID string } `json:"items"`
	}
	list.decode(t, &listBody)
	require.Empty(t, listBody.Items)
}

func TestImportLocalData_EventPetLocalIDMismatchRejected(t *testing.T) {
	resetDB(t)

	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	idempotencyKey := uuid.NewString()
	body := map[string]any{
		"pets": []map[string]any{{"local_id": "local-cat", "name": "Барсик", "species": "cat"}},
		"events": []map[string]any{
			{
				"local_id":     "local-event-1",
				"pet_local_id": "does-not-exist",
				"date":         time.Now().UTC().Format(time.RFC3339),
				"type":         "weight",
				"value":        map[string]any{"amount": 4.2},
			},
		},
	}
	resp := doRequest(t, http.MethodPost, "/import/local-data", body, tokens.AccessToken,
		map[string]string{"Idempotency-Key": idempotencyKey})
	require.Equal(t, http.StatusBadRequest, resp.status)

	list := doRequest(t, http.MethodGet, "/pet", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, list.status)
	var listBody struct {
		Items []struct{ ID string } `json:"items"`
	}
	list.decode(t, &listBody)
	require.Empty(t, listBody.Items)
}

// Дублирующийся events[].local_id делает отображение local_id -> id
// неоднозначным для поля events ответа — запрос должен быть отклонён целиком
// (см. "Импорт локальных данных — Backend", раздел 4).
func TestImportLocalData_DuplicateEventLocalIDRejected(t *testing.T) {
	resetDB(t)

	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	idempotencyKey := uuid.NewString()
	body := map[string]any{
		"pets": []map[string]any{{"local_id": "local-cat", "name": "Барсик", "species": "cat"}},
		"events": []map[string]any{
			{
				"local_id":     "dup-event",
				"pet_local_id": "local-cat",
				"date":         time.Now().UTC().Format(time.RFC3339),
				"type":         "weight",
				"value":        map[string]any{"amount": 4.2},
			},
			{
				"local_id":     "dup-event",
				"pet_local_id": "local-cat",
				"date":         time.Now().UTC().Format(time.RFC3339),
				"type":         "weight",
				"value":        map[string]any{"amount": 5.0},
			},
		},
	}
	resp := doRequest(t, http.MethodPost, "/import/local-data", body, tokens.AccessToken,
		map[string]string{"Idempotency-Key": idempotencyKey})
	require.Equal(t, http.StatusBadRequest, resp.status)

	list := doRequest(t, http.MethodGet, "/pet", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, list.status)
	var listBody struct {
		Items []struct{ ID string } `json:"items"`
	}
	list.decode(t, &listBody)
	require.Empty(t, listBody.Items)
}

func TestImportLocalData_Unauthorized(t *testing.T) {
	resetDB(t)

	body := map[string]any{"pets": []map[string]any{}, "events": []map[string]any{}}
	resp := doRequest(t, http.MethodPost, "/import/local-data", body, "",
		map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equal(t, http.StatusUnauthorized, resp.status)
}
