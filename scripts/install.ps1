# aifs one-line installer for Windows
# Detects and guides installation of all required dependencies:
#   WinFsp (FUSE filesystem), Podman

$ErrorActionPreference = "Stop"
$repo = "mars-base/aifs"
$installDir = "$env:LOCALAPPDATA\aifs"

# --- Helper functions -----------------------------------------------

function Write-Step([string]$msg) {
    Write-Host ""; Write-Host ">> $msg" -ForegroundColor Cyan
}
function Write-Ok([string]$msg) {
    Write-Host "  OK $msg" -ForegroundColor Green
}
function Write-Warn([string]$msg) {
    Write-Host "  ! $msg" -ForegroundColor Yellow
}
function Write-Fail([string]$msg) {
    Write-Host "  X $msg" -ForegroundColor Red
}
function Write-Info([string]$msg) {
    Write-Host "  $msg" -ForegroundColor Gray
}
function Write-Hint([string]$msg) {
    Write-Host "  Run: $msg" -ForegroundColor Gray
}

# --- Dependency probes (no side effects) ----------------------------

function Test-Podman {
    return (Get-Command podman -ErrorAction SilentlyContinue) -ne $null
}

function Test-WinFsp {
    # Check WinFsp service (most reliable indicator it's installed and running)
    $svc = Get-Service -Name "WinFsp.Launcher" -ErrorAction SilentlyContinue
    if ($svc) { return $true }
    # Fallback: check if the WinFsp DLL is present
    $dllPath = "${env:ProgramFiles(x86)}\WinFsp\bin\winfsp-x64.dll"
    if (Test-Path $dllPath) { return $true }
    $dllPath = "${env:ProgramFiles}\WinFsp\bin\winfsp-x64.dll"
    if (Test-Path $dllPath) { return $true }
    return $false
}

# --- Phase 1: Binary download --------------------------------------

Write-Step "Downloading aifs binary..."

# Detect arch
$arch = if ([Environment]::Is64BitOperatingSystem) { "amd64" } else { "386" }

# Fetch latest release
$release = Invoke-RestMethod -Uri "https://api.github.com/repos/${repo}/releases/latest"
$tag = $release.tag_name
$url = "https://github.com/${repo}/releases/latest/download/aifs-windows-${arch}.exe"

Write-Info "  $url"

New-Item -ItemType Directory -Force -Path $installDir | Out-Null
$target = "$installDir\aifs.exe"

Invoke-WebRequest -Uri $url -OutFile $target -UseBasicParsing

# Add to PATH for current user
$currentPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($currentPath -notlike "*$installDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$currentPath;$installDir", "User")
    $env:Path = "$env:Path;$installDir"
    Write-Ok "Added $installDir to user PATH"
}

Write-Ok "aifs $tag installed to $target"

# --- Phase 2: Dependency checks & guided install --------------------

Write-Step "Checking Windows dependencies..."

# --- WinFsp ---
if (Test-WinFsp) {
    Write-Ok "WinFsp found"
} else {
    Write-Warn "WinFsp not found -- required for FUSE filesystem mount"
    Write-Hint "winget install WinFsp.WinFsp"
    Write-Info "  Or download from: https://winfsp.dev/"
    Write-Info "  After installing WinFsp, reboot or start the service:"
    Write-Hint "net start WinFsp.Launcher"
    Write-Fail "Please install WinFsp, then re-run this script."
    exit 1
}

# --- Podman ---
if (Test-Podman) {
    $podmanVer = (podman --version 2>&1) -join ' '
    Write-Ok "Podman found ($podmanVer)"
} else {
    Write-Warn "Podman not found -- installing via winget..."
    winget install RedHat.Podman --accept-source-agreements --accept-package-agreements
    if ($LASTEXITCODE -ne 0) {
        Write-Warn "winget install failed."
        Write-Hint "Install manually: winget install RedHat.Podman"
        Write-Hint "  Or: choco install podman"
        Write-Hint "  Or: https://github.com/containers/podman/releases"
        Write-Fail "Please install Podman and ensure it is on your PATH, then re-run this script."
        exit 1
    }
    Write-Ok "Podman installed -- you may need to restart your shell"
    Write-Info "  Podman on Windows uses WSL2. If not already set up, run:"
    Write-Hint "wsl --install"
}

# --- Phase 3: Post-install notes ------------------------------------

Write-Host ""
Write-Host "OK aifs installation complete" -ForegroundColor Green
Write-Host ""
Write-Host "Quick start:"
Write-Host "  aifs version"
Write-Host "  aifs config init --add <instance-name>"
Write-Host "  aifs start -i <instance-name>"
Write-Host "  aifs format -i <instance-name> --volume <volume-name>"
Write-Host "  aifs mount -i <instance-name> Z:"
Write-Host ""
Write-Host "Windows notes:"
Write-Host "  - Mount to a drive letter (Z:, X:, etc.) for session-independent access."
Write-Host "  - Directory mounts require an interactive session (logged-on console)."
Write-Host "  - Podman uses WSL2 as its backend; ensure WSL2 is installed and working."
Write-Host ""
Write-Host "If 'aifs version' says the command is not found, this shell is using a"
Write-Host "stale PATH from before the install. Open a new terminal, or sign out and"
Write-Host "sign back in, then retry. (aifs was added to your user PATH at:"
Write-Host "  $installDir )"
Write-Host ""
