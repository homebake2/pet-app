//go:build integration

// Сквозные сценарии принятия документов: GET/POST /consents и закрытие
// защищённых методов до принятия (403 CONSENT_REQUIRED).
package integration

import (
	"myauthservice/database"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type errorBody struct {
	Code string `json:"code"`
}

func TestConsent_ProtectedMethodsClosedUntilAccepted(t *testing.T) {
	resetDB(t)

	login := "consent-" + uuid.NewString()[:8]
	resp := doRequest(t, http.MethodPost, "/auth/register", map[string]string{"login": login, "password": "password123"}, "")
	require.Equal(t, http.StatusOK, resp.status, "%s", resp.body)
	var auth struct {
		authTokens
		ConsentRequired bool `json:"consent_required"`
	}
	resp.decode(t, &auth)
	require.True(t, auth.ConsentRequired)

	// Защищённый метод закрыт.
	closed := doRequest(t, http.MethodGet, "/profile", nil, auth.AccessToken)
	require.Equal(t, http.StatusForbidden, closed.status, "%s", closed.body)
	var body errorBody
	closed.decode(t, &body)
	require.Equal(t, "CONSENT_REQUIRED", body.Code)

	// Статус документов доступен и показывает, что принимать.
	status := doRequest(t, http.MethodGet, "/consents", nil, auth.AccessToken)
	require.Equal(t, http.StatusOK, status.status, "%s", status.body)
	var parsed struct {
		ConsentRequired bool `json:"consent_required"`
		Documents       []struct {
			DocType  string `json:"doc_type"`
			Version  int    `json:"version"`
			Accepted bool   `json:"accepted"`
		} `json:"documents"`
	}
	status.decode(t, &parsed)
	require.True(t, parsed.ConsentRequired)
	require.Len(t, parsed.Documents, 2)

	// Принятие открывает методы; повторное принятие тоже 204.
	acceptConsents(t, auth.AccessToken)
	acceptConsents(t, auth.AccessToken)
	open := doRequest(t, http.MethodGet, "/profile", nil, auth.AccessToken)
	require.NotEqual(t, http.StatusForbidden, open.status, "%s", open.body)

	// Повторный вход: документы уже приняты.
	again := doRequest(t, http.MethodPost, "/auth/login", map[string]string{"login": login, "password": "password123"}, "")
	require.Equal(t, http.StatusOK, again.status)
	var againAuth struct {
		ConsentRequired bool `json:"consent_required"`
	}
	again.decode(t, &againAuth)
	require.False(t, againAuth.ConsentRequired)
}

// Тексты документов доступны без авторизации на обоих языках.
func TestConsentDocuments_PublicAndLocalized(t *testing.T) {
	resetDB(t)

	for _, lang := range []string{"ru", "en"} {
		resp := doRequest(t, http.MethodGet, "/consent-documents?language="+lang, nil, "")
		require.Equal(t, http.StatusOK, resp.status, "%s", resp.body)
		var parsed struct {
			Documents []struct {
				DocType  string `json:"doc_type"`
				Version  int    `json:"version"`
				Language string `json:"language"`
				Text     string `json:"text"`
			} `json:"documents"`
		}
		resp.decode(t, &parsed)
		require.Len(t, parsed.Documents, 2)
		for _, doc := range parsed.Documents {
			require.Equal(t, lang, doc.Language)
			require.Equal(t, 1, doc.Version)
			require.NotEmpty(t, doc.Text)
		}
	}

	// Без параметра — русский.
	def := doRequest(t, http.MethodGet, "/consent-documents", nil, "")
	require.Equal(t, http.StatusOK, def.status)
	require.Contains(t, string(def.body), `"language":"ru"`)
}

func TestConsent_VersionBumpAsksEveryoneAgain(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, "bump-"+uuid.NewString()[:8], "password123")

	ok := doRequest(t, http.MethodGet, "/profile", nil, tokens.AccessToken)
	require.NotEqual(t, http.StatusForbidden, ok.status)

	// Версию повышают в базе, без выпуска новой версии приложения.
	_, err := database.DB.Exec(`UPDATE consent_document SET version = version + 1 WHERE doc_type = 'personal_data'`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = database.DB.Exec(`UPDATE consent_document SET version = 1`)
	})

	closed := doRequest(t, http.MethodGet, "/profile", nil, tokens.AccessToken)
	require.Equal(t, http.StatusForbidden, closed.status, "%s", closed.body)

	// Старая версия текста уже не принимается.
	stale := doRequest(t, http.MethodPost, "/consents", map[string]any{"documents": []map[string]any{
		{"doc_type": "terms", "version": 1},
		{"doc_type": "personal_data", "version": 1},
	}}, tokens.AccessToken)
	require.Equal(t, http.StatusConflict, stale.status, "%s", stale.body)
	var body errorBody
	stale.decode(t, &body)
	require.Equal(t, "CONSENT_VERSION_MISMATCH", body.Code)

	acceptConsents(t, tokens.AccessToken)
	reopened := doRequest(t, http.MethodGet, "/profile", nil, tokens.AccessToken)
	require.NotEqual(t, http.StatusForbidden, reopened.status, "%s", reopened.body)
}

// Без принятия документов доступны выход и удаление аккаунта.
func TestConsent_LogoutAndDeleteAccountAllowedBeforeAccept(t *testing.T) {
	resetDB(t)
	tokens := registerUserWithoutConsent(t, "decline-"+uuid.NewString()[:8], "password123")

	deleted := doRequest(t, http.MethodDelete, "/profile", nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, deleted.status, "%s", deleted.body)

	var count int
	require.NoError(t, database.DB.QueryRow(`SELECT count(*) FROM users`).Scan(&count))
	require.Zero(t, count)
}
