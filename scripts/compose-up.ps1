# Sequential compose build + up for Docker Desktop (Windows).
# `docker compose up --build` sends every service to Bake at once and
# kills the engine: rpc Unavailable / error reading from server: EOF.
#
# Usage (from repo root):
#   .\scripts\compose-up.ps1
#   .\scripts\compose-up.ps1 -SkipAi

[CmdletBinding()]
param(
    [switch]$SkipAi
)

$ErrorActionPreference = "Stop"
Set-Location (Split-Path -Parent $PSScriptRoot)

$env:COMPOSE_BAKE = "false"
$env:COMPOSE_PARALLEL_LIMIT = "1"
$env:BUILDX_NO_DEFAULT_ATTESTATIONS = "1"

function Assert-Docker {
    docker info 1>$null 2>$null
    if ($LASTEXITCODE -ne 0) {
        throw @"
Docker Desktop is not running (or the engine is crashed).
Quit Docker Desktop fully, start it, wait until the whale is idle, then:
  docker info
When that works, re-run this script. Do not use: docker compose up -d --build
"@
    }
}

$services = @(
    "the_monkeys_authz",
    "the_monkeys_user",
    "the_monkeys_blog",
    "the_monkeys_groups",
    "the_monkeys_events",
    "the_monkeys_activity",
    "the_monkeys_storage",
    "the_monkeys_notification",
    "the_monkeys_gateway"
)

if (-not $SkipAi) {
    $services += "the_monkeys_ai_engine"
}

Write-Host "Checking Docker engine..."
Assert-Docker
Write-Host "Building $($services.Count) images one at a time."

$i = 0
foreach ($name in $services) {
    $i++
    Write-Host ""
    Write-Host "=== [$i/$($services.Count)] docker compose build $name ==="
    docker compose build $name
    if ($LASTEXITCODE -ne 0) {
        throw "Build failed: $name (exit $LASTEXITCODE). Fix that image, then re-run; finished services are cached."
    }
}

Write-Host ""
Write-Host "=== docker compose up -d (no --build) ==="
docker compose up -d
if ($LASTEXITCODE -ne 0) {
    throw "compose up failed (exit $LASTEXITCODE)"
}

Write-Host ""
Write-Host "Stack is up. Gateway is typically http://localhost:8081"
