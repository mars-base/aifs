# cpu-diag.ps1 — Windows CPU virtualization diagnostic
# Usage: powershell -ExecutionPolicy Bypass -File cpu-diag.ps1
#
# All wmic calls go through cmd.exe to avoid CLIXML output over WinRM.
# Uses chcp 437 for English output from cmd.exe commands.
# Avoids $LASTEXITCODE (unreliable over WinRM).

$ErrorActionPreference = "Continue"

Write-Host "======================================"
Write-Host "  AIFS Podman Readiness Check"
Write-Host "======================================"
Write-Host ""

# ─── Helper: read one wmic property cleanly ────────────────

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

# ─── Helper: check if an exe exists on PATH ─────────────

function CmdExists {
    param([string]$name)
    $sysRoot = $env:SystemRoot
    if (-not $sysRoot) { $sysRoot = "C:\Windows" }
    $sys32 = Join-Path $sysRoot "System32"
    # Check System32 first, then common locations
    if (Test-Path (Join-Path $sys32 "$name.exe")) { return $true }
    if (Test-Path (Join-Path "$sysRoot\SysWOW64" "$name.exe")) { return $true }
    # Check Program Files and common install locations
    $candidates = @(
        (Join-Path $env:ProgramFiles "$name\bin\$name.exe"),
        (Join-Path ${env:ProgramFiles(x86)} "$name\bin\$name.exe"),
        (Join-Path $env:LOCALAPPDATA "$name\bin\$name.exe")
    )
    foreach ($c in $candidates) {
        if (Test-Path $c) { return $true }
    }
    # Last resort: use Get-Command (works for anything on PATH)
    $found = $null
    try { $found = Get-Command $name -ErrorAction SilentlyContinue } catch {}
    return ($null -ne $found)
}

# ─── Helper: get exe path via where ──────────────────────

function GetExePath {
    param([string]$name)
    $raw = cmd.exe /c "chcp 437 >nul & where $name" 2>$null
    if ($raw) {
        $str = ($raw | Out-String).Trim()
        $firstLine = ($str -split '\r?\n')[0].Trim()
        if ($firstLine -and (Test-Path $firstLine)) { return $firstLine }
    }
    return ""
}

# ─── 1. CPU Info ──────────────────────────────────────────

Write-Host "[1] CPU & Virtualization Features"
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

$allOk = ($virtFW -eq "TRUE") -and ($slat -eq "TRUE") -and ($vmm -eq "TRUE")
if ($allOk) {
    Write-Host "  [OK] CPU supports nested virtualization"
} else {
    Write-Host "  [FAIL] CPU lacks virtualization features for WSL2/podman"
}
Write-Host ""

# ─── 2. Hyper-V Platform ──────────────────────────────────

Write-Host "[2] Hyper-V Platform"
Write-Host "-----------------------------------"

# Use wmic to check Hyper-V (avoids dism encoding/admin issues)
$hvRaw = cmd.exe /c "wmic /namespace:\\root\virtualization\v2 path Msvm_VirtualSystemManagementService get __path /value" 2>&1
$hvStr = ($hvRaw | Out-String).Trim()
if ($hvStr -match "__path") {
    Write-Host "  Hyper-V: available (WMI virtualization namespace detected)"
} else {
    Write-Host "  Hyper-V: not detected or not enabled"
}
Write-Host ""

# ─── 3. WSL Status ────────────────────────────────────────

Write-Host "[3] WSL Status"
Write-Host "-----------------------------------"

$wslInstalled = CmdExists "wsl"

if (-not $wslInstalled) {
    Write-Host "  WSL: not installed"
    Write-Host "  [HINT] Run: wsl --install"
} else {
    $wslPath = GetExePath "wsl"
    if ($wslPath) { Write-Host "  WSL path: $wslPath" }
    else { Write-Host "  WSL: found on PATH" }
    Write-Host "  WSL installed: yes"
}
Write-Host ""

# ─── 4. Podman Check ──────────────────────────────────────

Write-Host "[4] Podman"
Write-Host "-----------------------------------"

$podmanInstalled = CmdExists "podman"

if (-not $podmanInstalled) {
    Write-Host "  podman: not installed"
    Write-Host "  [HINT] Run: winget install RedHat.Podman"
} else {
    $podmanPath = GetExePath "podman"
    if ($podmanPath) { Write-Host "  podman path: $podmanPath" }
    $podmanVer = cmd.exe /c "chcp 437 >nul & podman --version" 2>$null
    if ($podmanVer) { Write-Host "  $podmanVer" }
}
Write-Host ""

# ─── 5. Summary ───────────────────────────────────────────

Write-Host "======================================"
Write-Host "  Summary"
Write-Host "======================================"

if ($allOk) {
    Write-Host "  This machine CAN run WSL2 + podman."
    Write-Host ""
    Write-Host "  Next steps:"
    if (-not $wslInstalled)    { Write-Host "    1. Install WSL2:  wsl --install" }
    if (-not $podmanInstalled) { Write-Host "    2. Install podman: winget install RedHat.Podman" }
    Write-Host "    3. podman machine init"
    Write-Host "    4. podman machine start"
    Write-Host "    5. Run aifs setup"
} else {
    Write-Host "  This machine is likely a NESTED VM without nested"
    Write-Host "  virtualization support. Podman/WSL2 will NOT work."
    Write-Host ""
    Write-Host "  Alternatives:"
    Write-Host "    - Run aifs on the KVM host directly (Linux)"
    Write-Host "    - Use a bare-metal Windows machine with VT-x enabled"
}

Write-Host ""
Write-Host "Done."
