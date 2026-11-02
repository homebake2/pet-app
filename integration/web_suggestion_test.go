//go:build integration

// Сквозные сценарии методов сайта: POST /web/suggestions
// (open-api/web-spec.json).
package integration

import (
	"myauthservice/database"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWebSuggestion_Create(t *testing.T) {
	resetDB(t)

	resp := doRequestAgainstSpec(t, webSpec, "open-api/web-spec.json", http.MethodPost, "/web/suggestions",
		map[string]any{
			"userId":   "local",
			"category": "reminders",
			"title":    "  Напоминание о вакцинации  ",
			"body":     "Подробнее",
			"contact":  "@me",
		}, "", map[string]string{"X-Api-Key": testWebAPIKey})
	require.Equal(t, http.StatusCreated, resp.status, "%s", resp.body)

	var created struct {
		ID string `json:"id"`
	}
	resp.decode(t, &created)

	var userID, category, title, body, contact string
	err := database.DB.QueryRow(`SELECT user_id, category, title, body, contact FROM suggestion WHERE id = $1`, created.ID).
		Scan(&userID, &category, &title, &body, &contact)
	require.NoError(t, err)
	require.Equal(t, []string{"local", "reminders", "Напоминание о вакцинации", "Подробнее", "@me"},
		[]string{userID, category, title, body, contact})
}

func TestWebSuggestion_AccessAndValidation(t *testing.T) {
	resetDB(t)
	valid := `{"userId":"local","category":"pets","title":"x"}`
	post := func(headers map[string]string, body string) apiResponse {
		return doRawRequest(t, http.MethodPost, "/web/suggestions", []byte(body), headers)
	}

	require.Equal(t, http.StatusUnauthorized, post(nil, valid).status)
	require.Equal(t, http.StatusUnauthorized, post(map[string]string{"X-Api-Key": "nope"}, valid).status)
	// Админский ключ не подходит: нужен именно X-Api-Key.
	require.Equal(t, http.StatusUnauthorized, post(map[string]string{"X-Admin-Key": testAdminKey}, valid).status)

	bad := post(map[string]string{"X-Api-Key": testWebAPIKey}, `{"userId":"local","category":"nope","title":""}`)
	require.Equal(t, http.StatusBadRequest, bad.status, "%s", bad.body)

	var count int
	require.NoError(t, database.DB.QueryRow(`SELECT count(*) FROM suggestion`).Scan(&count))
	require.Zero(t, count)

	t.Setenv("WEB_API_KEY", "")
	require.Equal(t, http.StatusNotFound, post(map[string]string{"X-Api-Key": ""}, valid).status)
}
