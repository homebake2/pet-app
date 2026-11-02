package handlers

import (
	"encoding/json"
	"log"
	"myauthservice/database"
	"myauthservice/openapi/webapi"
	"net/http"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/getsentry/sentry-go"
	"github.com/google/uuid"
)

// Методы для сайта pet-web. Контракт — open-api/web-spec.json (пакет webapi);
// мобильный клиент эти методы не вызывает.

// webAPIKeyEnv — переменная окружения с ключом сайта (заголовок X-Api-Key).
const webAPIKeyEnv = "WEB_API_KEY"

const webBodyLimit = 64 << 10

const (
	suggestionTitleMax   = 200
	suggestionBodyMax    = 5000
	suggestionContactMax = 200
)

var suggestionUserIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

var suggestionCategories = map[webapi.SuggestionCategory]bool{
	webapi.Pets:      true,
	webapi.Calendar:  true,
	webapi.Reminders: true,
	webapi.Dynamics:  true,
	webapi.Access:    true,
	webapi.Other:     true,
}

// requireWebAPIKey проверяет доступ по X-Api-Key. Если ключ в конфигурации не
// задан, методы сайта отключены и отвечают как несуществующий маршрут (404):
// пустой ключ не даёт доступа. Неверный или отсутствующий ключ — 401. Значение
// ключа не логируется.
func requireWebAPIKey(w http.ResponseWriter, r *http.Request) bool {
	configured := os.Getenv(webAPIKeyEnv)
	if configured == "" {
		http.NotFound(w, r)
		return false
	}
	if !keysEqual(r.Header.Get("X-Api-Key"), configured) {
		writeWebError(w, http.StatusUnauthorized, webapi.UNAUTHORIZED, "Отсутствует или неверен X-Api-Key", nil)
		return false
	}
	return true
}

func writeWebError(w http.ResponseWriter, status int, code webapi.WebErrorCode, message string, payload any) {
	body := webapi.WebErrorResponse{Code: code, Message: message}
	if payload != nil {
		body.Payload = &payload
	}
	writeJSON(w, status, body)
}

// CreateWebSuggestionHandler обрабатывает POST /web/suggestions — приём
// предложения с формы «Новые идеи» на сайте.
func CreateWebSuggestionHandler(w http.ResponseWriter, r *http.Request) {
	if !requireWebAPIKey(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		writeWebError(w, http.StatusMethodNotAllowed, webapi.BADREQUEST, "Method not allowed", nil)
		return
	}

	var req webapi.CreateSuggestionRequest
	r.Body = http.MaxBytesReader(w, r.Body, webBodyLimit)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeWebError(w, http.StatusBadRequest, webapi.BADREQUEST, "Тело запроса не является корректным JSON", nil)
		return
	}

	title := strings.TrimSpace(req.Title)
	body := strings.TrimSpace(derefString(req.Body))
	contact := strings.TrimSpace(derefString(req.Contact))

	var issues []bannerIssue
	if !suggestionUserIDPattern.MatchString(req.UserId) {
		issues = append(issues, bannerIssue{"userId", "должен соответствовать ^[A-Za-z0-9_-]{1,64}$"})
	}
	if !suggestionCategories[req.Category] {
		issues = append(issues, bannerIssue{"category", "неизвестный раздел"})
	}
	if title == "" {
		issues = append(issues, bannerIssue{"title", "не должен быть пустым"})
	} else if utf8.RuneCountInString(title) > suggestionTitleMax {
		issues = append(issues, bannerIssue{"title", "слишком длинный"})
	}
	if utf8.RuneCountInString(body) > suggestionBodyMax {
		issues = append(issues, bannerIssue{"body", "слишком длинный"})
	}
	if utf8.RuneCountInString(contact) > suggestionContactMax {
		issues = append(issues, bannerIssue{"contact", "слишком длинный"})
	}
	if len(issues) > 0 {
		writeWebError(w, http.StatusBadRequest, webapi.VALIDATIONERROR, "Предложение не прошло валидацию", issues)
		return
	}

	id, err := database.CreateSuggestion(req.UserId, string(req.Category), title, body, contact)
	if err != nil {
		sentry.CaptureException(err)
		log.Printf("web suggestion: не удалось сохранить предложение: %v", err)
		writeWebError(w, http.StatusInternalServerError, webapi.INTERNALERROR, "Ошибка базы данных", nil)
		return
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		log.Printf("web suggestion: БД вернула некорректный id %q: %v", id, err)
		writeWebError(w, http.StatusInternalServerError, webapi.INTERNALERROR, "Ошибка базы данных", nil)
		return
	}
	writeJSON(w, http.StatusCreated, webapi.CreateSuggestionResponse{Id: parsed})
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
