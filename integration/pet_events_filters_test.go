//go:build integration

// Фильтры GET /pet/{id}/events: types, from/to/tz, их пересечение с search и
// блок available_filters (только при offset=0, не зависит от фильтров), см.
// «Просмотр карточки питомца — Backend» и «Общие требования: Фильтры списков
// событий».
package integration

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

type petEventsFiltersBody struct {
	Items []struct {
		ID    string  `json:"id"`
		Type  string  `json:"type"`
		Date  string  `json:"date"`
		Notes *string `json:"notes"`
	} `json:"items"`
	Total            int `json:"total"`
	AvailableFilters *struct {
		Total int `json:"total"`
		Types []struct {
			Type  string `json:"type"`
			Count int    `json:"count"`
		} `json:"types"`
		DistinctDays int `json:"distinct_days"`
	} `json:"available_filters"`
}

func getPetEvents(t *testing.T, token, petID, query string) petEventsFiltersBody {
	t.Helper()
	resp := doRequest(t, http.MethodGet, "/pet/"+petID+"/events?"+query, nil, token)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	var body petEventsFiltersBody
	resp.decode(t, &body)
	return body
}

func createNotedEvent(t *testing.T, token, petID, date, eventType, notes string, value map[string]any) {
	t.Helper()
	resp := doRequest(t, http.MethodPost, "/events", map[string]any{
		"pet_ids": []string{petID},
		"date":    date,
		"type":    eventType,
		"notes":   notes,
		"value":   value,
	}, token)
	require.Equalf(t, http.StatusCreated, resp.status, "%s", resp.body)
}

// Набор событий: три дня по UTC (1, 2 и 3 января), но по Нью-Йорку (UTC-5)
// событие 2024-01-02T03:00Z относится к 1 января.
func seedPetEventsForFilters(t *testing.T, token string) string {
	t.Helper()
	petID := createPet(t, token, "Барсик")
	createNotedEvent(t, token, petID, "2024-01-01T10:00:00Z", "weight", "утро", map[string]any{"amount": 4.2})
	createNotedEvent(t, token, petID, "2024-01-02T03:00:00Z", "weight", "ночь", map[string]any{"amount": 4.3})
	createNotedEvent(t, token, petID, "2024-01-02T12:00:00Z", "feeding", "утро", feedingValue)
	createNotedEvent(t, token, petID, "2024-01-03T12:00:00Z", "feeding", "обед", feedingValue)
	return petID
}

func TestPetEventsFilters_TypesCSV(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := seedPetEventsForFilters(t, tokens.AccessToken)

	weight := getPetEvents(t, tokens.AccessToken, petID, "types=weight")
	require.Equal(t, 2, weight.Total)
	require.Len(t, weight.Items, 2)

	both := getPetEvents(t, tokens.AccessToken, petID, "types=weight,feeding")
	require.Equal(t, 4, both.Total)

	// Пустой types — без фильтра (спека такое значение не допускает, поэтому
	// запрос со спекой не сверяется).
	resp := doUnvalidatedRequest(t, http.MethodGet, "/pet/"+petID+"/events?types=", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, resp.status, "%s", resp.body)
	var empty petEventsFiltersBody
	resp.decode(t, &empty)
	require.Equal(t, 4, empty.Total)
}

func TestPetEventsFilters_PeriodInclusiveInClientTimeZone(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := seedPetEventsForFilters(t, tokens.AccessToken)

	// UTC: 2 января — два события (03:00Z и 12:00Z).
	utcDay := getPetEvents(t, tokens.AccessToken, petID, "from=2024-01-02&to=2024-01-02&tz=UTC")
	require.Equal(t, 2, utcDay.Total)

	// Нью-Йорк: событие 03:00Z относится к 1 января (22:00 накануне).
	nyFirst := getPetEvents(t, tokens.AccessToken, petID, "from=2024-01-01&to=2024-01-01&tz=America/New_York")
	require.Equal(t, 2, nyFirst.Total)

	// Только нижняя граница, только верхняя граница.
	fromOnly := getPetEvents(t, tokens.AccessToken, petID, "from=2024-01-03&tz=UTC")
	require.Equal(t, 1, fromOnly.Total)
	toOnly := getPetEvents(t, tokens.AccessToken, petID, "to=2024-01-01&tz=UTC")
	require.Equal(t, 1, toOnly.Total)

	// Диапазон включает обе границы.
	all := getPetEvents(t, tokens.AccessToken, petID, "from=2024-01-01&to=2024-01-03&tz=UTC")
	require.Equal(t, 4, all.Total)
}

func TestPetEventsFilters_CombineWithSearchAndPagination(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := seedPetEventsForFilters(t, tokens.AccessToken)

	// "утро": weight (1 января) и feeding (2 января); вместе с types=feeding
	// остаётся одно событие.
	byTypeAndSearch := getPetEvents(t, tokens.AccessToken, petID, "search="+url.QueryEscape("утро")+"&types=feeding")
	require.Equal(t, 1, byTypeAndSearch.Total)
	require.Len(t, byTypeAndSearch.Items, 1)
	require.Equal(t, "feeding", byTypeAndSearch.Items[0].Type)

	all := getPetEvents(t, tokens.AccessToken, petID, "search="+url.QueryEscape("утро")+"&types=weight,feeding&from=2024-01-02&to=2024-01-03&tz=UTC")
	require.Equal(t, 1, all.Total)

	// total не зависит от limit/offset, items — страница отфильтрованной выборки.
	page := getPetEvents(t, tokens.AccessToken, petID, "types=weight,feeding&limit=1&offset=3")
	require.Equal(t, 4, page.Total)
	require.Len(t, page.Items, 1)
}

func TestPetEventsFilters_AvailableFiltersOnlyOnFirstPageAndIndependent(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := seedPetEventsForFilters(t, tokens.AccessToken)

	// Фильтры сужают items/total, но не available_filters.
	filtered := getPetEvents(t, tokens.AccessToken, petID, "types=weight&search="+url.QueryEscape("ночь")+"&from=2024-01-02&to=2024-01-02&tz=UTC")
	require.Equal(t, 1, filtered.Total)
	require.NotNil(t, filtered.AvailableFilters)
	require.Equal(t, 4, filtered.AvailableFilters.Total)
	require.Equal(t, 3, filtered.AvailableFilters.DistinctDays)
	counts := map[string]int{}
	for _, item := range filtered.AvailableFilters.Types {
		counts[item.Type] = item.Count
	}
	require.Equal(t, map[string]int{"weight": 2, "feeding": 2}, counts)

	// При offset>0 блока нет.
	next := doRequest(t, http.MethodGet, "/pet/"+petID+"/events?offset=1", nil, tokens.AccessToken)
	require.Equalf(t, http.StatusOK, next.status, "%s", next.body)
	var raw map[string]any
	next.decode(t, &raw)
	require.NotContains(t, raw, "available_filters")
}

func TestPetEventsFilters_AvailableFiltersDistinctDaysDependOnTimeZone(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")
	// Два события в один день по UTC, но в разные дни по UTC+10.
	createNotedEvent(t, tokens.AccessToken, petID, "2024-01-01T01:00:00Z", "weight", "a", map[string]any{"amount": 4.2})
	createNotedEvent(t, tokens.AccessToken, petID, "2024-01-01T20:00:00Z", "weight", "b", map[string]any{"amount": 4.3})

	utc := getPetEvents(t, tokens.AccessToken, petID, "")
	require.NotNil(t, utc.AvailableFilters)
	require.Equal(t, 1, utc.AvailableFilters.DistinctDays)

	brisbane := getPetEvents(t, tokens.AccessToken, petID, "tz=Australia/Brisbane")
	require.NotNil(t, brisbane.AvailableFilters)
	require.Equal(t, 2, brisbane.AvailableFilters.DistinctDays)
}

func TestPetEventsFilters_AvailableFiltersEmptyPetAndMultiPetVisibility(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	cat := createSpeciesPet(t, tokens.AccessToken, "Барсик", "CAT")
	dog := createSpeciesPet(t, tokens.AccessToken, "Рекс", "DOG")

	empty := getPetEvents(t, tokens.AccessToken, cat, "")
	require.NotNil(t, empty.AvailableFilters)
	require.Equal(t, 0, empty.AvailableFilters.Total)
	require.Empty(t, empty.AvailableFilters.Types)
	require.Equal(t, 0, empty.AvailableFilters.DistinctDays)

	// Событие на двух питомцев учитывается у каждого один раз.
	resp := postEventPets(t, tokens.AccessToken, []string{cat, dog}, "feeding", feedingValue)
	require.Equalf(t, http.StatusCreated, resp.status, "%s", resp.body)

	for _, petID := range []string{cat, dog} {
		body := getPetEvents(t, tokens.AccessToken, petID, "types=feeding")
		require.Equal(t, 1, body.Total)
		require.Equal(t, 1, body.AvailableFilters.Total)
	}

	// После мягкого удаления одного питомца запись остаётся у второго.
	del := doRequest(t, http.MethodDelete, "/pet/"+dog, nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, del.status)
	after := getPetEvents(t, tokens.AccessToken, cat, "")
	require.Equal(t, 1, after.AvailableFilters.Total)
}

func TestPetEventsFilters_InvalidParams400(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	petID := createPet(t, tokens.AccessToken, "Барсик")

	for name, query := range map[string]string{
		"unknown type":      "types=weight,bogus",
		"bad from":          "from=2024-13-01&tz=UTC",
		"bad to":            "to=yesterday&tz=UTC",
		"from after to":     "from=2024-01-03&to=2024-01-01&tz=UTC",
		"from without tz":   "from=2024-01-01",
		"to without tz":     "to=2024-01-01",
		"unknown time zone": "from=2024-01-01&tz=Not/AZone",
		"tz alone invalid":  "tz=Not/AZone",
	} {
		t.Run(name, func(t *testing.T) {
			// Запросы намеренно нарушают контракт — запрос со спекой не сверяется.
			resp := doUnvalidatedRequest(t, http.MethodGet, "/pet/"+petID+"/events?"+query, nil, tokens.AccessToken)
			require.Equalf(t, http.StatusBadRequest, resp.status, "%s", resp.body)
		})
	}
}
