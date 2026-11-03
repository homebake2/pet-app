package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testWebAPIKey    = "test-web-key"
	suggestionRowID  = "3f2b8c1e-5d4a-4c6e-9b7a-1a2b3c4d5e6f"
	suggestionPath   = "/web/suggestions"
	suggestionInsert = `INSERT INTO suggestion`
)

func webRequest(t *testing.T, body any, key string) *http.Request {
	t.Helper()
	r := doRequest(http.MethodPost, suggestionPath, body)
	if key != "" {
		r.Header.Set("X-Api-Key", key)
	}
	return r
}

func serveWeb(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	NewMux().ServeHTTP(w, r)
	return w
}

func validSuggestion() map[string]any {
	return map[string]any{
		"userId":   "local",
		"category": "pets",
		"title":    "График веса по всем питомцам",
		"consent":  true,
	}
}

func TestWebSuggestion_DisabledWithoutConfiguredKey(t *testing.T) {
	t.Setenv(webAPIKeyEnv, "")
	for _, key := range []string{"", "anything"} {
		w := serveWeb(webRequest(t, validSuggestion(), key))
		assert.Equal(t, http.StatusNotFound, w.Code)
	}
}

func TestWebSuggestion_RejectsMissingOrWrongKey(t *testing.T) {
	t.Setenv(webAPIKeyEnv, testWebAPIKey)
	for _, key := range []string{"", "wrong"} {
		w := serveWeb(webRequest(t, validSuggestion(), key))
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), `"UNAUTHORIZED"`)
	}
}

func TestWebSuggestion_MethodNotAllowed(t *testing.T) {
	t.Setenv(webAPIKeyEnv, testWebAPIKey)
	r := doRequest(http.MethodGet, suggestionPath, nil)
	r.Header.Set("X-Api-Key", testWebAPIKey)
	assert.Equal(t, http.StatusMethodNotAllowed, serveWeb(r).Code)
}

func TestWebSuggestion_Created(t *testing.T) {
	t.Setenv(webAPIKeyEnv, testWebAPIKey)
	mock := setupMockDB(t)
	mock.ExpectQuery(suggestionInsert).
		WithArgs("local", "pets", "График веса", "подробности", "me@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(suggestionRowID))

	body := validSuggestion()
	body["title"] = "  График веса  "
	body["body"] = "подробности"
	body["contact"] = " me@example.com "
	w := serveWeb(webRequest(t, body, testWebAPIKey))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), suggestionRowID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWebSuggestion_OptionalFieldsDefaultToEmpty(t *testing.T) {
	t.Setenv(webAPIKeyEnv, testWebAPIKey)
	mock := setupMockDB(t)
	mock.ExpectQuery(suggestionInsert).
		WithArgs("anonymous", "other", "Идея", "", "").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(suggestionRowID))

	body := map[string]any{"userId": "anonymous", "category": "other", "title": "Идея", "consent": true}
	w := serveWeb(webRequest(t, body, testWebAPIKey))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWebSuggestion_TitleLimitCountsRunes(t *testing.T) {
	t.Setenv(webAPIKeyEnv, testWebAPIKey)
	mock := setupMockDB(t)
	title := strings.Repeat("я", suggestionTitleMax)
	mock.ExpectQuery(suggestionInsert).
		WithArgs("local", "pets", title, "", "").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(suggestionRowID))

	body := validSuggestion()
	body["title"] = title
	w := serveWeb(webRequest(t, body, testWebAPIKey))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
}

func TestWebSuggestion_ValidationErrors(t *testing.T) {
	t.Setenv(webAPIKeyEnv, testWebAPIKey)

	cases := map[string]struct {
		mutate func(map[string]any)
		field  string
	}{
		"пустой userId":        {func(b map[string]any) { b["userId"] = "" }, "userId"},
		"userId с пробелом":    {func(b map[string]any) { b["userId"] = "a b" }, "userId"},
		"неизвестный раздел":   {func(b map[string]any) { b["category"] = "nope" }, "category"},
		"пустой заголовок":     {func(b map[string]any) { b["title"] = "   " }, "title"},
		"длинный заголовок":    {func(b map[string]any) { b["title"] = strings.Repeat("я", suggestionTitleMax+1) }, "title"},
		"длинное описание":     {func(b map[string]any) { b["body"] = strings.Repeat("я", suggestionBodyMax+1) }, "body"},
		"длинный контакт":      {func(b map[string]any) { b["contact"] = strings.Repeat("я", suggestionContactMax+1) }, "contact"},
		"нет согласия":         {func(b map[string]any) { b["consent"] = false }, "consent"},
		"согласие не передано": {func(b map[string]any) { delete(b, "consent") }, "consent"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mock := setupMockDB(t)
			body := validSuggestion()
			tc.mutate(body)
			w := serveWeb(webRequest(t, body, testWebAPIKey))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), `"VALIDATION_ERROR"`)
			assert.Contains(t, w.Body.String(), `"path":"`+tc.field+`"`)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestWebSuggestion_InvalidJSON(t *testing.T) {
	t.Setenv(webAPIKeyEnv, testWebAPIKey)
	r := httptest.NewRequest(http.MethodPost, suggestionPath, strings.NewReader("{"))
	r.Header.Set("X-Api-Key", testWebAPIKey)
	w := serveWeb(r)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"BAD_REQUEST"`)
}

func TestWebSuggestion_DBError(t *testing.T) {
	t.Setenv(webAPIKeyEnv, testWebAPIKey)
	mock := setupMockDB(t)
	mock.ExpectQuery(suggestionInsert).WillReturnError(assertError)

	w := serveWeb(webRequest(t, validSuggestion(), testWebAPIKey))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), `"INTERNAL_ERROR"`)
}
