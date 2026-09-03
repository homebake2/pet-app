package handlers

import "net/http"

// NewMux строит http.ServeMux с зарегистрированными маршрутами сервиса.
// Вынесено из main.go, чтобы интеграционные тесты могли поднять тот же
// набор роутов поверх httptest.Server, не дублируя список.
func NewMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/register", RegisterHandler)
	mux.HandleFunc("/auth/login", LoginHandler)
	mux.HandleFunc("/auth/refresh", RefreshTokenHandler)
	mux.HandleFunc("/auth/logout", LogoutHandler)
	mux.HandleFunc("/auth/guest", GuestHandler)
	mux.HandleFunc("/profile", ProfileHandler)
	mux.HandleFunc("/pet/", PetByIDHandler)
	mux.HandleFunc("/pet", PetHandler)
	mux.HandleFunc("/events", CreateEventHandler)
	mux.HandleFunc("/events/", EventIDResponseHandler) // PATCH /events/{id} — частичное обновление
	mux.HandleFunc("/activities", GetActivitiesHandler)
	mux.HandleFunc("/import/local-data", ImportLocalDataHandler)
	return mux
}
