#!/usr/bin/env bash
set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
compose_file=$script_dir/docker-compose.yml
config_file=$script_dir/config.toml
oss_env_file=${OSS_ENV_FILE:-$script_dir/oss.env}
base_url=${TASKBOARD_BASE_URL:-}
backup_name=
attachment_path=
html_attachment_path=
archive_path=
outside_prefix_url=
header_args=()

usage() {
    cat <<'EOF'
Usage: oss-cutover-checklist.sh [options]

Interactive checklist for switching taskboard attachments from local to OSS.
It stops at the first failed gate and never changes config.toml automatically.

Maintenance steps（维护步骤）:
  1. 手动备份 → 停服.
  2. 迁移 dry-run：`migrate-attachments -to oss -dry-run`.
  3. 正式迁移：失败数 0、缺失数 0.
  4. 改配置启动.

Required post-switch checks:
  health；上传；附件 302 带 no-store；HTML 被强制下载；插图；
  Lightbox；访客；curl -L；归档包；统计；备份对象；zstd；
  默认域名返回 200；前缀外返回 403.
  The next day, confirm reconciliation found 0 orphan objects.

Options:
  --base-url URL           Public base URL (default: server.public_url)
  --compose-file PATH      Compose file (default: docker-compose.yml beside script)
  --config-file PATH       taskboard config (default: config.toml beside script)
  --oss-env-file PATH      OSS credential env file (default: oss.env beside script)
  --backup-name NAME       Backup object to check with backup-url
  --attachment-path PATH   Same-origin attachment path to test
  --html-attachment-path PATH
                           Same-origin HTML attachment path to test
  --archive-path PATH      Same-origin archive package path to test
  --outside-prefix-url URL
                           Short-lived signed URL outside the root prefix; expect 403
  --header HEADER          Additional curl header; repeat as needed (for example Cookie)
  -h, --help               Show this help

Rollback reminders:
  - Migration failure: keep backend = "local" and start the old service.
  - Startup failure: fix oss.env first; otherwise migrate back with -to local.
  - Later rollback: stop, migrate -to local, change backend = "local", start.
EOF
}

fail() {
    echo "cutover check failed: $*" >&2
    exit 1
}

confirm() {
    local prompt=$1 answer
    read -r -p "$prompt [yes/no] " answer
    [ "$answer" = yes ] || fail "$prompt was not confirmed"
}

confirm_seen() {
    confirm "$1"
}

compose() {
    docker compose -f "$compose_file" "$@"
}

config_value() {
    local section=$1 key=$2 value
    value=$(awk -v section="[$section]" -v key="$key" '
        {
            line = $0
            gsub(/^[[:space:]]+|[[:space:]]+$/, "", line)
            if (line == section) { in_section = 1; next }
            if (line ~ /^\[/) { in_section = 0 }
            if (in_section && line ~ "^" key "[[:space:]]*=") {
                sub("^" key "[[:space:]]*=[[:space:]]*", "", line)
                sub(/[[:space:]]*#.*$/, "", line)
                sub(/[[:space:]]+$/, "", line)
                gsub(/^"|"$/, "", line)
                gsub(/^'\''|'\''/, "", line)
                print line
                exit
            }
        }
    ' "$config_file")
    printf '%s\n' "$value"
}

compose_config() {
    docker compose -f "$compose_file" config >/dev/null
}

curl_same() {
    local path=$1 url
    shift
    url=$base_url$path
    if [ "${#header_args[@]}" -gt 0 ]; then
        curl -sS "$@" "${header_args[@]}" "$url"
    else
        curl -sS "$@" "$url"
    fi
}

http_code() {
    local url=$1
    curl -sS -o /dev/null -w '%{http_code}' "$url"
}

check_signed_redirect() {
    local label=$1 path=$2 headers status location
    [ -n "$path" ] || fail "$label path is required"
    headers=$(curl_same "$path" --max-redirs 0 -D - -o /dev/null) ||
        fail "cannot request $label"
    status=$(awk 'NR==1 {for (i=1; i<=NF; i++) if ($i ~ /^[0-9][0-9][0-9]$/) {print $i; exit}}' <<<"$headers")
    [ "$status" = 302 ] || fail "$label returned HTTP $status, expected 302"
    grep -qi '^cache-control:.*no-store' <<<"$headers" ||
        fail "$label 302 response is missing Cache-Control: no-store"
    location=$(awk -F ': ' 'tolower($1) == "location" {sub(/\r$/, "", $2); print $2; exit}' <<<"$headers")
    [ -n "$location" ] || fail "$label 302 response has no Location"
    printf '%s signed URL: %s\n' "$label" "$location" >&2
    printf '%s' "$location"
}

check_url_status() {
    local label=$1 url=$2 expected=$3 code
    code=$(http_code "$url") || fail "cannot request $label"
    [ "$code" = "$expected" ] ||
        fail "$label returned HTTP $code, expected $expected"
}

check_zstd() {
    local headers
    headers=$(curl -sS -o /dev/null -D - -H 'Accept-Encoding: zstd' "$base_url/") ||
        fail "cannot request the public home page"
    grep -qi '^content-encoding: zstd' <<<"$headers" ||
        fail "public home page is not compressed with zstd; Caddy encode is mandatory"
}

while [ "$#" -gt 0 ]; do
    case $1 in
        --base-url) base_url=$2; shift 2 ;;
        --compose-file) compose_file=$2; shift 2 ;;
        --config-file) config_file=$2; shift 2 ;;
        --oss-env-file) oss_env_file=$2; shift 2 ;;
        --backup-name) backup_name=$2; shift 2 ;;
        --attachment-path) attachment_path=$2; shift 2 ;;
        --html-attachment-path) html_attachment_path=$2; shift 2 ;;
        --archive-path) archive_path=$2; shift 2 ;;
        --outside-prefix-url) outside_prefix_url=$2; shift 2 ;;
        --header) header_args+=("$2"); shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
    esac
done

[ -f "$compose_file" ] || fail "compose file not found: $compose_file"
[ -f "$config_file" ] || fail "config file not found: $config_file"
[ -f "$oss_env_file" ] || fail "oss.env not found: $oss_env_file"
compose_config

backend=$(config_value attachments backend)
bucket=$(config_value attachments.oss bucket)
prefix=$(config_value attachments.oss prefix)
region=$(config_value attachments.oss region)
signing_host=$(config_value attachments.oss signing_host)
[ "$backend" = local ] || fail "before cutover [attachments] backend must be local, got '$backend'"
[ -n "$bucket" ] && [ -n "$prefix" ] && [ -n "$region" ] ||
    fail "bucket, region and prefix must be configured before migration"
[ -z "$signing_host" ] ||
    fail "the default-domain check requires signing_host to be empty"
case $region in
    oss-*) default_oss_host=$bucket.$region.aliyuncs.com ;;
    *) default_oss_host=$bucket.oss-$region.aliyuncs.com ;;
esac

if [ -z "$base_url" ]; then
    base_url=$(config_value server public_url)
fi
base_url=${base_url%/}
[ -n "$base_url" ] || fail "server.public_url or --base-url is required"

printf 'Cutover target: bucket=%s region=%s prefix=%s base_url=%s\n' \
    "$bucket" "$region" "$prefix" "$base_url"
printf 'Bucket prerequisites: private; public access blocked; SSE-OSS; no versioning; no CORS; no Referer rule.\n'
printf 'Lifecycle rules cover only %s: abort multipart uploads after 1 day and remove archive-packages after 8 days.\n' "$prefix"
confirm_seen "Bucket settings and the prefix bucket policy have been checked"
confirm_seen "oss.env permission is 600 and only taskboard can read it"

printf '\n== Manual backup and stop ==\n'
confirm "Run taskboard backup now"
compose exec -T taskboard /app/taskboard backup -config /app/config/config.toml
confirm "Stop taskboard now"
compose stop taskboard

printf '\n== Migration rehearsal ==\n'
confirm "Run migrate-attachments -to oss -dry-run"
dry_run_output=$(compose run --rm --no-deps taskboard \
    migrate-attachments -to oss -dry-run -config /app/config/config.toml)
printf '%s\n' "$dry_run_output"
grep -Eq '源端缺失 0 个' <<<"$dry_run_output" ||
    fail "dry run reports missing source objects; do not continue"
confirm_seen "Dry-run counts, total size and source-only report reviewed"

printf '\n== Formal migration ==\n'
confirm "Run migrate-attachments -to oss"
migration_output=$(compose run --rm --no-deps taskboard \
    migrate-attachments -to oss -config /app/config/config.toml)
printf '%s\n' "$migration_output"
grep -Eq '失败 0 个、源端缺失 0 个' <<<"$migration_output" ||
    fail "formal migration must finish with failure count 0 and missing count 0"

printf '\n== Change config and start ==\n'
echo "Edit $config_file: [attachments] backend = \"oss\""
confirm_seen "Config changed to OSS"
[ "$(config_value attachments backend)" = oss ] ||
    fail "config still does not say backend = oss"
confirm "Start taskboard now"
compose up -d --wait

printf '\n== Automated service checks ==\n'
health=$(curl -fsS "$base_url/api/v1/health") ||
    fail "health endpoint is unavailable"
check_zstd

[ -n "$attachment_path" ] || read -r -p "Same-origin attachment path to test: " attachment_path
attachment_url=$(check_signed_redirect "attachment" "$attachment_path")
check_url_status "attachment through its signed URL" "$attachment_url" 200
case $attachment_url in
    https://$default_oss_host/*) ;;
    *) fail "attachment URL is not on the default OSS domain $default_oss_host" ;;
esac
printf 'Equivalent client behavior: curl -L follows the same-origin 302 to the signed URL.\n'

[ -n "$html_attachment_path" ] || read -r -p "Same-origin HTML attachment path to test: " html_attachment_path
html_url=$(check_signed_redirect "HTML attachment" "$html_attachment_path")
html_headers=$(curl -sS -D - -o /dev/null "$html_url") ||
    fail "cannot request HTML attachment through OSS"
grep -qi '^content-disposition:.*attachment' <<<"$html_headers" ||
    fail "HTML attachment is not forced to download"

[ -n "$archive_path" ] || read -r -p "Same-origin archive package path to test: " archive_path
archive_url=$(check_signed_redirect "archive package" "$archive_path")
check_url_status "archive package through its signed URL" "$archive_url" 200

if [ -z "$backup_name" ]; then
    read -r -p "Backup object name to check (for example taskboard-YYYYMMDD-HHMMSS-daily.db): " backup_name
fi
backup_url=$(compose exec -T taskboard /app/taskboard backup-url "$backup_name" \
    -config /app/config/config.toml | tail -n 1)
[ -n "$backup_url" ] || fail "backup-url returned no URL"
check_url_status "OSS backup object" "$backup_url" 200

[ -n "$outside_prefix_url" ] || read -r -p "Short-lived signed URL outside $prefix (expect 403): " outside_prefix_url
check_url_status "outside-prefix signed URL" "$outside_prefix_url" 403

printf '\n== Browser and workflow checks ==\n'
confirm_seen "A new attachment and a new illustration were uploaded successfully"
confirm_seen "The inline illustration renders in the ticket brief"
confirm_seen "Clicking an image attachment opens Lightbox"
confirm_seen "A guest can open a ticket and download its attachment"
confirm_seen "Admin statistics load and report attachment usage under OSS"
confirm_seen "curl -L downloaded an attachment from an authenticated same-origin URL"

printf '\nAll in-window checks passed.\n'
printf 'Next day: confirm reconciliation found 0 orphan objects.\n'
