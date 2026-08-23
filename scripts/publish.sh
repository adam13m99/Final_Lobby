#!/usr/bin/env bash
# Build the app and publish it as a download on the server.
#
#   ./scripts/publish.sh
#
# This is the whole delivery pipeline. It reads the real server details,
# stamps them into the binaries so nobody has to type an address or an access
# code, builds one installer that carries the app and the service inside it,
# uploads it with its manifest, and prints the link to open.
#
# Running it again during a test session publishes a new build; installed
# copies notice on their next launch and offer it.
#
# SAFETY: this box also runs an unrelated live business - nginx SNI routing
# on TCP 443 fed over WireGuard, CoreDNS, a Postgres control plane. Nothing
# here touches any of them. The download is served by our own coordinator on
# TCP 7001, a port we already own, so no new listener and no firewall change.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
. scripts/env.sh

SERVER_FILE="mobinhost_server_1.txt"
PLINK="/c/Program Files/PuTTY/plink"
PSCP="/c/Program Files/PuTTY/pscp"
HOSTKEY="SHA256:6yklmkVkG3TnxYlwPCsWZYAzUDI5YV6eMRC/HFGD0Zo"
DIST_DIR="/var/lib/finallobby/dist"

[ -f "$SERVER_FILE" ] || { echo "missing $SERVER_FILE" >&2; exit 1; }
USER_HOST=$(grep -o 'ssh [^ ]*' "$SERVER_FILE" | awk '{print $2}')
PASS=$(grep -i '^password:' "$SERVER_FILE" | sed 's/^[Pp]assword:[[:space:]]*//' | tr -d '\r\n')
HOST="${USER_HOST#*@}"

ssh_run() { "$PLINK" -ssh -batch -hostkey "$HOSTKEY" "$USER_HOST" -pw "$PASS" "$@"; }
scp_up()  { "$PSCP" -batch -hostkey "$HOSTKEY" -pw "$PASS" "$1" "$USER_HOST:$2"; }

# An upload can fail silently when the target is locked by a running process.
# That cost twenty minutes of testing a binary nobody had changed, so every
# upload is checked on the far end before anything depends on it.
upload_verified() { # local remote
  scp_up "$1" "$2"
  local want got
  want=$(sha256sum "$1" | cut -d" " -f1)
  got=$(ssh_run "sha256sum $2 | cut -d' ' -f1" | tr -dc '0-9a-f')
  if [ "$want" != "$got" ]; then
    echo "  FAIL upload mismatch for $2: local $want, remote $got" >&2
    exit 1
  fi
  echo "  OK   $(basename "$2") verified on the server"
}

# ---------------------------------------------------------------- secrets

echo "==> reading the server's own configuration"
ssh_run bash -s <<'REMOTE'
set -euo pipefail
mkdir -p /etc/finallobby /var/lib/finallobby/dist
# The download path segment is the only thing in front of the installer,
# because a browser cannot send a bearer token. Generated once and kept.
if [ ! -s /etc/finallobby/download.key ]; then
  head -c 12 /dev/urandom | base64 | tr -d '=+/' | cut -c1-16 > /etc/finallobby/download.key
fi
# The coordinator runs as the finallobby user and has to read this, the same
# way it reads the API token beside it.
chown root:finallobby /etc/finallobby/download.key
chmod 640 /etc/finallobby/download.key
chmod 755 /var/lib/finallobby /var/lib/finallobby/dist
REMOTE

API_TOKEN=$(ssh_run 'cat /etc/finallobby/api.token' | tr -d '\r\n')
DL_KEY=$(ssh_run 'cat /etc/finallobby/download.key' | tr -d '\r\n')
[ -n "$API_TOKEN" ] || { echo "no API token on the server" >&2; exit 1; }
[ -n "$DL_KEY" ]    || { echo "no download key on the server" >&2; exit 1; }

COORDINATOR="http://$HOST:7001"
DOWNLOAD_BASE="$COORDINATOR/d/$DL_KEY"
VERSION="$(date -u +%Y.%m.%d-%H%M)"

# ------------------------------------------------------------------ build

echo "==> building $VERSION"
FL_VERSION="$VERSION" \
FL_COORDINATOR="$COORDINATOR" \
FL_AUTH_TOKEN="$API_TOKEN" \
FL_DOWNLOAD_BASE="$DOWNLOAD_BASE" \
  ./scripts/build.sh installer

SETUP="bin/FinalLobby-Setup.exe"
[ -f "$SETUP" ] || { echo "the installer was not built" >&2; exit 1; }

SUM=$(sha256sum "$SETUP" | cut -d" " -f1)
SIZE=$(stat -c %s "$SETUP")

mkdir -p dist
cat > dist/version.json <<EOF
{
  "version": "$VERSION",
  "sha256": "$SUM",
  "size": $SIZE,
  "url": "$DOWNLOAD_BASE/FinalLobby-Setup.exe",
  "built_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "installer": "FinalLobby-Setup.exe"
}
EOF

# ----------------------------------------------------------------- upload

echo "==> publishing"
upload_verified "$SETUP" "$DIST_DIR/FinalLobby-Setup.exe.new"
upload_verified dist/version.json "$DIST_DIR/version.json.new"

# Swap both into place together. A manifest that arrives before its
# installer means anyone downloading in that window gets a file whose hash
# does not match, and the app refuses it.
ssh_run bash -s <<REMOTE
set -euo pipefail
mv $DIST_DIR/FinalLobby-Setup.exe.new $DIST_DIR/FinalLobby-Setup.exe
mv $DIST_DIR/version.json.new $DIST_DIR/version.json
chmod 644 $DIST_DIR/FinalLobby-Setup.exe $DIST_DIR/version.json
REMOTE

# ------------------------------------------- make sure the server serves it

# The coordinator serves the download, so it has to be the build that knows
# how. Publishing a client against a coordinator that does not understand
# -dist-dir would leave the link dead and the reason invisible.
if ! ssh_run "grep -q -- '-dist-dir' /etc/systemd/system/coordinator.service"; then
  echo "==> the server's coordinator does not serve downloads yet; deploying it"
  ./scripts/deploy.sh coordinator
fi
ssh_run 'systemctl is-active coordinator.service'

# The unrelated business on this box must be untouched. Say so out loud
# rather than assuming it.
echo "==> confirming the unrelated services are still up"
ssh_run 'systemctl is-active nginx coredns relay | tr "\n" " "; echo'

# ------------------------------------------------------------------ done

echo
echo "  Published $VERSION"
echo
echo "  Download link, open this on each PC:"
echo
echo "      $DOWNLOAD_BASE/"
echo
echo "  Installed copies will offer this build the next time they start."
