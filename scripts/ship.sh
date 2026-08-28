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

echo "=== server: coordinator, terms, relay ==="
./scripts/deploy.sh all

echo
echo "=== app: build, stamp, upload, publish ==="
./scripts/publish.sh

echo
echo "=== shipped ==="
echo "  The server and the download now match this working tree."
echo "  An installed copy offers the new build the next time it starts."
