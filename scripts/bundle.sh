#!/usr/bin/env bash
# Build the test bundle to copy onto the second PC.
#
#   ./scripts/bundle.sh
#
# Produces dist/FinalLobby-test/ containing the two executables and a
# setup.txt with the real coordinator address and API token, read from the
# server. That file holds a secret, so dist/ is gitignored.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
. scripts/env.sh

OUT="dist/FinalLobby-test"
SERVER_FILE="mobinhost_server_1.txt"
PLINK="/c/Program Files/PuTTY/plink"
HOSTKEY="SHA256:6yklmkVkG3TnxYlwPCsWZYAzUDI5YV6eMRC/HFGD0Zo"

echo "==> building"
./scripts/build.sh netservice
./scripts/build.sh lobbyapp
./scripts/build.sh lobbycli

rm -rf "$OUT"
mkdir -p "$OUT"
cp bin/netservice.exe bin/lobbyapp.exe bin/lobbycli.exe "$OUT/"
cp deploy/install.ps1 deploy/uninstall.ps1 "$OUT/"

# wintun.dll is embedded in netservice.exe and written out on first run, so
# it is deliberately not shipped alongside.

echo "==> reading the coordinator token from the server"
[ -f "$SERVER_FILE" ] || { echo "missing $SERVER_FILE" >&2; exit 1; }
USER_HOST=$(grep -o 'ssh [^ ]*' "$SERVER_FILE" | awk '{print $2}')
PASS=$(grep -i '^password:' "$SERVER_FILE" | sed 's/^[Pp]assword:[[:space:]]*//' | tr -d '\r\n')
HOST="${USER_HOST#*@}"

TOKEN=$("$PLINK" -ssh -batch -hostkey "$HOSTKEY" "$USER_HOST" -pw "$PASS" \
  'cat /etc/finallobby/api.token' | tr -d '\r\n')
RELAY_PUB=$("$PLINK" -ssh -batch -hostkey "$HOSTKEY" "$USER_HOST" -pw "$PASS" \
  'cat /etc/finallobby/relay.pub' | tr -d '\r\n')

cat > "$OUT/setup.txt" <<EOF
Final Lobby - test build
========================

Server address : http://$HOST:7001
Access code    : $TOKEN

TO INSTALL
----------
1. Right-click "install.ps1" and choose "Run with PowerShell".
   If Windows asks for permission, say yes - this is the only time.

   If right-click does not offer it, open PowerShell as Administrator and run:
       powershell -ExecutionPolicy Bypass -File .\install.ps1

2. Open "Final Lobby" from your desktop.

3. On the first screen, paste the server address and access code above,
   and pick a player name. Use a DIFFERENT player name on each PC.

TO PLAY
-------
One person clicks "Create room". The other sees it in the list and clicks
"Join". Both click "Connect", then both click "Launch Dota 2" - the host
first, and the other once the host's game has loaded.

TO REMOVE
---------
Run uninstall.ps1 as Administrator. It takes the service, the firewall rule,
the virtual adapter and the settings with it.

For diagnostics there is also lobbycli.exe; see
docs/testing/two-pc-acceptance.md.

Relay          : $HOST:443
Relay key      : $RELAY_PUB
EOF

echo
echo "==> bundle ready in $OUT"
ls -la "$OUT"
echo
echo "Copy that whole folder to the second PC, then follow setup.txt."
