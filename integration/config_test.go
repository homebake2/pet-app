//go:build integration

package integration

import (
	"net/http"
	"testing"

	"myauthservice/database"

	"github.com/stretchr/testify/require"
)

type configBody struct {
	AboutMeURL string `json:"aboutMeUrl"`
}

// app_config не входит в TRUNCATE из resetDB, поэтому тесты возвращают
// строку к посеву миграции сами.
func resetAppConfig(t *testing.T) {
	t.Helper()
	_, err := database.DB.Exec(`UPDATE app_config SET about_me_url = 'https://example.com' WHERE id`)
	require.NoError(t, err)
}

// GET /config публичный: без Authorization отдаёт посев миграции, ответ
// сверяется со спекой.
func TestConfig_PublicReturnsSeededAboutMeURL(t *testing.T) {
	resetAppConfig(t)

	resp := doRequest(t, http.MethodGet, "/config", nil, "")
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)

	var body configBody
	resp.decode(t, &body)
	require.Equal(t, "https://example.com", body.AboutMeURL)
}

// Значение читается на каждый запрос: UPDATE в БД виден без перезапуска.
func TestConfig_ReflectsDBChangeWithoutRestart(t *testing.T) {
	resetAppConfig(t)
	t.Cleanup(func() { resetAppConfig(t) })

	_, err := database.DB.Exec(`UPDATE app_config SET about_me_url = 'https://example.org/about' WHERE id`)
	require.NoError(t, err)

	resp := doRequest(t, http.MethodGet, "/config", nil, "")
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	var body configBody
	resp.decode(t, &body)
	require.Equal(t, "https://example.org/about", body.AboutMeURL)
}

// Не-https значение отклоняется CHECK-ограничением БД.
func TestConfig_DBRejectsNonHTTPSURL(t *testing.T) {
	resetAppConfig(t)

	_, err := database.DB.Exec(`UPDATE app_config SET about_me_url = 'http://example.com' WHERE id`)
	require.Error(t, err)
}
