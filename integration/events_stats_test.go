//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type statsBucket struct {
	BucketStart string   `json:"bucket_start"`
	Value       *float64 `json:"value"`
	Count       int      `json:"count"`
}

type statsSeries struct {
	Type        string        `json:"type"`
	Metric      string        `json:"metric"`
	Category    string        `json:"category"`
	Unit        string        `json:"unit"`
	ValueKind   string        `json:"value_kind"`
	Aggregation string        `json:"aggregation"`
	Buckets     []statsBucket `json:"buckets"`
}

type statsResponse struct {
	PetName string        `json:"pet_name"`
	Series  []statsSeries `json:"series"`
}

func createPet(t *testing.T, token, name string) string {
	t.Helper()
	resp := doRequest(t, http.MethodPost, "/pet", map[string]any{
		"name":    name,
		"species": "cat",
	}, token)
	require.Equalf(t, http.StatusCreated, resp.status, "%s", resp.body)
	var pet struct {
		ID string `json:"id"`
	}
	resp.decode(t, &pet)
	return pet.ID
}

func createEvent(t *testing.T, token, petID, date, eventType string, value map[string]any) {
	t.Helper()
	resp := doRequest(t, http.MethodPost, "/events", map[string]any{
		"pet_id": petID,
		"date":   date,
		"type":   eventType,
		"value":  value,
	}, token)
	require.Equalf(t, http.StatusCreated, resp.status, "создание события %s не удалось: %s", eventType, resp.body)
}

func seriesByKey(series []statsSeries, eventType, metric, category, unit string) (statsSeries, bool) {
	for _, item := range series {
		if item.Type == eventType && item.Metric == metric && item.Category == category && item.Unit == unit {
			return item, true
		}
	}
	return statsSeries{}, false
}

// Полный проход агрегации на реальной БД: свёртка выполняется SQL-запросом,
// пустые интервалы присутствуют, несопоставимые значения разделены на серии.
func TestEventStats_AggregatesByRegistry(t *testing.T) {
	resetDB(t)

	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	createEvent(t, tokens.AccessToken, petID, "2024-01-01T08:00:00Z", "weight", map[string]any{"amount": 4.0})
	createEvent(t, tokens.AccessToken, petID, "2024-01-01T20:00:00Z", "weight", map[string]any{"amount": 5.0})
	createEvent(t, tokens.AccessToken, petID, "2024-01-03T10:00:00Z", "weight", map[string]any{"amount": 6.0})

	createEvent(t, tokens.AccessToken, petID, "2024-01-01T09:00:00Z", "temperature", map[string]any{"amount": 38.5, "kind": "body"})
	createEvent(t, tokens.AccessToken, petID, "2024-01-01T09:30:00Z", "temperature", map[string]any{"amount": 26.0, "kind": "environment"})

	createEvent(t, tokens.AccessToken, petID, "2024-01-01T07:00:00Z", "feeding", map[string]any{"amount": 100, "unit": "g", "food": "dry"})
	createEvent(t, tokens.AccessToken, petID, "2024-01-01T19:00:00Z", "feeding", map[string]any{"amount": 50, "unit": "g", "food": "wet"})
	createEvent(t, tokens.AccessToken, petID, "2024-01-01T19:30:00Z", "feeding", map[string]any{"amount": 3, "unit": "piece", "food": "insects"})

	createEvent(t, tokens.AccessToken, petID, "2024-01-01T12:00:00Z", "activity", map[string]any{"duration_min": 30, "kind": "walk", "distance_m": 1500})
	createEvent(t, tokens.AccessToken, petID, "2024-01-01T18:00:00Z", "activity", map[string]any{"duration_min": 20, "kind": "play"})

	createEvent(t, tokens.AccessToken, petID, "2024-01-01T11:00:00Z", "medication", map[string]any{"name": "Ципровет", "dose_amount": 2.5, "dose_unit": "mg"})
	createEvent(t, tokens.AccessToken, petID, "2024-01-01T23:00:00Z", "medication", map[string]any{"name": "Ципровет"})

	createEvent(t, tokens.AccessToken, petID, "2024-01-01T13:00:00Z", "urine", map[string]any{"status": "abnormal"})

	path := fmt.Sprintf("/events/stats?pet_id=%s&from=2024-01-01&to=2024-01-03&bucket=day&types=%s",
		petID, "weight,temperature,feeding,activity,medication,urine")
	resp := doRequest(t, http.MethodGet, path, nil, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)

	var stats statsResponse
	resp.decode(t, &stats)
	require.Equal(t, "Барсик", stats.PetName)
	require.NotEmpty(t, stats.Series)

	// measure: среднее и последнее значение интервала, пустой интервал — null.
	avg, ok := seriesByKey(stats.Series, "weight", "amount_avg", "", "kg")
	require.True(t, ok, "нет серии weight/amount_avg")
	require.Equal(t, "measure", avg.ValueKind)
	require.Len(t, avg.Buckets, 3)
	require.Equal(t, "2024-01-01", avg.Buckets[0].BucketStart)
	require.NotNil(t, avg.Buckets[0].Value)
	require.InDelta(t, 4.5, *avg.Buckets[0].Value, 0.0001)
	require.Equal(t, 2, avg.Buckets[0].Count)
	require.Nil(t, avg.Buckets[1].Value, "интервал без событий обязан присутствовать со значением null")
	require.Equal(t, 0, avg.Buckets[1].Count)
	require.NotNil(t, avg.Buckets[2].Value)

	last, ok := seriesByKey(stats.Series, "weight", "amount_last", "", "kg")
	require.True(t, ok)
	require.NotNil(t, last.Buckets[0].Value)
	require.InDelta(t, 5.0, *last.Buckets[0].Value, 0.0001)

	// temperature: тело и среда — разные серии, общего среднего нет.
	body, ok := seriesByKey(stats.Series, "temperature", "amount_avg", "body", "°C")
	require.True(t, ok)
	require.NotNil(t, body.Buckets[0].Value)
	require.InDelta(t, 38.5, *body.Buckets[0].Value, 0.0001)

	environment, ok := seriesByKey(stats.Series, "temperature", "amount_avg", "environment", "°C")
	require.True(t, ok)
	require.NotNil(t, environment.Buckets[0].Value)
	require.InDelta(t, 26.0, *environment.Buckets[0].Value, 0.0001)

	// feeding: суммы считаются в пределах одной единицы измерения.
	grams, ok := seriesByKey(stats.Series, "feeding", "amount_sum", "", "g")
	require.True(t, ok)
	require.NotNil(t, grams.Buckets[0].Value)
	require.InDelta(t, 150.0, *grams.Buckets[0].Value, 0.0001)

	pieces, ok := seriesByKey(stats.Series, "feeding", "amount_sum", "", "piece")
	require.True(t, ok)
	require.NotNil(t, pieces.Buckets[0].Value)
	require.InDelta(t, 3.0, *pieces.Buckets[0].Value, 0.0001)

	portions, ok := seriesByKey(stats.Series, "feeding", "amount_sum", "", "portion")
	require.True(t, ok, "серия без событий всё равно возвращается")
	require.Nil(t, portions.Buckets[0].Value)

	// activity: две независимые метрики с разными единицами.
	duration, ok := seriesByKey(stats.Series, "activity", "duration_min_sum", "", "min")
	require.True(t, ok)
	require.NotNil(t, duration.Buckets[0].Value)
	require.InDelta(t, 50.0, *duration.Buckets[0].Value, 0.0001)

	distance, ok := seriesByKey(stats.Series, "activity", "distance_m_sum", "", "m")
	require.True(t, ok)
	require.NotNil(t, distance.Buckets[0].Value)
	require.InDelta(t, 1500.0, *distance.Buckets[0].Value, 0.0001)

	// medication: количество приёмов, а не сумма доз.
	medication, ok := seriesByKey(stats.Series, "medication", "count", "", "")
	require.True(t, ok)
	require.Equal(t, "count", medication.Aggregation)
	require.NotNil(t, medication.Buckets[0].Value)
	require.InDelta(t, 2.0, *medication.Buckets[0].Value, 0.0001)

	// category: по серии на каждое значение словаря.
	abnormal, ok := seriesByKey(stats.Series, "urine", "count", "abnormal", "")
	require.True(t, ok)
	require.NotNil(t, abnormal.Buckets[0].Value)
	require.InDelta(t, 1.0, *abnormal.Buckets[0].Value, 0.0001)

	normal, ok := seriesByKey(stats.Series, "urine", "count", "normal", "")
	require.True(t, ok)
	require.Nil(t, normal.Buckets[0].Value)
	require.Equal(t, 0, normal.Buckets[0].Count)
}

// Мягко удалённые события исключаются из агрегации.
func TestEventStats_ExcludesDeletedEvents(t *testing.T) {
	resetDB(t)

	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	created := doRequest(t, http.MethodPost, "/events", map[string]any{
		"pet_id": petID,
		"date":   "2024-02-01T10:00:00Z",
		"type":   "water",
		"value":  map[string]any{"amount": 100},
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, created.status, "%s", created.body)
	var event struct {
		ID string `json:"id"`
	}
	created.decode(t, &event)

	deleted := doRequest(t, http.MethodDelete, "/events/"+event.ID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, deleted.status)

	path := fmt.Sprintf("/events/stats?pet_id=%s&from=2024-02-01&to=2024-02-01&bucket=day&types=water", petID)
	resp := doRequest(t, http.MethodGet, path, nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, resp.status)

	var stats statsResponse
	resp.decode(t, &stats)
	require.Len(t, stats.Series, 1)
	require.Len(t, stats.Series[0].Buckets, 1)
	require.Nil(t, stats.Series[0].Buckets[0].Value)
	require.Equal(t, 0, stats.Series[0].Buckets[0].Count)
}

func TestEventStats_ValidationAndOwnership(t *testing.T) {
	resetDB(t)

	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	// Неагрегируемый тип (value_kind=label) — 400, а не пустой ответ.
	other := doRequest(t, http.MethodGet,
		fmt.Sprintf("/events/stats?pet_id=%s&from=2024-01-01&to=2024-01-02&bucket=day&types=other", petID),
		nil, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, other.status)

	// from позже to.
	reversed := doRequest(t, http.MethodGet,
		fmt.Sprintf("/events/stats?pet_id=%s&from=2024-02-01&to=2024-01-01&bucket=day", petID),
		nil, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, reversed.status)

	// Диапазон свыше 366 дней.
	tooLong := doRequest(t, http.MethodGet,
		fmt.Sprintf("/events/stats?pet_id=%s&from=2024-01-01&to=2025-06-01&bucket=day", petID),
		nil, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, tooLong.status)

	// Несуществующий питомец — 404.
	missing := doRequest(t, http.MethodGet,
		fmt.Sprintf("/events/stats?pet_id=%s&from=2024-01-01&to=2024-01-02&bucket=day", uuid.NewString()),
		nil, tokens.AccessToken)
	require.Equal(t, http.StatusNotFound, missing.status)

	// Чужой питомец — тоже 404.
	stranger := registerUser(t, uniqueLogin(t), "correct-password")
	foreign := doRequest(t, http.MethodGet,
		fmt.Sprintf("/events/stats?pet_id=%s&from=2024-01-01&to=2024-01-02&bucket=day", petID),
		nil, stranger.AccessToken)
	require.Equal(t, http.StatusNotFound, foreign.status)

	// Мягко удалённый питомец недоступен.
	deleted := doRequest(t, http.MethodDelete, "/pet/"+petID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, deleted.status)
	afterDelete := doRequest(t, http.MethodGet,
		fmt.Sprintf("/events/stats?pet_id=%s&from=2024-01-01&to=2024-01-02&bucket=day", petID),
		nil, tokens.AccessToken)
	require.Equal(t, http.StatusNotFound, afterDelete.status)

	// Без токена — 401.
	unauthorized := doRequest(t, http.MethodGet,
		fmt.Sprintf("/events/stats?pet_id=%s&from=2024-01-01&to=2024-01-02&bucket=day", petID),
		nil, "")
	require.Equal(t, http.StatusUnauthorized, unauthorized.status)
}

// Значение события валидируется по реестру и на создании, и на редактировании
// — правила, которые контракт выразить не может (диапазоны, обязательность
// поля под конкретный type), проверяет приложение.
func TestEventValue_ValidatedByRegistry(t *testing.T) {
	resetDB(t)

	tokens := registerUser(t, uniqueLogin(t), "correct-password")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	invalid := []struct {
		name      string
		eventType string
		value     map[string]any
	}{
		{"вес вне диапазона", "weight", map[string]any{"amount": 500}},
		{"температура без вида замера", "temperature", map[string]any{"amount": 38.5}},
		{"кормление без вида корма", "feeding", map[string]any{"amount": 100, "unit": "g"}},
		{"доза без единицы", "medication", map[string]any{"name": "Ципровет", "dose_amount": 1}},
		{"длительность сна вне диапазона", "sleep", map[string]any{"duration_min": 0}},
		{"название препарата из пробелов", "medication", map[string]any{"name": "   "}},
	}

	for _, c := range invalid {
		t.Run(c.name, func(t *testing.T) {
			resp := doRequest(t, http.MethodPost, "/events", map[string]any{
				"pet_id": petID,
				"date":   "2024-03-01T10:00:00Z",
				"type":   c.eventType,
				"value":  c.value,
			}, tokens.AccessToken)
			require.Equalf(t, http.StatusBadRequest, resp.status, "%s", resp.body)
		})
	}

	created := doRequest(t, http.MethodPost, "/events", map[string]any{
		"pet_id": petID,
		"date":   "2024-03-01T10:00:00Z",
		"type":   "temperature",
		"value":  map[string]any{"amount": 38.5, "kind": "body"},
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusCreated, created.status, "%s", created.body)
	var event struct {
		ID string `json:"id"`
	}
	created.decode(t, &event)

	// PATCH заменяет value целиком: значение без обязательного kind
	// отклоняется, а не сливается с прежним объектом.
	patchInvalid := doRequest(t, http.MethodPatch, "/events/"+event.ID, map[string]any{
		"pet_id": petID,
		"value":  map[string]any{"amount": 39.0},
	}, tokens.AccessToken)
	require.Equal(t, http.StatusBadRequest, patchInvalid.status)

	patchValid := doRequest(t, http.MethodPatch, "/events/"+event.ID, map[string]any{
		"pet_id": petID,
		"value":  map[string]any{"amount": 39.0, "kind": "environment"},
	}, tokens.AccessToken)
	require.Equalf(t, http.StatusNoContent, patchValid.status, "%s", patchValid.body)

	read := doRequest(t, http.MethodGet, "/events/"+event.ID, nil, tokens.AccessToken)
	require.Equal(t, http.StatusOK, read.status)
	var readBody struct {
		Value struct {
			Amount float64 `json:"amount"`
			Kind   string  `json:"kind"`
		} `json:"value"`
	}
	read.decode(t, &readBody)
	require.InDelta(t, 39.0, readBody.Value.Amount, 0.0001)
	require.Equal(t, "environment", readBody.Value.Kind)
}
