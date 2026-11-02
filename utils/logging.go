package utils

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/getsentry/sentry-go"
)

// responseRecorder перехватывает статус и тело ответа, чтобы залогировать их
// после того, как хендлер отработает.
type responseRecorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (r *responseRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

// LoggingMiddleware логирует метод, путь, тело запроса, статус и тело ответа
// для каждого HTTP-запроса. Предназначено для вывода в stdout контейнера.
// Секретные поля тел (пароли, токены, коды, подписанные ссылки) маскируются —
// см. MaskBodyForLog.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody []byte
		if r.Body != nil {
			reqBody, _ = io.ReadAll(r.Body)
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(reqBody))
		}

		rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}

		// Версия мобильного приложения: в тег Sentry (ошибки и паники этого
		// запроса видно в разрезе версий) и в строку лога.
		appVersion := AppVersionFromRequest(r)
		if appVersion != "" {
			if hub := sentry.GetHubFromContext(r.Context()); hub != nil {
				hub.Scope().SetTag("app_version", appVersion)
			}
		}

		next.ServeHTTP(rec, r)

		// Обработанные 5xx не паникуют, поэтому sentryhttp их не видит.
		if rec.status >= http.StatusInternalServerError {
			if hub := sentry.GetHubFromContext(r.Context()); hub != nil {
				hub.CaptureMessage(fmt.Sprintf("%s %s: status %d", r.Method, r.URL.Path, rec.status))
			}
		}

		log.Printf(
			"%s %s | ip=%s | app_version=%s | request_body=%s | status=%d | response_body=%s",
			r.Method, r.URL.Path, ClientIP(r), appVersion, MaskBodyForLog(reqBody), rec.status, MaskBodyForLog(rec.body.Bytes()),
		)
	})
}
