# Деплой и инфраструктура

## Обзор

- **Бэкенд**: Go (`net/http`, без фреймворка), модуль `myauthservice`
- **Хостинг**: одна виртуальная машина в [Yandex Cloud](https://yandex.cloud) (Ubuntu), на ней Docker Compose: PostgreSQL 18, бэкенд и Caddy (HTTPS)
- **Файлы**: Yandex Object Storage (S3-совместимое, presigned-ссылки)
- **Репозиторий**: https://github.com/homebake2/pet-app
- **Продакшн URL**: https://api.pethealth-app.ru (домен в reg.ru, A-запись `api` на публичный IP ВМ)

Прежняя схема (Render, Neon, Backblaze B2) отключена; данные перенесены на ВМ.

## Состав стека

Описан в [`deploy/docker-compose.prod.yml`](../deploy/docker-compose.prod.yml):

| Сервис | Образ | Назначение |
|---|---|---|
| `postgres` | `postgres:18-alpine` | база; данные в именованном томе `pgdata`, порт наружу не публикуется |
| `backend` | собирается по [`deploy/Dockerfile`](../deploy/Dockerfile) | Go-сервис на порту 3000 внутри сети compose |
| `caddy` | `caddy:2-alpine` | единственный сервис с открытыми портами 80 и 443; сам выпускает и продлевает сертификат Let's Encrypt, проксирует на `backend:3000` ([`deploy/Caddyfile`](../deploy/Caddyfile)) |

На ВМ всё лежит в каталоге `/opt/pet`: `docker-compose.prod.yml`, `Caddyfile` и `.env` (создаётся деплоем, права 600).

## Деплой (GitHub Actions)

- [`test.yml`](../.github/workflows/test.yml) запускается автоматически на каждый push и pull request: `go vet ./...` и `go test ./...`.
- [`deploy.yml`](../.github/workflows/deploy.yml) запускается **только вручную**: Actions → deploy → Run workflow (выбрать ветку, по умолчанию `main`). Он не ждёт результата `test`, поэтому перед запуском проверьте, что у коммита зелёные тесты.

Шаги `deploy.yml`:

1. Собирает Docker-образ с тегами `pet-backend:latest` и `pet-backend:<sha коммита>`.
2. Формирует `.env` из GitHub Secrets и Variables с теми же именами, что в [`deploy/.env.example`](../deploy/.env.example); значения передаются через переменные окружения шага, а не подставляются в текст скрипта.
3. По SSH копирует на ВМ `docker-compose.prod.yml`, `Caddyfile` и `.env`.
4. Передаёт образ на ВМ через `docker save | gzip | ssh ... docker load` (реестр образов не используется).
5. Выполняет `docker compose up -d --no-build --remove-orphans` и чистит старые образы.
6. Проверяет снаружи `https://$DOMAIN/healthz` (до 24 попыток через 5 секунд), при неудаче деплой помечается красным.

Откат: образ предыдущего коммита остаётся на ВМ под тегом с хэшем, достаточно перетегировать его в `latest` и перезапустить compose. Автоматического отката нет.

### GitHub Secrets и Variables

Variables: `DOMAIN`, `POSTGRES_USER`, `POSTGRES_DB`, `S3_ENDPOINT`, `S3_REGION`, `S3_BUCKET`, необязательная `SENTRY_ENVIRONMENT` (по умолчанию `production`).

Secrets: `DEPLOY_HOST` (IP ВМ), `DEPLOY_USER`, `DEPLOY_SSH_KEY` (приватный ключ, публичный лежит в `authorized_keys` на ВМ), `DEPLOY_KNOWN_HOSTS` (вывод `ssh-keyscan <IP>`; привязан к IP, при смене адреса обновить вместе с `DEPLOY_HOST`), `POSTGRES_PASSWORD` (без символа `$`), `JWT_SECRET`, `S3_KEY_ID`, `S3_APPLICATION_KEY`, необязательные `ADMIN_KEY`, `WEB_API_KEY`, `SENTRY_DSN`.

### Переменные окружения бэкенда

| Переменная | Комментарий |
|---|---|
| `DATABASE_URL` | в compose собирается из `POSTGRES_*` и указывает на сервис `postgres`; обязателен, без него `database.InitDB()` вызывает `log.Fatal` |
| `PORT` | задан в compose (3000); без него фолбэк на `3000` (`main.go`) |
| `JWT_SECRET` | секрет подписи JWT (`utils/jwt.go`); обязателен в проде, при смене все выданные токены перестают действовать |
| `ADMIN_KEY` | ключ для `/admin/*` (заголовок `X-Admin-Key`); необязателен, пустое значение отключает admin-методы (404). В логи не пишется |
| `WEB_API_KEY` | ключ сайта pet-web для `/web/*` (заголовок `X-Api-Key`); то же значение задаётся в серверном окружении сайта на Vercel; необязателен, пустое значение отключает методы сайта (404) |
| `S3_ENDPOINT`, `S3_REGION`, `S3_BUCKET`, `S3_KEY_ID`, `S3_APPLICATION_KEY` | Yandex Object Storage: `https://storage.yandexcloud.net`, `ru-central1`, бакет и статический ключ сервисного аккаунта с ролью `storage.editor`; обязательны (`s3client.ConfigFromEnv()`) |
| `SENTRY_DSN` | необязателен; без него Sentry выключен |
| `SENTRY_ENVIRONMENT` | деплой подставляет `production`; без переменной вне Render было бы `development` |
| `SENTRY_RELEASE` | деплой подставляет хэш коммита |
| `CLIENT_IP_HEADER` | в compose `X-Real-IP`: Caddy каждый раз перезаписывает этот заголовок адресом соединения (`header_up X-Real-IP {remote_host}`), поэтому подделать его клиент не может. `X-Forwarded-For` не используется |

## База данных на проде

- PostgreSQL 18 в контейнере на ВМ, данные в томе `pgdata`, наружу порт не открыт.
- Резервных копий по умолчанию нет: нужно настроить снапшоты диска ВМ и `pg_dump` (см. ниже «Известные ограничения»).
- Точечные правки данных на проде (конфигурация, баннеры, согласия) делаются через `psql` в контейнере по SSH: `docker compose -f docker-compose.prod.yml exec -T postgres psql ...` из `/opt/pet`. Схему так не менять, только данные: схема живёт в миграциях.
- Данные из прежней базы (Neon) перенесены один раз: `pg_dump --data-only --disable-triggers` клиентом той же версии, что у источника, в уже мигрированную схему.

### Схема БД и миграции

Схема версионируется миграциями в [`database/migrations/`](../database/migrations/) (формат [golang-migrate](https://github.com/golang-migrate/migrate), файлы `NNNNNN_name.up.sql` / `.down.sql`). Таблицы:

- `users` — логин/пароль/refresh_token
- `profile` — профиль пользователя (1:1 с `users` через `user_id`)
- `pet` — питомцы (many:1 с `profile` через `profile_id`), soft delete через `deleted_at`
- `event` — события по питомцу (many:1 с `pet` через `pet_id`)
- `file` — generic-таблица файлов сущностей (presigned S3 upload/read), параметризована `owner_type`/`owner_id`; первое подключение — `owner_type = "pet_photo"` (см. `handlers/files.go`)

Все первичные ключи — `uuid`, генерируются через `gen_random_uuid()` (встроено в Postgres с версии 13, дополнительных расширений не требует).

Миграции встроены в бинарник через `//go:embed` (`database/migrate.go`) и применяются **автоматически** при старте приложения — `database.InitDB()` вызывает `RunMigrations()` сразу после подключения к базе, до того как сервер начинает принимать запросы. Поэтому на каждый деплой (см. ниже — ручной запуск workflow `deploy`) контейнер бэкенда перезапускается и сам подтягивает ещё не применённые миграции к базе на ВМ. Ручного шага "применить SQL к продовой базе" больше не требуется.

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

1. Внести изменения, закоммитить, `git push origin main`; `test.yml` прогонит тесты автоматически
2. Убедиться, что `test` зелёный, затем запустить деплой вручную: Actions → deploy → Run workflow (подробности в разделе «Деплой (GitHub Actions)»)
3. Шаг `Check /healthz` в самом workflow проверит сервис; руками: `curl https://api.pethealth-app.ru/healthz` — ответ `200 {"status":"ok"}` означает, что процесс принимает запросы и БД отвечает; `503 {"status":"unavailable"}` — БД недоступна (причина в логах, в ответ она не попадает)
4. Если менялась схема БД — ничего вручную делать не нужно: миграции из `database/migrations/` применяются автоматически при старте процесса (см. выше)

## Мониторинг доступности

`GET /healthz` — публичная проверка живости (без авторизации, вне OpenAPI-контракта мобильного клиента): `200`, если БД отвечает на `SELECT 1` в пределах 3 секунд, иначе `503`. Использовать:

- как проверку после деплоя (шаг `Check /healthz` в `deploy.yml`);
- как цель внешнего монитора доступности (например, UptimeRobot) с алертом на `503` и таймаут: `https://api.pethealth-app.ru/healthz`.

## Логи

`utils.LoggingMiddleware` пишет в stdout метод, путь, версию приложения (`app_version`, из заголовка `X-App-Version`; та же версия ставится тегом в события Sentry; в OpenAPI — необязательный параметр `AppVersion` на каждом пути), статус и тела запроса/ответа. Секретные поля тел (`password`, `*token*`, `device_id`, `code` кроме кода ошибки API, любые поля с суффиксом `url` — presigned-ссылки) маскируются значением `***`; тело, которое не разобралось как JSON, не логируется вовсе (только размер). Новое секретное поле в API нужно добавить в `sensitiveKeys` (`utils/log_mask.go`).

## Защита от перебора

Вход в существующий аккаунт ограничен по неудачным попыткам: 10 на логин и 30 на IP за 15 минут (таблица `login_rate_limit`, см. `handlers/login_rate_limit.go`), затем `429 RATE_LIMITED` с `Retry-After`. Регистрация новых аккаунтов — отдельный лимит, 3 в сутки на IP (`registration_rate_limit`). IP берётся из доверенного заголовка, имя которого задаёт `CLIENT_IP_HEADER` (на ВМ это `X-Real-IP`, его выставляет Caddy; `utils/client_ip.go`), а при его отсутствии или некорректном значении — из адреса соединения; `X-Forwarded-For` игнорируется, поэтому подменой заголовка лимит не обойти. IP клиента пишется в каждую строку лога (`ip=`): после деплоя стоит убедиться, что там реальные адреса клиентов, а не один и тот же адрес прокси — иначе лимиты по IP стали бы общими на всех.

## Таймауты, лимиты и остановка

`http.Server` в `main.go`: `ReadHeaderTimeout` 10 с, `ReadTimeout` 30 с, `WriteTimeout` 60 с, `IdleTimeout` 120 с. Тело запроса ограничено 1 МиБ (`utils.BodyLimitMiddleware`): при известной длине больше лимита — `413` с телом `{code: BAD_REQUEST, message}`, для chunked-тела чтение сверх лимита завершается ошибкой. Файлы идут по presigned-ссылкам мимо сервиса, поэтому лимит на них не влияет. По `SIGTERM` (деплой/остановка контейнера) сервис перестаёт принимать новые соединения и до 25 с дожидается текущих запросов (`server.Shutdown`), затем сбрасывает Sentry.

## Известные ограничения / TODO

- `JWT_SECRET` при отсутствии переменной подменяется публичным дефолтом из `utils/jwt.go` — на проде переменная обязана быть задана.
- Резервные копии базы не настроены: данные лежат в одном томе на одной ВМ. Нужны снапшоты диска по расписанию и регулярный `pg_dump` в отдельный бакет.
- Группа безопасности ВМ пока разрешает весь входящий трафик; нужно оставить только порты 22, 80 и 443.
- Маскирование в логах не покрывает персональные поля (e-mail, телефон, ФИО): они попадают в тела ответов, которые пишет `LoggingMiddleware`. Логи контейнеров ротируются (3 файла по 10 МБ), но для 152-ФЗ стоит решить, нужны ли тела ответов в логах.
