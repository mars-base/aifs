# scripts/test-drive-mount-runner.ps1 — Drive-letter mount regression test runner via S4U
#
# Usage:
#   .\scripts\test-drive-mount-runner.ps1 [-Proxy <proxy_url>]
#
# This script downloads the aifs Windows binary and the test-drive-mount.ps1
# test script, then runs them inside an S4U scheduled task (non-interactive
# session). The wrapped test exercises:
#   config init --add drivemount → start → format --volume drivemount →
#   mount Z: -d (background) → write/read Z:\hello.txt → umount Z: → stop → cleanup
#
# Logs are captured with Start-Transcript to %USERPROFILE%\drive-mount-test.log.
[CmdletBinding()]
param([string]$Proxy = "http://10.241.21.97:8118")

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$baseUrl    = "http://10.241.21.97:1357"
$exeUrl     = "$baseUrl/aifs-windows-amd64.exe"
$scriptUrl  = "$baseUrl/test-drive-mount.ps1"
$exeFile    = Join-Path $env:USERPROFILE "aifs-windows-amd64.exe"
$scriptFile = Join-Path $env:USERPROFILE "test-drive-mount.ps1"
$logFile    = Join-Path $env:USERPROFILE "drive-mount-test.log"
$taskName   = "aifs-drive-mount-test"

$env:HTTPS_PROXY = $Proxy
$env:HTTP_PROXY  = $Proxy

Write-Host "-> Downloading aifs binary..."
Invoke-WebRequest -Uri $exeUrl -OutFile $exeFile -UseBasicParsing
Write-Host "  OK: $((Get-Item $exeFile).Length) bytes"

Write-Host "-> Downloading drive-mount test script..."
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

Write-Host "=== Drive-letter mount test start: `$(Get-Date -Format 'yyyy-MM-dd HH:mm:ss') ==="

try {
    Set-Location `$env:USERPROFILE
    & "$scriptFile" -Proxy "$Proxy"
    Write-Host "`n=== DRIVE-MOUNT TEST PASSED ==="
} catch {
    Write-Host "`n=== DRIVE-MOUNT TEST FAILED: `$_ ==="
    `$_ | Format-List -Force | Out-String | Write-Host
} finally {
    Write-Host "`n=== Test finished at: `$(Get-Date -Format 'yyyy-MM-dd HH:mm:ss') ==="
    Stop-Transcript | Out-Null
}
"@
$wrapperFile = Join-Path $env:USERPROFILE "drive-mount-wrapper.ps1"
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
