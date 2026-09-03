#!/usr/bin/env bash
# Rehearse scripts/qa-lobby.sh against a throwaway coordinator on loopback.
#
#   bash scripts/qa-lobby-selftest.sh
#
# qa-lobby.sh is the one script here that writes to the live server, and it
# writes a lot: two dozen accounts that cannot be deleted afterwards. So it is
# proved somewhere harmless first. This boots the same coordinator the rest of
# the harness uses, on a throwaway database, runs the whole fixture against it
# with the rate-limit waits shortened, and asserts the lobby that comes out is
# the one the fixture claims to build.
#
# Loopback only. It never contacts 87.107.110.199.
set -uo pipefail
cd "$(dirname "$0")/.."
. scripts/env.sh
. scripts/sandbox.sh

trap 'bash scripts/qa-lobby.sh down > /dev/null 2>&1; sandbox_cleanup' EXIT

echo "=== the QA fixture, against a throwaway coordinator ==="
sandbox_build       > /dev/null || exit 1
sandbox_coordinator > /dev/null || exit 1

# The sandbox coordinator is started with no -auth-token, so it accepts any
# bearer. A placeholder is still needed: it is what tells qa-lobby.sh not to
# go and read the real one off the live server over ssh.
TOKEN=sandbox-accepts-anything
export QA_COORD="$COORD" QA_TOKEN="$TOKEN"
# The limiter is the same code, but a rehearsal that waits the full five
# seconds a sign-up costs takes three minutes. The live run does wait.
export QA_SLOW_AUTH=0 QA_SLOW_JOIN=0

bash scripts/qa-lobby.sh up > "$WORK/qa-up.log" 2>&1
UP=$?
if [ $UP -ne 0 ]; then
  echo "  FAIL  the fixture did not build"
  tail -25 "$WORK/qa-up.log"
  exit 1
fi

BAD=0
say() { if [ "$1" = 0 ]; then echo "  OK    $2"; else echo "  FAIL  $2"; echo "        $3"; BAD=1; fi; }

ROOMS=$(curl -sS -H "Authorization: Bearer $TOKEN" "$COORD/v1/rooms")

check() { # check NAME PYTHON-EXPRESSION
  local why
  why=$(printf '%s' "$ROOMS" | python -c "
import sys, json
d = json.load(sys.stdin)
rooms = d.get('rooms', []) if isinstance(d, dict) else d
by = {r.get('name',''): r for r in rooms}
$2
" 2>&1)
  say $? "$1" "$why"
}

check "six QA rooms exist" "
assert len(rooms) == 6, '%d rooms: %s' % (len(rooms), [r.get('name') for r in rooms])
"
check "the open room holds two players and two watchers" "
r = by['QA - open, all pick']
assert r['seats'] == 3, 'seats=%s want 3 (host + two)' % r['seats']
"
check "the nine-of-ten room has exactly one seat left" "
r = by['QA - nine of ten, one seat left']
assert r['seats'] == 9, 'seats=%s want 9' % r['seats']
assert r.get('min_mmr') == 2000, 'min_mmr=%s want 2000' % r.get('min_mmr')
"
check "the password room asks for one" "
r = by['QA - password is qa1234']
assert r.get('needs_password'), 'the room does not ask for a password'
"
check "the friends-only and invite-only doors are shut" "
assert by['QA - friends only']['privacy'] == 'friends', by['QA - friends only']['privacy']
assert by['QA - invite only']['privacy'] == 'invite', by['QA - invite only']['privacy']
"
check "the in-game room is locked to new players" "
r = by['QA - match in progress']
assert r['status'] == 'locked_in_game', 'status=%s' % r['status']
"
check "every room names a game mode" "
missing = [r['name'] for r in rooms if not r.get('game_mode')]
assert not missing, 'no mode on: %s' % missing
"

# The heartbeat is the whole reason this fixture can exist at all: stop it and
# every room must die, because their hosts have gone quiet (D84). That is what
# makes it worth asserting - if rooms outlived it, `down` would be a lie.
echo "  ..    stopping the heartbeat and waiting for the rooms to close"
bash scripts/qa-lobby.sh down > /dev/null 2>&1
LEFT=$(curl -sS -H "Authorization: Bearer $TOKEN" "$COORD/v1/rooms" | python -c "
import sys, json
d = json.load(sys.stdin)
rooms = d.get('rooms', []) if isinstance(d, dict) else d
print(len([r for r in rooms if r.get('name','').startswith('QA')]))")
say $([ "$LEFT" = 0 ] && echo 0 || echo 1) "down empties every QA room" "$LEFT still open"

echo
if [ $BAD -eq 0 ]; then
  echo "RESULT: the fixture builds the lobby it says it does"
else
  echo "RESULT: PROBLEMS FOUND - do not point this at the live server"
fi
exit $BAD
