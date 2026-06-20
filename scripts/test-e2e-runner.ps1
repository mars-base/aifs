# scripts/test-e2e-runner.ps1 — Full PITR e2e test runner via S4U scheduled task
#
# Usage:
#   .\scripts\test-e2e-runner.ps1 [-Proxy <proxy_url>]
#
# This script downloads the aifs Windows binary and the test-e2e.ps1 test
# script, then runs them inside an S4U scheduled task (non-interactive session).
# The wrapped test exercises the full PITR flow:
#   config init → start → format → mount drive letter → write files →
#   full snapshot → write more files → record PITR target time → restore →
#   remount → verify file-level rollback → cleanup
#
# Logs are captured with Start-Transcript to %USERPROFILE%\e2e-full.log.
[CmdletBinding()]
param([string]$Proxy = "http://10.241.21.97:8118")

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$baseUrl    = "http://10.241.21.97:1357"
$exeUrl     = "$baseUrl/aifs-windows-amd64.exe"
$scriptUrl  = "$baseUrl/test-e2e.ps1"
$exeFile    = Join-Path $env:USERPROFILE "aifs-windows-amd64.exe"
$scriptFile = Join-Path $env:USERPROFILE "test-e2e.ps1"
$logFile    = Join-Path $env:USERPROFILE "e2e-full.log"
$taskName   = "aifs-e2e-full"

$env:HTTPS_PROXY = $Proxy
$env:HTTP_PROXY  = $Proxy

Write-Host "-> Downloading aifs binary..."
Invoke-WebRequest -Uri $exeUrl -OutFile $exeFile -UseBasicParsing
Write-Host "  OK: $((Get-Item $exeFile).Length) bytes"

Write-Host "-> Downloading e2e test script..."
Invoke-WebRequest -Uri $scriptUrl -OutFile $scriptFile -UseBasicParsing
Write-Host "  OK: $scriptFile"

# Wrapper that runs inside scheduled task with full transcript capture
$wrapper = @"
`$ErrorActionPreference = "Continue"
`$ProgressPreference = "SilentlyContinue"
`$logFile = "$logFile"
Start-Transcript -Path `$logFile -Append | Out-Null
`$env:HTTPS_PROXY = "$Proxy"
`$env:HTTP_PROXY  = "$Proxy"
`$env:AIFS_BIN = "$exeFile"
`$env:FORCE_CLEAN = "1"
`$env:CONTAINER_HOST = "tcp://localhost:2375"

Write-Host "=== Full E2E Test Start: `$(Get-Date -Format 'yyyy-MM-dd HH:mm:ss') ==="
Write-Host "Binary: `$env:AIFS_BIN"
Write-Host "Binary size: `$((Get-Item `$env:AIFS_BIN).Length) bytes"

# Pre-cleanup: remove any leftover containers from previous runs
Write-Host "`n--- Pre-cleanup ---"
try {
    wsl -d podman-machine-default --exec podman rm -f aifs-pg-pitrwin aifs-backup-pitrwin-* 2>&1 | Write-Host
} catch {
    Write-Host "Pre-cleanup: no containers to remove (OK)"
}

# Run e2e test
Write-Host "`n--- Running e2e-windows.ps1 ---"
try {
    Set-Location `$env:USERPROFILE
    & "$scriptFile" -Instance pitrwin
    Write-Host "`n=== E2E TEST PASSED ==="
} catch {
    Write-Host "`n=== E2E TEST FAILED: `$_ ==="
    `$_ | Format-List -Force | Out-String | Write-Host
} finally {
    Write-Host "`n=== Test finished at: `$(Get-Date -Format 'yyyy-MM-dd HH:mm:ss') ==="
    Stop-Transcript | Out-Null
}
"@
$wrapperFile = Join-Path $env:USERPROFILE "test-e2e-wrapper.ps1"
[System.IO.File]::WriteAllText($wrapperFile, $wrapper, [System.Text.UTF8Encoding]::new($false))
Write-Host "Wrapper written to: $wrapperFile"

# Clean up any existing task with the same name
try {
    Unregister-ScheduledTask -TaskName $taskName -Confirm:$false -ErrorAction SilentlyContinue
} catch { }

# Schedule task (S4U + highest privileges — no password needed)
$action    = New-ScheduledTaskAction -Execute "powershell.exe" `
    -Argument "-ExecutionPolicy Bypass -File `"$wrapperFile`""
$principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType S4U -RunLevel Highest
Register-ScheduledTask -TaskName $taskName -Action $action -Principal $principal -Force | Out-Null
Start-ScheduledTask -TaskName $taskName
Write-Host "  Task started: $taskName"
Write-Host "  Log: $logFile"
Write-Host "  Check progress: Get-ScheduledTask -TaskName $taskName | Select State"
