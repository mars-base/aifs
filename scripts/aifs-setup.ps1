# aifs-setup.ps1 — Check virtualization, install WSL + podman, init podman machine
# Usage: powershell -ExecutionPolicy Bypass -File aifs-setup.ps1 [-Proxy <url>|none]
#
# Steps:
#   1. Check CPU virtualization (VT-x/AMD-V, SLAT, VMM)
#   2. Install WSL2 if not present (requires admin + reboot)
#   3. Install podman via winget
#   4. Init and start podman machine
#
# Download from: http://10.241.21.97:1357/aifs-setup.ps1

[CmdletBinding()]
param(
    [string]$Proxy = ""
)

$useProxy = ($Proxy -and ($Proxy -ne "none"))

$ErrorActionPreference = "Continue"

Write-Host ""
Write-Host "========================================"
Write-Host "  AIFS Setup - WSL2 + Podman Installer"
Write-Host "========================================"
Write-Host ""

# ─── Helper: read one wmic property ─────────────────────

function ReadWmic {
    param([string]$prop)
    $raw = cmd.exe /c "wmic cpu get $prop /value" 2>&1
    $str = ($raw | Out-String).Trim()
    foreach ($line in $str.Split([Environment]::NewLine, [StringSplitOptions]::RemoveEmptyEntries)) {
        $line = $line.Trim()
        if ($line -match "^\s*$prop\s*=\s*(.+)$") {
            return $Matches[1].Trim()
        }
    }
    return "UNKNOWN"
}

# ─── Helper: check if an exe exists ─────────────────────

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

# ─── Helper: refresh PATH in current session ────────────

function Refresh-Path {
    $machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
    $userPath    = [Environment]::GetEnvironmentVariable("Path", "User")
    $env:Path    = "$machinePath;$userPath"
}

# ─── Helper: print step result ──────────────────────────

function Print-Result {
    param([string]$label, [string]$status, [string]$detail)
    $icon = if ($status -eq "ok")   { "[OK]" }
            elseif ($status -eq "skip") { "[SKIP]" }
            elseif ($status -eq "warn") { "[WARN]" }
            else { "[FAIL]" }
    Write-Host "  $icon $label"
    if ($detail) { Write-Host "         $detail" }
}

# ═══════════════════════════════════════════════════════════
# PHASE 1: Check CPU virtualization
# ═══════════════════════════════════════════════════════════

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

$cpuOk = ($virtFW -eq "TRUE") -and ($slat -eq "TRUE") -and ($vmm -eq "TRUE")

if ($cpuOk) {
    Print-Result "CPU supports virtualization" "ok"
} else {
    Print-Result "CPU lacks virtualization" "fail"
    Write-Host ""
    Write-Host "  This machine cannot run WSL2/podman."
    Write-Host "  Virtualization must be enabled in BIOS,"
    Write-Host "  or this may be a nested VM without passthrough."
    Write-Host ""
    Write-Host "Setup aborted."
    exit 1
}
Write-Host ""

# ═══════════════════════════════════════════════════════════
# PHASE 2: Install WSL2
# ═══════════════════════════════════════════════════════════

Write-Host "[Phase 2] WSL2 setup"
Write-Host "-----------------------------------"

# Check if wsl.exe exists AND actually works (inbox stub may exist but be non-functional)
$wslExists = CmdExists "wsl"
$wslWorking = $false

if ($wslExists) {
    # Probe: does wsl actually work? (inbox stub returns help text for everything)
    $probe = Start-Process -FilePath "wsl.exe" -ArgumentList "--status" -Wait -PassThru -NoNewWindow `
        -RedirectStandardOutput "$env:TEMP\wsl-probe.out" -RedirectStandardError "$env:TEMP\wsl-probe.err"
    if ($probe.ExitCode -eq 0) {
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
        Write-Host "  *** REBOOT REQUIRED to activate WSL features ***"
        Write-Host "  Please reboot, then re-run this script."
        Print-Result "Reboot required" "warn" "dism returned 3010"
        exit 1
    }

    # Step 2: Try wsl --install --no-distribution (may upgrade inbox stub to Store version)
    Write-Host ""
    Write-Host "  Running: wsl --install --no-distribution"
    $wslProc = Start-Process -FilePath "wsl.exe" -ArgumentList "--install","--no-distribution" -Wait -PassThru -NoNewWindow

    if ($wslProc.ExitCode -eq 0) {
        Print-Result "wsl --install --no-distribution" "ok"
    } else {
        Print-Result "wsl --install" "warn" "Exit code: $($wslProc.ExitCode) (may be ok after reboot)"
    }

    # Refresh PATH
    Refresh-Path
}

# Set WSL2 as default version and update kernel (runs whether freshly installed or pre-existing)
if (CmdExists "wsl") {
    $setDefault = Start-Process -FilePath "wsl.exe" -ArgumentList "--set-default-version","2" -Wait -PassThru -NoNewWindow
    if ($setDefault.ExitCode -eq 0) {
        Print-Result "WSL default version 2" "ok"
    } else {
        Print-Result "WSL set-default-version" "warn" "Exit code: $($setDefault.ExitCode) — reboot may be needed"
    }

    $updateKernel = Start-Process -FilePath "wsl.exe" -ArgumentList "--update" -Wait -PassThru -NoNewWindow
    if ($updateKernel.ExitCode -eq 0) {
        Print-Result "WSL kernel updated" "ok"
    } else {
        Print-Result "WSL update" "warn" "Exit code: $($updateKernel.ExitCode) — reboot may be needed"
    }
}
Write-Host ""

# ═══════════════════════════════════════════════════════════
# PHASE 3: Install Podman
# ═══════════════════════════════════════════════════════════

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
Write-Host ""

# ═══════════════════════════════════════════════════════════
# PHASE 4: Init & Start podman machine
# ═══════════════════════════════════════════════════════════

Write-Host "[Phase 4] Podman machine"
Write-Host "-----------------------------------"

if (-not (CmdExists "podman")) {
    Print-Result "Podman not available" "fail" "Cannot init podman machine"
    Write-Host ""
    Write-Host "  Please open a new terminal and run:"
    Write-Host "    podman machine init"
    Write-Host "    podman machine start"
    exit 1
}

# Check if machine already exists
$machineInfo = cmd.exe /c "chcp 437 >nul & podman machine info" 2>&1
$machineInfoStr = ($machineInfo | Out-String).Trim()

$hasMachine = $false
if ($machineInfoStr -match "VM:\s*$") {
    # "VM:" with no name means no machine created yet
    $hasMachine = $false
} elseif ($machineInfoStr -match "Name:\s+\S+") {
    $hasMachine = $true
}

if ($hasMachine) {
    Print-Result "Podman machine exists" "skip"

    # Check if running
    $machineLs = cmd.exe /c "chcp 437 >nul & podman machine list --format {{.Running}}" 2>$null
    $machineLsStr = ($machineLs | Out-String).Trim()
    if ($machineLsStr -match "(?i)true") {
        Print-Result "Podman machine running" "ok"
    } else {
        Write-Host "  Starting podman machine..."
        $startProc = Start-Process -FilePath "podman" -ArgumentList "machine","start" -Wait -PassThru -NoNewWindow
        if ($startProc.ExitCode -eq 0) {
            Print-Result "Podman machine started" "ok"
        } else {
            Print-Result "Podman machine start" "fail" "Exit code: $($startProc.ExitCode)"
        }
    }
} else {
    Write-Host "  Initializing podman machine (downloading VM image, this takes a few minutes)..."
    Write-Host "  Running: podman machine init --cpus 4 --memory 4096 --disk-size 100"
    Write-Host ""

    $initProc = Start-Process -FilePath "podman" `
        -ArgumentList "machine","init","--cpus","4","--memory","4096","--disk-size","100" `
        -Wait -PassThru -NoNewWindow

    if ($initProc.ExitCode -ne 0) {
        Print-Result "Podman machine init" "fail" "Exit code: $($initProc.ExitCode)"
        Write-Host "  Try manually: podman machine init"
        exit 1
    }

    Print-Result "Podman machine initialized" "ok"

    Write-Host "  Starting podman machine..."
    $startProc = Start-Process -FilePath "podman" -ArgumentList "machine","start" -Wait -PassThru -NoNewWindow
    if ($startProc.ExitCode -eq 0) {
        Print-Result "Podman machine started" "ok"
    } else {
        Print-Result "Podman machine start" "fail" "Exit code: $($startProc.ExitCode)"
        Write-Host "  Try manually: podman machine start"
    }
}
Write-Host ""

# ═══════════════════════════════════════════════════════════
# Summary
# ═══════════════════════════════════════════════════════════

Write-Host "========================================"
Write-Host "  Setup Complete"
Write-Host "========================================"
Write-Host ""

# Quick verification
$wslOk  = CmdExists "wsl"
$pmOk   = CmdExists "podman"

if ($wslOk -and $pmOk) {
    $podmanVer = cmd.exe /c "chcp 437 >nul & podman --version" 2>$null
    Write-Host "  WSL2   : installed"
    Write-Host "  Podman : $podmanVer"
    Write-Host ""
    Write-Host "  Next step: install and run aifs"
    Write-Host "    podman exec -it <container> bash"
    Write-Host ""
} else {
    Write-Host "  Some components may need attention:"
    if (-not $wslOk) { Write-Host "    - WSL2: may need reboot" }
    if (-not $pmOk)  { Write-Host "    - Podman: open a new terminal" }
    Write-Host ""
}

Write-Host "Done."
