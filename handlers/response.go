package handlers

import (
	"database/sql"
	"encoding/json"
	"myauthservice/database"
	"myauthservice/openapi"
	"myauthservice/utils"
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

// requireUserID проверяет Bearer-токен из заголовка Authorization и то, что
// пользователь принял текущие версии документов (иначе 403 CONSENT_REQUIRED).
// При ошибке сама пишет ответ клиенту и возвращает ok=false —
// вызывающему достаточно сделать `if !ok { return }`.
func requireUserID(w http.ResponseWriter, r *http.Request) (userID string, ok bool) {
	return authenticate(w, r, true)
}

// requireUserIDWithoutConsent — то же, что requireUserID, но без проверки
// принятия документов. Только для методов, которые должны работать до
// принятия: статус и принятие документов, удаление аккаунта.
func requireUserIDWithoutConsent(w http.ResponseWriter, r *http.Request) (userID string, ok bool) {
	return authenticate(w, r, false)
}

func authenticate(w http.ResponseWriter, r *http.Request, requireConsent bool) (userID string, ok bool) {
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

	invalidatedAt, consentAccepted, err := database.GetAuthState(id)
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
	if requireConsent && !consentAccepted {
		writeError(w, http.StatusForbidden, openapi.CONSENTREQUIRED, "Требуется принять условия использования и согласие на обработку персональных данных")
		return "", false
	}

	return id, true
}
