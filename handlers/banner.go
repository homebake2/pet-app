package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"myauthservice/database"
	"myauthservice/openapi"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// GetBannerHandler обрабатывает GET /banner — список баннеров, которые можно
// показать пользователю сейчас, уже локализованных и отсортированных по
// priority DESC, created_at DESC (см. "Баннеры — Backend"). Невалидные
// баннеры исключаются из ответа с записью причины в лог и никогда не
// приводят к 5xx. ETag считается по телу с учётом языка; совпавший
// If-None-Match даёт 304 без тела.
func GetBannerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	language := r.Header.Get("LanguageCode")
	if !isSupportedLocale(language) {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Отсутствует или некорректен заголовок LanguageCode")
		return
	}
	platform := r.URL.Query().Get("platform")
	if !containsString(bannerPlatforms, platform) {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Отсутствует или некорректен параметр platform (ios, android)")
		return
	}
	appVersion, versionOK := parseAppVersion(r.URL.Query().Get("appVersion"))
	if !versionOK {
		writeError(w, http.StatusBadRequest, openapi.BADREQUEST, "Отсутствует или некорректен параметр appVersion (MAJOR.MINOR.PATCH)")
		return
	}

	candidates, err := database.ListBannerCandidates(userID)
	if err != nil {
		log.Printf("banner: ошибка выборки кандидатов: %v", err)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения баннеров")
		return
	}

	items := make([]openapi.Banner, 0, len(candidates))
	if len(candidates) > 0 {
		items, err = buildLocalizedBanners(candidates, userID, language, platform, appVersion)
		if err != nil {
			log.Printf("banner: ошибка выборки данных баннеров: %v", err)
			writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка получения баннеров")
			return
		}
	}

	body, err := json.Marshal(openapi.GetBannerResponse{Items: items})
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка формирования ответа")
		return
	}

	// Язык входит в хеш: один и тот же набор баннеров на разных языках
	// не должен получать одинаковый ETag.
	sum := sha256.Sum256(append([]byte(language+"\n"), body...))
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	w.Header().Set("ETag", etag)

	if ifNoneMatch := r.Header.Get("If-None-Match"); ifNoneMatch != "" && etagMatches(ifNoneMatch, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// buildLocalizedBanners применяет таргетинг к кандидатам, загружает переводы
// и слайды одним запросом на каждую таблицу и собирает локализованные баннеры
// в порядке кандидатов. Ошибку возвращает только при сбое БД; невалидные
// баннеры пропускаются с записью в лог.
func buildLocalizedBanners(candidates []database.BannerRecord, userID, language, platform string, appVersion [3]int) ([]openapi.Banner, error) {
	ids := make([]string, len(candidates))
	for i, c := range candidates {
		ids[i] = c.ID
	}

	targeting, err := database.GetBannerTargeting(ids)
	if err != nil {
		return nil, err
	}

	var petsKnown, petsValue bool
	targetCtx := targetingContext{
		platform:   platform,
		appVersion: appVersion,
		language:   language,
		hasPets: func() (bool, error) {
			if petsKnown {
				return petsValue, nil
			}
			value, err := database.UserHasPets(userID)
			if err != nil {
				return false, err
			}
			petsKnown, petsValue = true, value
			return value, nil
		},
	}

	var matched []database.BannerRecord
	for _, c := range candidates {
		ok, err := matchesTargeting(c.ID, targeting[c.ID], targetCtx)
		if err != nil {
			return nil, err
		}
		if ok {
			matched = append(matched, c)
		}
	}
	if len(matched) == 0 {
		return []openapi.Banner{}, nil
	}

	matchedIDs := make([]string, len(matched))
	var carouselIDs []string
	for i, c := range matched {
		matchedIDs[i] = c.ID
		if c.Layout == "carousel" {
			carouselIDs = append(carouselIDs, c.ID)
		}
	}

	chain := localeChain(language)
	translations, err := database.GetBannerTranslations(matchedIDs, chain)
	if err != nil {
		return nil, err
	}
	var slides map[string][]database.BannerSlideRecord
	if len(carouselIDs) > 0 {
		slides, err = database.GetBannerSlides(carouselIDs, chain)
		if err != nil {
			return nil, err
		}
	}

	items := make([]openapi.Banner, 0, len(matched))
	for _, rec := range matched {
		rec.Translations = translations[rec.ID]
		rec.Slides = slides[rec.ID]
		banner, issues := buildLocalizedBanner(rec, chain)
		if len(issues) > 0 {
			log.Printf("banner %s исключён из выдачи как невалидный: %s", rec.ID, formatBannerIssues(issues))
			continue
		}
		items = append(items, banner)
	}
	return items, nil
}

// matchesTargeting вычисляет правила таргетинга баннера (условия разных типов
// объединяются по И). Невалидное правило исключает баннер и пишется в лог;
// ошибка возвращается только при сбое БД при вычислении has_pets.
func matchesTargeting(bannerID string, rules []database.BannerTargetingRecord, ctx targetingContext) (bool, error) {
	parsed := make([]targetingRule, 0, len(rules))
	for _, rule := range rules {
		evaluator, err := parseTargetingRule(rule.Type, rule.Params)
		if err != nil {
			log.Printf("banner %s исключён из выдачи как невалидный: targeting %s: %v", bannerID, rule.Type, err)
			return false, nil
		}
		parsed = append(parsed, evaluator)
	}
	for _, evaluator := range parsed {
		ok, err := evaluator.matches(ctx)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func formatBannerIssues(issues []bannerIssue) string {
	parts := make([]string, len(issues))
	for i, issue := range issues {
		parts[i] = issue.Path + ": " + issue.Reason
	}
	return strings.Join(parts, "; ")
}

// resolveBannerText выбирает значения переводимых полей баннера по цепочке
// языков независимо по каждому полю; картинка (url + blurhash) берётся из
// одной строки перевода.
func resolveBannerText(chain []string, rows map[string]database.BannerTranslationRecord) bannerText {
	text := bannerText{
		Overline:       pickTranslatedString(chain, rows, func(r database.BannerTranslationRecord) *string { return r.Overline }),
		Title:          pickTranslatedString(chain, rows, func(r database.BannerTranslationRecord) *string { return r.Title }),
		Body:           pickTranslatedString(chain, rows, func(r database.BannerTranslationRecord) *string { return r.Body }),
		Badge:          pickTranslatedString(chain, rows, func(r database.BannerTranslationRecord) *string { return r.Badge }),
		CtaLabel:       pickTranslatedString(chain, rows, func(r database.BannerTranslationRecord) *string { return r.CtaLabel }),
		SecondaryLabel: pickTranslatedString(chain, rows, func(r database.BannerTranslationRecord) *string { return r.SecondaryLabel }),
	}
	imageRow, ok := pickTranslationRow(chain, rows, func(r database.BannerTranslationRecord) bool { return !isBlankString(r.ImageURL) })
	if ok {
		text.Image = &bannerImage{URL: *imageRow.ImageURL, Blurhash: nonBlankString(imageRow.ImageBlurhash)}
	}
	return text
}

func resolveSlideText(chain []string, rows map[string]database.BannerSlideTranslationRecord) slideText {
	slide := slideText{
		Title: pickTranslatedString(chain, rows, func(r database.BannerSlideTranslationRecord) *string { return r.Title }),
		Body:  pickTranslatedString(chain, rows, func(r database.BannerSlideTranslationRecord) *string { return r.Body }),
	}
	imageRow, ok := pickTranslationRow(chain, rows, func(r database.BannerSlideTranslationRecord) bool { return !isBlankString(r.ImageURL) })
	if ok {
		slide.Image = &bannerImage{URL: *imageRow.ImageURL, Blurhash: nonBlankString(imageRow.ImageBlurhash)}
	}
	return slide
}

// buildLocalizedBanner локализует баннер, проверяет его валидность и
// собирает элемент ответа. Для невалидного баннера возвращает список проблем.
func buildLocalizedBanner(rec database.BannerRecord, chain []string) (openapi.Banner, []bannerIssue) {
	issues := checkBannerEnums(rec.Layout, rec.Tone, rec.Frequency, rec.CtaAction)
	if len(issues) > 0 {
		return openapi.Banner{}, issues
	}

	spec := layoutSpecs[rec.Layout]
	text := resolveBannerText(chain, rec.Translations)

	var slides []slideText
	if spec.slides {
		slides = make([]slideText, len(rec.Slides))
		for i, slide := range rec.Slides {
			slides[i] = resolveSlideText(chain, slide.Translations)
		}
	}

	action := bannerAction{Action: rec.CtaAction, Target: nonBlankString(rec.CtaTarget)}
	issues = checkBannerContent(rec.Layout, text, slides, action, "translation", func(i int) string {
		return "slides[" + strconv.Itoa(i) + "]"
	})
	if len(issues) > 0 {
		return openapi.Banner{}, issues
	}

	id, err := uuid.Parse(rec.ID)
	if err != nil {
		return openapi.Banner{}, []bannerIssue{{"id", "не uuid"}}
	}

	banner := openapi.Banner{
		Id:        id,
		Layout:    openapi.BannerLayout(rec.Layout),
		Frequency: openapi.BannerFrequency(rec.Frequency),
	}
	if rec.Tone != nil {
		tone := openapi.BannerTone(*rec.Tone)
		banner.Tone = &tone
	}

	// Поля, не входящие в состав шаблона, сервером игнорируются (остаются null).
	if spec.title {
		banner.Title = text.Title
	}
	if spec.overline {
		banner.Overline = text.Overline
	}
	if spec.badge {
		banner.Badge = text.Badge
	}
	if spec.body {
		banner.Body = text.Body
	}
	if spec.secondary {
		banner.SecondaryLabel = text.SecondaryLabel
	}
	if spec.image && text.Image != nil {
		banner.Image = &openapi.BannerImage{Url: text.Image.URL, Blurhash: text.Image.Blurhash}
	}
	if spec.slides {
		out := make([]openapi.BannerSlide, len(slides))
		for i, slide := range slides {
			out[i] = openapi.BannerSlide{Title: *slide.Title, Body: slide.Body}
			if slide.Image != nil {
				out[i].Image = &openapi.BannerImage{Url: slide.Image.URL, Blurhash: slide.Image.Blurhash}
			}
		}
		banner.Slides = &out
	}
	if rec.CtaAction != nil {
		cta := openapi.BannerPrimaryCta{Action: openapi.BannerCtaAction(*rec.CtaAction), Label: text.CtaLabel}
		if *rec.CtaAction != "store" {
			cta.Target = action.Target
		}
		banner.PrimaryCta = &cta
	}
	return banner, nil
}
