#!/usr/bin/env bash
set -euo pipefail
export MSYS_NO_PATHCONV=1

usage() {
    echo "usage: deploy.sh <version>" >&2
    exit 2
}

[ "$#" -eq 1 ] || usage
new_version=$1
deploy_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
compose_file=$deploy_dir/docker-compose.yml
config_file=$deploy_dir/config.toml
env_file=$deploy_dir/deploy.env

[ -f "$compose_file" ] || { echo "missing docker-compose.yml in $deploy_dir" >&2; exit 1; }
[ -f "$config_file" ] || { echo "missing config.toml in $deploy_dir" >&2; exit 1; }
[ -f "$env_file" ] || { echo "missing deploy.env in $deploy_dir" >&2; exit 1; }
# shellcheck disable=SC1091
. "$env_file"
[ -n "${IMAGE_REPO:-}" ] || { echo "deploy.env must define IMAGE_REPO" >&2; exit 1; }
service=${COMPOSE_SERVICE:-taskboard}
verify_timeout=${VERIFY_TIMEOUT:-120}
backup_dir=${BACKUP_DIR:-/app/data/backups}

docker_path() {
    if [[ "$OSTYPE" == msys* || "$OSTYPE" == cygwin* ]]; then
        cygpath -w "$1"
    else
        printf '%s\n' "$1"
    fi
}
compose() { docker compose -f "$(docker_path "$compose_file")" "$@"; }

read_public_url() {
    local line value
    while IFS= read -r line || [ -n "$line" ]; do
        line=${line#"${line%%[![:space:]]*}"}
        case "$line" in
            public_url[[:space:]]*=*)
                value=${line#public_url[[:space:]]*=}
                value=${value#"${value%%[![:space:]]*}"}
                value=${value%"${value##*[![:space:]]}"}
                value=${value%,}
                case "$value" in
                    \'*\') value=${value#\'}; value=${value%\'} ;;
                    \"*\") value=${value#\"}; value=${value%\"} ;;
                esac
                [ -n "$value" ] || break
                printf '%s\n' "${value%/}"
                return 0
                ;;
        esac
    done < "$config_file"
    echo "config.toml must define server public_url" >&2
    return 1
}

current_image=$(awk '
    $1 == "image:" {
        sub(/^[[:space:]]*image:[[:space:]]*/, "")
        sub(/[[:space:]]*#.*$/, "")
        gsub(/^"|"$/, "")
        print
        exit
    }
' "$compose_file")
[ -n "$current_image" ] || { echo "cannot read current image from docker-compose.yml" >&2; exit 1; }
current_version=${current_image##*:}
[ -n "$current_version" ] && [ "$current_version" != "$current_image" ] || {
    echo "current image has no version tag: $current_image" >&2
    exit 1
}
if [ "$current_version" = "$new_version" ]; then
    echo "already running $new_version; nothing to do"
    exit 0
fi
public_url=$(read_public_url)

echo "Backing up $current_image before upgrade to $new_version"
backup_output=$(compose exec -T "$service" /app/taskboard backup -preupdate \
    -config /app/config/config.toml)
backup_file=$(printf '%s\n' "$backup_output" | tail -n 1)
case "$backup_file" in
    taskboard-*-preupdate.db) ;;
    *) echo "cannot parse preupdate backup filename from: $backup_output" >&2; exit 1 ;;
esac
echo "Preupdate backup: $backup_file"

new_image=$IMAGE_REPO:$new_version
echo "Pulling $new_image"
docker pull "$new_image"

set_image() {
    local image=$1 tmp=$compose_file.new
    awk -v image="$image" '
        BEGIN { changed = 0 }
        !changed && $1 == "image:" { print "    image: " image; changed = 1; next }
        { print }
    ' "$compose_file" > "$tmp"
    mv "$tmp" "$compose_file"
}

verify() {
    local expected=$1 deadline=$((SECONDS + verify_timeout)) health server web code content_type
    while [ "$SECONDS" -lt "$deadline" ]; do
        health=$(curl -fsS --max-time 3 "$public_url/api/v1/health" 2>/dev/null || true)
        if [ -n "$health" ]; then
            server=$(printf '%s' "$health" | sed -n 's/.*"server_version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
            web=$(printf '%s' "$health" | sed -n 's/.*"web_version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
            response=$(curl -fsS --max-time 3 -w '\n%{http_code} %{content_type}\n' "$public_url/" 2>/dev/null || true)
            if [ -n "$response" ]; then
                code=$(printf '%s\n' "$response" | tail -n1 | cut -d' ' -f1)
                content_type=$(printf '%s\n' "$response" | tail -n1 | cut -d' ' -f2)
                if [ "$server" = "$expected" ] && [ "$web" = "$expected" ] &&
                    [ "$code" = 200 ] && [[ "$content_type" == text/html* ]]; then
                    echo "Verified version $expected through $public_url"
                    return 0
                fi
            fi
        fi
        sleep 3
    done
    echo "verification timed out for version $expected at $public_url" >&2
    return 1
}

backup_compose=$compose_file.bak-$(date +%Y%m%d%H%M%S)
cp -p "$compose_file" "$backup_compose"
set_image "$new_image"

rollback() {
    local reason=$1 restore_status verify_status
    echo "Deployment failed: $reason" >&2
    echo "Rolling back to $current_image and restoring $backup_file" >&2
    set +e
    compose stop "$service" >/dev/null 2>&1
    set_image "$current_image"
    compose run --rm --no-deps "$service" restore "$backup_dir/$backup_file" \
        -config /app/config/config.toml
    restore_status=$?
    compose up -d --wait
    local up_status=$?
    verify "$current_version"
    verify_status=$?
    set -e
    if [ "$restore_status" -eq 0 ] && [ "$up_status" -eq 0 ] && [ "$verify_status" -eq 0 ]; then
        echo "Rollback completed with pre-update backup restored." >&2
        exit 20
    fi
    echo "ROLLBACK ALSO FAILED. Restore=$restore_status up=$up_status verify=$verify_status. Manual intervention is required." >&2
    exit 30
}

if ! compose up -d --wait; then
    rollback "docker compose up --wait failed"
fi
if ! verify "$new_version"; then
    rollback "public verification failed"
fi

echo "Deployed $new_image successfully"
