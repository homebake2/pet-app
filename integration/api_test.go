//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func uniqueLogin(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("user-%s@example.com", uuid.NewString())
}

func TestAuthFlow(t *testing.T) {
	resetDB(t)

	login := uniqueLogin(t)
	tokens := registerUser(t, login, "correct-password")

	// Повторная "регистрация" с тем же логином и правильным паролем — это вход.
	same := doRequest(t, http.MethodPost, "/auth/login", map[string]string{
		"login":    login,
		"password": "correct-password",
	}, "")
	require.Equal(t, http.StatusOK, same.status)

	// Неверный пароль → 403 FORBIDDEN.
	wrong := doRequest(t, http.MethodPost, "/auth/login", map[string]string{
		"login":    login,
		"password": "wrong-password",
	}, "")
	require.Equal(t, http.StatusForbidden, wrong.status)

	// Refresh выдаёт новую пару токенов.
	refreshed := doRequest(t, http.MethodPost, "/auth/refresh", map[string]string{
		"refresh_token": tokens.RefreshToken,
	}, "")
	require.Equal(t, http.StatusOK, refreshed.status)
	var newTokens authTokens
	refreshed.decode(t, &newTokens)
	require.NotEmpty(t, newTokens.AccessToken)

	// Logout инвалидирует refresh token.
	loggedOut := doRequest(t, http.MethodPost, "/auth/logout", map[string]string{
		"refresh_token": newTokens.RefreshToken,
	}, "")
	require.Equal(t, http.StatusNoContent, loggedOut.status)

	reused := doRequest(t, http.MethodPost, "/auth/refresh", map[string]string{
		"refresh_token": newTokens.RefreshToken,
	}, "")
	require.Equal(t, http.StatusUnauthorized, reused.status)
}

func TestProfileCRUD(t *testing.T) {
	resetDB(t)

	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	created := doRequest(t, http.MethodPost, "/profile", map[string]string{
		"first_name": "Иван",
	}, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, created.status)

	got := doRequest(t, http.MethodGet, "/profile", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, got.status)
	var profile struct {
		FirstName string `json:"first_name"`
	}
	got.decode(t, &profile)
	require.Equal(t, "Иван", profile.FirstName)

	updated := doRequest(t, http.MethodPut, "/profile", map[string]string{
		"last_name": "Иванов",
	}, tokens.AccessToken)
	require.Equal(t, http.StatusOK, updated.status)

	// DELETE /profile удаляет весь аккаунт (см. handlers.DeleteAccountHandler),
	// а не только строку профиля — токен пользователя после этого недействителен.
	deleted := doRequest(t, http.MethodDelete, "/profile", nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, deleted.status)

	afterDelete := doRequest(t, http.MethodGet, "/profile", nil, tokens.AccessToken)
	require.Equal(t, http.StatusUnauthorized, afterDelete.status)
}

func TestPetAndEventLifecycle(t *testing.T) {
	resetDB(t)

	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	createProfile(t, tokens.AccessToken, "Иван")

	petResp := doRequest(t, http.MethodPost, "/pet", map[string]any{
		"name":    "Барсик",
		"species": "cat",
		"gender":  "male",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, petResp.status, "%s", petResp.body)
	var pet struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	petResp.decode(t, &pet)
	require.NotEmpty(t, pet.ID)
	require.Equal(t, "Барсик", pet.Name)

	list := doRequest(t, http.MethodGet, "/pet", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, list.status)
	var listBody struct {
		Items []struct{ ID string } `json:"items"`
	}
	list.decode(t, &listBody)
	require.Len(t, listBody.Items, 1)

	getOne := doRequest(t, http.MethodGet, "/pet/"+pet.ID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, getOne.status)

	eventDate := time.Now().UTC().Format(time.RFC3339)
	eventResp := doRequest(t, http.MethodPost, "/events", map[string]any{
		"pet_id": pet.ID,
		"date":   eventDate,
		"type":   "weight",
		"value":  "4.2",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, eventResp.status, "%s", eventResp.body)
	var event struct {
		ID      string `json:"id"`
		PetID   string `json:"pet_id"`
		PetName string `json:"pet_name"`
	}
	eventResp.decode(t, &event)
	require.NotEmpty(t, event.ID)
	require.Equal(t, pet.ID, event.PetID)
	require.Equal(t, "Барсик", event.PetName)

	getEvent := doRequest(t, http.MethodGet, "/events/"+event.ID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, getEvent.status)

	patched := doRequest(t, http.MethodPatch, "/events/"+event.ID, map[string]any{
		"pet_id": pet.ID,
		"value":  "4.3",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, patched.status, "%s", patched.body)

	petEvents := doRequest(t, http.MethodGet, "/pet/"+pet.ID+"/events", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, petEvents.status)
	var petEventsBody struct {
		Items []struct{ Value string } `json:"items"`
	}
	petEvents.decode(t, &petEventsBody)
	require.Len(t, petEventsBody.Items, 1)
	require.Equal(t, "4.3", petEventsBody.Items[0].Value)

	today := time.Now().UTC().Format("2006-01-02")
	activities := doRequest(t, http.MethodGet,
		fmt.Sprintf("/activities?pet_id=%s&from=%s&to=%s", pet.ID, today, today),
		nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, activities.status)
	var activitiesBody struct {
		PetName string `json:"pet_name"`
		Items   []struct {
			Events []struct{ ID string } `json:"events"`
		} `json:"items"`
	}
	activities.decode(t, &activitiesBody)
	require.Equal(t, "Барсик", activitiesBody.PetName)
	require.Len(t, activitiesBody.Items, 1)
	require.Len(t, activitiesBody.Items[0].Events, 1)

	deletedEvent := doRequest(t, http.MethodDelete, "/events/"+event.ID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, deletedEvent.status)

	deletedPet := doRequest(t, http.MethodDelete, "/pet/"+pet.ID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, deletedPet.status)

	getDeletedPet := doRequest(t, http.MethodGet, "/pet/"+pet.ID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNotFound, getDeletedPet.status)
}

func TestPetOwnershipIsolation(t *testing.T) {
	resetDB(t)

	ownerTokens := registerUser(t, uniqueLogin(t), "correct-password")
	createProfile(t, ownerTokens.AccessToken, "Владелец")
	petResp := doRequest(t, http.MethodPost, "/pet", map[string]any{
		"name":    "Рекс",
		"species": "dog",
	}, ownerTokens.AccessToken)
	require.Equal(t, http.StatusCreated, petResp.status)
	var pet struct {
		ID string `json:"id"`
	}
	petResp.decode(t, &pet)

	strangerTokens := registerUser(t, uniqueLogin(t), "correct-password")
	createProfile(t, strangerTokens.AccessToken, "Чужой")

	forbidden := doRequest(t, http.MethodGet, "/pet/"+pet.ID, nil, strangerTokens.AccessToken)
	require.Equal(t, http.StatusNotFound, forbidden.status)
}

func TestUnauthorizedAccess(t *testing.T) {
	resetDB(t)

	resp := doRequest(t, http.MethodGet, "/pet", nil, "")
	require.Equal(t, http.StatusUnauthorized, resp.status)
	var errBody struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	resp.decode(t, &errBody)
	require.Equal(t, "UNAUTHORIZED", errBody.Code)
}
