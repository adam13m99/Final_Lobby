#!/usr/bin/env bash
# Stand a lobby full of test players and test rooms up on the LIVE server, so
# the interface can be clicked through by a person.
#
#   ./scripts/qa-lobby.sh up       build it, and start the heartbeat
#   ./scripts/qa-lobby.sh status   what the coordinator has right now
#   ./scripts/qa-lobby.sh down     empty the rooms and stop the heartbeat
#   ./scripts/qa-lobby.sh keep     the heartbeat itself (started by `up`)
#
# THIS ONE TOUCHES THE LIVE SERVER. Everything else that seeds a lobby -
# try.sh, preview.sh, live.sh - is loopback on a throwaway database, and
# nothing there is visible from an installed copy of the app. This is for
# the opposite case: the owner testing the real product on the real server,
# who needs somebody to play with.
#
# ── The one thing to understand before running it ──
#
# A room closes the moment its host stops answering (D84). There is no grace;
# the only delay is the thirty seconds the coordinator waits before calling a
# silent host offline. So a test room is not a thing you create and leave -
# it exists exactly as long as something keeps syncing on its host's behalf.
#
# That something is `keep`, running on this PC. Stop it, close this terminal,
# or let the machine sleep, and every QA room is gone within half a minute.
# `up` starts it; `down` stops it.
#
# Rooms are in memory in the coordinator, so they also vanish whenever it
# restarts - which ./scripts/ship.sh does every time. Ship first, then build
# the lobby.
#
# ── What it leaves behind ──
#
# The rooms are temporary. The ACCOUNTS ARE NOT: they are real rows in the
# live database and there is no API that deletes a player. Every one of them
# is named `qa_*` with a display name starting "QA", so they are obvious in
# the lobby and in the friends rail, and `down` empties their rooms - but the
# accounts stay until somebody removes them from the database by hand.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

SERVER_FILE="mobinhost_server_1.txt"
PLINK="/c/Program Files/PuTTY/plink"
HOSTKEY="SHA256:6yklmkVkG3TnxYlwPCsWZYAzUDI5YV6eMRC/HFGD0Zo"
STATE="scripts/.qa-lobby"
PIDFILE="scripts/.qa-lobby.pid"
KEEPLOG="scripts/.qa-lobby.log"

# One password for every test account. These are throwaway logins on a test
# lobby; the point of them is that the owner can sign in as one to see the
# product from a second person's side.
QA_PASS="qa-test-account-2026"
ROOM_PASS="qa1234"

# The live server, unless something says otherwise. QA_COORD and QA_TOKEN
# together point this at a loopback sandbox instead, which is how the fixture
# itself is rehearsed before it is aimed at the real one - see
# scripts/qa-lobby-selftest.sh. Nothing else in this file knows the
# difference.
COORD="${QA_COORD:-http://87.107.110.199:7001}"

# ---------------------------------------------------------------- helpers

need_token() {
  if [ -n "${QA_TOKEN:-}" ]; then TOKEN="$QA_TOKEN"; return 0; fi
  [ -f "$SERVER_FILE" ] || { echo "missing $SERVER_FILE" >&2; exit 1; }
  local user_host pass
  user_host=$(grep -o 'ssh [^ ]*' "$SERVER_FILE" | awk '{print $2}')
  pass=$(grep -i '^password:' "$SERVER_FILE" | sed 's/^[Pp]assword:[[:space:]]*//' | tr -d '\r\n')
  # Read straight into the environment. The token is a secret and never
  # reaches a file in this repository.
  TOKEN=$("$PLINK" -ssh -batch -hostkey "$HOSTKEY" "$user_host" -pw "$pass" \
    'cat /etc/finallobby/api.token' < /dev/null | tr -d '\r\n')
  [ -n "$TOKEN" ] || { echo "could not read the API token from the server" >&2; exit 1; }
}

jf() { python -c "import sys,json
try: d=json.load(sys.stdin)
except Exception: d={}
print(d.get('$1','') if isinstance(d,dict) else '')"; }

# Every call carries the shared bearer token; calls made *as somebody* carry
# their session as well.
#
# Both ride the rate limiter rather than dying on it. Two dozen sign-ups from
# one address is exactly the shape the limiter exists to slow down, and the
# fixed waits below are a guess at its arithmetic - a guess that is wrong the
# moment somebody else is using the server from this building, or the owner
# has the app open while this runs. A "slow down" is not an error here, it is
# the server saying "later": wait and ask again. Anything else is returned
# untouched, so a real refusal still reads as one.
#
# Getting this wrong is expensive in a way most retries are not. The fixture
# writes accounts that cannot be deleted, so a run that dies two thirds of the
# way through leaves a half-built lobby and litter behind it.
_req() {  # _req SESSION-OR-EMPTY METHOD PATH [BODY]
  local sess="$1" m="$2" p="$3"; shift 3
  local try out
  for try in 1 2 3 4 5 6 7 8; do
    if [ -n "$sess" ]; then
      if [ $# -ge 1 ]; then
        out=$(curl -sS --max-time 25 -X "$m" -H "Authorization: Bearer $TOKEN" \
          -H "X-LobbyBaz-Session: $sess" -H 'Content-Type: application/json' -d "$1" "$COORD$p")
      else
        out=$(curl -sS --max-time 25 -X "$m" -H "Authorization: Bearer $TOKEN" \
          -H "X-LobbyBaz-Session: $sess" "$COORD$p")
      fi
    else
      if [ $# -ge 1 ]; then
        out=$(curl -sS --max-time 25 -X "$m" -H "Authorization: Bearer $TOKEN" \
          -H 'Content-Type: application/json' -d "$1" "$COORD$p")
      else
        out=$(curl -sS --max-time 25 -X "$m" -H "Authorization: Bearer $TOKEN" "$COORD$p")
      fi
    fi
    case "$out" in
      *'"slow down"'*) sleep $((try * 2)) ;;
      *) printf '%s' "$out"; return 0 ;;
    esac
  done
  printf '%s' "$out"
  return 0
}

anon() { local m="$1" p="$2"; shift 2; _req "" "$m" "$p" "$@"; }
as()   { local s="$1" m="$2" p="$3"; shift 3; _req "$s" "$m" "$p" "$@"; }

# The coordinator throttles sign-ups to five and then one every five seconds,
# keyed by address - and every one of these comes from this PC. Waiting is
# correct; a fixture that needed the limit lifted would not be testing the
# product. Joining and creating are throttled too, more gently.
SLOW_AUTH="${QA_SLOW_AUTH:-5}"
SLOW_JOIN="${QA_SLOW_JOIN:-2}"

# ---------------------------------------------------------------- roster

# nick, mmr. The order is the order they are created in.
ROSTER='
qa_open|QA Open Host|3400
qa_pass|QA Password Host|2900
qa_friends|QA Friends Host|4100
qa_invite|QA Invite Host|5200
qa_nine|QA Nine Host|3000
qa_game|QA In-Game Host|3600
qa_p01|QA Player 01|2400
qa_p02|QA Player 02|3900
qa_p03|QA Player 03|3100
qa_p04|QA Player 04|2700
qa_p05|QA Player 05|4400
qa_p06|QA Player 06|3300
qa_p07|QA Player 07|2100
qa_p08|QA Player 08|4800
qa_p09|QA Player 09|3500
qa_p10|QA Player 10|2800
qa_p11|QA Player 11|5100
qa_p12|QA Player 12|3200
qa_p13|QA Player 13|2600
qa_w01|QA Watcher 01|3700
qa_w02|QA Watcher 02|4000
qa_idle1|QA Idle 01|3050
qa_idle2|QA Idle 02|4600
qa_idle3|QA Idle 03|2200
'

# awk, not `grep -P`: this shell's grep refuses -P outside a UTF-8 locale,
# and it refuses it by matching nothing rather than by failing, so the first
# version of this looked up every session, found none, and sent two dozen
# requests with no session header on them.
sid()  { awk -F'\t' -v u="$1" '$1==u{print $2}' "$STATE" 2>/dev/null; }
pid_() { awk -F'\t' -v u="$1" '$1==u{print $3}' "$STATE" 2>/dev/null; }

# ---------------------------------------------------------------- build

cmd_up() {
  need_token
  echo "==> live server $COORD"

  local ver
  ver=$(anon GET /healthz | jf terms_version)
  [ -n "$ver" ] || { echo "the coordinator did not answer /healthz" >&2; exit 1; }
  echo "    terms version $ver"

  : > "$STATE"
  local n=0
  echo "==> accounts (the sign-up limiter means this takes a couple of minutes)"
  while IFS='|' read -r user nick mmr; do
    [ -n "$user" ] || continue
    n=$((n + 1))
    local out session player
    out=$(anon POST /v1/auth/signup "$(printf '{"username":"%s","display_name":"%s","password":"%s","device":"qa","accept_terms_version":"%s"}' \
      "$user" "$nick" "$QA_PASS" "$ver")")
    session=$(printf '%s' "$out" | jf session)
    if [ -z "$session" ]; then
      # Already there from a previous run: sign in instead. This is what
      # makes `up` safe to run twice.
      out=$(anon POST /v1/auth/login "$(printf '{"username":"%s","password":"%s","device":"qa"}' "$user" "$QA_PASS")")
      session=$(printf '%s' "$out" | jf session)
    fi
    player=$(printf '%s' "$out" | jf player_id)
    if [ -z "$session" ] || [ -z "$player" ]; then
      echo "    FAILED $user: $(printf '%s' "$out" | head -c 200)" >&2
      exit 1
    fi
    printf '%s\t%s\t%s\t%s\n' "$user" "$session" "$player" "$nick" >> "$STATE"
    as "$session" POST /v1/me "{\"mmr\":$mmr}" > /dev/null
    printf '    %-10s %s\n' "$user" "$nick"
    sleep "$SLOW_AUTH"
  done <<< "$(printf '%s' "$ROSTER" | sed '/^$/d')"
  echo "    $n accounts ready"

  # Somebody has to be answering for each host before its room is worth
  # anything, so the heartbeat starts before the rooms are built.
  echo "==> starting the heartbeat"
  cmd_keep_start

  echo "==> rooms"
  local r_open r_pass r_friends r_invite r_nine r_game

  r_open=$(mkroom qa_open   'QA - open, all pick'        public   '' 0    1); sleep "$SLOW_JOIN"
  r_pass=$(mkroom qa_pass   'QA - password is qa1234'    password "$ROOM_PASS" 0 23); sleep "$SLOW_JOIN"
  r_friends=$(mkroom qa_friends 'QA - friends only'      friends  '' 0    2); sleep "$SLOW_JOIN"
  r_invite=$(mkroom qa_invite   'QA - invite only'       invite   '' 0    3); sleep "$SLOW_JOIN"
  r_nine=$(mkroom qa_nine   'QA - nine of ten, one seat left' public '' 2000 1); sleep "$SLOW_JOIN"
  r_game=$(mkroom qa_game   'QA - match in progress'     public   '' 0    2); sleep "$SLOW_JOIN"

  for id in "$r_open" "$r_pass" "$r_friends" "$r_invite" "$r_nine" "$r_game"; do
    [ -n "$id" ] || { echo "a room was refused; stopping before the lobby is half built" >&2; exit 1; }
  done

  describe qa_open    "$r_open"    "Two players and two watchers. Join this one."
  describe qa_pass    "$r_pass"    "The password is qa1234."
  describe qa_friends "$r_friends" "Friends of the host only - you should be refused."
  describe qa_invite  "$r_invite"  "Invitation only - you should be refused."
  describe qa_nine    "$r_nine"    "One seat left, and a 2000 MMR floor."
  describe qa_game    "$r_game"    "Locked. No new player may join until the host reopens it."

  echo "==> seating players"
  seat qa_p01 "$r_open"
  seat qa_p02 "$r_open"
  watch qa_w01 "$r_open"
  watch qa_w02 "$r_open"
  for u in qa_p03 qa_p04 qa_p05 qa_p06 qa_p07 qa_p08 qa_p09 qa_p10; do
    seat "$u" "$r_nine"
  done
  seat qa_p11 "$r_game"
  seat qa_p12 "$r_game"
  seat qa_p13 "$r_game"

  echo "==> starting the match in the in-game room"
  as "$(sid qa_game)" POST "/v1/rooms/$r_game/status" '{"status":"locked_in_game"}' > /dev/null

  echo "==> a little lobby chat"
  say qa_open  "QA lobby is up - six rooms, every door."
  say qa_p01   "open room has space, come in"
  say qa_nine  "one seat left in mine"
  say qa_idle1 "just watching the lobby"

  echo
  cmd_status
  cat <<'NOTE'

  These rooms live only while the heartbeat runs on this PC.
    ./scripts/qa-lobby.sh status   see what is up
    ./scripts/qa-lobby.sh down     empty the rooms and stop

  Sign in as any of them in a second copy of the app:
    username  qa_p01 ... qa_p13, qa_open, qa_nine, qa_idle1 ...
    password  qa-test-account-2026
    room password for the locked-door room: qa1234
NOTE
}

mkroom() {  # mkroom USER NAME PRIVACY PASSWORD MINMMR MODE -> room id on stdout
  local s body out id
  s=$(sid "$1")
  body=$(python -c "
import json,sys
print(json.dumps({'name':sys.argv[1],'privacy':sys.argv[2],'password':sys.argv[3],
                  'min_mmr':int(sys.argv[4]),'game_mode':int(sys.argv[5])}))" \
    "$2" "$3" "$4" "$5" "$6")
  out=$(as "$s" POST /v1/rooms "$body")
  id=$(printf '%s' "$out" | jf room_id)
  if [ -z "$id" ]; then
    # This runs inside $( ), so exiting here ends the subshell and nothing
    # else - the caller checks for the empty string and stops for real.
    echo "    FAILED room $2: $(printf '%s' "$out" | head -c 200)" >&2
    return 1
  fi
  printf '    %-34s %s\n' "$2" "$id" >&2
  printf '%s' "$id"
}

describe() { as "$(sid "$1")" POST "/v1/rooms/$2/description" \
  "$(python -c "import json,sys;print(json.dumps({'description':sys.argv[1]}))" "$3")" > /dev/null; sleep 1; }

seat() {
  local out err
  out=$(as "$(sid "$1")" POST "/v1/rooms/$2/join" '{}')
  err=$(printf '%s' "$out" | jf error)
  printf '    %-10s -> %s %s\n' "$1" "$2" "${err:+REFUSED: $err}"
  [ -z "$err" ] || { echo "seating was refused; stopping" >&2; exit 1; }
  sleep "$SLOW_JOIN"
}

watch() {
  local out err
  out=$(as "$(sid "$1")" POST "/v1/rooms/$2/spectate" '{}')
  err=$(printf '%s' "$out" | jf error)
  printf '    %-10s -> %s (watching) %s\n' "$1" "$2" "${err:+REFUSED: $err}"
  [ -z "$err" ] || { echo "a watching seat was refused; stopping" >&2; exit 1; }
  sleep "$SLOW_JOIN"
}

say() {
  as "$(sid "$1")" POST /v1/chat \
    "$(python -c "import json,sys;print(json.dumps({'channel':'lobby','text':sys.argv[1]}))" "$2")" > /dev/null
  sleep 1
}

# ---------------------------------------------------------------- heartbeat

# One sync per player per cycle. Twelve seconds is comfortably inside the
# thirty the coordinator allows before it calls a host offline, and twenty-odd
# players at that cadence is about two requests a second against a limiter
# that allows five.
cmd_keep() {
  need_token
  while :; do
    while IFS=$'\t' read -r user session player nick; do
      [ -n "$session" ] || continue
      as "$session" POST /v1/sync \
        "$(python -c "import json,sys;print(json.dumps({'player_id':sys.argv[1],'nick':sys.argv[2]}))" \
          "$player" "$nick")" > /dev/null 2>&1
      sleep 0.3
    done < "$STATE"
    sleep 6
  done
}

cmd_keep_start() {
  cmd_keep_stop
  nohup bash "$0" keep > "$KEEPLOG" 2>&1 &
  echo $! > "$PIDFILE"
  echo "    heartbeat pid $(cat "$PIDFILE") - the rooms live only while it runs"
  sleep 2
}

cmd_keep_stop() {
  if [ -f "$PIDFILE" ]; then
    kill "$(cat "$PIDFILE")" 2>/dev/null
    rm -f "$PIDFILE"
  fi
  pkill -f "qa-lobby.sh keep" 2>/dev/null
  return 0
}

# ---------------------------------------------------------------- status

cmd_status() {
  need_token
  if [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
    echo "heartbeat: running (pid $(cat "$PIDFILE"))"
  else
    echo "heartbeat: STOPPED - any rooms below are closing within 30 seconds"
  fi
  anon GET /v1/rooms | python -c "
import sys, json
d = json.load(sys.stdin)
rooms = d.get('rooms') or d if isinstance(d, list) else d.get('rooms', [])
print()
print('  %-34s %-9s %-16s %-6s %s' % ('room', 'players', 'door', 'mmr', 'status'))
for r in rooms:
    door = r.get('privacy') or 'public'
    if r.get('needs_password'): door = 'password'
    print('  %-34s %-9s %-16s %-6s %s' % (
        (r.get('name') or '')[:34],
        '%s/10' % (r.get('seats') or 0),
        door,
        r.get('min_mmr') or '-',
        r.get('status') or ''))
print()
print('  %d rooms' % len(rooms))
"
}

# ---------------------------------------------------------------- teardown

cmd_down() {
  need_token
  echo "==> stopping the heartbeat"
  cmd_keep_stop
  if [ ! -s "$STATE" ]; then
    echo "    no state file; nothing to empty. Any rooms close on their own within 30s."
    return 0
  fi
  echo "==> emptying the rooms"
  # Everybody leaves whatever they are in. A host leaving closes the room
  # outright (D84), which is exactly what is wanted here.
  #
  # The field is in_room_id, not room_id: sync answers with the room the
  # SERVER has you seated in, deliberately named apart from the room_id the
  # client sends up as its belief (D82). Reading the wrong one is silent -
  # every player looks unseated and nobody leaves.
  while IFS=$'\t' read -r user session player nick; do
    [ -n "$session" ] || continue
    local where
    where=$(as "$session" POST /v1/sync \
      "$(python -c "import json,sys;print(json.dumps({'player_id':sys.argv[1],'nick':sys.argv[2]}))" \
        "$player" "$nick")" | jf in_room_id)
    if [ -n "$where" ]; then
      as "$session" POST "/v1/rooms/$where/leave" '{}' > /dev/null
      printf '    %-10s left %s\n' "$user" "$where"
      sleep 0.5
    fi
  done < "$STATE"
  echo
  cmd_status
  echo
  echo "  The accounts remain in the live database - there is no API that"
  echo "  deletes a player. They are all named qa_* if you want them gone."
}

# ---------------------------------------------------------------- main

case "${1:-}" in
  up)     cmd_up ;;
  keep)   cmd_keep ;;
  status) cmd_status ;;
  down)   cmd_down ;;
  *)      sed -n '2,12p' "$0" | sed 's/^# \?//'; exit 2 ;;
esac
