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

// Системный тип события vaccination: факт, напоминание на следующую
// вакцинацию и дополнительные напоминания несут type=vaccination и
// value={name[, vaccination_at]}.

func assertExtrasVaccinationAt(t *testing.T, token string, item vaccinationItemBody, name, at string) {
	t.Helper()
	require.NotEmpty(t, item.ExtraReminders)
	for _, extra := range item.ExtraReminders {
		plan := getReminderPlan(t, token, extra.PlanID)
		require.Equal(t, "vaccination", plan.Type)
		require.Equal(t, name, plan.Value.Name)
		require.Equal(t, at, plan.Value.VaccinationAt)
	}
}

// Создание: факт и напоминания — type=vaccination; у основного и факта нет
// vaccination_at, у дополнительных он равен моменту next_date + next_time.
func TestVaccinationType_CreateWritesTypedRecords(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	_, item := createVaccinationWithExtras(t, tokens.AccessToken, petID)

	events := listPetEvents(t, tokens.AccessToken, petID)
	require.Len(t, events, 1)
	require.Equal(t, "vaccination", events[0].Type)
	require.Equal(t, "Rabies", events[0].Value.Name)
	require.Empty(t, events[0].Value.VaccinationAt)

	main := getReminderPlan(t, tokens.AccessToken, *item.NextPlanID)
	require.Equal(t, "vaccination", main.Type)
	require.Equal(t, "Rabies", main.Value.Name)
	require.Empty(t, main.Value.VaccinationAt)

	assertExtrasVaccinationAt(t, tokens.AccessToken, item, "Rabies", futureDate(30)+"T10:00:00Z")
}

// vaccination_at записывается со смещением пояса tz запроса.
func TestVaccinationType_VaccinationAtUsesTimeZoneOffset(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	resp := doRequest(t, http.MethodPost, "/pet/"+petID+"/vaccinations?tz=Europe/Moscow", map[string]any{
		"name": "Rabies", "administered_date": "2024-01-01",
		"next_date": futureDate(30), "next_time": "10:00",
		"extra_reminders": []map[string]any{extraReminder(futureDate(10), "08:00")},
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, resp.status, "%s", resp.body)

	items := listVaccinations(t, tokens.AccessToken, petID)
	assertExtrasVaccinationAt(t, tokens.AccessToken, items[0], "Rabies", futureDate(30)+"T10:00:00+03:00")
}

// PATCH вакцинации: смена name обновляет value.name у факта, основного и
// дополнительных; смена next_date/next_time пересчитывает vaccination_at
// дополнительных, не трогая их расписание.
func TestVaccinationType_PatchSyncsNameAndVaccinationAt(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	id, item := createVaccinationWithExtras(t, tokens.AccessToken, petID)
	path := "/vaccinations/" + id + "?tz=UTC"

	rename := doRequest(t, http.MethodPatch, path, map[string]any{"name": "Rabies 2"}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, rename.status, "%s", rename.body)
	events := listPetEvents(t, tokens.AccessToken, petID)
	require.Equal(t, "Rabies 2", events[0].Value.Name)
	require.Equal(t, "Rabies 2", getReminderPlan(t, tokens.AccessToken, *item.NextPlanID).Value.Name)
	items := listVaccinations(t, tokens.AccessToken, petID)
	assertExtrasVaccinationAt(t, tokens.AccessToken, items[0], "Rabies 2", futureDate(30)+"T10:00:00Z")

	move := doRequest(t, http.MethodPatch, path, map[string]any{"next_date": futureDate(40), "next_time": "11:30"}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, move.status, "%s", move.body)
	items = listVaccinations(t, tokens.AccessToken, petID)
	assertExtrasVaccinationAt(t, tokens.AccessToken, items[0], "Rabies 2", futureDate(40)+"T11:30:00Z")
	require.Equal(t, futureDate(10), items[0].ExtraReminders[0].Date)
	require.Equal(t, "08:00", items[0].ExtraReminders[0].Time)
	require.Equal(t, futureDate(20), items[0].ExtraReminders[1].Date)

	// Новое дополнительное создаётся сразу с актуальным vaccination_at.
	add := doRequest(t, http.MethodPatch, path, map[string]any{
		"extra_reminders": []map[string]any{
			{"plan_id": items[0].ExtraReminders[0].PlanID, "date": futureDate(10), "time": "08:00"},
			extraReminder(futureDate(25), "07:00"),
		},
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, add.status, "%s", add.body)
	items = listVaccinations(t, tokens.AccessToken, petID)
	require.Len(t, items[0].ExtraReminders, 2)
	assertExtrasVaccinationAt(t, tokens.AccessToken, items[0], "Rabies 2", futureDate(40)+"T11:30:00Z")
}

// Перенос основного напоминания из календаря пересчитывает vaccination_at
// дополнительных в той же транзакции.
func TestVaccinationType_CalendarMoveSyncsVaccinationAt(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	_, item := createVaccinationWithExtras(t, tokens.AccessToken, petID)

	move := doRequest(t, http.MethodPatch, "/reminder-plans/"+*item.NextPlanID+"?tz=UTC", map[string]any{
		"frequency_type": "once", "times": []string{"16:20"}, "start_date": futureDate(45),
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, move.status, "%s", move.body)

	items := listVaccinations(t, tokens.AccessToken, petID)
	require.Equal(t, futureDate(45), *items[0].NextDate)
	assertExtrasVaccinationAt(t, tokens.AccessToken, items[0], "Rabies", futureDate(45)+"T16:20:00Z")
	// Расписание дополнительных не меняется.
	require.Equal(t, futureDate(10), items[0].ExtraReminders[0].Date)
	require.Equal(t, "08:00", items[0].ExtraReminders[0].Time)
}

// Запреты: POST /events и POST /reminder-plans с type=vaccination — 400;
// type/value у записи типа vaccination через PATCH не меняются, дата и
// заметка факта редактируются.
func TestVaccinationType_Prohibitions(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	_, item := createVaccinationWithExtras(t, tokens.AccessToken, petID)

	postEvent := doRequest(t, http.MethodPost, "/events", map[string]any{
		"pet_ids": []string{petID}, "date": "2024-01-01T08:00:00Z", "type": "vaccination",
		"value": map[string]any{"name": "Rabies"},
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, postEvent.status, "%s", postEvent.body)

	postPlan := postReminderPlan(t, tokens.AccessToken, map[string]any{
		"id": uuid.NewString(), "pet_ids": []string{petID}, "type": "vaccination",
		"value": map[string]any{"name": "Rabies"}, "frequency_type": "once",
		"times": []string{"10:00"}, "start_date": futureDate(5),
	}, "UTC")
	require.Equalf(t, http.StatusBadRequest, postPlan.status, "%s", postPlan.body)

	// PATCH факта: type/value — 400, дата и заметка — 204.
	eventID := listPetEvents(t, tokens.AccessToken, petID)[0].ID
	for name, body := range map[string]map[string]any{
		"value":      {"value": map[string]any{"name": "Другое"}},
		"type+value": {"type": "other", "value": map[string]any{"label": "x"}},
	} {
		resp := doRequest(t, http.MethodPatch, "/events/"+eventID, body, tokens.AccessToken)
		require.Equalf(t, http.StatusBadRequest, resp.status, "%s: %s", name, resp.body)
	}
	other := createEventReturningID(t, tokens.AccessToken, petID, "2024-01-01T08:00:00Z", "other", map[string]any{"label": "x"})
	toVaccination := doRequest(t, http.MethodPatch, "/events/"+other, map[string]any{
		"type": "vaccination", "value": map[string]any{"name": "Rabies"},
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, toVaccination.status, "%s", toVaccination.body)
	editable := doRequest(t, http.MethodPatch, "/events/"+eventID, map[string]any{
		"date": "2024-01-05T10:00:00Z", "notes": "заметка",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, editable.status, "%s", editable.body)

	// PATCH настроек: type/value — 400.
	extraPath := "/reminder-plans/" + item.ExtraReminders[0].PlanID + "?tz=UTC"
	for name, body := range map[string]map[string]any{
		"value":      {"value": map[string]any{"name": "Другое"}},
		"type+value": {"type": "other", "value": map[string]any{"label": "x"}},
	} {
		resp := doRequest(t, http.MethodPatch, extraPath, body, tokens.AccessToken)
		require.Equalf(t, http.StatusBadRequest, resp.status, "%s: %s", name, resp.body)
	}
}

// Поиск по событиям питомца находит и по value.name у type=vaccination.
func TestVaccinationType_EventsSearchByName(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	createVaccinationWithExtras(t, tokens.AccessToken, petID)
	createEventReturningID(t, tokens.AccessToken, petID, "2024-01-02T08:00:00Z", "other", map[string]any{"label": "хромота"})

	resp := doRequest(t, http.MethodGet, "/pet/"+petID+"/events?search=rabi", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	var body struct {
		Items []petEventItem `json:"items"`
		Total int            `json:"total"`
	}
	resp.decode(t, &body)
	require.Equal(t, 1, body.Total)
	require.Len(t, body.Items, 1)
	require.Equal(t, "vaccination", body.Items[0].Type)
}

// Тип vaccination не агрегируется: графики по нему не строятся.
func TestVaccinationType_NotInStats(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	createVaccinationWithExtras(t, tokens.AccessToken, petID)

	resp := doUnvalidatedRequest(t, http.MethodGet, "/events/stats?pet_id="+petID+"&types=vaccination&from=2024-01-01&to=2024-12-31&interval=month&tz=UTC", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, resp.status, "%s", resp.body)
}

// Импорт принимает события и настройки type=vaccination (в том числе
// vaccination_at у дополнительных), проверяя форму value.
func TestVaccinationType_Import(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	moment := futureDate(30) + "T10:00:00Z"

	body := map[string]any{
		"pets": []map[string]any{{"local_id": "local-cat", "name": "Барсик", "species": "cat"}},
		"events": []map[string]any{
			{"local_id": "ev-vac", "pet_local_ids": []string{"local-cat"}, "date": "2024-01-01T08:00:00Z", "type": "vaccination", "value": map[string]any{"name": "Rabies"}},
		},
		"reminder_plans": []map[string]any{
			{
				"local_id": "plan-extra", "pet_local_ids": []string{"local-cat"}, "type": "vaccination",
				"value":          map[string]any{"name": "Rabies", "vaccination_at": moment},
				"frequency_type": "once", "times": []string{"08:00"}, "start_date": futureDate(10), "tz": "UTC",
				"reminders": []map[string]any{{"local_id": "rem-extra", "remind_at": futureDate(10) + "T08:00:00Z"}},
			},
		},
	}
	resp := doRequest(t, http.MethodPost, "/import/local-data", body, tokens.AccessToken,
		map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM event WHERE type = 'vaccination' AND value ->> 'name' = 'Rabies'`))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM reminder_plan WHERE type = 'vaccination' AND value ->> 'vaccination_at' = $1`, moment))

	bad := map[string]any{
		"pets": []map[string]any{{"local_id": "local-dog", "name": "Рекс", "species": "dog"}},
		"events": []map[string]any{
			{"local_id": "ev-bad", "pet_local_ids": []string{"local-dog"}, "date": "2024-01-01T08:00:00Z", "type": "vaccination", "value": map[string]any{"label": "Rabies"}},
		},
		"reminder_plans": []map[string]any{},
	}
	badResp := doUnvalidatedRequest(t, http.MethodPost, "/import/local-data", bad, tokens.AccessToken,
		map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equalf(t, http.StatusBadRequest, badResp.status, "%s", badResp.body)
}

// Миграция 000031: факты и настройки напоминаний вакцинации переводятся с
// type=other/value.label на type=vaccination/value.name (у дополнительных —
// ещё vaccination_at); down возвращает прежнюю форму.
func TestVaccinationType_Migration(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	createVaccinationWithExtras(t, tokens.AccessToken, petID)
	other := createEventReturningID(t, tokens.AccessToken, petID, "2024-01-02T08:00:00Z", "other", map[string]any{"label": "хромота"})

	// Приводим данные к форме до миграции.
	_, err := database.DB.Exec(`UPDATE event SET type = 'other', value = '{"label":"Вакцинация: Rabies"}'::jsonb WHERE type = 'vaccination'`)
	require.NoError(t, err)
	_, err = database.DB.Exec(`UPDATE reminder_plan SET type = 'other', value = '{"label":"Вакцинация: Rabies"}'::jsonb`)
	require.NoError(t, err)

	up, err := os.ReadFile("../database/migrations/000031_vaccination_event_type.up.sql")
	require.NoError(t, err)
	_, err = database.DB.Exec(string(up))
	require.NoError(t, err)

	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM event WHERE type = 'vaccination' AND value = '{"name":"Rabies"}'::jsonb`))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM event WHERE id = $1 AND type = 'other'`, other))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM reminder_plan WHERE source = 'vaccination' AND type = 'vaccination' AND value = '{"name":"Rabies"}'::jsonb`))
	require.Equal(t, 2, countRows(t,
		`SELECT COUNT(*) FROM reminder_plan WHERE source = 'vaccination_extra' AND type = 'vaccination' AND value ->> 'name' = 'Rabies' AND value ->> 'vaccination_at' = $1`,
		futureDate(30)+"T10:00:00Z"))

	down, err := os.ReadFile("../database/migrations/000031_vaccination_event_type.down.sql")
	require.NoError(t, err)
	_, err = database.DB.Exec(string(down))
	require.NoError(t, err)
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM event WHERE type = 'vaccination'`))
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder_plan WHERE type = 'vaccination'`))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM event WHERE type = 'other' AND value ->> 'label' = 'Вакцинация: Rabies'`))
	require.Equal(t, 3, countRows(t, `SELECT COUNT(*) FROM reminder_plan WHERE type = 'other' AND value ->> 'label' = 'Вакцинация: Rabies'`))
}
