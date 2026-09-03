package database

import (
	"database/sql"
	"log"
	"os"

	_ "github.com/lib/pq"
)

var DB *sql.DB

// dbExecutor описывает общий поднабор методов *sql.DB и *sql.Tx. Функции,
// которым нужно работать как вне транзакции (обычные запросы), так и внутри
// неё (см. ImportLocalData), принимают dbExecutor вместо конкретного типа.
type dbExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func InitDB() {
	var err error
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		log.Fatal("DATABASE_URL не задан")
	}

	DB, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Ошибка подключения к базе: %v", err)
	}
	if err = DB.Ping(); err != nil {
		log.Fatalf("Не удалось подключиться к базе: %v", err)
	}

	if err := RunMigrations(); err != nil {
		log.Fatalf("Ошибка применения миграций: %v", err)
	}
}
