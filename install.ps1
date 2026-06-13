<#
.SYNOPSIS
    dnspick installer (Windows / PowerShell)

.DESCRIPTION
    Auto-detects the CPU architecture, downloads the matching prebuilt binary,
    installs it under the user directory and adds it to PATH.

.EXAMPLE
    irm https://raw.githubusercontent.com/palemoky/dnspick/main/install.ps1 | iex

.NOTES
    Optional environment variables:
      DNSPICK_VERSION   version to install (default: latest), e.g. v2.0.0
      DNSPICK_BIN_DIR   install directory (default: %LOCALAPPDATA%\Programs\dnspick)
#>

$ErrorActionPreference = 'Stop'

$Repo = 'palemoky/dnspick'
$App  = 'dnspick'
$Version = if ($env:DNSPICK_VERSION) { $env:DNSPICK_VERSION } else { 'latest' }

function Info($m) { Write-Host "==> $m" -ForegroundColor Blue }
function Ok($m)   { Write-Host "[OK] $m" -ForegroundColor Green }
function Warn($m) { Write-Host "[!] $m"  -ForegroundColor Yellow }
function Die($m)  { Write-Host "[x] $m"  -ForegroundColor Red; exit 1 }

# 1. Detect the CPU architecture
switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { $Arch = 'amd64' }
    'ARM64' { $Arch = 'arm64' }
    'x86'   { Die '32-bit (x86) systems are not supported' }
    default { Die "unsupported architecture: $($env:PROCESSOR_ARCHITECTURE)" }
}

# 2. Build the download URL
$Asset = "$App-windows-$Arch.zip"
$Url = if ($Version -eq 'latest') {
    "https://github.com/$Repo/releases/latest/download/$Asset"
} else {
    "https://github.com/$Repo/releases/download/$Version/$Asset"
}

Info "platform: windows/$Arch, version: $Version"

# 3. Download and extract into a temp directory
$Tmp = Join-Path $env:TEMP ("dnspick-" + [System.Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $Tmp -Force | Out-Null
try {
    $ZipPath = Join-Path $Tmp $Asset
    Info "downloading $Asset ..."
    try {
        Invoke-WebRequest -Uri $Url -OutFile $ZipPath -UseBasicParsing
    } catch {
        Die "download failed: $Url"
    }

    Info "extracting ..."
    Expand-Archive -Path $ZipPath -DestinationPath $Tmp -Force

    # The binary in the archive is named dnspick-windows-<arch>.exe
    $BinSrc = Join-Path $Tmp "$App-windows-$Arch.exe"
    if (-not (Test-Path $BinSrc)) { $BinSrc = Join-Path $Tmp "$App.exe" }
    if (-not (Test-Path $BinSrc)) { Die 'no executable found in the archive' }

    # 4. Choose the install directory
    $BinDir = if ($env:DNSPICK_BIN_DIR) {
        $env:DNSPICK_BIN_DIR
    } else {
        Join-Path $env:LOCALAPPDATA "Programs\$App"
    }
    New-Item -ItemType Directory -Path $BinDir -Force | Out-Null

    $Dest = Join-Path $BinDir "$App.exe"
    Move-Item -Path $BinSrc -Destination $Dest -Force
    Ok "installed to $Dest"

    # 5. Add to the user PATH
    $UserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (($UserPath -split ';') -notcontains $BinDir) {
        [Environment]::SetEnvironmentVariable('Path', "$UserPath;$BinDir", 'User')
        $env:Path = "$env:Path;$BinDir"
        Warn "added $BinDir to the user PATH; restart your terminal to take effect."
    }

    Write-Host "Done!" -ForegroundColor Green -NoNewline
    Write-Host " Run " -NoNewline
    Write-Host "$App --help" -ForegroundColor Blue -NoNewline
    Write-Host " to get started."
}
finally {
    Remove-Item -Path $Tmp -Recurse -Force -ErrorAction SilentlyContinue
}
