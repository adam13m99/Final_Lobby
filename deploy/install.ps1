# Final Lobby - installer for a test machine.
#
# Right-click this file and choose "Run with PowerShell" as Administrator,
# or from an Administrator PowerShell:
#
#     powershell -ExecutionPolicy Bypass -File .\install.ps1
#
# It copies the program into your user folder, registers the background
# network service, opens the firewall for hosting, and puts a shortcut on
# your desktop. Administrator rights are needed once, here, and never again.

#Requires -Version 5.1

$ErrorActionPreference = 'Stop'

function Fail($msg) { Write-Host ""; Write-Host "  $msg" -ForegroundColor Red; Write-Host ""; exit 1 }
function Step($msg) { Write-Host "  $msg" -ForegroundColor Cyan }
function Done($msg) { Write-Host "  $msg" -ForegroundColor Green }

Write-Host ""
Write-Host "  Final Lobby - setup" -ForegroundColor White
Write-Host "  ===================" -ForegroundColor DarkGray
Write-Host ""

# --- checks -------------------------------------------------------------

$id = [Security.Principal.WindowsIdentity]::GetCurrent()
if (-not (New-Object Security.Principal.WindowsPrincipal($id)).IsInRole(
        [Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Fail "Please run this as Administrator. Right-click PowerShell and choose 'Run as administrator', then run this file again."
}

$here = Split-Path -Parent $MyInvocation.MyCommand.Path
foreach ($f in @('netservice.exe', 'lobbyapp.exe')) {
    if (-not (Test-Path (Join-Path $here $f))) {
        Fail "$f is missing from this folder. Copy the whole folder across, not just the script."
    }
}

# --- install ------------------------------------------------------------

$target = Join-Path $env:LOCALAPPDATA 'FinalLobby'
Step "Installing to $target"
New-Item -ItemType Directory -Force -Path $target | Out-Null

# Clear out an older install first, or the running binary cannot be replaced.
# The service and its executable can go missing independently - a half-removed
# install is exactly the state a second attempt runs into - so handle each.
$existing = Get-Service FinalLobbyNet -ErrorAction SilentlyContinue
if ($existing) {
    Step "Removing the previous version"
    $oldExe = Join-Path $target 'netservice.exe'
    if (Test-Path $oldExe) {
        & $oldExe uninstall 2>&1 | Out-Null
    } else {
        # Its binary is gone; unregister the service directly.
        Stop-Service FinalLobbyNet -Force -ErrorAction SilentlyContinue
        & sc.exe delete FinalLobbyNet 2>&1 | Out-Null
    }
    # Windows keeps a service registered until every handle to it closes, and
    # creating a new one with the same name fails until it does.
    for ($i = 0; $i -lt 20; $i++) {
        Start-Sleep -Milliseconds 500
        if (-not (Get-Service FinalLobbyNet -ErrorAction SilentlyContinue)) { break }
    }
}

# The app may be running from the target folder and hold its own file open.
Get-Process lobbyapp -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue

foreach ($f in @('netservice.exe', 'lobbyapp.exe', 'lobbycli.exe', 'setup.txt')) {
    $src = Join-Path $here $f
    if (Test-Path $src) { Copy-Item $src -Destination $target -Force }
}

Step "Registering the background network service"
$out = & (Join-Path $target 'netservice.exe') install 2>&1
$out | ForEach-Object { Write-Host "    $_" -ForegroundColor DarkGray }
if ($LASTEXITCODE -ne 0) { Fail "The service would not install. See the message above." }

Start-Sleep -Seconds 2
$svc = Get-Service FinalLobbyNet -ErrorAction SilentlyContinue
if (-not $svc -or $svc.Status -ne 'Running') {
    Fail "The service was installed but is not running. Try: Start-Service FinalLobbyNet"
}
Done "Service is running"

# --- shortcut -----------------------------------------------------------

Step "Adding a desktop shortcut"
$desktop = [Environment]::GetFolderPath('Desktop')
$lnk = Join-Path $desktop 'Final Lobby.lnk'
$shell = New-Object -ComObject WScript.Shell
$sc = $shell.CreateShortcut($lnk)
$sc.TargetPath = Join-Path $target 'lobbyapp.exe'
$sc.WorkingDirectory = $target
$sc.Description = 'Final Lobby - play Dota 2 with friends'
$sc.Save()
Done "Shortcut created"

# --- done ---------------------------------------------------------------

Write-Host ""
Done "Setup finished."
Write-Host ""
Write-Host "  Open 'Final Lobby' from your desktop to play." -ForegroundColor White
Write-Host "  You will not need Administrator again." -ForegroundColor DarkGray
Write-Host ""
if (Test-Path (Join-Path $target 'setup.txt')) {
    Write-Host "  The first screen asks for a server address and access code." -ForegroundColor DarkGray
    Write-Host "  Both are in setup.txt in this folder." -ForegroundColor DarkGray
    Write-Host ""
}
