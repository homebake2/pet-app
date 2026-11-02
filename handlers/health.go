package handlers

import (
	"context"
	"log"
	"myauthservice/database"
	"net/http"
	"time"
)

// healthCheckTimeout ограничивает проверку БД: монитор доступности должен
// получить ответ быстрее, чем сработает его собственный таймаут.
const healthCheckTimeout = 3 * time.Second

// HealthHandler обрабатывает GET/HEAD /healthz — проверку живости сервиса для
// внешнего монитора и health check хостинга. 200 означает, что процесс
// принимает запросы и БД отвечает; при недоступной БД — 503. Без авторизации
// и вне OpenAPI-контракта мобильного клиента; в ответе нет деталей ошибки.
func HealthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), healthCheckTimeout)
	defer cancel()

	var one int
	if err := database.DB.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		log.Printf("healthz: БД недоступна: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
