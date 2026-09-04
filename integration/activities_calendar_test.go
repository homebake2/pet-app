//go:build integration

// Сквозные сценарии двух новых эндпоинтов экрана календаря — GET
// /activities/calendar (количество событий по дням) и GET /activities/day
// (все события всех питомцев пользователя за один день), см.
// artifacts/PET/pages/calendar/prosmotr-kalendarya-backend.md.
package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetActivitiesCalendar_CountsAcrossAllPetsNoGaps(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	createProfile(t, tokens.AccessToken, "Іван")

	catID := createPet(t, tokens.AccessToken, "Барсик")
	dogID := createPet(t, tokens.AccessToken, "Рекс")

	createEvent(t, tokens.AccessToken, catID, "2024-01-01T10:00:00Z", "weight", map[string]any{"amount": 4.2})
	createEvent(t, tokens.AccessToken, dogID, "2024-01-01T12:00:00Z", "weight", map[string]any{"amount": 12.0})
	createEvent(t, tokens.AccessToken, catID, "2024-01-03T10:00:00Z", "weight", map[string]any{"amount": 4.3})

	resp := doRequest(t, http.MethodGet, "/activities/calendar?from=2024-01-01&to=2024-01-03", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)

	var result struct {
		Items []struct {
			Date  string `json:"date"`
			Count int    `json:"count"`
		} `json:"items"`
	}
	resp.decode(t, &result)
	require.Len(t, result.Items, 3)
	require.Equal(t, "2024-01-01", result.Items[0].Date)
	require.Equal(t, 2, result.Items[0].Count)
	require.Equal(t, "2024-01-02", result.Items[1].Date)
	require.Equal(t, 0, result.Items[1].Count) // день без событий — не "дыра", а count: 0
	require.Equal(t, "2024-01-03", result.Items[2].Date)
	require.Equal(t, 1, result.Items[2].Count)
}

func TestGetActivitiesCalendar_NoPetsReturnsAllZeros(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")

	resp := doRequest(t, http.MethodGet, "/activities/calendar?from=2024-01-01&to=2024-01-02", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)

	var result struct {
		Items []struct {
			Count int `json:"count"`
		} `json:"items"`
	}
	resp.decode(t, &result)
	require.Len(t, result.Items, 2)
	require.Equal(t, 0, result.Items[0].Count)
	require.Equal(t, 0, result.Items[1].Count)
}

func TestGetActivitiesCalendar_RangeTooLong400(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")

	resp := doRequest(t, http.MethodGet, "/activities/calendar?from=2023-01-01&to=2024-06-01", nil, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, resp.status)
}

func TestGetActivitiesCalendar_Unauthorized(t *testing.T) {
	resetDB(t)
	resp := doRequest(t, http.MethodGet, "/activities/calendar?from=2024-01-01&to=2024-01-02", nil, "")
	require.Equal(t, http.StatusUnauthorized, resp.status)
}

func TestGetActivitiesDay_MixesAllPetsSortedByDateTime(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	createProfile(t, tokens.AccessToken, "Іван")

	catID := createPet(t, tokens.AccessToken, "Барсик")
	dogID := createPet(t, tokens.AccessToken, "Рекс")

	// Вставлены не по порядку времени — ответ должен быть отсортирован по
	// date_time по возрастанию, независимо от порядка создания.
	createEvent(t, tokens.AccessToken, dogID, "2024-01-01T15:00:00Z", "weight", map[string]any{"amount": 12.0})
	createEvent(t, tokens.AccessToken, catID, "2024-01-01T08:00:00Z", "weight", map[string]any{"amount": 4.2})
	// Другой день — не должен попасть в результат.
	createEvent(t, tokens.AccessToken, catID, "2024-01-02T08:00:00Z", "weight", map[string]any{"amount": 4.3})

	resp := doRequest(t, http.MethodGet, "/activities/day?date=2024-01-01", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)

	var result struct {
		Date  string `json:"date"`
		Items []struct {
			Date    string `json:"date"`
			PetID   string `json:"pet_id"`
			PetName string `json:"pet_name"`
		} `json:"items"`
	}
	resp.decode(t, &result)
	require.Equal(t, "2024-01-01", result.Date)
	require.Len(t, result.Items, 2)
	require.Equal(t, catID, result.Items[0].PetID)
	require.Equal(t, "Барсик", result.Items[0].PetName)
	require.Equal(t, dogID, result.Items[1].PetID)
	require.Equal(t, "Рекс", result.Items[1].PetName)
}

func TestGetActivitiesDay_EmptyIsNot404(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")

	resp := doRequest(t, http.MethodGet, "/activities/day?date=2024-01-01", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)

	var result struct {
		Items []any `json:"items"`
	}
	resp.decode(t, &result)
	require.Empty(t, result.Items)
}

func TestGetActivitiesDay_ExcludesSoftDeletedPetAndEvent(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	createProfile(t, tokens.AccessToken, "Іван")

	catID := createPet(t, tokens.AccessToken, "Барсик")
	dogID := createPet(t, tokens.AccessToken, "Рекс")
	createEvent(t, tokens.AccessToken, catID, "2024-01-01T08:00:00Z", "weight", map[string]any{"amount": 4.2})
	createEvent(t, tokens.AccessToken, dogID, "2024-01-01T09:00:00Z", "weight", map[string]any{"amount": 12.0})

	// Мягко удаляем питомца dogID — его события должны исчезнуть из выдачи.
	deletePet := doRequest(t, http.MethodDelete, "/pet/"+dogID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, deletePet.status)

	resp := doRequest(t, http.MethodGet, "/activities/day?date=2024-01-01", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)

	var result struct {
		Items []struct {
			PetID string `json:"pet_id"`
		} `json:"items"`
	}
	resp.decode(t, &result)
	require.Len(t, result.Items, 1)
	require.Equal(t, catID, result.Items[0].PetID)
}

func TestGetActivitiesDay_Unauthorized(t *testing.T) {
	resetDB(t)
	resp := doRequest(t, http.MethodGet, "/activities/day?date=2024-01-01", nil, "")
	require.Equal(t, http.StatusUnauthorized, resp.status)
}
