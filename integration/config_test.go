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
	AdsEnabled bool   `json:"adsEnabled"`
}

// app_config не входит в TRUNCATE из resetDB, поэтому тесты возвращают
// строку к посеву миграции сами.
func resetAppConfig(t *testing.T) {
	t.Helper()
	_, err := database.DB.Exec(`UPDATE app_config SET about_me_url = 'https://example.com', ads_enabled = false WHERE id`)
	require.NoError(t, err)
}

// adsEnabled читается на каждый запрос: UPDATE в БД виден без перезапуска.
func TestConfig_AdsEnabledReflectsDBChange(t *testing.T) {
	resetAppConfig(t)
	t.Cleanup(func() { resetAppConfig(t) })

	resp := doRequest(t, http.MethodGet, "/config", nil, "")
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	var body configBody
	resp.decode(t, &body)
	require.False(t, body.AdsEnabled)

	_, err := database.DB.Exec(`UPDATE app_config SET ads_enabled = true WHERE id`)
	require.NoError(t, err)

	resp = doRequest(t, http.MethodGet, "/config", nil, "")
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	resp.decode(t, &body)
	require.True(t, body.AdsEnabled)
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

// /healthz публичный и вне OpenAPI-контракта: при доступной БД — 200.
func TestHealthz_OKWithDatabase(t *testing.T) {
	resp := doRawRequest(t, http.MethodGet, "/healthz", nil, nil)
	require.Equal(t, http.StatusOK, resp.status)
	require.JSONEq(t, `{"status":"ok"}`, string(resp.body))
}
