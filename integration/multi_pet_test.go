//go:build integration

package integration

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Несколько питомцев в событии и напоминании: запись одна, связь с
// питомцами — таблицы event_pet и reminder_plan_pet.

func createSpeciesPet(t *testing.T, token, name, species string) string {
	t.Helper()
	resp := doRequest(t, http.MethodPost, "/pet", map[string]any{"name": name, "species": species}, token)
	require.Equalf(t, http.StatusCreated, resp.status, "%s", resp.body)
	var pet struct {
		ID string `json:"id"`
	}
	resp.decode(t, &pet)
	return pet.ID
}

type eventPetsBody struct {
	ID   string       `json:"id"`
	Pets []petRefBody `json:"pets"`
}

func petIDsOf(pets []petRefBody) []string {
	ids := make([]string, len(pets))
	for i, p := range pets {
		ids[i] = p.PetID
	}
	return ids
}

func postEventPets(t *testing.T, token string, petIDs []string, eventType string, value map[string]any, extra ...map[string]string) apiResponse {
	t.Helper()
	// Часть вызовов намеренно нарушает контракт (повторы, пустой набор).
	return doUnvalidatedRequest(t, http.MethodPost, "/events", map[string]any{
		"pet_ids": petIDs,
		"date":    "2024-01-01T10:00:00Z",
		"type":    eventType,
		"value":   value,
	}, token, extra...)
}

func getEventPets(t *testing.T, token, eventID string) eventPetsBody {
	t.Helper()
	resp := doRequest(t, http.MethodGet, "/events/"+eventID, nil, token)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	var body eventPetsBody
	resp.decode(t, &body)
	return body
}

func patchEventPets(t *testing.T, token, eventID string, body map[string]any) apiResponse {
	t.Helper()
	return doUnvalidatedRequest(t, http.MethodPatch, "/events/"+eventID, body, token)
}

var feedingValue = map[string]any{"amount": 50.0, "unit": "g", "food": "dry"}

func TestMultiPet_CreateReadAndPerPetQueries(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	cat := createSpeciesPet(t, tokens.AccessToken, "Барсик", "CAT")
	dog := createSpeciesPet(t, tokens.AccessToken, "Рекс", "DOG")
	other := createSpeciesPet(t, tokens.AccessToken, "Мурка", "CAT")

	resp := postEventPets(t, tokens.AccessToken, []string{cat, dog}, "feeding", feedingValue)
	require.Equalf(t, http.StatusCreated, resp.status, "%s", resp.body)
	var created eventPetsBody
	resp.decode(t, &created)
	require.Equal(t, []string{cat, dog}, petIDsOf(created.Pets))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM event`))
	require.Equal(t, 2, countRows(t, `SELECT COUNT(*) FROM event_pet`))

	require.Equal(t, []string{cat, dog}, petIDsOf(getEventPets(t, tokens.AccessToken, created.ID).Pets))

	// Запись попадает в выборку каждого из своих питомцев и не попадает к
	// чужому.
	require.Len(t, listPetEvents(t, tokens.AccessToken, cat), 1)
	require.Len(t, listPetEvents(t, tokens.AccessToken, dog), 1)
	require.Empty(t, listPetEvents(t, tokens.AccessToken, other))

	for _, petID := range []string{cat, dog} {
		act := doRequest(t, http.MethodGet, "/activities?pet_id="+petID+"&from=2024-01-01&to=2024-01-01&tz=UTC", nil, tokens.AccessToken)
		require.Equalf(t, http.StatusOK, act.status, "%s", act.body)
		var actBody struct {
			Items []struct {
				Events []eventPetsBody `json:"events"`
			} `json:"items"`
		}
		act.decode(t, &actBody)
		require.Len(t, actBody.Items[0].Events, 1)
		require.Len(t, actBody.Items[0].Events[0].Pets, 2)
	}

	// Календарь: одна карточка со всеми питомцами, на сетке — один раз.
	day := doRequest(t, http.MethodGet, "/activities/day?date=2024-01-01&tz=UTC", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, day.status, "%s", day.body)
	var dayBody struct {
		Items []eventPetsBody `json:"items"`
	}
	day.decode(t, &dayBody)
	require.Len(t, dayBody.Items, 1)
	require.Len(t, dayBody.Items[0].Pets, 2)

	cal := doRequest(t, http.MethodGet, "/activities/calendar?from=2024-01-01&to=2024-01-01&tz=UTC", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, cal.status, "%s", cal.body)
	var calBody struct {
		Items []struct {
			Count int `json:"count"`
		} `json:"items"`
	}
	cal.decode(t, &calBody)
	require.Equal(t, 1, calBody.Items[0].Count)
}

func TestMultiPet_CreateValidation(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	stranger := registerUser(t, uniqueLogin(t), "password123")
	cat := createSpeciesPet(t, tokens.AccessToken, "Барсик", "CAT")
	dog := createSpeciesPet(t, tokens.AccessToken, "Рекс", "DOG")
	fish := createSpeciesPet(t, tokens.AccessToken, "Немо", "FISH")
	foreign := createSpeciesPet(t, stranger.AccessToken, "Чужой", "CAT")
	deleted := createSpeciesPet(t, tokens.AccessToken, "Удалён", "CAT")
	require.Equal(t, http.StatusNoContent, doRequest(t, http.MethodDelete, "/pet/"+deleted, nil, tokens.AccessToken).status)

	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = uuid.NewString()
	}

	cases := []struct {
		name      string
		petIDs    []string
		eventType string
		value     map[string]any
		want      int
	}{
		{"пересечение типов: heat_cycle неприменим к рыбе", []string{cat, fish}, "heat_cycle", map[string]any{"phase": "started"}, http.StatusBadRequest},
		{"пересечение типов: подходящие виды", []string{cat, dog}, "heat_cycle", map[string]any{"phase": "started"}, http.StatusCreated},
		{"измерительный тип с несколькими питомцами: weight", []string{cat, dog}, "weight", map[string]any{"amount": 4.0}, http.StatusBadRequest},
		{"измерительный тип с несколькими питомцами: temperature", []string{cat, dog}, "temperature", map[string]any{"amount": 38.0, "kind": "body"}, http.StatusBadRequest},
		{"измерительный тип с одним питомцем", []string{cat}, "weight", map[string]any{"amount": 4.0}, http.StatusCreated},
		{"значение словаря неприменимо ко всем", []string{fish, dog}, "hygiene", map[string]any{"procedure": "water_change"}, http.StatusBadRequest},
		{"чужой питомец", []string{cat, foreign}, "feeding", feedingValue, http.StatusNotFound},
		{"несуществующий питомец", []string{cat, uuid.NewString()}, "feeding", feedingValue, http.StatusNotFound},
		{"мягко удалённый питомец", []string{cat, deleted}, "feeding", feedingValue, http.StatusBadRequest},
		{"повторы", []string{cat, cat}, "feeding", feedingValue, http.StatusBadRequest},
		{"пустой набор", []string{}, "feeding", feedingValue, http.StatusBadRequest},
		{"более 10 питомцев", eleven, "feeding", feedingValue, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := postEventPets(t, tokens.AccessToken, c.petIDs, c.eventType, c.value)
			require.Equalf(t, c.want, resp.status, "%s", resp.body)
		})
	}
}

// Ключ идемпотентности уникален в пределах пользователя: повтор с теми же
// питомцами возвращает ту же запись.
func TestMultiPet_IdempotencyKeyPerUser(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	cat := createSpeciesPet(t, tokens.AccessToken, "Барсик", "CAT")
	dog := createSpeciesPet(t, tokens.AccessToken, "Рекс", "DOG")
	key := map[string]string{"Idempotency-Key": uuid.NewString()}

	first := postEventPets(t, tokens.AccessToken, []string{cat, dog}, "feeding", feedingValue, key)
	require.Equalf(t, http.StatusCreated, first.status, "%s", first.body)
	second := postEventPets(t, tokens.AccessToken, []string{cat, dog}, "feeding", feedingValue, key)
	require.Equalf(t, http.StatusCreated, second.status, "%s", second.body)

	var a, b eventPetsBody
	first.decode(t, &a)
	second.decode(t, &b)
	require.Equal(t, a.ID, b.ID)
	require.Len(t, b.Pets, 2)
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM event`))
}

func TestMultiPet_AttachDetachAndEmptySet(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	stranger := registerUser(t, uniqueLogin(t), "password123")
	cat := createSpeciesPet(t, tokens.AccessToken, "Барсик", "CAT")
	dog := createSpeciesPet(t, tokens.AccessToken, "Рекс", "DOG")
	fish := createSpeciesPet(t, tokens.AccessToken, "Немо", "FISH")
	foreign := createSpeciesPet(t, stranger.AccessToken, "Чужой", "CAT")

	resp := postEventPets(t, tokens.AccessToken, []string{cat}, "feeding", feedingValue)
	require.Equal(t, http.StatusCreated, resp.status)
	var created eventPetsBody
	resp.decode(t, &created)

	// Привязка второго питомца: событие не копируется, появляется у питомца.
	patch := patchEventPets(t, tokens.AccessToken, created.ID, map[string]any{"pet_ids": []string{cat, dog}})
	require.Equalf(t, http.StatusNoContent, patch.status, "%s", patch.body)
	require.Equal(t, []string{cat, dog}, petIDsOf(getEventPets(t, tokens.AccessToken, created.ID).Pets))
	require.Len(t, listPetEvents(t, tokens.AccessToken, dog), 1)
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM event`))

	// Повтор того же набора идемпотентен.
	again := patchEventPets(t, tokens.AccessToken, created.ID, map[string]any{"pet_ids": []string{cat, dog}})
	require.Equal(t, http.StatusNoContent, again.status)
	require.Equal(t, 2, countRows(t, `SELECT COUNT(*) FROM event_pet`))

	// Изменение полей и набора в одном запросе.
	both := patchEventPets(t, tokens.AccessToken, created.ID, map[string]any{
		"pet_ids": []string{dog},
		"notes":   "общая заметка",
	})
	require.Equalf(t, http.StatusNoContent, both.status, "%s", both.body)
	require.Equal(t, []string{dog}, petIDsOf(getEventPets(t, tokens.AccessToken, created.ID).Pets))
	require.Empty(t, listPetEvents(t, tokens.AccessToken, cat))
	require.Len(t, listPetEvents(t, tokens.AccessToken, dog), 1)

	// Пустой набор, повторы, чужой питомец, неприменимый вид.
	require.Equal(t, http.StatusBadRequest, patchEventPets(t, tokens.AccessToken, created.ID, map[string]any{"pet_ids": []string{}}).status)
	require.Equal(t, http.StatusBadRequest, patchEventPets(t, tokens.AccessToken, created.ID, map[string]any{"pet_ids": []string{dog, dog}}).status)
	require.Equal(t, http.StatusNotFound, patchEventPets(t, tokens.AccessToken, created.ID, map[string]any{"pet_ids": []string{dog, foreign}}).status)
	require.Equal(t, http.StatusBadRequest, patchEventPets(t, tokens.AccessToken, created.ID, map[string]any{
		"pet_ids": []string{dog, fish},
		"type":    "hygiene", "value": map[string]any{"procedure": "water_change"},
	}).status)
	// Отклонённые запросы набор питомцев не меняют.
	require.Equal(t, []string{dog}, petIDsOf(getEventPets(t, tokens.AccessToken, created.ID).Pets))

	// Чужое событие недоступно для изменения.
	require.Equal(t, http.StatusNotFound, patchEventPets(t, stranger.AccessToken, created.ID, map[string]any{"notes": "x"}).status)
}

// Привязка второго питомца к событию с измерительным типом отклоняется.
func TestMultiPet_AttachToMeasurementEventRejected(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	cat := createSpeciesPet(t, tokens.AccessToken, "Барсик", "CAT")
	dog := createSpeciesPet(t, tokens.AccessToken, "Рекс", "DOG")
	id := createEventReturningID(t, tokens.AccessToken, cat, "2024-01-01T10:00:00Z", "weight", map[string]any{"amount": 4.0})

	require.Equal(t, http.StatusBadRequest, patchEventPets(t, tokens.AccessToken, id, map[string]any{"pet_ids": []string{cat, dog}}).status)
	require.Equal(t, []string{cat}, petIDsOf(getEventPets(t, tokens.AccessToken, id).Pets))
}

func TestMultiPet_DeleteEventWholeAndSoftDeletePet(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	cat := createSpeciesPet(t, tokens.AccessToken, "Барсик", "CAT")
	dog := createSpeciesPet(t, tokens.AccessToken, "Рекс", "DOG")

	resp := postEventPets(t, tokens.AccessToken, []string{cat, dog}, "feeding", feedingValue)
	require.Equal(t, http.StatusCreated, resp.status)
	var shared eventPetsBody
	resp.decode(t, &shared)

	// Мягкое удаление питомца: запись остаётся у другого, видимые питомцы
	// только не удалённые.
	require.Equal(t, http.StatusNoContent, doRequest(t, http.MethodDelete, "/pet/"+cat, nil, tokens.AccessToken).status)
	require.Equal(t, []string{dog}, petIDsOf(getEventPets(t, tokens.AccessToken, shared.ID).Pets))
	require.Len(t, listPetEvents(t, tokens.AccessToken, dog), 1)
	require.Equal(t, 2, countRows(t, `SELECT COUNT(*) FROM event_pet`), "связь с удалённым питомцем сохраняется, но скрыта")

	// Остался без видимых питомцев — запись нигде не показывается.
	require.Equal(t, http.StatusNoContent, doRequest(t, http.MethodDelete, "/pet/"+dog, nil, tokens.AccessToken).status)
	require.Equal(t, http.StatusNotFound, doRequest(t, http.MethodGet, "/events/"+shared.ID, nil, tokens.AccessToken).status)

	// Удаление события целиком у всех питомцев.
	cat2 := createSpeciesPet(t, tokens.AccessToken, "Мурка", "CAT")
	dog2 := createSpeciesPet(t, tokens.AccessToken, "Бобик", "DOG")
	resp = postEventPets(t, tokens.AccessToken, []string{cat2, dog2}, "feeding", feedingValue)
	require.Equal(t, http.StatusCreated, resp.status)
	var second eventPetsBody
	resp.decode(t, &second)
	require.Equal(t, http.StatusNoContent, doRequest(t, http.MethodDelete, "/events/"+second.ID, nil, tokens.AccessToken).status)
	require.Empty(t, listPetEvents(t, tokens.AccessToken, cat2))
	require.Empty(t, listPetEvents(t, tokens.AccessToken, dog2))
}

func TestMultiPet_ReminderPlanLifecycle(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	cat := createSpeciesPet(t, tokens.AccessToken, "Барсик", "CAT")
	dog := createSpeciesPet(t, tokens.AccessToken, "Рекс", "DOG")
	fish := createSpeciesPet(t, tokens.AccessToken, "Немо", "FISH")

	// Измерительный тип с несколькими питомцами отклоняется.
	bad := postReminderPlan(t, tokens.AccessToken, map[string]any{
		"id": uuid.NewString(), "pet_ids": []string{cat, dog}, "type": "weight", "value": map[string]any{"amount": 4.0},
		"frequency_type": "once", "times": []string{"09:00"}, "start_date": futureDate(3),
	}, "UTC")
	require.Equal(t, http.StatusBadRequest, bad.status)

	plan := createPlan(t, tokens.AccessToken, map[string]any{
		"pet_ids": []string{cat, dog}, "type": "feeding", "value": feedingValue,
		"frequency_type": "once", "times": []string{"09:00"}, "start_date": futureDate(3),
	})
	require.Equal(t, []string{cat, dog}, petIDsOf(plan.Pets))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM reminder`))

	// Ближайшие по питомцу: общее напоминание у каждого.
	for _, petID := range []string{cat, dog} {
		up := doRequest(t, http.MethodGet, "/pet/"+petID+"/reminders/upcoming", nil, tokens.AccessToken)
		require.Equalf(t, http.StatusOK, up.status, "%s", up.body)
		var upBody struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		up.decode(t, &upBody)
		require.Len(t, upBody.Items, 1)
	}
	require.Empty(t, upcomingFor(t, tokens.AccessToken, fish))

	// Привязка и отвязка питомцев без пересоздания напоминаний.
	patch := doRequest(t, http.MethodPatch, "/reminder-plans/"+plan.ID+"?tz=UTC", map[string]any{"pet_ids": []string{dog, fish}}, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, patch.status, "%s", patch.body)
	got := getReminderPlan(t, tokens.AccessToken, plan.ID)
	require.ElementsMatch(t, []string{dog, fish}, petIDsOf(got.Pets))
	require.Len(t, got.Reminders, 1)
	require.Equal(t, plan.Reminders[0].ID, got.Reminders[0].ID)
	require.Empty(t, upcomingFor(t, tokens.AccessToken, cat))

	empty := doUnvalidatedRequest(t, http.MethodPatch, "/reminder-plans/"+plan.ID+"?tz=UTC", map[string]any{"pet_ids": []string{}}, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, empty.status)

	// «Выполнено»: один факт на видимых питомцев настроек.
	pastReminder(t, plan.Reminders[0].ID)
	done := doRequest(t, http.MethodPost, "/reminders/"+plan.Reminders[0].ID+"/complete", map[string]any{"done": true}, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, done.status, "%s", done.body)
	var doneBody struct {
		FactEventID string `json:"fact_event_id"`
	}
	done.decode(t, &doneBody)
	fact := getEventPets(t, tokens.AccessToken, doneBody.FactEventID)
	require.ElementsMatch(t, []string{dog, fish}, petIDsOf(fact.Pets))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM event`))
}

func upcomingFor(t *testing.T, token, petID string) []string {
	t.Helper()
	resp := doRequest(t, http.MethodGet, "/pet/"+petID+"/reminders/upcoming", nil, token)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	var body struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	resp.decode(t, &body)
	ids := make([]string, len(body.Items))
	for i, it := range body.Items {
		ids[i] = it.ID
	}
	return ids
}

// Мягкое удаление питомца: связи с настройками удаляются жёстко, настройки
// без питомцев — вместе с напоминаниями; общие настройки остаются.
func TestMultiPet_SoftDeletePetUnlinksReminderPlans(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	cat := createSpeciesPet(t, tokens.AccessToken, "Барсик", "CAT")
	dog := createSpeciesPet(t, tokens.AccessToken, "Рекс", "DOG")

	shared := createPlan(t, tokens.AccessToken, map[string]any{
		"pet_ids": []string{cat, dog}, "type": "feeding", "value": feedingValue,
		"frequency_type": "once", "times": []string{"09:00"}, "start_date": futureDate(3),
	})
	onlyCat := createOncePlan(t, tokens.AccessToken, cat, "other", map[string]any{"label": "x"}, futureDate(4), "09:00")

	require.Equal(t, http.StatusNoContent, doRequest(t, http.MethodDelete, "/pet/"+cat, nil, tokens.AccessToken).status)

	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM reminder_plan_pet`))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM reminder`))
	require.Equal(t, 0, countRows(t, `SELECT COUNT(*) FROM reminder_plan WHERE id = $1`, onlyCat.ID))
	got := getReminderPlan(t, tokens.AccessToken, shared.ID)
	require.Equal(t, []string{dog}, petIDsOf(got.Pets))
}

// Мягко удалённый питомец в наборе настроек напоминания неотличим от
// несуществующего: 404 (в отличие от события, где 400).
func TestMultiPet_ReminderPlanSoftDeletedPetIs404(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	cat := createSpeciesPet(t, tokens.AccessToken, "Барсик", "CAT")
	gone := createSpeciesPet(t, tokens.AccessToken, "Удалён", "CAT")
	require.Equal(t, http.StatusNoContent, doRequest(t, http.MethodDelete, "/pet/"+gone, nil, tokens.AccessToken).status)

	resp := doRequest(t, http.MethodPost, "/reminder-plans?tz=UTC", map[string]any{
		"id": uuid.NewString(), "pet_ids": []string{cat, gone}, "type": "feeding", "value": feedingValue,
		"frequency_type": "once", "times": []string{"09:00"}, "start_date": futureDate(3),
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusNotFound, resp.status, "%s", resp.body)
}

// Замена одного напоминания новой записью с другим набором питомцев.
func TestMultiPet_DetachWithDifferentPets(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	cat := createSpeciesPet(t, tokens.AccessToken, "Барсик", "CAT")
	dog := createSpeciesPet(t, tokens.AccessToken, "Рекс", "DOG")
	plan := createPlan(t, tokens.AccessToken, map[string]any{
		"pet_ids": []string{cat}, "type": "feeding", "value": feedingValue,
		"frequency_type": "daily", "times": []string{"09:00"}, "start_date": futureDate(3), "end_date": futureDate(4),
	})
	require.Len(t, plan.Reminders, 2)

	resp := doRequest(t, http.MethodPost, "/reminders/"+plan.Reminders[0].ID+"/detach", map[string]any{
		"event": map[string]any{
			"pet_ids": []string{cat, dog}, "date": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
			"type": "feeding", "value": feedingValue,
		},
	}, tokens.AccessToken, map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equalf(t, http.StatusCreated, resp.status, "%s", resp.body)
	var body struct {
		Event eventPetsBody `json:"event"`
	}
	resp.decode(t, &body)
	require.Equal(t, []string{cat, dog}, petIDsOf(body.Event.Pets))
	// Исходные настройки и их питомцы не меняются.
	require.Equal(t, []string{cat}, petIDsOf(getReminderPlan(t, tokens.AccessToken, plan.ID).Pets))
}

func TestMultiPet_ImportLocalData(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")

	body := map[string]any{
		"pets": []map[string]any{
			{"local_id": "local-cat", "name": "Барсик", "species": "cat"},
			{"local_id": "local-dog", "name": "Рекс", "species": "dog"},
		},
		"events": []map[string]any{{
			"local_id":      "event-1",
			"pet_local_ids": []string{"local-cat", "local-dog"},
			"date":          "2024-01-01T10:00:00Z",
			"type":          "feeding",
			"value":         feedingValue,
		}},
		"reminder_plans": []map[string]any{{
			"local_id":       "plan-1",
			"pet_local_ids":  []string{"local-cat", "local-dog"},
			"type":           "feeding",
			"value":          feedingValue,
			"frequency_type": "once",
			"times":          []string{"09:00"},
			"start_date":     futureDate(3),
			"tz":             "UTC",
			"reminders":      []map[string]any{{"local_id": "rem-1", "remind_at": futureDate(3) + "T09:00:00Z"}},
		}},
	}
	resp := doRequest(t, http.MethodPost, "/import/local-data", body, tokens.AccessToken,
		map[string]string{"Idempotency-Key": uuid.NewString()})
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM event`))
	require.Equal(t, 2, countRows(t, `SELECT COUNT(*) FROM event_pet`))
	require.Equal(t, 1, countRows(t, `SELECT COUNT(*) FROM reminder_plan`))
	require.Equal(t, 2, countRows(t, `SELECT COUNT(*) FROM reminder_plan_pet`))

	// Измерительный тип с несколькими питомцами и неизвестная ссылка — 400.
	for name, mutate := range map[string]func(ev map[string]any){
		"измерительный тип": func(ev map[string]any) {
			ev["type"] = "weight"
			ev["value"] = map[string]any{"amount": 4.0}
		},
		"неизвестная ссылка": func(ev map[string]any) {
			ev["pet_local_ids"] = []string{"local-cat", "nope"}
		},
		"повтор": func(ev map[string]any) {
			ev["pet_local_ids"] = []string{"local-cat", "local-cat"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			ev := map[string]any{
				"local_id": "event-2", "pet_local_ids": []string{"local-cat", "local-dog"},
				"date": "2024-01-01T10:00:00Z", "type": "feeding", "value": feedingValue,
			}
			mutate(ev)
			bad := doUnvalidatedRequest(t, http.MethodPost, "/import/local-data", map[string]any{
				"pets": []map[string]any{
					{"local_id": "local-cat", "name": "Барсик", "species": "cat"},
					{"local_id": "local-dog", "name": "Рекс", "species": "dog"},
				},
				"events":         []map[string]any{ev},
				"reminder_plans": []map[string]any{},
			}, tokens.AccessToken, map[string]string{"Idempotency-Key": uuid.NewString()})
			require.Equalf(t, http.StatusBadRequest, bad.status, "%s", bad.body)
		})
	}
}
