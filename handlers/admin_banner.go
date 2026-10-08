package handlers

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"myauthservice/database"
	"myauthservice/openapi/adminapi"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Административные методы управления баннерами (см. "Баннеры — Управление
// (admin API) — Backend"). Контракт — open-api/admin-spec.json (пакет
// adminapi); мобильный клиент эти методы не вызывает.

// adminKeyEnv — переменная окружения с административным ключом.
const adminKeyEnv = "ADMIN_KEY"

const adminBodyLimit = 1 << 20

// requireAdminKey проверяет доступ по X-Admin-Key. Если ключ в конфигурации
// не задан, admin-методы отключены и отвечают как несуществующий маршрут
// (404), чтобы нельзя было войти с пустым ключом. Неверный или
// отсутствующий ключ — 401. Значение ключа не логируется.
func requireAdminKey(w http.ResponseWriter, r *http.Request) bool {
	configured := os.Getenv(adminKeyEnv)
	if configured == "" {
		http.NotFound(w, r)
		return false
	}

	// Сравнение хешей фиксированной длины: время не зависит ни от содержимого,
	// ни от длины переданного ключа.
	provided := sha256.Sum256([]byte(r.Header.Get("X-Admin-Key")))
	expected := sha256.Sum256([]byte(configured))
	if subtle.ConstantTimeCompare(provided[:], expected[:]) != 1 {
		writeAdminError(w, http.StatusUnauthorized, adminapi.UNAUTHORIZED, "Отсутствует или неверен X-Admin-Key", nil)
		return false
	}
	return true
}

func writeAdminError(w http.ResponseWriter, status int, code adminapi.AdminErrorCode, message string, payload any) {
	body := adminapi.AdminErrorResponse{Code: code, Message: message}
	if payload != nil {
		body.Payload = &payload
	}
	writeJSON(w, status, body)
}

func writeAdminValidationError(w http.ResponseWriter, issues []bannerIssue) {
	writeAdminError(w, http.StatusBadRequest, adminapi.VALIDATIONERROR, "Документ баннера не прошёл валидацию", issues)
}

func writeAdminInternalError(w http.ResponseWriter, what string, err error) {
	log.Printf("admin banner: %s: %v", what, err)
	writeAdminError(w, http.StatusInternalServerError, adminapi.INTERNALERROR, "Ошибка базы данных", nil)
}

// AdminBannerCollectionHandler обрабатывает /admin/banner:
// POST — создать баннер, GET — список всех баннеров.
func AdminBannerCollectionHandler(w http.ResponseWriter, r *http.Request) {
	if !requireAdminKey(w, r) {
		return
	}

	switch r.Method {
	case http.MethodPost:
		createAdminBanner(w, r)
	case http.MethodGet:
		listAdminBanners(w)
	default:
		writeAdminError(w, http.StatusMethodNotAllowed, adminapi.BADREQUEST, "Method not allowed", nil)
	}
}

// AdminBannerByIDHandler обрабатывает /admin/banner/{id}: GET, PUT, PATCH, DELETE.
func AdminBannerByIDHandler(w http.ResponseWriter, r *http.Request) {
	if !requireAdminKey(w, r) {
		return
	}

	rawID := strings.TrimPrefix(r.URL.Path, "/admin/banner/")
	if rawID == "" || strings.Contains(rawID, "/") {
		http.NotFound(w, r)
		return
	}
	id, ok := parseBannerID(rawID)
	if !ok {
		writeAdminValidationError(w, []bannerIssue{{"id", "должен быть валидным uuid"}})
		return
	}

	switch r.Method {
	case http.MethodGet:
		getAdminBanner(w, id)
	case http.MethodPut:
		putAdminBanner(w, r, id)
	case http.MethodPatch:
		patchAdminBanner(w, r, id)
	case http.MethodDelete:
		deleteAdminBanner(w, id)
	default:
		writeAdminError(w, http.StatusMethodNotAllowed, adminapi.BADREQUEST, "Method not allowed", nil)
	}
}

func parseBannerID(raw string) (string, bool) {
	if len(raw) != 36 {
		return "", false
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return "", false
	}
	return parsed.String(), true
}

func createAdminBanner(w http.ResponseWriter, r *http.Request) {
	rec, ok := readAndValidateBannerDoc(w, r, uuid.NewString())
	if !ok {
		return
	}

	_, createdAt, err := database.SaveBanner(rec)
	if err != nil {
		writeAdminInternalError(w, "создание баннера "+rec.ID, err)
		return
	}
	rec.CreatedAt = createdAt
	log.Printf("admin banner: создан banner_id=%s", rec.ID)
	writeJSON(w, http.StatusCreated, bannerRecordToDoc(rec))
}

func putAdminBanner(w http.ResponseWriter, r *http.Request, id string) {
	rec, ok := readAndValidateBannerDoc(w, r, id)
	if !ok {
		return
	}

	created, createdAt, err := database.SaveBanner(rec)
	if err != nil {
		writeAdminInternalError(w, "запись баннера "+id, err)
		return
	}
	rec.CreatedAt = createdAt

	if created {
		log.Printf("admin banner: создан banner_id=%s", id)
		writeJSON(w, http.StatusCreated, bannerRecordToDoc(rec))
		return
	}
	log.Printf("admin banner: заменён banner_id=%s", id)
	writeJSON(w, http.StatusOK, bannerRecordToDoc(rec))
}

func patchAdminBanner(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		IsShow *bool `json:"isShow"`
	}
	if !decodeAdminBody(w, r, &req) {
		return
	}
	if req.IsShow == nil {
		writeAdminValidationError(w, []bannerIssue{{"isShow", "обязательное поле boolean"}})
		return
	}

	found, err := database.SetBannerIsShow(id, *req.IsShow)
	if err != nil {
		writeAdminInternalError(w, "переключение показа баннера "+id, err)
		return
	}
	if !found {
		writeAdminError(w, http.StatusNotFound, adminapi.NOTFOUND, "Баннер не найден", nil)
		return
	}

	rec, err := database.GetBannerByID(id)
	if err == sql.ErrNoRows {
		writeAdminError(w, http.StatusNotFound, adminapi.NOTFOUND, "Баннер не найден", nil)
		return
	}
	if err != nil {
		writeAdminInternalError(w, "чтение баннера "+id, err)
		return
	}
	log.Printf("admin banner: is_show=%t banner_id=%s", *req.IsShow, id)
	writeJSON(w, http.StatusOK, bannerRecordToDoc(rec))
}

func getAdminBanner(w http.ResponseWriter, id string) {
	rec, err := database.GetBannerByID(id)
	if err == sql.ErrNoRows {
		writeAdminError(w, http.StatusNotFound, adminapi.NOTFOUND, "Баннер не найден", nil)
		return
	}
	if err != nil {
		writeAdminInternalError(w, "чтение баннера "+id, err)
		return
	}
	writeJSON(w, http.StatusOK, bannerRecordToDoc(rec))
}

func listAdminBanners(w http.ResponseWriter) {
	records, err := database.ListAllBanners()
	if err != nil {
		writeAdminInternalError(w, "список баннеров", err)
		return
	}
	items := make([]adminapi.BannerDocResponse, len(records))
	for i, rec := range records {
		items[i] = bannerRecordToDoc(rec)
	}
	writeJSON(w, http.StatusOK, adminapi.AdminBannerListResponse{Items: items})
}

func deleteAdminBanner(w http.ResponseWriter, id string) {
	found, err := database.DeleteBanner(id)
	if err != nil {
		writeAdminInternalError(w, "удаление баннера "+id, err)
		return
	}
	if !found {
		writeAdminError(w, http.StatusNotFound, adminapi.NOTFOUND, "Баннер не найден", nil)
		return
	}
	log.Printf("admin banner: удалён banner_id=%s", id)
	w.WriteHeader(http.StatusNoContent)
}

// decodeAdminBody читает JSON-тело; при ошибке сама пишет 400 VALIDATION_ERROR.
func decodeAdminBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, adminBodyLimit)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeAdminValidationError(w, []bannerIssue{{"body", "тело запроса не является корректным JSON нужной структуры: " + err.Error()}})
		return false
	}
	return true
}

// readAndValidateBannerDoc читает BannerDoc, проверяет его целиком (включая
// существование userIds в БД) и собирает запись для сохранения. При ошибке
// сама пишет ответ клиенту.
func readAndValidateBannerDoc(w http.ResponseWriter, r *http.Request, id string) (database.BannerRecord, bool) {
	var doc adminapi.BannerDoc
	if !decodeAdminBody(w, r, &doc) {
		return database.BannerRecord{}, false
	}

	rec, issues := validateBannerDoc(doc, id)
	userIDs := rec.UserIDs
	if len(userIDs) > 0 {
		existing, err := database.ExistingUserIDs(userIDs)
		if err != nil {
			writeAdminInternalError(w, "проверка userIds", err)
			return database.BannerRecord{}, false
		}
		for i, userID := range userIDs {
			if !existing[userID] {
				issues = append(issues, bannerIssue{fmt.Sprintf("userIds[%d]", i), "пользователь не найден"})
			}
		}
	}

	issues = dedupeBannerIssues(issues)
	if len(issues) > 0 {
		writeAdminValidationError(w, issues)
		return database.BannerRecord{}, false
	}
	return rec, true
}

func dedupeBannerIssues(issues []bannerIssue) []bannerIssue {
	seen := map[bannerIssue]bool{}
	var result []bannerIssue
	for _, issue := range issues {
		if seen[issue] {
			continue
		}
		seen[issue] = true
		result = append(result, issue)
	}
	return result
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func adminImageToText(img *adminapi.BannerDocImage) *bannerImage {
	if img == nil {
		return nil
	}
	return &bannerImage{URL: img.Url, Blurhash: nonBlankString(img.Blurhash)}
}

// validateBannerDoc проверяет документ, собирая все найденные проблемы, и
// строит запись для БД (пустые строки нормализуются в NULL). Существование
// userIds проверяется отдельно (требует БД).
func validateBannerDoc(doc adminapi.BannerDoc, id string) (database.BannerRecord, []bannerIssue) {
	var issues []bannerIssue

	rec := database.BannerRecord{
		ID:           id,
		Layout:       string(doc.Layout),
		IsShow:       true,
		Frequency:    string(doc.Frequency),
		StartsAt:     doc.StartsAt,
		EndsAt:       doc.EndsAt,
		Translations: map[string]database.BannerTranslationRecord{},
	}
	if doc.Tone != nil {
		tone := string(*doc.Tone)
		rec.Tone = &tone
	}
	if doc.IsShow != nil {
		rec.IsShow = *doc.IsShow
	}
	if doc.Priority != nil {
		rec.Priority = *doc.Priority
		if rec.Priority < -2147483648 || rec.Priority > 2147483647 {
			issues = append(issues, bannerIssue{"priority", "вне диапазона 32-битного целого"})
		}
	}

	action := bannerAction{}
	if doc.Cta != nil {
		if doc.Cta.Action != nil {
			a := string(*doc.Cta.Action)
			rec.CtaAction = &a
			action.Action = &a
		}
		rec.CtaTarget = nonBlankString(doc.Cta.Target)
		action.Target = rec.CtaTarget
	}

	issues = append(issues, checkBannerEnums(rec.Layout, rec.Tone, rec.Frequency, rec.CtaAction)...)
	if doc.StartsAt != nil && doc.EndsAt != nil && !doc.EndsAt.After(*doc.StartsAt) {
		issues = append(issues, bannerIssue{"endsAt", "должен быть позже startsAt"})
	}

	// Переводы баннера: язык по умолчанию обязателен, остальные — только
	// поддерживаемые; обязательные поля шаблона проверяются на языке по умолчанию.
	var defaultText bannerText
	if _, ok := doc.Translations[defaultLocale]; !ok {
		issues = append(issues, bannerIssue{"translations." + defaultLocale, "обязателен язык по умолчанию"})
	}
	for _, locale := range sortedKeys(doc.Translations) {
		path := "translations." + locale
		if !isSupportedLocale(locale) {
			issues = append(issues, bannerIssue{path, "неподдерживаемый язык (допустимы: " + strings.Join(supportedLocales, ", ") + ")"})
			continue
		}
		tr := doc.Translations[locale]
		issues = append(issues, checkBannerImage(path+".image", adminImageToText(tr.Image))...)

		rec.Translations[locale] = database.BannerTranslationRecord{
			Overline:       nonBlankString(tr.Overline),
			Title:          nonBlankString(tr.Title),
			Body:           nonBlankString(tr.Body),
			Badge:          nonBlankString(tr.Badge),
			CtaLabel:       nonBlankString(tr.CtaLabel),
			SecondaryLabel: nonBlankString(tr.SecondaryLabel),
		}
		if tr.Image != nil {
			stored := rec.Translations[locale]
			stored.ImageURL = nonBlankString(&tr.Image.Url)
			stored.ImageBlurhash = nonBlankString(tr.Image.Blurhash)
			rec.Translations[locale] = stored
		}

		if locale == defaultLocale {
			defaultText = bannerText{
				Overline:       nonBlankString(tr.Overline),
				Title:          nonBlankString(tr.Title),
				Body:           nonBlankString(tr.Body),
				Badge:          nonBlankString(tr.Badge),
				CtaLabel:       nonBlankString(tr.CtaLabel),
				SecondaryLabel: nonBlankString(tr.SecondaryLabel),
			}
			if tr.Image != nil && !isBlankString(&tr.Image.Url) {
				defaultText.Image = adminImageToText(tr.Image)
			}
		}
	}

	// Слайды: позиция — порядок элементов массива, начиная с 1.
	var slides []slideText
	if doc.Slides != nil {
		for i, slide := range *doc.Slides {
			slideRec := database.BannerSlideRecord{
				ID:           uuid.NewString(),
				Position:     i + 1,
				Translations: map[string]database.BannerSlideTranslationRecord{},
			}
			var defaultSlide slideText
			for _, locale := range sortedKeys(slide.Translations) {
				path := fmt.Sprintf("slides[%d].translations.%s", i, locale)
				if !isSupportedLocale(locale) {
					issues = append(issues, bannerIssue{path, "неподдерживаемый язык (допустимы: " + strings.Join(supportedLocales, ", ") + ")"})
					continue
				}
				tr := slide.Translations[locale]
				issues = append(issues, checkBannerImage(path+".image", adminImageToText(tr.Image))...)

				stored := database.BannerSlideTranslationRecord{
					Title: nonBlankString(tr.Title),
					Body:  nonBlankString(tr.Body),
				}
				if tr.Image != nil {
					stored.ImageURL = nonBlankString(&tr.Image.Url)
					stored.ImageBlurhash = nonBlankString(tr.Image.Blurhash)
				}
				slideRec.Translations[locale] = stored

				if locale == defaultLocale {
					defaultSlide = slideText{Title: stored.Title, Body: stored.Body}
					if stored.ImageURL != nil {
						defaultSlide.Image = adminImageToText(tr.Image)
					}
				}
			}
			rec.Slides = append(rec.Slides, slideRec)
			slides = append(slides, defaultSlide)
		}
	}

	issues = append(issues, checkBannerContent(rec.Layout, defaultText, slides, action,
		"translations."+defaultLocale, func(i int) string {
			return fmt.Sprintf("slides[%d].translations.%s", i, defaultLocale)
		})...)

	// Правила таргетинга: не более одного правила каждого типа.
	if doc.Targeting != nil {
		seenTypes := map[string]bool{}
		for i, rule := range *doc.Targeting {
			path := fmt.Sprintf("targeting[%d]", i)
			if seenTypes[rule.Type] {
				issues = append(issues, bannerIssue{path + ".type", "правило типа " + rule.Type + " уже задано"})
				continue
			}
			seenTypes[rule.Type] = true

			params, err := json.Marshal(rule.Params)
			if err != nil {
				issues = append(issues, bannerIssue{path + ".params", err.Error()})
				continue
			}
			if _, err := parseTargetingRule(rule.Type, params); err != nil {
				issues = append(issues, bannerIssue{path, err.Error()})
				continue
			}
			rec.Targeting = append(rec.Targeting, database.BannerTargetingRecord{Type: rule.Type, Params: params})
		}
	}

	if doc.UserIds != nil {
		seenUsers := map[string]bool{}
		for _, userID := range *doc.UserIds {
			value := userID.String()
			if !seenUsers[value] {
				seenUsers[value] = true
				rec.UserIDs = append(rec.UserIDs, value)
			}
		}
	}

	return rec, issues
}

// bannerRecordToDoc собирает нелокализованный документ (все переводы, как хранятся).
func bannerRecordToDoc(rec database.BannerRecord) adminapi.BannerDocResponse {
	id, _ := uuid.Parse(rec.ID)

	doc := adminapi.BannerDocResponse{
		CreatedAt: rec.CreatedAt,
		EndsAt:    rec.EndsAt,
		Frequency: adminapi.AdminBannerFrequency(rec.Frequency),
		Id:        id,
		Layout:    adminapi.AdminBannerLayout(rec.Layout),
		StartsAt:  rec.StartsAt,
	}
	isShow, priority := rec.IsShow, rec.Priority
	doc.IsShow, doc.Priority = &isShow, &priority
	if rec.Tone != nil {
		tone := adminapi.AdminBannerTone(*rec.Tone)
		doc.Tone = &tone
	}
	if rec.CtaAction != nil || rec.CtaTarget != nil {
		cta := adminapi.BannerDocCta{Target: rec.CtaTarget}
		if rec.CtaAction != nil {
			action := adminapi.AdminBannerCtaAction(*rec.CtaAction)
			cta.Action = &action
		}
		doc.Cta = &cta
	}

	doc.Translations = map[string]adminapi.BannerDocTranslation{}
	for locale, tr := range rec.Translations {
		doc.Translations[locale] = adminapi.BannerDocTranslation{
			Overline:       tr.Overline,
			Title:          tr.Title,
			Body:           tr.Body,
			Badge:          tr.Badge,
			Image:          docImage(tr.ImageURL, tr.ImageBlurhash),
			CtaLabel:       tr.CtaLabel,
			SecondaryLabel: tr.SecondaryLabel,
		}
	}

	slides := make([]adminapi.BannerDocSlide, 0, len(rec.Slides))
	for _, slide := range rec.Slides {
		out := adminapi.BannerDocSlide{Translations: map[string]adminapi.BannerDocSlideTranslation{}}
		for locale, tr := range slide.Translations {
			out.Translations[locale] = adminapi.BannerDocSlideTranslation{
				Title: tr.Title,
				Body:  tr.Body,
				Image: docImage(tr.ImageURL, tr.ImageBlurhash),
			}
		}
		slides = append(slides, out)
	}
	doc.Slides = &slides

	targeting := make([]adminapi.BannerDocTargeting, 0, len(rec.Targeting))
	for _, rule := range rec.Targeting {
		params := map[string]interface{}{}
		if err := json.Unmarshal(rule.Params, &params); err != nil || params == nil {
			params = map[string]interface{}{}
		}
		targeting = append(targeting, adminapi.BannerDocTargeting{Type: rule.Type, Params: params})
	}
	doc.Targeting = &targeting

	userIDs := make([]openapi_types.UUID, 0, len(rec.UserIDs))
	for _, raw := range rec.UserIDs {
		if parsed, err := uuid.Parse(raw); err == nil {
			userIDs = append(userIDs, parsed)
		}
	}
	doc.UserIds = &userIDs
	return doc
}

func docImage(url, blurhash *string) *adminapi.BannerDocImage {
	if isBlankString(url) {
		return nil
	}
	return &adminapi.BannerDocImage{Url: *url, Blurhash: blurhash}
}
