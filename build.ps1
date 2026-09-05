$ErrorActionPreference = "Stop"

# Always run from the directory containing this script.
Set-Location -LiteralPath $PSScriptRoot

Write-Host ""
Write-Host "========================================" -ForegroundColor Cyan
Write-Host " WPKM BUILD" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""

Write-Host "[1/8] Checking Go..." -ForegroundColor Yellow
go version

Write-Host ""
Write-Host "[2/8] Checking target..." -ForegroundColor Yellow
$goos = go env GOOS
$goarch = go env GOARCH

Write-Host "GOOS : $goos"
Write-Host "GOARCH: $goarch"

if ($goos -ne "windows") {
    throw "WPKM must be built for Windows."
}

if ($goarch -ne "amd64") {
    throw "WPKM currently requires windows/amd64."
}

Write-Host ""
Write-Host "[3/8] Cleaning previous build..." -ForegroundColor Yellow
go clean

$Output = Join-Path $PSScriptRoot "wpkm.exe"

if (Test-Path -LiteralPath $Output) {
    Remove-Item -LiteralPath $Output -Force
}

Write-Host ""
Write-Host "[4/8] Formatting source..." -ForegroundColor Yellow
gofmt -w `
    (Join-Path $PSScriptRoot "cmd\wpkm\main.go") `
    (Join-Path $PSScriptRoot "internal\auth\token.go") `
    (Join-Path $PSScriptRoot "internal\hash\hash.go") `
    (Join-Path $PSScriptRoot "internal\registry\github.go")

Write-Host ""
Write-Host "[5/8] Updating dependencies..." -ForegroundColor Yellow
go mod tidy
go mod download

Write-Host ""
Write-Host "[6/8] Running tests..." -ForegroundColor Yellow
go test ./...

Write-Host ""
Write-Host "[7/8] Running go vet..." -ForegroundColor Yellow
go vet ./...

Write-Host ""
Write-Host "[8/8] Building wpkm.exe..." -ForegroundColor Yellow
go build -o $Output ./cmd/wpkm

if (-not (Test-Path -LiteralPath $Output)) {
    throw "Build failed: wpkm.exe was not created."
}

$File = Get-Item -LiteralPath $Output

Write-Host ""
Write-Host "========================================" -ForegroundColor Green
Write-Host " BUILD SUCCESSFUL" -ForegroundColor Green
Write-Host "========================================" -ForegroundColor Green
Write-Host ""
Write-Host "Output : $($File.FullName)" -ForegroundColor Green
Write-Host "Size   : $($File.Length) bytes" -ForegroundColor Green
Write-Host ""

Write-Host "Testing executable..." -ForegroundColor Yellow
& $Output version

Write-Host ""
Write-Host "Done. 🚀" -ForegroundColor Green
