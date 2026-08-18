package main

import (
	"log"
	"myauthservice/database"
	"myauthservice/handlers"
	"net/http"
	"os"
)

func main() {
	// Инициализация базы данных
	database.InitDB()

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/register", handlers.RegisterHandler)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Сервер работает!"))
	})
	mux.HandleFunc("/auth/login", handlers.LoginHandler)
	mux.HandleFunc("/auth/refresh", handlers.RefreshTokenHandler)
	mux.HandleFunc("/auth/logout", handlers.LogoutHandler)
	mux.HandleFunc("/profile", handlers.ProfileHandler)
	mux.HandleFunc("/pet/", handlers.PetByIDHandler)
	mux.HandleFunc("/pet", handlers.PetHandler)
	mux.HandleFunc("/events", handlers.CreateEventHandler)
	mux.HandleFunc("/events/", handlers.EventIDResonseHandler)
	mux.HandleFunc("/activities", handlers.GetActivitiesHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000" // локально
	}

	log.Printf("Запускаю на :%s", port)

	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Ошибка: %v", err)
	}
}
