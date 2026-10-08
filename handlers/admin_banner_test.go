package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAdminKey = "test-admin-key"

func adminRequest(t *testing.T, method, path string, body any, key string) *http.Request {
	t.Helper()
	r := doRequest(method, path, body)
	if key != "" {
		r.Header.Set("X-Admin-Key", key)
	}
	return r
}

// serveAdmin прогоняет запрос через реальный mux, чтобы проверить и маршрутизацию.
func serveAdmin(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	NewMux().ServeHTTP(w, r)
	return w
}

func validBannerDoc() map[string]any {
	return map[string]any{
		"layout":    "dialog",
		"frequency": "once",
		"translations": map[string]any{
			"ru": map[string]any{"title": "Привет"},
		},
	}
}

type issuesResponse struct {
	Code    string        `json:"code"`
	Payload []bannerIssue `json:"payload"`
}

// --- Доступ ---

func TestAdminBanner_DisabledWithoutConfiguredKey(t *testing.T) {
	t.Setenv(adminKeyEnv, "")
	// Даже пустой X-Admin-Key не даёт доступа: маршрут «не существует».
	for _, key := range []string{"", "anything"} {
		w := serveAdmin(adminRequest(t, http.MethodGet, "/admin/banner", nil, key))
		assert.Equal(t, http.StatusNotFound, w.Code)
	}
	w := serveAdmin(adminRequest(t, http.MethodDelete, "/admin/banner/"+bannerID1, nil, "x"))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAdminBanner_KeyRequired(t *testing.T) {
	t.Setenv(adminKeyEnv, testAdminKey)
	for _, key := range []string{"", "wrong", testAdminKey + "x"} {
		w := serveAdmin(adminRequest(t, http.MethodGet, "/admin/banner", nil, key))
		assert.Equal(t, http.StatusUnauthorized, w.Code, key)
		assert.Contains(t, w.Body.String(), "UNAUTHORIZED")
		assert.NotContains(t, w.Body.String(), testAdminKey)
	}
}

func TestAdminBanner_UserTokenDoesNotGiveAccess(t *testing.T) {
	t.Setenv(adminKeyEnv, testAdminKey)
	r := adminRequest(t, http.MethodGet, "/admin/banner", nil, "")
	r.Header.Set("Authorization", "Bearer "+validAccessToken(t, testUserID))
	assert.Equal(t, http.StatusUnauthorized, serveAdmin(r).Code)
}

func TestAdminBanner_InvalidID(t *testing.T) {
	t.Setenv(adminKeyEnv, testAdminKey)
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		w := serveAdmin(adminRequest(t, method, "/admin/banner/not-a-uuid", validBannerDoc(), testAdminKey))
		assert.Equal(t, http.StatusBadRequest, w.Code, method)
	}
}

// --- Валидация записи ---

func TestAdminBanner_ValidationCollectsAllIssues(t *testing.T) {
	t.Setenv(adminKeyEnv, testAdminKey)
	doc := map[string]any{
		"layout":    "cover", // нужен title и image
		"tone":      "loud",
		"frequency": "sometimes",
		"startsAt":  "2026-10-31T00:00:00Z",
		"endsAt":    "2026-10-30T00:00:00Z",
		"cta":       map[string]any{"action": "url", "target": "http://example.com"},
		"translations": map[string]any{
			"ru": map[string]any{"image": map[string]any{"url": "http://img/a.png"}},
			"de": map[string]any{"title": "Hallo"},
		},
		"targeting": []any{
			map[string]any{"type": "platform", "params": map[string]any{"values": []string{"windows"}}},
			map[string]any{"type": "platform", "params": map[string]any{"values": []string{"ios"}}},
			map[string]any{"type": "app_version", "params": map[string]any{"min": "1.2"}},
		},
	}
	w := serveAdmin(adminRequest(t, http.MethodPost, "/admin/banner", doc, testAdminKey))

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	var resp issuesResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "VALIDATION_ERROR", resp.Code)

	paths := map[string]bool{}
	for _, issue := range resp.Payload {
		paths[issue.Path] = true
	}
	for _, want := range []string{
		"tone", "frequency", "endsAt", "cta.target",
		"translations.ru.title", "translations.ru.image.url", "translations.ru.ctaLabel",
		"translations.de", "targeting[0]", "targeting[1].type", "targeting[2]",
	} {
		assert.True(t, paths[want], "ожидалась проблема по пути %s, получено: %v", want, resp.Payload)
	}
}

func TestAdminBanner_ValidationRequiresDefaultLanguage(t *testing.T) {
	t.Setenv(adminKeyEnv, testAdminKey)
	doc := validBannerDoc()
	doc["translations"] = map[string]any{"en": map[string]any{"title": "Hello"}}
	w := serveAdmin(adminRequest(t, http.MethodPost, "/admin/banner", doc, testAdminKey))

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "translations.ru")
}

func TestAdminBanner_ValidationCarouselSlides(t *testing.T) {
	t.Setenv(adminKeyEnv, testAdminKey)
	doc := validBannerDoc()
	doc["layout"] = "carousel"
	doc["slides"] = []any{
		map[string]any{"translations": map[string]any{"ru": map[string]any{"body": "без заголовка"}}},
	}
	w := serveAdmin(adminRequest(t, http.MethodPost, "/admin/banner", doc, testAdminKey))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "slides[0].translations.ru.title")

	doc["slides"] = []any{}
	w = serveAdmin(adminRequest(t, http.MethodPost, "/admin/banner", doc, testAdminKey))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), `"slides"`)
}

func TestAdminBanner_ValidationUnknownUser(t *testing.T) {
	t.Setenv(adminKeyEnv, testAdminKey)
	mock := setupMockDB(t)
	mock.ExpectQuery(`SELECT id FROM users WHERE id = ANY`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(testUserID))

	doc := validBannerDoc()
	doc["userIds"] = []string{testUserID, "33333333-3333-3333-3333-333333333333"}
	w := serveAdmin(adminRequest(t, http.MethodPost, "/admin/banner", doc, testAdminKey))

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "userIds[1]")
	assert.NotContains(t, w.Body.String(), "userIds[0]")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAdminBanner_MalformedJSON(t *testing.T) {
	t.Setenv(adminKeyEnv, testAdminKey)
	r := httptest.NewRequest(http.MethodPost, "/admin/banner", nil)
	r.Header.Set("X-Admin-Key", testAdminKey)
	assert.Equal(t, http.StatusBadRequest, serveAdmin(r).Code)
}

// --- Запись ---

var savedAtRow = func() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"inserted", "created_at"}).AddRow(true, time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC))
}

func TestAdminBanner_PostCreatesInTransaction(t *testing.T) {
	t.Setenv(adminKeyEnv, testAdminKey)
	mock := setupMockDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO banner \(`).WillReturnRows(savedAtRow())
	mock.ExpectExec(`INSERT INTO banner_translation`).WithArgs(sqlmock.AnyArg(), "ru", nil, "Привет", nil, nil, nil, nil, nil, nil).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	w := serveAdmin(adminRequest(t, http.MethodPost, "/admin/banner", validBannerDoc(), testAdminKey))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["id"])
	assert.Equal(t, true, resp["isShow"], "isShow по умолчанию true")
	assert.EqualValues(t, 0, resp["priority"])
	assert.Equal(t, []any{}, resp["slides"])
	assert.Equal(t, []any{}, resp["targeting"])
	assert.Equal(t, []any{}, resp["userIds"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAdminBanner_PostRollsBackOnDBError(t *testing.T) {
	t.Setenv(adminKeyEnv, testAdminKey)
	mock := setupMockDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO banner \(`).WillReturnRows(savedAtRow())
	mock.ExpectExec(`INSERT INTO banner_translation`).WillReturnError(assertError)
	mock.ExpectRollback()

	w := serveAdmin(adminRequest(t, http.MethodPost, "/admin/banner", validBannerDoc(), testAdminKey))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAdminBanner_PutCreateReturns201AndReplaceReturns200(t *testing.T) {
	t.Setenv(adminKeyEnv, testAdminKey)

	// Новый баннер: 201, строки связанных таблиц не удаляются.
	mock := setupMockDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO banner \(`).WithArgs(bannerID1, "dialog", nil, true, nil, nil, 0, "once", nil, nil).
		WillReturnRows(savedAtRow())
	mock.ExpectExec(`INSERT INTO banner_translation`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	w := serveAdmin(adminRequest(t, http.MethodPut, "/admin/banner/"+bannerID1, validBannerDoc(), testAdminKey))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())

	// Существующий: 200, всё, чего нет в теле, удаляется.
	mock = setupMockDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO banner \(`).
		WillReturnRows(sqlmock.NewRows([]string{"inserted", "created_at"}).AddRow(false, time.Now()))
	for _, table := range []string{"banner_translation", "banner_slide", "banner_targeting", "banner_user"} {
		mock.ExpectExec(`DELETE FROM ` + table + ` WHERE banner_id = \$1`).WithArgs(bannerID1).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectExec(`INSERT INTO banner_translation`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	w = serveAdmin(adminRequest(t, http.MethodPut, "/admin/banner/"+bannerID1, validBannerDoc(), testAdminKey))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), bannerID1)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- Чтение, PATCH, DELETE ---

func TestAdminBanner_GetNotFound(t *testing.T) {
	t.Setenv(adminKeyEnv, testAdminKey)
	mock := setupMockDB(t)
	mock.ExpectQuery(`FROM banner b WHERE b\.id = \$1`).WithArgs(bannerID1).WillReturnRows(sqlmock.NewRows(bannerBaseColumns))

	w := serveAdmin(adminRequest(t, http.MethodGet, "/admin/banner/"+bannerID1, nil, testAdminKey))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAdminBanner_ListReturnsAllWithTranslations(t *testing.T) {
	t.Setenv(adminKeyEnv, testAdminKey)
	mock := setupMockDB(t)
	rows := sqlmock.NewRows(bannerBaseColumns)
	rows.AddRow(bannerID1, "dialog", nil, false, nil, nil, 5, "once", nil, nil, time.Now())
	mock.ExpectQuery(`FROM banner b ORDER BY b\.created_at DESC`).WillReturnRows(rows)
	mock.ExpectQuery(translationQuery).WillReturnRows(sqlmock.NewRows(translationColumns).
		AddRow(bannerID1, "ru", nil, "Привет", nil, nil, nil, nil, nil, nil).
		AddRow(bannerID1, "en", nil, "Hello", nil, nil, nil, nil, nil, nil))
	mock.ExpectQuery(slidesQuery).WillReturnRows(sqlmock.NewRows([]string{"banner_id", "id", "position", "locale", "title", "body", "image_url", "image_blurhash"}))
	mock.ExpectQuery(targetingQuery).WillReturnRows(sqlmock.NewRows([]string{"banner_id", "type", "params"}).
		AddRow(bannerID1, "platform", []byte(`{"values":["ios"]}`)))
	mock.ExpectQuery(`FROM banner_user`).WillReturnRows(sqlmock.NewRows([]string{"banner_id", "user_id"}).AddRow(bannerID1, testUserID))

	w := serveAdmin(adminRequest(t, http.MethodGet, "/admin/banner", nil, testAdminKey))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Items []struct {
			IsShow       bool                       `json:"isShow"`
			Translations map[string]json.RawMessage `json:"translations"`
			Targeting    []struct {
				Type   string         `json:"type"`
				Params map[string]any `json:"params"`
			} `json:"targeting"`
			UserIDs []string `json:"userIds"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
	assert.False(t, resp.Items[0].IsShow, "выключенные баннеры тоже в списке")
	assert.Len(t, resp.Items[0].Translations, 2)
	require.Len(t, resp.Items[0].Targeting, 1)
	assert.Equal(t, "platform", resp.Items[0].Targeting[0].Type)
	assert.Equal(t, []string{testUserID}, resp.Items[0].UserIDs)
}

func TestAdminBanner_Patch(t *testing.T) {
	t.Setenv(adminKeyEnv, testAdminKey)

	// Нет поля isShow - 400.
	w := serveAdmin(adminRequest(t, http.MethodPatch, "/admin/banner/"+bannerID1, map[string]any{}, testAdminKey))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// Баннера нет - 404.
	mock := setupMockDB(t)
	mock.ExpectExec(`UPDATE banner SET is_show = \$2 WHERE id = \$1`).WithArgs(bannerID1, false).WillReturnResult(sqlmock.NewResult(0, 0))
	w = serveAdmin(adminRequest(t, http.MethodPatch, "/admin/banner/"+bannerID1, map[string]any{"isShow": false}, testAdminKey))
	assert.Equal(t, http.StatusNotFound, w.Code)

	// Успех - 200 с полным документом.
	mock = setupMockDB(t)
	mock.ExpectExec(`UPDATE banner SET is_show`).WithArgs(bannerID1, false).WillReturnResult(sqlmock.NewResult(0, 1))
	rows := sqlmock.NewRows(bannerBaseColumns)
	rows.AddRow(bannerID1, "dialog", nil, false, nil, nil, 0, "once", nil, nil, time.Now())
	mock.ExpectQuery(`FROM banner b WHERE b\.id = \$1`).WillReturnRows(rows)
	mock.ExpectQuery(translationQuery).WillReturnRows(sqlmock.NewRows(translationColumns))
	mock.ExpectQuery(slidesQuery).WillReturnRows(sqlmock.NewRows([]string{"banner_id", "id", "position", "locale", "title", "body", "image_url", "image_blurhash"}))
	mock.ExpectQuery(targetingQuery).WillReturnRows(sqlmock.NewRows([]string{"banner_id", "type", "params"}))
	mock.ExpectQuery(`FROM banner_user`).WillReturnRows(sqlmock.NewRows([]string{"banner_id", "user_id"}))
	w = serveAdmin(adminRequest(t, http.MethodPatch, "/admin/banner/"+bannerID1, map[string]any{"isShow": false}, testAdminKey))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"isShow":false`)
}

func TestAdminBanner_Delete(t *testing.T) {
	t.Setenv(adminKeyEnv, testAdminKey)

	mock := setupMockDB(t)
	mock.ExpectExec(`DELETE FROM banner WHERE id = \$1`).WithArgs(bannerID1).WillReturnResult(sqlmock.NewResult(0, 1))
	w := serveAdmin(adminRequest(t, http.MethodDelete, "/admin/banner/"+bannerID1, nil, testAdminKey))
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.String())

	mock = setupMockDB(t)
	mock.ExpectExec(`DELETE FROM banner WHERE id = \$1`).WillReturnResult(sqlmock.NewResult(0, 0))
	w = serveAdmin(adminRequest(t, http.MethodDelete, "/admin/banner/"+bannerID1, nil, testAdminKey))
	assert.Equal(t, http.StatusNotFound, w.Code)

	mock = setupMockDB(t)
	mock.ExpectExec(`DELETE FROM banner`).WillReturnError(sql.ErrConnDone)
	w = serveAdmin(adminRequest(t, http.MethodDelete, "/admin/banner/"+bannerID1, nil, testAdminKey))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestAdminBanner_MethodNotAllowed(t *testing.T) {
	t.Setenv(adminKeyEnv, testAdminKey)
	w := serveAdmin(adminRequest(t, http.MethodDelete, "/admin/banner", nil, testAdminKey))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
