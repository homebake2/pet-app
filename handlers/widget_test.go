package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const widgetEventCountQuery = `SELECT ep\.pet_id, e\.type, COUNT\(\*\)\s+FROM event e\s+JOIN event_pet ep`

type widgetResponseBody struct {
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

func TestGetWidgetHandler_RequiresTimeZone(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	GetWidgetHandler(w, eventRequest(t, http.MethodGet, "/widget", nil, true))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetWidgetHandler_Unauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	GetWidgetHandler(w, eventRequest(t, http.MethodGet, "/widget?tz=Europe/Moscow", nil, false))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetWidgetHandler_MethodNotAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	GetWidgetHandler(w, eventRequest(t, http.MethodPost, "/widget?tz=Europe/Moscow", nil, true))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestGetWidgetHandler_NoPets(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, breed, species FROM pet`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "breed", "species"}))

	w := httptest.NewRecorder()
	GetWidgetHandler(w, eventRequest(t, http.MethodGet, "/widget?tz=Europe/Moscow", nil, true))

	require.Equalf(t, http.StatusOK, w.Code, "%s", w.Body.String())
	assert.JSONEq(t, `{"items": []}`, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// Кнопки сортируются по частоте типа за окно, при равенстве — по порядку
// справочника; напоминания и фото приходят в том же ответе.
func TestGetWidgetHandler_Success(t *testing.T) {
	mock := setupMockDB(t)
	setupFakeStorage(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(`SELECT id, name, breed, species FROM pet`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "breed", "species"}).
			AddRow(testPetID, "Tom", "Siamese", "CAT"))
	fileID := "55555555-5555-5555-5555-555555555555"
	mock.ExpectQuery(`FROM file\s+WHERE owner_type = \$1 AND owner_id = ANY\(\$2\) AND confirmed_at IS NOT NULL`).
		WillReturnRows(sqlmock.NewRows(fileColumns).AddRow(
			fileID, "pet_photo", testPetID, testUserID, "pet_photo/"+testPetID+"/"+fileID, "image/jpeg", nil, nil, time.Now(), time.Now(),
		))
	mock.ExpectQuery(widgetEventCountQuery).
		WithArgs(testUserID, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"pet_id", "type", "count"}).
			AddRow(testPetID, "vomit", 5).
			AddRow(testPetID, "mood", 2).
			AddRow(testPetID, "weight", 9))
	reminderID := "55555555-5555-4555-8555-555555555556"
	planID := "66666666-6666-4666-8666-666666666666"
	mock.ExpectQuery(reminderCalendarQuery).
		WithArgs(testUserID, sqlmock.AnyArg(), 3, testPetID).
		WillReturnRows(sqlmock.NewRows(reminderCalendarColumns).
			AddRow(reminderID, planID, time.Date(2030, 1, 1, 8, 0, 0, 0, time.UTC), nil, nil, "feeding", []byte(`{}`), "manual", nil, 1))
	mock.ExpectQuery(`SELECT link\.plan_id, pet\.id, pet\.name, pet\.species\s+FROM reminder_plan_pet link`).
		WillReturnRows(sqlmock.NewRows([]string{"plan_id", "id", "name", "species"}).AddRow(planID, testPetID, "Tom", "CAT"))

	w := httptest.NewRecorder()
	GetWidgetHandler(w, eventRequest(t, http.MethodGet, "/widget?tz=Europe/Moscow", nil, true))

	require.Equalf(t, http.StatusOK, w.Code, "%s", w.Body.String())
	var resp widgetResponseBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
	item := resp.Items[0]
	assert.Equal(t, "Tom", item.Name)
	assert.Equal(t, "CAT", item.Species)
	require.NotNil(t, item.PhotoURL)
	assert.Equal(t, fakePresignedGetURL, *item.PhotoURL)

	require.Len(t, item.UpcomingReminders, 1)
	assert.Equal(t, "feeding", item.UpcomingReminders[0].Type)
	assert.Equal(t, "2030-01-01T08:00:00Z", item.UpcomingReminders[0].RemindAt)

	require.GreaterOrEqual(t, len(item.QuickActions), 2)
	assert.LessOrEqual(t, len(item.QuickActions), 6)
	assert.Equal(t, "vomit", item.QuickActions[0].Type)
	assert.Equal(t, map[string]string{"status": "normal"}, item.QuickActions[0].Value)
	assert.Equal(t, "mood", item.QuickActions[1].Type)
	assert.Equal(t, map[string]string{"state": "calm"}, item.QuickActions[1].Value)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWidgetQuickActions_TieKeepsDictionaryOrderAndLimit(t *testing.T) {
	actions := widgetQuickActions("OTHER", nil)
	require.Len(t, actions, widgetQuickActionsLimit)

	types := make([]string, 0, len(actions))
	for _, a := range actions {
		types = append(types, a.Type)
	}
	// Без частот порядок — порядок справочника типов.
	assert.Equal(t, []string{"mood", "urine", "defecation", "vomit", "diarrhea", "molting"}, types)

	boosted := widgetQuickActions("OTHER", map[string]int{"heat_cycle": 3})
	assert.Equal(t, "heat_cycle", boosted[0].Type)
}

func TestWidgetQuickActions_SpeciesWithoutCandidates(t *testing.T) {
	actions := widgetQuickActions("ANT_FARM", map[string]int{})
	assert.NotNil(t, actions)
	for _, a := range actions {
		assert.NotEmpty(t, a.Type)
	}
}
