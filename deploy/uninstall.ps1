# Final Lobby - remove everything this installed.
#
# Run as Administrator:
#     powershell -ExecutionPolicy Bypass -File .\uninstall.ps1

#Requires -Version 5.1
$ErrorActionPreference = 'Continue'

$id = [Security.Principal.WindowsIdentity]::GetCurrent()
if (-not (New-Object Security.Principal.WindowsPrincipal($id)).IsInRole(
        [Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Host "  Please run this as Administrator." -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "  Removing Final Lobby" -ForegroundColor White
Write-Host ""

$target = Join-Path $env:LOCALAPPDATA 'FinalLobby'
$svcExe = Join-Path $target 'netservice.exe'

# Close the app if it is open, or its files cannot be deleted.
Get-Process lobbyapp -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue

if (Test-Path $svcExe) {
    Write-Host "  Removing the network service and its firewall rule" -ForegroundColor Cyan
    & $svcExe uninstall 2>&1 | ForEach-Object { Write-Host "    $_" -ForegroundColor DarkGray }
    Start-Sleep -Seconds 2
}

# Belt and braces: the rule may survive if the service binary was already gone.
netsh advfirewall firewall delete rule name="Final Lobby (Dota 2 match hosting)" 2>&1 | Out-Null

$lnk = Join-Path ([Environment]::GetFolderPath('Desktop')) 'Final Lobby.lnk'
if (Test-Path $lnk) { Remove-Item $lnk -Force }

if (Test-Path $target) { Remove-Item $target -Recurse -Force -ErrorAction SilentlyContinue }

# Saved settings live with the user's other app config.
$cfg = Join-Path $env:APPDATA 'FinalLobby'
if (Test-Path $cfg) { Remove-Item $cfg -Recurse -Force -ErrorAction SilentlyContinue }

Write-Host ""
Write-Host "  Final Lobby removed. The virtual network adapter goes with it." -ForegroundColor Green
Write-Host ""
