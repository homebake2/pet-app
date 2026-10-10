//go:build integration

// Напоминания: настройки напоминания (reminder_plan) и напоминания
// (reminder) — создание, чтение, изменение, закрытие, замена, файлы,
// источники (лекарства и вакцинации) и чтение календаря, поверх реального HTTP
// (handlers.NewMux()) и Postgres со сверкой каждого запроса и ответа с
// open-api/spec.json.
package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"myauthservice/database"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// futureDate — календарная дата через days дней от сегодня (UTC).
func futureDate(days int) string {
	return time.Now().UTC().AddDate(0, 0, days).Format("2006-01-02")
}

// countRows выполняет SELECT COUNT(*) и возвращает число.
func countRows(t *testing.T, query string, args ...any) int {
	t.Helper()
	var count int
	require.NoError(t, database.DB.QueryRow(query, args...).Scan(&count))
	return count
}

type reminderRefBody struct {
	ID       string `json:"id"`
	RemindAt string `json:"remind_at"`
}

type planFileBody struct {
	FileID      string  `json:"file_id"`
	URL         string  `json:"url"`
	ContentType string  `json:"content_type"`
	Filename    *string `json:"filename"`
}

type planBody struct {
	ID      string `json:"id"`
	PetID   string `json:"pet_id"`
	PetName string `json:"pet_name"`
	Type    string `json:"type"`
	Value   struct {
		Name  string `json:"name"`
		Label string `json:"label"`
	} `json:"value"`
	Notes                         *string           `json:"notes"`
	FrequencyType                 string            `json:"frequency_type"`
	Weekdays                      *[]int            `json:"weekdays"`
	IntervalDays                  *int              `json:"interval_days"`
	Times                         []string          `json:"times"`
	StartDate                     string            `json:"start_date"`
	EndDate                       *string           `json:"end_date"`
	Source                        string            `json:"source"`
	SourceID                      *string           `json:"source_id"`
	SourceTitle                   *string           `json:"source_title"`
	Reminders                     []reminderRefBody `json:"reminders"`
	Files                         []planFileBody    `json:"files"`
	FutureRemindersWithFilesCount int               `json:"future_reminders_with_files_count"`
}

type reminderBody struct {
	ID                string         `json:"id"`
	PlanID            string         `json:"plan_id"`
	RemindAt          string         `json:"remind_at"`
	Type              string         `json:"type"`
	Notes             *string        `json:"notes"`
	PetID             string         `json:"pet_id"`
	PlanSource        string         `json:"plan_source"`
	PlanSourceTitle   *string        `json:"plan_source_title"`
	PlanFiles         []planFileBody `json:"plan_files"`
	Files             []planFileBody `json:"files"`
	PlanUnclosedCount int            `json:"plan_unclosed_count"`
}

func getReminderPlan(t *testing.T, token, planID string) planBody {
	t.Helper()
	resp := doRequest(t, http.MethodGet, "/reminder-plans/"+planID, nil, token)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	var plan planBody
	resp.decode(t, &plan)
	return plan
}

func getReminder(t *testing.T, token, reminderID string) reminderBody {
	t.Helper()
	resp := doRequest(t, http.MethodGet, "/reminders/"+reminderID, nil, token)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	var reminder reminderBody
	resp.decode(t, &reminder)
	return reminder
}

func postReminderPlan(t *testing.T, token string, body map[string]any, tz string) apiResponse {
	t.Helper()
	path := "/reminder-plans"
	if tz != "" {
		path += "?tz=" + tz
	}
	// Намеренно невалидные по контракту запросы (нет tz, недопустимый type,
	// слишком длинные notes) проверяют реакцию сервера — без сверки запроса
	// со спекой.
	return doUnvalidatedRequest(t, http.MethodPost, path, body, token)
}

func createPlan(t *testing.T, token string, body map[string]any) planBody {
	t.Helper()
	if _, ok := body["id"]; !ok {
		body["id"] = uuid.NewString()
	}
	resp := postReminderPlan(t, token, body, "UTC")
	require.Equalf(t, http.StatusCreated, resp.status, "%s", resp.body)
	var plan planBody
	resp.decode(t, &plan)
	return plan
}

// createOncePlan создаёт разовые настройки на дату date (YYYY-MM-DD) и
// время timeOfDay (HH:mm) по UTC.
func createOncePlan(t *testing.T, token, petID, eventType string, value map[string]any, date, timeOfDay string) planBody {
	t.Helper()
	return createPlan(t, token, map[string]any{
		"pet_id":         petID,
		"type":           eventType,
		"value":          value,
		"frequency_type": "once",
		"times":          []string{timeOfDay},
		"start_date":     date,
	})
}

// pastReminder сдвигает момент напоминания в прошлое напрямую в БД: через
// API прошедшие напоминания создать нельзя (расписание материализует только
// будущие моменты), а закрывать «выполнено» можно только наступившие.
func pastReminder(t *testing.T, reminderID string) {
	t.Helper()
	_, err := database.DB.Exec(`UPDATE reminder SET remind_at = now() - interval '1 hour' WHERE id = $1`, reminderID)
	require.NoError(t, err)
}

// --- A. POST /reminder-plans, B. GET /reminder-plans/{id} ---

func TestReminderPlan_CreateAndRead(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	start, end := futureDate(1), futureDate(2)
	plan := createPlan(t, tokens.AccessToken, map[string]any{
		"pet_id":         petID,
		"type":           "medication",
		"value":          map[string]any{"name": "Нурофен"},
		"notes":          "после еды",
		"frequency_type": "daily",
		"times":          []string{"08:00", "20:00"},
		"start_date":     start,
		"end_date":       end,
	})
	require.Equal(t, "manual", plan.Source)
	require.Nil(t, plan.SourceID)
	require.Nil(t, plan.SourceTitle)
	require.Equal(t, "Барсик", plan.PetName)
	require.Equal(t, "Нурофен", plan.Value.Name)
	require.NotNil(t, plan.Notes)
	require.Equal(t, "после еды", *plan.Notes)
	require.Equal(t, []string{"08:00", "20:00"}, plan.Times)
	require.Len(t, plan.Reminders, 4) // 2 дня x 2 времени
	require.Empty(t, plan.Files)
	require.Equal(t, 0, plan.FutureRemindersWithFilesCount)

	got := getReminderPlan(t, tokens.AccessToken, plan.ID)
	require.Equal(t, plan.ID, got.ID)
	require.Len(t, got.Reminders, 4)

	reminder := getReminder(t, tokens.AccessToken, plan.Reminders[0].ID)
	require.Equal(t, plan.ID, reminder.PlanID)
	require.Equal(t, "medication", reminder.Type)
	require.Equal(t, 4, reminder.PlanUnclosedCount)
	require.Equal(t, "manual", reminder.PlanSource)
	require.NotNil(t, reminder.Notes)
	require.Equal(t, "после еды", *reminder.Notes)
	require.Equal(t, start+"T08:00:00Z", reminder.RemindAt)

	// Напоминания — не факты: событий у питомца нет.
	require.Empty(t, listPetEvents(t, tokens.AccessToken, petID))
}

// Повтор создания с тем же клиентским id возвращает те же настройки (201)
// без повторной вставки; занятый чужими настройками id — 409.
func TestReminderPlan_CreateIdempotentByClientID(t *testing.T) {
	resetDB(t)
	owner := registerUser(t, uniqueLogin(t), "password123")
	stranger := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, owner.AccessToken, "Барсик")
	strangerPetID := createPet(t, stranger.AccessToken, "Рекс")

	body := map[string]any{
		"id":             uuid.NewString(),
		"pet_id":         petID,
		"type":           "weight",
		"value":          map[string]any{"amount": 4.2},
		"frequency_type": "once",
		"times":          []string{"08:00"},
		"start_date":     futureDate(3),
	}
	first := postReminderPlan(t, owner.AccessToken, body, "UTC")
	require.Equalf(t, http.StatusCreated, first.status, "%s", first.body)
	replay := postReminderPlan(t, owner.AccessToken, body, "UTC")
	require.Equalf(t, http.StatusCreated, replay.status, "%s", replay.body)
	var firstPlan, replayPlan planBody
	first.decode(t, &firstPlan)
	replay.decode(t, &replayPlan)
	require.Equal(t, firstPlan.ID, replayPlan.ID)
	require.Equal(t, firstPlan.Reminders[0].ID, replayPlan.Reminders[0].ID)
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM reminder`))

	// Тот же id у другого пользователя — 409.
	conflictBody := map[string]any{
		"id": body["id"], "pet_id": strangerPetID, "type": "weight", "value": map[string]any{"amount": 5.0},
		"frequency_type": "once", "times": []string{"08:00"}, "start_date": futureDate(3),
	}
	conflict := postReminderPlan(t, stranger.AccessToken, conflictBody, "UTC")
	require.Equal(t, http.StatusConflict, conflict.status)
}

func TestReminderPlan_CreateValidation(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	stranger := registerUser(t, uniqueLogin(t), "password123")

	base := func() map[string]any {
		return map[string]any{
			"id":             uuid.NewString(),
			"pet_id":         petID,
			"type":           "weight",
			"value":          map[string]any{"amount": 4.2},
			"frequency_type": "once",
			"times":          []string{"08:00"},
			"start_date":     futureDate(3),
		}
	}
	with := func(key string, value any) map[string]any {
		b := base()
		b[key] = value
		return b
	}

	cases := []struct {
		name string
		body map[string]any
		tz   string
		want int
	}{
		{"нет tz", base(), "", http.StatusBadRequest},
		{"неизвестный tz", base(), "Mars/Olympus", http.StatusBadRequest},
		{"tz Local", base(), "Local", http.StatusBadRequest},
		{"недопустимый type", with("type", "unknown"), "UTC", http.StatusBadRequest},
		{"value не по форме type", with("value", map[string]any{"amount": 9999}), "UTC", http.StatusBadRequest},
		{"момент в прошлом", with("start_date", "2020-01-01"), "UTC", http.StatusBadRequest},
		{"once с end_date", with("end_date", futureDate(5)), "UTC", http.StatusBadRequest},
		{"specific_days без weekdays", with("frequency_type", "specific_days"), "UTC", http.StatusBadRequest},
		{"два времени при once", with("times", []string{"08:00", "09:00"}), "UTC", http.StatusBadRequest},
		{"notes длиннее 500", with("notes", string(make([]byte, 501))), "UTC", http.StatusBadRequest},
		{"несуществующий питомец", with("pet_id", uuid.NewString()), "UTC", http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := c.body
			if c.name == "notes длиннее 500" {
				long := make([]byte, 501)
				for i := range long {
					long[i] = 'a'
				}
				body["notes"] = string(long)
			}
			resp := postReminderPlan(t, tokens.AccessToken, body, c.tz)
			require.Equalf(t, c.want, resp.status, "%s", resp.body)
		})
	}

	// Чужой питомец — 404.
	foreign := postReminderPlan(t, stranger.AccessToken, base(), "UTC")
	require.Equal(t, http.StatusNotFound, foreign.status)
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
}

// Потолок — 60 создаваемых напоминаний за операцию, считая по всем временам
// сразу; остаток расписания не материализуется.
func TestReminderPlan_CapAt60(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	plan := createPlan(t, tokens.AccessToken, map[string]any{
		"pet_id":         petID,
		"type":           "medication",
		"value":          map[string]any{"name": "Нурофен"},
		"frequency_type": "daily",
		"times":          []string{"08:00", "12:00", "16:00", "20:00"},
		"start_date":     futureDate(1),
	})
	require.Len(t, plan.Reminders, 60)
	require.Equal(t, 60, countRows(t, `SELECT COUNT(*) FROM reminder`))
}

// Расписание every_n_days и specific_days.
func TestReminderPlan_FrequencyKinds(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	everyThree := createPlan(t, tokens.AccessToken, map[string]any{
		"pet_id": petID, "type": "medication", "value": map[string]any{"name": "A"},
		"frequency_type": "every_n_days", "interval_days": 3, "times": []string{"08:00"},
		"start_date": futureDate(1), "end_date": futureDate(10),
	})
	require.Len(t, everyThree.Reminders, 4) // +1, +4, +7, +10
	require.NotNil(t, everyThree.IntervalDays)
	require.Equal(t, 3, *everyThree.IntervalDays)

	weekly := createPlan(t, tokens.AccessToken, map[string]any{
		"pet_id": petID, "type": "medication", "value": map[string]any{"name": "B"},
		"frequency_type": "specific_days", "weekdays": []int{1, 3, 5}, "times": []string{"09:00"},
		"start_date": futureDate(1), "end_date": futureDate(14),
	})
	require.NotEmpty(t, weekly.Reminders)
	for _, ref := range weekly.Reminders {
		moment, err := time.Parse(time.RFC3339, ref.RemindAt)
		require.NoError(t, err)
		weekday := int(moment.Weekday())
		require.Contains(t, []int{1, 3, 5}, weekday)
	}
	require.NotNil(t, weekly.Weekdays)
	require.Equal(t, []int{1, 3, 5}, *weekly.Weekdays)
}

// --- C. PATCH /reminder-plans/{id} ---

func TestReminderPlan_PatchDataAndSchedule(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	plan := createPlan(t, tokens.AccessToken, map[string]any{
		"pet_id": petID, "type": "medication", "value": map[string]any{"name": "Нурофен"},
		"frequency_type": "daily", "times": []string{"08:00"},
		"start_date": futureDate(1), "end_date": futureDate(3),
	})
	require.Len(t, plan.Reminders, 3)

	// Данные меняются только в настройках: id напоминаний сохраняются, а все
	// напоминания сразу отдают новые данные.
	patch := doRequest(t, http.MethodPatch, "/reminder-plans/"+plan.ID+"?tz=UTC", map[string]any{
		"notes": "после еды",
		"type":  "medication",
		"value": map[string]any{"name": "Нурофен Форте"},
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, patch.status, "%s", patch.body)
	changed := getReminderPlan(t, tokens.AccessToken, plan.ID)
	require.Equal(t, "Нурофен Форте", changed.Value.Name)
	require.Equal(t, plan.Reminders[0].ID, changed.Reminders[0].ID)
	require.Len(t, changed.Reminders, 3)

	// Закрываем среднее напоминание удалением: момент остаётся закрытым.
	del := doRequest(t, http.MethodDelete, "/reminders/"+plan.Reminders[1].ID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, del.status)

	// Новое расписание: те же даты плюс ещё один день. Закрытый момент не
	// создаётся заново, остальные будущие пересоздаются.
	patch = doRequest(t, http.MethodPatch, "/reminder-plans/"+plan.ID+"?tz=UTC", map[string]any{
		"frequency_type": "daily",
		"times":          []string{"08:00"},
		"start_date":     futureDate(1),
		"end_date":       futureDate(4),
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, patch.status, "%s", patch.body)
	changed = getReminderPlan(t, tokens.AccessToken, plan.ID)
	got := make([]string, 0, len(changed.Reminders))
	for _, ref := range changed.Reminders {
		got = append(got, ref.RemindAt)
	}
	require.Equal(t, []string{
		futureDate(1) + "T08:00:00Z",
		futureDate(3) + "T08:00:00Z",
		futureDate(4) + "T08:00:00Z",
	}, got)
	require.NotNil(t, changed.EndDate)
	require.Equal(t, futureDate(4), *changed.EndDate)

	// Расписание, которое не даёт ни одного будущего момента, — 400.
	bad := doRequest(t, http.MethodPatch, "/reminder-plans/"+plan.ID+"?tz=UTC", map[string]any{
		"frequency_type": "daily", "times": []string{"08:00"}, "start_date": "2020-01-01", "end_date": "2020-01-05",
	}, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, bad.status)
}

func TestReminderPlan_PatchValidation(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	plan := createOncePlan(t, tokens.AccessToken, petID, "weight", map[string]any{"amount": 4.2}, futureDate(3), "08:00")
	path := "/reminder-plans/" + plan.ID

	cases := []struct {
		name string
		path string
		body map[string]any
	}{
		{"нет tz", path, map[string]any{"notes": "x"}},
		{"пустое тело", path + "?tz=UTC", map[string]any{}},
		{"type без value", path + "?tz=UTC", map[string]any{"type": "weight"}},
		{"расписание не целиком", path + "?tz=UTC", map[string]any{"frequency_type": "daily"}},
		{"только end_date", path + "?tz=UTC", map[string]any{"end_date": futureDate(9)}},
		{"value не по форме", path + "?tz=UTC", map[string]any{"value": map[string]any{"amount": 9999}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := doUnvalidatedRequest(t, http.MethodPatch, c.path, c.body, tokens.AccessToken)
			require.Equalf(t, http.StatusBadRequest, resp.status, "%s", resp.body)
		})
	}

	// Чужие и несуществующие настройки — 404.
	stranger := registerUser(t, uniqueLogin(t), "password123")
	foreign := doRequest(t, http.MethodPatch, path+"?tz=UTC", map[string]any{"notes": "x"}, stranger.AccessToken)
	require.Equal(t, http.StatusNotFound, foreign.status)
	missing := doRequest(t, http.MethodPatch, "/reminder-plans/"+uuid.NewString()+"?tz=UTC", map[string]any{"notes": "x"}, tokens.AccessToken)
	require.Equal(t, http.StatusNotFound, missing.status)
}

// --- D. DELETE /reminder-plans/{id}, F. DELETE /reminders/{id} ---

func TestReminder_DeleteOneAndLastRemovesPlan(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	plan := createPlan(t, tokens.AccessToken, map[string]any{
		"pet_id": petID, "type": "weight", "value": map[string]any{"amount": 4.2},
		"frequency_type": "daily", "times": []string{"08:00"},
		"start_date": futureDate(1), "end_date": futureDate(2),
	})
	require.Len(t, plan.Reminders, 2)

	// Удаление одного напоминания: закрытая строка остаётся как отметка.
	first := doRequest(t, http.MethodDelete, "/reminders/"+plan.Reminders[0].ID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, first.status)
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM reminder WHERE closed_at IS NOT NULL AND close_reason = 'deleted'`))
	reminder := getReminder(t, tokens.AccessToken, plan.Reminders[1].ID)
	require.Equal(t, 1, reminder.PlanUnclosedCount)

	// Закрытое напоминание недоступно.
	gone := doRequest(t, http.MethodGet, "/reminders/"+plan.Reminders[0].ID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNotFound, gone.status)
	again := doRequest(t, http.MethodDelete, "/reminders/"+plan.Reminders[0].ID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNotFound, again.status)

	// Последнее напоминание: настройки удаляются вместе со всеми строками.
	last := doRequest(t, http.MethodDelete, "/reminders/"+plan.Reminders[1].ID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, last.status)
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder`))
	notFound := doRequest(t, http.MethodGet, "/reminder-plans/"+plan.ID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNotFound, notFound.status)
}

func TestReminderPlan_DeleteAndOwnership(t *testing.T) {
	resetDB(t)
	owner := registerUser(t, uniqueLogin(t), "password123")
	stranger := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, owner.AccessToken, "Барсик")
	plan := createPlan(t, owner.AccessToken, map[string]any{
		"pet_id": petID, "type": "weight", "value": map[string]any{"amount": 4.2},
		"frequency_type": "daily", "times": []string{"08:00"},
		"start_date": futureDate(1), "end_date": futureDate(3),
	})

	// Чужие настройки и напоминания — 404 на всех эндпоинтах.
	require.Equal(t, http.StatusNotFound, doRequest(t, http.MethodGet, "/reminder-plans/"+plan.ID, nil, stranger.AccessToken).status)
	require.Equal(t, http.StatusNotFound, doRequest(t, http.MethodDelete, "/reminder-plans/"+plan.ID, nil, stranger.AccessToken).status)
	require.Equal(t, http.StatusNotFound, doRequest(t, http.MethodGet, "/reminders/"+plan.Reminders[0].ID, nil, stranger.AccessToken).status)
	require.Equal(t, http.StatusNotFound, doRequest(t, http.MethodDelete, "/reminders/"+plan.Reminders[0].ID, nil, stranger.AccessToken).status)
	require.Equal(t, http.StatusNotFound, doRequest(t, http.MethodPost, "/reminders/"+plan.Reminders[0].ID+"/complete", map[string]any{"done": true}, stranger.AccessToken).status)

	del := doRequest(t, http.MethodDelete, "/reminder-plans/"+plan.ID, nil, owner.AccessToken)
	require.Equal(t, http.StatusNoContent, del.status)
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder`))
	require.Equal(t, http.StatusNotFound, doRequest(t, http.MethodDelete, "/reminder-plans/"+plan.ID, nil, owner.AccessToken).status)
}

// --- G. POST /reminders/{id}/complete ---

func TestReminder_CompleteDoneCreatesFactAndClosesReminder(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	plan := createPlan(t, tokens.AccessToken, map[string]any{
		"pet_id": petID, "type": "weight", "value": map[string]any{"amount": 4.2}, "notes": "после еды",
		"frequency_type": "daily", "times": []string{"08:00"},
		"start_date": futureDate(1), "end_date": futureDate(2),
	})
	reminderID := plan.Reminders[0].ID

	// Будущее напоминание закрыть этим способом нельзя.
	early := doRequest(t, http.MethodPost, "/reminders/"+reminderID+"/complete", map[string]any{"done": true}, tokens.AccessToken)
	require.Equal(t, http.StatusConflict, early.status)

	pastReminder(t, reminderID)

	// Без обязательного поля done — 400.
	noDone := doUnvalidatedRequest(t, http.MethodPost, "/reminders/"+reminderID+"/complete", map[string]any{}, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, noDone.status)

	done := doRequest(t, http.MethodPost, "/reminders/"+reminderID+"/complete", map[string]any{"done": true}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, done.status, "%s", done.body)
	var result struct {
		FactEventID *string `json:"fact_event_id"`
	}
	done.decode(t, &result)
	require.NotNil(t, result.FactEventID)

	// Факт: те же type/value, notes из настроек, date = remind_at.
	event := doRequest(t, http.MethodGet, "/events/"+*result.FactEventID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, event.status)
	var eventBody struct {
		Type  string  `json:"type"`
		Notes *string `json:"notes"`
		PetID string  `json:"pet_id"`
	}
	event.decode(t, &eventBody)
	require.Equal(t, "weight", eventBody.Type)
	require.Equal(t, petID, eventBody.PetID)
	require.NotNil(t, eventBody.Notes)
	require.Equal(t, "после еды", *eventBody.Notes)
	require.Len(t, listPetEvents(t, tokens.AccessToken, petID), 1)

	// Повторный вызов — тот же fact_event_id, ничего не создаётся.
	replay := doRequest(t, http.MethodPost, "/reminders/"+reminderID+"/complete", map[string]any{"done": false}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, replay.status, "%s", replay.body)
	var replayResult struct {
		FactEventID *string `json:"fact_event_id"`
	}
	replay.decode(t, &replayResult)
	require.NotNil(t, replayResult.FactEventID)
	require.Equal(t, *result.FactEventID, *replayResult.FactEventID)
	require.Len(t, listPetEvents(t, tokens.AccessToken, petID), 1)

	// Закрытое напоминание больше не отдаётся.
	require.Equal(t, http.StatusNotFound, doRequest(t, http.MethodGet, "/reminders/"+reminderID, nil, tokens.AccessToken).status)
}

func TestReminder_CompleteSkippedCreatesNoFact(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	plan := createOncePlan(t, tokens.AccessToken, petID, "weight", map[string]any{"amount": 4.2}, futureDate(1), "08:00")
	pastReminder(t, plan.Reminders[0].ID)

	resp := doRequest(t, http.MethodPost, "/reminders/"+plan.Reminders[0].ID+"/complete", map[string]any{"done": false}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	var result struct {
		FactEventID *string `json:"fact_event_id"`
	}
	resp.decode(t, &result)
	require.Nil(t, result.FactEventID)
	require.Empty(t, listPetEvents(t, tokens.AccessToken, petID))

	// Единственное напоминание закрыто — настройки удалены; повтор — 404.
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
	again := doRequest(t, http.MethodPost, "/reminders/"+plan.Reminders[0].ID+"/complete", map[string]any{"done": false}, tokens.AccessToken)
	require.Equal(t, http.StatusNotFound, again.status)
}

// Собственная заметка напоминания показывается вместо заметки настроек и
// попадает в факт (заметка напоминания лекарства — доза по времени приёма).
func TestReminder_OwnNotesWinOverPlanNotes(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	createMed := doRequest(t, http.MethodPost, "/pet/"+petID+"/medications?tz=UTC", map[string]any{
		"name": "Amoxicillin", "dosage": "1 tablet", "frequency_type": "daily",
		"times":      []map[string]any{{"time": "08:00", "dose_note": "2 пипетки"}, {"time": "20:00"}},
		"start_date": futureDate(1), "end_date": futureDate(1), "add_reminders": true,
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, createMed.status, "%s", createMed.body)
	medications := listMedications(t, tokens.AccessToken, petID)
	plan := getReminderPlan(t, tokens.AccessToken, *medications[0].ReminderPlanID)
	require.Len(t, plan.Reminders, 2)

	morning := getReminder(t, tokens.AccessToken, plan.Reminders[0].ID)
	require.NotNil(t, morning.Notes)
	require.Equal(t, "2 пипетки", *morning.Notes)
	evening := getReminder(t, tokens.AccessToken, plan.Reminders[1].ID)
	require.NotNil(t, evening.Notes)
	require.Equal(t, "1 tablet", *evening.Notes)

	// День календаря показывает ту же заметку.
	day := doRequest(t, http.MethodGet, "/activities/day?date="+futureDate(1)+"&tz=UTC", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, day.status)
	var dayBody struct {
		Items []struct {
			ItemType        string  `json:"item_type"`
			Notes           *string `json:"notes"`
			PlanSource      *string `json:"plan_source"`
			PlanSourceTitle *string `json:"plan_source_title"`
			PlanUnclosed    *int    `json:"plan_unclosed_count"`
		} `json:"items"`
	}
	day.decode(t, &dayBody)
	require.Len(t, dayBody.Items, 2)
	require.Equal(t, "reminder", dayBody.Items[0].ItemType)
	// Данные настроек для выбора «это / все» приходят в самом списке дня.
	require.NotNil(t, dayBody.Items[0].PlanSource)
	require.Equal(t, "medication", *dayBody.Items[0].PlanSource)
	require.NotNil(t, dayBody.Items[0].PlanSourceTitle)
	require.Equal(t, "Amoxicillin", *dayBody.Items[0].PlanSourceTitle)
	require.NotNil(t, dayBody.Items[0].PlanUnclosed)
	require.Equal(t, 2, *dayBody.Items[0].PlanUnclosed)
	require.Equal(t, "2 пипетки", *dayBody.Items[0].Notes)
	require.Equal(t, "1 tablet", *dayBody.Items[1].Notes)

	// Факт при отметке «выполнено» получает заметку напоминания.
	pastReminder(t, plan.Reminders[0].ID)
	done := doRequest(t, http.MethodPost, "/reminders/"+plan.Reminders[0].ID+"/complete", map[string]any{"done": true}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, done.status, "%s", done.body)
	var result struct {
		FactEventID string `json:"fact_event_id"`
	}
	done.decode(t, &result)
	event := doRequest(t, http.MethodGet, "/events/"+result.FactEventID, nil, tokens.AccessToken)
	var eventBody struct {
		Notes *string `json:"notes"`
		Type  string  `json:"type"`
	}
	event.decode(t, &eventBody)
	require.Equal(t, "medication", eventBody.Type)
	require.NotNil(t, eventBody.Notes)
	require.Equal(t, "2 пипетки", *eventBody.Notes)
}

// --- H. POST /reminders/{id}/detach ---

func TestReminder_DetachWithEventAndPlan(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	otherPetID := createPet(t, tokens.AccessToken, "Рекс")
	plan := createPlan(t, tokens.AccessToken, map[string]any{
		"pet_id": petID, "type": "weight", "value": map[string]any{"amount": 4.2},
		"frequency_type": "daily", "times": []string{"08:00"},
		"start_date": futureDate(1), "end_date": futureDate(3),
	})
	require.Len(t, plan.Reminders, 3)

	// Замена фактом: новый факт создаётся, исходное напоминание закрыто
	// как replaced, остальные напоминания настроек на месте.
	key := uuid.NewString()
	detachEvent := doRequest(t, http.MethodPost, "/reminders/"+plan.Reminders[0].ID+"/detach", map[string]any{
		"event": map[string]any{
			"pet_id": petID, "date": "2024-01-01T08:00:00Z", "type": "weight", "value": map[string]any{"amount": 5.5},
		},
	}, tokens.AccessToken, map[string]string{"Idempotency-Key": key})
	require.Equalf(t, http.StatusCreated, detachEvent.status, "%s", detachEvent.body)
	var eventResult struct {
		Event struct {
			ID    string `json:"id"`
			PetID string `json:"pet_id"`
		} `json:"event"`
	}
	detachEvent.decode(t, &eventResult)
	require.NotEmpty(t, eventResult.Event.ID)
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM reminder WHERE close_reason = 'replaced'`))
	require.Len(t, getReminderPlan(t, tokens.AccessToken, plan.ID).Reminders, 2)

	// Повтор после успешного выполнения: напоминания уже нет, но факт с тем
	// же Idempotency-Key возвращается вместо 404.
	replay := doRequest(t, http.MethodPost, "/reminders/"+plan.Reminders[0].ID+"/detach", map[string]any{
		"event": map[string]any{
			"pet_id": petID, "date": "2024-01-01T08:00:00Z", "type": "weight", "value": map[string]any{"amount": 5.5},
		},
	}, tokens.AccessToken, map[string]string{"Idempotency-Key": key})
	require.Equalf(t, http.StatusCreated, replay.status, "%s", replay.body)
	var replayResult struct {
		Event struct {
			ID string `json:"id"`
		} `json:"event"`
	}
	replay.decode(t, &replayResult)
	require.Equal(t, eventResult.Event.ID, replayResult.Event.ID)

	// Факт с датой в будущем — 400 (правило факта), напоминание не закрыто.
	futureFact := doRequest(t, http.MethodPost, "/reminders/"+plan.Reminders[1].ID+"/detach", map[string]any{
		"event": map[string]any{
			"pet_id": petID, "date": time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339), "type": "weight", "value": map[string]any{"amount": 5.5},
		},
	}, tokens.AccessToken, map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equal(t, http.StatusBadRequest, futureFact.status)

	// Перенос на другого питомца не поддерживается.
	otherPet := doRequest(t, http.MethodPost, "/reminders/"+plan.Reminders[1].ID+"/detach", map[string]any{
		"event": map[string]any{
			"pet_id": otherPetID, "date": "2024-01-01T08:00:00Z", "type": "weight", "value": map[string]any{"amount": 5.5},
		},
	}, tokens.AccessToken, map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equal(t, http.StatusBadRequest, otherPet.status)

	// Ровно одно из event и plan.
	both := doRequest(t, http.MethodPost, "/reminders/"+plan.Reminders[1].ID+"/detach", map[string]any{}, tokens.AccessToken,
		map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equal(t, http.StatusBadRequest, both.status)

	// Замена новыми независимыми настройками (manual).
	newPlanID := uuid.NewString()
	detachPlan := doRequest(t, http.MethodPost, "/reminders/"+plan.Reminders[1].ID+"/detach?tz=UTC", map[string]any{
		"plan": map[string]any{
			"id": newPlanID, "pet_id": petID, "type": "weight", "value": map[string]any{"amount": 6.0},
			"frequency_type": "once", "times": []string{"10:00"}, "start_date": futureDate(10),
		},
	}, tokens.AccessToken, map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equalf(t, http.StatusCreated, detachPlan.status, "%s", detachPlan.body)
	var planResult struct {
		Plan planBody `json:"plan"`
	}
	detachPlan.decode(t, &planResult)
	require.Equal(t, newPlanID, planResult.Plan.ID)
	require.Equal(t, "manual", planResult.Plan.Source)
	require.Len(t, planResult.Plan.Reminders, 1)
	require.Len(t, getReminderPlan(t, tokens.AccessToken, plan.ID).Reminders, 1)

	// Без tz для plan — 400.
	noTZ := doUnvalidatedRequest(t, http.MethodPost, "/reminders/"+plan.Reminders[2].ID+"/detach", map[string]any{
		"plan": map[string]any{
			"id": uuid.NewString(), "pet_id": petID, "type": "weight", "value": map[string]any{"amount": 6.0},
			"frequency_type": "once", "times": []string{"10:00"}, "start_date": futureDate(10),
		},
	}, tokens.AccessToken, map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equal(t, http.StatusBadRequest, noTZ.status)
}

// --- Файлы: ссылочные строки file ---

func uploadAndConfirmFile(t *testing.T, token, ownerType, ownerID, contentType string) string {
	t.Helper()
	upload := doRequest(t, http.MethodPost, "/files/upload-url", map[string]any{
		"owner_type": ownerType, "owner_id": ownerID, "content_type": contentType,
	}, token)
	require.Equalf(t, http.StatusOK, upload.status, "%s", upload.body)
	var out uploadURLResponse
	upload.decode(t, &out)
	complete := doRequest(t, http.MethodPost, "/files/"+out.FileID+"/complete", nil, token)
	require.Equalf(t, http.StatusNoContent, complete.status, "complete: %s", complete.body)
	return out.FileID
}

// Файлы настроек и собственные файлы напоминания при отметке «выполнено»
// достаются факту ссылочными строками file: тот же object_key, объект S3 не
// копируется; удаление файла факта не удаляет объект, пока на него указывают
// другие строки.
func TestReminderFiles_ReferenceRowsOnComplete(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	plan := createOncePlan(t, tokens.AccessToken, petID, "weight", map[string]any{"amount": 4.2}, futureDate(1), "08:00")
	reminderID := plan.Reminders[0].ID

	planFileID := uploadAndConfirmFile(t, tokens.AccessToken, "reminder_plan_file", plan.ID, "image/jpeg")
	ownFileID := uploadAndConfirmFile(t, tokens.AccessToken, "reminder_file", reminderID, "application/pdf")

	reminder := getReminder(t, tokens.AccessToken, reminderID)
	require.Len(t, reminder.PlanFiles, 1)
	require.Equal(t, planFileID, reminder.PlanFiles[0].FileID)
	require.Len(t, reminder.Files, 1)
	require.Equal(t, ownFileID, reminder.Files[0].FileID)
	require.Equal(t, 1, getReminderPlan(t, tokens.AccessToken, plan.ID).FutureRemindersWithFilesCount)

	// В календаре files_count — файлы настроек плюс собственные.
	day := doRequest(t, http.MethodGet, "/activities/day?date="+futureDate(1)+"&tz=UTC", nil, tokens.AccessToken)
	var dayBody struct {
		Items []struct {
			FilesCount int `json:"files_count"`
		} `json:"items"`
	}
	day.decode(t, &dayBody)
	require.Len(t, dayBody.Items, 1)
	require.Equal(t, 2, dayBody.Items[0].FilesCount)

	pastReminder(t, reminderID)
	done := doRequest(t, http.MethodPost, "/reminders/"+reminderID+"/complete", map[string]any{"done": true}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, done.status, "%s", done.body)
	var result struct {
		FactEventID string `json:"fact_event_id"`
	}
	done.decode(t, &result)

	// У факта две ссылочные строки; объектов в S3 по-прежнему два.
	eventResp := doRequest(t, http.MethodGet, "/events/"+result.FactEventID, nil, tokens.AccessToken)
	var event eventWithFiles
	eventResp.decode(t, &event)
	require.Len(t, event.Files, 2)
	require.Equal(t, 2, countRows(t, `SELECT COUNT(*) FROM file WHERE owner_type = 'event_file' AND owner_id = $1`, result.FactEventID))
	require.Equal(t, 2, countRows(t, `SELECT COUNT(DISTINCT object_key) FROM file`))

	// Настройки удалены (напоминание было единственным) вместе со своими
	// строками file; файлы факта остались.
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM file WHERE owner_type IN ('reminder_plan_file', 'reminder_file')`))

	// Удаление файла факта убирает строку, но не ломает второй файл.
	del := doRequest(t, http.MethodDelete, "/files/"+event.Files[0].FileID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, del.status)
	eventResp = doRequest(t, http.MethodGet, "/events/"+result.FactEventID, nil, tokens.AccessToken)
	eventResp.decode(t, &event)
	require.Len(t, event.Files, 1)
}

// Файлы настроек и собственные файлы напоминания вместе помещаются в один
// факт (до 10): подтверждение сверх суммы — 409.
func TestReminderFiles_CombinedLimit409(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	plan := createPlan(t, tokens.AccessToken, map[string]any{
		"pet_id": petID, "type": "weight", "value": map[string]any{"amount": 4.2},
		"frequency_type": "daily", "times": []string{"08:00"}, "start_date": futureDate(1), "end_date": futureDate(2),
	})

	// 7 файлов настроек + 3 собственных файла первого напоминания = 10.
	for i := 0; i < 7; i++ {
		uploadAndConfirmFile(t, tokens.AccessToken, "reminder_plan_file", plan.ID, "image/png")
	}
	for i := 0; i < 3; i++ {
		uploadAndConfirmFile(t, tokens.AccessToken, "reminder_file", plan.Reminders[0].ID, "image/png")
	}

	// Ещё один файл настроек — сумма стала бы 11.
	extraPlan := doRequest(t, http.MethodPost, "/files/upload-url", map[string]any{
		"owner_type": "reminder_plan_file", "owner_id": plan.ID, "content_type": "image/png",
	}, tokens.AccessToken)
	var extraPlanUpload uploadURLResponse
	extraPlan.decode(t, &extraPlanUpload)
	require.Equal(t, http.StatusConflict, doRequest(t, http.MethodPost, "/files/"+extraPlanUpload.FileID+"/complete", nil, tokens.AccessToken).status)

	// Ещё один собственный файл у любого напоминания этих настроек — тоже.
	extraOwn := doRequest(t, http.MethodPost, "/files/upload-url", map[string]any{
		"owner_type": "reminder_file", "owner_id": plan.Reminders[1].ID, "content_type": "image/png",
	}, tokens.AccessToken)
	var extraOwnUpload uploadURLResponse
	extraOwn.decode(t, &extraOwnUpload)
	require.Equal(t, 7, countRows(t, `SELECT COUNT(*) FROM file WHERE owner_type = 'reminder_plan_file' AND confirmed_at IS NOT NULL`))
	// У второго напоминания своих файлов нет, поэтому до 10 - 7 = 3 допустимы.
	for i := 0; i < 2; i++ {
		uploadAndConfirmFile(t, tokens.AccessToken, "reminder_file", plan.Reminders[1].ID, "image/png")
	}
	require.Equal(t, http.StatusNoContent, doRequest(t, http.MethodPost, "/files/"+extraOwnUpload.FileID+"/complete", nil, tokens.AccessToken).status)
	over := doRequest(t, http.MethodPost, "/files/upload-url", map[string]any{
		"owner_type": "reminder_file", "owner_id": plan.Reminders[1].ID, "content_type": "image/png",
	}, tokens.AccessToken)
	var overUpload uploadURLResponse
	over.decode(t, &overUpload)
	require.Equal(t, http.StatusConflict, doRequest(t, http.MethodPost, "/files/"+overUpload.FileID+"/complete", nil, tokens.AccessToken).status)
}

// Замена одного напоминания переносит файлы новой записи ссылками.
func TestReminderFiles_ReferenceRowsOnDetach(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	plan := createPlan(t, tokens.AccessToken, map[string]any{
		"pet_id": petID, "type": "weight", "value": map[string]any{"amount": 4.2},
		"frequency_type": "daily", "times": []string{"08:00"}, "start_date": futureDate(1), "end_date": futureDate(2),
	})
	uploadAndConfirmFile(t, tokens.AccessToken, "reminder_plan_file", plan.ID, "image/jpeg")
	uploadAndConfirmFile(t, tokens.AccessToken, "reminder_file", plan.Reminders[0].ID, "image/jpeg")

	newPlanID := uuid.NewString()
	resp := doRequest(t, http.MethodPost, "/reminders/"+plan.Reminders[0].ID+"/detach?tz=UTC", map[string]any{
		"plan": map[string]any{
			"id": newPlanID, "pet_id": petID, "type": "weight", "value": map[string]any{"amount": 6.0},
			"frequency_type": "once", "times": []string{"10:00"}, "start_date": futureDate(10),
		},
	}, tokens.AccessToken, map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equalf(t, http.StatusCreated, resp.status, "%s", resp.body)
	var result struct {
		Plan planBody `json:"plan"`
	}
	resp.decode(t, &result)
	require.Len(t, result.Plan.Files, 2)

	// У исходных настроек остался один файл (файл настроек), собственный файл
	// заменённого напоминания удалён; объектов в хранилище по-прежнему два.
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM file WHERE owner_type = 'reminder_plan_file' AND owner_id = $1`, plan.ID))
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM file WHERE owner_type = 'reminder_file'`))
	require.Equal(t, 2, countRows(t, `SELECT COUNT(DISTINCT object_key) FROM file`))
	require.Equal(t, 3, countRows(t, `SELECT COUNT(*) FROM file`))
}

// --- I, J. Ближайшие напоминания ---

func TestRemindersUpcoming(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	catID := createPet(t, tokens.AccessToken, "Барсик")
	dogID := createPet(t, tokens.AccessToken, "Рекс")

	cat := createPlan(t, tokens.AccessToken, map[string]any{
		"pet_id": catID, "type": "weight", "value": map[string]any{"amount": 4.2},
		"frequency_type": "daily", "times": []string{"08:00"}, "start_date": futureDate(1), "end_date": futureDate(3),
	})
	dog := createOncePlan(t, tokens.AccessToken, dogID, "weight", map[string]any{"amount": 12.0}, futureDate(2), "07:00")

	resp := doRequest(t, http.MethodGet, "/reminders/upcoming?limit=3", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	var body struct {
		Items []struct {
			ID      string `json:"id"`
			PlanID  string `json:"plan_id"`
			PetID   string `json:"pet_id"`
			PetName string `json:"pet_name"`
			Type    string `json:"type"`
		} `json:"items"`
	}
	resp.decode(t, &body)
	require.Len(t, body.Items, 3)
	require.Equal(t, cat.Reminders[0].ID, body.Items[0].ID)
	require.Equal(t, dog.Reminders[0].ID, body.Items[1].ID)
	require.Equal(t, "Рекс", body.Items[1].PetName)
	require.Equal(t, cat.Reminders[1].ID, body.Items[2].ID)

	// По умолчанию — все (до 64), по питомцу — только его напоминания.
	all := doRequest(t, http.MethodGet, "/reminders/upcoming", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, all.status)
	all.decode(t, &body)
	require.Len(t, body.Items, 4)

	petResp := doRequest(t, http.MethodGet, "/pet/"+catID+"/reminders/upcoming?limit=2", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, petResp.status, "%s", petResp.body)
	var petBody struct {
		Items []struct {
			ID     string `json:"id"`
			PlanID string `json:"plan_id"`
			Type   string `json:"type"`
		} `json:"items"`
	}
	petResp.decode(t, &petBody)
	require.Len(t, petBody.Items, 2)
	require.Equal(t, cat.Reminders[0].ID, petBody.Items[0].ID)

	// Невалидный limit и чужой/мягко удалённый питомец.
	require.Equal(t, http.StatusBadRequest, doUnvalidatedRequest(t, http.MethodGet, "/reminders/upcoming?limit=65", nil, tokens.AccessToken).status)
	require.Equal(t, http.StatusBadRequest, doUnvalidatedRequest(t, http.MethodGet, "/pet/"+catID+"/reminders/upcoming?limit=11", nil, tokens.AccessToken).status)
	stranger := registerUser(t, uniqueLogin(t), "password123")
	require.Equal(t, http.StatusNotFound, doRequest(t, http.MethodGet, "/pet/"+catID+"/reminders/upcoming", nil, stranger.AccessToken).status)
	require.Equal(t, http.StatusUnauthorized, doRequest(t, http.MethodGet, "/reminders/upcoming", nil, "").status)

	// Мягко удалённый питомец исчезает из выдачи и даёт 404.
	require.Equal(t, http.StatusNoContent, doRequest(t, http.MethodDelete, "/pet/"+catID, nil, tokens.AccessToken).status)
	require.Equal(t, http.StatusNotFound, doRequest(t, http.MethodGet, "/pet/"+catID+"/reminders/upcoming", nil, tokens.AccessToken).status)
	after := doRequest(t, http.MethodGet, "/reminders/upcoming", nil, tokens.AccessToken)
	after.decode(t, &body)
	require.Len(t, body.Items, 1)
	require.Equal(t, dog.Reminders[0].ID, body.Items[0].ID)
}

// --- Календарь: факты и напоминания вместе ---

func TestCalendar_MixesFactsAndReminders(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	day := futureDate(3)
	plan := createOncePlan(t, tokens.AccessToken, petID, "weight", map[string]any{"amount": 4.2}, day, "12:00")
	// Факт в тот же день (допустим, как прошлое, — день берём из прошлого
	// диапазона отдельно): здесь проверяем только счётчики напоминаний.
	createEvent(t, tokens.AccessToken, petID, "2024-01-01T10:00:00Z", "weight", map[string]any{"amount": 4.0})

	cal := doRequest(t, http.MethodGet, "/activities/calendar?from="+day+"&to="+day+"&tz=UTC", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, cal.status, "%s", cal.body)
	var calBody struct {
		Items []struct {
			Date         string `json:"date"`
			Count        int    `json:"count"`
			HasReminders bool   `json:"has_reminders"`
		} `json:"items"`
	}
	cal.decode(t, &calBody)
	require.Len(t, calBody.Items, 1)
	require.Equal(t, 1, calBody.Items[0].Count)
	require.True(t, calBody.Items[0].HasReminders)

	past := doRequest(t, http.MethodGet, "/activities/calendar?from=2024-01-01&to=2024-01-01&tz=UTC", nil, tokens.AccessToken)
	past.decode(t, &calBody)
	require.Equal(t, 1, calBody.Items[0].Count)
	require.False(t, calBody.Items[0].HasReminders)

	// Закрытое напоминание в календаре не показывается.
	require.Equal(t, http.StatusNoContent, doRequest(t, http.MethodDelete, "/reminders/"+plan.Reminders[0].ID, nil, tokens.AccessToken).status)
	cal = doRequest(t, http.MethodGet, "/activities/calendar?from="+day+"&to="+day+"&tz=UTC", nil, tokens.AccessToken)
	cal.decode(t, &calBody)
	require.Equal(t, 0, calBody.Items[0].Count)
	require.False(t, calBody.Items[0].HasReminders)
}

// Наступившее незавершённое напоминание остаётся в календаре, пока
// пользователь его не закроет.
func TestCalendar_DayKeepsPassedOpenReminder(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	plan := createOncePlan(t, tokens.AccessToken, petID, "weight", map[string]any{"amount": 4.2}, futureDate(1), "08:00")
	pastReminder(t, plan.Reminders[0].ID)

	date := time.Now().UTC().Add(-time.Hour).Format("2006-01-02")
	day := doRequest(t, http.MethodGet, "/activities/day?date="+date+"&tz=UTC", nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, day.status)
	var body struct {
		Items []struct {
			ItemType string `json:"item_type"`
			ID       string `json:"id"`
			PlanID   string `json:"plan_id"`
		} `json:"items"`
	}
	day.decode(t, &body)
	require.Len(t, body.Items, 1)
	require.Equal(t, "reminder", body.Items[0].ItemType)
	require.Equal(t, plan.Reminders[0].ID, body.Items[0].ID)
	require.Equal(t, plan.ID, body.Items[0].PlanID)
}

// --- Источники: лекарство и вакцинация ---

func TestReminderPlan_MedicationSourceRules(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	createMed := doRequest(t, http.MethodPost, "/pet/"+petID+"/medications?tz=UTC", map[string]any{
		"name": "Amoxicillin", "dosage": "1 tablet", "frequency_type": "daily",
		"times":      []map[string]any{{"time": "08:00", "dose_note": "2 пипетки"}, {"time": "20:00", "dose_note": "1 пипетка"}},
		"start_date": futureDate(1), "end_date": futureDate(2), "add_reminders": true,
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, createMed.status, "%s", createMed.body)
	var med idResponse
	createMed.decode(t, &med)
	medications := listMedications(t, tokens.AccessToken, petID)
	planID := *medications[0].ReminderPlanID
	path := "/reminder-plans/" + planID + "?tz=UTC"

	// Тип, значение и заметка связанных настроек не принимаются.
	for name, body := range map[string]map[string]any{
		"type":  {"type": "other", "value": map[string]any{"label": "x"}},
		"value": {"value": map[string]any{"name": "Другое"}},
		"notes": {"notes": "заметка"},
	} {
		resp := doRequest(t, http.MethodPatch, path, body, tokens.AccessToken)
		require.Equalf(t, http.StatusBadRequest, resp.status, "%s: %s", name, resp.body)
	}
	// Разовое расписание у лекарства недопустимо.
	once := doRequest(t, http.MethodPatch, path, map[string]any{
		"frequency_type": "once", "times": []string{"08:00"}, "start_date": futureDate(5),
	}, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, once.status)

	// Замена одного напоминания допустима для лекарства; тип обязан
	// совпадать с типом напоминания.
	plan := getReminderPlan(t, tokens.AccessToken, planID)
	wrongType := doRequest(t, http.MethodPost, "/reminders/"+plan.Reminders[0].ID+"/detach", map[string]any{
		"event": map[string]any{"pet_id": petID, "date": "2024-01-01T08:00:00Z", "type": "weight", "value": map[string]any{"amount": 4.0}},
	}, tokens.AccessToken, map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equal(t, http.StatusBadRequest, wrongType.status)

	// Расписание из календаря применяется к лекарству: поля расписания
	// заменяются, у оставшегося времени сохраняется его доза, у добавленного
	// доза пуста.
	patch := doRequest(t, http.MethodPatch, path, map[string]any{
		"frequency_type": "daily", "times": []string{"08:00", "22:00"},
		"start_date": futureDate(1), "end_date": futureDate(3),
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, patch.status, "%s", patch.body)

	list := doRequest(t, http.MethodGet, "/pet/"+petID+"/medications?tz=UTC", nil, tokens.AccessToken)
	var listBody struct {
		Items []struct {
			EndDate *string `json:"end_date"`
			Times   []struct {
				Time     string  `json:"time"`
				DoseNote *string `json:"dose_note"`
			} `json:"times"`
		} `json:"items"`
	}
	list.decode(t, &listBody)
	require.Len(t, listBody.Items, 1)
	require.Equal(t, futureDate(3), *listBody.Items[0].EndDate)
	require.Len(t, listBody.Items[0].Times, 2)
	require.Equal(t, "08:00", listBody.Items[0].Times[0].Time)
	require.NotNil(t, listBody.Items[0].Times[0].DoseNote)
	require.Equal(t, "2 пипетки", *listBody.Items[0].Times[0].DoseNote)
	require.Equal(t, "22:00", listBody.Items[0].Times[1].Time)
	require.Nil(t, listBody.Items[0].Times[1].DoseNote)

	changed := getReminderPlan(t, tokens.AccessToken, planID)
	require.Len(t, changed.Reminders, 6) // 3 дня x 2 времени
	first := getReminder(t, tokens.AccessToken, changed.Reminders[0].ID)
	require.NotNil(t, first.Notes)
	require.Equal(t, "2 пипетки", *first.Notes)
	require.Equal(t, "Amoxicillin", *first.PlanSourceTitle)

	// Название и дозировка лекарства применяются к настройкам.
	rename := doRequest(t, http.MethodPatch, "/medications/"+med.ID+"?tz=UTC", map[string]any{"name": "Amoxicillin 2"}, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, rename.status)
	renamed := getReminderPlan(t, tokens.AccessToken, planID)
	require.Equal(t, "Amoxicillin 2", renamed.Value.Name)
	require.Equal(t, "Amoxicillin 2", *renamed.SourceTitle)

	// Удаление лекарства удаляет настройки и напоминания.
	require.Equal(t, http.StatusNoContent, doRequest(t, http.MethodDelete, "/medications/"+med.ID, nil, tokens.AccessToken).status)
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder`))
}

func TestReminderPlan_VaccinationSourceRules(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	createVac := doRequest(t, http.MethodPost, "/pet/"+petID+"/vaccinations?tz=UTC", map[string]any{
		"name": "Rabies", "administered_date": "2024-01-01", "next_date": futureDate(30),
		"add_reminder_on_next": true, "next_time": "09:00",
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, createVac.status, "%s", createVac.body)
	var vac struct {
		ID         string  `json:"id"`
		NextPlanID *string `json:"next_plan_id"`
	}
	createVac.decode(t, &vac)
	require.NotNil(t, vac.NextPlanID)
	plan := getReminderPlan(t, tokens.AccessToken, *vac.NextPlanID)
	path := "/reminder-plans/" + plan.ID + "?tz=UTC"

	// Замена одного напоминания вакцинации недоступна (409).
	detach := doRequest(t, http.MethodPost, "/reminders/"+plan.Reminders[0].ID+"/detach", map[string]any{
		"event": map[string]any{"pet_id": petID, "date": "2024-01-01T08:00:00Z", "type": "other", "value": map[string]any{"label": "x"}},
	}, tokens.AccessToken, map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equalf(t, http.StatusConflict, detach.status, "%s", detach.body)

	// Допустимо только разовое расписание; подпись и тип не меняются.
	daily := doRequest(t, http.MethodPatch, path, map[string]any{
		"frequency_type": "daily", "times": []string{"09:00"}, "start_date": futureDate(30),
	}, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, daily.status)
	label := doRequest(t, http.MethodPatch, path, map[string]any{"value": map[string]any{"label": "Другое"}}, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, label.status)

	// Перенос из календаря записывает новую дату в next_date прививки.
	newDate := futureDate(40)
	patch := doRequest(t, http.MethodPatch, path, map[string]any{
		"frequency_type": "once", "times": []string{"11:15"}, "start_date": newDate,
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, patch.status, "%s", patch.body)
	list := doRequest(t, http.MethodGet, "/pet/"+petID+"/vaccinations", nil, tokens.AccessToken)
	var listBody struct {
		Items []struct {
			NextDate   *string `json:"next_date"`
			NextPlanID *string `json:"next_plan_id"`
		} `json:"items"`
	}
	list.decode(t, &listBody)
	require.Equal(t, newDate, *listBody.Items[0].NextDate)

	// Отметка «выполнено» создаёт факт и очищает next_date; ссылка на
	// удалённые настройки становится null.
	moved := getReminderPlan(t, tokens.AccessToken, plan.ID)
	pastReminder(t, moved.Reminders[0].ID)
	done := doRequest(t, http.MethodPost, "/reminders/"+moved.Reminders[0].ID+"/complete", map[string]any{"done": true}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, done.status, "%s", done.body)
	list = doRequest(t, http.MethodGet, "/pet/"+petID+"/vaccinations", nil, tokens.AccessToken)
	// Поля с null опускаются в ответе, поэтому декодируем в свежую структуру.
	listBody.Items = nil
	list.decode(t, &listBody)
	require.Nil(t, listBody.Items[0].NextDate)
	require.Nil(t, listBody.Items[0].NextPlanID)
	// Факт на дату введения (создаётся вместе с прививкой) и факт отметки
	// «выполнено».
	require.Len(t, listPetEvents(t, tokens.AccessToken, petID), 2)
}

// --- Удаление питомца ---

func TestPetDelete_HardDeletesRemindersAndFiles(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	plan := createPlan(t, tokens.AccessToken, map[string]any{
		"pet_id": petID, "type": "weight", "value": map[string]any{"amount": 4.2},
		"frequency_type": "daily", "times": []string{"08:00"}, "start_date": futureDate(1), "end_date": futureDate(2),
	})
	uploadAndConfirmFile(t, tokens.AccessToken, "reminder_plan_file", plan.ID, "image/jpeg")
	uploadAndConfirmFile(t, tokens.AccessToken, "reminder_file", plan.Reminders[0].ID, "image/jpeg")
	createEvent(t, tokens.AccessToken, petID, "2024-01-01T08:00:00Z", "weight", map[string]any{"amount": 4.0})

	require.Equal(t, http.StatusNoContent, doRequest(t, http.MethodDelete, "/pet/"+petID, nil, tokens.AccessToken).status)

	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder`))
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM file WHERE owner_type IN ('reminder_plan_file', 'reminder_file')`))
	// Факты питомца удаляются мягко по общему правилу.
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM event WHERE pet_id = $1`, petID))
}

// --- События — только факты ---

func TestEvent_FutureDateRejectedAndNoNotificationsField(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	future := doRequest(t, http.MethodPost, "/events", map[string]any{
		"pet_id": petID, "date": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"type": "weight", "value": map[string]any{"amount": 4.2},
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, future.status, "%s", future.body)

	// В пределах допуска (5 минут) дата допустима.
	withinTolerance := doRequest(t, http.MethodPost, "/events", map[string]any{
		"pet_id": petID, "date": time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339),
		"type": "weight", "value": map[string]any{"amount": 4.2},
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, withinTolerance.status, "%s", withinTolerance.body)
	var created struct {
		ID string `json:"id"`
	}
	withinTolerance.decode(t, &created)
	require.NotContains(t, string(withinTolerance.body), "notifications_enabled")

	// PATCH: перенос даты в будущее — 400.
	patch := doRequest(t, http.MethodPatch, "/events/"+created.ID, map[string]any{
		"pet_id": petID, "date": time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339),
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusBadRequest, patch.status, "%s", patch.body)
}

// Все эндпоинты чтения календаря и графиков требуют tz.
func TestTimeZoneRequiredEverywhere(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	paths := []string{
		fmt.Sprintf("/activities?pet_id=%s&from=2024-01-01&to=2024-01-02", petID),
		"/activities/calendar?from=2024-01-01&to=2024-01-02",
		"/activities/day?date=2024-01-01",
		fmt.Sprintf("/events/stats?pet_id=%s&from=2024-01-01&to=2024-01-02&bucket=day", petID),
		"/pet/" + petID + "/medications",
	}
	for _, path := range paths {
		resp := doUnvalidatedRequest(t, http.MethodGet, path, nil, tokens.AccessToken)
		require.Equalf(t, http.StatusBadRequest, resp.status, "%s: %s", path, resp.body)
	}
}
