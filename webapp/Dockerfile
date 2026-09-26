# Сборка из корня репозитория: docker build -f webapp/Dockerfile .
FROM node:24-alpine AS build
WORKDIR /app
COPY webapp/package.json webapp/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY webapp/ ./
RUN npm run build

FROM caddy:2-alpine
COPY webapp/Caddyfile /etc/caddy/Caddyfile
COPY --from=build /app/dist /srv
