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
| `ADMIN_KEY` | административный ключ для `/admin/*` (заголовок `X-Admin-Key`) | необязателен; если не задан, admin-методы управления баннерами отключены и отвечают 404. Значение в логи не пишется |
| `JWT_SECRET` | секрет для подписи JWT | обязателен в проде; код читает его в `utils/jwt.go`, при отсутствии переменной локально используется дефолтное значение — на проде это небезопасно, задать реальный секрет |
| `S3_ENDPOINT` | endpoint S3-совместимого API Backblaze B2 | обязателен, без него `s3client.ConfigFromEnv()` возвращает ошибку и `main()` вызывает `log.Fatal` |
| `S3_KEY_ID` | Backblaze B2 access key (`keyID`) | обязателен; значение хранится только в Render, никогда не в репозитории и не передаётся клиенту |
| `S3_APPLICATION_KEY` | Backblaze B2 secret key (`applicationKey`) | обязателен; та же политика хранения, что и `S3_KEY_ID` |
| `S3_BUCKET` | имя bucket в Backblaze B2 | обязателен |
| `SENTRY_DSN` | DSN проекта `pet-health-backend` в Sentry (организация `self-b6r`) | необязателен; без него Sentry выключен. Ошибки, паники и трейсы уходят в Sentry только при заданном DSN |
| `SENTRY_ENVIRONMENT` | окружение события, например `production` или `staging` | необязателен; если не задан — `production` на Render (там задана переменная `RENDER`) и `development` локально, так что события с прода не смешиваются с локальными. Для staging-окружения на Render задать явно |
| `CLIENT_IP_HEADER` | заголовок с IP клиента для rate-limiting и логов | необязателен; на Render (задана `RENDER`) по умолчанию `CF-Connecting-IP` — сервис стоит за Cloudflare, который затирает присланное клиентом значение. Без заголовка (локально) берётся адрес соединения. `X-Forwarded-For` не используется: его элементы подделываются клиентом |
| `SENTRY_RELEASE` | версия релиза | необязателен; если не задан, берётся `RENDER_GIT_COMMIT`, который Render подставляет сам |
| `S3_REGION` | регион для AWS SDK | необязателен, по умолчанию `us-east-1` — Backblaze B2 не проверяет регион, но SDK требует непустое значение |

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
- `file` — generic-таблица файлов сущностей (presigned S3 upload/read), параметризована `owner_type`/`owner_id`; первое подключение — `owner_type = "pet_photo"` (см. `handlers/files.go`)

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
3. Проверить, что сервис поднялся: `curl https://pet-app-dk8u.onrender.com/healthz` — ответ `200 {"status":"ok"}` означает, что процесс принимает запросы и БД отвечает; `503 {"status":"unavailable"}` — БД недоступна (причина в логах, в ответ она не попадает)
4. Если менялась схема БД — ничего вручную делать не нужно: миграции из `database/migrations/` применяются автоматически при старте процесса (см. выше)

## Мониторинг доступности

`GET /healthz` — публичная проверка живости (без авторизации, вне OpenAPI-контракта мобильного клиента): `200`, если БД отвечает на `SELECT 1` в пределах 3 секунд, иначе `503`. Использовать:

- как **Health Check Path** сервиса в настройках Render (`/healthz`);
- как цель внешнего монитора доступности (например, UptimeRobot) с алертом на `503` и таймаут. На free tier регулярный пинг заодно не даёт сервису засыпать.

## Логи

`utils.LoggingMiddleware` пишет в stdout метод, путь, версию приложения (`app_version`, из заголовка `X-App-Version`; та же версия ставится тегом в события Sentry; в OpenAPI — необязательный параметр `AppVersion` на каждом пути), статус и тела запроса/ответа. Секретные поля тел (`password`, `*token*`, `device_id`, `code` кроме кода ошибки API, любые поля с суффиксом `url` — presigned-ссылки) маскируются значением `***`; тело, которое не разобралось как JSON, не логируется вовсе (только размер). Новое секретное поле в API нужно добавить в `sensitiveKeys` (`utils/log_mask.go`).

## Защита от перебора

Вход в существующий аккаунт ограничен по неудачным попыткам: 10 на логин и 30 на IP за 15 минут (таблица `login_rate_limit`, см. `handlers/login_rate_limit.go`), затем `429 RATE_LIMITED` с `Retry-After`. Регистрация новых аккаунтов — отдельный лимит, 3 в сутки на IP (`registration_rate_limit`). IP берётся из доверенного заголовка `CF-Connecting-IP` (см. `CLIENT_IP_HEADER`, `utils/client_ip.go`), а при его отсутствии или некорректном значении — из адреса соединения; `X-Forwarded-For` игнорируется, поэтому подменой заголовка лимит не обойти. IP клиента пишется в каждую строку лога (`ip=`): после деплоя стоит убедиться, что там реальные адреса клиентов, а не один и тот же адрес прокси — иначе лимиты по IP стали бы общими на всех.

## Таймауты, лимиты и остановка

`http.Server` в `main.go`: `ReadHeaderTimeout` 10 с, `ReadTimeout` 30 с, `WriteTimeout` 60 с, `IdleTimeout` 120 с. Тело запроса ограничено 1 МиБ (`utils.BodyLimitMiddleware`): при известной длине больше лимита — `413` с телом `{code: BAD_REQUEST, message}`, для chunked-тела чтение сверх лимита завершается ошибкой. Файлы идут по presigned-ссылкам мимо сервиса, поэтому лимит на них не влияет. По `SIGTERM` (деплой/остановка на Render) сервис перестаёт принимать новые соединения и до 25 с дожидается текущих запросов (`server.Shutdown`), затем сбрасывает Sentry.

## Известные ограничения / TODO

- `JWT_SECRET` при отсутствии переменной подменяется публичным дефолтом из `utils/jwt.go` — на проде переменная обязана быть задана.
