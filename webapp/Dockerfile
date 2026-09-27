# Сборка из корня репозитория: docker build -f webapp/Dockerfile .
FROM node:24.21.0-alpine AS build
WORKDIR /app
COPY webapp/package.json webapp/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY webapp/ ./
RUN npm run build

FROM caddy:2.11-alpine
COPY webapp/Caddyfile /etc/caddy/Caddyfile
COPY --from=build /app/dist /srv

# Жив ли Caddy и загружен ли конфиг: admin API слушает только localhost внутри контейнера.
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s CMD wget -qO /dev/null http://127.0.0.1:2019/config/ || exit 1
