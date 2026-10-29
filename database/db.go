package database

import (
	"database/sql"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

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

// Executor — экспортируемое имя dbExecutor для кода вне пакета, который
// выполняет составные операции в транзакции (см. RunInTx).
type Executor = dbExecutor

func InitDB() {
	var err error
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		log.Fatal("DATABASE_URL не задан")
	}

	connStr, err = withBinaryParameters(connStr)
	if err != nil {
		log.Fatalf("Некорректный DATABASE_URL: %v", err)
	}

	DB, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("Ошибка подключения к базе: %v", err)
	}
	configurePool(DB)
	if err = DB.Ping(); err != nil {
		log.Fatalf("Не удалось подключиться к базе: %v", err)
	}

	if err := RunMigrations(); err != nil {
		log.Fatalf("Ошибка применения миграций: %v", err)
	}
}

// withBinaryParameters включает в строке подключения режим lib/pq
// binary_parameters=yes, если он не задан явно. Без него запрос с параметрами
// стоит двух обращений к серверу (отдельный prepare, затем выполнение), с ним —
// одного; задержка сети до базы — основная часть времени ответа API. В этом
// режиме параметры []byte передаются как bytea в бинарном формате, поэтому
// значения jsonb передаются в запросы строкой (см. InsertEventWith).
func withBinaryParameters(connStr string) (string, error) {
	if strings.Contains(connStr, "binary_parameters") {
		return connStr, nil
	}
	if !strings.HasPrefix(connStr, "postgres://") && !strings.HasPrefix(connStr, "postgresql://") {
		// Строка вида "key=value key=value".
		return connStr + " binary_parameters=yes", nil
	}
	u, err := url.Parse(connStr)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("binary_parameters", "yes")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// configurePool задаёт параметры пула соединений. По умолчанию database/sql
// держит открытыми только два простаивающих соединения: параллельные запросы
// клиента (на старте приложения их несколько) открывают новые соединения с
// TLS и аутентификацией, а после ответа лишние закрываются. Простаивающие
// соединения закрываются раньше, чем serverless-база (Neon) останавливает
// вычислитель и обрывает их сама.
func configurePool(db *sql.DB) {
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxIdleTime(4 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)
}
