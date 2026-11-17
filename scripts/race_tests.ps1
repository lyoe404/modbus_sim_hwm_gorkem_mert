# Run from the project root on Windows with Docker Desktop in Linux containers mode.
# Runs the actual module tests with the race detector on Linux/amd64 (CGO enabled).
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
foreach ($module in @('backend', 'simulators')) {
    Write-Host "=== go test -race ./$module ==="
    docker run --rm --platform linux/amd64 `
      -v "${root}:/src" -w "/src/$module" `
      -e CGO_ENABLED=1 golang:1.26-bookworm `
      go test -race -count=1 ./...
    if ($LASTEXITCODE -ne 0) {
        throw "$module race tests FAILED (exit code $LASTEXITCODE)"
    }
    Write-Host "PASS: $module"
}
Write-Host 'PASS: backend and simulators race tests'
