package database

import (
	"embed"
	"errors"
	"log"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// RunMigrations применяет все непримененные миграции к базе данных.
// Вызывается при старте приложения, поэтому при мёрже в main
// и последующем деплое/рестарте сервиса схема БД обновляется автоматически.
func RunMigrations() error {
	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return err
	}

	driver, err := postgres.WithInstance(DB, &postgres.Config{})
	if err != nil {
		return err
	}

	m, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		return err
	}

	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			log.Println("Миграции: изменений нет, база уже актуальна")
			return nil
		}
		return err
	}

	log.Println("Миграции успешно применены")
	return nil
}
