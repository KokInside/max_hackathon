# Сборка из корня репозитория: docker build -f backend/Dockerfile .
FROM golang:1.26-alpine AS build

WORKDIR /src

COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY backend/ ./

RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/app

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -H -u 10001 app \
 && mkdir -p /data/files && chown app:app /data/files

# Корневой сертификат Минцифры: им подписан сертификат platform-api2.max.ru.
COPY certs/rootca_ssl_rsa2022.crt /usr/local/share/ca-certificates/russian_trusted_root_ca.crt
RUN update-ca-certificates

WORKDIR /app

COPY --from=build /out/app /app/app
COPY config /app/config
COPY assets/fonts /app/assets/fonts

ENV CONFIG_DIR=/app/config FONTS_DIR=/app/assets/fonts FILES_DIR=/data/files HTTP_ADDR=:8080

USER app

EXPOSE 8080

HEALTHCHECK --interval=15s --timeout=3s --start-period=20s CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1

ENTRYPOINT ["/app/app"]
