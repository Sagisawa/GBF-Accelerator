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

# 1. Read App Version from engine/config/config.go (single source of truth).
# Fail hard if the version cannot be extracted, to avoid shipping a mislabeled package.
$AppVersion = ""
$ConfigGo = Join-Path $EngineDir "config\config.go"
if (Test-Path $ConfigGo) {
    $Match = Select-String -Path $ConfigGo -Pattern 'AppVersion\s*=\s*"([^"]+)"'
    if ($Match -and $Match.Matches.Groups.Count -gt 1) {
        $AppVersion = $Match.Matches.Groups[1].Value
    }
}
if ([string]::IsNullOrWhiteSpace($AppVersion)) {
    throw "Failed to extract AppVersion from $ConfigGo. Ensure it contains: const AppVersion = `"X.Y.Z`""
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

    # Compile Windows PE icon resource if windres is available
    $Windres = Get-Command windres.exe -ErrorAction SilentlyContinue
    $RcPath = Join-Path $EngineDir "gbf_accelerator.rc"
    if ($Windres -and (Test-Path $RcPath)) {
        Push-Location $EngineDir
        try {
            & $Windres.Source -i gbf_accelerator.rc -O coff -F pe-x86-64 -o resource_windows_amd64.syso
            if ($LASTEXITCODE -eq 0) {
                Write-Host "[+] Windows PE icon resource compiled (resource_windows_amd64.syso)" -ForegroundColor Green
            }
        } catch {
            Write-Warning "Failed to compile Windows icon resource: $_"
        } finally {
            Pop-Location
        }
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

        $AuxFiles = @("SwitchyOmega_GBF.bak", "proxy.pac", "install_ca.bat", "start_proxy.bat", "LICENSE", "使用说明.txt")
        $FilesToZip = @($ExePath)
        foreach ($Aux in $AuxFiles) {
            $AuxPath = Join-Path $RootDir $Aux
            if (-not (Test-Path $AuxPath)) {
                $AuxPath = Join-Path (Join-Path $EngineDir "res") $Aux
            }
            if (Test-Path $AuxPath) {
                $FilesToZip += $AuxPath
            }
        }
        Get-ChildItem -Path $RootDir -Filter "*.txt" | ForEach-Object { $FilesToZip += $_.FullName }

        Compress-Archive -Path $FilesToZip -DestinationPath $ZipPath -Force
        $ZipSizeMb = (Get-Item $ZipPath).Length / 1MB
        Write-Host ("[***] RELEASE READY: {0} ({1:F2} MB)" -f $ZipPath, $ZipSizeMb) -ForegroundColor Cyan
    }
}

# 5. Compile macOS Binaries
function Build-DarwinArch ([string]$Arch) {
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
}

# Package a single macOS release zip with a stable universal2 asset name so the
# updater's cross-platform matching (which prefers "universal") stays consistent
# across both build scripts. On hosts with `lipo` (macOS), both architectures are
# merged into a true Universal 2 binary; otherwise the arm64 build is shipped.
function Build-Darwin {
    Build-DarwinArch "arm64"
    Build-DarwinArch "amd64"

    if ($SkipZip) { return }

    $ArmBin = Join-Path $BinDir "GBF_Accelerator_darwin_arm64"
    $AmdBin = Join-Path $BinDir "GBF_Accelerator_darwin_amd64"
    $MacBin = $ArmBin

    $Lipo = Get-Command lipo -ErrorAction SilentlyContinue
    if (-not $Lipo) {
        throw "lipo command not found; cannot package macOS universal2 release on this host. Official universal2 release archives must be built on macOS."
    }

    $UniversalBin = Join-Path $BinDir "GBF_Accelerator_darwin_universal"
    Write-Host "[*] Combining universal2 binary via lipo..." -ForegroundColor Yellow
    & lipo -create -output $UniversalBin $ArmBin $AmdBin
    if ($LASTEXITCODE -ne 0) {
        throw "lipo merge failed with exit code $LASTEXITCODE; aborted packaging."
    }
    $MacBin = $UniversalBin

    $ZipPath = Join-Path $ReleaseDir "GBF_Accelerator_v$($AppVersion)_macOS_universal2.zip"
    if (Test-Path $ZipPath) { Remove-Item $ZipPath -Force }

    Write-Host "[*] Constructing macOS .app bundle and packaging: $ZipPath..." -ForegroundColor Yellow
    $StagingDir = Join-Path $ReleaseDir "staging_macos"
    if (Test-Path $StagingDir) { Remove-Item $StagingDir -Recurse -Force }
    $AppDir = Join-Path $StagingDir "GBF_Accelerator.app"
    $ContentsDir = Join-Path $AppDir "Contents"
    $MacOsDir = Join-Path $ContentsDir "MacOS"
    $ResourcesDir = Join-Path $ContentsDir "Resources"
    New-Item -ItemType Directory -Path $MacOsDir -Force | Out-Null
    New-Item -ItemType Directory -Path $ResourcesDir -Force | Out-Null

    Copy-Item -Path $MacBin -Destination (Join-Path $MacOsDir "GBF_Accelerator") -Force
    $IcnsSrc = Join-Path $RootDir "gbf_accelerator.icns"
    if (Test-Path $IcnsSrc) {
        Copy-Item -Path $IcnsSrc -Destination (Join-Path $ResourcesDir "gbf_accelerator.icns") -Force
    }

    $PlistContent = @"
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleDevelopmentRegion</key>
    <string>zh_CN</string>
    <key>CFBundleExecutable</key>
    <string>GBF_Accelerator</string>
    <key>CFBundleIconFile</key>
    <string>gbf_accelerator.icns</string>
    <key>CFBundleIdentifier</key>
    <string>com.sagisawa.gbf-accelerator</string>
    <key>CFBundleInfoDictionaryVersion</key>
    <string>6.0</string>
    <key>CFBundleName</key>
    <string>GBF Accelerator</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleShortVersionString</key>
    <string>$AppVersion</string>
    <key>CFBundleVersion</key>
    <string>$AppVersion</string>
    <key>LSMinimumSystemVersion</key>
    <string>10.15</string>
    <key>NSHighResolutionCapable</key>
    <true/>
</dict>
</plist>
"@
    Set-Content -Path (Join-Path $ContentsDir "Info.plist") -Value $PlistContent -Encoding UTF8

    $AuxFiles = @("SwitchyOmega_GBF.bak", "proxy.pac", "install_ca.sh", "start_proxy.sh", "LICENSE")
    foreach ($Aux in $AuxFiles) {
        $AuxPath = Join-Path $RootDir $Aux
        if (-not (Test-Path $AuxPath)) {
            $AuxPath = Join-Path (Join-Path $EngineDir "res") $Aux
        }
        if (Test-Path $AuxPath) {
            Copy-Item -Path $AuxPath -Destination (Join-Path $StagingDir $Aux) -Force
        }
    }
    Get-ChildItem -Path $RootDir -Filter "*.txt" | ForEach-Object {
        Copy-Item -Path $_.FullName -Destination (Join-Path $StagingDir $_.Name) -Force
    }

    Compress-Archive -Path "$StagingDir\*" -DestinationPath $ZipPath -Force
    Remove-Item $StagingDir -Recurse -Force
    $ZipSizeMb = (Get-Item $ZipPath).Length / 1MB
    Write-Host ("[***] RELEASE READY: {0} ({1:F2} MB)" -f $ZipPath, $ZipSizeMb) -ForegroundColor Cyan
}

if ($Target -eq "windows" -or $Target -eq "all") {
    Build-Windows
}

if ($Target -eq "darwin" -or $Target -eq "all") {
    Build-Darwin
}

Write-Host "`n[+] All release build steps completed successfully." -ForegroundColor Green
