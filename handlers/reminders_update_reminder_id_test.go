package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"myauthservice/models"
)

func updateReminderPlanRequest(t *testing.T, body any) *http.Request {
	t.Helper()
	r := doRequest(http.MethodPatch, "/reminder-plans/"+uuid.NewString()+"?tz=UTC", body)
	r.Header.Set("Authorization", "Bearer "+validAccessToken(t, testUserID))
	return r
}

func TestUpdateReminderPlanHandler_InvalidReminderID(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	UpdateReminderPlanHandler(w, updateReminderPlanRequest(t, map[string]any{
		"notes": "x", "reminder_id": "not-a-uuid",
	}), uuid.New())

	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "VALIDATION_ERROR")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateReminderPlanHandler_ReminderIDAloneIsNotAnUpdateField(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)

	w := httptest.NewRecorder()
	UpdateReminderPlanHandler(w, updateReminderPlanRequest(t, map[string]any{
		"reminder_id": uuid.NewString(),
	}), uuid.New())

	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "хотя бы одно поле")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSuccessorReminderID(t *testing.T) {
	base := time.Date(2030, 1, 1, 8, 0, 0, 0, time.UTC)
	moments := []models.ReminderMoment{
		{RemindAt: base.Add(48 * time.Hour)},
		{RemindAt: base},
		{RemindAt: base.Add(24 * time.Hour)},
	}
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}

	t.Run("момент совпал", func(t *testing.T) {
		got := successorReminderID(moments, ids, base.Add(24*time.Hour))
		require.NotNil(t, got)
		assert.Equal(t, ids[2], *got)
	})
	t.Run("момент совпал в другом часовом поясе", func(t *testing.T) {
		got := successorReminderID(moments, ids, base.In(time.FixedZone("x", 3*3600)))
		require.NotNil(t, got)
		assert.Equal(t, ids[1], *got)
	})
	t.Run("не совпал - первое по remind_at", func(t *testing.T) {
		got := successorReminderID(moments, ids, base.Add(time.Hour))
		require.NotNil(t, got)
		assert.Equal(t, ids[1], *got)
	})
	t.Run("нет созданных", func(t *testing.T) {
		assert.Nil(t, successorReminderID(nil, nil, base))
	})
}
