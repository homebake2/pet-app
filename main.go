package main

import (
	"log"
	"myauthservice/database"
	"myauthservice/handlers"
	"myauthservice/utils"
	"net/http"
	"os"
)

func main() {
	// Инициализация базы данных
	database.InitDB()

	// Список роутов живёт в handlers.NewMux() — единственном месте, где виден
	// весь HTTP-контракт сервиса. Он не проверяется автоматически против
	// open-api/spec.json, поэтому при добавлении/изменении эндпоинта нужно
	// вручную обновлять оба места (см. openapi/generate.go про регенерацию типов).
	mux := handlers.NewMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Сервер работает!"))
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000" // локально
	}

	log.Printf("Запускаю на :%s", port)

	if err := http.ListenAndServe(":"+port, utils.CORSMiddleware(utils.LoggingMiddleware(mux))); err != nil {
		log.Fatalf("Ошибка: %v", err)
	}
}
