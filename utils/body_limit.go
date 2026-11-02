package utils

import (
	"encoding/json"
	"net/http"
)

// MaxRequestBodyBytes — предельный размер тела запроса. Файлы пользователей
// идут в хранилище по presigned-ссылкам и через сервис не проходят, так что
// все тела — небольшой JSON.
const MaxRequestBodyBytes = 1 << 20

// BodyLimitMiddleware отклоняет запросы с телом больше limit байт: по
// Content-Length — сразу ответом 413, а для тел без длины (chunked) ограничивает
// чтение, и дочитывание сверх лимита завершается ошибкой чтения.
func BodyLimitMiddleware(limit int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > limit {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"code":    "BAD_REQUEST",
				"message": "Тело запроса слишком большое",
			})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}
