#!/usr/bin/env sh
# Обязательные проверки перед сдачей и перед каждым коммитом в main.
# Запуск из корня репозитория: scripts/check.sh
# Интеграционные тесты (app, api, bot) идут на PostgreSQL из TEST_DATABASE_URL, без неё пропускаются:
#   docker run -d --name priemka-testdb -e POSTGRES_USER=priemka -e POSTGRES_PASSWORD=test -e POSTGRES_DB=priemka -p 127.0.0.1:55432:5432 postgres:17-alpine
#   TEST_DATABASE_URL=postgres://priemka:test@127.0.0.1:55432/priemka?sslmode=disable scripts/check.sh
set -eu

STATICCHECK=honnef.co/go/tools/cmd/staticcheck@v0.8.1
GOVULNCHECK=golang.org/x/vuln/cmd/govulncheck@v1.8.0

step() { printf '\n== %s\n' "$*"; }

cd backend
step "go vet";       go vet ./...
step "staticcheck";  go run "$STATICCHECK" ./...
step "go test -race"
[ -n "${TEST_DATABASE_URL:-}" ] || echo "TEST_DATABASE_URL не задана: интеграционные тесты будут пропущены"
go test -race -count=1 ./...
step "govulncheck";  go run "$GOVULNCHECK" ./...
cd ../webapp
step "npm ci";       npm ci --no-audit --no-fund --silent
step "tsc";          npm run --silent typecheck
step "npm audit";    npm audit --audit-level=low
cd ..
step "docker compose config"; docker compose config -q
printf '\nВсе проверки пройдены.\n'
