//go:build integration

// Сквозной сценарий generic-механизма файлов сущностей (см.
// artifacts/PET/pages/integrations/obschie-trebovaniya-fayly-sushchnostei.md)
// на его первом конкретном подключении — фотографии питомца (owner_type =
// "pet_photo", см. artifacts/PET/pages/pets/fotografiya-pitomtsa-backend.md).
package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

type uploadURLResponse struct {
	FileID      string `json:"file_id"`
	UploadURL   string `json:"upload_url"`
	ContentType string `json:"content_type"`
	ExpiresIn   int    `json:"expires_in"`
}

type petPhotoFields struct {
	PhotoURL    *string `json:"photo_url"`
	PhotoFileID *string `json:"photo_file_id"`
}

// requestPetPhotoUploadURL запрашивает presigned PUT URL для owner_type =
// "pet_photo" и данного питомца.
func requestPetPhotoUploadURL(t *testing.T, token, petID, contentType string) uploadURLResponse {
	t.Helper()
	resp := doRequest(t, http.MethodPost, "/files/upload-url", map[string]any{
		"owner_type":   "pet_photo",
		"owner_id":     petID,
		"content_type": contentType,
	}, token)
	require.Equalf(t, http.StatusOK, resp.status, "upload-url не удался: %s", resp.body)
	var out uploadURLResponse
	resp.decode(t, &out)
	require.NotEmpty(t, out.FileID)
	require.NotEmpty(t, out.UploadURL)
	return out
}

func TestFilesUploadCompleteDelete_PetPhotoFullFlow(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	createProfile(t, tokens.AccessToken, "Іван")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	// Изначально фотографии нет — оба поля null.
	petResp := doRequest(t, http.MethodGet, "/pet/"+petID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, petResp.status)
	var pet petPhotoFields
	petResp.decode(t, &pet)
	require.Nil(t, pet.PhotoURL)
	require.Nil(t, pet.PhotoFileID)

	// 1. upload-url
	upload := requestPetPhotoUploadURL(t, tokens.AccessToken, petID, "image/jpeg")

	// 2. клиент грузит байты напрямую в S3 — вне зоны ответственности backend,
	// в этом тесте не выполняется (presigned URL — синтетический, реального
	// S3 у теста нет).

	// 3. complete
	completeResp := doRequest(t, http.MethodPost, "/files/"+upload.FileID+"/complete", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, completeResp.status, "complete не удался: %s", completeResp.body)

	// Теперь фотография видна в GET /pet/{id}.
	petResp = doRequest(t, http.MethodGet, "/pet/"+petID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, petResp.status)
	petResp.decode(t, &pet)
	require.NotNil(t, pet.PhotoURL)
	require.NotNil(t, pet.PhotoFileID)
	require.Equal(t, upload.FileID, *pet.PhotoFileID)

	// ...и в списке GET /pet.
	listResp := doRequest(t, http.MethodGet, "/pet", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, listResp.status)
	var list struct {
		Items []struct {
			ID       string  `json:"id"`
			PhotoURL *string `json:"photo_url"`
		} `json:"items"`
	}
	listResp.decode(t, &list)
	require.Len(t, list.Items, 1)
	require.NotNil(t, list.Items[0].PhotoURL)

	// Замена: повторная загрузка + complete заменяет фотографию (кардинальность
	// «ровно 1»).
	secondUpload := requestPetPhotoUploadURL(t, tokens.AccessToken, petID, "image/png")
	secondComplete := doRequest(t, http.MethodPost, "/files/"+secondUpload.FileID+"/complete", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, secondComplete.status, "повторный complete не удался: %s", secondComplete.body)

	petResp = doRequest(t, http.MethodGet, "/pet/"+petID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, petResp.status)
	petResp.decode(t, &pet)
	require.NotNil(t, pet.PhotoFileID)
	require.Equal(t, secondUpload.FileID, *pet.PhotoFileID, "photo_file_id должен указывать на новую фотографию после замены")

	// Старый file_id больше не адресуем — DELETE на него теперь 404
	// (строка удалена при подтверждении замены).
	deleteOld := doRequest(t, http.MethodDelete, "/files/"+upload.FileID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNotFound, deleteOld.status)

	// 4-5. удаление текущей фотографии.
	deleteCurrent := doRequest(t, http.MethodDelete, "/files/"+secondUpload.FileID, nil, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, deleteCurrent.status, "delete не удался: %s", deleteCurrent.body)

	petResp = doRequest(t, http.MethodGet, "/pet/"+petID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, petResp.status)
	petResp.decode(t, &pet)
	require.Nil(t, pet.PhotoURL)
	require.Nil(t, pet.PhotoFileID)

	// Повторный DELETE того же file_id — идемпотентный 404 («уже удалено»).
	deleteAgain := doRequest(t, http.MethodDelete, "/files/"+secondUpload.FileID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNotFound, deleteAgain.status)
}

func TestFilesUploadUrl_UnknownOwnerType(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	createProfile(t, tokens.AccessToken, "Іван")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	resp := doRequest(t, http.MethodPost, "/files/upload-url", map[string]any{
		"owner_type":   "event_photo",
		"owner_id":     petID,
		"content_type": "image/jpeg",
	}, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, resp.status)
}

func TestFilesUploadUrl_BadContentType(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	createProfile(t, tokens.AccessToken, "Іван")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	resp := doRequest(t, http.MethodPost, "/files/upload-url", map[string]any{
		"owner_type":   "pet_photo",
		"owner_id":     petID,
		"content_type": "application/pdf",
	}, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, resp.status)
}

func TestFilesUploadUrl_ForeignPetOwnership404(t *testing.T) {
	resetDB(t)
	ownerTokens := registerUser(t, uniqueLogin(t), "password123")
	createProfile(t, ownerTokens.AccessToken, "Іван")
	petID := createPet(t, ownerTokens.AccessToken, "Барсик")

	strangerTokens := registerUser(t, uniqueLogin(t), "password123")
	createProfile(t, strangerTokens.AccessToken, "Петро")

	resp := doRequest(t, http.MethodPost, "/files/upload-url", map[string]any{
		"owner_type":   "pet_photo",
		"owner_id":     petID,
		"content_type": "image/jpeg",
	}, strangerTokens.AccessToken)
	require.Equal(t, http.StatusNotFound, resp.status)
}

func TestFilesUploadUrl_DeletedPet404(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	createProfile(t, tokens.AccessToken, "Іван")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	deleteResp := doRequest(t, http.MethodDelete, "/pet/"+petID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, deleteResp.status)

	resp := doRequest(t, http.MethodPost, "/files/upload-url", map[string]any{
		"owner_type":   "pet_photo",
		"owner_id":     petID,
		"content_type": "image/jpeg",
	}, tokens.AccessToken)
	require.Equal(t, http.StatusNotFound, resp.status)
}
