# SteadIP Windows TUI installer
# Usage:
#   irm https://steadip.com/install-windows.ps1 | iex
#
# Inspect first:
#   irm https://steadip.com/install-windows.ps1 -OutFile install-windows.ps1
#   notepad .\install-windows.ps1
#   powershell -ExecutionPolicy Bypass -File .\install-windows.ps1

$ErrorActionPreference = "Stop"

$AppName = "steadip"
$RepoOwner = "mlowasp"
$RepoName = "steadip"
$Branch = if ($env:STEADIP_BRANCH) { $env:STEADIP_BRANCH } else { "main" }
$BaseUrl = if ($env:STEADIP_BASE_URL) {
    $env:STEADIP_BASE_URL.TrimEnd("/")
} else {
    "https://raw.githubusercontent.com/$RepoOwner/$RepoName/$Branch/cli/steadip-go-cli/dist"
}

$InstallDir = if ($env:STEADIP_INSTALL_DIR) {
    $env:STEADIP_INSTALL_DIR
} else {
    Join-Path $env:LOCALAPPDATA "SteadIP\bin"
}

$BinPath = Join-Path $InstallDir "steadip.exe"

function Write-Info($Message) {
    Write-Host "==> $Message" -ForegroundColor Cyan
}

function Write-Ok($Message) {
    Write-Host "✓ $Message" -ForegroundColor Green
}

function Write-Warn($Message) {
    Write-Host "! $Message" -ForegroundColor Yellow
}

function Get-SteadIPArch {
    $arch = $env:PROCESSOR_ARCHITECTURE

    if ($arch -eq "AMD64") {
        return "amd64"
    }

    if ($arch -eq "ARM64") {
        return "arm64"
    }

    if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() -eq "Arm64") {
        return "arm64"
    }

    throw "Unsupported CPU architecture: $arch"
}

function Add-ToUserPath($Dir) {
    $current = [Environment]::GetEnvironmentVariable("Path", "User")

    if (-not $current) {
        $current = ""
    }

    $parts = $current -split ";" | Where-Object { $_ -ne "" }

    if ($parts -contains $Dir) {
        return $false
    }

    $newPath = if ($current.Trim().Length -gt 0) {
        "$current;$Dir"
    } else {
        $Dir
    }

    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    $env:Path = "$env:Path;$Dir"

    return $true
}

function Main {
    $arch = Get-SteadIPArch
    $binary = "steadip-windows-$arch.exe"
    $url = "$BaseUrl/$binary"

    Write-Info "Installing SteadIP TUI CLI"
    Write-Info "Platform: Windows/$arch"
    Write-Info "Source: $url"
    Write-Info "Install dir: $InstallDir"

    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null

    $tmp = Join-Path $env:TEMP ("steadip-" + [System.Guid]::NewGuid().ToString() + ".exe")

    try {
        Invoke-WebRequest -Uri $url -OutFile $tmp -UseBasicParsing
        Move-Item -Force $tmp $BinPath
    } finally {
        if (Test-Path $tmp) {
            Remove-Item -Force $tmp -ErrorAction SilentlyContinue
        }
    }

    Write-Ok "Installed: $BinPath"

    $added = Add-ToUserPath $InstallDir
    if ($added) {
        Write-Warn "Added $InstallDir to your user PATH."
        Write-Warn "Open a new PowerShell window if 'steadip' is not found immediately."
    }

    try {
        & $BinPath --help *> $null
        Write-Ok "SteadIP CLI is ready."
    } catch {
        Write-Warn "Installed binary, but --help returned a non-zero status."
    }

    Write-Host ""
    Write-Host "Next steps:"
    Write-Host "  steadip login"
    Write-Host "  steadip up"
    Write-Host "  steadip status"
    Write-Host ""
}

Main
