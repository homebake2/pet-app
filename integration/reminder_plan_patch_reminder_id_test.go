//go:build integration

package integration

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// patchPlanReminderID отправляет PATCH /reminder-plans/{id} и возвращает
// reminder_id из ответа (nil — null); ответ проверяется по спеке.
func patchPlanReminderID(t *testing.T, token, planID string, body map[string]any) *string {
	t.Helper()
	resp := doRequest(t, http.MethodPatch, "/reminder-plans/"+planID+"?tz=UTC", body, token)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	var out map[string]*string
	resp.decode(t, &out)
	value, present := out["reminder_id"]
	require.Truef(t, present, "в ответе нет поля reminder_id: %s", resp.body)
	return value
}

func createDailyMedicationPlan(t *testing.T, token, petID string, days int) planBody {
	t.Helper()
	return createPlan(t, token, map[string]any{
		"pet_ids": []string{petID}, "type": "medication", "value": map[string]any{"name": "Нурофен"},
		"frequency_type": "daily", "times": []string{"08:00"},
		"start_date": futureDate(1), "end_date": futureDate(days),
	})
}

func TestReminderPlan_PatchReminderID_ScheduleUnchanged(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	plan := createDailyMedicationPlan(t, tokens.AccessToken, petID, 3)
	require.Len(t, plan.Reminders, 3)
	target := plan.Reminders[1].ID

	// Меняются только данные: расписание то же, напоминание сохраняется.
	got := patchPlanReminderID(t, tokens.AccessToken, plan.ID, map[string]any{
		"notes": "после еды", "reminder_id": target,
	})
	require.NotNil(t, got)
	require.Equal(t, target, *got)

	// То же расписание целиком: напоминания не пересоздаются.
	got = patchPlanReminderID(t, tokens.AccessToken, plan.ID, map[string]any{
		"frequency_type": "daily", "times": []string{"08:00"},
		"start_date": futureDate(1), "end_date": futureDate(3), "reminder_id": target,
	})
	require.NotNil(t, got)
	require.Equal(t, target, *got)
}

func TestReminderPlan_PatchReminderID_OnceMoved(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	plan := createOncePlan(t, tokens.AccessToken, petID, "weight", map[string]any{"amount": 4.2}, futureDate(3), "08:00")
	require.Len(t, plan.Reminders, 1)
	old := plan.Reminders[0].ID

	got := patchPlanReminderID(t, tokens.AccessToken, plan.ID, map[string]any{
		"frequency_type": "once", "times": []string{"09:30"}, "start_date": futureDate(5), "reminder_id": old,
	})
	require.NotNil(t, got)
	require.NotEqual(t, old, *got)

	changed := getReminderPlan(t, tokens.AccessToken, plan.ID)
	require.Len(t, changed.Reminders, 1)
	require.Equal(t, changed.Reminders[0].ID, *got)
	require.Equal(t, futureDate(5)+"T09:30:00Z", changed.Reminders[0].RemindAt)
}

func TestReminderPlan_PatchReminderID_RecurringMatchedMoment(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	plan := createDailyMedicationPlan(t, tokens.AccessToken, petID, 3)
	require.Len(t, plan.Reminders, 3)
	old := plan.Reminders[1]

	// Расписание удлинено: момент старого напоминания есть и в новом.
	got := patchPlanReminderID(t, tokens.AccessToken, plan.ID, map[string]any{
		"frequency_type": "daily", "times": []string{"08:00"},
		"start_date": futureDate(1), "end_date": futureDate(5), "reminder_id": old.ID,
	})
	require.NotNil(t, got)
	require.NotEqual(t, old.ID, *got)

	changed := getReminderPlan(t, tokens.AccessToken, plan.ID)
	require.Len(t, changed.Reminders, 5)
	var found bool
	for _, ref := range changed.Reminders {
		if ref.ID == *got {
			found = true
			require.Equal(t, old.RemindAt, ref.RemindAt)
		}
	}
	require.True(t, found, "id из ответа не найден среди напоминаний настроек")
}

func TestReminderPlan_PatchReminderID_RecurringNoMatchedMoment(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	plan := createDailyMedicationPlan(t, tokens.AccessToken, petID, 3)
	require.Len(t, plan.Reminders, 3)
	old := plan.Reminders[2]

	// Время приёма сменилось: ни один новый момент не совпадает со старым,
	// возвращается первое по remind_at.
	got := patchPlanReminderID(t, tokens.AccessToken, plan.ID, map[string]any{
		"frequency_type": "daily", "times": []string{"09:00"},
		"start_date": futureDate(1), "end_date": futureDate(3), "reminder_id": old.ID,
	})
	require.NotNil(t, got)
	require.NotEqual(t, old.ID, *got)

	changed := getReminderPlan(t, tokens.AccessToken, plan.ID)
	require.Len(t, changed.Reminders, 3)
	require.Equal(t, changed.Reminders[0].ID, *got)
	require.Equal(t, futureDate(1)+"T09:00:00Z", changed.Reminders[0].RemindAt)
}

func TestReminderPlan_PatchReminderID_Null(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	plan := createDailyMedicationPlan(t, tokens.AccessToken, petID, 3)
	other := createOncePlan(t, tokens.AccessToken, petID, "weight", map[string]any{"amount": 5.0}, futureDate(4), "08:00")

	// reminder_id не передан.
	require.Nil(t, patchPlanReminderID(t, tokens.AccessToken, plan.ID, map[string]any{"notes": "x"}))

	// Напоминание других настроек и несуществующее напоминание — не ошибка.
	require.Nil(t, patchPlanReminderID(t, tokens.AccessToken, plan.ID, map[string]any{
		"notes": "y", "reminder_id": other.Reminders[0].ID,
	}))
	require.Nil(t, patchPlanReminderID(t, tokens.AccessToken, plan.ID, map[string]any{
		"notes": "z", "reminder_id": uuid.NewString(),
	}))

	// Закрытое (удалённое) напоминание.
	closed := plan.Reminders[0].ID
	del := doRequest(t, http.MethodDelete, "/reminders/"+closed, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, del.status)
	require.Nil(t, patchPlanReminderID(t, tokens.AccessToken, plan.ID, map[string]any{
		"notes": "w", "reminder_id": closed,
	}))
}

func TestReminderPlan_PatchReminderID_Validation(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	plan := createOncePlan(t, tokens.AccessToken, petID, "weight", map[string]any{"amount": 4.2}, futureDate(3), "08:00")
	path := "/reminder-plans/" + plan.ID + "?tz=UTC"

	// Невалидный uuid — 400 VALIDATIONERROR.
	bad := doUnvalidatedRequest(t, http.MethodPatch, path, map[string]any{"notes": "x", "reminder_id": "not-a-uuid"}, tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, bad.status, "%s", bad.body)
	var errBody struct {
		Code string `json:"code"`
	}
	bad.decode(t, &errBody)
	require.Equal(t, "VALIDATION_ERROR", errBody.Code)

	// Один reminder_id не считается полем обновления.
	only := doUnvalidatedRequest(t, http.MethodPatch, path, map[string]any{"reminder_id": plan.Reminders[0].ID}, tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, only.status, "%s", only.body)
}
