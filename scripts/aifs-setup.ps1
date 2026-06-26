# aifs-setup.ps1 -- Check virtualization, install WSL2 + podman CLI for aifs on Windows
# Usage: powershell -ExecutionPolicy Bypass -File aifs-setup.ps1 [-Proxy <url>|none]
#
# Steps:
#   1. Check CPU virtualization (VT-x/AMD-V, SLAT, VMM)
#   2. Install WSL2 if not present (requires admin + reboot)
#   3. Install Windows podman CLI via winget / MSI
#   4. Install WinFsp (runtime dependency for `aifs mount`)
#
# Note: aifs does NOT use `podman machine init/start`. Instead it starts the
# WSL podman API service (`podman system service -t 0 tcp://0.0.0.0:2375`)
# on first `aifs start` and connects via CONTAINER_HOST=tcp://localhost:2375.
#
# Download from: http://10.241.21.97:1357/aifs-setup.ps1

# Accept proxy via:
#   - powershell -File aifs-setup.ps1 -Proxy <url>     (parsed from $args)
#   - $env:AI_FS_PROXY = "<url>" then irm ... | iex    (for the pipe form)
#   - reuses $env:HTTPS_PROXY if already set
# "none" disables proxy explicitly.
$Proxy = ""
if ($env:AI_FS_PROXY) { $Proxy = $env:AI_FS_PROXY }
if (-not $Proxy -and $env:HTTPS_PROXY) { $Proxy = $env:HTTPS_PROXY }
# Parse -Proxy <url> from $args (works under `powershell -File ... -Proxy ...`)
for ($i = 0; $i -lt $args.Count; $i++) {
    if ("$($args[$i])" -ieq "-Proxy" -and ($i + 1) -lt $args.Count) {
        $Proxy = "$($args[$i + 1])"; $i++
    }
}

$useProxy = ($Proxy -and ($Proxy -ne "none"))

$ErrorActionPreference = "Continue"

Write-Host ""
Write-Host "========================================"
Write-Host "  AIFS Setup - WSL2 + Podman Installer"
Write-Host "========================================"
Write-Host ""
Write-Host "Note: enabling WSL2 may require a reboot. If the script says"
Write-Host "'REBOOT REQUIRED', restart the machine and re-run the SAME"
Write-Host "command -- it will resume from where it left off."
Write-Host ""

# --- Helper: read one CPU property via CIM --------------
# Note: Win11 24H2 / LTSC 2024 removed wmic.exe by default, so we use
# Get-CimInstance Win32_Processor (the modern WMI replacement) instead.
# CIM returns booleans as "True"/"False" (title case), unlike wmic's
# "TRUE"/"FALSE"; callers compare case-insensitively.

$script:cpuInfo = $null
function Get-CpuInfo {
    if ($null -ne $script:cpuInfo) { return $script:cpuInfo }
    $script:cpuInfo = Get-CimInstance Win32_Processor -ErrorAction SilentlyContinue
    return $script:cpuInfo
}

function ReadWmic {
    param([string]$prop)
    $cpu = Get-CpuInfo
    if (-not $cpu) { return "UNKNOWN" }
    $val = $cpu.$prop
    if ($null -eq $val) { return "UNKNOWN" }
    return "$val".Trim()
}

# --- Helper: check if an exe exists ---------------------

function CmdExists {
    param([string]$name)
    $sysRoot = $env:SystemRoot
    if (-not $sysRoot) { $sysRoot = "C:\Windows" }
    $sys32 = Join-Path $sysRoot "System32"
    if (Test-Path (Join-Path $sys32 "$name.exe")) { return $true }
    $sysWow64 = Join-Path $sysRoot "SysWOW64"
    if (Test-Path (Join-Path $sysWow64 "$name.exe")) { return $true }
    $candidates = @(
        (Join-Path $env:ProgramFiles "$name\bin\$name.exe"),
        (Join-Path ${env:ProgramFiles(x86)} "$name\bin\$name.exe"),
        (Join-Path $env:LOCALAPPDATA "$name\bin\$name.exe")
    )
    foreach ($c in $candidates) {
        if (Test-Path $c) { return $true }
    }
    $found = $null
    try { $found = Get-Command $name -ErrorAction SilentlyContinue } catch {}
    return ($null -ne $found)
}

# --- Helper: refresh PATH in current session ------------

function Refresh-Path {
    $machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
    $userPath    = [Environment]::GetEnvironmentVariable("Path", "User")
    $env:Path    = "$machinePath;$userPath"
}

# --- Helper: print step result --------------------------

function Print-Result {
    param([string]$label, [string]$status, [string]$detail)
    $icon = if ($status -eq "ok")   { "[OK]" }
            elseif ($status -eq "skip") { "[SKIP]" }
            elseif ($status -eq "warn") { "[WARN]" }
            else { "[FAIL]" }
    Write-Host "  $icon $label"
    if ($detail) { Write-Host "         $detail" }
}

# --- Helper: pause for a keypress in interactive sessions ---
# In an interactive console (e.g. `irm | iex` run by a human), wait for a
# keypress so the user can read the message before the window closes. In a
# non-interactive context (WinRM, scheduled task S4U, CI), return immediately
# -- blocking on Read-Host there would hang forever.
function Pause-IfInteractive {
    param([string]$prompt = "Press any key to continue...")
    $interactive = $false
    try {
        $interactive = [Environment]::UserInteractive -and ($Host.Name -eq "ConsoleHost")
    } catch { $interactive = $false }
    if (-not $interactive) { return }
    Write-Host ""
    Write-Host $prompt -NoNewline
    try { [void][System.Console]::ReadKey($true) } catch { [void](Read-Host) }
    Write-Host ""
}

# --- Helper: invoke wsl.exe safely ----------------------
# Start-Process -FilePath "wsl.exe" can throw a terminating
# InvalidOperationException ("system cannot find the file specified") when the
# inbox wsl.exe stub is present but not resolvable in the current context
# (e.g. scheduled task S4U, or features enabled but kernel not yet installed).
# That terminating error would abort the whole script. Resolve the full path
# via Get-Command first, and wrap the call in try/catch so a wsl invocation
# failure is reported as a warning rather than crashing setup.
function Invoke-Wsl {
    param([Parameter(Mandatory)][string[]]$WslArgs)
    $exe = (Get-Command wsl.exe -ErrorAction SilentlyContinue).Source
    if (-not $exe) {
        Write-Host "  wsl.exe not found on PATH; skipping wsl invocation."
        return $null
    }
    try {
        $p = Start-Process -FilePath $exe -ArgumentList $WslArgs -Wait -PassThru -NoNewWindow `
            -RedirectStandardOutput "$env:TEMP\wsl.out" -RedirectStandardError "$env:TEMP\wsl.err"
        return $p.ExitCode
    } catch {
        Write-Host "  wsl.exe invocation failed: $($_.Exception.Message)"
        return $null
    }
}

# ===========================================================
# PHASE 1: Check CPU virtualization
# ===========================================================

Write-Host "[Phase 1] Checking CPU virtualization..."
Write-Host "-----------------------------------"

$cpuName = ReadWmic "Name"
$virtFW  = ReadWmic "VirtualizationFirmwareEnabled"
$slat    = ReadWmic "SecondLevelAddressTranslationExtensions"
$vmm     = ReadWmic "VmMonitorModeExtensions"

Write-Host "  CPU          : $cpuName"
Write-Host "  VT-x / AMD-V : $virtFW"
Write-Host "  SLAT (EPT)   : $slat"
Write-Host "  VMM Monitor  : $vmm"
Write-Host ""

# CIM returns "True"/"False", legacy wmic returned "TRUE"/"FALSE"; compare case-insensitively.
$cpuOk = ($virtFW -ieq "TRUE") -and ($slat -ieq "TRUE") -and ($vmm -ieq "TRUE")

if ($cpuOk) {
    Print-Result "CPU supports virtualization" "ok"
} else {
    Print-Result "wmic reports no virtualization flags" "warn" "May be a nested VM; cross-checking..."

    # Cross-check 1: systeminfo reports an active hypervisor
    $sysInfo = cmd.exe /c "systeminfo /fo csv" 2>&1
    $sysInfoStr = ($sysInfo | Out-String).Trim()
    if (($sysInfoStr -match "hypervisor has been detected")) {
        Print-Result "systeminfo reports active hypervisor" "ok"
        $cpuOk = $true
    }

    # Cross-check 2: wsl --status actually works
    if (-not $cpuOk -and (CmdExists "wsl")) {
        $probeRc = Invoke-Wsl -WslArgs "--status"
        if ($probeRc -eq 0) {
            Print-Result "wsl --status succeeded" "ok" "WSL2 is functional"
            $cpuOk = $true
        }
    }

    if (-not $cpuOk) {
        Print-Result "CPU lacks virtualization" "fail"
        Write-Host ""
        Write-Host "  This machine cannot run WSL2/podman."
        Write-Host "  Virtualization must be enabled in BIOS,"
        Write-Host "  or this may be a nested VM without passthrough."
        Write-Host ""
        Write-Host "Setup aborted."
        exit 1
    }
}
Write-Host ""

# ===========================================================
# PHASE 2: Install WSL2
# ===========================================================

Write-Host "[Phase 2] WSL2 setup"
Write-Host "-----------------------------------"

# Check if wsl.exe exists AND actually works (inbox stub may exist but be non-functional)
$wslExists = CmdExists "wsl"
$wslWorking = $false

if ($wslExists) {
    # Probe: does wsl actually work? (inbox stub returns help text for everything)
    $probeRc = Invoke-Wsl -WslArgs "--status"
    if ($probeRc -eq 0) {
        $wslWorking = $true
        Print-Result "WSL already installed and working" "skip"
    } else {
        Write-Host "  wsl.exe exists but is not functional (inbox stub or features disabled)"
        Print-Result "WSL stub detected, enabling features" "warn"
    }
}

if (-not $wslWorking) {
    # Set proxy for WSL download (configurable, default to internal proxy)
    if ($useProxy) {
        $env:HTTPS_PROXY = $Proxy
        $env:HTTP_PROXY  = $Proxy
        Write-Host "  Using proxy: $Proxy"
    } else {
        Write-Host "  No proxy configured"
    }

    # Step 1: Enable Windows features via dism (works for both fresh and stub cases)
    Write-Host "  Enabling WSL features via dism..."

    # dism exit codes: 0 = success, 3010 = success but reboot required
    $dism1 = Start-Process -FilePath "dism.exe" `
        -ArgumentList "/online","/enable-feature","/featurename:Microsoft-Windows-Subsystem-Linux","/all","/norestart" `
        -Wait -PassThru -NoNewWindow

    $needReboot = $false
    if ($dism1.ExitCode -eq 0 -or $dism1.ExitCode -eq 3010) {
        if ($dism1.ExitCode -eq 3010) { $needReboot = $true }
        Print-Result "dism: Microsoft-Windows-Subsystem-Linux" "ok"
    } else {
        Print-Result "dism: Microsoft-Windows-Subsystem-Linux" "fail" "Exit code: $($dism1.ExitCode)"
    }

    $dism2 = Start-Process -FilePath "dism.exe" `
        -ArgumentList "/online","/enable-feature","/featurename:VirtualMachinePlatform","/all","/norestart" `
        -Wait -PassThru -NoNewWindow

    if ($dism2.ExitCode -eq 0 -or $dism2.ExitCode -eq 3010) {
        if ($dism2.ExitCode -eq 3010) { $needReboot = $true }
        Print-Result "dism: VirtualMachinePlatform" "ok"
    } else {
        Print-Result "dism: VirtualMachinePlatform" "fail" "Exit code: $($dism2.ExitCode)"
    }

    if ($dism1.ExitCode -notin @(0, 3010) -or $dism2.ExitCode -notin @(0, 3010)) {
        Write-Host ""
        Write-Host "  WSL feature enable failed. Possible causes:"
        Write-Host "    - Not running as Administrator"
        Write-Host "    - Windows version too old (need Win 11 or Win 10 21H2+)"
        Write-Host ""
        Write-Host "  Please reboot and re-run this script."
        exit 1
    }

    if ($needReboot) {
        Write-Host ""
        Write-Host "  ============================================================"
        Write-Host "    REBOOT REQUIRED to activate WSL features (dism: 3010)"
        Write-Host "    This is normal -- not an error."
        Write-Host ""
        Write-Host "    Next steps:"
        Write-Host "      1. Restart this machine."
        Write-Host "      2. Re-run the SAME install command you just ran."
        Write-Host "         The script will resume from where it left off."
        Write-Host "  ============================================================"
        Print-Result "Reboot required" "warn" "dism returned 3010 -- reboot then re-run"
        # A required reboot is a normal setup step, not a failure: exit 0 (not 1)
        # so `irm | iex` does not abort with an error. Pause in an interactive
        # session so the user can read this before the window closes; in
        # non-interactive contexts (WinRM/scheduled task) just return.
        Pause-IfInteractive "Press any key to exit, then reboot and re-run the install command..."
        exit 0
    }

    # Step 2: Try wsl --install --no-distribution (may upgrade inbox stub to Store version)
    Write-Host ""
    Write-Host "  Running: wsl --install --no-distribution"
    $wslInstallRc = Invoke-Wsl -WslArgs "--install","--no-distribution"

    if ($wslInstallRc -eq 0) {
        Print-Result "wsl --install --no-distribution" "ok"
    } elseif ($null -eq $wslInstallRc) {
        Print-Result "wsl --install" "warn" "wsl.exe could not be launched (stub not active yet?) -- retry after reboot"
    } else {
        Print-Result "wsl --install" "warn" "Exit code: $wslInstallRc (may be ok after reboot)"
    }

    # Refresh PATH
    Refresh-Path
}

# Set WSL2 as default version and update kernel (runs whether freshly installed or pre-existing)
if (CmdExists "wsl") {
    $setDefaultRc = Invoke-Wsl -WslArgs "--set-default-version","2"
    if ($setDefaultRc -eq 0) {
        Print-Result "WSL default version 2" "ok"
    } elseif ($null -eq $setDefaultRc) {
        Print-Result "WSL set-default-version" "warn" "wsl.exe not launchable -- retry after reboot"
    } else {
        Print-Result "WSL set-default-version" "warn" "Exit code: $setDefaultRc -- reboot may be needed"
    }

    $updateKernelRc = Invoke-Wsl -WslArgs "--update"
    if ($updateKernelRc -eq 0) {
        Print-Result "WSL kernel updated" "ok"
    } elseif ($null -eq $updateKernelRc) {
        Print-Result "WSL update" "warn" "wsl.exe not launchable -- retry after reboot"
    } else {
        Print-Result "WSL update" "warn" "Exit code: $updateKernelRc -- reboot may be needed"
        Write-Host ""
        Write-Host "  If podman machine init later fails with a WSL2 kernel error,"
        Write-Host "  manually download and install the WSL2 Linux kernel update package:"
        Write-Host ""
        Write-Host "    URL: https://wslstorestorage.blob.core.windows.net/wslblob/wsl_update_x64.msi"
        Write-Host ""
        Write-Host "  After installing the MSI, reboot the machine, then re-run this script."
        Write-Host ""
    }
}

# Verify WSL is functional before proceeding -- if it isn't (e.g. kernel
# update missing on Windows 10), stop here with a clear message rather
# than letting "podman machine init" fail with a cryptic WSL import error.
if (CmdExists "wsl") {
    Write-Host "  Verifying WSL is functional..."
    $wslReadyRc = Invoke-Wsl -WslArgs "--status"
    if ($wslReadyRc -ne 0) {
        Print-Result "WSL not ready" "fail"
        Write-Host ""
        Write-Host "  ============================================================"
        Write-Host "    WSL2 IS NOT READY -- cannot proceed to Podman setup"
        Write-Host "  ============================================================"
        Write-Host ""
        Write-Host "  On Windows 10, WSL2 requires a separate Linux kernel update"
        Write-Host "  package that must be installed manually."
        Write-Host ""
        Write-Host "  Steps to fix:"
        Write-Host "    1. Download the kernel update package:"
        Write-Host "         https://wslstorestorage.blob.core.windows.net/wslblob/wsl_update_x64.msi"
        Write-Host "    2. Run the MSI to install it."
        Write-Host "    3. Reboot the machine."
        Write-Host "    4. Re-run the same install command -- setup will resume from here."
        Write-Host ""
        Pause-IfInteractive "Press any key to exit, then install the kernel package, reboot, and re-run..."
        exit 1
    }
    Print-Result "WSL is functional" "ok"
}
Write-Host ""

# ===========================================================
# PHASE 3: Install Podman
# ===========================================================

Write-Host "[Phase 3] Podman setup"
Write-Host "-----------------------------------"

# Refresh PATH in case podman was installed in a previous run
Refresh-Path

$podmanInstalled = CmdExists "podman"

if ($podmanInstalled) {
    $podmanVer = cmd.exe /c "chcp 437 >nul & podman --version" 2>$null
    Print-Result "Podman already installed" "skip" $podmanVer
} else {
    $installed = $false

    # Strategy 1: winget
    if (CmdExists "winget") {
        Write-Host "  Installing podman via winget..."
        Write-Host "  Running: winget install RedHat.Podman --accept-source-agreements --accept-package-agreements"
        Write-Host ""

        $wingetProc = Start-Process -FilePath "winget" `
            -ArgumentList "install","RedHat.Podman","--accept-source-agreements","--accept-package-agreements" `
            -Wait -PassThru -NoNewWindow

        if ($wingetProc.ExitCode -eq 0) {
            Print-Result "Podman installed via winget" "ok"
            $installed = $true
        } else {
            Print-Result "winget install failed" "warn" "Exit code: $($wingetProc.ExitCode), trying direct download..."
        }
    } else {
        Print-Result "winget not available" "warn" "Falling back to direct download"
    }

    # Strategy 2: Direct MSI download from GitHub
    if (-not $installed) {
        $podmanVersion = "5.8.3"
        $msiUrl = "https://github.com/podman-container-tools/podman/releases/download/v${podmanVersion}/podman-installer-windows-amd64.msi"
        $tmpDir = Join-Path $env:TEMP ("aifs-setup-" + [System.IO.Path]::GetRandomFileName().Split('.')[0])
        if (-not (Test-Path $tmpDir)) { New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null }
        $installerPath = Join-Path $tmpDir "podman.msi"

        Write-Host "  Downloading podman v${podmanVersion} installer..."
        Write-Host "  URL: $msiUrl"
        Write-Host ""

        $downloadOk = $false

        # Attempt 1: NET.WebClient with proxy (handles 302 redirects properly)
        if ($useProxy) {
            try {
                Write-Host "  Trying via proxy $Proxy ..."
                $wc = New-Object System.Net.WebClient
                $wc.Proxy = New-Object System.Net.WebProxy($Proxy, $true)
                $wc.DownloadFile($msiUrl, $installerPath)
                if ((Test-Path $installerPath) -and ((Get-Item $installerPath).Length -gt 1MB)) {
                    $downloadOk = $true
                    Print-Result "Downloaded via proxy ($('{0:N0}' -f (Get-Item $installerPath).Length) bytes)" "ok"
                } else {
                    Write-Host "  Downloaded file too small or missing"
                }
            } catch {
                Write-Host "  Proxy attempt failed: $($_.Exception.Message)"
            }
        }

        # Attempt 2: NET.WebClient direct (no proxy)
        if (-not $downloadOk) {
            try {
                Write-Host "  Trying direct download..."
                $wc = New-Object System.Net.WebClient
                $wc.Proxy = [System.Net.GlobalProxySelection]::GetEmptyWebProxy()
                $wc.DownloadFile($msiUrl, $installerPath)
                if ((Test-Path $installerPath) -and ((Get-Item $installerPath).Length -gt 1MB)) {
                    $downloadOk = $true
                    Print-Result "Downloaded direct ($('{0:N0}' -f (Get-Item $installerPath).Length) bytes)" "ok"
                }
            } catch {
                Write-Host "  Direct attempt failed: $($_.Exception.Message)"
            }
        }

        if (-not $downloadOk) {
            Print-Result "Download failed" "fail"
            Write-Host ""
            Write-Host "  Download podman manually from https://podman.io"
            Write-Host "  Or install App Installer for winget: https://aka.ms/getwinget"
            exit 1
        }

        # Silent install via msiexec (no UAC elevation needed)
        Write-Host "  Running silent install (msiexec)..."
        $msiLog = Join-Path $tmpDir "install.log"
        $installProc = Start-Process -FilePath "msiexec.exe" `
            -ArgumentList "/i",$installerPath,"/qn","/norestart","/l*v",$msiLog `
            -Wait -PassThru -NoNewWindow

        if ($installProc.ExitCode -ne 0) {
            Print-Result "Podman installer" "fail" "Exit code: $($installProc.ExitCode)"
            Write-Host "  Install log tail:"
            if (Test-Path $msiLog) {
                Get-Content $msiLog -Tail 10 | ForEach-Object { Write-Host "    $_" }
            }
            Write-Host "  Try running manually: msiexec /i $installerPath"
            exit 1
        }

        Print-Result "Podman installed via MSI" "ok"

        # Cleanup
        Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
    }

    # Refresh PATH
    Refresh-Path

    if (-not (CmdExists "podman")) {
        Print-Result "podman not found on PATH after install" "warn"
        Write-Host "  You may need to open a new terminal."
    } else {
        $podmanVer = cmd.exe /c "chcp 437 >nul & podman --version" 2>$null
        Print-Result "Podman version" "ok" $podmanVer
    }
}

# --- Podman machine (WSL distro) ------------------------------------
# aifs does NOT use `podman machine start`; it talks to the podman API
# service directly inside the machine's WSL distro (podman-machine-default).
# But that distro must exist first -- `podman machine init` creates it.
# This is idempotent: skipped if a machine already exists.
if (CmdExists "podman") {
    Write-Host "  Ensuring podman machine exists..."
    $mlOut = cmd.exe /c "chcp 437 >nul & podman machine list --format {{.Name}}" 2>$null
    $hasMachine = $false
    foreach ($line in ($mlOut -split "`n")) {
        if ($line.Trim() -ne "") { $hasMachine = $true; break }
    }
    if ($hasMachine) {
        Print-Result "Podman machine exists" "skip"
    } else {
        Write-Host "  Running: podman machine init"
        $initProc = Start-Process -FilePath "podman" `
            -ArgumentList "machine","init" `
            -Wait -PassThru -NoNewWindow
        if ($initProc.ExitCode -eq 0) {
            Print-Result "Podman machine initialized" "ok"
        } else {
            Print-Result "podman machine init failed" "warn" "Exit code: $($initProc.ExitCode)"
            Write-Host "  aifs start will fail until a machine exists. Try manually:"
            Write-Host "    podman machine init"
        }
    }
}
Write-Host ""

function WinFspInstalled {
    $keys = @(
        "HKLM:\SOFTWARE\WOW6432Node\WinFsp",
        "HKLM:\SOFTWARE\WinFsp"
    )
    foreach ($k in $keys) {
        if (Test-Path $k) { return $true }
    }
    # Also look for the main DLL in common install locations.
    $candidates = @(
        (Join-Path $env:ProgramFiles "WinFsp\bin\winfsp-x64.dll"),
        (Join-Path ${env:ProgramFiles(x86)} "WinFsp\bin\winfsp-x64.dll")
    )
    foreach ($c in $candidates) {
        if (Test-Path $c) { return $true }
    }
    return $false
}

# ===========================================================
# PHASE 4: Install WinFsp (runtime dependency for aifs mount)
# ===========================================================

Write-Host "[Phase 4] WinFsp (FUSE runtime)"
Write-Host "-----------------------------------"

if (WinFspInstalled) {
    Print-Result "WinFsp installed" "skip"
} else {
    Write-Host "  WinFsp is required for 'aifs mount' on Windows."
    $installed = $false

    if (CmdExists "winget") {
        Write-Host "  Installing WinFsp via winget..."
        $wingetProc = Start-Process -FilePath "winget" `
            -ArgumentList "install","WinFsp.WinFsp","--accept-source-agreements","--accept-package-agreements","--silent" `
            -Wait -PassThru -NoNewWindow
        if ($wingetProc.ExitCode -eq 0) {
            Print-Result "WinFsp installed via winget" "ok"
            $installed = $true
        } else {
            Print-Result "winget install WinFsp" "warn" "Exit code: $($wingetProc.ExitCode)"
        }
    }

    if (-not $installed -and (CmdExists "choco")) {
        Write-Host "  Installing WinFsp via Chocolatey..."
        $chocoProc = Start-Process -FilePath "choco" `
            -ArgumentList "install","winfsp","-y" `
            -Wait -PassThru -NoNewWindow
        if ($chocoProc.ExitCode -eq 0) {
            Print-Result "WinFsp installed via Chocolatey" "ok"
            $installed = $true
        } else {
            Print-Result "choco install winfsp" "warn" "Exit code: $($chocoProc.ExitCode)"
        }
    }

    # Fallback: download MSI from GitHub releases and install silently
    if (-not $installed) {
        $msiUrl = "https://github.com/winfsp/winfsp/releases/download/v2.1/winfsp-2.1.25156.msi"
        Write-Host "  Trying WinFsp MSI from GitHub releases..."
        Write-Host "  URL: $msiUrl"
        try {
            $tmpDir = Join-Path $env:TEMP ("winfsp-" + [System.IO.Path]::GetRandomFileName().Split('.')[0])
            New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null
            $msiPath = Join-Path $tmpDir "winfsp.msi"

            $dlOk = $false
            # Try with proxy first
            if ($useProxy) {
                try {
                    $wc = New-Object System.Net.WebClient
                    $wc.Proxy = New-Object System.Net.WebProxy($Proxy, $true)
                    $wc.DownloadFile($msiUrl, $msiPath)
                    if ((Test-Path $msiPath) -and ((Get-Item $msiPath).Length -gt 500KB)) {
                        $dlOk = $true
                    }
                } catch {
                    Write-Host "  Proxy download failed: $($_.Exception.Message)"
                }
            }
            # Try direct download
            if (-not $dlOk) {
                try {
                    $wc = New-Object System.Net.WebClient
                    $wc.Proxy = [System.Net.GlobalProxySelection]::GetEmptyWebProxy()
                    $wc.DownloadFile($msiUrl, $msiPath)
                    if ((Test-Path $msiPath) -and ((Get-Item $msiPath).Length -gt 500KB)) {
                        $dlOk = $true
                    }
                } catch {
                    Write-Host "  Direct download failed: $($_.Exception.Message)"
                }
            }

            if ($dlOk) {
                Write-Host "  Running msiexec /i winfsp.msi /qn /norestart..."
                $msiProc = Start-Process -FilePath "msiexec.exe" `
                    -ArgumentList "/i",$msiPath,"/qn","/norestart" `
                    -Wait -PassThru -NoNewWindow
                if ($msiProc.ExitCode -eq 0) {
                    Print-Result "WinFsp installed via MSI" "ok"
                    $installed = $true
                } else {
                    Print-Result "WinFsp MSI install" "warn" "msiexec exit code: $($msiProc.ExitCode)"
                }
            } else {
                Print-Result "WinFsp MSI download" "warn" "Could not download from GitHub"
            }
            Remove-Item $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
        } catch {
            Print-Result "WinFsp MSI fallback" "warn" "$($_.Exception.Message)"
        }
    }

    if (-not $installed) {
        Print-Result "WinFsp not installed" "warn"
        Write-Host ""
        Write-Host "  aifs mount requires WinFsp. Install it manually from:"
        Write-Host "    https://winfsp.dev/rel/"
        Write-Host "  Or run one of the following in an elevated terminal:"
        Write-Host "    winget install WinFsp.WinFsp"
        Write-Host "    choco install winfsp"
        Write-Host ""
    }
}
Write-Host ""

# ===========================================================
# PHASE 5: Verify podman CLI (no podman machine needed)
# ===========================================================

Write-Host "[Phase 5] Podman CLI verification"
Write-Host "-----------------------------------"

if (-not (CmdExists "podman")) {
    Print-Result "Podman not available" "fail"
    Write-Host ""
    Write-Host "  Please open a new terminal and verify:"
    Write-Host "    podman --version"
    Write-Host ""
    exit 1
}

$podmanVer = cmd.exe /c "chcp 437 >nul & podman --version" 2>$null
Print-Result "Podman CLI available" "ok" $podmanVer

Write-Host ""
Write-Host "  Note: On first 'aifs start' it will start the WSL podman API service"
Write-Host "        and set CONTAINER_HOST=tcp://localhost:2375 automatically."
Write-Host ""
Write-Host ""

# ===========================================================
# Summary
# ===========================================================

Write-Host "========================================"
Write-Host "  Setup Complete"
Write-Host "========================================"
Write-Host ""

# Quick verification
$wslOk    = CmdExists "wsl"
$pmOk     = CmdExists "podman"
$winfspOk = WinFspInstalled

if ($wslOk -and $pmOk) {
    $podmanVer = cmd.exe /c "chcp 437 >nul & podman --version" 2>$null
    Write-Host "  WSL2   : installed"
    Write-Host "  Podman : $podmanVer"
    $winfspStatus = if ($winfspOk) { 'installed' } else { 'MISSING (required for aifs mount)' }
    Write-Host "  WinFsp : $winfspStatus"
    Write-Host ""
    Write-Host "  Next step: install aifs, then:"
    Write-Host ""
    Write-Host "    # 1. Initialize config (dedicated data dir recommended)"
    Write-Host "    aifs config init --add myproject --base-dir D:\aifs"
    Write-Host ""
    Write-Host "    # 2. Start PostgreSQL + backup container"
    Write-Host "    aifs start -i myproject"
    Write-Host ""
    Write-Host "    # 3. Format the filesystem (one-time)"
    Write-Host "    aifs format -i myproject"
    Write-Host ""
    Write-Host "    # 4. Mount as a drive letter (Z: recommended)"
    Write-Host "    aifs mount -i myproject Z: -d"
    Write-Host ""
    Write-Host "    # 5. Snapshot before risky work, rewind if needed"
    Write-Host "    aifs snapshot create -i myproject --type full"
    Write-Host "    aifs restore -i myproject --time `"2026-06-15 14:30:00+00`""
    if (-not $winfspOk) {
        Write-Host ""
        Write-Host "  WARNING: WinFsp is missing. Install it before running 'aifs mount':"
        Write-Host "    winget install WinFsp.WinFsp"
        Write-Host "    choco install winfsp"
        Write-Host "    https://winfsp.dev/rel/"
    }
    Write-Host ""
} else {
    Write-Host "  Some components may need attention:"
    if (-not $wslOk)    { Write-Host "    - WSL2: may need reboot" }
    if (-not $pmOk)     { Write-Host "    - Podman: open a new terminal" }
    if (-not $winfspOk) { Write-Host "    - WinFsp: required for 'aifs mount'" }
    Write-Host ""
}

Write-Host "Done."
Write-Host ""
Write-Host "  Note: After logging in, wait 1-2 minutes for WSL to start before running aifs." -ForegroundColor Yellow

# Check if aifs is accessible in the current session
if (-not (Get-Command aifs -ErrorAction SilentlyContinue)) {
    Write-Host ""
    Write-Host "  ! 'aifs' command is not available in this session." -ForegroundColor Yellow
    Write-Host "    Install aifs first, then sign out and sign back in (or open a new" -ForegroundColor Yellow
    Write-Host "    PowerShell window) for the PATH change to take effect." -ForegroundColor Yellow
    Write-Host ""
    Write-Host "  -> Install aifs:" -ForegroundColor Cyan
    Write-Host "     irm https://raw.githubusercontent.com/mars-base/aifs/main/scripts/install.ps1 | iex" -ForegroundColor Cyan
    Write-Host ""
}
