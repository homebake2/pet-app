package handlers

import (
	"database/sql"
	"errors"
	"log"
	"myauthservice/database"
	"myauthservice/openapi"
	"net/http"

	"github.com/getsentry/sentry-go"
)

// GetConfigHandler обрабатывает GET /config — публичный эндпоинт
// (Authorization не требуется и не проверяется). Значения читаются из
// таблицы app_config на каждый запрос, без кеша в памяти (см.
// "Конфигурация приложения — Backend").
func GetConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, openapi.BADREQUEST, "Method not allowed")
		return
	}

	cfg, err := database.GetAppConfig()
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			sentry.CaptureException(err)
		}
		log.Printf("Ошибка чтения конфигурации приложения: %v", err)
		writeError(w, http.StatusInternalServerError, openapi.INTERNALERROR, "Не удалось получить конфигурацию приложения")
		return
	}

	writeJSON(w, http.StatusOK, openapi.GetConfigResponse{AboutMeUrl: cfg.AboutMeURL})
}
