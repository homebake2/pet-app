package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"myauthservice/models"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	fakePresignedPutURL = "https://fake-s3.example.com/put"
	fakePresignedGetURL = "https://fake-s3.example.com/get"
)

// fakeStorage — тестовая реализация Storage без сетевых обращений: presign
// возвращает фиксированные URL, DeleteObject только запоминает ключ.
type fakeStorage struct {
	deletedKeys []string
	deleteErr   error
}

func (f *fakeStorage) PresignPutURL(ctx context.Context, objectKey, contentType string) (string, int, error) {
	return fakePresignedPutURL, 300, nil
}

func (f *fakeStorage) PresignGetURL(ctx context.Context, objectKey string) (string, error) {
	return fakePresignedGetURL, nil
}

func (f *fakeStorage) DeleteObject(ctx context.Context, objectKey string) error {
	f.deletedKeys = append(f.deletedKeys, objectKey)
	return f.deleteErr
}

// setupFakeStorage подменяет пакетную переменную storage на fakeStorage на
// время теста — аналогично setupMockDB для database.DB.
func setupFakeStorage(t *testing.T) *fakeStorage {
	t.Helper()
	fs := &fakeStorage{}
	original := storage
	storage = fs
	t.Cleanup(func() { storage = original })
	return fs
}

func filesRequest(t *testing.T, method, path string, body any, authed bool) *http.Request {
	t.Helper()
	r := doRequest(method, path, body)
	if authed {
		r.Header.Set("Authorization", "Bearer "+validAccessToken(t, testUserID))
	}
	return r
}

func expectPetOwnership(mock sqlmock.Sqlmock, petID string, userID string, count int) {
	mock.ExpectQuery(`SELECT COUNT\(1\) FROM pet WHERE id = \$1 AND user_id = \$2 AND deleted_at IS NULL`).
		WithArgs(petID, userID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(count))
}

// --- POST /files/upload-url ---

func TestFilesUploadUrlHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	setupFakeStorage(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnership(mock, testPetID, testUserID, 1)
	mock.ExpectExec(`INSERT INTO file \(id, owner_type, owner_id, user_id, object_key, content_type, position, confirmed_at\)`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	body := models.PostFilesUploadUrlRequest{OwnerType: "pet_photo", OwnerID: testPetID, ContentType: "image/jpeg"}
	w := httptest.NewRecorder()
	FilesUploadUrlHandler(w, filesRequest(t, http.MethodPost, "/files/upload-url", body, true))

	require.Equal(t, http.StatusOK, w.Code)
	var resp models.PostFilesUploadUrlResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.FileID)
	assert.Equal(t, fakePresignedPutURL, resp.UploadURL)
	assert.Equal(t, "image/jpeg", resp.ContentType)
	assert.Equal(t, 300, resp.ExpiresIn)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFilesUploadUrlHandler_Unauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	body := models.PostFilesUploadUrlRequest{OwnerType: "pet_photo", OwnerID: testPetID, ContentType: "image/jpeg"}
	FilesUploadUrlHandler(w, filesRequest(t, http.MethodPost, "/files/upload-url", body, false))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestFilesUploadUrlHandler_UnknownOwnerType(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	body := models.PostFilesUploadUrlRequest{OwnerType: "event_photo", OwnerID: testPetID, ContentType: "image/jpeg"}
	w := httptest.NewRecorder()
	FilesUploadUrlHandler(w, filesRequest(t, http.MethodPost, "/files/upload-url", body, true))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFilesUploadUrlHandler_OwnershipFailure(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnership(mock, testPetID, testUserID, 0)

	body := models.PostFilesUploadUrlRequest{OwnerType: "pet_photo", OwnerID: testPetID, ContentType: "image/jpeg"}
	w := httptest.NewRecorder()
	FilesUploadUrlHandler(w, filesRequest(t, http.MethodPost, "/files/upload-url", body, true))

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFilesUploadUrlHandler_BadContentType(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectPetOwnership(mock, testPetID, testUserID, 1)

	body := models.PostFilesUploadUrlRequest{OwnerType: "pet_photo", OwnerID: testPetID, ContentType: "application/pdf"}
	w := httptest.NewRecorder()
	FilesUploadUrlHandler(w, filesRequest(t, http.MethodPost, "/files/upload-url", body, true))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFilesUploadUrlHandler_MissingFields(t *testing.T) {
	w := httptest.NewRecorder()
	body := models.PostFilesUploadUrlRequest{OwnerType: "", OwnerID: "", ContentType: ""}
	FilesUploadUrlHandler(w, filesRequest(t, http.MethodPost, "/files/upload-url", body, false))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// --- POST /files/{file_id}/complete ---

var fileRowColumns = []string{
	"id", "owner_type", "owner_id", "user_id", "object_key", "content_type", "position", "confirmed_at", "created_at",
}

const testFileID = "66666666-6666-6666-6666-666666666666"

func expectGetFileByID(mock sqlmock.Sqlmock, fileID, ownerID, userID, objectKey string, confirmedAt any) {
	mock.ExpectQuery(`SELECT id, owner_type, owner_id, user_id, object_key, content_type, position, confirmed_at, created_at\s+FROM file\s+WHERE id = \$1`).
		WithArgs(fileID).
		WillReturnRows(sqlmock.NewRows(fileRowColumns).AddRow(
			fileID, "pet_photo", ownerID, userID, objectKey, "image/jpeg", nil, confirmedAt, time.Now(),
		))
}

func TestFilesCompleteHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	setupFakeStorage(t)
	expectTokensValid(mock, testUserID)
	objectKey := "pet_photo/" + testPetID + "/" + testFileID
	expectGetFileByID(mock, testFileID, testPetID, testUserID, objectKey, nil)
	expectPetOwnership(mock, testPetID, testUserID, 1)

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE file SET confirmed_at = now\(\) WHERE id = \$1`).
		WithArgs(testFileID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT id, object_key FROM file\s+WHERE owner_type = \$1 AND owner_id = \$2 AND confirmed_at IS NOT NULL AND id != \$3`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "object_key"}))
	mock.ExpectCommit()

	w := httptest.NewRecorder()
	FilesCompleteHandler(w, filesRequest(t, http.MethodPost, "/files/"+testFileID+"/complete", nil, true), testFileID)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFilesCompleteHandler_CardinalityReplacement(t *testing.T) {
	mock := setupMockDB(t)
	fs := setupFakeStorage(t)
	expectTokensValid(mock, testUserID)
	objectKey := "pet_photo/" + testPetID + "/" + testFileID
	expectGetFileByID(mock, testFileID, testPetID, testUserID, objectKey, nil)
	expectPetOwnership(mock, testPetID, testUserID, 1)

	oldFileID := "77777777-7777-7777-7777-777777777777"
	oldObjectKey := "pet_photo/" + testPetID + "/" + oldFileID

	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE file SET confirmed_at = now\(\) WHERE id = \$1`).
		WithArgs(testFileID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT id, object_key FROM file\s+WHERE owner_type = \$1 AND owner_id = \$2 AND confirmed_at IS NOT NULL AND id != \$3`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "object_key"}).AddRow(oldFileID, oldObjectKey))
	mock.ExpectExec(`DELETE FROM file WHERE id = ANY\(\$1\)`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	w := httptest.NewRecorder()
	FilesCompleteHandler(w, filesRequest(t, http.MethodPost, "/files/"+testFileID+"/complete", nil, true), testFileID)

	assert.Equal(t, http.StatusNoContent, w.Code)
	require.Contains(t, fs.deletedKeys, oldObjectKey)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFilesCompleteHandler_OwnershipRecheckFails(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	objectKey := "pet_photo/" + testPetID + "/" + testFileID
	expectGetFileByID(mock, testFileID, testPetID, testUserID, objectKey, nil)
	// Питомец мог быть удалён/передан между upload-url и complete — та же
	// проверка владения, повторно выполненная и не пройденная.
	expectPetOwnership(mock, testPetID, testUserID, 0)

	w := httptest.NewRecorder()
	FilesCompleteHandler(w, filesRequest(t, http.MethodPost, "/files/"+testFileID+"/complete", nil, true), testFileID)

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFilesCompleteHandler_FileNotFound(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, owner_type, owner_id, user_id, object_key, content_type, position, confirmed_at, created_at\s+FROM file\s+WHERE id = \$1`).
		WillReturnError(sql.ErrNoRows)

	w := httptest.NewRecorder()
	FilesCompleteHandler(w, filesRequest(t, http.MethodPost, "/files/"+testFileID+"/complete", nil, true), testFileID)

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFilesCompleteHandler_InvalidFileID(t *testing.T) {
	w := httptest.NewRecorder()
	FilesCompleteHandler(w, filesRequest(t, http.MethodPost, "/files/not-a-uuid/complete", nil, true), "not-a-uuid")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// --- DELETE /files/{file_id} ---

func TestFilesDeleteHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	fs := setupFakeStorage(t)
	expectTokensValid(mock, testUserID)
	objectKey := "pet_photo/" + testPetID + "/" + testFileID
	now := time.Now()
	expectGetFileByID(mock, testFileID, testPetID, testUserID, objectKey, now)
	expectPetOwnership(mock, testPetID, testUserID, 1)
	mock.ExpectQuery(`DELETE FROM file WHERE id = \$1 RETURNING object_key`).
		WithArgs(testFileID).
		WillReturnRows(sqlmock.NewRows([]string{"object_key"}).AddRow(objectKey))

	w := httptest.NewRecorder()
	FilesDeleteHandler(w, filesRequest(t, http.MethodDelete, "/files/"+testFileID, nil, true), testFileID)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Contains(t, fs.deletedKeys, objectKey)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFilesDeleteHandler_NotFound(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, owner_type, owner_id, user_id, object_key, content_type, position, confirmed_at, created_at\s+FROM file\s+WHERE id = \$1`).
		WillReturnError(sql.ErrNoRows)

	w := httptest.NewRecorder()
	FilesDeleteHandler(w, filesRequest(t, http.MethodDelete, "/files/"+testFileID, nil, true), testFileID)

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestFilesDeleteHandler_RepeatIsIdempotent411404 — повторный вызов на уже
// удалённый file_id трактуется клиентом как «уже удалено» (та же идиома,
// что и повторный DELETE /pet/{id}), см. «Удаление файла».
func TestFilesDeleteHandler_RepeatReturns404(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, owner_type, owner_id, user_id, object_key, content_type, position, confirmed_at, created_at\s+FROM file\s+WHERE id = \$1`).
		WillReturnError(sql.ErrNoRows)

	w := httptest.NewRecorder()
	FilesDeleteHandler(w, filesRequest(t, http.MethodDelete, "/files/"+testFileID, nil, true), testFileID)
	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFilesDeleteHandler_OwnershipRecheckFails(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	objectKey := "pet_photo/" + testPetID + "/" + testFileID
	now := time.Now()
	expectGetFileByID(mock, testFileID, testPetID, testUserID, objectKey, now)
	expectPetOwnership(mock, testPetID, testUserID, 0)

	w := httptest.NewRecorder()
	FilesDeleteHandler(w, filesRequest(t, http.MethodDelete, "/files/"+testFileID, nil, true), testFileID)

	assert.Equal(t, http.StatusNotFound, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFilesDeleteHandler_InvalidFileID(t *testing.T) {
	w := httptest.NewRecorder()
	FilesDeleteHandler(w, filesRequest(t, http.MethodDelete, "/files/not-a-uuid", nil, true), "not-a-uuid")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// --- routing ---

func TestFilesByIDHandler_RoutesComplete(t *testing.T) {
	w := httptest.NewRecorder()
	FilesByIDHandler(w, filesRequest(t, http.MethodPost, "/files/not-a-uuid/complete", nil, true))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestFilesByIDHandler_RoutesDelete(t *testing.T) {
	w := httptest.NewRecorder()
	FilesByIDHandler(w, filesRequest(t, http.MethodDelete, "/files/not-a-uuid", nil, true))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestFilesByIDHandler_CompleteWrongMethod(t *testing.T) {
	w := httptest.NewRecorder()
	FilesByIDHandler(w, filesRequest(t, http.MethodGet, "/files/"+testFileID+"/complete", nil, true))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestFilesByIDHandler_DeleteWrongMethod(t *testing.T) {
	w := httptest.NewRecorder()
	FilesByIDHandler(w, filesRequest(t, http.MethodGet, "/files/"+testFileID, nil, true))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
