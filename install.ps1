<#
.SYNOPSIS
Installs pvr from GitLab Packages.

.DESCRIPTION
Downloads and installs the latest or specified version of pvr for Windows.

.EXAMPLE
.\install.ps1
Installs the latest stable version of pvr.

.EXAMPLE
$env:PVR_CHANNEL="develop"; .\install.ps1
Installs the latest develop version of pvr.

.EXAMPLE
$env:PVR_VERSION="046"; .\install.ps1
Installs version 046 of pvr.
#>

$ErrorActionPreference = "Stop"

# Ensure TLS 1.2 is used for Invoke-RestMethod/Invoke-WebRequest
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Version = $env:PVR_VERSION
if ([string]::IsNullOrWhiteSpace($Version)) { $Version = "latest" }

$Channel = $env:PVR_CHANNEL
if ([string]::IsNullOrWhiteSpace($Channel)) { $Channel = "stable" }

$GitLabHost = $env:GITLAB_HOST
if ([string]::IsNullOrWhiteSpace($GitLabHost)) { $GitLabHost = "gitlab.com" }

$ProjectId = $env:PVR_PROJECT_ID
if ([string]::IsNullOrWhiteSpace($ProjectId)) { $ProjectId = "pantacor%2Fpvr" }

$InstallDir = $env:PVR_INSTALL_DIR
if ([string]::IsNullOrWhiteSpace($InstallDir)) { $InstallDir = "$env:USERPROFILE\.local\bin" }

function Write-Info($Message) {
    Write-Host "[INFO] " -NoNewline -ForegroundColor Green
    Write-Host $Message
}

function Write-Warn($Message) {
    Write-Host "[WARN] " -NoNewline -ForegroundColor Yellow
    Write-Host $Message
}

function Write-Err($Message) {
    Write-Host "[ERROR] " -NoNewline -ForegroundColor Red
    Write-Host $Message
}

function Get-LatestVersion {
    $url = "https://$GitLabHost/api/v4/projects/$ProjectId/packages?per_page=100&order_by=created_at&sort=desc"
    try {
        $response = Invoke-RestMethod -Uri $url -UseBasicParsing
        
        if ($Channel -eq "stable") {
            $package = $response | Where-Object { $_.version -notmatch "develop" } | Select-Object -First 1
        } else {
            $package = $response | Select-Object -First 1
        }
        
        if ($null -eq $package -or [string]::IsNullOrWhiteSpace($package.version)) {
            Write-Err "Failed to fetch version from GitLab"
            exit 1
        }
        
        return $package.version
    } catch {
        Write-Err "Failed to fetch version from GitLab: $_"
        exit 1
    }
}

$Architecture = $env:PROCESSOR_ARCHITECTURE
if ($Architecture -eq "AMD64" -or $Architecture -eq "ARM64") {
    $arch = "x64"
} elseif ($Architecture -eq "x86") {
    $arch = "x32"
} else {
    Write-Err "Unsupported architecture: $Architecture"
    exit 1
}

Write-Info "Detected OS: windows"
Write-Info "Detected Arch: $arch"
Write-Info "Channel: $Channel"

if ($Version -eq "latest") {
    $Version = Get-LatestVersion
    Write-Info "Latest version: $Version"
}

$os_name = "windows"
$filename = "pvr.${Version}.${os_name}.${arch}.zip"
$url = "https://${GitLabHost}/api/v4/projects/${ProjectId}/packages/generic/pvr/${Version}/${filename}"

Write-Info "Downloading pvr $Version for $os_name/$arch..."
Write-Info "URL: $url"

$tmpDir = Join-Path [System.IO.Path]::GetTempPath() ([System.Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Force -Path $tmpDir | Out-Null

$downloadFile = Join-Path $tmpDir $filename

try {
    Invoke-WebRequest -Uri $url -OutFile $downloadFile -UseBasicParsing
} catch {
    Write-Err "Failed to download pvr from $url"
    Remove-Item -Recurse -Force $tmpDir
    exit 1
}

Write-Info "Extracting pvr..."
try {
    Expand-Archive -Path $downloadFile -DestinationPath $tmpDir -Force
} catch {
    Write-Err "Failed to extract $downloadFile"
    Remove-Item -Recurse -Force $tmpDir
    exit 1
}

$extractedExe = Join-Path $tmpDir "pvr.exe"
if (-not (Test-Path $extractedExe)) {
    $found = Get-ChildItem -Path $tmpDir -Filter "pvr.exe" -Recurse | Select-Object -First 1
    if ($null -ne $found) {
        $extractedExe = $found.FullName
    } else {
        Write-Err "Failed to extract pvr binary (pvr.exe not found)"
        Remove-Item -Recurse -Force $tmpDir
        exit 1
    }
}

if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
}

$targetExe = Join-Path $InstallDir "pvr.exe"
Move-Item -Path $extractedExe -Destination $targetExe -Force

Remove-Item -Recurse -Force $tmpDir

Write-Info "Successfully installed pvr to $targetExe"
Write-Host ""
Write-Info "Add $InstallDir to your PATH if it is not already included"
