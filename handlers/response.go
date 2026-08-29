package handlers

import (
	"database/sql"
	"encoding/json"
	"myauthservice/database"
	"myauthservice/openapi"
	"myauthservice/utils"
	"net"
	"net/http"
	"strings"
	"time"
)

// writeJSON записывает JSON-тело с заданным статусом.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		json.NewEncoder(w).Encode(body)
	}
}

// writeError записывает ответ об ошибке в формате { code, message },
// как того требует components.schemas.GetErrorResponse из open-api/spec.json.
func writeError(w http.ResponseWriter, status int, code openapi.ErrorCodeEnum, message string) {
	writeJSON(w, status, openapi.GetErrorResponse{
		Code:    code,
		Message: message,
	})
}

// requireUserID проверяет Bearer-токен из заголовка Authorization.
// При ошибке сама пишет ответ клиенту и возвращает ok=false —
// вызывающему достаточно сделать `if !ok { return }`.
func requireUserID(w http.ResponseWriter, r *http.Request) (userID string, ok bool) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		writeError(w, http.StatusUnauthorized, openapi.UNAUTHORIZED, "Отсутствует токен авторизации")
		return "", false
	}

	tokenStr, found := strings.CutPrefix(authHeader, "Bearer ")
	if !found || tokenStr == "" {
		writeError(w, http.StatusUnauthorized, openapi.UNAUTHORIZED, "Некорректный заголовок авторизации")
		return "", false
	}

	claims, err := utils.ParseToken(tokenStr)
	if err != nil {
		writeError(w, http.StatusUnauthorized, openapi.UNAUTHORIZED, "Токен недействителен или истёк")
		return "", false
	}

	id, isString := claims["id"].(string)
	if !isString || id == "" {
		writeError(w, http.StatusUnauthorized, openapi.UNAUTHORIZED, "Невалидный токен")
		return "", false
	}

	iat, isNumber := claims["iat"].(float64)
	if !isNumber {
		writeError(w, http.StatusUnauthorized, openapi.UNAUTHORIZED, "Невалидный токен")
		return "", false
	}

	invalidatedAt, err := database.GetTokensInvalidatedAt(id)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusUnauthorized, openapi.UNAUTHORIZED, "Пользователь не найден")
		return "", false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Ошибка базы данных")
		return "", false
	}
	if invalidatedAt.Valid && time.Unix(int64(iat), 0).Before(invalidatedAt.Time) {
		writeError(w, http.StatusUnauthorized, openapi.UNAUTHORIZED, "Токен отозван")
		return "", false
	}

	return id, true
}

// clientIP определяет IP-адрес клиента для rate-limiting: сначала пробует
// первый адрес из X-Forwarded-For (запрос мог пройти через прокси/балансировщик),
// иначе берёт r.RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		if ip := strings.TrimSpace(first); ip != "" {
			return ip
		}
	}

	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && host != "" {
		return host
	}

	return r.RemoteAddr
}
