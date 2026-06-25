# scripts/test-drive-mount.ps1 — Drive-letter mount regression test
#
# Usage:
#   .\scripts\test-drive-mount.ps1 [-Proxy <proxy_url>]
#
# Environment:
#   AIFS_BIN    path to aifs binary (default: downloaded from the HTTP file server)
#   FORCE_CLEAN skip the confirmation prompt when set to "1"
#
# This script downloads the aifs Windows binary (unless AIFS_BIN is provided) and
# runs a minimal drive-letter mount regression test. It can be run interactively
# or scheduled via test-drive-mount-runner.ps1 for remote/WinRM deployment.
# It exercises:
#   1. config init --add drivemount --base-dir <temp>
#   2. start
#   3. format --volume drivemount
#   4. mount Z: -d (background)
#   5. write/read Z:\hello.txt
#   6. umount Z:
#   7. stop
#   8. cleanup
#
# The test verifies that drive-letter mounts work on Windows, including from a
# non-interactive (WinRM/S4U) session where directory pathname mounts would fail.
[CmdletBinding()]
param([string]$Proxy = "http://10.241.21.97:8118")

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$baseUrl    = "http://10.241.21.97:1357"
$exeUrl     = "$baseUrl/aifs-windows-amd64.exe"
# Prefer AIFS_BIN env, then system-installed aifs, then download as last resort.
if ($env:AIFS_BIN) {
    $exeFile = $env:AIFS_BIN
} elseif (Get-Command aifs -ErrorAction SilentlyContinue) {
    $exeFile = (Get-Command aifs).Source
} else {
    $exeFile = Join-Path $env:USERPROFILE "aifs-windows-amd64.exe"
}

$env:HTTPS_PROXY = $Proxy
$env:HTTP_PROXY  = $Proxy

if (-not (Test-Path $exeFile)) {
    Write-Host "-> Downloading aifs binary..."
    Invoke-WebRequest -Uri $exeUrl -OutFile $exeFile -UseBasicParsing
    Write-Host "  OK: $((Get-Item $exeFile).Length) bytes"
} else {
    Write-Host "-> Using aifs binary: $exeFile"
}

$Suffix = "drivemount-$PID"
$BackupContainer = "aifs-backup-${Suffix}"

$base = "C:\temp\aifs-drive-mount-test"
$mp   = "Z:"
$bin  = $exeFile

# Pre-cleanup: remove any leftover containers/directories from a previous run.
$ErrorActionPreference = "Continue"
try { wsl -d podman-machine-default --exec podman rm -f aifs-pg-drivemount $BackupContainer 2>$null } catch { }
Remove-Item -Recurse -Force $base -ErrorAction SilentlyContinue
$ErrorActionPreference = "Stop"

New-Item -ItemType Directory -Path $base -Force | Out-Null
$cfg = Join-Path $base "config.yaml"

function Invoke-Aifs {
    $allArgs = @($bin, "-c", $cfg, "-i", "drivemount") + $args
    Write-Host "  > $($allArgs -join ' ')"
    & $allArgs[0] $allArgs[1..$allArgs.Length]
    if ($LASTEXITCODE -ne 0) { throw "aifs failed: $($allArgs -join ' ')" }
}

Write-Host "=== 1. config init ==="
Invoke-Aifs config init -o $cfg --add drivemount --base-dir $base

# Isolate the backup container from any existing aifs environment.
$cfgLines = Get-Content $cfg
    $pat = '^( *)container_name: aifs-backup$'
    $repl = "`${1}container_name: ${BackupContainer}"
    $cfgLines = $cfgLines -replace $pat, $repl
$cfgLines | Set-Content -Path $cfg -Encoding UTF8

Write-Host "=== 2. start ==="
Invoke-Aifs start

Write-Host "=== 3. format ==="
Invoke-Aifs format --volume drivemount

Write-Host "=== 4. mount drive $mp (background) ==="
Invoke-Aifs mount $mp -d
Start-Sleep -Seconds 2

Write-Host "=== 5. write/read file ==="
Set-Content -Path "$mp\hello.txt" -Value "drive mount works" -NoNewline
$actual = Get-Content -Raw -Path "$mp\hello.txt"
Write-Host "read back: [$actual]"
if ($actual.Trim() -ne "drive mount works") { throw "content mismatch" }

Write-Host "=== 6. umount ==="
Invoke-Aifs umount $mp

Write-Host "=== 7. destroy ==="
Invoke-Aifs destroy --clean-data --force

Write-Host "=== 8. cleanup ==="
$ErrorActionPreference = "Continue"
# destroy --clean-data restarts the backup container to remove the stanza;
# remove it again after destroy completes.
& podman rm -f aifs-pg-drivemount $BackupContainer 2>$null
Remove-Item -Recurse -Force $base -ErrorAction SilentlyContinue
Write-Host "=== drive-letter mount test PASSED ==="
