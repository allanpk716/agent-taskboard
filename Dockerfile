# syntax=docker/dockerfile:1

ARG APP_VERSION=0.0.0
ARG BUILD_TAGS=""
ARG CLIENT_VERSION

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

FROM alpine:3.22 AS client
WORKDIR /src
ARG CLIENT_VERSION
RUN apk add --no-cache curl \
    && test -n "${CLIENT_VERSION}" \
      || { echo "CLIENT_VERSION build-arg is required; set it to the latest client release version" >&2; exit 1; } \
    && mkdir -p /out \
    && base="https://github.com/allanpk716/agent-taskboard/releases/download/client/v${CLIENT_VERSION}" \
    && curl --fail --location --retry 3 --connect-timeout 10 --max-time 300 \
      -o /out/manifest.json "$base/manifest.json" \
    && curl --fail --location --retry 3 --connect-timeout 10 --max-time 300 \
      -o /out/taskboard-client.exe "$base/taskboard-client.exe" \
    && expected="$(awk '$0 ~ /"name": "taskboard-client.exe"/ { getline; sub(/^.*"sha256": "/, ""); sub(/".*$/, ""); print }' /out/manifest.json)" \
    && test -n "$expected" \
    && echo "${expected}  /out/taskboard-client.exe" | sha256sum -c - \
    && mkdir -p /out/client-package \
    && mv /out/manifest.json /out/taskboard-client.exe /out/client-package/

FROM alpine:3.22
ARG APP_VERSION
RUN apk add --no-cache curl \
    && addgroup -g 65532 taskboard \
    && adduser -D -u 65532 -G taskboard -H -h /app taskboard \
    && mkdir -p /app/data /app/client-package \
    && chown -R taskboard:taskboard /app
COPY --from=server --chown=taskboard:taskboard /out/taskboard /app/taskboard
COPY --from=web --chown=taskboard:taskboard /src/web /app/web
RUN printf '{"component":"web","version":"%s"}\n' "${APP_VERSION}" \
      > /app/web/version.json \
    && chown taskboard:taskboard /app/web/version.json
COPY --from=client --chown=taskboard:taskboard /out/client-package/ /app/client-package/
USER taskboard:taskboard
WORKDIR /app
ENV TASKBOARD_DEPLOY=container
EXPOSE 8080
VOLUME ["/app/data"]
ENTRYPOINT ["/app/taskboard"]
CMD ["serve", "-config", "/app/config/config.toml"]
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=6 \
  CMD /app/taskboard healthcheck -config /app/config/config.toml
