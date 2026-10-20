package main

import (
	"log"
	"myauthservice/database"
	"myauthservice/handlers"
	"myauthservice/s3client"
	"myauthservice/utils"
	"net/http"
	"os"
	"time"
)

// runPetTransferCleanup периодически переводит истёкшие активные передачи
// питомцев в expired: сразу при старте и затем раз в interval. Истёкшие
// передачи и без этого трактуются как expired при обращении к ним — очистка
// лишь приводит статусы в БД в соответствие со сроком.
func runPetTransferCleanup(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		expired, err := database.ExpireStalePetTransfers()
		if err != nil {
			log.Printf("Ошибка очистки истёкших передач питомцев: %v", err)
		} else if expired > 0 {
			log.Printf("Истёкших передач питомцев переведено в expired: %d", expired)
		}
		<-ticker.C
	}
}

func main() {
	// Инициализация базы данных
	database.InitDB()

	// Конфигурация S3-совместимого хранилища (Backblaze B2) для
	// generic-механизма файлов сущностей — presigned PUT/GET URL, backend
	// никогда не проксирует байты файла (см. handlers/files.go).
	s3Config, err := s3client.ConfigFromEnv()
	if err != nil {
		log.Fatalf("Ошибка конфигурации S3-хранилища: %v", err)
	}
	handlers.SetStorage(s3client.New(s3Config))

	// Список роутов живёт в handlers.NewMux() — единственном месте, где виден
	// весь HTTP-контракт сервиса. Он не проверяется автоматически против
	// open-api/spec.json, поэтому при добавлении/изменении эндпоинта нужно
	// вручную обновлять оба места (см. openapi/generate.go про регенерацию типов).
	mux := handlers.NewMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Сервер работает!"))
	})

	go runPetTransferCleanup(time.Hour)

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000" // локально
	}

	log.Printf("Запускаю на :%s", port)

	if err := http.ListenAndServe(":"+port, utils.CORSMiddleware(utils.LoggingMiddleware(mux))); err != nil {
		log.Fatalf("Ошибка: %v", err)
	}
}
