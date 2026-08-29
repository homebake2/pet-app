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
	if _, err := database.DB.Exec(`TRUNCATE TABLE event, pet, profile, users, registration_rate_limit RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("не удалось очистить БД перед тестом: %v", err)
	}
}
