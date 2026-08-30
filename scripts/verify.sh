#!/usr/bin/env bash
# The whole harness in one command, cheapest rung first.
#
#   bash scripts/verify.sh          everything (a few minutes)
#   bash scripts/verify.sh fast     unit level only (under a minute)
#
# Read this first if you are picking the project up. These rungs are the
# entire safety net, and every one of them exists because the thing it now
# catches was once shipped to the owner and found by hand:
#
#   check.sh       every module builds, passes its own tests, and parses
#   smoke.sh       a real player can browse, read the terms, sign up, host
#                  a room and sign back in
#   uicheck.sh     the interface survives polls that change nothing - no
#                  duplicated rows, no rebuilt cards, no lost focus (D75)
#   chatcheck.sh   the chat dock opens when a message arrives (D56)
#   termscheck.sh  the terms cannot be accepted without being read (D61)
#
# Every rung binds loopback only and uses a throwaway database with a
# redirected APPDATA. None of them can reach the live server, and none of
# them can see the developer's own session.
#
# This keeps going after a failure on purpose, so that one run tells you
# everything that is wrong instead of the first thing. The exit code is the
# number of rungs that failed.
#
# Deliberately NOT in here: preview.sh and try.sh, which make pictures and a
# window for a person to look at, and live.sh, which talks to the real
# server. Nothing automatic can grade those, and the last one has consequences.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

MODE="${1:-all}"
case "$MODE" in
  all)  RUNGS="check smoke uicheck chatcheck termscheck" ;;
  fast) RUNGS="check" ;;
  *)    echo "usage: bash scripts/verify.sh [all|fast]" >&2; exit 2 ;;
esac

FAILED=0
SUMMARY=""

for rung in $RUNGS; do
  echo
  echo "############################################################"
  echo "### $rung"
  echo "############################################################"
  START=$(date +%s)
  if bash "scripts/$rung.sh"; then
    VERDICT="ok"
  else
    VERDICT="FAILED"
    FAILED=$((FAILED + 1))
  fi
  TOOK=$(( $(date +%s) - START ))
  SUMMARY="$SUMMARY
  $(printf '%-12s %-8s %3ds' "$rung" "$VERDICT" "$TOOK")"
done

echo
echo "############################################################"
echo "### verdict"
echo "############################################################"
echo "$SUMMARY"
echo

# A rung with no Chrome on the machine prints a WARN and exits zero rather
# than failing, so "ok" on uicheck, chatcheck or termscheck can also mean
# "skipped". They say so themselves in their own output; that is why this
# script prints every rung in full rather than only the summary.

if [ "$FAILED" -eq 0 ]; then
  if [ "$MODE" = "fast" ]; then
    echo "VERDICT: the units are healthy."
    echo "         Run 'bash scripts/verify.sh' before committing - fast mode"
    echo "         has never once caught an interface bug."
  else
    echo "VERDICT: fit to ship."
    echo "         Next: update docs/STATE.md, commit naming the task,"
    echo "         ./scripts/git-sync.sh push, then ./scripts/ship.sh."
  fi
else
  echo "VERDICT: $FAILED rung(s) FAILED - do not commit, do not ship."
fi
exit "$FAILED"
