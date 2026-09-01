[CmdletBinding()]
param(
    [ValidateSet('amd64', 'arm64')]
    [string]$Arch = 'amd64',

    [string]$OutputDir = 'dist',

    [switch]$SkipTests
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$projectRoot = $PSScriptRoot
$outputRoot = if ([System.IO.Path]::IsPathRooted($OutputDir)) {
    [System.IO.Path]::GetFullPath($OutputDir)
} else {
    [System.IO.Path]::GetFullPath((Join-Path $projectRoot $OutputDir))
}

$packageName = "llama-control-windows-$Arch"
$exePath = Join-Path $outputRoot 'llama-control.exe'
$zipPath = Join-Path $outputRoot "$packageName.zip"
$checksumPath = "$zipPath.sha256"
$stageDir = Join-Path ([System.IO.Path]::GetTempPath()) ("llama-control-build-" + [guid]::NewGuid().ToString('N'))

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw 'Go was not found. Install Go 1.22 or newer and add it to PATH.'
}

$oldGOOS = $env:GOOS
$oldGOARCH = $env:GOARCH
$oldCGOEnabled = $env:CGO_ENABLED

try {
    Push-Location $projectRoot
    try {
        if (-not $SkipTests) {
            Write-Host 'Running tests...'
            & go test ./...
            if ($LASTEXITCODE -ne 0) { throw 'Tests failed.' }
        }

        New-Item -ItemType Directory -Path $outputRoot -Force | Out-Null
        New-Item -ItemType Directory -Path $stageDir -Force | Out-Null

        $env:GOOS = 'windows'
        $env:GOARCH = $Arch
        $env:CGO_ENABLED = '0'

        Write-Host "Building Windows/$Arch..."
        & go build -trimpath -ldflags '-s -w' -o $exePath .
        if ($LASTEXITCODE -ne 0) { throw 'Build failed.' }

        Copy-Item -LiteralPath $exePath -Destination (Join-Path $stageDir 'llama-control.exe')
        if (Test-Path -LiteralPath $zipPath) {
            Remove-Item -LiteralPath $zipPath -Force
        }

        Compress-Archive -Path (Join-Path $stageDir '*') -DestinationPath $zipPath -CompressionLevel Optimal
        $hash = (Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash.ToLowerInvariant()
        Set-Content -LiteralPath $checksumPath -Value "$hash  $([System.IO.Path]::GetFileName($zipPath))" -Encoding ascii

        Write-Host ''
        Write-Host 'Build package completed:'
        Write-Host "  EXE:    $exePath"
        Write-Host "  ZIP:    $zipPath"
        Write-Host "  SHA256: $checksumPath"
    } finally {
        Pop-Location
    }
} finally {
    $env:GOOS = $oldGOOS
    $env:GOARCH = $oldGOARCH
    $env:CGO_ENABLED = $oldCGOEnabled
    if (Test-Path -LiteralPath $stageDir) {
        Remove-Item -LiteralPath $stageDir -Recurse -Force
    }
}
