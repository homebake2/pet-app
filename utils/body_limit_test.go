package utils

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBodyLimitMiddleware(t *testing.T) {
	var readErr error
	handler := BodyLimitMiddleware(10, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("тело в пределах лимита проходит", func(t *testing.T) {
		readErr = nil
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("POST", "/", strings.NewReader("0123456789")))

		if rec.Code != http.StatusOK || readErr != nil {
			t.Fatalf("status=%d readErr=%v", rec.Code, readErr)
		}
	})

	t.Run("известная длина больше лимита — 413 без вызова хендлера", func(t *testing.T) {
		readErr = nil
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("POST", "/", strings.NewReader("0123456789ab")))

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status=%d, want 413", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"code":"BAD_REQUEST"`) {
			t.Fatalf("тело ответа без кода ошибки: %s", rec.Body.String())
		}
	})

	t.Run("тело без длины больше лимита — ошибка чтения", func(t *testing.T) {
		readErr = nil
		req := httptest.NewRequest("POST", "/", io.NopCloser(strings.NewReader("0123456789ab")))
		req.ContentLength = -1
		handler.ServeHTTP(httptest.NewRecorder(), req)

		if readErr == nil {
			t.Fatal("ожидалась ошибка чтения тела сверх лимита")
		}
	})
}
