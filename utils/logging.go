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
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody []byte
		if r.Body != nil {
			reqBody, _ = io.ReadAll(r.Body)
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(reqBody))
		}

		rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		// Обработанные 5xx не паникуют, поэтому sentryhttp их не видит.
		if rec.status >= http.StatusInternalServerError {
			if hub := sentry.GetHubFromContext(r.Context()); hub != nil {
				hub.CaptureMessage(fmt.Sprintf("%s %s: status %d", r.Method, r.URL.Path, rec.status))
			}
		}

		log.Printf(
			"%s %s | request_body=%s | status=%d | response_body=%s",
			r.Method, r.URL.Path, string(reqBody), rec.status, rec.body.String(),
		)
	})
}
