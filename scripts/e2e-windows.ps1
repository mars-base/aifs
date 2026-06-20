# scripts/e2e-windows.ps1 — End-to-end PITR test through the aifs filesystem on Windows.
#
# Usage:
#   .\scripts\e2e-windows.ps1 [instance_name]
#
# Environment:
#   AIFS_BIN    path to aifs binary (default: .\build\aifs-windows-amd64.exe)
#   FORCE_CLEAN skip the confirmation prompt when set to "1"
#
# The script uses an isolated work directory and config file. It exercises:
#   config init → start → format → mount → write pre-backup files →
#   full snapshot → write post-backup files → record PITR target time →
#   write final files → umount → restore → remount → verify file-level rollback.

[CmdletBinding()]
param(
    [string]$Instance = "pitrwin"
)

$ErrorActionPreference = "Stop"

$AifsBin = if ($env:AIFS_BIN) { $env:AIFS_BIN } else { ".\build\aifs-windows-amd64.exe" }
$ForceClean = $env:FORCE_CLEAN -eq "1"

$Suffix = "pitrwin-$PID"
$BackupContainer = "aifs-backup-${Suffix}"
$Container = "aifs-pg-${Instance}"

$WorkDir = Join-Path $env:TEMP "aifs-pitr-win-${Suffix}"
$Config = Join-Path $WorkDir "config.yaml"

$RepoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $RepoRoot

if (-not (Test-Path $AifsBin)) {
    Write-Error "AIFS binary not found: $AifsBin`nBuild it first:`n  make release"
}

function Get-AvailableDriveLetter {
    for ($c = [int][char]'Z'; $c -ge [int][char]'D'; $c--) {
        $letter = [char]$c
        if (-not (Get-PSDrive -Name $letter -ErrorAction SilentlyContinue)) {
            return "${letter}:"
        }
    }
    throw "No available drive letter found"
}

function Get-FreePort {
    $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
    $listener.Start()
    $port = $listener.LocalEndpoint.Port
    $listener.Stop()
    return $port
}

function Invoke-Aifs {
    # Use $args (simple function) instead of [Parameter()] (advanced function)
    # to prevent PowerShell from intercepting flags like -o as common parameters
    # (-OutVariable / -OutBuffer) in PowerShell 5.1.
    $allArgs = @($AifsBin, "-c", $Config, "-i", $Instance) + $args
    Write-Host "  > $($allArgs -join ' ')"
    & $allArgs[0] $allArgs[1..$allArgs.Length]
    if ($LASTEXITCODE -ne 0) { throw "aifs failed: $($allArgs -join ' ')" }
}

function Wait-PostgresReady {
    Write-Host "→ Waiting for PostgreSQL to be ready..."
    for ($i = 0; $i -lt 60; $i++) {
        $ready = $false
        try {
            $null = & podman exec $Container pg_isready -U aifs -d "${Instance}_db" 2>$null
            if ($LASTEXITCODE -eq 0) { $ready = $true }
        } catch { }
        if ($ready) {
            Write-Host "  ✓ PostgreSQL ready"
            return
        }
        Start-Sleep -Seconds 1
    }
    Write-Host "  ✗ PostgreSQL did not become ready; dumping container status and logs..."
    try { & podman ps -a | Out-String | Write-Host } catch { }
    try { & podman logs --tail 100 $Container 2>&1 | Out-String | Write-Host } catch { }
    throw "PostgreSQL did not become ready"
}

function Test-FileContent {
    param([string]$Path, [string]$Expected)
    if (-not (Test-Path $Path)) { throw "missing file: $Path" }
    $actual = Get-Content -Raw -Path $Path
    if ($actual.Trim() -ne $Expected) {
        throw "content mismatch in ${Path}: expected '$Expected', got '$actual'"
    }
}

function Cleanup {
    Write-Host ""
    Write-Host "→ Cleaning up..."
    $ErrorActionPreference = "Continue"
    try { Invoke-Aifs umount $MountPoint } catch { }
    try { Invoke-Aifs stop } catch { }
    try { & podman rm -f $Container $BackupContainer 2>$null } catch { }
    if (Test-Path $WorkDir) {
        Remove-Item -Recurse -Force $WorkDir -ErrorAction SilentlyContinue
    }
}

# trap { Cleanup; break }

if (-not $ForceClean) {
    Write-Host "⚠️  This script will create an isolated aifs environment under ${WorkDir}."
    Write-Host "    It will be automatically cleaned up when the script exits."
    $ans = Read-Host "Continue? [y/N]"
    if ($ans -notmatch '^[yY]') {
        Write-Host "Cancelled"
        exit 0
    }
}

$MountPoint = Get-AvailableDriveLetter
$HostPort = Get-FreePort

Write-Host "=== aifs filesystem PITR end-to-end test (Windows) ==="
Write-Host "Instance:       ${Instance}"
Write-Host "Work dir:       ${WorkDir}"
Write-Host "Mount point:    ${MountPoint}"
Write-Host "Backup container: ${BackupContainer}"
Write-Host "Host port:      ${HostPort}"
Write-Host ""

New-Item -ItemType Directory -Path $WorkDir -Force | Out-Null

Write-Host "=== 1. config init ==="
Invoke-Aifs config init -o $Config --add $Instance --base-dir $WorkDir

# Isolate the backup container from any existing aifs environment.
$cfgLines = Get-Content $Config
$cfgLines = $cfgLines -replace '^( *)container_name: aifs-backup$', "`$1container_name: ${BackupContainer}"
$cfgLines = $cfgLines -replace '^( *)host_port: .*$', "`$1host_port: ${HostPort}"
$cfgLines | Set-Content -Path $Config -Encoding UTF8

Write-Host ""
Write-Host "=== 2. start instance ==="
Invoke-Aifs start

Write-Host ""
Write-Host "=== 3. format filesystem ==="
Invoke-Aifs format --volume $Instance

Write-Host ""
Write-Host "=== 4. mount filesystem (-d background) ==="
Invoke-Aifs mount $MountPoint -d
Start-Sleep -Seconds 2

Write-Host ""
Write-Host "=== 5. write pre-backup files ==="
Set-Content -Path "$MountPoint\file-before.txt" -Value "before backup" -NoNewline
New-Item -ItemType Directory -Path "$MountPoint\dir1" -Force | Out-Null
Set-Content -Path "$MountPoint\dir1\before.txt" -Value "before backup in dir1" -NoNewline

Test-FileContent -Path "$MountPoint\file-before.txt" -Expected "before backup"
if (-not (Test-Path "$MountPoint\dir1")) { throw "pre-backup directory missing" }
Write-Host "  ✓ pre-backup files written"

Write-Host ""
Write-Host "=== 6. take full snapshot ==="
Invoke-Aifs snapshot create --type full --tail-logs

Write-Host ""
Write-Host "=== 7. write post-backup files ==="
Set-Content -Path "$MountPoint\file-after.txt" -Value "after backup" -NoNewline
Set-Content -Path "$MountPoint\dir1\after.txt" -Value "after backup in dir1" -NoNewline
if (-not (Test-Path "$MountPoint\file-after.txt")) { throw "post-backup file missing" }
if (-not (Test-Path "$MountPoint\dir1\after.txt")) { throw "post-backup dir1 file missing" }
Write-Host "  ✓ post-backup files written"

# Give WAL archiving a moment to advance past the post-backup writes.
Start-Sleep -Seconds 2
$TargetTimeUtc = [DateTime]::UtcNow.ToString("yyyy-MM-dd HH:mm:ss+00")
Write-Host ""
Write-Host "=== 8. recorded PITR target time (UTC): ${TargetTimeUtc} ==="

# Continue writing files that should disappear after restore.
Start-Sleep -Seconds 2
Set-Content -Path "$MountPoint\file-final.txt" -Value "final after target" -NoNewline
Set-Content -Path "$MountPoint\dir1\final.txt" -Value "final after target in dir1" -NoNewline
if (-not (Test-Path "$MountPoint\file-final.txt")) { throw "final file missing" }
Write-Host "  ✓ final files written (should be rolled back)"

# Let the final writes be archived.
Start-Sleep -Seconds 2

Write-Host ""
Write-Host "=== 9. umount before restore ==="
Invoke-Aifs umount $MountPoint

Write-Host ""
Write-Host "=== 10. restore to ${TargetTimeUtc} ==="
Invoke-Aifs restore --time "$TargetTimeUtc" --force

Write-Host ""
Write-Host "=== 11. wait for PostgreSQL to be ready ==="
Wait-PostgresReady

Write-Host ""
Write-Host "=== 12. remount filesystem ==="
Invoke-Aifs mount $MountPoint -d
Start-Sleep -Seconds 2

Write-Host ""
Write-Host "=== 13. verify file-level rollback ==="

# Files written before and right after the backup must still exist.
if (-not (Test-Path "$MountPoint\file-before.txt")) { throw "file-before.txt missing after restore" }
Test-FileContent -Path "$MountPoint\file-before.txt" -Expected "before backup"

if (-not (Test-Path "$MountPoint\dir1\before.txt")) { throw "dir1\before.txt missing after restore" }
Test-FileContent -Path "$MountPoint\dir1\before.txt" -Expected "before backup in dir1"

if (-not (Test-Path "$MountPoint\file-after.txt")) { throw "file-after.txt missing after restore" }
Test-FileContent -Path "$MountPoint\file-after.txt" -Expected "after backup"

if (-not (Test-Path "$MountPoint\dir1\after.txt")) { throw "dir1\after.txt missing after restore" }
Test-FileContent -Path "$MountPoint\dir1\after.txt" -Expected "after backup in dir1"

# Files written after the target time must be gone.
if (Test-Path "$MountPoint\file-final.txt") { throw "file-final.txt should have been rolled back" }
if (Test-Path "$MountPoint\dir1\final.txt") { throw "dir1\final.txt should have been rolled back" }

Write-Host "  ✓ pre-target files preserved, post-target files rolled back"

Write-Host ""
Write-Host "✓ aifs filesystem PITR end-to-end test completed successfully"

Cleanup
