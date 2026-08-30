#!/usr/bin/env bash
# Put everything that has changed onto the live server, in the right order.
#
#   ./scripts/ship.sh            server, then the app
#   ./scripts/ship.sh --check    run scripts/check.sh first and stop if it fails
#
# This exists because a change that only lives on this PC cannot be tested by
# the person who asked for it (D62). The standing instruction is that the live
# server matches the repository after every task, and "matches" means three
# separate things that used to be three separate commands run from memory:
#
#   1. the coordinator binary and the terms text it serves,
#   2. the relay binary,
#   3. the desktop app, published as an installer that existing copies
#      upgrade themselves to.
#
# Forgetting the third is the easy mistake and the invisible one: the server
# is healthy, the API is new, and every installed copy is still running last
# week's interface.
#
# SAFETY: this box runs an unrelated live business - nginx SNI routing on TCP
# 443, CoreDNS, a Postgres control plane, real paying users. Everything below
# goes through deploy.sh and publish.sh, which touch UDP 443, TCP 7001 and
# nothing else, and which say out loud at the end whether the neighbours are
# still up.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

if [ "${1:-}" = "--check" ]; then
  echo "=== check ==="
  bash scripts/check.sh || { echo "check.sh failed; nothing shipped" >&2; exit 1; }
  echo
fi

# Rooms live in the coordinator's memory and nowhere else (D12). Restarting it
# is therefore not a transparent operation: every open room disappears and
# everybody sitting in one is dropped back to the lobby mid-match. That is a
# fine trade at four in the morning and a bad one on a Friday evening, and the
# only way to tell the difference is to ask before restarting.
warn_about_live_rooms() {
  local server_file="mobinhost_server_1.txt" host rooms
  [ -f "$server_file" ] || return 0
  host=$(grep -o 'ssh [^ ]*' "$server_file" | awk '{print $2}')
  host="${host#*@}"
  [ -n "$host" ] || return 0
  rooms=$(curl -s --max-time 6 "http://$host:7001/healthz" | grep -o '"rooms":[0-9]*' | tr -dc '0-9')
  [ -n "$rooms" ] || { echo "  (could not reach the live coordinator to count rooms)"; return 0; }
  if [ "$rooms" -gt 0 ]; then
    echo
    echo "  !! $rooms room(s) are open on the live server right now."
    echo "  !! Shipping restarts the coordinator, which closes all of them and"
    echo "  !! drops everybody in them back to the lobby. Rooms are in memory."
    if [ -t 0 ]; then
      read -r -p "  Ship anyway? [y/N] " answer
      case "$answer" in [yY]*) ;; *) echo "  nothing shipped"; exit 1 ;; esac
    else
      echo "  !! No terminal to ask at, so this is going ahead. Ctrl-C now if"
      echo "  !! that is wrong."
      sleep 5
    fi
    echo
  else
    echo "  no rooms open on the live server; a restart costs nobody anything"
  fi
}

echo "=== live server ==="
warn_about_live_rooms

echo
echo "=== server: coordinator, terms, relay ==="
./scripts/deploy.sh all

echo
echo "=== app: build, stamp, upload, publish ==="
./scripts/publish.sh

echo
echo "=== shipped ==="
echo "  The server and the download now match this working tree."
echo "  An installed copy offers the new build the next time it starts."
