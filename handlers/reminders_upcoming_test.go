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

// GET /reminders/upcoming возвращает в каждом элементе value настроек
// напоминания (у дополнительных напоминаний вакцинации — name и
// vaccination_at).
func TestGetUpcomingRemindersHandler_ReturnsValue(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	reminderID := "55555555-5555-4555-8555-555555555555"
	planID := "66666666-6666-4666-8666-666666666666"
	mock.ExpectQuery(reminderCalendarQuery).
		WithArgs(testUserID, sqlmock.AnyArg(), 64).
		WillReturnRows(sqlmock.NewRows(reminderCalendarColumns).
			AddRow(reminderID, planID, time.Date(2030, 1, 1, 8, 0, 0, 0, time.UTC), nil, nil, "vaccination",
				[]byte(`{"name":"Rabies","vaccination_at":"2030-02-01T10:00:00Z"}`), "vaccination_extra", "Rabies", 1, testUserID))
	mock.ExpectQuery(`SELECT link\.plan_id, pet\.id, pet\.name, pet\.species\s+FROM reminder_plan_pet link`).
		WillReturnRows(sqlmock.NewRows([]string{"plan_id", "id", "name", "species"}).
			AddRow(planID, testPetID, "Rex", "DOG"))

	w := httptest.NewRecorder()
	GetUpcomingRemindersHandler(w, eventRequest(t, http.MethodGet, "/reminders/upcoming", nil, true))

	require.Equalf(t, http.StatusOK, w.Code, "%s", w.Body.String())
	var resp struct {
		Items []struct {
			ID    string `json:"id"`
			Type  string `json:"type"`
			Value struct {
				Name          string `json:"name"`
				VaccinationAt string `json:"vaccination_at"`
			} `json:"value"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
	assert.Equal(t, reminderID, resp.Items[0].ID)
	assert.Equal(t, "vaccination", resp.Items[0].Type)
	assert.Equal(t, "Rabies", resp.Items[0].Value.Name)
	assert.Equal(t, "2030-02-01T10:00:00Z", resp.Items[0].Value.VaccinationAt)
}
