//go:build integration

// Package integration гоняет HTTP-запросы через реальный net/http.ServeMux
// (тот же handlers.NewMux(), что и main.go) поверх настоящей Postgres и
// сверяет каждый запрос/ответ с open-api/spec.json через kin-openapi.
//
// Запуск: поднять локальную БД (docker compose up -d в корне backend/) и
//
//	go test -tags integration ./integration/...
//
// DATABASE_URL по умолчанию указывает на локальный docker-compose (см.
// CLAUDE.md); тесты полностью очищают её таблицы перед каждым тестом —
// никогда не указывайте сюда прод/staging базу.
package integration

import (
	"myauthservice/database"
	"myauthservice/handlers"
	"myauthservice/s3client"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

var (
	server *httptest.Server
	spec   *openapi3.T
)

func TestMain(m *testing.M) {
	if os.Getenv("DATABASE_URL") == "" {
		os.Setenv("DATABASE_URL", "postgres://postgres@127.0.0.1:5555/pets?sslmode=disable")
	}
	if os.Getenv("JWT_SECRET") == "" {
		os.Setenv("JWT_SECRET", "test-secret")
	}

	database.InitDB()

	// S3-хранилище для generic-механизма файлов сущностей: presign — чисто
	// локальная операция (не требует сети), поэтому дефолты годятся и без
	// реального Backblaze B2 endpoint'а. S3_ENDPOINT намеренно указывает на
	// заведомо недоступный локальный порт — best-effort DeleteObject (см.
	// «Удаление файла») быстро завершится с ошибкой соединения вместо
	// зависания, не влияя на проверяемый контракт.
	if os.Getenv("S3_ENDPOINT") == "" {
		os.Setenv("S3_ENDPOINT", "http://127.0.0.1:1")
	}
	if os.Getenv("S3_KEY_ID") == "" {
		os.Setenv("S3_KEY_ID", "test-key-id")
	}
	if os.Getenv("S3_APPLICATION_KEY") == "" {
		os.Setenv("S3_APPLICATION_KEY", "test-application-key")
	}
	if os.Getenv("S3_BUCKET") == "" {
		os.Setenv("S3_BUCKET", "test-bucket")
	}
	s3Config, err := s3client.ConfigFromEnv()
	if err != nil {
		panic("не удалось сконфигурировать S3-хранилище для тестов: " + err.Error())
	}
	handlers.SetStorage(s3client.New(s3Config))

	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile("../open-api/spec.json")
	if err != nil {
		panic("не удалось загрузить open-api/spec.json: " + err.Error())
	}
	// doc.Validate() специально не вызывается: спека экспортирована из
	// Stoplight и содержит поля вроде info.summary, которые strict-валидатор
	// kin-openapi не признаёт, хотя для маршрутизации и валидации
	// запросов/ответов это не мешает.
	spec = doc

	server = httptest.NewServer(handlers.NewMux())

	code := m.Run()
	server.Close()
	os.Exit(code)
}

// resetDB очищает все таблицы перед тестом, обеспечивая изоляцию между тестами.
func resetDB(t *testing.T) {
	t.Helper()
	if _, err := database.DB.Exec(`TRUNCATE TABLE file, event, pet, profile, users, registration_rate_limit, pet_idempotency_key, import_local_data_idempotency_key RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("не удалось очистить БД перед тестом: %v", err)
	}
}
