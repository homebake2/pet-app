package utils

import (
	"net/http"
	"regexp"
)

// AppVersionHeader — заголовок, в котором мобильное приложение сообщает свою
// версию в каждом запросе.
const AppVersionHeader = "X-App-Version"

var appVersionPattern = regexp.MustCompile(`^\d{1,5}\.\d{1,5}\.\d{1,5}$`)

// AppVersionFromRequest возвращает версию приложения из заголовка или пустую
// строку, если заголовка нет или значение не в формате MAJOR.MINOR.PATCH.
// Значение приходит от клиента и используется только для диагностики (лог,
// тег Sentry), поэтому нестандартное значение отбрасывается, а не отклоняет
// запрос.
func AppVersionFromRequest(r *http.Request) string {
	value := r.Header.Get(AppVersionHeader)
	if !appVersionPattern.MatchString(value) {
		return ""
	}
	return value
}
