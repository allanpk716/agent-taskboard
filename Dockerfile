# syntax=docker/dockerfile:1

ARG APP_VERSION=0.0.0
ARG BUILD_TAGS=""

FROM node:22-alpine AS web
WORKDIR /src/web-src
COPY web-src/package.json web-src/package-lock.json ./
RUN npm ci
COPY web-src ./
RUN npm run build

FROM golang:1.26-alpine AS server
WORKDIR /src
ARG APP_VERSION
ARG BUILD_TAGS
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN GO_TAGS=""; if [ -n "$BUILD_TAGS" ]; then GO_TAGS="-tags $BUILD_TAGS"; fi \
    && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $GO_TAGS -trimpath \
      -ldflags="-s -w -X github.com/allanpk716/agent-taskboard/internal/version.Server=${APP_VERSION}" \
      -o /out/taskboard ./cmd/taskboard

FROM alpine:3.22
ARG APP_VERSION
RUN apk add --no-cache curl \
    && addgroup -g 65532 taskboard \
    && adduser -D -u 65532 -G taskboard -H -h /app taskboard \
    && mkdir -p /app/data \
    && chown -R taskboard:taskboard /app
COPY --from=server --chown=taskboard:taskboard /out/taskboard /app/taskboard
COPY --from=web --chown=taskboard:taskboard /src/web /app/web
RUN printf '{"component":"web","version":"%s"}\n' "${APP_VERSION}" \
      > /app/web/version.json \
    && chown taskboard:taskboard /app/web/version.json
USER taskboard:taskboard
WORKDIR /app
ENV TASKBOARD_DEPLOY=container
EXPOSE 8080
VOLUME ["/app/data"]
ENTRYPOINT ["/app/taskboard"]
CMD ["serve", "-config", "/app/config/config.toml"]
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=6 \
  CMD /app/taskboard healthcheck -config /app/config/config.toml
