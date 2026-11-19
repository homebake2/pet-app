//go:build integration

package integration

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type petBreedFields struct {
	ID          string  `json:"id"`
	Breed       *string `json:"breed"`
	BreedCustom *string `json:"breed_custom"`
}

func getPetBreed(t *testing.T, token, petID string) petBreedFields {
	t.Helper()
	resp := doRequest(t, http.MethodGet, "/pet/"+petID, nil, token)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	var pet petBreedFields
	resp.decode(t, &pet)
	return pet
}

// TestPetBreedCustom проверяет сквозной цикл собственного названия породы:
// сохранение при breed = other, автоочистку при смене породы, явную очистку
// пустой строкой и отказ при breed, отличном от other.
func TestPetBreedCustom(t *testing.T) {
	resetDB(t)

	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	createProfile(t, tokens.AccessToken, "Иван")

	// breed_custom без breed = other отклоняется.
	rejected := doRequest(t, http.MethodPost, "/pet", map[string]any{
		"name": "Рекс", "species": "dog", "breed": "111a9c3e-5b6d-4f9a-8c2e-1d6b9a5f3e01", "breed_custom": "Дворняга",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, rejected.status, "%s", rejected.body)

	// Создание с other и названием (пробелы по краям обрезаются).
	createResp := doRequest(t, http.MethodPost, "/pet", map[string]any{
		"name": "Рекс", "species": "dog", "breed": "other", "breed_custom": "  Дворняга ",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, createResp.status, "%s", createResp.body)
	var created petBreedFields
	createResp.decode(t, &created)
	require.NotEmpty(t, created.ID)

	pet := getPetBreed(t, tokens.AccessToken, created.ID)
	require.Equal(t, "other", *pet.Breed)
	require.Equal(t, "Дворняга", *pet.BreedCustom)

	// Список питомцев отдаёт breed_custom.
	listResp := doRequest(t, http.MethodGet, "/pet", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, listResp.status, "%s", listResp.body)
	var list struct {
		Items []petBreedFields `json:"items"`
	}
	listResp.decode(t, &list)
	require.Len(t, list.Items, 1)
	require.Equal(t, "Дворняга", *list.Items[0].BreedCustom)

	// Смена только названия при сохранённом breed = other.
	update := doRequest(t, http.MethodPut, "/pet/"+created.ID, map[string]any{"breed_custom": "Метис"}, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, update.status, "%s", update.body)
	require.Equal(t, "Метис", *getPetBreed(t, tokens.AccessToken, created.ID).BreedCustom)

	// Пустая строка очищает название, breed остаётся other.
	clear := doRequest(t, http.MethodPut, "/pet/"+created.ID, map[string]any{"breed_custom": ""}, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, clear.status, "%s", clear.body)
	pet = getPetBreed(t, tokens.AccessToken, created.ID)
	require.Equal(t, "other", *pet.Breed)
	require.Nil(t, pet.BreedCustom)

	// Вернули название и сменили породу на справочную: название очищается само.
	restore := doRequest(t, http.MethodPut, "/pet/"+created.ID, map[string]any{"breed_custom": "Дворняга"}, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, restore.status, "%s", restore.body)
	switched := doRequest(t, http.MethodPut, "/pet/"+created.ID, map[string]any{
		"breed": "111a9c3e-5b6d-4f9a-8c2e-1d6b9a5f3e01",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, switched.status, "%s", switched.body)
	pet = getPetBreed(t, tokens.AccessToken, created.ID)
	require.Equal(t, "111a9c3e-5b6d-4f9a-8c2e-1d6b9a5f3e01", *pet.Breed)
	require.Nil(t, pet.BreedCustom)

	// Название при итоговом breed, отличном от other, отклоняется.
	badUpdate := doRequest(t, http.MethodPut, "/pet/"+created.ID, map[string]any{"breed_custom": "Дворняга"}, tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, badUpdate.status, "%s", badUpdate.body)
}

// TestImportLocalDataPetBreedCustom проверяет, что перенос локальных данных
// сохраняет собственное название породы, а недопустимое сочетание отклоняет.
func TestImportLocalDataPetBreedCustom(t *testing.T) {
	resetDB(t)

	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	bad := doRequest(t, http.MethodPost, "/import/local-data", map[string]any{
		"pets":           []map[string]any{{"local_id": "p1", "name": "Рекс", "species": "dog", "breed_custom": "Дворняга"}},
		"events":         []any{},
		"reminder_plans": []any{},
	}, tokens.AccessToken, map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equalf(t, http.StatusBadRequest, bad.status, "%s", bad.body)

	ok := doRequest(t, http.MethodPost, "/import/local-data", map[string]any{
		"pets":           []map[string]any{{"local_id": "p1", "name": "Рекс", "species": "dog", "breed": "other", "breed_custom": "Дворняга"}},
		"events":         []any{},
		"reminder_plans": []any{},
	}, tokens.AccessToken, map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equalf(t, http.StatusOK, ok.status, "%s", ok.body)

	listResp := doRequest(t, http.MethodGet, "/pet", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, listResp.status)
	var list struct {
		Items []petBreedFields `json:"items"`
	}
	listResp.decode(t, &list)
	require.Len(t, list.Items, 1)
	require.Equal(t, "Дворняга", *list.Items[0].BreedCustom)
}
