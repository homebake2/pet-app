# Деплой и инфраструктура

## Обзор

- **Бэкенд**: Go (`net/http`, без фреймворка), модуль `myauthservice`
- **Хостинг бэкенда**: [Render](https://render.com) — Web Service, free tier
- **База данных**: PostgreSQL на [Neon](https://neon.tech) — serverless Postgres, free tier
- **Репозиторий**: https://github.com/homebake2/pet-app
- **Продакшн URL**: https://pet-app-dk8u.onrender.com

## Render (бэкенд)

### Настройки сервиса

| Параметр | Значение |
|---|---|
| Runtime | Go |
| Build Command | `go build -tags netgo -ldflags '-s -w' -o app` |
| Start Command | `./app` |
| Instance Type | Free |
| Auto-deploy | включён — деплой запускается автоматически на каждый push в ветку `main` |

Флаги сборки:
- `-tags netgo` — форсирует чистый Go DNS-резолвер вместо cgo (не нужен libc в контейнере)
- `-ldflags '-s -w'` — обрезает debug-информацию, бинарник меньше

### Переменные окружения

Заданы в Render → Environment:

| Переменная | Значение | Комментарий |
|---|---|---|
| `DATABASE_URL` | connection string от Neon (см. ниже) | обязателен, без него `database.InitDB()` вызывает `log.Fatal` |
| `PORT` | не задаётся вручную | Render подставляет сам; код читает `os.Getenv("PORT")`, локально фолбэк на `3000` (`main.go:29-32`) |
| `JWT_SECRET` | секрет для подписи JWT | обязателен в проде; код читает его в `utils/jwt.go`, при отсутствии переменной локально используется дефолтное значение — на проде это небезопасно, задать реальный секрет |

### Особенности free tier

- Сервис **засыпает после ~15 минут простоя**. Первый запрос после сна ждёт ~30-60 секунд, пока инстанс поднимается — на клиенте (мобильное приложение) на это стоит показывать лоадер/делать retry с таймаутом.
- Ограниченные CPU/RAM ресурсы — для текущей нагрузки достаточно.

## Neon (база данных)

- Проект создан без Neon Auth (не нужен — своя JWT-аутентификация в `utils/jwt.go` + `handlers/auth.go`)
- Регион: `eu-west-2` (AWS)
- Connection string хранится **только** в Render → Environment Variables (`DATABASE_URL`), не в репозитории и не в этом файле — актуальное значение смотреть в дашборде Neon или Render.
- Формат: `postgresql://<user>:<password>@<host>/<db>?sslmode=require`

### Схема БД и миграции

Схема версионируется миграциями в [`database/migrations/`](../database/migrations/) (формат [golang-migrate](https://github.com/golang-migrate/migrate), файлы `NNNNNN_name.up.sql` / `.down.sql`). Таблицы:

- `users` — логин/пароль/refresh_token
- `profile` — профиль пользователя (1:1 с `users` через `user_id`)
- `pet` — питомцы (many:1 с `profile` через `profile_id`), soft delete через `deleted_at`
- `event` — события по питомцу (many:1 с `pet` через `pet_id`)

Все первичные ключи — `uuid`, генерируются через `gen_random_uuid()` (встроено в Postgres с версии 13, дополнительных расширений не требует).

Миграции встроены в бинарник через `//go:embed` (`database/migrate.go`) и применяются **автоматически** при старте приложения — `database.InitDB()` вызывает `RunMigrations()` сразу после подключения к базе, до того как сервер начинает принимать запросы. Поэтому на каждый деплой на Render (см. ниже — автодеплой на push в `main`) процесс перезапускается и сам подтягивает ещё не применённые миграции к базе на Neon. Ручного шага "применить SQL к продовой базе" больше не требуется.

Добавить новую миграцию:

```bash
# имя без номера — порядковый номер выбирается автоматически
go run github.com/golang-migrate/migrate/v4/cmd/migrate create -ext sql -dir database/migrations -seq <название_миграции>
```

Применить/накатить миграции локально вручную (например, чтобы проверить `down`):

```bash
export DATABASE_URL="postgres://postgres@127.0.0.1:5555/pets?sslmode=disable"
go run -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate -database "$DATABASE_URL" -path database/migrations up
go run -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate -database "$DATABASE_URL" -path database/migrations down 1
```

(флаг `-tags postgres` обязателен — без него в CLI не зарегистрирован драйвер postgres и команда падает с `unknown driver postgres`)

`database/schema.sql` больше не используется приложением — оставлен только как исторический снимок первой схемы, актуальную схему смотреть в `database/migrations/`.

## Локальная разработка

Postgres поднимается через `docker-compose.yml`:

```bash
docker compose up -d
```

Слушает `127.0.0.1:5555` (проброшено с внутреннего порта `5433` в контейнере), база `pets`, юзер `postgres`, `POSTGRES_HOST_AUTH_METHOD: trust` (без пароля — только для локальной разработки).

Локальный запуск сервера:

```bash
export DATABASE_URL="postgres://postgres@127.0.0.1:5555/pets?sslmode=disable"
go run .
```

Порт по умолчанию — `3000` (переопределяется через `PORT`).

## Процесс деплоя

1. Внести изменения, закоммитить, `git push origin main`
2. Render автоматически подхватывает push и передеплоивает (см. вкладку Deploys в дашборде Render)
3. Проверить, что сервис поднялся: `curl https://pet-app-dk8u.onrender.com/`
4. Если менялась схема БД — ничего вручную делать не нужно: миграции из `database/migrations/` применяются автоматически при старте процесса (см. выше)

## Известные ограничения / TODO

- `LoginHandler` (`handlers/auth.go`) не проверяет пароль — любой ввод для существующего/несуществующего логина сейчас возвращает валидные токены. Требует фикса перед реальным использованием аутентификации.
- Пароли пользователей хранятся в открытом виде в `users.password` — нужно хэширование (например, bcrypt) перед продакшн-использованием.
