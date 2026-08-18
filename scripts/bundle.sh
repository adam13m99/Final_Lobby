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
./scripts/build.sh lobbycli

rm -rf "$OUT"
mkdir -p "$OUT"
cp bin/netservice.exe bin/lobbycli.exe "$OUT/"

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
Final Lobby - test bundle
=========================

Coordinator : http://$HOST:7001
API token   : $TOKEN
Relay       : $HOST:443
Relay key   : $RELAY_PUB

On the second PC, from this folder:

  1. Open PowerShell AS ADMINISTRATOR, once:

       .\\netservice.exe install

  2. Then in a NORMAL PowerShell window:

       .\\lobbycli.exe setup -coordinator http://$HOST:7001 ^
           -token $TOKEN -player bob -nick Bob

     Use a different -player name from the other PC.

  3. Check it works:

       .\\lobbycli.exe rooms

Full instructions: docs/testing/two-pc-acceptance.md
EOF

echo
echo "==> bundle ready in $OUT"
ls -la "$OUT"
echo
echo "Copy that whole folder to the second PC, then follow setup.txt."
