# agyent Windows PowerShell Installer
# Usage: irm https://raw.githubusercontent.com/khanhbkqt/agyent/main/install.ps1 | iex

$ErrorActionPreference = 'Stop'

$repo = "khanhbkqt/agyent"
$githubUrl = "https://github.com/$repo"
$binaryName = "agyent.exe"

Write-Host "=========================================================" -ForegroundColor Cyan
Write-Host "   ðŸš€ Installing agyent (Autonomous AI Assistant Gateway)" -ForegroundColor Cyan
Write-Host "=========================================================" -ForegroundColor Cyan

# 1. Detect Architecture
$arch = "amd64"
if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") {
    $arch = "arm64"
}
Write-Host "Detected platform: windows-$arch" -ForegroundColor Gray

# 2. Determine Version
$version = $env:AGYENT_VERSION
if (-not $version) {
    Write-Host "Fetching latest release from GitHub..." -ForegroundColor Gray
    try {
        $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -Headers @{ "User-Agent" = "PowerShell" }
        $version = $release.tag_name
    } catch {
        $version = "v1.0.0"
    }
}
if (-not $version.StartsWith("v")) {
    $version = "v$version"
}
Write-Host "Selected version: $version" -ForegroundColor Green

# 3. Download Archive
$archiveName = "agyent-$version-windows-$arch.zip"
$downloadUrl = "$githubUrl/releases/download/$version/$archiveName"
$tempZip = Join-Path $env:TEMP $archiveName
$tempExtract = Join-Path $env:TEMP "agyent_extract_$([Guid]::NewGuid().ToString('N'))"

Write-Host "Downloading $archiveName..." -ForegroundColor Gray
try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    Invoke-WebRequest -Uri $downloadUrl -OutFile $tempZip -UseBasicParsing
} catch {
    Write-Host "âŒ Failed to download release from $downloadUrl" -ForegroundColor Red
    Write-Host "Please check available releases at: $githubUrl/releases" -ForegroundColor Red
    exit 1
}

# 4. Extract
Write-Host "Extracting archive..." -ForegroundColor Gray
Expand-Archive -Path $tempZip -DestinationPath $tempExtract -Force

# 5. Install to ~/.agyent/bin
$installDir = Join-Path $HOME ".agyent\bin"
if (-not (Test-Path $installDir)) {
    New-Item -ItemType Directory -Path $installDir -Force | Out-Null
}

$extractedBinary = Join-Path $tempExtract $binaryName
if (-not (Test-Path $extractedBinary)) {
    # Check in subdirectories if zipped with folder
    $found = Get-ChildItem -Path $tempExtract -Filter $binaryName -Recurse | Select-Object -First 1
    if ($found) {
        $extractedBinary = $found.FullName
    }
}

# Stop running instance if currently active
Get-Process -Name "agyent" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 300

$destFile = Join-Path $installDir $binaryName
if (Test-Path $destFile) {
    try {
        Remove-Item -Path $destFile -Force -ErrorAction Stop
    } catch {
        $oldFile = "$destFile.old.$([Guid]::NewGuid().ToString('N').Substring(0,8))"
        Move-Item -Path $destFile -Destination $oldFile -Force -ErrorAction SilentlyContinue
    }
}
Copy-Item -Path $extractedBinary -Destination $destFile -Force

# Cleanup temp files
Remove-Item -Path $tempZip -Force -ErrorAction SilentlyContinue
Remove-Item -Path $tempExtract -Recurse -Force -ErrorAction SilentlyContinue

# 6. Add to PATH if not already present
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$installDir*") {
    Write-Host "Adding $installDir to User PATH..." -ForegroundColor Yellow
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$installDir", "User")
    $env:Path = "$env:Path;$installDir"
}

# 7. Verification
Write-Host ""
Write-Host "Verifying installation..." -ForegroundColor Gray
$installedExe = Join-Path $installDir $binaryName
if (Test-Path $installedExe) {
    & $installedExe version
}

Write-Host ""
Write-Host "=========================================================" -ForegroundColor Green
Write-Host "   ðŸŽ‰ agyent has been successfully installed!" -ForegroundColor Green
Write-Host "=========================================================" -ForegroundColor Green
Write-Host ""
Write-Host "Next steps:" -ForegroundColor White
Write-Host "  1. Run the setup wizard:    agyent init" -ForegroundColor Yellow
Write-Host "  2. Register bot commands:  agyent register-commands" -ForegroundColor Yellow
Write-Host "  3. Start the gateway:      agyent run" -ForegroundColor Yellow
Write-Host ""
