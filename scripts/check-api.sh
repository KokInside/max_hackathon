#!/usr/bin/env sh
# Прогон обязательных проверок из DATA-API.yaml против развёрнутого API.
# Проверки читаются из самого DATA-API.yaml (backend/cmd/checkapi): код ответа, Content-Type, обязательные поля.
# Использование: scripts/check-api.sh https://<домен>/api/v1 <CHAIRMAN_TOKEN> <RESIDENT_TOKEN>
# Локально: scripts/check-api.sh http://localhost:8080/api/v1 <токены из TEST_ACCOUNTS>
# Нужен Go (версия из backend/go.mod). Проверки можно повторять: сервер держит у тестового председателя рабочий акт.
set -eu
[ $# -eq 3 ] || { echo "использование: $0 <BASE_URL> <CHAIRMAN_TOKEN> <RESIDENT_TOKEN>" >&2; exit 2; }
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root/backend"
CHAIRMAN_TOKEN=$2 RESIDENT_TOKEN=$3 exec go run ./cmd/checkapi -spec "$root/DATA-API.yaml" -base "$1"
