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

$url = "https://github.com/${repo}/releases/latest/download/aifs-windows-${arch}.exe"

# Try to resolve version tag for display (GitHub API may rate-limit; non-fatal).
$tag = ""
try {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/${repo}/releases/latest" -TimeoutSec 10
    $tag = $release.tag_name
} catch {
    $tag = "latest"
}

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

# --- Phase 3: Configure WSL auto-start ---------------------------------
# Ensures the podman API service survives Windows reboots:
#   1. .wslconfig — disable WSL VM idle timeout
#   2. /etc/wsl.conf [boot] — starts podman system service when VM boots
#   3. Scheduled Task — wakes WSL VM at user logon so [boot] fires

Write-Step "Configuring WSL auto-start for podman..."

# --- 3a. .wslconfig (no idle timeout) ---
# By default WSL2 VMs shut down ~8s after the last terminal exits. Disable
# idle timeout so the podman service stays alive across reboots.
# .wslconfig is only read on WSL VM cold-boot. On first install (no
# existing config), shut down the VM so 3b cold-boots with the new config.
# On upgrade, skip the shutdown to avoid killing running containers/mounts.
$wslConfig = @"
[wsl2]
vmIdleTimeout=-1
"@
$wslConfigPath = "$env:USERPROFILE\.wslconfig"
$existingConfig = ""
if (Test-Path $wslConfigPath) {
    $existingConfig = Get-Content $wslConfigPath -Raw -ErrorAction SilentlyContinue
}
$needShutdown = ($existingConfig -notmatch 'vmIdleTimeout=-1')
try {
    $wslConfig | Out-File -FilePath $wslConfigPath -Encoding ascii -Force
    if ($needShutdown) {
        # Shut down so next wsl cold-boots with the new .wslconfig.
        wsl --shutdown 2>$null
        Write-Ok ".wslconfig created (vmIdleTimeout=-1)"
    } else {
        Write-Ok ".wslconfig already configured (vmIdleTimeout=-1), skipped shutdown"
    }
} catch {
    Write-Warn ".wslconfig creation failed: $_"
}

# --- 3b. WSL [boot] configuration ---
# Use -u root to write /etc/wsl.conf (default user does not own /etc).
# This is the first wsl command — it cold-boots the VM, so .wslconfig must
# already exist.
$bootConfig = @"
[boot]
systemd=true
command=podman system service -t 0 tcp://0.0.0.0:2375

[automount]
options = metadata
"@
$bootB64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($bootConfig))
try {
    wsl -d podman-machine-default -u root --exec sh -c "echo $bootB64 | base64 -d > /etc/wsl.conf" 2>$null
    Write-Ok "/etc/wsl.conf [boot] configured"
} catch {
    Write-Warn "WSL /etc/wsl.conf config failed: $_"
}

# --- 3b-restart. Restart WSL VM so wsl.conf takes effect ---
# wsl.conf (including [automount] options=metadata) is only read on VM
# cold-boot. Shut down the VM now so the next wsl command cold-boots with
# the new config. This is required for PostgreSQL initdb to succeed on
# Windows paths (/mnt/c/...) — without metadata, chmod fails and PG exits.
try {
    wsl --shutdown 2>$null
    Write-Ok "WSL VM restarted (wsl.conf applied)"
} catch {
    Write-Warn "wsl --shutdown failed: $_"
}

# --- 3c. Scheduled Task: WSL Wake at logon ---
# WSL VM shuts down when all wsl.exe clients exit (vmIdleTimeout=-1 doesn't
# work in WSL 2.7.8). This task runs a persistent "sleep infinity" at user
# logon to keep one wsl.exe alive, preventing idle shutdown. [boot] already
# starts podman system service, so sleep infinity just acts as a keep-alive.
try {
    $taskName = "WSL Podman Wake"
    Unregister-ScheduledTask -TaskName $taskName -Confirm:$false -ErrorAction SilentlyContinue

    $action    = New-ScheduledTaskAction -Execute "wsl.exe" -Argument '-d podman-machine-default --exec sleep infinity'
    $trigger   = New-ScheduledTaskTrigger -AtLogon -User "$env:USERDOMAIN\$env:USERNAME"
    $principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType S4U -RunLevel Highest
    $settings  = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -MultipleInstances IgnoreNew
    Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
    Write-Ok "Scheduled Task '$taskName' created (persistent WSL keep-alive)"
} catch {
    Write-Warn "Scheduled Task creation failed: $_"
}

# --- 3d. Start podman service in the current session ---
# [boot] only fires on VM cold-boot, not on an already-running VM.
# Start the service explicitly so Phase 4 can pull images.
try {
    Start-Process -WindowStyle Hidden -FilePath wsl -ArgumentList '-d','podman-machine-default','--exec','podman','system','service','-t','0','tcp://0.0.0.0:2375'
    # Wait for the service to be reachable.
    $env:CONTAINER_HOST = "tcp://localhost:2375"
    for ($i = 0; $i -lt 20; $i++) {
        $info = podman info 2>$null
        if ($LASTEXITCODE -eq 0) {
            Write-Ok "Podman service started and reachable"
            break
        }
        Start-Sleep -Milliseconds 500
    }
} catch {
    Write-Warn "Podman service startup failed, images will be pulled on first aifs start"
}

# --- Phase 4: Pull container images ----------------------------------

Write-Step "Pulling aifs container images..."

# podman on Windows needs CONTAINER_HOST to reach the WSL service.
$env:CONTAINER_HOST = "tcp://localhost:2375"

# These are the default image tags from aifs config.
# Failures here are non-fatal -- images will be pulled on first use.
foreach ($img in @(
    "ghcr.io/mars-base/aifs/aifs-pg:18-2.58.0",
    "ghcr.io/mars-base/aifs/aifs-backup:2.58.0",
    "docker.io/library/alpine:3.20"
)) {
    $short = $img.Split("/")[-1]
    podman pull $img
    if ($LASTEXITCODE -eq 0) {
        Write-Ok "$short"
    } else {
        Write-Warn "$short pull failed, will retry on first use"
    }
}

# --- Phase 5: Post-install notes ------------------------------------

Write-Host ""
Write-Host "OK aifs installation complete" -ForegroundColor Green
Write-Host ""
Write-Host "Quick start:"
Write-Host "  aifs version"
Write-Host "  aifs config init --add <instance-name>"
Write-Host "  aifs start -i <instance-name>"
Write-Host "  aifs format -i <instance-name>"
Write-Host "  aifs mount -i <instance-name> Z:"
Write-Host ""
Write-Host "Windows notes:"
Write-Host "  - Mount to a drive letter (Z:, X:, etc.) for session-independent access."
Write-Host "  - Directory mounts require an interactive session (logged-on console)."
Write-Host "  - Podman uses WSL2 as its backend; ensure WSL2 is installed and working."
Write-Host "  - After logging in, wait 1-2 minutes for WSL to start before running aifs."
Write-Host ""
# Check if aifs is accessible in the current session
if (-not (Get-Command aifs -ErrorAction SilentlyContinue)) {
    Write-Host ""
    Write-Host "  ! 'aifs' command is not available in this session." -ForegroundColor Yellow
    Write-Host "    This is normal — the PATH change takes effect in new sessions only." -ForegroundColor Yellow
    Write-Host ""
    Write-Host "  -> Sign out and sign back in, or open a new PowerShell window," -ForegroundColor Cyan
    Write-Host "     then run:  aifs version" -ForegroundColor Cyan
    Write-Host ""
} else {
    Write-Host "  aifs is ready: $(aifs version 2>&1)" -ForegroundColor Green
    Write-Host ""
}
