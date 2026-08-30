#!/usr/bin/env bash
# Assert what the interface does while it changes.
#
#   bash scripts/uicheck.sh
#
# The rung between smoke.sh and preview.sh. smoke.sh proves the page renders
# once and the console is quiet; preview.sh photographs it so somebody can
# look. Neither can see the *second* render, and that is where every interface
# bug this project has shipped has lived:
#
#   - a room row left behind in the document when its replacement was
#     inserted, so the lobby grew a second copy of your own room (D75);
#   - twenty seat cards and forty room rows destroyed and rebuilt twice a
#     second under the pointer (D71);
#   - a render guard that was never once true, because a relay ping that
#     changes on every poll was inside its signature (D73).
#
# Everything runs on loopback against a throwaway coordinator and a throwaway
# database, exactly as preview.sh does. It never contacts the live server.
set -uo pipefail
cd "$(dirname "$0")/.."
. scripts/env.sh

CHROME="/c/Program Files/Google/Chrome/Application/chrome.exe"
if [ ! -f "$CHROME" ]; then
  echo "  SKIP  interface checks (Chrome is not installed at the expected path)"
  exit 0
fi

. scripts/sandbox.sh
cleanup() { [ -n "${CH_PID:-}" ] && kill "$CH_PID" 2>/dev/null; sandbox_cleanup; }
trap cleanup EXIT

echo "=== the interface, while it changes ==="
sandbox_build      > /dev/null || exit 1
sandbox_coordinator > /dev/null || exit 1
sandbox_app        > /dev/null || exit 1
sandbox_seed       > /dev/null
sandbox_you        > /dev/null
CDP=$(sandbox_port)

"$CHROME" --headless=new --disable-gpu --no-first-run --no-default-browser-check \
  --remote-debugging-port="$CDP" --user-data-dir="$(cygpath -w "$WORK")\chrome" \
  --window-size=1440,820 about:blank > "$WORK/chrome.log" 2>&1 &
CH_PID=$!
sleep 3

node scripts/ui-assert.js "$APP_URL" "$CDP"
RESULT=$?

echo
if [ $RESULT -eq 0 ]; then
  echo "RESULT: the interface holds together under change"
else
  echo "RESULT: PROBLEMS FOUND - fix before continuing"
fi
exit $RESULT
