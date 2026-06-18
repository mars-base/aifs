# aifs-setup.ps1 — Check virtualization, install WSL2 + podman CLI for aifs on Windows
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
        $probe = Start-Process -FilePath "wsl.exe" -ArgumentList "--status" -Wait -PassThru -NoNewWindow `
            -RedirectStandardOutput "$env:TEMP\wsl-probe.out" -RedirectStandardError "$env:TEMP\wsl-probe.err"
        if ($probe.ExitCode -eq 0) {
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

# ═══════════════════════════════════════════════════════════
# PHASE 4: Install WinFsp (runtime dependency for aifs mount)
# ═══════════════════════════════════════════════════════════

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

# ═══════════════════════════════════════════════════════════
# PHASE 5: Verify podman CLI (no podman machine needed)
# ═══════════════════════════════════════════════════════════

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

# ═══════════════════════════════════════════════════════════
# Summary
# ═══════════════════════════════════════════════════════════

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
    Write-Host "  Next step: install and run aifs"
    Write-Host "    aifs config init"
    Write-Host "    aifs start"
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
