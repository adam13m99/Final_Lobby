#!/usr/bin/env bash
# Build the server binaries and install them on the MobinHost box.
#
#   ./scripts/deploy.sh relay     build + upload + restart the relay
#   ./scripts/deploy.sh status    show what is running
#   ./scripts/deploy.sh logs      tail the relay journal
#
# Connection details come from mobinhost_server_1.txt, which is gitignored
# and must stay that way.
#
# SAFETY: this server runs an unrelated live SNI-proxy business - CoreDNS on
# 53 and nginx on TCP 443, with real paying users. We bind UDP 443 only, and
# this script never touches nginx, CoreDNS, or any TCP port.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
. scripts/env.sh

SERVER_FILE="mobinhost_server_1.txt"
PLINK="/c/Program Files/PuTTY/plink"
PSCP="/c/Program Files/PuTTY/pscp"
HOSTKEY="SHA256:6yklmkVkG3TnxYlwPCsWZYAzUDI5YV6eMRC/HFGD0Zo"

[ -f "$SERVER_FILE" ] || { echo "missing $SERVER_FILE" >&2; exit 1; }
USER_HOST=$(grep -o 'ssh [^ ]*' "$SERVER_FILE" | awk '{print $2}')
PASS=$(grep -i '^password:' "$SERVER_FILE" | sed 's/^[Pp]assword:[[:space:]]*//' | tr -d '\r\n')
HOST="${USER_HOST#*@}"

ssh_run() {
  "$PLINK" -ssh -batch -hostkey "$HOSTKEY" "$USER_HOST" -pw "$PASS" "$@"
}

scp_up() { # local remote
  "$PSCP" -batch -hostkey "$HOSTKEY" -pw "$PASS" "$1" "$USER_HOST:$2"
}

deploy_relay() {
  echo "==> building"
  ./scripts/build.sh relay

  echo "==> preparing host"
  ssh_run bash -s <<'REMOTE'
set -euo pipefail
id finallobby >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin finallobby
mkdir -p /opt/finallobby /etc/finallobby
chmod 750 /etc/finallobby
chown root:finallobby /etc/finallobby

# Generate the relay identity once and keep it. Regenerating it would
# invalidate every client that has the old public key baked in.
if [ ! -f /etc/finallobby/relay.key ]; then
  echo "NEEDKEY"
fi
REMOTE

  echo "==> uploading binary"
  scp_up bin/relay /opt/finallobby/relay.new
  scp_up deploy/relay.service /etc/systemd/system/relay.service

  echo "==> installing"
  ssh_run bash -s <<'REMOTE'
set -euo pipefail
chmod 755 /opt/finallobby/relay.new
mv /opt/finallobby/relay.new /opt/finallobby/relay

if [ ! -f /etc/finallobby/relay.key ]; then
  /opt/finallobby/relay -genkey > /tmp/relaykey.txt
  awk '/^private/{print $2}' /tmp/relaykey.txt > /etc/finallobby/relay.key
  awk '/^public/{print $2}'  /tmp/relaykey.txt > /etc/finallobby/relay.pub
  shred -u /tmp/relaykey.txt
  chmod 640 /etc/finallobby/relay.key
  chmod 644 /etc/finallobby/relay.pub
  chown root:finallobby /etc/finallobby/relay.key /etc/finallobby/relay.pub
  echo "generated a new relay identity"
fi

systemctl daemon-reload
systemctl enable relay.service >/dev/null 2>&1 || true
systemctl restart relay.service
sleep 2
systemctl is-active relay.service

echo "--- relay public key (clients need this) ---"
cat /etc/finallobby/relay.pub

echo "--- listening sockets ---"
ss -lunp | grep -E ':443\b' || echo "WARNING: relay is not on UDP 443"
echo "--- TCP 443 must still belong to nginx ---"
ss -ltnp | grep -E ':443\b' || echo "note: nothing on TCP 443"
REMOTE
}

case "${1:-relay}" in
  relay)  deploy_relay ;;
  status) ssh_run 'systemctl status relay.service --no-pager -l | head -25; echo; ss -lunp | grep :443 || true' ;;
  logs)   ssh_run 'journalctl -u relay.service -n 80 --no-pager' ;;
  *)      echo "usage: $0 [relay|status|logs]" >&2; exit 2 ;;
esac
