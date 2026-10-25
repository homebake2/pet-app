//go:build integration

package integration

import (
	"net/http"
	"os"
	"testing"

	"myauthservice/database"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Дополнительные напоминания вакцинации, next_time, completed_at и
// skip_administered_event (см. «Вакцинации — Backend»).

type vaccinationExtraBody struct {
	PlanID string `json:"plan_id"`
	Date   string `json:"date"`
	Time   string `json:"time"`
}

type vaccinationItemBody struct {
	ID                  string                 `json:"id"`
	Name                string                 `json:"name"`
	NextDate            *string                `json:"next_date"`
	NextTime            *string                `json:"next_time"`
	CompletedAt         *string                `json:"completed_at"`
	AdministeredEventID *string                `json:"administered_event_id"`
	NextPlanID          *string                `json:"next_plan_id"`
	ExtraReminders      []vaccinationExtraBody `json:"extra_reminders"`
}

func listVaccinations(t *testing.T, token, petID string) []vaccinationItemBody {
	t.Helper()
	resp := doRequest(t, http.MethodGet, "/pet/"+petID+"/vaccinations", nil, token)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	var body struct {
		Items []vaccinationItemBody `json:"items"`
	}
	resp.decode(t, &body)
	return body.Items
}

func extraReminder(date, timeOfDay string) map[string]any {
	return map[string]any{"date": date, "time": timeOfDay}
}

// createVaccinationWithExtras создаёт вакцинацию с напоминанием на next_date
// (futureDate(30), 10:00) и двумя дополнительными (futureDate(10) 08:00 и
// futureDate(20) 09:00).
func createVaccinationWithExtras(t *testing.T, token, petID string) (vaccinationID string, item vaccinationItemBody) {
	t.Helper()
	resp := doRequest(t, http.MethodPost, "/pet/"+petID+"/vaccinations?tz=UTC", map[string]any{
		"name": "Rabies", "administered_date": "2024-01-01",
		"next_date": futureDate(30), "next_time": "10:00", "add_reminder_on_next": true,
		"extra_reminders": []map[string]any{
			extraReminder(futureDate(20), "09:00"),
			extraReminder(futureDate(10), "08:00"),
		},
	}, token)
	require.Equalf(t, http.StatusCreated, resp.status, "%s", resp.body)
	var created idResponse
	resp.decode(t, &created)
	items := listVaccinations(t, token, petID)
	require.Len(t, items, 1)
	return created.ID, items[0]
}

func TestVaccinationExtras_CreateAndList(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	id, item := createVaccinationWithExtras(t, tokens.AccessToken, petID)
	require.Equal(t, id, item.ID)
	require.NotNil(t, item.NextTime)
	require.Equal(t, "10:00", *item.NextTime)
	require.NotNil(t, item.NextPlanID)
	require.Nil(t, item.CompletedAt)

	// По возрастанию момента, в часовом поясе настроек.
	require.Len(t, item.ExtraReminders, 2)
	require.Equal(t, futureDate(10), item.ExtraReminders[0].Date)
	require.Equal(t, "08:00", item.ExtraReminders[0].Time)
	require.Equal(t, futureDate(20), item.ExtraReminders[1].Date)
	require.Equal(t, "09:00", item.ExtraReminders[1].Time)

	// Основное (source=vaccination) + два дополнительных
	// (source=vaccination_extra) — отдельные настройки с одним напоминанием
	// каждые.
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM reminder_plan WHERE source = 'vaccination' AND source_id = $1`, id))
	require.Equal(t, 2, countRows(t, `SELECT COUNT(*) FROM reminder_plan WHERE source = 'vaccination_extra' AND source_id = $1`, id))
	require.Equal(t, 3, countRows(t, `SELECT COUNT(*) FROM reminder`))
	extra := getReminderPlan(t, tokens.AccessToken, item.ExtraReminders[0].PlanID)
	require.Equal(t, "vaccination_extra", extra.Source)
	require.NotNil(t, extra.SourceTitle)
	require.Equal(t, "Rabies", *extra.SourceTitle)
	require.Equal(t, "once", extra.FrequencyType)
	require.Len(t, extra.Reminders, 1)
	require.Equal(t, futureDate(10)+"T08:00:00Z", extra.Reminders[0].RemindAt)
}

// Дополнительные напоминания не зависят от add_reminder_on_next, а
// skip_administered_event не создаёт факт.
func TestVaccinationExtras_WithoutMainReminderAndSkipFact(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	resp := doRequest(t, http.MethodPost, "/pet/"+petID+"/vaccinations?tz=UTC", map[string]any{
		"name": "Rabies", "administered_date": "2024-01-01", "skip_administered_event": true,
		"next_date":       futureDate(30),
		"extra_reminders": []map[string]any{extraReminder(futureDate(5), "12:00")},
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, resp.status, "%s", resp.body)

	items := listVaccinations(t, tokens.AccessToken, petID)
	require.Len(t, items, 1)
	require.Nil(t, items[0].AdministeredEventID)
	require.Nil(t, items[0].NextPlanID)
	require.NotNil(t, items[0].NextTime)
	require.Equal(t, "00:00", *items[0].NextTime)
	require.Len(t, items[0].ExtraReminders, 1)
	require.Empty(t, listPetEvents(t, tokens.AccessToken, petID))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
}

func TestVaccinationExtras_CreateValidation(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	path := "/pet/" + petID + "/vaccinations?tz=UTC"

	six := make([]map[string]any, 6)
	for i := range six {
		six[i] = extraReminder(futureDate(5+i), "08:00")
	}
	cases := map[string]map[string]any{
		"без next_date": {
			"name": "Rabies", "administered_date": "2024-01-01",
			"extra_reminders": []map[string]any{extraReminder(futureDate(5), "08:00")},
		},
		"более пяти": {
			"name": "Rabies", "administered_date": "2024-01-01", "next_date": futureDate(30), "extra_reminders": six,
		},
		"в прошлом": {
			"name": "Rabies", "administered_date": "2024-01-01", "next_date": futureDate(30),
			"extra_reminders": []map[string]any{extraReminder("2020-01-01", "08:00")},
		},
		"позже следующей вакцинации": {
			"name": "Rabies", "administered_date": "2024-01-01", "next_date": futureDate(30), "next_time": "10:00",
			"extra_reminders": []map[string]any{extraReminder(futureDate(30), "10:01")},
		},
		"совпадение между собой": {
			"name": "Rabies", "administered_date": "2024-01-01", "next_date": futureDate(30),
			"extra_reminders": []map[string]any{extraReminder(futureDate(5), "08:00"), extraReminder(futureDate(5), "08:00")},
		},
		"совпадение с основным": {
			"name": "Rabies", "administered_date": "2024-01-01", "next_date": futureDate(30), "next_time": "10:00",
			"add_reminder_on_next": true,
			"extra_reminders":      []map[string]any{extraReminder(futureDate(30), "10:00")},
		},
	}
	for name, body := range cases {
		// Лимит в пять элементов закреплён и в спецификации, поэтому запрос
		// с шестью проходит мимо валидации контракта.
		send := doRequest
		if name == "более пяти" {
			send = doUnvalidatedRequest
		}
		resp := send(t, http.MethodPost, path, body, tokens.AccessToken)
		require.Equalf(t, http.StatusBadRequest, resp.status, "%s: %s", name, resp.body)
	}
	// Ни прививок, ни фактов, ни настроек.
	require.Empty(t, listVaccinations(t, tokens.AccessToken, petID))
	require.Empty(t, listPetEvents(t, tokens.AccessToken, petID))
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
}

// Повтор с тем же Idempotency-Key не создаёт ни вакцинации, ни напоминаний.
func TestVaccinationExtras_IdempotentCreate(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	body := map[string]any{
		"name": "Rabies", "administered_date": "2024-01-01", "next_date": futureDate(30), "next_time": "10:00",
		"add_reminder_on_next": true,
		"extra_reminders":      []map[string]any{extraReminder(futureDate(10), "08:00")},
	}
	headers := map[string]string{"Idempotency-Key": uuid.NewString()}
	path := "/pet/" + petID + "/vaccinations?tz=UTC"
	first := doRequest(t, http.MethodPost, path, body, tokens.AccessToken, headers)
	require.Equalf(t, http.StatusCreated, first.status, "%s", first.body)
	second := doRequest(t, http.MethodPost, path, body, tokens.AccessToken, headers)
	require.Equalf(t, http.StatusCreated, second.status, "%s", second.body)
	var a, b idResponse
	first.decode(t, &a)
	second.decode(t, &b)
	require.Equal(t, a.ID, b.ID)

	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM vaccination`))
	require.Equal(t, 2, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
	require.Len(t, listPetEvents(t, tokens.AccessToken, petID), 1)
}

// PATCH: полный итоговый набор дополнительных напоминаний — обновление на
// месте по plan_id, создание без plan_id, жёсткое удаление отсутствующих;
// не переданный набор не меняется.
func TestVaccinationExtras_PatchFullSet(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	id, item := createVaccinationWithExtras(t, tokens.AccessToken, petID)
	path := "/vaccinations/" + id + "?tz=UTC"
	keptID, droppedID := item.ExtraReminders[0].PlanID, item.ExtraReminders[1].PlanID

	// Не передан — не трогаются.
	rename := doRequest(t, http.MethodPatch, path, map[string]any{"name": "Rabies 2"}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, rename.status, "%s", rename.body)
	items := listVaccinations(t, tokens.AccessToken, petID)
	require.Len(t, items[0].ExtraReminders, 2)
	// Название дополнительных обновляется вместе с именем.
	renamed := getReminderPlan(t, tokens.AccessToken, keptID)
	require.Equal(t, "vaccination", renamed.Type)
	require.Equal(t, "Rabies 2", renamed.Value.Name)

	// Обновить на месте, добавить новое, убрать второе.
	patch := doRequest(t, http.MethodPatch, path, map[string]any{
		"extra_reminders": []map[string]any{
			{"plan_id": keptID, "date": futureDate(12), "time": "07:30"},
			extraReminder(futureDate(15), "18:00"),
		},
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, patch.status, "%s", patch.body)

	items = listVaccinations(t, tokens.AccessToken, petID)
	require.Len(t, items[0].ExtraReminders, 2)
	require.Equal(t, keptID, items[0].ExtraReminders[0].PlanID, "обновление на месте сохраняет plan_id")
	require.Equal(t, futureDate(12), items[0].ExtraReminders[0].Date)
	require.Equal(t, "07:30", items[0].ExtraReminders[0].Time)
	require.Equal(t, futureDate(15), items[0].ExtraReminders[1].Date)
	require.NotEqual(t, droppedID, items[0].ExtraReminders[1].PlanID)
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder_plan WHERE id = $1`, droppedID))
	moved := getReminderPlan(t, tokens.AccessToken, keptID)
	require.Len(t, moved.Reminders, 1)
	require.Equal(t, futureDate(12)+"T07:30:00Z", moved.Reminders[0].RemindAt)

	// plan_id основного напоминания либо чужой — 400.
	foreign := doRequest(t, http.MethodPatch, path, map[string]any{
		"extra_reminders": []map[string]any{{"plan_id": *item.NextPlanID, "date": futureDate(12), "time": "07:30"}},
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, foreign.status, "%s", foreign.body)
	unknown := doRequest(t, http.MethodPatch, path, map[string]any{
		"extra_reminders": []map[string]any{{"plan_id": uuid.NewString(), "date": futureDate(12), "time": "07:30"}},
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, unknown.status, "%s", unknown.body)

	// Пустой массив удаляет все.
	clear := doRequest(t, http.MethodPatch, path, map[string]any{"extra_reminders": []map[string]any{}}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, clear.status, "%s", clear.body)
	items = listVaccinations(t, tokens.AccessToken, petID)
	require.Empty(t, items[0].ExtraReminders)
	require.NotNil(t, items[0].NextPlanID, "основное напоминание не затронуто")
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
}

// Инвариант «дополнительные не позже следующей вакцинации»: перенос
// next_date раньше любого дополнительного — 400 и ничего не меняется;
// next_date: null удаляет основное и все дополнительные напоминания.
func TestVaccinationExtras_NextMomentInvariantAndClear(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	id, item := createVaccinationWithExtras(t, tokens.AccessToken, petID)
	path := "/vaccinations/" + id + "?tz=UTC"

	tooEarly := doRequest(t, http.MethodPatch, path, map[string]any{"next_date": futureDate(15)}, tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, tooEarly.status, "%s", tooEarly.body)
	tooEarlyTime := doRequest(t, http.MethodPatch, path, map[string]any{"next_date": futureDate(20), "next_time": "08:59"}, tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, tooEarlyTime.status, "%s", tooEarlyTime.body)
	items := listVaccinations(t, tokens.AccessToken, petID)
	require.Equal(t, futureDate(30), *items[0].NextDate)
	require.Equal(t, "10:00", *items[0].NextTime)
	require.Len(t, items[0].ExtraReminders, 2)

	// Допустимый перенос: основное напоминание переносится на месте.
	ok := doRequest(t, http.MethodPatch, path, map[string]any{"next_date": futureDate(40), "next_time": "11:00"}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, ok.status, "%s", ok.body)
	main := getReminderPlan(t, tokens.AccessToken, *item.NextPlanID)
	require.Equal(t, futureDate(40)+"T11:00:00Z", main.Reminders[0].RemindAt)

	// Непустой extra_reminders вместе с next_date: null — 400.
	conflict := doRequest(t, http.MethodPatch, path, map[string]any{
		"next_date": nil, "extra_reminders": []map[string]any{extraReminder(futureDate(10), "08:00")},
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, conflict.status, "%s", conflict.body)
	require.Equal(t, 3, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))

	clear := doRequest(t, http.MethodPatch, path, map[string]any{"next_date": nil}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, clear.status, "%s", clear.body)
	items = listVaccinations(t, tokens.AccessToken, petID)
	require.Nil(t, items[0].NextDate)
	require.Nil(t, items[0].NextTime)
	require.Nil(t, items[0].CompletedAt, "очистка даты пользователем не завершает вакцинацию")
	require.Empty(t, items[0].ExtraReminders)
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder`))
}

// Календарь: перенос дополнительного напоминания не меняет next_date и
// next_time; перенос позже следующей вакцинации либо основного раньше
// дополнительного — 400; замена одного напоминания — 409.
func TestVaccinationExtras_CalendarReschedule(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	_, item := createVaccinationWithExtras(t, tokens.AccessToken, petID)
	extraPath := "/reminder-plans/" + item.ExtraReminders[0].PlanID + "?tz=UTC"
	mainPath := "/reminder-plans/" + *item.NextPlanID + "?tz=UTC"

	once := func(date, timeOfDay string) map[string]any {
		return map[string]any{"frequency_type": "once", "times": []string{timeOfDay}, "start_date": date}
	}

	ok := doRequest(t, http.MethodPatch, extraPath, once(futureDate(11), "06:00"), tokens.AccessToken)
	require.Equalf(t, http.StatusOK, ok.status, "%s", ok.body)
	items := listVaccinations(t, tokens.AccessToken, petID)
	require.Equal(t, futureDate(30), *items[0].NextDate)
	require.Equal(t, "10:00", *items[0].NextTime)
	require.Equal(t, futureDate(11), items[0].ExtraReminders[0].Date)
	require.Equal(t, "06:00", items[0].ExtraReminders[0].Time)

	later := doRequest(t, http.MethodPatch, extraPath, once(futureDate(31), "06:00"), tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, later.status, "%s", later.body)
	sameAsMain := doRequest(t, http.MethodPatch, extraPath, once(futureDate(30), "10:00"), tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, sameAsMain.status, "%s", sameAsMain.body)

	earlierMain := doRequest(t, http.MethodPatch, mainPath, once(futureDate(15), "10:00"), tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, earlierMain.status, "%s", earlierMain.body)

	// Основное — на новую дату и время: next_date и next_time вакцинации.
	moved := doRequest(t, http.MethodPatch, mainPath, once(futureDate(45), "16:20"), tokens.AccessToken)
	require.Equalf(t, http.StatusOK, moved.status, "%s", moved.body)
	items = listVaccinations(t, tokens.AccessToken, petID)
	require.Equal(t, futureDate(45), *items[0].NextDate)
	require.Equal(t, "16:20", *items[0].NextTime)

	// Замена одного напоминания дополнительного — 409.
	extra := getReminderPlan(t, tokens.AccessToken, item.ExtraReminders[0].PlanID)
	detach := doRequest(t, http.MethodPost, "/reminders/"+extra.Reminders[0].ID+"/detach", map[string]any{
		"event": map[string]any{"pet_ids": []string{petID}, "date": "2024-01-01T08:00:00Z", "type": "other", "value": map[string]any{"label": "x"}},
	}, tokens.AccessToken, map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equalf(t, http.StatusConflict, detach.status, "%s", detach.body)
}

// Отметка «выполнено»: дополнительное напоминание создаёт факт и не меняет
// next_date; основное завершает вакцинацию (next_date/next_time очищены,
// completed_at задан, дополнительные удалены); next_date, заданная на
// завершённой, снова делает её активной.
func TestVaccinationExtras_CompleteFlow(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	id, item := createVaccinationWithExtras(t, tokens.AccessToken, petID)

	// Дополнительное напоминание.
	extra := getReminderPlan(t, tokens.AccessToken, item.ExtraReminders[0].PlanID)
	pastReminder(t, extra.Reminders[0].ID)
	done := doRequest(t, http.MethodPost, "/reminders/"+extra.Reminders[0].ID+"/complete", map[string]any{"done": true}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, done.status, "%s", done.body)
	items := listVaccinations(t, tokens.AccessToken, petID)
	require.Equal(t, futureDate(30), *items[0].NextDate)
	require.Nil(t, items[0].CompletedAt)
	require.Len(t, items[0].ExtraReminders, 1)
	require.Len(t, listPetEvents(t, tokens.AccessToken, petID), 2)

	// Удаление дополнительного в календаре убирает его из extra_reminders.
	rest := getReminderPlan(t, tokens.AccessToken, items[0].ExtraReminders[0].PlanID)
	del := doRequest(t, http.MethodDelete, "/reminders/"+rest.Reminders[0].ID, nil, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, del.status, "%s", del.body)
	items = listVaccinations(t, tokens.AccessToken, petID)
	require.Empty(t, items[0].ExtraReminders)
	require.NotNil(t, items[0].NextDate)

	// Добавим дополнительное снова и завершим вакцинацию основным.
	add := doRequest(t, http.MethodPatch, "/vaccinations/"+id+"?tz=UTC", map[string]any{
		"extra_reminders": []map[string]any{extraReminder(futureDate(12), "08:00")},
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, add.status, "%s", add.body)
	main := getReminderPlan(t, tokens.AccessToken, *item.NextPlanID)
	pastReminder(t, main.Reminders[0].ID)
	done = doRequest(t, http.MethodPost, "/reminders/"+main.Reminders[0].ID+"/complete", map[string]any{"done": true}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, done.status, "%s", done.body)

	items = listVaccinations(t, tokens.AccessToken, petID)
	require.Nil(t, items[0].NextDate)
	require.Nil(t, items[0].NextTime)
	require.Nil(t, items[0].NextPlanID)
	require.NotNil(t, items[0].CompletedAt)
	require.Empty(t, items[0].ExtraReminders)
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))

	// Повторный PATCH без next_date вакцинацию не оживляет; с next_date — да.
	rename := doRequest(t, http.MethodPatch, "/vaccinations/"+id+"?tz=UTC", map[string]any{"name": "Rabies 2"}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, rename.status, "%s", rename.body)
	require.NotNil(t, listVaccinations(t, tokens.AccessToken, petID)[0].CompletedAt)

	reopen := doRequest(t, http.MethodPatch, "/vaccinations/"+id+"?tz=UTC", map[string]any{"next_date": futureDate(60)}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, reopen.status, "%s", reopen.body)
	items = listVaccinations(t, tokens.AccessToken, petID)
	require.Nil(t, items[0].CompletedAt)
	require.Equal(t, futureDate(60), *items[0].NextDate)
	require.Equal(t, "00:00", *items[0].NextTime)
}

// Удаление вакцинации и мягкое удаление питомца жёстко удаляют настройки
// дополнительных напоминаний.
func TestVaccinationExtras_DeleteCascades(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	id, _ := createVaccinationWithExtras(t, tokens.AccessToken, petID)
	require.Equal(t, 3, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
	del := doRequest(t, http.MethodDelete, "/vaccinations/"+id, nil, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, del.status, "%s", del.body)
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder`))

	createVaccinationWithExtras(t, tokens.AccessToken, petID)
	require.Equal(t, 3, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
	require.Equal(t, http.StatusNoContent, doRequest(t, http.MethodDelete, "/pet/"+petID, nil, tokens.AccessToken).status)
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder`))
}

// Импорт: next_time, completed_at и extra_reminder_plan_local_ids переносятся;
// настройки дополнительных получают source=vaccination_extra и source_id.
func TestImportLocalData_VaccinationExtrasAndCompletion(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	oncePlan := func(localID, date, timeOfDay string) map[string]any {
		return map[string]any{
			"local_id": localID, "pet_local_ids": []string{"local-cat"}, "type": "vaccination",
			"value": map[string]any{"name": "Rabies"}, "frequency_type": "once",
			"times": []string{timeOfDay}, "start_date": date, "tz": "UTC",
			"reminders": []map[string]any{{"local_id": localID + "-r", "remind_at": date + "T" + timeOfDay + ":00Z"}},
		}
	}
	body := map[string]any{
		"pets":   []map[string]any{{"local_id": "local-cat", "name": "Барсик", "species": "cat"}},
		"events": []map[string]any{},
		"reminder_plans": []map[string]any{
			oncePlan("plan-main", futureDate(30), "10:00"),
			oncePlan("plan-extra-1", futureDate(10), "08:00"),
			oncePlan("plan-extra-2", futureDate(20), "09:00"),
		},
		"vaccinations": []map[string]any{
			{
				"local_id": "vac-active", "pet_local_id": "local-cat", "name": "Rabies", "administered_date": "2024-01-01",
				"next_date": futureDate(30), "next_time": "10:00", "add_reminder_on_next": true,
				"next_reminder_plan_local_id":   "plan-main",
				"extra_reminder_plan_local_ids": []string{"plan-extra-2", "plan-extra-1"},
			},
			{
				"local_id": "vac-done", "pet_local_id": "local-cat", "name": "Distemper", "administered_date": "2023-01-01",
				"completed_at": "2024-03-01T08:00:00Z",
			},
		},
	}
	resp := doRequest(t, http.MethodPost, "/import/local-data", body, tokens.AccessToken, map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)

	var result struct {
		Vaccinations []struct {
			LocalID string `json:"local_id"`
			ID      string `json:"id"`
		} `json:"vaccinations"`
	}
	resp.decode(t, &result)
	require.Len(t, result.Vaccinations, 2)
	activeID := result.Vaccinations[0].ID

	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM reminder_plan WHERE source = 'vaccination' AND source_id = $1`, activeID))
	require.Equal(t, 2, countRows(t, `SELECT COUNT(*) FROM reminder_plan WHERE source = 'vaccination_extra' AND source_id = $1`, activeID))

	pets := doRequest(t, http.MethodGet, "/pet", nil, tokens.AccessToken)
	var petsBody struct {
		Items []struct{ ID string } `json:"items"`
	}
	pets.decode(t, &petsBody)
	items := listVaccinations(t, tokens.AccessToken, petsBody.Items[0].ID)
	require.Len(t, items, 2)

	// Сначала вакцинации с ближайшей next_date, затем без неё.
	require.Equal(t, activeID, items[0].ID)
	require.Equal(t, "10:00", *items[0].NextTime)
	require.NotNil(t, items[0].NextPlanID)
	require.Len(t, items[0].ExtraReminders, 2)
	require.Equal(t, futureDate(10), items[0].ExtraReminders[0].Date)
	require.Equal(t, futureDate(20), items[0].ExtraReminders[1].Date)

	require.Equal(t, "Distemper", items[1].Name)
	require.Nil(t, items[1].NextDate)
	require.NotNil(t, items[1].CompletedAt)
	require.Equal(t, "2024-03-01T08:00:00Z", *items[1].CompletedAt)
	require.Empty(t, items[1].ExtraReminders)
}

// Импорт: недопустимые комбинации — 400, ничего не сохраняется.
func TestImportLocalData_VaccinationExtrasRejected(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	plan := map[string]any{
		"local_id": "plan-1", "pet_local_ids": []string{"local-cat"}, "type": "vaccination",
		"value": map[string]any{"name": "Rabies"}, "frequency_type": "once",
		"times": []string{"10:00"}, "start_date": futureDate(10), "tz": "UTC",
		"reminders": []map[string]any{{"local_id": "rem-1", "remind_at": futureDate(10) + "T10:00:00Z"}},
	}
	vaccination := func(extra map[string]any) map[string]any {
		v := map[string]any{"local_id": "vac-1", "pet_local_id": "local-cat", "name": "Rabies", "administered_date": "2024-01-01"}
		for k, val := range extra {
			v[k] = val
		}
		return v
	}
	cases := map[string]map[string]any{
		"extra без next_date":     vaccination(map[string]any{"extra_reminder_plan_local_ids": []string{"plan-1"}}),
		"next_time без next_date": vaccination(map[string]any{"next_time": "10:00"}),
		"completed_at с next_date": vaccination(map[string]any{
			"next_date": futureDate(30), "completed_at": "2024-03-01T08:00:00Z",
		}),
		"extra включает основное": vaccination(map[string]any{
			"next_date": futureDate(30), "next_reminder_plan_local_id": "plan-1", "extra_reminder_plan_local_ids": []string{"plan-1"},
		}),
		"extra не найден": vaccination(map[string]any{
			"next_date": futureDate(30), "extra_reminder_plan_local_ids": []string{"nope"},
		}),
	}
	for name, vac := range cases {
		body := map[string]any{
			"pets":           []map[string]any{{"local_id": "local-cat", "name": "Барсик", "species": "cat"}},
			"events":         []map[string]any{},
			"reminder_plans": []map[string]any{plan},
			"vaccinations":   []map[string]any{vac},
		}
		resp := doRequest(t, http.MethodPost, "/import/local-data", body, tokens.AccessToken, map[string]string{"Idempotency-Key": uuid.NewString()})
		require.Equalf(t, http.StatusBadRequest, resp.status, "%s: %s", name, resp.body)
	}
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM vaccination`))
}

// Список дня: основное напоминание — plan_source=vaccination, дополнительное —
// plan_source=vaccination_extra; название вакцинации у обоих; type/value и
// питомец связанных настроек не меняются, расписание — только разовое.
func TestVaccinationExtras_PlanSourceInDayListAndLinkedRules(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	_, item := createVaccinationWithExtras(t, tokens.AccessToken, petID)

	day := doRequest(t, http.MethodGet, "/activities/day?date="+futureDate(10)+"&tz=UTC", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, day.status)
	var dayBody struct {
		Items []struct {
			ItemType        string  `json:"item_type"`
			PlanSource      *string `json:"plan_source"`
			PlanSourceTitle *string `json:"plan_source_title"`
		} `json:"items"`
	}
	day.decode(t, &dayBody)
	require.Len(t, dayBody.Items, 1)
	require.Equal(t, "reminder", dayBody.Items[0].ItemType)
	require.NotNil(t, dayBody.Items[0].PlanSource)
	require.Equal(t, "vaccination_extra", *dayBody.Items[0].PlanSource)
	require.NotNil(t, dayBody.Items[0].PlanSourceTitle)
	require.Equal(t, "Rabies", *dayBody.Items[0].PlanSourceTitle)

	mainDay := doRequest(t, http.MethodGet, "/activities/day?date="+futureDate(30)+"&tz=UTC", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, mainDay.status)
	dayBody.Items = nil
	mainDay.decode(t, &dayBody)
	require.Len(t, dayBody.Items, 1)
	require.Equal(t, "vaccination", *dayBody.Items[0].PlanSource)

	extraPath := "/reminder-plans/" + item.ExtraReminders[0].PlanID + "?tz=UTC"
	data := doRequest(t, http.MethodPatch, extraPath, map[string]any{"notes": "x"}, tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, data.status, "%s", data.body)
	pets := doRequest(t, http.MethodPatch, extraPath, map[string]any{"pet_ids": []string{petID}}, tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, pets.status, "%s", pets.body)
	daily := doRequest(t, http.MethodPatch, extraPath, map[string]any{
		"frequency_type": "daily", "times": []string{"08:00"}, "start_date": futureDate(10),
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, daily.status, "%s", daily.body)
}

// GET /reminders/upcoming отдаёт value настроек: у дополнительных напоминаний
// вакцинации есть name и vaccination_at (момент вакцинации), у основного —
// только name.
func TestVaccinationExtras_UpcomingRemindersValue(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	createVaccinationWithExtras(t, tokens.AccessToken, petID)

	resp := doRequest(t, http.MethodGet, "/reminders/upcoming", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	var body struct {
		Items []struct {
			Type  string `json:"type"`
			Value struct {
				Name          string  `json:"name"`
				VaccinationAt *string `json:"vaccination_at"`
			} `json:"value"`
		} `json:"items"`
	}
	resp.decode(t, &body)
	require.Len(t, body.Items, 3)

	// Порядок по remind_at: два дополнительных, затем основное.
	for _, it := range body.Items[:2] {
		require.Equal(t, "vaccination", it.Type)
		require.Equal(t, "Rabies", it.Value.Name)
		require.NotNil(t, it.Value.VaccinationAt)
		require.Equal(t, futureDate(30)+"T10:00:00Z", *it.Value.VaccinationAt)
	}
	require.Equal(t, "vaccination", body.Items[2].Type)
	require.Equal(t, "Rabies", body.Items[2].Value.Name)
	require.Nil(t, body.Items[2].Value.VaccinationAt)
}

// Миграция 000030: настройки source=vaccination, чей id не равен
// vaccination.next_plan_id, переводятся в vaccination_extra; основное
// напоминание остаётся vaccination.
func TestVaccinationExtras_MigrationBackfillsSource(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	id, item := createVaccinationWithExtras(t, tokens.AccessToken, petID)

	// Приводим данные к форме до миграции: дополнительные — тоже vaccination.
	_, err := database.DB.Exec(`UPDATE reminder_plan SET source = 'vaccination' WHERE source = 'vaccination_extra'`)
	require.NoError(t, err)
	require.Equal(t, 3, countRows(t, `SELECT COUNT(*) FROM reminder_plan WHERE source = 'vaccination' AND source_id = $1`, id))

	up, err := os.ReadFile("../database/migrations/000030_reminder_plan_source_vaccination_extra.up.sql")
	require.NoError(t, err)
	_, err = database.DB.Exec(string(up))
	require.NoError(t, err)

	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM reminder_plan WHERE source = 'vaccination' AND source_id = $1`, id))
	require.Equal(t, 2, countRows(t, `SELECT COUNT(*) FROM reminder_plan WHERE source = 'vaccination_extra' AND source_id = $1`, id))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM reminder_plan WHERE source = 'vaccination' AND id = $1`, *item.NextPlanID))
}
