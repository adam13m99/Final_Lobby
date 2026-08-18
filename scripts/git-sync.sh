#!/usr/bin/env bash
# Push/pull through the domestic server, because GitHub is DPI-blocked
# from Iranian connections directly. Opens a temporary SOCKS proxy over
# SSH to the MobinHost box, which does have international access.
#
# Usage:  ./scripts/git-sync.sh push
#         ./scripts/git-sync.sh pull
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

TOKEN_FILE="github_token_admin.txt"
SERVER_FILE="mobinhost_server_1.txt"
PROXY_PORT=1080
PLINK="/c/Program Files/PuTTY/plink"
HOSTKEY="SHA256:6yklmkVkG3TnxYlwPCsWZYAzUDI5YV6eMRC/HFGD0Zo"

for f in "$TOKEN_FILE" "$SERVER_FILE"; do
  [ -f "$f" ] || { echo "missing $f" >&2; exit 1; }
done

TOKEN=$(tr -d '\r\n' < "$TOKEN_FILE" | sed 's/^Token:[[:space:]]*//')
SERVER_USER_HOST=$(grep -o 'ssh [^ ]*' "$SERVER_FILE" | awk '{print $2}')
SERVER_PASS=$(grep -i '^password:' "$SERVER_FILE" | sed 's/^[Pp]assword:[[:space:]]*//' | tr -d '\r\n')

# Reuse an existing tunnel if one is already listening.
if ! (exec 3<>"/dev/tcp/127.0.0.1/$PROXY_PORT") 2>/dev/null; then
  echo "opening SOCKS tunnel via $SERVER_USER_HOST ..."
  "$PLINK" -ssh -batch -hostkey "$HOSTKEY" -D "$PROXY_PORT" -N \
    "$SERVER_USER_HOST" -pw "$SERVER_PASS" &
  TUNNEL_PID=$!
  trap 'kill "$TUNNEL_PID" 2>/dev/null || true' EXIT
  sleep 4
else
  echo "reusing existing tunnel on port $PROXY_PORT"
fi

# The token is supplied via an ephemeral credential helper so it is never
# written into .git/config.
git -c http.proxy="socks5h://127.0.0.1:$PROXY_PORT" \
    -c "credential.helper=!f(){ echo username=adam13m99; echo password=$TOKEN; };f" \
    "${@:-push}" 2>&1 | sed 's/ghp_[A-Za-z0-9]*/ghp_***/g'
