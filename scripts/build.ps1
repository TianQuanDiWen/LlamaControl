[CmdletBinding()]
param(
    [ValidateSet('all', 'windows', 'darwin', 'linux')]
    [string]$OS = 'all',

    [ValidateSet('all', 'amd64', 'arm64')]
    [string]$Arch = 'all',

    [string]$OutputDir = 'dist',

    [switch]$SkipTests
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$scriptDir = $PSScriptRoot
$projectRoot = if (Test-Path (Join-Path $scriptDir "go.mod")) { $scriptDir } else { (Split-Path -Parent $scriptDir) }
$outputRoot = if ([System.IO.Path]::IsPathRooted($OutputDir)) {
    [System.IO.Path]::GetFullPath($OutputDir)
} else {
    [System.IO.Path]::GetFullPath((Join-Path $projectRoot $OutputDir))
}

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw 'Go was not found. Install Go 1.22 or newer and add it to PATH.'
}

# 定义所有支持的发布目标
$targets = @(
    @{ TargetOS = 'windows'; TargetArch = 'amd64'; Bin = 'llama-control.exe'; Ext = 'zip' }
    @{ TargetOS = 'windows'; TargetArch = 'arm64'; Bin = 'llama-control.exe'; Ext = 'zip' }
    @{ TargetOS = 'darwin';  TargetArch = 'arm64'; Bin = 'llama-control';     Ext = 'tar.gz' }
    @{ TargetOS = 'darwin';  TargetArch = 'amd64'; Bin = 'llama-control';     Ext = 'tar.gz' }
    @{ TargetOS = 'linux';   TargetArch = 'amd64'; Bin = 'llama-control';     Ext = 'tar.gz' }
    @{ TargetOS = 'linux';   TargetArch = 'arm64'; Bin = 'llama-control';     Ext = 'tar.gz' }
)

$selectedTargets = @($targets | Where-Object {
    ($OS -eq 'all' -or $_.TargetOS -eq $OS) -and
    ($Arch -eq 'all' -or $_.TargetArch -eq $Arch)
})

if ($selectedTargets.Count -eq 0) {
    throw "未找到匹配的目标平台与架构配置 (OS: $OS, Arch: $Arch)"
}

$oldGOOS = $env:GOOS
$oldGOARCH = $env:GOARCH
$oldCGOEnabled = $env:CGO_ENABLED

try {
    Push-Location $projectRoot
    try {
        if (-not $SkipTests) {
            Write-Host 'Running tests...' -ForegroundColor Cyan
            & go test ./...
            if ($LASTEXITCODE -ne 0) { throw 'Tests failed.' }
        }

        New-Item -ItemType Directory -Path $outputRoot -Force | Out-Null

        $results = @()

        $packerDir = $null
        $packerExe = $null
        $tarTargets = @($selectedTargets | Where-Object { $_.Ext -eq 'tar.gz' })
        if ($tarTargets.Count -gt 0) {
            $packerDir = Join-Path ([System.IO.Path]::GetTempPath()) ("llama-control-packer-" + [guid]::NewGuid().ToString('N'))
            New-Item -ItemType Directory -Path $packerDir -Force | Out-Null
            $hostExeSuffix = (& go env GOEXE).Trim()
            $packerExe = Join-Path $packerDir "package_tar$hostExeSuffix"
            $tarPackerSource = Join-Path $scriptDir "package_tar.go"

            Write-Host 'Building host packaging tool...' -ForegroundColor Cyan
            $env:GOOS = $oldGOOS
            $env:GOARCH = $oldGOARCH
            $env:CGO_ENABLED = $oldCGOEnabled
            & go build -trimpath -ldflags '-s -w' -o $packerExe $tarPackerSource
            if ($LASTEXITCODE -ne 0) { throw "Failed to build host packaging tool." }
        }

        foreach ($t in $selectedTargets) {
            $tOS = $t.TargetOS
            $tArch = $t.TargetArch
            $binName = $t.Bin
            $packageName = "llama-control-$tOS-$tArch"
            $archiveExt = $t.Ext
            $archiveName = "$packageName.$archiveExt"
            $archivePath = Join-Path $outputRoot $archiveName
            $checksumPath = "$archivePath.sha256"

            Write-Host "Building $tOS/$tArch..." -ForegroundColor Green

            $stageDir = Join-Path ([System.IO.Path]::GetTempPath()) ("llama-control-build-" + [guid]::NewGuid().ToString('N'))
            New-Item -ItemType Directory -Path $stageDir -Force | Out-Null

            try {
                $env:GOOS = $tOS
                $env:GOARCH = $tArch
                $env:CGO_ENABLED = '0'

                $stageBin = Join-Path $stageDir $binName
                & go build -trimpath -ldflags '-s -w' -o $stageBin ./cmd/llama-control
                if ($LASTEXITCODE -ne 0) { throw "Build failed for $tOS/$tArch." }

                # 保留常用平台的原生独立可执行文件到 dist
                if ($tOS -eq 'windows' -and $tArch -eq 'amd64') {
                    Copy-Item -LiteralPath $stageBin -Destination (Join-Path $outputRoot 'llama-control.exe') -Force
                } elseif ($tOS -eq 'darwin' -and $tArch -eq 'arm64') {
                    Copy-Item -LiteralPath $stageBin -Destination (Join-Path $outputRoot 'llama-control-darwin-arm64') -Force
                }

                if (Test-Path -LiteralPath $archivePath) {
                    Remove-Item -LiteralPath $archivePath -Force
                }

                if ($archiveExt -eq 'tar.gz') {
                    & $packerExe $archivePath $stageDir $binName
                    if ($LASTEXITCODE -ne 0) { throw "Build failed to package tar.gz for $tOS/$tArch." }
                } else {
                    Compress-Archive -Path (Join-Path $stageDir '*') -DestinationPath $archivePath -CompressionLevel Optimal
                }

                $hash = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
                Set-Content -LiteralPath $checksumPath -Value "$hash  $archiveName" -Encoding ascii

                $results += [PSCustomObject]@{
                    Target  = "$tOS/$tArch"
                    Archive = $archiveName
                    Size    = "{0:N2} MB" -f ((Get-Item $archivePath).Length / 1MB)
                    SHA256  = $hash.Substring(0, 10) + '...'
                }
            } finally {
                $env:GOOS = $oldGOOS
                $env:GOARCH = $oldGOARCH
                $env:CGO_ENABLED = $oldCGOEnabled
                if (Test-Path -LiteralPath $stageDir) {
                    Remove-Item -LiteralPath $stageDir -Recurse -Force
                }
            }
        }

        Write-Host ''
        Write-Host '=========================================' -ForegroundColor Cyan
        Write-Host '       Build Packages Completed' -ForegroundColor Cyan
        Write-Host '=========================================' -ForegroundColor Cyan
        $results | Format-Table -AutoSize
        Write-Host "Output Directory: $outputRoot"
    } finally {
        if ($packerDir -and (Test-Path -LiteralPath $packerDir)) {
            Remove-Item -LiteralPath $packerDir -Recurse -Force
        }
        Pop-Location
    }
} finally {
    $env:GOOS = $oldGOOS
    $env:GOARCH = $oldGOARCH
    $env:CGO_ENABLED = $oldCGOEnabled
}
