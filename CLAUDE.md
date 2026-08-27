# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project overview

`myauthservice` — a Go backend (`net/http`, no framework) for a pet-health tracking app: users, profiles, pets, and pet events (vet visits, weight, etc.). Frontend project `petHealth` lives at `/Users/oka/petHealth` and consumes the OpenAPI spec this repo produces.

## Commands

```bash
# Local Postgres (listens on 127.0.0.1:5555, db `pets`, user `postgres`, no password)
docker compose up -d

# Run the server locally
export DATABASE_URL="postgres://postgres@127.0.0.1:5555/pets?sslmode=disable"
go run .                         # port 3000 by default, override with PORT

# Tests
go test ./...
go test ./handlers/ -run TestName -v   # single test

# Build (matches Render's build command)
go build -tags netgo -ldflags '-s -w' -o app

# Regenerate OpenAPI types after editing open-api/spec.json
go generate ./...                # runs oapi-codegen per openapi/generate.go -> openapi/types.gen.go

# Migrations
go run github.com/golang-migrate/migrate/v4/cmd/migrate create -ext sql -dir database/migrations -seq <name>
go run -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate -database "$DATABASE_URL" -path database/migrations up
go run -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate -database "$DATABASE_URL" -path database/migrations down 1
```

Migrations are embedded into the binary (`database/migrate.go`, `//go:embed migrations/*.sql`) and applied automatically on startup via `database.InitDB()` — no manual step needed after deploy. `database/schema.sql` is a historical snapshot only; the real schema lives in `database/migrations/`.

## Architecture

- **Entry point**: `main.go` wires `net/http.ServeMux` routes directly to handler functions and wraps everything in `utils.CORSMiddleware`. No router library, no per-route middleware chains — auth is instead checked per-handler via `requireUserID`.
- **Layering**: `handlers/` (HTTP request/response, validation) → `database/db.go` (all SQL lives here as free functions, no repository interfaces/structs) → Postgres. `models/` holds plain structs for DB rows and API-shaped request/response bodies (distinct from the generated OpenAPI types).
- **OpenAPI types**: `open-api/spec.json` is the source of truth for the API contract. `openapi/types.gen.go` is generated from it (`go generate ./...`) and provides response/error-code types (e.g. `openapi.GetErrorResponse`, `openapi.ErrorCodeEnum`) used by handlers via `handlers/response.go`. When changing request/response shapes, update `open-api/spec.json` first, then regenerate.
- **Auth**: JWT-based (`utils/jwt.go`), secret from `JWT_SECRET` env var (insecure default for local dev only). `handlers/response.go:requireUserID` is the shared auth guard every protected handler calls first — it validates the Bearer token, then checks `tokens_invalidated_at` in the `users` table so that logout/token-revocation works even for already-issued access tokens (not just refresh tokens).
- **Soft delete**: `pet` rows use `deleted_at` instead of hard deletes; all pet queries must filter `deleted_at IS NULL` (see `database.GetPetByIDAndProfileID` vs `GetPetIdDBByIDAndProfileID`, which intentionally omits the filter for update/ownership checks).
- **Ownership checks**: resources are scoped by `profile_id` (derived from the authenticated user's `user_id` via `GetProfileIDByUserID`), not directly by `user_id`. Cross-user access must be checked at the handler level (e.g. `CheckPetBelongsToProfile`) before mutating/returning nested resources like events.
- **Testing**: handlers are tested with `github.com/DATA-DOG/go-sqlmock` swapped in for `database.DB` — see `handlers/testutil_test.go` for the shared harness (`setupMockDB`, token helpers, `expectTokensValid`). No real DB needed for `go test`.
- **Error responses**: always `{ code, message }` JSON via `writeError`/`writeJSON` in `handlers/response.go`, matching `components.schemas.GetErrorResponse` in the spec.

## Deployment

Full details in `docs/deploy.md`. Summary: Render (Go web service, auto-deploy on push to `main`) + Neon (serverless Postgres). `DATABASE_URL` and `JWT_SECRET` are set in Render's environment, never in the repo.

## Known limitations (tracked in docs/deploy.md)

- `LoginHandler` does not verify passwords.
- Passwords are stored in plaintext in `users.password`.

## Other notes

- `testevents/` is a scratch/experiment `main.go`, unrelated to the service — not part of the build.
- A Claude skill (`.claude/skills/perenesi-speku-na-front`) converts `open-api/spec.json` to YAML and copies it into the `petHealth` frontend repo; only invoke it when asked to "перенеси спеку на фронт".
