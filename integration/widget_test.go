//go:build integration

package integration

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type widgetBody struct {
	Items []struct {
		ID                string  `json:"id"`
		Name              string  `json:"name"`
		Species           string  `json:"species"`
		PhotoURL          *string `json:"photo_url"`
		UpcomingReminders []struct {
			Type     string `json:"type"`
			RemindAt string `json:"remind_at"`
		} `json:"upcoming_reminders"`
		QuickActions []struct {
			Type  string            `json:"type"`
			Value map[string]string `json:"value"`
		} `json:"quick_actions"`
	} `json:"items"`
}

// GET /widget: порядок питомцев как у GET /pet, ближайшие напоминания
// питомца и кнопки, отсортированные по частоте событий за 365 дней.
func TestWidget_AggregatesPetsRemindersAndQuickActions(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	cat := createSpeciesPet(t, tokens.AccessToken, "Барсик", "CAT")
	fish := createSpeciesPet(t, tokens.AccessToken, "Немо", "FISH")

	now := time.Now().UTC().Format(time.RFC3339)
	createEvent(t, tokens.AccessToken, cat, now, "urine", map[string]any{"status": "normal"})
	createEvent(t, tokens.AccessToken, cat, now, "urine", map[string]any{"status": "normal"})
	createEvent(t, tokens.AccessToken, cat, now, "mood", map[string]any{"state": "calm"})
	// Событие за пределами окна 365 дней в частоте не участвует.
	old := time.Now().UTC().AddDate(-2, 0, 0).Format(time.RFC3339)
	createEvent(t, tokens.AccessToken, cat, old, "heat_cycle", map[string]any{"phase": "started"})
	createEvent(t, tokens.AccessToken, cat, old, "heat_cycle", map[string]any{"phase": "started"})
	createEvent(t, tokens.AccessToken, cat, old, "heat_cycle", map[string]any{"phase": "started"})

	// Четыре напоминания: в виджет попадают три ближайших по порядку.
	createOncePlan(t, tokens.AccessToken, cat, "feeding", map[string]any{"amount": 50, "unit": "g", "food": "dry"}, futureDate(5), "09:00")
	createOncePlan(t, tokens.AccessToken, cat, "feeding", map[string]any{"amount": 50, "unit": "g", "food": "dry"}, futureDate(2), "09:00")
	createOncePlan(t, tokens.AccessToken, cat, "feeding", map[string]any{"amount": 50, "unit": "g", "food": "dry"}, futureDate(4), "09:00")
	createOncePlan(t, tokens.AccessToken, cat, "feeding", map[string]any{"amount": 50, "unit": "g", "food": "dry"}, futureDate(3), "09:00")

	resp := doRequest(t, http.MethodGet, "/widget?tz=Europe/Moscow", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	var body widgetBody
	resp.decode(t, &body)

	require.Len(t, body.Items, 2)
	require.Equal(t, cat, body.Items[0].ID)
	require.Equal(t, fish, body.Items[1].ID)
	require.Nil(t, body.Items[0].PhotoURL)

	catReminders := body.Items[0].UpcomingReminders
	require.Len(t, catReminders, 3)
	for i, reminder := range catReminders {
		require.Equal(t, "feeding", reminder.Type)
		if i > 0 {
			require.Less(t, catReminders[i-1].RemindAt, reminder.RemindAt)
		}
	}
	require.Empty(t, body.Items[1].UpcomingReminders)

	catActions := body.Items[0].QuickActions
	require.NotEmpty(t, catActions)
	require.LessOrEqual(t, len(catActions), 6)
	require.Equal(t, "urine", catActions[0].Type)
	require.Equal(t, "mood", catActions[1].Type)
	require.Equal(t, map[string]string{"status": "normal"}, catActions[0].Value)
	// heat_cycle применим к кошке, но его события старше окна — частота 0,
	// поэтому он не опережает типы с событиями.
	for i, action := range catActions {
		if action.Type == "heat_cycle" {
			require.Greater(t, i, 1)
		}
	}
}

func TestWidget_RequiresTimeZone(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	resp := doUnvalidatedRequest(t, http.MethodGet, "/widget", nil, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, resp.status)
}

func TestWidget_EmptyForUserWithoutPets(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	resp := doRequest(t, http.MethodGet, "/widget?tz=UTC", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	var body widgetBody
	resp.decode(t, &body)
	require.Empty(t, body.Items)
}
