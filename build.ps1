<#
.SYNOPSIS
    GBF-Accelerator Native Go Single-Binary Packager & Release Builder

.DESCRIPTION
    Compiles the native Go proxy engine with embedded Web UI assets and desktop tray
    into a standalone, high-performance single-binary release package.
    Supports Windows native build and macOS (arm64/amd64) cross-compilation.
    Zero Python dependencies required.

.PARAMETER RebuildWeb
    Rebuild the React Web SPA using npm before compiling the Go engine.

.PARAMETER Console
    Build with console window enabled (for debugging). Default is GUI mode (hidden console).

.PARAMETER SkipZip
    Skip creating distribution zip archives in release/.

.PARAMETER Target
    Target platform: "windows", "darwin", or "all". Default is "windows".
#>

param (
    [switch]$RebuildWeb,
    [switch]$Console,
    [switch]$SkipZip,
    [ValidateSet("windows", "darwin", "all")]
    [string]$Target = "windows"
)

$ErrorActionPreference = "Stop"

$RootDir = $PSScriptRoot
$EngineDir = Join-Path $RootDir "engine"
$BinDir = Join-Path $RootDir "bin"
$ReleaseDir = Join-Path $RootDir "release"
$WebDir = Join-Path $RootDir "web"
$UiDistDir = Join-Path $EngineDir "ui\dist"

# 1. Read App Version from engine/config/config.go
$AppVersion = "1.8.0"
$ConfigGo = Join-Path $EngineDir "config\config.go"
if (Test-Path $ConfigGo) {
    $Match = Select-String -Path $ConfigGo -Pattern 'AppVersion\s*=\s*"([^"]+)"'
    if ($Match -and $Match.Matches.Groups.Count -gt 1) {
        $AppVersion = $Match.Matches.Groups[1].Value
    }
}

Write-Host "=================================================================" -ForegroundColor Cyan
Write-Host "   GBF-Accelerator Native Go Release Builder (v$AppVersion)" -ForegroundColor Cyan
Write-Host "=================================================================" -ForegroundColor Cyan

# 2. Terminate running instances to prevent file lock errors
Get-Process -Name "GBF_Accelerator", "gbf-proxy", "gbf_proxy" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue

# 3. Synchronize Web UI assets
if ($RebuildWeb) {
    Write-Host "[*] Rebuilding Web UI assets (npm run build)..." -ForegroundColor Yellow
    Push-Location $WebDir
    try {
        npm.cmd run build
        if ($LASTEXITCODE -ne 0) {
            throw "npm run build failed with exit code $LASTEXITCODE"
        }
    } finally {
        Pop-Location
    }
}

$WebDist = Join-Path $WebDir "dist"
if (-not (Test-Path (Join-Path $WebDist "index.html"))) {
    throw "Web dist directory missing at $WebDist. Run with -RebuildWeb first."
}

Write-Host "[*] Synchronizing embedded web assets into engine/ui/dist..." -ForegroundColor Yellow
if (-not (Test-Path $UiDistDir)) {
    New-Item -ItemType Directory -Path $UiDistDir -Force | Out-Null
}

# Clean existing destination assets
$DestAssets = Join-Path $UiDistDir "assets"
if (Test-Path $DestAssets) {
    Remove-Item -Path $DestAssets -Recurse -Force
}

# Copy web/dist contents to engine/ui/dist
Copy-Item -Path "$WebDist\*" -Destination $UiDistDir -Recurse -Force

# Copy application icons
$IcoSrc = Join-Path $RootDir "gbf_accelerator.ico"
if (Test-Path $IcoSrc) {
    Copy-Item -Path $IcoSrc -Destination (Join-Path $EngineDir "ui\icon.ico") -Force
    Copy-Item -Path $IcoSrc -Destination (Join-Path $UiDistDir "favicon.ico") -Force
}

$AssetCount = (Get-ChildItem -Path $UiDistDir -Recurse).Count
Write-Host "[+] Embedded assets synchronized ($AssetCount files)" -ForegroundColor Green

if (-not (Test-Path $BinDir)) {
    New-Item -ItemType Directory -Path $BinDir -Force | Out-Null
}
if (-not (Test-Path $ReleaseDir)) {
    New-Item -ItemType Directory -Path $ReleaseDir -Force | Out-Null
}

# 4. Compile Windows Binary
function Build-Windows {
    $ExePath = Join-Path $BinDir "GBF_Accelerator.exe"
    $LdFlags = "-s -w"
    if (-not $Console) {
        $LdFlags += " -H=windowsgui"
    }

    Write-Host "[*] Compiling Windows single native binary ($ExePath)..." -ForegroundColor Yellow
    $env:CGO_ENABLED = "0"

    Push-Location $EngineDir
    try {
        go build -ldflags $LdFlags -o $ExePath .
        if ($LASTEXITCODE -ne 0) {
            throw "go build failed with exit code $LASTEXITCODE"
        }
    } finally {
        Pop-Location
    }

    Copy-Item -Path $ExePath -Destination (Join-Path $BinDir "gbf-proxy.exe") -Force
    Copy-Item -Path $ExePath -Destination (Join-Path $BinDir "gbf_proxy.exe") -Force

    $SizeMb = (Get-Item $ExePath).Length / 1MB
    Write-Host ("[+] Windows binary compiled successfully: {0} ({1:F2} MB)" -f $ExePath, $SizeMb) -ForegroundColor Green

    if (-not $SkipZip) {
        $ZipPath = Join-Path $ReleaseDir "GBF_Accelerator_v$($AppVersion)_GUI.zip"
        if (Test-Path $ZipPath) { Remove-Item $ZipPath -Force }

        Write-Host "[*] Packaging Windows release zip: $ZipPath..." -ForegroundColor Yellow
        $AuxFiles = @("SwitchyOmega_GBF.bak", "proxy.pac", "使用说明.txt", "LICENSE")
        $FilesToZip = @($ExePath)
        foreach ($Aux in $AuxFiles) {
            $AuxPath = Join-Path $RootDir $Aux
            if (Test-Path $AuxPath) {
                $FilesToZip += $AuxPath
            }
        }

        Compress-Archive -Path $FilesToZip -DestinationPath $ZipPath -Force
        $ZipSizeMb = (Get-Item $ZipPath).Length / 1MB
        Write-Host ("[***] RELEASE READY: {0} ({1:F2} MB)" -f $ZipPath, $ZipSizeMb) -ForegroundColor Cyan
    }
}

# 5. Compile macOS Binaries
function Build-Darwin ([string]$Arch) {
    $DarwinBin = Join-Path $BinDir "GBF_Accelerator_darwin_$Arch"
    Write-Host "[*] Cross-compiling macOS ($Arch) binary ($DarwinBin)..." -ForegroundColor Yellow

    $env:CGO_ENABLED = "0"
    $env:GOOS = "darwin"
    $env:GOARCH = $Arch

    Push-Location $EngineDir
    try {
        go build -ldflags "-s -w" -o $DarwinBin .
        if ($LASTEXITCODE -ne 0) {
            throw "go build darwin/$Arch failed with exit code $LASTEXITCODE"
        }
    } finally {
        Pop-Location
        $env:GOOS = ""
        $env:GOARCH = ""
    }

    $SizeMb = (Get-Item $DarwinBin).Length / 1MB
    Write-Host ("[+] macOS ({0}) binary compiled successfully: {1} ({2:F2} MB)" -f $Arch, $DarwinBin, $SizeMb) -ForegroundColor Green

    if (-not $SkipZip) {
        $ZipPath = Join-Path $ReleaseDir "GBF_Accelerator_v$($AppVersion)_macOS_$Arch.zip"
        if (Test-Path $ZipPath) { Remove-Item $ZipPath -Force }

        Write-Host "[*] Packaging macOS ($Arch) release zip: $ZipPath..." -ForegroundColor Yellow
        $AuxFiles = @("SwitchyOmega_GBF.bak", "proxy.pac", "install_ca.sh", "start_proxy.sh", "使用说明.txt", "LICENSE")
        $FilesToZip = @($DarwinBin)
        foreach ($Aux in $AuxFiles) {
            $AuxPath = Join-Path $RootDir $Aux
            if (Test-Path $AuxPath) {
                $FilesToZip += $AuxPath
            }
        }

        Compress-Archive -Path $FilesToZip -DestinationPath $ZipPath -Force
        $ZipSizeMb = (Get-Item $ZipPath).Length / 1MB
        Write-Host ("[***] RELEASE READY: {0} ({1:F2} MB)" -f $ZipPath, $ZipSizeMb) -ForegroundColor Cyan
    }
}

if ($Target -eq "windows" -or $Target -eq "all") {
    Build-Windows
}

if ($Target -eq "darwin" -or $Target -eq "all") {
    Build-Darwin "arm64"
    Build-Darwin "amd64"
}

Write-Host "`n[+] All release build steps completed successfully." -ForegroundColor Green
