//go:build integration

// Сквозные сценарии баннеров: GET /banner (open-api/spec.json) и admin-методы
// /admin/banner* (open-api/admin-spec.json), см. "Баннеры — Backend" и
// "Баннеры — Управление (admin API) — Backend".
package integration

import (
	"bytes"
	"io"
	"myauthservice/database"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// doAdminRequest выполняет запрос к admin-методу с верным X-Admin-Key и
// сверяет запрос/ответ с open-api/admin-spec.json.
func doAdminRequest(t *testing.T, method, path string, body any) apiResponse {
	t.Helper()
	return doRequestAgainstSpec(t, adminSpec, "open-api/admin-spec.json", method, path, body, "",
		map[string]string{"X-Admin-Key": testAdminKey})
}

// doRawRequest отправляет запрос без сверки со спекой — для намеренно
// некорректных запросов (нет заголовка, неверный ключ, битый id).
func doRawRequest(t *testing.T, method, path string, body []byte, headers map[string]string) apiResponse {
	t.Helper()
	req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(body))
	require.NoError(t, err)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return apiResponse{status: resp.StatusCode, header: resp.Header.Clone(), body: data}
}

type bannerItem struct {
	ID         string  `json:"id"`
	Layout     string  `json:"layout"`
	Title      *string `json:"title"`
	Body       *string `json:"body"`
	PrimaryCta *struct {
		Label  *string `json:"label"`
		Action string  `json:"action"`
		Target *string `json:"target"`
	} `json:"primaryCta"`
	Image *struct {
		URL      string  `json:"url"`
		Blurhash *string `json:"blurhash"`
	} `json:"image"`
	Slides *[]struct {
		Title string `json:"title"`
	} `json:"slides"`
}

// getBanners вызывает GET /banner с валидным запросом и возвращает ответ и элементы.
func getBanners(t *testing.T, token, lang, platform, version string, extra ...map[string]string) (apiResponse, []bannerItem) {
	t.Helper()
	headers := map[string]string{"LanguageCode": lang}
	for _, e := range extra {
		for k, v := range e {
			headers[k] = v
		}
	}
	resp := doRequest(t, http.MethodGet, "/banner?platform="+platform+"&appVersion="+version, nil, token, headers)
	var result struct {
		Items []bannerItem `json:"items"`
	}
	if resp.status == http.StatusOK {
		resp.decode(t, &result)
	}
	return resp, result.Items
}

func dialogDoc(title string) map[string]any {
	return map[string]any{
		"layout":    "dialog",
		"frequency": "once",
		"translations": map[string]any{
			"ru": map[string]any{"title": title},
		},
	}
}

func createBanner(t *testing.T, doc map[string]any) string {
	t.Helper()
	resp := doAdminRequest(t, http.MethodPost, "/admin/banner", doc)
	require.Equalf(t, http.StatusCreated, resp.status, "%s", resp.body)
	var created struct {
		ID string `json:"id"`
	}
	resp.decode(t, &created)
	return created.ID
}

func userIDByLogin(t *testing.T, login string) string {
	t.Helper()
	var id string
	require.NoError(t, database.DB.QueryRow(`SELECT id FROM users WHERE login = $1`, login).Scan(&id))
	return id
}

func titles(items []bannerItem) []string {
	result := make([]string, len(items))
	for i, item := range items {
		if item.Title != nil {
			result[i] = *item.Title
		}
	}
	return result
}

// --- admin API ---

func TestAdminBanner_FullLifecycle(t *testing.T) {
	resetDB(t)
	login := uniqueLogin(t)
	registerUser(t, login, "password123")
	userID := userIDByLogin(t, login)

	doc := map[string]any{
		"layout":    "carousel",
		"tone":      "promo",
		"isShow":    true,
		"startsAt":  "2020-01-01T00:00:00Z",
		"endsAt":    "2099-01-01T00:00:00Z",
		"priority":  10,
		"frequency": "every_launch",
		"cta":       map[string]any{"action": "url", "target": "https://example.com/offer"},
		"translations": map[string]any{
			"ru": map[string]any{"overline": "АКЦИЯ", "ctaLabel": "Записаться", "secondaryLabel": "Не сейчас"},
			"en": map[string]any{"ctaLabel": "Book"},
		},
		"slides": []any{
			map[string]any{"translations": map[string]any{
				"ru": map[string]any{"title": "Один", "image": map[string]any{"url": "https://img/1.png", "blurhash": "LEHV6n"}},
				"en": map[string]any{"title": "One"},
			}},
			map[string]any{"translations": map[string]any{"ru": map[string]any{"title": "Два", "body": "Текст"}}},
		},
		"targeting": []any{
			map[string]any{"type": "platform", "params": map[string]any{"values": []string{"ios"}}},
			map[string]any{"type": "app_version", "params": map[string]any{"min": "1.2.0"}},
		},
		"userIds": []string{userID},
	}
	id := createBanner(t, doc)

	// GET по id возвращает документ со всеми переводами и порядком слайдов.
	get := doAdminRequest(t, http.MethodGet, "/admin/banner/"+id, nil)
	require.Equal(t, http.StatusOK, get.status, "%s", get.body)
	var got struct {
		CreatedAt    string                    `json:"createdAt"`
		Translations map[string]map[string]any `json:"translations"`
		Slides       []struct {
			Translations map[string]map[string]any `json:"translations"`
		} `json:"slides"`
		Targeting []struct {
			Type string `json:"type"`
		} `json:"targeting"`
		UserIDs []string `json:"userIds"`
	}
	get.decode(t, &got)
	require.NotEmpty(t, got.CreatedAt)
	require.Len(t, got.Translations, 2)
	require.Len(t, got.Slides, 2)
	require.Equal(t, "Один", got.Slides[0].Translations["ru"]["title"])
	require.Equal(t, "Два", got.Slides[1].Translations["ru"]["title"])
	require.Len(t, got.Targeting, 2)
	require.Equal(t, []string{userID}, got.UserIDs)

	// PUT существующего баннера: 200 и полная замена (en, слайд, правила и пользователи удаляются).
	replaced := dialogDoc("Заменён")
	put := doAdminRequest(t, http.MethodPut, "/admin/banner/"+id, replaced)
	require.Equal(t, http.StatusOK, put.status, "%s", put.body)

	after := doAdminRequest(t, http.MethodGet, "/admin/banner/"+id, nil)
	var afterDoc struct {
		Layout       string            `json:"layout"`
		Translations map[string]any    `json:"translations"`
		Slides       []any             `json:"slides"`
		Targeting    []any             `json:"targeting"`
		UserIDs      []string          `json:"userIds"`
		CreatedAt    string            `json:"createdAt"`
		Cta          map[string]string `json:"cta"`
	}
	after.decode(t, &afterDoc)
	require.Equal(t, "dialog", afterDoc.Layout)
	require.Len(t, afterDoc.Translations, 1)
	require.Empty(t, afterDoc.Slides)
	require.Empty(t, afterDoc.Targeting)
	require.Empty(t, afterDoc.UserIDs)
	require.Equal(t, got.CreatedAt, afterDoc.CreatedAt, "created_at не меняется при PUT")

	var orphanRows int
	require.NoError(t, database.DB.QueryRow(`SELECT (SELECT count(*) FROM banner_slide) + (SELECT count(*) FROM banner_slide_translation) + (SELECT count(*) FROM banner_targeting) + (SELECT count(*) FROM banner_user)`).Scan(&orphanRows))
	require.Equal(t, 0, orphanRows)

	// PUT несуществующего id создаёт баннер с этим id.
	newID := "12345678-1234-1234-1234-123456789abc"
	created := doAdminRequest(t, http.MethodPut, "/admin/banner/"+newID, dialogDoc("Новый"))
	require.Equal(t, http.StatusCreated, created.status, "%s", created.body)

	// PATCH меняет только показ.
	patch := doAdminRequest(t, http.MethodPatch, "/admin/banner/"+newID, map[string]any{"isShow": false})
	require.Equal(t, http.StatusOK, patch.status, "%s", patch.body)
	var patched struct {
		IsShow bool `json:"isShow"`
	}
	patch.decode(t, &patched)
	require.False(t, patched.IsShow)
	missing := doAdminRequest(t, http.MethodPatch, "/admin/banner/ffffffff-ffff-ffff-ffff-ffffffffffff", map[string]any{"isShow": true})
	require.Equal(t, http.StatusNotFound, missing.status)

	// Список содержит все баннеры, включая выключенный.
	list := doAdminRequest(t, http.MethodGet, "/admin/banner", nil)
	require.Equal(t, http.StatusOK, list.status, "%s", list.body)
	var listed struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	list.decode(t, &listed)
	require.Len(t, listed.Items, 2)
	require.Equal(t, newID, listed.Items[0].ID, "сортировка created_at DESC")

	// DELETE: 204, повторно 404, GET 404.
	del := doAdminRequest(t, http.MethodDelete, "/admin/banner/"+id, nil)
	require.Equal(t, http.StatusNoContent, del.status)
	require.Equal(t, http.StatusNotFound, doAdminRequest(t, http.MethodDelete, "/admin/banner/"+id, nil).status)
	require.Equal(t, http.StatusNotFound, doAdminRequest(t, http.MethodGet, "/admin/banner/"+id, nil).status)
}

func TestAdminBanner_DeleteCascadesAllRelatedRows(t *testing.T) {
	resetDB(t)
	login := uniqueLogin(t)
	registerUser(t, login, "password123")
	userID := userIDByLogin(t, login)

	doc := dialogDoc("T")
	doc["layout"] = "carousel"
	doc["slides"] = []any{map[string]any{"translations": map[string]any{"ru": map[string]any{"title": "S"}}}}
	doc["targeting"] = []any{map[string]any{"type": "has_pets", "params": map[string]any{"value": true}}}
	doc["userIds"] = []string{userID}
	id := createBanner(t, doc)

	require.Equal(t, http.StatusNoContent, doAdminRequest(t, http.MethodDelete, "/admin/banner/"+id, nil).status)

	for _, table := range []string{"banner", "banner_translation", "banner_slide", "banner_slide_translation", "banner_targeting", "banner_user"} {
		var n int
		require.NoError(t, database.DB.QueryRow(`SELECT count(*) FROM `+table).Scan(&n))
		require.Equal(t, 0, n, table)
	}
}

func TestAdminBanner_ValidationReturnsAllIssuesAndSavesNothing(t *testing.T) {
	resetDB(t)
	doc := map[string]any{
		"layout":    "cover",
		"frequency": "once",
		"translations": map[string]any{
			"ru": map[string]any{"image": map[string]any{"url": "http://img/a.png"}},
		},
		"userIds": []string{"33333333-3333-3333-3333-333333333333"},
	}
	resp := doAdminRequest(t, http.MethodPost, "/admin/banner", doc)
	require.Equal(t, http.StatusBadRequest, resp.status, "%s", resp.body)

	// Значение enum вне спеки спека-валидатор отвергнет на стороне запроса,
	// поэтому отправляем такой документ без сверки и проверяем ответ сервера.
	raw := doRawRequest(t, http.MethodPost, "/admin/banner",
		[]byte(`{"layout":"hologram","frequency":"weekly","translations":{"ru":{"title":"T"}}}`),
		map[string]string{"X-Admin-Key": testAdminKey, "Content-Type": "application/json"})
	require.Equal(t, http.StatusBadRequest, raw.status, "%s", raw.body)
	require.Contains(t, string(raw.body), "layout")
	require.Contains(t, string(raw.body), "frequency")

	require.Equal(t, http.StatusBadRequest, resp.status, "%s", resp.body)
	var errResp struct {
		Code    string `json:"code"`
		Payload []struct {
			Path   string `json:"path"`
			Reason string `json:"reason"`
		} `json:"payload"`
	}
	resp.decode(t, &errResp)
	require.Equal(t, "VALIDATION_ERROR", errResp.Code)
	require.Len(t, errResp.Payload, 3) // title, image.url, userIds[0]

	var n int
	require.NoError(t, database.DB.QueryRow(`SELECT count(*) FROM banner`).Scan(&n))
	require.Equal(t, 0, n)
}

func TestAdminBanner_AccessControl(t *testing.T) {
	resetDB(t)

	missing := doRawRequest(t, http.MethodGet, "/admin/banner", nil, nil)
	require.Equal(t, http.StatusUnauthorized, missing.status)
	wrong := doRawRequest(t, http.MethodGet, "/admin/banner", nil, map[string]string{"X-Admin-Key": "nope"})
	require.Equal(t, http.StatusUnauthorized, wrong.status)
	badID := doRawRequest(t, http.MethodGet, "/admin/banner/xyz", nil, map[string]string{"X-Admin-Key": testAdminKey})
	require.Equal(t, http.StatusBadRequest, badID.status)

	// Пользовательский токен доступа не даёт.
	tokens := registerUser(t, uniqueLogin(t), "password123")
	byToken := doRawRequest(t, http.MethodGet, "/admin/banner", nil, map[string]string{"Authorization": "Bearer " + tokens.AccessToken})
	require.Equal(t, http.StatusUnauthorized, byToken.status)

	// Ключ в конфигурации не задан - admin-методы отвечают как несуществующий маршрут.
	t.Setenv("ADMIN_KEY", "")
	disabled := doRawRequest(t, http.MethodGet, "/admin/banner", nil, map[string]string{"X-Admin-Key": ""})
	require.Equal(t, http.StatusNotFound, disabled.status)
	disabled = doRawRequest(t, http.MethodGet, "/admin/banner", nil, map[string]string{"X-Admin-Key": testAdminKey})
	require.Equal(t, http.StatusNotFound, disabled.status)
}

// --- GET /banner ---

func TestGetBanner_RequestValidationAndAuth(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")

	noToken := doRequest(t, http.MethodGet, "/banner?platform=ios&appVersion=1.0.0", nil, "", map[string]string{"LanguageCode": "ru"})
	require.Equal(t, http.StatusUnauthorized, noToken.status)

	auth := map[string]string{"Authorization": "Bearer " + tokens.AccessToken}
	withAuth := func(extra map[string]string) map[string]string {
		for k, v := range auth {
			extra[k] = v
		}
		return extra
	}
	require.Equal(t, http.StatusBadRequest, doRawRequest(t, http.MethodGet, "/banner?platform=ios&appVersion=1.0.0", nil, withAuth(map[string]string{})).status, "нет LanguageCode")
	require.Equal(t, http.StatusBadRequest, doRawRequest(t, http.MethodGet, "/banner?platform=ios&appVersion=1.0.0", nil, withAuth(map[string]string{"LanguageCode": "de"})).status)
	require.Equal(t, http.StatusBadRequest, doRawRequest(t, http.MethodGet, "/banner?platform=web&appVersion=1.0.0", nil, withAuth(map[string]string{"LanguageCode": "ru"})).status)
	require.Equal(t, http.StatusBadRequest, doRawRequest(t, http.MethodGet, "/banner?platform=ios&appVersion=1.0", nil, withAuth(map[string]string{"LanguageCode": "ru"})).status)
	require.Equal(t, http.StatusBadRequest, doRawRequest(t, http.MethodGet, "/banner?platform=ios", nil, withAuth(map[string]string{"LanguageCode": "ru"})).status)
}

func TestGetBanner_EmptyList(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	resp, items := getBanners(t, tokens.AccessToken, "ru", "ios", "1.0.0")
	require.Equal(t, http.StatusOK, resp.status)
	require.Empty(t, items)
	require.JSONEq(t, `{"items": []}`, string(resp.body))
}

func TestGetBanner_SelectionByWindowSwitchAndSorting(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")

	low := dialogDoc("низкий")
	low["priority"] = 1
	high := dialogDoc("высокий")
	high["priority"] = 50
	hidden := dialogDoc("выключен")
	hidden["isShow"] = false
	future := dialogDoc("будущий")
	future["startsAt"] = "2099-01-01T00:00:00Z"
	expired := dialogDoc("истёк")
	expired["endsAt"] = "2000-01-01T00:00:00Z"
	older := dialogDoc("тот же приоритет, старее")
	newer := dialogDoc("тот же приоритет, новее")
	for _, doc := range []map[string]any{low, high, hidden, future, expired, older, newer} {
		createBanner(t, doc)
	}

	resp, items := getBanners(t, tokens.AccessToken, "ru", "ios", "1.0.0")
	require.Equal(t, http.StatusOK, resp.status)
	require.Equal(t, []string{"высокий", "низкий", "тот же приоритет, новее", "тот же приоритет, старее"}, titles(items))
}

func TestGetBanner_TargetingRules(t *testing.T) {
	resetDB(t)
	login := uniqueLogin(t)
	tokens := registerUser(t, login, "password123")
	createProfile(t, tokens.AccessToken, "Иван")

	rule := func(title, ruleType string, params map[string]any) {
		doc := dialogDoc(title)
		doc["targeting"] = []any{map[string]any{"type": ruleType, "params": params}}
		createBanner(t, doc)
	}
	rule("ios-only", "platform", map[string]any{"values": []string{"ios"}})
	rule("android-only", "platform", map[string]any{"values": []string{"android"}})
	rule("версия от 1.9.0", "app_version", map[string]any{"min": "1.9.0"})
	rule("версия до 1.9.5", "app_version", map[string]any{"max": "1.9.5"})
	rule("только en", "language", map[string]any{"values": []string{"en"}})
	rule("есть питомцы", "has_pets", map[string]any{"value": true})
	rule("нет питомцев", "has_pets", map[string]any{"value": false})

	// Клиент ios 1.10.0 (числовое сравнение: 1.10.0 > 1.9.0), без питомцев, язык ru.
	_, items := getBanners(t, tokens.AccessToken, "ru", "ios", "1.10.0")
	require.ElementsMatch(t, []string{"ios-only", "версия от 1.9.0", "нет питомцев"}, titles(items))

	// Появляется питомец - меняется результат has_pets, язык en включает "только en"
	// (en-перевода у баннеров нет - берётся ru).
	createPet(t, tokens.AccessToken, "Барсик")
	_, items = getBanners(t, tokens.AccessToken, "en", "android", "1.9.0")
	require.ElementsMatch(t, []string{"android-only", "версия от 1.9.0", "версия до 1.9.5", "только en", "есть питомцы"}, titles(items))

	// Условия разных типов объединяются по И.
	both := dialogDoc("ios и версия от 2.0.0")
	both["targeting"] = []any{
		map[string]any{"type": "platform", "params": map[string]any{"values": []string{"ios"}}},
		map[string]any{"type": "app_version", "params": map[string]any{"min": "2.0.0"}},
	}
	createBanner(t, both)
	_, items = getBanners(t, tokens.AccessToken, "ru", "ios", "1.10.0")
	require.NotContains(t, titles(items), "ios и версия от 2.0.0")
	_, items = getBanners(t, tokens.AccessToken, "ru", "ios", "2.0.0")
	require.Contains(t, titles(items), "ios и версия от 2.0.0")
}

func TestGetBanner_PersonalList(t *testing.T) {
	resetDB(t)
	loginA := uniqueLogin(t)
	tokensA := registerUser(t, loginA, "password123")
	tokensB := registerUser(t, uniqueLogin(t), "password123")
	idA := userIDByLogin(t, loginA)

	personal := dialogDoc("личный")
	personal["userIds"] = []string{idA}
	createBanner(t, personal)
	createBanner(t, dialogDoc("для всех"))

	_, itemsA := getBanners(t, tokensA.AccessToken, "ru", "ios", "1.0.0")
	require.ElementsMatch(t, []string{"личный", "для всех"}, titles(itemsA))
	_, itemsB := getBanners(t, tokensB.AccessToken, "ru", "ios", "1.0.0")
	require.Equal(t, []string{"для всех"}, titles(itemsB))
}

func TestGetBanner_LocalizationFallbackPerField(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")

	doc := map[string]any{
		"layout":    "sheet",
		"frequency": "once",
		"cta":       map[string]any{"action": "deeplink", "target": "app://vet"},
		"translations": map[string]any{
			"ru": map[string]any{
				"title": "Заголовок", "body": "Текст", "ctaLabel": "Открыть",
				"image": map[string]any{"url": "https://img/ru.png", "blurhash": "ru-hash"},
			},
			"en": map[string]any{
				"title": "Title",
				// body и ctaLabel не переведены - берутся из ru; картинка en без blurhash
				// не должна подмешивать ru-хеш.
				"image": map[string]any{"url": "https://img/en.png"},
			},
		},
	}
	createBanner(t, doc)

	_, en := getBanners(t, tokens.AccessToken, "en", "ios", "1.0.0")
	require.Len(t, en, 1)
	require.Equal(t, "Title", *en[0].Title)
	require.Equal(t, "Текст", *en[0].Body)
	require.Equal(t, "Открыть", *en[0].PrimaryCta.Label)
	require.Equal(t, "deeplink", en[0].PrimaryCta.Action)
	require.Equal(t, "app://vet", *en[0].PrimaryCta.Target)
	require.Equal(t, "https://img/en.png", en[0].Image.URL)
	require.Nil(t, en[0].Image.Blurhash)

	_, ru := getBanners(t, tokens.AccessToken, "ru", "ios", "1.0.0")
	require.Equal(t, "Заголовок", *ru[0].Title)
	require.Equal(t, "ru-hash", *ru[0].Image.Blurhash)
}

func TestGetBanner_CarouselSlidesOrdered(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	doc := dialogDoc("")
	doc["layout"] = "carousel"
	doc["slides"] = []any{
		map[string]any{"translations": map[string]any{"ru": map[string]any{"title": "Первый"}, "en": map[string]any{"title": "First"}}},
		map[string]any{"translations": map[string]any{"ru": map[string]any{"title": "Второй"}}},
	}
	createBanner(t, doc)

	_, items := getBanners(t, tokens.AccessToken, "en", "ios", "1.0.0")
	require.Len(t, items, 1)
	require.NotNil(t, items[0].Slides)
	slides := *items[0].Slides
	require.Len(t, slides, 2)
	require.Equal(t, "First", slides[0].Title)
	require.Equal(t, "Второй", slides[1].Title)
}

func TestGetBanner_InvalidRowsAreExcludedNotFatal(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	createBanner(t, dialogDoc("валидный"))

	// Данные, внесённые в обход admin API: диалог без заголовка и баннер с
	// неизвестным типом правила таргетинга.
	var noTitle, badRule string
	require.NoError(t, database.DB.QueryRow(`INSERT INTO banner (layout, frequency) VALUES ('dialog', 'once') RETURNING id`).Scan(&noTitle))
	_, err := database.DB.Exec(`INSERT INTO banner_translation (banner_id, locale, body) VALUES ($1, 'ru', 'только текст')`, noTitle)
	require.NoError(t, err)
	require.NoError(t, database.DB.QueryRow(`INSERT INTO banner (layout, frequency) VALUES ('dialog', 'once') RETURNING id`).Scan(&badRule))
	_, err = database.DB.Exec(`INSERT INTO banner_translation (banner_id, locale, title) VALUES ($1, 'ru', 'с кривым правилом')`, badRule)
	require.NoError(t, err)
	_, err = database.DB.Exec(`INSERT INTO banner_targeting (banner_id, type, params) VALUES ($1, 'moon_phase', '{}')`, badRule)
	require.NoError(t, err)

	resp, items := getBanners(t, tokens.AccessToken, "ru", "ios", "1.0.0")
	require.Equal(t, http.StatusOK, resp.status)
	require.Equal(t, []string{"валидный"}, titles(items))
}

func TestGetBanner_ETagAndNotModified(t *testing.T) {
	resetDB(t)
	tokens := registerUser(t, uniqueLogin(t), "password123")
	createBanner(t, dialogDoc("Привет"))

	first, _ := getBanners(t, tokens.AccessToken, "ru", "ios", "1.0.0")
	etag := first.header.Get("ETag")
	require.NotEmpty(t, etag)

	notModified, _ := getBanners(t, tokens.AccessToken, "ru", "ios", "1.0.0", map[string]string{"If-None-Match": etag})
	require.Equal(t, http.StatusNotModified, notModified.status)
	require.Empty(t, notModified.body)

	// Другой язык (набор данных тот же, en-перевода нет) - другой ETag, 304 не отдаётся.
	en, _ := getBanners(t, tokens.AccessToken, "en", "ios", "1.0.0", map[string]string{"If-None-Match": etag})
	require.Equal(t, http.StatusOK, en.status)
	require.NotEqual(t, etag, en.header.Get("ETag"))

	// Изменение данных меняет ETag без перезапуска сервера.
	createBanner(t, dialogDoc("Ещё один"))
	changed, items := getBanners(t, tokens.AccessToken, "ru", "ios", "1.0.0", map[string]string{"If-None-Match": etag})
	require.Equal(t, http.StatusOK, changed.status)
	require.Len(t, items, 2)
}

func TestBannerSchema_ChecksRejectBadData(t *testing.T) {
	resetDB(t)
	_, err := database.DB.Exec(`INSERT INTO banner (layout, frequency) VALUES ('hologram', 'once')`)
	require.Error(t, err)
	_, err = database.DB.Exec(`INSERT INTO banner (layout, frequency, starts_at, ends_at) VALUES ('dialog', 'once', now(), now())`)
	require.Error(t, err)
	_, err = database.DB.Exec(`INSERT INTO banner (layout, frequency, cta_action) VALUES ('dialog', 'once', 'teleport')`)
	require.Error(t, err)

	var id string
	require.NoError(t, database.DB.QueryRow(`INSERT INTO banner (layout, frequency) VALUES ('dialog', 'once') RETURNING id`).Scan(&id))
	_, err = database.DB.Exec(`INSERT INTO banner_translation (banner_id, locale) VALUES ($1, 'de')`, id)
	require.Error(t, err)
	_, err = database.DB.Exec(`INSERT INTO banner_translation (banner_id, locale) VALUES ($1, 'ru')`, id)
	require.NoError(t, err)
	_, err = database.DB.Exec(`INSERT INTO banner_translation (banner_id, locale) VALUES ($1, 'ru')`, id)
	require.Error(t, err, "PK (banner_id, locale)")
}
