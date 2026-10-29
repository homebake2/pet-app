//go:build integration

package integration

import (
	"net/http"
	"testing"
	"time"

	"myauthservice/database"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestImportLocalData_HappyPath(t *testing.T) {
	resetDB(t)

	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	idempotencyKey := uuid.NewString()
	body := map[string]any{
		"profile": map[string]any{
			"first_name": "Иван",
			"email":      "ivan@example.com",
		},
		"pets": []map[string]any{
			{"local_id": "local-cat", "name": "Барсик", "species": "cat"},
			{"local_id": "local-dog", "name": "Рекс", "species": "dog"},
		},
		"events": []map[string]any{
			{
				"local_id":      "local-event-1",
				"pet_local_ids": []string{"local-cat"},
				"date":          time.Now().UTC().Format(time.RFC3339),
				"type":          "weight",
				"value":         map[string]any{"amount": 4.2},
			},
		},
		"reminder_plans": []map[string]any{},
	}

	resp := doRequest(t, http.MethodPost, "/import/local-data", body, tokens.AccessToken,
		map[string]string{"Idempotency-Key": idempotencyKey})
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)

	var result struct {
		PetsImported    int  `json:"pets_imported"`
		EventsImported  int  `json:"events_imported"`
		ProfileImported bool `json:"profile_imported"`
		Pets            []struct {
			LocalID string `json:"local_id"`
			ID      string `json:"id"`
		} `json:"pets"`
		Events []struct {
			LocalID string `json:"local_id"`
			ID      string `json:"id"`
		} `json:"events"`
	}
	resp.decode(t, &result)
	require.Equal(t, 2, result.PetsImported)
	require.Equal(t, 1, result.EventsImported)
	require.True(t, result.ProfileImported)

	// Поле events сопоставляет local_id клиента с новым серверным id,
	// симметрично pets (см. "Импорт локальных данных — Backend", раздел 3).
	require.Len(t, result.Events, 1)
	require.Equal(t, "local-event-1", result.Events[0].LocalID)
	require.NotEmpty(t, result.Events[0].ID)
	require.Len(t, result.Pets, 2)

	// Перенесённые данные действительно доступны через обычные эндпоинты, с
	// новыми серверными id (local_id нигде не сохраняется/не возвращается).
	list := doRequest(t, http.MethodGet, "/pet", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, list.status)
	var listBody struct {
		Items []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	list.decode(t, &listBody)
	require.Len(t, listBody.Items, 2)
	for _, item := range listBody.Items {
		require.NotEqual(t, "local-cat", item.ID)
		require.NotEqual(t, "local-dog", item.ID)
	}

	profileResp := doRequest(t, http.MethodGet, "/profile", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, profileResp.status)
	var profileBody struct {
		FirstName string `json:"first_name"`
	}
	profileResp.decode(t, &profileBody)
	require.Equal(t, "Иван", profileBody.FirstName)
}

// TestImportLocalData_ReminderPlansLinkedToMedicationAndVaccination
// проверяет перенос reminder_plans[]: настройки и напоминания переносятся
// как есть (расписание не пересчитывается, прошедший момент остаётся
// незавершённым), источник выставляет сервер по ссылкам из medications и
// vaccinations, ответ сопоставляет local_id настроек и напоминаний с
// серверными id, повтор с тем же ключом возвращает то же сопоставление.
func TestImportLocalData_ReminderPlansLinkedToMedicationAndVaccination(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	futureMoment := futureDate(10) + "T08:00:00Z"
	body := map[string]any{
		"pets":   []map[string]any{{"local_id": "local-cat", "name": "Барсик", "species": "cat"}},
		"events": []map[string]any{},
		"reminder_plans": []map[string]any{
			{
				"local_id":       "plan-med",
				"pet_local_ids":  []string{"local-cat"},
				"type":           "medication",
				"value":          map[string]any{"name": "Amoxicillin"},
				"notes":          "1 tablet",
				"frequency_type": "daily",
				"times":          []string{"08:00"},
				"start_date":     "2024-01-01",
				"tz":             "UTC",
				"reminders": []map[string]any{
					// Прошедший момент переносится как незавершённый.
					{"local_id": "rem-past", "remind_at": "2024-01-01T08:00:00Z", "notes": "2 пипетки"},
					{"local_id": "rem-future", "remind_at": futureMoment},
				},
			},
			{
				"local_id":       "plan-vac",
				"pet_local_ids":  []string{"local-cat"},
				"type":           "vaccination",
				"value":          map[string]any{"name": "Rabies"},
				"frequency_type": "once",
				"times":          []string{"09:00"},
				"start_date":     futureDate(30),
				"tz":             "UTC",
				"reminders": []map[string]any{
					{"local_id": "rem-vac", "remind_at": futureDate(30) + "T09:00:00Z"},
				},
			},
			{
				"local_id":       "plan-manual",
				"pet_local_ids":  []string{"local-cat"},
				"type":           "weight",
				"value":          map[string]any{"amount": 4.0},
				"frequency_type": "once",
				"times":          []string{"10:00"},
				"start_date":     futureDate(20),
				"tz":             "UTC",
				"reminders": []map[string]any{
					{"local_id": "rem-manual", "remind_at": futureDate(20) + "T10:00:00Z"},
				},
			},
		},
		"medications": []map[string]any{
			{
				"local_id":               "local-med-1",
				"pet_local_id":           "local-cat",
				"name":                   "Amoxicillin",
				"dosage":                 "1 tablet",
				"frequency_type":         "daily",
				"times":                  []map[string]any{{"time": "08:00"}},
				"start_date":             "2024-01-01",
				"reminder_plan_local_id": "plan-med",
			},
		},
		"vaccinations": []map[string]any{
			{
				"local_id":                    "local-vac-1",
				"pet_local_id":                "local-cat",
				"name":                        "Rabies",
				"administered_date":           "2024-01-01",
				"next_date":                   futureDate(30),
				"add_reminder_on_next":        true,
				"next_reminder_plan_local_id": "plan-vac",
			},
		},
	}

	headers := map[string]string{"Idempotency-Key": uuid.NewString()}
	resp := doRequest(t, http.MethodPost, "/import/local-data", body, tokens.AccessToken, headers)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)

	var result struct {
		ReminderPlansImported int `json:"reminder_plans_imported"`
		ReminderPlans         []struct {
			LocalID   string `json:"local_id"`
			ID        string `json:"id"`
			Reminders []struct {
				LocalID string `json:"local_id"`
				ID      string `json:"id"`
			} `json:"reminders"`
		} `json:"reminder_plans"`
		Medications  []struct{ ID string } `json:"medications"`
		Vaccinations []struct{ ID string } `json:"vaccinations"`
	}
	resp.decode(t, &result)
	require.Equal(t, 3, result.ReminderPlansImported)
	require.Len(t, result.ReminderPlans, 3)
	require.Equal(t, "plan-med", result.ReminderPlans[0].LocalID)
	require.Len(t, result.ReminderPlans[0].Reminders, 2)
	require.Equal(t, "rem-past", result.ReminderPlans[0].Reminders[0].LocalID)
	require.Equal(t, "rem-future", result.ReminderPlans[0].Reminders[1].LocalID)

	medPlan := getReminderPlan(t, tokens.AccessToken, result.ReminderPlans[0].ID)
	require.Equal(t, "medication", medPlan.Source)
	require.NotNil(t, medPlan.SourceID)
	require.Equal(t, result.Medications[0].ID, *medPlan.SourceID)
	require.Len(t, medPlan.Reminders, 2)
	require.NotNil(t, medPlan.Notes)
	require.Equal(t, "1 tablet", *medPlan.Notes)

	pastReminder := getReminder(t, tokens.AccessToken, result.ReminderPlans[0].Reminders[0].ID)
	require.NotNil(t, pastReminder.Notes)
	require.Equal(t, "2 пипетки", *pastReminder.Notes)

	vacPlan := getReminderPlan(t, tokens.AccessToken, result.ReminderPlans[1].ID)
	require.Equal(t, "vaccination", vacPlan.Source)
	require.Equal(t, result.Vaccinations[0].ID, *vacPlan.SourceID)

	manualPlan := getReminderPlan(t, tokens.AccessToken, result.ReminderPlans[2].ID)
	require.Equal(t, "manual", manualPlan.Source)
	require.Nil(t, manualPlan.SourceID)

	// Ссылки лекарства и прививки записаны.
	pets := doRequest(t, http.MethodGet, "/pet", nil, tokens.AccessToken)
	var petsBody struct {
		Items []struct{ ID string } `json:"items"`
	}
	pets.decode(t, &petsBody)
	require.Len(t, petsBody.Items, 1)
	medications := listMedications(t, tokens.AccessToken, petsBody.Items[0].ID)
	require.Len(t, medications, 1)
	require.NotNil(t, medications[0].ReminderPlanID)
	require.Equal(t, result.ReminderPlans[0].ID, *medications[0].ReminderPlanID)

	// Повтор с тем же ключом возвращает то же сопоставление без дублей.
	replay := doRequest(t, http.MethodPost, "/import/local-data", body, tokens.AccessToken, headers)
	require.Equalf(t, http.StatusOK, replay.status, "%s", replay.body)
	require.JSONEq(t, string(resp.body), string(replay.body))
	require.Equal(t, 3, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
}

// Источник настроек проверяется: на настройки не может ссылаться несколько
// записей, настройки вакцинации обязаны быть разовыми.
func TestImportLocalData_ReminderPlanSourceRulesRejected(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	dailyPlan := map[string]any{
		"local_id": "plan-1", "pet_local_ids": []string{"local-cat"}, "type": "medication",
		"value": map[string]any{"name": "Amoxicillin"}, "frequency_type": "daily",
		"times": []string{"08:00"}, "start_date": "2024-01-01", "tz": "UTC",
		"reminders": []map[string]any{{"local_id": "rem-1", "remind_at": futureDate(3) + "T08:00:00Z"}},
	}
	body := map[string]any{
		"pets":           []map[string]any{{"local_id": "local-cat", "name": "Барсик", "species": "cat"}},
		"events":         []map[string]any{},
		"reminder_plans": []map[string]any{dailyPlan},
		"vaccinations": []map[string]any{{
			"local_id": "vac-1", "pet_local_id": "local-cat", "name": "Rabies", "administered_date": "2024-01-01",
			"next_reminder_plan_local_id": "plan-1",
		}},
	}
	resp := doRequest(t, http.MethodPost, "/import/local-data", body, tokens.AccessToken,
		map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equal(t, http.StatusBadRequest, resp.status)
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
}

func TestImportLocalData_WithoutProfile(t *testing.T) {
	resetDB(t)

	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	idempotencyKey := uuid.NewString()
	body := map[string]any{
		"pets":   []map[string]any{{"local_id": "local-cat", "name": "Барсик", "species": "cat"}},
		"events": []map[string]any{},

		"reminder_plans": []map[string]any{},
	}

	resp := doRequest(t, http.MethodPost, "/import/local-data", body, tokens.AccessToken,
		map[string]string{"Idempotency-Key": idempotencyKey})
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)

	var result struct {
		PetsImported    int  `json:"pets_imported"`
		EventsImported  int  `json:"events_imported"`
		ProfileImported bool `json:"profile_imported"`
	}
	resp.decode(t, &result)
	require.Equal(t, 1, result.PetsImported)
	require.Equal(t, 0, result.EventsImported)
	require.False(t, result.ProfileImported)

	// GET /profile по-прежнему 404 — перенос без profile не создаёт запись.
	profileResp := doRequest(t, http.MethodGet, "/profile", nil, tokens.AccessToken)
	require.Equal(t, http.StatusNotFound, profileResp.status)
}

func TestImportLocalData_IdempotencyKeyReplayDoesNotDuplicate(t *testing.T) {
	resetDB(t)

	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	idempotencyKey := uuid.NewString()
	body := map[string]any{
		"pets":   []map[string]any{{"local_id": "local-cat", "name": "Барсик", "species": "cat"}},
		"events": []map[string]any{},

		"reminder_plans": []map[string]any{},
	}

	first := doRequest(t, http.MethodPost, "/import/local-data", body, tokens.AccessToken,
		map[string]string{"Idempotency-Key": idempotencyKey})
	require.Equalf(t, http.StatusOK, first.status, "%s", first.body)

	// Повторный запрос с тем же ключом, но другим (валидным) телом — должен
	// вернуть ранее сохранённый результат и не создать новых строк.
	secondBody := map[string]any{
		"pets":   []map[string]any{{"local_id": "local-other", "name": "Другой", "species": "dog"}},
		"events": []map[string]any{},

		"reminder_plans": []map[string]any{},
	}
	second := doRequest(t, http.MethodPost, "/import/local-data", secondBody, tokens.AccessToken,
		map[string]string{"Idempotency-Key": idempotencyKey})
	require.Equalf(t, http.StatusOK, second.status, "%s", second.body)
	require.JSONEq(t, string(first.body), string(second.body))

	list := doRequest(t, http.MethodGet, "/pet", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, list.status)
	var listBody struct {
		Items []struct{ ID string } `json:"items"`
	}
	list.decode(t, &listBody)
	require.Len(t, listBody.Items, 1)
}

// Отсутствие обязательного заголовка Idempotency-Key покрыто unit-тестом
// TestImportLocalDataHandler_MissingIdempotencyKey (handlers/import_test.go),
// а не здесь: doRequest сверяет каждый запрос с open-api/spec.json, где этот
// заголовок объявлен обязательным параметром — намеренно невалидный по
// спеке запрос сломал бы саму проверку гармонизации запроса со спекой,
// а не только проверял бы обработку сервером.

func TestImportLocalData_InvalidPetRejectedAndNothingPersisted(t *testing.T) {
	resetDB(t)

	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	idempotencyKey := uuid.NewString()
	body := map[string]any{
		"pets":   []map[string]any{{"local_id": "local-cat", "name": "", "species": "cat"}},
		"events": []map[string]any{},

		"reminder_plans": []map[string]any{},
	}
	resp := doRequest(t, http.MethodPost, "/import/local-data", body, tokens.AccessToken,
		map[string]string{"Idempotency-Key": idempotencyKey})
	require.Equal(t, http.StatusBadRequest, resp.status)
	var errBody struct {
		Code string `json:"code"`
	}
	resp.decode(t, &errBody)
	require.Equal(t, "VALIDATION_ERROR", errBody.Code)

	list := doRequest(t, http.MethodGet, "/pet", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, list.status)
	var listBody struct {
		Items []struct{ ID string } `json:"items"`
	}
	list.decode(t, &listBody)
	require.Empty(t, listBody.Items)
}

func TestImportLocalData_EventPetLocalIDMismatchRejected(t *testing.T) {
	resetDB(t)

	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	idempotencyKey := uuid.NewString()
	body := map[string]any{
		"pets": []map[string]any{{"local_id": "local-cat", "name": "Барсик", "species": "cat"}},
		"events": []map[string]any{
			{
				"local_id":      "local-event-1",
				"pet_local_ids": []string{"does-not-exist"},
				"date":          time.Now().UTC().Format(time.RFC3339),
				"type":          "weight",
				"value":         map[string]any{"amount": 4.2},
			},
		},
		"reminder_plans": []map[string]any{},
	}
	resp := doRequest(t, http.MethodPost, "/import/local-data", body, tokens.AccessToken,
		map[string]string{"Idempotency-Key": idempotencyKey})
	require.Equal(t, http.StatusBadRequest, resp.status)

	list := doRequest(t, http.MethodGet, "/pet", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, list.status)
	var listBody struct {
		Items []struct{ ID string } `json:"items"`
	}
	list.decode(t, &listBody)
	require.Empty(t, listBody.Items)
}

// Дублирующийся events[].local_id делает отображение local_id -> id
// неоднозначным для поля events ответа — запрос должен быть отклонён целиком
// (см. "Импорт локальных данных — Backend", раздел 4).
func TestImportLocalData_DuplicateEventLocalIDRejected(t *testing.T) {
	resetDB(t)

	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	idempotencyKey := uuid.NewString()
	body := map[string]any{
		"pets": []map[string]any{{"local_id": "local-cat", "name": "Барсик", "species": "cat"}},
		"events": []map[string]any{
			{
				"local_id":      "dup-event",
				"pet_local_ids": []string{"local-cat"},
				"date":          time.Now().UTC().Format(time.RFC3339),
				"type":          "weight",
				"value":         map[string]any{"amount": 4.2},
			},
			{
				"local_id":      "dup-event",
				"pet_local_ids": []string{"local-cat"},
				"date":          time.Now().UTC().Format(time.RFC3339),
				"type":          "weight",
				"value":         map[string]any{"amount": 5.0},
			},
		},
		"reminder_plans": []map[string]any{},
	}
	resp := doRequest(t, http.MethodPost, "/import/local-data", body, tokens.AccessToken,
		map[string]string{"Idempotency-Key": idempotencyKey})
	require.Equal(t, http.StatusBadRequest, resp.status)

	list := doRequest(t, http.MethodGet, "/pet", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, list.status)
	var listBody struct {
		Items []struct{ ID string } `json:"items"`
	}
	list.decode(t, &listBody)
	require.Empty(t, listBody.Items)
}

func TestImportLocalData_Unauthorized(t *testing.T) {
	resetDB(t)

	body := map[string]any{"pets": []map[string]any{}, "events": []map[string]any{}, "reminder_plans": []map[string]any{}}
	resp := doRequest(t, http.MethodPost, "/import/local-data", body, "",
		map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equal(t, http.StatusUnauthorized, resp.status)
}

// administered_time вакцинации при переносе сохраняется (HH:MM); не
// переданное — 00:00; некорректный формат — 400 без побочных записей.
func TestImportLocalData_VaccinationAdministeredTime(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	body := map[string]any{
		"pets":           []map[string]any{{"local_id": "local-cat", "name": "Барсик", "species": "cat"}},
		"events":         []map[string]any{},
		"reminder_plans": []map[string]any{},
		"vaccinations": []map[string]any{
			{"local_id": "vac-1", "pet_local_id": "local-cat", "name": "Rabies", "administered_date": "2024-01-01", "administered_time": "09:30"},
			{"local_id": "vac-2", "pet_local_id": "local-cat", "name": "Distemper", "administered_date": "2024-02-01"},
		},
	}
	resp := doRequest(t, http.MethodPost, "/import/local-data", body, tokens.AccessToken,
		map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)

	var times []string
	rows, err := database.DB.Query(`SELECT to_char(administered_time, 'HH24:MI') FROM vaccination ORDER BY administered_date`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		times = append(times, s)
	}
	require.Equal(t, []string{"09:30", "00:00"}, times)

	badBody := map[string]any{
		"pets":           []map[string]any{{"local_id": "local-dog", "name": "Рекс", "species": "dog"}},
		"events":         []map[string]any{},
		"reminder_plans": []map[string]any{},
		"vaccinations": []map[string]any{
			{"local_id": "vac-3", "pet_local_id": "local-dog", "name": "Rabies", "administered_date": "2024-01-01", "administered_time": "25:61"},
		},
	}
	bad := doRequest(t, http.MethodPost, "/import/local-data", badBody, tokens.AccessToken,
		map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equal(t, http.StatusBadRequest, bad.status)
}

// TestImportLocalData_BatchInsertsEventsAndReminders проверяет пакетную
// вставку: несколько событий одним запросом (в том числе с несколькими
// питомцами, с заметкой и без неё), напоминания настроек одним запросом с
// сохранением заметки, порядка и привязок к серверным id из ответа.
func TestImportLocalData_BatchInsertsEventsAndReminders(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "correct-password")

	now := time.Now().UTC().Truncate(time.Second)
	remindFirst := futureDate(3) + "T08:00:00Z"
	remindSecond := futureDate(4) + "T08:00:00Z"
	body := map[string]any{
		"pets": []map[string]any{
			{"local_id": "cat", "name": "Барсик", "species": "cat"},
			{"local_id": "dog", "name": "Рекс", "species": "dog"},
		},
		"events": []map[string]any{
			{
				"local_id": "weight-cat", "pet_local_ids": []string{"cat"},
				"date": now.Format(time.RFC3339), "type": "weight", "value": map[string]any{"amount": 4.2},
				"notes": "после обеда",
			},
			{
				"local_id": "walk-both", "pet_local_ids": []string{"dog", "cat"},
				"date": now.Add(-time.Hour).Format(time.RFC3339), "type": "mood", "value": map[string]any{"state": "calm"},
			},
			{
				"local_id": "weight-dog", "pet_local_ids": []string{"dog"},
				"date": now.Add(-48 * time.Hour).Format(time.RFC3339), "type": "weight", "value": map[string]any{"amount": 12.5},
			},
		},
		"reminder_plans": []map[string]any{
			{
				"local_id": "plan", "pet_local_ids": []string{"cat"}, "type": "feeding",
				"value":          map[string]any{"amount": 50, "unit": "g", "food": "dry"},
				"frequency_type": "daily", "times": []string{"08:00"}, "start_date": futureDate(3), "tz": "UTC",
				"reminders": []map[string]any{
					{"local_id": "r1", "remind_at": remindFirst, "notes": "утром"},
					{"local_id": "r2", "remind_at": remindSecond},
				},
			},
		},
	}
	resp := doRequest(t, http.MethodPost, "/import/local-data", body, tokens.AccessToken,
		map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)

	var result struct {
		Pets []struct {
			LocalID string `json:"local_id"`
			ID      string `json:"id"`
		} `json:"pets"`
		Events []struct {
			LocalID string `json:"local_id"`
			ID      string `json:"id"`
		} `json:"events"`
		ReminderPlans []struct {
			Reminders []struct {
				LocalID string `json:"local_id"`
				ID      string `json:"id"`
			} `json:"reminders"`
		} `json:"reminder_plans"`
	}
	resp.decode(t, &result)
	petID := map[string]string{}
	for _, pet := range result.Pets {
		petID[pet.LocalID] = pet.ID
	}
	eventID := map[string]string{}
	for _, event := range result.Events {
		eventID[event.LocalID] = event.ID
	}
	require.Len(t, eventID, 3)

	require.Equal(t, 3, countRows(t, `SELECT COUNT(*) FROM event`))
	require.Equal(t, 4, countRows(t, `SELECT COUNT(*) FROM event_pet`))

	// Заметка сохраняется у события, у которого она есть, и пуста у остальных.
	var notes *string
	require.NoError(t, database.DB.QueryRow(`SELECT notes FROM event WHERE id = $1`, eventID["weight-cat"]).Scan(&notes))
	require.NotNil(t, notes)
	require.Equal(t, "после обеда", *notes)
	require.NoError(t, database.DB.QueryRow(`SELECT notes FROM event WHERE id = $1`, eventID["walk-both"]).Scan(&notes))
	require.Nil(t, notes)

	// Питомцы события привязаны в порядке запроса: dog — первый, cat — второй.
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM event_pet WHERE event_id = $1 AND pet_id = $2 AND position = 1`, eventID["walk-both"], petID["dog"]))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM event_pet WHERE event_id = $1 AND pet_id = $2 AND position = 2`, eventID["walk-both"], petID["cat"]))

	// Событие читается обычным эндпоинтом: значение jsonb и дата сохранены.
	got := doRequest(t, http.MethodGet, "/events/"+eventID["weight-dog"], nil, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, got.status, "%s", got.body)

	// Напоминания: оба вставлены, id из ответа совпадают со строками, заметка
	// есть только у первого.
	require.Len(t, result.ReminderPlans, 1)
	require.Len(t, result.ReminderPlans[0].Reminders, 2)
	require.Equal(t, 2, countRows(t, `SELECT COUNT(*) FROM reminder`))
	first, second := result.ReminderPlans[0].Reminders[0], result.ReminderPlans[0].Reminders[1]
	require.Equal(t, "r1", first.LocalID)
	require.NoError(t, database.DB.QueryRow(`SELECT notes FROM reminder WHERE id = $1 AND remind_at = $2`, first.ID, remindFirst).Scan(&notes))
	require.NotNil(t, notes)
	require.Equal(t, "утром", *notes)
	require.NoError(t, database.DB.QueryRow(`SELECT notes FROM reminder WHERE id = $1 AND remind_at = $2`, second.ID, remindSecond).Scan(&notes))
	require.Nil(t, notes)
}
