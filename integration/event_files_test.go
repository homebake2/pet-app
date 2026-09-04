//go:build integration

// Сквозной сценарий generic-механизма файлов сущностей на втором конкретном
// подключении — файлы события (owner_type = "event_file", кардинальность
// «до 10», см. artifacts/PET/pages/calendar/fayly-sobytiya-backend.md).
package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// createEventReturningID — как createEvent (events_stats_test.go), но
// возвращает id созданного события — нужен тестам файлов события для
// owner_id.
func createEventReturningID(t *testing.T, token, petID, date, eventType string, value map[string]any) string {
	t.Helper()
	resp := doRequest(t, http.MethodPost, "/events", map[string]any{
		"pet_id": petID,
		"date":   date,
		"type":   eventType,
		"value":  value,
	}, token)
	require.Equalf(t, http.StatusCreated, resp.status, "создание события %s не удалось: %s", eventType, resp.body)
	var event struct {
		ID string `json:"id"`
	}
	resp.decode(t, &event)
	require.NotEmpty(t, event.ID)
	return event.ID
}

func requestEventFileUploadURL(t *testing.T, token, eventID, contentType string, filename *string) uploadURLResponse {
	t.Helper()
	body := map[string]any{
		"owner_type":   "event_file",
		"owner_id":     eventID,
		"content_type": contentType,
	}
	if filename != nil {
		body["filename"] = *filename
	}
	resp := doRequest(t, http.MethodPost, "/files/upload-url", body, token)
	require.Equalf(t, http.StatusOK, resp.status, "upload-url не удался: %s", resp.body)
	var out uploadURLResponse
	resp.decode(t, &out)
	require.NotEmpty(t, out.FileID)
	require.NotEmpty(t, out.UploadURL)
	return out
}

type eventFileItem struct {
	FileID      string  `json:"file_id"`
	URL         string  `json:"url"`
	ContentType string  `json:"content_type"`
	Filename    *string `json:"filename"`
}

type eventWithFiles struct {
	ID    string          `json:"id"`
	Files []eventFileItem `json:"files"`
}

// TestFilesUploadCompleteDelete_EventFileFullFlow проверяет полный протокол
// (upload-url/complete/delete) для owner_type = event_file: filename
// сохраняется и возвращается, files в GetEventResponse (POST /events,
// GET /events/{id}) отражает подтверждённые файлы в порядке position,
// удаление одного файла не переприсваивает position оставшимся.
func TestFilesUploadCompleteDelete_EventFileFullFlow(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	createProfile(t, tokens.AccessToken, "Іван")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	eventID := createEventReturningID(t, tokens.AccessToken, petID, "2024-01-01T10:00:00Z", "weight", map[string]any{"amount": 4.2})

	// Изначально файлов нет.
	eventResp := doRequest(t, http.MethodGet, "/events/"+eventID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, eventResp.status)
	var event eventWithFiles
	eventResp.decode(t, &event)
	require.Empty(t, event.Files)

	// Фотография: filename не передаётся и не нужен.
	photoUpload := requestEventFileUploadURL(t, tokens.AccessToken, eventID, "image/jpeg", nil)
	photoComplete := doRequest(t, http.MethodPost, "/files/"+photoUpload.FileID+"/complete", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, photoComplete.status, "complete фото не удался: %s", photoComplete.body)

	// Документ: filename передаётся и должен вернуться при чтении.
	docFilename := "analysis.pdf"
	docUpload := requestEventFileUploadURL(t, tokens.AccessToken, eventID, "application/pdf", &docFilename)
	docComplete := doRequest(t, http.MethodPost, "/files/"+docUpload.FileID+"/complete", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, docComplete.status, "complete документа не удался: %s", docComplete.body)

	eventResp = doRequest(t, http.MethodGet, "/events/"+eventID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, eventResp.status)
	eventResp.decode(t, &event)
	require.Len(t, event.Files, 2)
	// Порядок — по position, то есть по порядку подтверждения: фото раньше документа.
	require.Equal(t, photoUpload.FileID, event.Files[0].FileID)
	require.Nil(t, event.Files[0].Filename)
	require.Equal(t, docUpload.FileID, event.Files[1].FileID)
	require.NotNil(t, event.Files[1].Filename)
	require.Equal(t, docFilename, *event.Files[1].Filename)

	// files_count в списочных эндпоинтах отражает то же количество.
	activitiesResp := doRequest(t, http.MethodGet, "/activities?pet_id="+petID+"&from=2024-01-01&to=2024-01-01", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, activitiesResp.status)
	var activities struct {
		Items []struct {
			Events []struct {
				FilesCount int `json:"files_count"`
			} `json:"events"`
		} `json:"items"`
	}
	activitiesResp.decode(t, &activities)
	require.Len(t, activities.Items, 1)
	require.Len(t, activities.Items[0].Events, 1)
	require.Equal(t, 2, activities.Items[0].Events[0].FilesCount)

	// Удаление одного файла: оставшийся сохраняет свой исходный position (не
	// переприсваивается), т.е. документ остаётся единственным элементом.
	deletePhoto := doRequest(t, http.MethodDelete, "/files/"+photoUpload.FileID, nil, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, deletePhoto.status, "delete фото не удался: %s", deletePhoto.body)

	eventResp = doRequest(t, http.MethodGet, "/events/"+eventID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, eventResp.status)
	eventResp.decode(t, &event)
	require.Len(t, event.Files, 1)
	require.Equal(t, docUpload.FileID, event.Files[0].FileID)
}

// TestFilesCompleteHandler_EventFile_LimitReached409 проверяет лимит
// кардинальности «до 10»: 11-я попытка complete для того же события
// возвращает 409, а 10 предыдущих — успешны.
func TestFilesCompleteHandler_EventFile_LimitReached409(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	createProfile(t, tokens.AccessToken, "Іван")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	eventID := createEventReturningID(t, tokens.AccessToken, petID, "2024-01-01T10:00:00Z", "weight", map[string]any{"amount": 4.2})

	for i := 0; i < 10; i++ {
		upload := requestEventFileUploadURL(t, tokens.AccessToken, eventID, "image/jpeg", nil)
		complete := doRequest(t, http.MethodPost, "/files/"+upload.FileID+"/complete", nil, tokens.AccessToken)
		require.Equalf(t, http.StatusNoContent, complete.status, "complete файла #%d не удался: %s", i+1, complete.body)
	}

	eleventhUpload := requestEventFileUploadURL(t, tokens.AccessToken, eventID, "image/jpeg", nil)
	eleventhComplete := doRequest(t, http.MethodPost, "/files/"+eleventhUpload.FileID+"/complete", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusConflict, eleventhComplete.status, "%s", eleventhComplete.body)

	eventResp := doRequest(t, http.MethodGet, "/events/"+eventID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, eventResp.status)
	var event eventWithFiles
	eventResp.decode(t, &event)
	require.Len(t, event.Files, 10)
}

func TestFilesUploadUrl_EventFile_ForeignEventOwnership404(t *testing.T) {
	resetDB(t)
	ownerTokens := registerUser(t, uniqueLogin(t), "password123")
	createProfile(t, ownerTokens.AccessToken, "Іван")
	petID := createPet(t, ownerTokens.AccessToken, "Барсик")
	eventID := createEventReturningID(t, ownerTokens.AccessToken, petID, "2024-01-01T10:00:00Z", "weight", map[string]any{"amount": 4.2})

	strangerTokens := registerUser(t, uniqueLogin(t), "password123")
	createProfile(t, strangerTokens.AccessToken, "Петро")

	resp := doRequest(t, http.MethodPost, "/files/upload-url", map[string]any{
		"owner_type":   "event_file",
		"owner_id":     eventID,
		"content_type": "image/jpeg",
	}, strangerTokens.AccessToken)
	require.Equal(t, http.StatusNotFound, resp.status)
}

func TestFilesUploadUrl_EventFile_BadContentType(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	createProfile(t, tokens.AccessToken, "Іван")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	eventID := createEventReturningID(t, tokens.AccessToken, petID, "2024-01-01T10:00:00Z", "weight", map[string]any{"amount": 4.2})

	resp := doRequest(t, http.MethodPost, "/files/upload-url", map[string]any{
		"owner_type":   "event_file",
		"owner_id":     eventID,
		"content_type": "video/mp4",
	}, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, resp.status)
}

// Валидация длины filename (≤255 символов) покрыта unit-тестом
// TestFilesUploadUrlHandler_EventFile_FilenameTooLong (handlers/files_test.go),
// а не здесь: doRequest сверяет каждый запрос с open-api/spec.json, где
// filename объявлен с maxLength 255 — сознательно невалидный по спеке запрос
// сломал бы саму проверку гармонизации запроса со спекой, а не только
// проверял бы обработку сервером (та же идиома, что и у отсутствующего
// Idempotency-Key в integration/import_test.go).
