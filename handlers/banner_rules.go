package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Правила баннеров, общие для чтения (GET /banner: невалидный баннер
// исключается из выдачи) и записи (admin API: невалидный документ
// отклоняется). Обе стороны вызывают одни и те же проверки.

const maxBannerSlides = 10

var (
	bannerLayouts     = []string{"sheet", "cover", "dialog", "carousel", "poster"}
	bannerTones       = []string{"info", "attention", "negative", "positive", "promo"}
	bannerFrequencies = []string{"once", "every_launch"}
	bannerCtaActions  = []string{"url", "deeplink", "store"}
	bannerPlatforms   = []string{"ios", "android"}

	appVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
)

func containsString(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// bannerIssue — одна найденная проблема: путь к полю и причина.
type bannerIssue struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// layoutSpec описывает, какие поля использует шаблон и какие из них обязательны.
type layoutSpec struct {
	title         bool // заголовок используется (и обязателен)
	overline      bool
	badge         bool
	body          bool
	image         bool
	imageRequired bool
	secondary     bool
	slides        bool
	ctaLabel      bool // подпись действия обязательна, если действие задано
}

var layoutSpecs = map[string]layoutSpec{
	"sheet":    {title: true, overline: true, body: true, image: true, secondary: true, ctaLabel: true},
	"cover":    {title: true, badge: true, body: true, image: true, imageRequired: true, ctaLabel: true},
	"dialog":   {title: true, badge: true, body: true, ctaLabel: true},
	"carousel": {overline: true, secondary: true, slides: true, ctaLabel: true},
	"poster":   {badge: true, image: true, imageRequired: true},
}

// bannerImage — пара image_url/blurhash, выбранная из одной строки перевода.
type bannerImage struct {
	URL      string
	Blurhash *string
}

// bannerText — значения переводимых полей баннера на одном языке
// (уже выбранные по цепочке на чтении либо взятые из языка по умолчанию на записи).
type bannerText struct {
	Overline       *string
	Title          *string
	Body           *string
	Badge          *string
	Image          *bannerImage
	CtaLabel       *string
	SecondaryLabel *string
}

type slideText struct {
	Title *string
	Body  *string
	Image *bannerImage
}

type bannerAction struct {
	Action *string
	Target *string
}

// checkBannerEnums проверяет значения enum-полей баннера.
func checkBannerEnums(layout string, tone *string, frequency string, ctaAction *string) []bannerIssue {
	var issues []bannerIssue
	if !containsString(bannerLayouts, layout) {
		issues = append(issues, bannerIssue{"layout", "допустимы: " + strings.Join(bannerLayouts, ", ")})
	}
	if tone != nil && !containsString(bannerTones, *tone) {
		issues = append(issues, bannerIssue{"tone", "допустимы: " + strings.Join(bannerTones, ", ")})
	}
	if !containsString(bannerFrequencies, frequency) {
		issues = append(issues, bannerIssue{"frequency", "допустимы: " + strings.Join(bannerFrequencies, ", ")})
	}
	if ctaAction != nil && !containsString(bannerCtaActions, *ctaAction) {
		issues = append(issues, bannerIssue{"cta.action", "допустимы: " + strings.Join(bannerCtaActions, ", ")})
	}
	return issues
}

// isAbsoluteHTTPSURL: абсолютный URL со схемой https и непустым хостом.
func isAbsoluteHTTPSURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.Scheme == "https" && u.Host != ""
}

// checkBannerImage проверяет, что URL картинки — абсолютный https.
func checkBannerImage(path string, img *bannerImage) []bannerIssue {
	if img == nil || isAbsoluteHTTPSURL(img.URL) {
		return nil
	}
	return []bannerIssue{{path + ".url", "должен быть абсолютным https URL"}}
}

// checkBannerContent проверяет обязательные поля шаблона. text — значения на
// выбранном языке; textPath — префикс путей для сообщений (например,
// "translations.ru"); slidePath(i) — префикс пути i-го слайда. Enum-поля
// проверяет checkBannerEnums; для неизвестного layout здесь ничего не
// проверяется.
func checkBannerContent(layout string, text bannerText, slides []slideText, action bannerAction, textPath string, slidePath func(i int) string) []bannerIssue {
	spec, known := layoutSpecs[layout]
	if !known {
		return nil
	}

	var issues []bannerIssue
	if spec.title && isBlankString(text.Title) {
		issues = append(issues, bannerIssue{textPath + ".title", "обязательно для шаблона " + layout})
	}
	if spec.imageRequired && text.Image == nil {
		issues = append(issues, bannerIssue{textPath + ".image", "обязательно для шаблона " + layout})
	}
	if spec.image {
		issues = append(issues, checkBannerImage(textPath+".image", text.Image)...)
	}

	if spec.slides {
		if len(slides) < 1 || len(slides) > maxBannerSlides {
			issues = append(issues, bannerIssue{"slides", fmt.Sprintf("для шаблона carousel нужно от 1 до %d слайдов, передано %d", maxBannerSlides, len(slides))})
		}
		for i, slide := range slides {
			if isBlankString(slide.Title) {
				issues = append(issues, bannerIssue{slidePath(i) + ".title", "обязательно для слайда"})
			}
			issues = append(issues, checkBannerImage(slidePath(i)+".image", slide.Image)...)
		}
	}

	issues = append(issues, checkBannerAction(spec, text, action, textPath)...)
	return issues
}

// checkBannerAction проверяет действие: тип, цель и подпись.
func checkBannerAction(spec layoutSpec, text bannerText, action bannerAction, textPath string) []bannerIssue {
	if action.Action == nil || !containsString(bannerCtaActions, *action.Action) {
		return nil
	}

	var issues []bannerIssue
	switch *action.Action {
	case "url":
		if isBlankString(action.Target) {
			issues = append(issues, bannerIssue{"cta.target", "обязательно для действия url"})
		} else if !isAbsoluteHTTPSURL(*action.Target) {
			issues = append(issues, bannerIssue{"cta.target", "для действия url должен быть абсолютным https URL"})
		}
	case "deeplink":
		if isBlankString(action.Target) {
			issues = append(issues, bannerIssue{"cta.target", "обязательно для действия deeplink"})
		}
	}
	if spec.ctaLabel && isBlankString(text.CtaLabel) {
		issues = append(issues, bannerIssue{textPath + ".ctaLabel", "обязательно, если задано действие"})
	}
	return issues
}

// --- Правила таргетинга ---

// targetingContext — данные клиента, по которым вычисляются правила.
type targetingContext struct {
	platform   string
	appVersion [3]int
	language   string
	// hasPets вычисляется лениво: запрос в БД делается, только если у какого-либо
	// кандидата есть правило has_pets.
	hasPets func() (bool, error)
}

// targetingRule — вычислитель одного типа правила. Новый тип условия — это
// новая ветка в parseTargetingRule и новый тип, реализующий этот интерфейс.
type targetingRule interface {
	matches(ctx targetingContext) (bool, error)
}

// parseTargetingRule разбирает params правила типа ruleType и проверяет их
// соответствие типу; неизвестный тип или несоответствие — ошибка.
func parseTargetingRule(ruleType string, params json.RawMessage) (targetingRule, error) {
	switch ruleType {
	case "platform":
		var p struct {
			Values []string `json:"values"`
		}
		if err := decodeStrictParams(params, &p); err != nil {
			return nil, err
		}
		if len(p.Values) == 0 {
			return nil, fmt.Errorf("values не может быть пустым")
		}
		for _, v := range p.Values {
			if !containsString(bannerPlatforms, v) {
				return nil, fmt.Errorf("неизвестная платформа %q", v)
			}
		}
		return platformRule{values: p.Values}, nil
	case "app_version":
		var p struct {
			Min *string `json:"min"`
			Max *string `json:"max"`
		}
		if err := decodeStrictParams(params, &p); err != nil {
			return nil, err
		}
		if p.Min == nil && p.Max == nil {
			return nil, fmt.Errorf("нужен хотя бы один из ключей min, max")
		}
		rule := appVersionRule{}
		if p.Min != nil {
			v, ok := parseAppVersion(*p.Min)
			if !ok {
				return nil, fmt.Errorf("min должен быть в формате MAJOR.MINOR.PATCH")
			}
			rule.min = &v
		}
		if p.Max != nil {
			v, ok := parseAppVersion(*p.Max)
			if !ok {
				return nil, fmt.Errorf("max должен быть в формате MAJOR.MINOR.PATCH")
			}
			rule.max = &v
		}
		if rule.min != nil && rule.max != nil && compareAppVersions(*rule.min, *rule.max) > 0 {
			return nil, fmt.Errorf("min не может быть больше max")
		}
		return rule, nil
	case "language":
		var p struct {
			Values []string `json:"values"`
		}
		if err := decodeStrictParams(params, &p); err != nil {
			return nil, err
		}
		if len(p.Values) == 0 {
			return nil, fmt.Errorf("values не может быть пустым")
		}
		for _, v := range p.Values {
			if !isSupportedLocale(v) {
				return nil, fmt.Errorf("неподдерживаемый язык %q", v)
			}
		}
		return languageRule{values: p.Values}, nil
	case "has_pets":
		var p struct {
			Value *bool `json:"value"`
		}
		if err := decodeStrictParams(params, &p); err != nil {
			return nil, err
		}
		if p.Value == nil {
			return nil, fmt.Errorf("value обязателен и должен быть boolean")
		}
		return hasPetsRule{value: *p.Value}, nil
	default:
		return nil, fmt.Errorf("неизвестный тип правила %q", ruleType)
	}
}

// decodeStrictParams разбирает params в структуру, отвергая неизвестные ключи,
// не-объекты и null.
func decodeStrictParams(params json.RawMessage, dst any) error {
	trimmed := bytes.TrimSpace(params)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("params должен быть объектом")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("params не соответствуют типу правила: %v", err)
	}
	return nil
}

type platformRule struct{ values []string }

func (r platformRule) matches(ctx targetingContext) (bool, error) {
	return containsString(r.values, ctx.platform), nil
}

type languageRule struct{ values []string }

func (r languageRule) matches(ctx targetingContext) (bool, error) {
	return containsString(r.values, ctx.language), nil
}

type appVersionRule struct{ min, max *[3]int }

func (r appVersionRule) matches(ctx targetingContext) (bool, error) {
	if r.min != nil && compareAppVersions(ctx.appVersion, *r.min) < 0 {
		return false, nil
	}
	if r.max != nil && compareAppVersions(ctx.appVersion, *r.max) > 0 {
		return false, nil
	}
	return true, nil
}

type hasPetsRule struct{ value bool }

func (r hasPetsRule) matches(ctx targetingContext) (bool, error) {
	has, err := ctx.hasPets()
	if err != nil {
		return false, err
	}
	return has == r.value, nil
}

// parseAppVersion разбирает MAJOR.MINOR.PATCH в числовые компоненты.
func parseAppVersion(s string) ([3]int, bool) {
	var result [3]int
	if !appVersionPattern.MatchString(s) {
		return result, false
	}
	for i, part := range strings.Split(s, ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return result, false
		}
		result[i] = n
	}
	return result, true
}

// compareAppVersions сравнивает версии покомпонентно как числа (1.10.0 > 1.9.0).
func compareAppVersions(a, b [3]int) int {
	for i := 0; i < 3; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}
