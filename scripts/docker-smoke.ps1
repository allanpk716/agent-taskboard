[CmdletBinding()]
param(
    [string]$ImageName = "agent-taskboard:232-smoke",
    [Parameter(Mandatory = $true)]
    [string]$ClientVersion,
    [int]$Port = 8932
)

$ErrorActionPreference = "Stop"
if ($Port -ge 8911 -and $Port -le 8914 -or $Port -eq 8901) {
    throw "Smoke port must not use 8901 or 8911-8914"
}

$Project = "taskboard-smoke-232"
$Root = Join-Path $env:TEMP ("taskboard-smoke-232-" + $PID)
New-Item -ItemType Directory -Path $Root | Out-Null
$ConfigPath = Join-Path $Root "config.toml"
$RequestDir = Join-Path $Root "requests"
New-Item -ItemType Directory -Path $RequestDir | Out-Null
$LoginPath = Join-Path $RequestDir "login.json"
$ComposePath = Join-Path $Root "compose.yml"
$ConfigForCompose = $ConfigPath -replace '\\', '/'
$RequestDirForCompose = $RequestDir -replace '\\', '/'
$PublicUrl = "http://127.0.0.1:$Port"

@(
    "data_dir = '/app/data'"
    "port = $Port"
    "web_dir = '/app/web'"
    ""
    "[server]"
    "public_url = '$PublicUrl'"
    "deploy = 'container'"
    "client_image_dir = '/app/client-package'"
    ""
    "[backup]"
    "target = '/app/data/backups'"
    ""
    "[update]"
    "sources = []"
) | Set-Content -Encoding ascii -Path $ConfigPath

@"
services:
  taskboard:
    image: $ImageName
    network_mode: host
    restart: unless-stopped
    environment:
      TASKBOARD_PORT: "$Port"
    volumes:
      - ${Project}-data:/app/data
      - ${ConfigForCompose}:/app/config/config.toml:ro
      - ${RequestDirForCompose}:/app/requests
volumes:
  ${Project}-data:
"@ | Set-Content -Encoding ascii -Path $ComposePath

$cleanup = {
    docker compose -p $Project -f $ComposePath down -v | Out-Host
    docker image rm $ImageName | Out-Host
}
trap {
    & $cleanup
    break
}

function Invoke-ContainerJson {
    param([string]$Method = "GET", [string]$Path = "health", [string]$Body = "")
    $curlArgs = @("compose", "-p", $Project, "-f", $ComposePath, "exec", "-T", "taskboard", "curl", "-fsS")
    if ($Method -eq "POST") {
        Set-Content -Encoding ascii -Path $LoginPath -Value $Body
        $curlArgs += @("-H", "Content-Type: application/json", "--data-binary", "@/app/requests/login.json")
    }
    $curlArgs += "$PublicUrl/api/v1/$Path"
    Write-Host "HTTP $Method /api/v1/$Path"
    $raw = docker @curlArgs
    if ($LASTEXITCODE -ne 0 -or -not $raw) { throw "HTTP $Method /api/v1/$Path failed: $raw" }
    return ($raw | ConvertFrom-Json)
}

docker build -t $ImageName --build-arg APP_VERSION=0.0.0 `
    --build-arg CLIENT_VERSION=$ClientVersion `
    --build-arg BUILD_TAGS=e2e_fixed_captcha .
if ($LASTEXITCODE -ne 0) { throw "docker build failed" }

docker compose -p $Project -f $ComposePath up -d --wait
if ($LASTEXITCODE -ne 0) { throw "container startup failed" }

$health = Invoke-ContainerJson
if ($health.status -ne "ok") { throw "health returned $($health.status)" }
Write-Host "Health passed: server $($health.server_version), web $($health.web_version)"

$id = docker compose -p $Project -f $ComposePath exec -T taskboard id
if ($id -notmatch "uid=65532\(taskboard\)") { throw "Container process is not non-root taskboard user: $id" }
Write-Host "Non-root check passed: $id"

docker compose -p $Project -f $ComposePath exec -T taskboard /app/taskboard create-admin `
    -config /app/config/config.toml -username smoke-admin -password smoke-pass-123 | Out-Host
if ($LASTEXITCODE -ne 0) { throw "create-admin failed" }

$captcha = Invoke-ContainerJson -Path "login-captcha"
$loginBody = @{
    username = "smoke-admin"
    password = "smoke-pass-123"
    captcha_id = $captcha.id
    captcha_answer = "TB123"
} | ConvertTo-Json -Compress
$login = Invoke-ContainerJson -Method POST -Path "session" -Body $loginBody
if ($login.viewer.kind -ne "member") { throw "Login returned an unexpected viewer kind" }
Write-Host "Captcha login passed: smoke-admin"

docker compose -p $Project -f $ComposePath down | Out-Host
if ($LASTEXITCODE -ne 0) { throw "stopping container failed" }
docker compose -p $Project -f $ComposePath up -d --wait | Out-Host
if ($LASTEXITCODE -ne 0) { throw "recreating container failed" }

$captcha = Invoke-ContainerJson -Path "login-captcha"
$persistBody = @{
    username = "smoke-admin"
    password = "smoke-pass-123"
    captcha_id = $captcha.id
    captcha_answer = "TB123"
} | ConvertTo-Json -Compress
$persistLogin = Invoke-ContainerJson -Method POST -Path "session" -Body $persistBody
if ($persistLogin.viewer.member.username -ne "smoke-admin") {
    throw "Login failed after recreation; data was not retained"
}
Write-Host "Data retention after container recreation passed"

$size = docker image inspect $ImageName --format "{{.Size}}"
Write-Host "Image size: $([math]::Round($size / 1MB, 1)) MB"

& $cleanup
Write-Host "Docker smoke test passed"
