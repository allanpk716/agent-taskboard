# syntax=docker/dockerfile:1

ARG APP_VERSION=0.0.0

FROM node:22-alpine AS web
WORKDIR /src/web-src
COPY web-src/package.json web-src/package-lock.json ./
RUN npm ci
COPY web-src ./
RUN npm run build

FROM golang:1.26-alpine AS server
WORKDIR /src
ARG APP_VERSION
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
      -ldflags="-s -w -X github.com/allanpk716/agent-taskboard/internal/version.Server=${APP_VERSION}" \
      -o /out/taskboard ./cmd/taskboard

FROM golang:1.26-alpine AS client
WORKDIR /src
ARG APP_VERSION
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
      -ldflags="-s -w -H=windowsgui -X github.com/allanpk716/agent-taskboard/internal/version.Client=${APP_VERSION}" \
      -o /out/client-package/taskboard-client.exe ./cmd/taskboard-client \
    && go run ./cmd/release check-exe -goos windows -goarch amd64 \
      /out/client-package/taskboard-client.exe \
    && go run ./cmd/release manifest -component client -version "${APP_VERSION}" \
      -dir /out/client-package

FROM alpine:3.22
RUN apk add --no-cache curl \
    && addgroup -g 65532 taskboard \
    && adduser -D -u 65532 -G taskboard -H -h /app taskboard \
    && mkdir -p /app/data /app/client-package \
    && chown -R taskboard:taskboard /app
COPY --from=server --chown=taskboard:taskboard /out/taskboard /app/taskboard
COPY --from=web --chown=taskboard:taskboard /src/web /app/web
COPY --from=client --chown=taskboard:taskboard /out/client-package/ /app/client-package/
USER taskboard:taskboard
WORKDIR /app
EXPOSE 8080
VOLUME ["/app/data"]
ENTRYPOINT ["/app/taskboard"]
CMD ["serve", "-config", "/app/config/config.toml"]
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=6 \
  CMD curl -fsS http://127.0.0.1:${TASKBOARD_PORT:-8080}/api/v1/health >/dev/null
