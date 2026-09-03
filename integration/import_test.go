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
	}
	resp.decode(t, &result)
	require.Equal(t, 2, result.PetsImported)
	require.Equal(t, 1, result.EventsImported)
	require.True(t, result.ProfileImported)

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

func TestImportLocalData_Unauthorized(t *testing.T) {
	resetDB(t)

	body := map[string]any{"pets": []map[string]any{}, "events": []map[string]any{}}
	resp := doRequest(t, http.MethodPost, "/import/local-data", body, "",
		map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equal(t, http.StatusUnauthorized, resp.status)
}
