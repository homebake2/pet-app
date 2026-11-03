package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"myauthservice/models"
)

func consentRequest(t *testing.T, method, userID, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, "/consents", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+validAccessToken(t, userID))
	return r
}

// Пользователь без принятых документов может запросить статус, хотя защищённые
// методы ему закрыты.
func TestGetConsents_NotAccepted(t *testing.T) {
	mock := setupMockDB(t)
	expectConsentNotAccepted(mock, "user-1")
	mock.ExpectQuery(`FROM consent_document d`).
		WithArgs("user-1").
		WillReturnRows(sqlmock.NewRows([]string{"doc_type", "version", "accepted"}).
			AddRow("personal_data", 2, false).
			AddRow("terms", 1, true))

	w := httptest.NewRecorder()
	ConsentsHandler(w, consentRequest(t, http.MethodGet, "user-1", ""))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp models.ConsentsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.ConsentRequired)
	require.Len(t, resp.Documents, 2)
	assert.Equal(t, "personal_data", resp.Documents[0].DocType)
	assert.Equal(t, 2, resp.Documents[0].Version)
	assert.False(t, resp.Documents[0].Accepted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetConsents_AllAccepted(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, "user-1")
	mock.ExpectQuery(`FROM consent_document d`).
		WithArgs("user-1").
		WillReturnRows(sqlmock.NewRows([]string{"doc_type", "version", "accepted"}).
			AddRow("personal_data", 1, true).
			AddRow("terms", 1, true))

	w := httptest.NewRecorder()
	ConsentsHandler(w, consentRequest(t, http.MethodGet, "user-1", ""))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp models.ConsentsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.ConsentRequired)
}

func TestGetConsents_Unauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	ConsentsHandler(w, httptest.NewRequest(http.MethodGet, "/consents", nil))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestConsents_MethodNotAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	ConsentsHandler(w, httptest.NewRequest(http.MethodPut, "/consents", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestGetConsentDocuments_DefaultLanguageIsRussian(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(`FROM consent_document d\s+JOIN consent_document_text t`).
		WithArgs("ru").
		WillReturnRows(sqlmock.NewRows([]string{"doc_type", "version", "text"}).
			AddRow("personal_data", 3, "Политика").
			AddRow("terms", 2, "Условия"))

	w := httptest.NewRecorder()
	ConsentDocumentsHandler(w, httptest.NewRequest(http.MethodGet, "/consent-documents", nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp models.ConsentDocumentsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Documents, 2)
	assert.Equal(t, "personal_data", resp.Documents[0].DocType)
	assert.Equal(t, 3, resp.Documents[0].Version)
	assert.Equal(t, "ru", resp.Documents[0].Language)
	assert.Equal(t, "Политика", resp.Documents[0].Text)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetConsentDocuments_English(t *testing.T) {
	mock := setupMockDB(t)
	mock.ExpectQuery(`FROM consent_document d\s+JOIN consent_document_text t`).
		WithArgs("en").
		WillReturnRows(sqlmock.NewRows([]string{"doc_type", "version", "text"}).AddRow("terms", 1, "Terms"))

	w := httptest.NewRecorder()
	ConsentDocumentsHandler(w, httptest.NewRequest(http.MethodGet, "/consent-documents?language=en", nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetConsentDocuments_UnknownLanguage(t *testing.T) {
	w := httptest.NewRecorder()
	ConsentDocumentsHandler(w, httptest.NewRequest(http.MethodGet, "/consent-documents?language=de", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "VALIDATION_ERROR")
}

func TestGetConsentDocuments_MethodNotAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	ConsentDocumentsHandler(w, httptest.NewRequest(http.MethodPost, "/consent-documents", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

const validConsentBody = `{"documents":[{"doc_type":"terms","version":1},{"doc_type":"personal_data","version":1}]}`

func TestPostConsents_Accepts(t *testing.T) {
	mock := setupMockDB(t)
	expectConsentNotAccepted(mock, "user-1")
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT doc_type, version FROM consent_document`).
		WillReturnRows(sqlmock.NewRows([]string{"doc_type", "version"}).AddRow("terms", 1).AddRow("personal_data", 1))
	mock.ExpectExec(`INSERT INTO user_consent`).WithArgs("user-1", "terms", 1).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO user_consent`).WithArgs("user-1", "personal_data", 1).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	w := httptest.NewRecorder()
	ConsentsHandler(w, consentRequest(t, http.MethodPost, "user-1", validConsentBody))

	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostConsents_StaleVersionReturns409(t *testing.T) {
	mock := setupMockDB(t)
	expectConsentNotAccepted(mock, "user-1")
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT doc_type, version FROM consent_document`).
		WillReturnRows(sqlmock.NewRows([]string{"doc_type", "version"}).AddRow("terms", 2).AddRow("personal_data", 1))
	mock.ExpectCommit()

	w := httptest.NewRecorder()
	ConsentsHandler(w, consentRequest(t, http.MethodPost, "user-1", validConsentBody))

	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "CONSENT_VERSION_MISMATCH")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostConsents_Validation(t *testing.T) {
	cases := map[string]string{
		"один документ":    `{"documents":[{"doc_type":"terms","version":1}]}`,
		"дубликат":         `{"documents":[{"doc_type":"terms","version":1},{"doc_type":"terms","version":1}]}`,
		"неизвестный тип":  `{"documents":[{"doc_type":"terms","version":1},{"doc_type":"other","version":1}]}`,
		"нулевая версия":   `{"documents":[{"doc_type":"terms","version":0},{"doc_type":"personal_data","version":1}]}`,
		"пустой документы": `{"documents":[]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			mock := setupMockDB(t)
			expectConsentNotAccepted(mock, "user-1")

			w := httptest.NewRecorder()
			ConsentsHandler(w, consentRequest(t, http.MethodPost, "user-1", body))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), "VALIDATION_ERROR")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestPostConsents_InvalidJSON(t *testing.T) {
	mock := setupMockDB(t)
	expectConsentNotAccepted(mock, "user-1")

	w := httptest.NewRecorder()
	ConsentsHandler(w, consentRequest(t, http.MethodPost, "user-1", `{`))

	require.Equal(t, http.StatusBadRequest, w.Code)
}
