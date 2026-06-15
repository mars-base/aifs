# aifs one-line installer for Windows
param([switch]$NoPrompt)

$ErrorActionPreference = "Stop"
$repo = "mars-base/aifs"
$installDir = "$env:LOCALAPPDATA\aifs"

# Detect arch
$arch = if ([Environment]::Is64BitOperatingSystem) { "amd64" } else { "386" }

# Fetch latest release
$release = Invoke-RestMethod -Uri "https://api.github.com/repos/${repo}/releases/latest"
$tag = $release.tag_name
$url = "https://github.com/${repo}/releases/latest/download/aifs-windows-${arch}.exe"

Write-Host "Downloading aifs $tag (windows-${arch})..."
Write-Host "  $url"

New-Item -ItemType Directory -Force -Path $installDir | Out-Null
$target = "$installDir\aifs.exe"

Invoke-WebRequest -Uri $url -OutFile $target -UseBasicParsing

# Add to PATH for current user
$currentPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($currentPath -notlike "*$installDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$currentPath;$installDir", "User")
    $env:Path = "$env:Path;$installDir"
    Write-Host "  Added $installDir to user PATH"
}

Write-Host ""
Write-Host "✓ aifs $tag installed to $target"
Write-Host "  Run: aifs version"
