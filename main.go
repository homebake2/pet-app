package main

import (
	"context"
	"log"
	"myauthservice/database"
	"myauthservice/handlers"
	"myauthservice/s3client"
	"myauthservice/utils"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"
)

// shutdownTimeout — сколько ждём завершения текущих запросов после SIGTERM.
// Render по умолчанию дожидается остановки сервиса около 30 секунд.
const shutdownTimeout = 25 * time.Second

// sentryEnvironment возвращает окружение событий Sentry: SENTRY_ENVIRONMENT,
// а если оно не задано — "production" на Render (там задана RENDER) и
// "development" локально. Так события с прода не смешиваются с локальными
// даже при забытой переменной.
func sentryEnvironment() string {
	if environment := os.Getenv("SENTRY_ENVIRONMENT"); environment != "" {
		return environment
	}
	if os.Getenv("RENDER") != "" {
		return "production"
	}
	return "development"
}

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
		Environment:      sentryEnvironment(),
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

// runLoginRateLimitCleanup периодически удаляет из login_rate_limit записи,
// окно которых давно закончилось: сразу при старте и затем раз в interval.
// Устаревшие записи и без этого не влияют на лимит — очистка лишь не даёт
// таблице расти.
func runLoginRateLimitCleanup(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		deleted, err := database.DeleteStaleLoginFailures(24 * time.Hour)
		if err != nil {
			sentry.CaptureException(err)
			log.Printf("Ошибка очистки счётчиков неудачных входов: %v", err)
		} else if deleted > 0 {
			log.Printf("Удалено устаревших счётчиков неудачных входов: %d", deleted)
		}
		<-ticker.C
	}
}

// maxRetentionBatches ограничивает число порций одного вида за один проход
// очистки: остаток уйдёт на следующем проходе, а сбой, при котором порции не
// кончаются, не зациклит горутину.
const maxRetentionBatches = 100

// objectDeleter — часть хранилища файлов, нужная очистке.
type objectDeleter interface {
	DeleteObject(ctx context.Context, objectKey string) error
}

// runRetentionCleanup применяет сроки хранения данных (см. «Общие требования:
// Сроки хранения данных»): сразу при старте и затем раз в interval. Данные,
// у которых вышел срок, API и так не показывает — очистка лишь физически их
// удаляет, поэтому сбой одного шага не мешает остальным и проходу в целом.
func runRetentionCleanup(interval time.Duration, storage objectDeleter) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		runRetentionPass(storage)
		<-ticker.C
	}
}

func runRetentionPass(storage objectDeleter) {
	for i := 0; i < maxRetentionBatches; i++ {
		purged, err := database.PurgeSoftDeleted(database.SoftDeletedRetention)
		if err != nil {
			sentry.CaptureException(err)
			log.Printf("Ошибка удаления записей с истёкшим сроком хранения: %v", err)
			break
		}
		if purged == 0 {
			break
		}
		log.Printf("Физически удалено записей с истёкшим сроком хранения: %d", purged)
	}

	for i := 0; i < maxRetentionBatches; i++ {
		deleted, orphanKeys, err := database.SweepOrphanFiles(database.UnconfirmedFileRetention)
		if err != nil {
			sentry.CaptureException(err)
			log.Printf("Ошибка очистки файлов без владельца: %v", err)
			break
		}
		if deleted == 0 {
			break
		}
		log.Printf("Удалено строк файлов без владельца: %d", deleted)
		// Объект без строки file пользователю не виден, поэтому сбой удаления
		// объекта только логируется.
		for _, key := range orphanKeys {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := storage.DeleteObject(ctx, key); err != nil {
				log.Printf("Не удалось удалить объект %q из хранилища: %v", key, err)
			}
			cancel()
		}
	}

	if deleted, err := database.DeleteStaleRegistrationAttempts(database.RegistrationAttemptRetentionDays); err != nil {
		sentry.CaptureException(err)
		log.Printf("Ошибка очистки счётчиков регистраций: %v", err)
	} else if deleted > 0 {
		log.Printf("Удалено устаревших счётчиков регистраций: %d", deleted)
	}

	if deleted, err := database.DeleteStaleSuggestions(database.SuggestionRetentionMonths); err != nil {
		sentry.CaptureException(err)
		log.Printf("Ошибка удаления устаревших предложений: %v", err)
	} else if deleted > 0 {
		log.Printf("Удалено устаревших предложений: %d", deleted)
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
	fileStorage := s3client.New(s3Config)
	handlers.SetStorage(fileStorage)

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
	go runLoginRateLimitCleanup(time.Hour)
	go runRetentionCleanup(time.Hour, fileStorage)

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000" // локально
	}

	log.Printf("Запускаю на :%s", port)

	// Sentry — самый внешний слой: ловит паники всех слоёв ниже и ведёт трейс
	// запроса. Repanic оставляет панику net/http, который сам закроет соединение.
	sentryHandler := sentryhttp.New(sentryhttp.Options{Repanic: true})

	// Лимит тела стоит снаружи LoggingMiddleware: тот читает тело целиком.
	handler := sentryHandler.Handle(utils.CORSMiddleware(utils.BodyLimitMiddleware(utils.MaxRequestBodyBytes, utils.LoggingMiddleware(mux))))

	server := &http.Server{
		Addr:    ":" + port,
		Handler: handler,
		// Таймауты закрывают медленные соединения (slowloris) и зависшие
		// запросы. Файлы идут мимо сервиса по presigned-ссылкам, поэтому
		// долгих загрузок через него нет.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Render при деплое и остановке шлёт SIGTERM: даём текущим запросам
	// дозавершиться, а не обрываем их.
	stop, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		sentry.CaptureException(err)
		sentry.Flush(2 * time.Second)
		log.Fatalf("Ошибка: %v", err)
	case <-stop.Done():
		log.Printf("Получен сигнал остановки, завершаю запросы (до %s)", shutdownTimeout)
		ctx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancelShutdown()
		if err := server.Shutdown(ctx); err != nil {
			sentry.CaptureException(err)
			log.Printf("Ошибка корректной остановки: %v", err)
		}
		sentry.Flush(2 * time.Second)
	}
}
