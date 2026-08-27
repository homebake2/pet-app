package database

import (
	"database/sql"
	"log"
	"os"

	_ "github.com/lib/pq"
)

var DB *sql.DB

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
