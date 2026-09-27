#!/usr/bin/env sh
# Резервная копия: дамп БД и архив файлов (фото, акты, PDF) из работающего compose-стека.
# Запуск из корня репозитория: scripts/backup.sh [каталог]  (по умолчанию ./backups)
# Ежедневно через cron: 30 3 * * * cd <путь к priemka> && mkdir -p backups && scripts/backup.sh >> backups/backup.log 2>&1
# Хранятся копии за KEEP_DAYS дней (по умолчанию 14).
# Восстановление:
#   docker compose exec -T db pg_restore -U priemka -d priemka --clean --if-exists < db-<дата>.dump
#   docker compose exec -T app tar -C /data -xzf - < files-<дата>.tar.gz
set -eu

dir=${1:-backups}
keep=${KEEP_DAYS:-14}
stamp=$(date +%Y%m%d-%H%M%S)
mkdir -p "$dir"

docker compose exec -T db pg_dump -U priemka -Fc priemka > "$dir/db-$stamp.dump.tmp"
mv "$dir/db-$stamp.dump.tmp" "$dir/db-$stamp.dump"

docker compose exec -T app tar -C /data -czf - files > "$dir/files-$stamp.tar.gz.tmp"
mv "$dir/files-$stamp.tar.gz.tmp" "$dir/files-$stamp.tar.gz"

find "$dir" -maxdepth 1 \( -name 'db-*.dump' -o -name 'files-*.tar.gz' \) -mtime +"$keep" -delete
echo "$(date -Iseconds) backup ok: $dir/db-$stamp.dump, $dir/files-$stamp.tar.gz"
