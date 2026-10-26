#!/usr/bin/env bash
set -euo pipefail
export MSYS_NO_PATHCONV=1

if [ "$#" -ne 2 ]; then
    echo "usage: $0 <image> <expected-version>" >&2
    exit 2
fi

image=$1
expected_version=$2
port=${SMOKE_PORT:-8080}
container=taskboard-image-smoke-$$

cid=$(docker run -d --name "$container" --network host --entrypoint /bin/sh \
    --mount type=tmpfs,destination=/app/data,tmpfs-mode=1777 \
    "$image" -c "printf '%s\n' \
        'data_dir = \"/app/data\"' 'port = $port' 'web_dir = \"/app/web\"' \
        '' '[server]' 'public_url = \"http://127.0.0.1:$port\"' 'deploy = \"container\"' \
        'client_image_dir = \"/app/client-package\"' '' '[backup]' \
        'target = \"/app/data/backups\"' '' '[update]' 'sources = []' \
        > /tmp/config.toml && exec /app/taskboard serve -config /tmp/config.toml")
trap 'docker rm -f "$container" >/dev/null 2>&1 || true' EXIT

request() {
    local path=$1 marker=.taskboard-smoke-eof
    docker exec "$container" sh -c "curl -fsS --max-time 5 -w '\n$marker %{http_code} %{content_type}\n' 'http://127.0.0.1:$port$path'" | awk -v marker="$marker" '
        $1 == marker { code = $2; type = $3; exit }
        { body[NR] = $0 }
        END {
            print code
            print type
            sep = sprintf("%c", 28)
            for (i = 1; i <= NR - 1; i++) printf "%s%s", body[i], (i < NR - 1 ? sep : "")
        }
    '
}

health=""
for _ in $(seq 1 30); do
    if health=$(docker exec "$container" curl -fsS --max-time 2 "http://127.0.0.1:$port/api/v1/health" 2>/dev/null); then
        break
    fi
    sleep 1
done
[ -n "$health" ] || { echo "health endpoint did not become ready" >&2; exit 1; }

mapfile -t home < <(request /)
mapfile -t login < <(request /login)
home_code=${home[0]}
home_type=${home[1]}
home_body=${home[2]}
login_code=${login[0]}
login_body=${login[2]}
if [ "$home_code" != 200 ] || [[ "$home_type" != text/html* ]]; then
    echo "home check failed: HTTP $home_code, Content-Type $home_type" >&2
    exit 1
fi
if [ "$login_code" != 200 ] || [ "$home_body" != "$login_body" ]; then
    echo "SPA route check failed: /login HTTP $login_code, same HTML=$([ "$home_body" = "$login_body" ] && echo yes || echo no)" >&2
    exit 1
fi

asset=$(printf '%s\n' "$home_body" | tr '\034' '\n' | grep -oE '(src|href)="[^"]*/assets/[^"]*"' | head -1 | cut -d'"' -f2)
if [ -z "$asset" ]; then
    echo "no hashed /assets/* reference found in index.html" >&2
    exit 1
fi
mapfile -t asset_response < <(request "$asset")
if [ "${asset_response[0]}" != 200 ]; then
    echo "asset check failed: $asset HTTP ${asset_response[0]}" >&2
    exit 1
fi

server_version=$(printf '%s' "$health" | sed -n 's/.*"server_version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
web_version=$(printf '%s' "$health" | sed -n 's/.*"web_version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
if [ "$server_version" != "$expected_version" ] || [ "$web_version" != "$expected_version" ]; then
    echo "version check failed: server=$server_version web=$web_version expected=$expected_version" >&2
    exit 1
fi

echo "image smoke passed: $image (server=$server_version web=$web_version asset=$asset)"
