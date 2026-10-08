package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"myauthservice/database"
	"myauthservice/openapi"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	bannerID1 = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	bannerID2 = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
)

func strp(s string) *string { return &s }

// --- Механизм выбора перевода ---

type fakeTr struct {
	Title    *string
	Body     *string
	ImageURL *string
	Blurhash *string
}

func TestLocaleChain(t *testing.T) {
	assert.Equal(t, []string{"ru"}, localeChain("ru"))
	assert.Equal(t, []string{"en", "ru"}, localeChain("en"))
}

func TestPickTranslatedString_FieldsAreIndependent(t *testing.T) {
	rows := map[string]fakeTr{
		"en": {Title: strp("Hello")},
		"ru": {Title: strp("Привет"), Body: strp("Текст")},
	}
	chain := localeChain("en")

	title := pickTranslatedString(chain, rows, func(r fakeTr) *string { return r.Title })
	body := pickTranslatedString(chain, rows, func(r fakeTr) *string { return r.Body })

	require.NotNil(t, title)
	assert.Equal(t, "Hello", *title)
	require.NotNil(t, body)
	assert.Equal(t, "Текст", *body)
}

func TestPickTranslatedString_BlankIsAbsent(t *testing.T) {
	rows := map[string]fakeTr{
		"en": {Title: strp("")},
		"ru": {Title: strp("Привет")},
	}
	title := pickTranslatedString(localeChain("en"), rows, func(r fakeTr) *string { return r.Title })
	require.NotNil(t, title)
	assert.Equal(t, "Привет", *title)

	missing := pickTranslatedString(localeChain("en"), map[string]fakeTr{"en": {}}, func(r fakeTr) *string { return r.Title })
	assert.Nil(t, missing)
}

func TestPickTranslationRow_ImagePairFromOneRow(t *testing.T) {
	rows := map[string]fakeTr{
		"en": {Blurhash: strp("en-hash")}, // blurhash есть, а url нет — строка не подходит
		"ru": {ImageURL: strp("https://x/ru.png"), Blurhash: strp("ru-hash")},
	}
	row, ok := pickTranslationRow(localeChain("en"), rows, func(r fakeTr) bool { return !isBlankString(r.ImageURL) })
	require.True(t, ok)
	assert.Equal(t, "https://x/ru.png", *row.ImageURL)
	assert.Equal(t, "ru-hash", *row.Blurhash)
}

func TestEtagMatches(t *testing.T) {
	assert.True(t, etagMatches(`"abc"`, `"abc"`))
	assert.True(t, etagMatches(`W/"abc"`, `"abc"`))
	assert.True(t, etagMatches(`"x", "abc"`, `"abc"`))
	assert.True(t, etagMatches(`*`, `"abc"`))
	assert.False(t, etagMatches(`"other"`, `"abc"`))
}

// --- Правила таргетинга ---

func TestCompareAppVersions_Numeric(t *testing.T) {
	v19, _ := parseAppVersion("1.9.0")
	v110, _ := parseAppVersion("1.10.0")
	assert.Equal(t, -1, compareAppVersions(v19, v110), "1.9.0 < 1.10.0 (не строковое сравнение)")
	assert.Equal(t, 0, compareAppVersions(v19, v19))
	assert.Equal(t, 1, compareAppVersions(v110, v19))

	_, ok := parseAppVersion("1.2")
	assert.False(t, ok)
	_, ok = parseAppVersion("1.2.x")
	assert.False(t, ok)
	_, ok = parseAppVersion("1.2.3-beta")
	assert.False(t, ok)
}

func TestParseTargetingRule_Invalid(t *testing.T) {
	cases := []struct{ ruleType, params string }{
		{"unknown", `{}`},
		{"platform", `{"values": ["windows"]}`},
		{"platform", `{"values": []}`},
		{"platform", `{"values": ["ios"], "extra": 1}`},
		{"platform", `null`},
		{"app_version", `{}`},
		{"app_version", `{"min": "1.2"}`},
		{"app_version", `{"min": "2.0.0", "max": "1.0.0"}`},
		{"language", `{"values": ["de"]}`},
		{"has_pets", `{"value": "yes"}`},
		{"has_pets", `{}`},
	}
	for _, c := range cases {
		_, err := parseTargetingRule(c.ruleType, json.RawMessage(c.params))
		assert.Error(t, err, "%s %s", c.ruleType, c.params)
	}
}

func TestTargetingRules_Matches(t *testing.T) {
	version, _ := parseAppVersion("1.10.0")
	ctx := targetingContext{
		platform:   "ios",
		appVersion: version,
		language:   "en",
		hasPets:    func() (bool, error) { return true, nil },
	}
	cases := []struct {
		ruleType, params string
		want             bool
	}{
		{"platform", `{"values": ["ios", "android"]}`, true},
		{"platform", `{"values": ["android"]}`, false},
		{"app_version", `{"min": "1.9.0"}`, true},
		{"app_version", `{"min": "1.10.0", "max": "1.10.0"}`, true},
		{"app_version", `{"max": "1.9.0"}`, false},
		{"app_version", `{"min": "1.10.1"}`, false},
		{"language", `{"values": ["ru"]}`, false},
		{"language", `{"values": ["ru", "en"]}`, true},
		{"has_pets", `{"value": true}`, true},
		{"has_pets", `{"value": false}`, false},
	}
	for _, c := range cases {
		rule, err := parseTargetingRule(c.ruleType, json.RawMessage(c.params))
		require.NoError(t, err, "%s %s", c.ruleType, c.params)
		got, err := rule.matches(ctx)
		require.NoError(t, err)
		assert.Equal(t, c.want, got, "%s %s", c.ruleType, c.params)
	}
}

// --- Локализация и валидность баннера ---

func sheetRecord() database.BannerRecord {
	return database.BannerRecord{
		ID:        bannerID1,
		Layout:    "sheet",
		Frequency: "once",
		IsShow:    true,
		Translations: map[string]database.BannerTranslationRecord{
			"ru": {Title: strp("Заголовок"), Body: strp("Текст"), ImageURL: strp("https://img/ru.png"), ImageBlurhash: strp("ru-hash")},
		},
	}
}

func TestBuildLocalizedBanner_FallbackToDefaultLanguagePerField(t *testing.T) {
	rec := sheetRecord()
	rec.Translations["en"] = database.BannerTranslationRecord{Title: strp("Title")}

	banner, issues := buildLocalizedBanner(rec, localeChain("en"))
	require.Empty(t, issues)
	require.NotNil(t, banner.Title)
	assert.Equal(t, "Title", *banner.Title)
	require.NotNil(t, banner.Body)
	assert.Equal(t, "Текст", *banner.Body, "текст не переведён - берётся ru")
	require.NotNil(t, banner.Image)
	assert.Equal(t, "https://img/ru.png", banner.Image.Url)
	require.NotNil(t, banner.Image.Blurhash)
	assert.Equal(t, "ru-hash", *banner.Image.Blurhash)
	assert.Nil(t, banner.Slides)
	assert.Nil(t, banner.PrimaryCta)
}

func TestBuildLocalizedBanner_ImagePairFromSameRow(t *testing.T) {
	rec := sheetRecord()
	rec.Translations["en"] = database.BannerTranslationRecord{
		Title:         strp("Title"),
		ImageURL:      strp("https://img/en.png"),
		ImageBlurhash: nil, // у en нет blurhash: ru-хеш к en-картинке не подмешивается
	}

	banner, issues := buildLocalizedBanner(rec, localeChain("en"))
	require.Empty(t, issues)
	require.NotNil(t, banner.Image)
	assert.Equal(t, "https://img/en.png", banner.Image.Url)
	assert.Nil(t, banner.Image.Blurhash)
}

func TestBuildLocalizedBanner_Invalid(t *testing.T) {
	cases := map[string]func(rec *database.BannerRecord){
		"нет заголовка ни на en, ни на ru": func(rec *database.BannerRecord) {
			rec.Translations["ru"] = database.BannerTranslationRecord{Body: strp("x")}
		},
		"layout вне enum": func(rec *database.BannerRecord) { rec.Layout = "hologram" },
		"tone вне enum":   func(rec *database.BannerRecord) { rec.Tone = strp("loud") },
		"frequency вне enum": func(rec *database.BannerRecord) {
			rec.Frequency = "sometimes"
		},
		"http-картинка": func(rec *database.BannerRecord) {
			rec.Translations["ru"] = database.BannerTranslationRecord{Title: strp("T"), ImageURL: strp("http://img/a.png")}
		},
		"url-действие без цели": func(rec *database.BannerRecord) {
			rec.CtaAction = strp("url")
			rec.Translations["ru"] = database.BannerTranslationRecord{Title: strp("T"), CtaLabel: strp("Go")}
		},
		"url-действие с http-целью": func(rec *database.BannerRecord) {
			rec.CtaAction = strp("url")
			rec.CtaTarget = strp("http://example.com")
			rec.Translations["ru"] = database.BannerTranslationRecord{Title: strp("T"), CtaLabel: strp("Go")}
		},
		"действие без подписи": func(rec *database.BannerRecord) {
			rec.CtaAction = strp("store")
		},
		"cover без картинки": func(rec *database.BannerRecord) {
			rec.Layout = "cover"
			rec.Translations["ru"] = database.BannerTranslationRecord{Title: strp("T")}
		},
		"poster без картинки": func(rec *database.BannerRecord) {
			rec.Layout = "poster"
			rec.Translations["ru"] = database.BannerTranslationRecord{Title: strp("T")}
		},
		"carousel без слайдов": func(rec *database.BannerRecord) {
			rec.Layout = "carousel"
		},
		"carousel: у слайда нет title": func(rec *database.BannerRecord) {
			rec.Layout = "carousel"
			rec.Slides = []database.BannerSlideRecord{{ID: "s1", Position: 1, Translations: map[string]database.BannerSlideTranslationRecord{"ru": {Body: strp("x")}}}}
		},
	}
	for name, mutate := range cases {
		rec := sheetRecord()
		mutate(&rec)
		_, issues := buildLocalizedBanner(rec, localeChain("en"))
		assert.NotEmpty(t, issues, name)
	}
}

func TestBuildLocalizedBanner_CarouselTooManySlides(t *testing.T) {
	rec := sheetRecord()
	rec.Layout = "carousel"
	for i := 1; i <= 11; i++ {
		rec.Slides = append(rec.Slides, database.BannerSlideRecord{
			ID: "s", Position: i,
			Translations: map[string]database.BannerSlideTranslationRecord{"ru": {Title: strp("T")}},
		})
	}
	_, issues := buildLocalizedBanner(rec, localeChain("ru"))
	assert.NotEmpty(t, issues)

	rec.Slides = rec.Slides[:10]
	banner, issues := buildLocalizedBanner(rec, localeChain("ru"))
	require.Empty(t, issues)
	require.NotNil(t, banner.Slides)
	assert.Len(t, *banner.Slides, 10)
}

func TestBuildLocalizedBanner_PosterCtaWithoutLabelAndStoreHasNoTarget(t *testing.T) {
	rec := sheetRecord()
	rec.Layout = "poster"
	rec.CtaAction = strp("store")
	rec.CtaTarget = strp("ignored")
	rec.Translations["ru"] = database.BannerTranslationRecord{ImageURL: strp("https://img/p.png")}

	banner, issues := buildLocalizedBanner(rec, localeChain("ru"))
	require.Empty(t, issues)
	require.NotNil(t, banner.PrimaryCta)
	assert.Equal(t, openapi.BannerCtaAction("store"), banner.PrimaryCta.Action)
	assert.Nil(t, banner.PrimaryCta.Label)
	assert.Nil(t, banner.PrimaryCta.Target)
	assert.Nil(t, banner.Title, "поля вне состава шаблона игнорируются")
}

// --- GET /banner ---

const (
	candidatesQuery  = `SELECT b\.id, b\.layout.*FROM banner b\s+WHERE b\.is_show`
	targetingQuery   = `FROM banner_targeting`
	translationQuery = `FROM banner_translation`
	slidesQuery      = `FROM banner_slide s`
	petsQuery        = `SELECT EXISTS \(SELECT 1 FROM pet WHERE user_id = \$1 AND deleted_at IS NULL\)`
)

var bannerBaseColumns = []string{"id", "layout", "tone", "is_show", "starts_at", "ends_at", "priority", "frequency", "cta_action", "cta_target", "created_at"}

func baseRow(rows *sqlmock.Rows, id, layout string) *sqlmock.Rows {
	return rows.AddRow(id, layout, nil, true, nil, nil, 0, "once", nil, nil, time.Now())
}

var translationColumns = []string{"banner_id", "locale", "overline", "title", "body", "badge", "image_url", "image_blurhash", "cta_label", "secondary_label"}

func bannerRequest(t *testing.T, query string, lang string) *http.Request {
	t.Helper()
	r := eventRequest(t, http.MethodGet, "/banner"+query, nil, true)
	if lang != "" {
		r.Header.Set("LanguageCode", lang)
	}
	return r
}

func TestGetBannerHandler_MethodNotAllowed(t *testing.T) {
	w := httptest.NewRecorder()
	GetBannerHandler(w, eventRequest(t, http.MethodPost, "/banner", nil, false))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestGetBannerHandler_Unauthorized(t *testing.T) {
	w := httptest.NewRecorder()
	GetBannerHandler(w, eventRequest(t, http.MethodGet, "/banner?platform=ios&appVersion=1.0.0", nil, false))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetBannerHandler_BadParams(t *testing.T) {
	cases := []struct{ query, lang string }{
		{"?platform=ios&appVersion=1.0.0", ""},
		{"?platform=ios&appVersion=1.0.0", "de"},
		{"?appVersion=1.0.0", "ru"},
		{"?platform=windows&appVersion=1.0.0", "ru"},
		{"?platform=ios", "ru"},
		{"?platform=ios&appVersion=1.0", "ru"},
	}
	for _, c := range cases {
		mock := setupMockDB(t)
		expectTokensValid(mock, testUserID)
		w := httptest.NewRecorder()
		GetBannerHandler(w, bannerRequest(t, c.query, c.lang))
		assert.Equal(t, http.StatusBadRequest, w.Code, "%s %q", c.query, c.lang)
	}
}

func TestGetBannerHandler_EmptyList(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(candidatesQuery).WithArgs(testUserID).WillReturnRows(sqlmock.NewRows(bannerBaseColumns))

	w := httptest.NewRecorder()
	GetBannerHandler(w, bannerRequest(t, "?platform=ios&appVersion=1.0.0", "ru"))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"items": []}`, w.Body.String())
	assert.NotEmpty(t, w.Header().Get("ETag"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetBannerHandler_DBError(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	mock.ExpectQuery(candidatesQuery).WillReturnError(assertError)

	w := httptest.NewRecorder()
	GetBannerHandler(w, bannerRequest(t, "?platform=ios&appVersion=1.0.0", "ru"))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// expectTwoBannersFlow мокает выдачу двух кандидатов: первый подходит всем,
// второй имеет правило platform=android.
func expectTwoBannersFlow(mock sqlmock.Sqlmock) {
	rows := sqlmock.NewRows(bannerBaseColumns)
	baseRow(rows, bannerID1, "dialog")
	baseRow(rows, bannerID2, "dialog")
	mock.ExpectQuery(candidatesQuery).WithArgs(testUserID).WillReturnRows(rows)
	mock.ExpectQuery(targetingQuery).WillReturnRows(sqlmock.NewRows([]string{"banner_id", "type", "params"}).
		AddRow(bannerID2, "platform", []byte(`{"values": ["android"]}`)))
	mock.ExpectQuery(translationQuery).WillReturnRows(sqlmock.NewRows(translationColumns).
		AddRow(bannerID1, "ru", nil, "Привет", nil, nil, nil, nil, nil, nil).
		AddRow(bannerID1, "en", nil, "Hello", nil, nil, nil, nil, nil, nil))
}

func TestGetBannerHandler_TargetingFiltersAndLocalizes(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectTwoBannersFlow(mock)

	w := httptest.NewRecorder()
	GetBannerHandler(w, bannerRequest(t, "?platform=ios&appVersion=1.0.0", "en"))

	require.Equal(t, http.StatusOK, w.Code)
	var resp openapi.GetBannerResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1, "баннер для android не должен попасть в выдачу для ios")
	assert.Equal(t, bannerID1, resp.Items[0].Id.String())
	require.NotNil(t, resp.Items[0].Title)
	assert.Equal(t, "Hello", *resp.Items[0].Title)

	// Поля, которых нет у баннера, отдаются как null, а не пропускаются.
	var raw struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	assert.Equal(t, "null", string(raw.Items[0]["image"]))
	assert.Equal(t, "null", string(raw.Items[0]["primaryCta"]))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetBannerHandler_InvalidBannerIsExcluded(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	rows := sqlmock.NewRows(bannerBaseColumns)
	baseRow(rows, bannerID1, "dialog") // у него не будет title
	baseRow(rows, bannerID2, "dialog")
	mock.ExpectQuery(candidatesQuery).WillReturnRows(rows)
	mock.ExpectQuery(targetingQuery).WillReturnRows(sqlmock.NewRows([]string{"banner_id", "type", "params"}))
	mock.ExpectQuery(translationQuery).WillReturnRows(sqlmock.NewRows(translationColumns).
		AddRow(bannerID1, "ru", nil, nil, "только текст", nil, nil, nil, nil, nil).
		AddRow(bannerID2, "ru", nil, "Ок", nil, nil, nil, nil, nil, nil))

	w := httptest.NewRecorder()
	GetBannerHandler(w, bannerRequest(t, "?platform=ios&appVersion=1.0.0", "ru"))

	require.Equal(t, http.StatusOK, w.Code)
	var resp openapi.GetBannerResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
	assert.Equal(t, bannerID2, resp.Items[0].Id.String())
}

func TestGetBannerHandler_UnknownTargetingTypeExcludesBanner(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	rows := sqlmock.NewRows(bannerBaseColumns)
	baseRow(rows, bannerID1, "dialog")
	mock.ExpectQuery(candidatesQuery).WillReturnRows(rows)
	mock.ExpectQuery(targetingQuery).WillReturnRows(sqlmock.NewRows([]string{"banner_id", "type", "params"}).
		AddRow(bannerID1, "moon_phase", []byte(`{}`)))

	w := httptest.NewRecorder()
	GetBannerHandler(w, bannerRequest(t, "?platform=ios&appVersion=1.0.0", "ru"))

	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"items": []}`, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetBannerHandler_HasPetsRuleQueriesPetsOnce(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	rows := sqlmock.NewRows(bannerBaseColumns)
	baseRow(rows, bannerID1, "dialog")
	baseRow(rows, bannerID2, "dialog")
	mock.ExpectQuery(candidatesQuery).WillReturnRows(rows)
	mock.ExpectQuery(targetingQuery).WillReturnRows(sqlmock.NewRows([]string{"banner_id", "type", "params"}).
		AddRow(bannerID1, "has_pets", []byte(`{"value": true}`)).
		AddRow(bannerID2, "has_pets", []byte(`{"value": false}`)))
	mock.ExpectQuery(petsQuery).WithArgs(testUserID).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(translationQuery).WillReturnRows(sqlmock.NewRows(translationColumns).
		AddRow(bannerID1, "ru", nil, "Для владельцев", nil, nil, nil, nil, nil, nil))

	w := httptest.NewRecorder()
	GetBannerHandler(w, bannerRequest(t, "?platform=ios&appVersion=1.0.0", "ru"))

	require.Equal(t, http.StatusOK, w.Code)
	var resp openapi.GetBannerResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
	assert.Equal(t, bannerID1, resp.Items[0].Id.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetBannerHandler_CarouselSlides(t *testing.T) {
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	rows := sqlmock.NewRows(bannerBaseColumns)
	baseRow(rows, bannerID1, "carousel")
	mock.ExpectQuery(candidatesQuery).WillReturnRows(rows)
	mock.ExpectQuery(targetingQuery).WillReturnRows(sqlmock.NewRows([]string{"banner_id", "type", "params"}))
	mock.ExpectQuery(translationQuery).WillReturnRows(sqlmock.NewRows(translationColumns))
	mock.ExpectQuery(slidesQuery).WillReturnRows(sqlmock.NewRows([]string{"banner_id", "id", "position", "locale", "title", "body", "image_url", "image_blurhash"}).
		AddRow(bannerID1, "s1", 1, "ru", "Первый", nil, nil, nil).
		AddRow(bannerID1, "s1", 1, "en", "First", "Body", nil, nil).
		AddRow(bannerID1, "s2", 2, "ru", "Второй", "Тело", "https://img/2.png", "h2"))

	w := httptest.NewRecorder()
	GetBannerHandler(w, bannerRequest(t, "?platform=ios&appVersion=1.0.0", "en"))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp openapi.GetBannerResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
	require.NotNil(t, resp.Items[0].Slides)
	slides := *resp.Items[0].Slides
	require.Len(t, slides, 2)
	assert.Equal(t, "First", slides[0].Title)
	assert.Equal(t, "Второй", slides[1].Title, "у слайда нет перевода на en - берётся ru")
	require.NotNil(t, slides[1].Image)
	assert.Equal(t, "https://img/2.png", slides[1].Image.Url)
}

func TestGetBannerHandler_ETagAndNotModified(t *testing.T) {
	// Первый запрос (en): получаем ETag.
	mock := setupMockDB(t)
	expectTokensValid(mock, testUserID)
	expectTwoBannersFlow(mock)
	w1 := httptest.NewRecorder()
	GetBannerHandler(w1, bannerRequest(t, "?platform=ios&appVersion=1.0.0", "en"))
	require.Equal(t, http.StatusOK, w1.Code)
	etag := w1.Header().Get("ETag")
	require.NotEmpty(t, etag)

	// Тот же ответ с If-None-Match - 304 без тела.
	expectTokensValid(mock, testUserID)
	expectTwoBannersFlow(mock)
	r2 := bannerRequest(t, "?platform=ios&appVersion=1.0.0", "en")
	r2.Header.Set("If-None-Match", etag)
	w2 := httptest.NewRecorder()
	GetBannerHandler(w2, r2)
	assert.Equal(t, http.StatusNotModified, w2.Code)
	assert.Empty(t, w2.Body.String())
	assert.Equal(t, etag, w2.Header().Get("ETag"))

	// Тот же набор данных на другом языке (баннеры без переводов en совпадают
	// по телу с ru) - ETag всё равно разный, потому что язык входит в хеш.
	same := func(lang string) string {
		expectTokensValid(mock, testUserID)
		rows := sqlmock.NewRows(bannerBaseColumns)
		baseRow(rows, bannerID1, "dialog")
		mock.ExpectQuery(candidatesQuery).WillReturnRows(rows)
		mock.ExpectQuery(targetingQuery).WillReturnRows(sqlmock.NewRows([]string{"banner_id", "type", "params"}))
		mock.ExpectQuery(translationQuery).WillReturnRows(sqlmock.NewRows(translationColumns).
			AddRow(bannerID1, "ru", nil, "Привет", nil, nil, nil, nil, nil, nil))
		w := httptest.NewRecorder()
		GetBannerHandler(w, bannerRequest(t, "?platform=ios&appVersion=1.0.0", lang))
		require.Equal(t, http.StatusOK, w.Code)
		return w.Header().Get("ETag")
	}
	assert.NotEqual(t, same("ru"), same("en"))
}
