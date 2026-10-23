#!/usr/bin/env bash
set -euo pipefail
export MSYS_NO_PATHCONV=1

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
docker_path() {
    if [[ "$OSTYPE" == msys* || "$OSTYPE" == cygwin* ]]; then
        cygpath -w "$1"
    else
        printf '%s\n' "$1"
    fi
}
dc() {
    local file=$1
    shift
    docker compose -f "$(docker_path "$file")" "$@"
}
case_name=deploy-test-$$
registry_name=taskboard-deploy-test-registry-$$
registry_url=
had_registry=false
temp_dirs=()
projects=()
compose_files=()
local_images=(
    taskboard:deploy-test-a
    taskboard:deploy-test-b
    taskboard:deploy-test-bad
)
base_port=$((19000 + $$ % 500))
normal_port=$base_port
rollback_port=$((base_port + 1))
pullfail_port=$((base_port + 2))

cleanup() {
    set +e
    local id volume dir
    for id in $(docker ps -aq --filter name=deploy-test); do
        docker rm -f "$id" >/dev/null 2>&1
    done
    for volume in $(docker volume ls --format '{{.Name}}' | grep deploy-test); do
        docker volume rm "$volume" >/dev/null 2>&1
    done
    [ -n "$registry_url" ] && docker rmi \
        "$registry_url:0.1.0" "$registry_url:0.2.0" "$registry_url:0.3.0" >/dev/null 2>&1
    docker rmi "${local_images[@]}" >/dev/null 2>&1
    docker rm -f "$registry_name" >/dev/null 2>&1
    $had_registry || docker rmi registry:2 >/dev/null 2>&1
    for dir in "${temp_dirs[@]}"; do
        rm -f "$dir/docker-compose.yml" "$dir/config.toml" "$dir/deploy.env" \
            "$dir/deploy.sh" "$dir/bin/curl"
        rmdir "$dir/bin" "$dir" >/dev/null 2>&1
    done
    set -e
}
trap cleanup EXIT

start_registry() {
    local port name
    if docker image inspect registry:2 >/dev/null 2>&1; then
        had_registry=true
    fi
    for port in 5055 5056 5057 5058; do
        if docker run -d --name "$registry_name" -p "$port:5000" registry:2 >/dev/null; then
            if [[ "$OSTYPE" == msys* || "$OSTYPE" == cygwin* ]]; then
                registry_url=127.0.0.1:$port/taskboard
            else
                registry_url=127.0.0.1:$port/taskboard
            fi
            return 0
        fi
        docker rm -f "$registry_name" >/dev/null 2>&1 || true
    done
    echo "cannot start local registry" >&2
    return 1
}

build_images() {
    local client_version build_root
    client_version=${CLIENT_VERSION:-$(curl -fsSL \
        https://github.com/allanpk716/agent-taskboard/releases/download/channel-stable/latest.json \
        | awk '/"client"[[:space:]]*:/ {in_client=1; next}
            in_client && /"version"/ {gsub(/.*"version"[[:space:]]*:[[:space:]]*"|".*/, ""); print; exit}')}
    [ -n "$client_version" ] || { echo "cannot determine CLIENT_VERSION" >&2; exit 1; }

    build_root=$(docker_path "$repo_root")
    docker build --build-arg APP_VERSION=0.1.0 --build-arg CLIENT_VERSION="$client_version" \
        --build-arg BUILD_TAGS=e2e_fixed_captcha -t taskboard:deploy-test-a "$build_root" >/dev/null
    docker build --build-arg APP_VERSION=0.2.0 --build-arg CLIENT_VERSION="$client_version" \
        --build-arg BUILD_TAGS=e2e_fixed_captcha -t taskboard:deploy-test-b "$build_root" >/dev/null
    docker build --build-arg APP_VERSION=9.9.9 --build-arg CLIENT_VERSION="$client_version" \
        --build-arg BUILD_TAGS=e2e_fixed_captcha -t taskboard:deploy-test-bad "$build_root" >/dev/null
}

push_images() {
    docker tag taskboard:deploy-test-a "$registry_url:0.1.0"
    docker tag taskboard:deploy-test-b "$registry_url:0.2.0"
    docker tag taskboard:deploy-test-bad "$registry_url:0.3.0"
    docker push "$registry_url:0.1.0" >/dev/null
    docker push "$registry_url:0.2.0" >/dev/null
    docker push "$registry_url:0.3.0" >/dev/null
}

make_case() {
    local name=$1 port=$2
    local dir project
    dir=$(mktemp -d -t "$name-$case_name-XXXXXX")
    project=$(basename "$dir")
    project=${project,,}
    mkdir -p "$dir/bin"
    temp_dirs+=("$dir")
    projects+=("$project")

    cat > "$dir/docker-compose.yml" <<EOF
services:
  taskboard:
    image: $registry_url:0.1.0
    network_mode: host
    restart: unless-stopped
    volumes:
      - taskboard-data:/app/data
      - ./config.toml:/app/config/config.toml:ro
volumes:
  taskboard-data:
EOF
    cat > "$dir/config.toml" <<EOF
data_dir = '/app/data'
port = $port
web_dir = '/app/web'

[server]
public_url = 'http://127.0.0.1:$port'
deploy = 'container'
client_image_dir = '/app/client-package'

[backup]
target = '/app/data/backups'

[update]
sources = []
EOF
    cat > "$dir/deploy.env" <<EOF
IMAGE_REPO=$registry_url
VERIFY_TIMEOUT=10
EOF
    cp "$script_dir/deploy.sh" "$dir/deploy.sh"
    chmod +x "$dir/deploy.sh"
    compose_files+=("$dir/docker-compose.yml")
    printf '%s\n' "$dir"
}

write_curl_wrapper() {
    local dir=$1 port=$2 project file real_curl
    project=$(basename "$dir")
    project=${project,,}
    file=$(docker_path "$dir/docker-compose.yml")
    real_curl=$(command -v curl)
    cat > "$dir/bin/curl" <<EOF
#!/usr/bin/env bash
args=("\$@")
url=\${args[\${#args[@]}-1]}
unset 'args[\${#args[@]}-1]'
case "\$url" in
    http://127.0.0.1:$port/*) exec docker compose -p "$project" -f '$file' exec -T taskboard curl "\${args[@]}" "\$url" ;;
    *) exec '$real_curl' "\${args[@]}" "\$url" ;;
esac
EOF
    chmod +x "$dir/bin/curl"
}

compose_api() {
    local project=$1 file=$2
    shift 2
    dc "$file" exec -T taskboard curl "$@"
}

health_version() {
    compose_api "$1" "$2" -fsS http://127.0.0.1:"$3"/api/v1/health \
        | sed -n 's/.*"server_version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'
}

rollback_session=
login_session() {
    local project=$1 file=$2 port=$3 captcha_id
    captcha_id=$(compose_api "$project" "$file" -fsS http://127.0.0.1:$port/api/v1/login-captcha \
        | sed -n 's/.*"id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
    compose_api "$project" "$file" -fsS -i \
        -H 'Content-Type: application/json' \
        --data "{\"username\":\"admin\",\"password\":\"secret-pass-123\",\"captcha_id\":\"$captcha_id\",\"captcha_answer\":\"TB123\"}" \
        http://127.0.0.1:$port/api/v1/session \
        | sed -n 's/.*Set-Cookie: tb_session=\([^;]*\).*/\1/p' | head -1
}

create_preupgrade_ticket() {
    local project=$1 file=$2 port=$3 ticket
    rollback_session=$(login_session "$project" "$file" "$port")
    [ -n "$rollback_session" ] || { echo "test login failed" >&2; exit 1; }
    ticket=$(compose_api "$project" "$file" -fsS \
        -H "Cookie: tb_session=$rollback_session" -H 'Content-Type: application/json' \
        --data '{"kind":"task","title":"rollback marker","brief_md":"data before upgrade","labels":["机型:UD-2300"]}' \
        http://127.0.0.1:$port/api/v1/tickets)
    printf '%s\n' "$ticket" | sed -n 's/.*"number"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1
}

start_case() {
    local dir=$1 port=$2 project file
    project=$(basename "$dir")
    file=$dir/docker-compose.yml
    dc "$file" up -d --wait >/dev/null
    dc "$file" exec -T taskboard /app/taskboard create-admin \
        -config /app/config/config.toml -username admin -password secret-pass-123 >/dev/null
}

assert_image_tag() {
    local dir=$1 expected=$2
    local got
    got=$(awk '$1 == "image:" {print $2; exit}' "$dir/docker-compose.yml")
    [ "$got" = "$expected" ] || { echo "expected image $expected, got $got" >&2; exit 1; }
}

start_registry
build_images
push_images

normal_dir=$(make_case normal "$normal_port")
write_curl_wrapper "$normal_dir" "$normal_port"
start_case "$normal_dir" "$normal_port"
(cd "$normal_dir" && PATH="$PWD/bin:$PATH" ./deploy.sh 0.2.0) >/dev/null
[ "$(health_version "$(basename "$normal_dir")" "$normal_dir/docker-compose.yml" "$normal_port")" = 0.2.0 ]
assert_image_tag "$normal_dir" "$registry_url:0.2.0"
echo "normal upgrade passed"

rollback_dir=$(make_case rollback "$rollback_port")
write_curl_wrapper "$rollback_dir" "$rollback_port"
start_case "$rollback_dir" "$rollback_port"
ticket=$(create_preupgrade_ticket "$(basename "$rollback_dir")" "$rollback_dir/docker-compose.yml" "$rollback_port")
[ -n "$ticket" ] || { echo "cannot create rollback marker ticket" >&2; exit 1; }
rollback_project=$(basename "$rollback_dir")
rollback_file=$rollback_dir/docker-compose.yml
if (cd "$rollback_dir" && PATH="$PWD/bin:$PATH" ./deploy.sh 0.3.0) >/dev/null; then
    echo "bad version deployment should fail and roll back" >&2
    exit 1
fi
[ "$(health_version "$rollback_project" "$rollback_file" "$rollback_port")" = 0.1.0 ]
assert_image_tag "$rollback_dir" "$registry_url:0.1.0"
status=$(compose_api "$rollback_project" "$rollback_file" -s -o /dev/null -w '%{http_code}' \
    -H "Cookie: tb_session=$(login_session "$rollback_project" "$rollback_file" "$rollback_port")" \
    http://127.0.0.1:$rollback_port/api/v1/tickets/$ticket)
[ "$status" = 200 ] || { echo "pre-upgrade ticket was not restored: HTTP $status" >&2; exit 1; }
dc "$rollback_file" exec -T taskboard \
    sh -c "test -n \"\$(ls /app/data/backups/*-preupdate.db 2>/dev/null | head -1)\""
echo "failed deployment rollback passed"

pullfail_dir=$(make_case pullfail "$pullfail_port")
write_curl_wrapper "$pullfail_dir" "$pullfail_port"
start_case "$pullfail_dir" "$pullfail_port"
if (cd "$pullfail_dir" && PATH="$PWD/bin:$PATH" ./deploy.sh 99.0.0) >/dev/null; then
    echo "missing image pull should fail" >&2
    exit 1
fi
[ "$(health_version "$(basename "$pullfail_dir")" "$pullfail_dir/docker-compose.yml" "$pullfail_port")" = 0.1.0 ]
assert_image_tag "$pullfail_dir" "$registry_url:0.1.0"
echo "pull failure passed"

echo "deploy script tests passed"
