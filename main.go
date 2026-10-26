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

	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"
)

// initSentry включает отправку ошибок и трейсов в Sentry. Без SENTRY_DSN SDK
// остаётся выключенным (локальная разработка). Release берётся из
// SENTRY_RELEASE, а на Render — из RENDER_GIT_COMMIT.
func initSentry() {
	release := os.Getenv("SENTRY_RELEASE")
	if release == "" {
		release = os.Getenv("RENDER_GIT_COMMIT")
	}

	err := sentry.Init(sentry.ClientOptions{
		Dsn:              os.Getenv("SENTRY_DSN"),
		Environment:      os.Getenv("SENTRY_ENVIRONMENT"),
		Release:          release,
		SendDefaultPII:   false,
		AttachStacktrace: true,
		EnableTracing:    true,
		TracesSampleRate: 0.1,
	})
	if err != nil {
		log.Printf("Ошибка инициализации Sentry: %v", err)
	}
}

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
			sentry.CaptureException(err)
			log.Printf("Ошибка очистки истёкших передач питомцев: %v", err)
		} else if expired > 0 {
			log.Printf("Истёкших передач питомцев переведено в expired: %d", expired)
		}
		<-ticker.C
	}
}

// runPetAccessInviteCleanup периодически переводит истёкшие приглашения к
// доступу в expired, а приглашения без действующих питомцев — в cancelled:
// сразу при старте и затем раз в interval. Такие приглашения и без этого
// трактуются как неактивные при обращении к ним — очистка лишь приводит
// статусы в БД в соответствие.
func runPetAccessInviteCleanup(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		changed, err := database.ExpireStalePetAccessInvites()
		if err != nil {
			sentry.CaptureException(err)
			log.Printf("Ошибка очистки приглашений к доступу: %v", err)
		} else if changed > 0 {
			log.Printf("Приглашений к доступу переведено в expired/cancelled: %d", changed)
		}
		<-ticker.C
	}
}

func main() {
	initSentry()
	defer sentry.Flush(2 * time.Second)

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
	go runPetAccessInviteCleanup(time.Hour)

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000" // локально
	}

	log.Printf("Запускаю на :%s", port)

	// Sentry — самый внешний слой: ловит паники всех слоёв ниже и ведёт трейс
	// запроса. Repanic оставляет панику net/http, который сам закроет соединение.
	sentryHandler := sentryhttp.New(sentryhttp.Options{Repanic: true})

	handler := sentryHandler.Handle(utils.CORSMiddleware(utils.LoggingMiddleware(mux)))
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		sentry.CaptureException(err)
		sentry.Flush(2 * time.Second)
		log.Fatalf("Ошибка: %v", err)
	}
}
