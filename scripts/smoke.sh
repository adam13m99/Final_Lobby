#!/usr/bin/env bash
# End-to-end smoke test: a real coordinator, a real app, one player.
#
# `check.sh` proves each module compiles and its own tests pass. It cannot
# prove the app can sign in, because that crosses four packages, a database,
# an HTTP API and a session file - exactly the seam where T5's accounts and
# T11's sign-in screen were written months apart and never met.
#
# This starts a coordinator with a throwaway database, builds the app pointed
# at it, and walks the path a new player walks: browse without an account,
# read the terms, create an account, make a room, be in it. Everything it
# creates lives in one temporary directory and is deleted on the way out.
#
# It never touches the live server. Both processes bind loopback only.
set -uo pipefail

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/env.sh
. scripts/env.sh

FAIL=0
ok()   { printf '  OK    %s\n' "$*"; }
bad()  { printf '  FAIL  %s\n' "$*"; FAIL=1; }
say()  { printf '%s\n' "$*"; }

WORK=$(mktemp -d)
COORD_PID=""
APP_PID=""
APP_B_PID=""

cleanup() {
  [ -n "$APP_B_PID" ] && kill "$APP_B_PID" 2>/dev/null
  [ -n "$APP_PID" ]   && kill "$APP_PID"   2>/dev/null
  [ -n "$COORD_PID" ] && kill "$COORD_PID" 2>/dev/null
  sleep 1
  # Windows keeps the SQLite file open for a moment after the process dies;
  # a leftover temp directory is not worth failing a passing test over.
  rm -rf "$WORK" 2>/dev/null
}
trap cleanup EXIT

# A free port, asked for rather than assumed: the dev box also runs the real
# thing on 7001 sometimes, and a smoke test that quietly talks to it would be
# worse than no smoke test.
free_port() {
  python -c "import socket;s=socket.socket();s.bind(('127.0.0.1',0));print(s.getsockname()[1]);s.close()"
}

# expect NAME EXPECTED-SUBSTRING ACTUAL
expect() {
  case "$3" in
    *"$2"*) ok "$1" ;;
    *)      bad "$1"; printf '        wanted %s\n        got    %s\n' "$2" "$(printf '%s' "$3" | head -c 300)" ;;
  esac
}

# refuse NAME UNWANTED-SUBSTRING ACTUAL
refuse() {
  case "$3" in
    *"$2"*) bad "$1"; printf '        must not contain %s\n' "$2" ;;
    *)      ok "$1" ;;
  esac
}

say "=== building ==="
PORT=$(free_port)
COORD="http://127.0.0.1:$PORT"

# The relay key the coordinator hands to clients. A throwaway one: this test
# never opens a tunnel, and the real key must never leave the server.
python -c "import secrets;print(secrets.token_hex(32))" > "$WORK/relay.pub"

if (cd coordinator && go build -o "$WORK/coordinator.exe" ./cmd/coordinator); then
  ok "built the coordinator"
else
  bad "built the coordinator"; exit 1
fi

if (cd lobbyapp && go build -ldflags "-X lobbybaz/client/build.Coordinator=$COORD" \
      -o "$WORK/lobbyapp.exe" .); then
  ok "built the app, pointed at $COORD"
else
  bad "built the app"; exit 1
fi

say ""
say "=== the coordinator, with accounts switched on ==="
# -db is the flag the live server does not have set yet. This is the only
# place it runs, so this is the only place the account path is exercised.
"$WORK/coordinator.exe" \
  -listen "127.0.0.1:$PORT" \
  -db "$WORK/smoke.db" \
  -relay-pub "$WORK/relay.pub" \
  -terms-file docs/terms-en.md \
  -tick 1s > "$WORK/coordinator.log" 2>&1 &
COORD_PID=$!

for _ in $(seq 1 40); do
  curl -fsS "$COORD/healthz" >/dev/null 2>&1 && break
  sleep 0.25
done

HEALTH=$(curl -fsS "$COORD/healthz" 2>&1)
expect "the coordinator answers /healthz"   '"ok"'                "$HEALTH"
expect "it reports accounts are available"  '"accounts":true'     "$HEALTH"
expect "it reports friends are available"   '"friends":true'      "$HEALTH"
expect "it names a terms version"           '"terms_version"'     "$HEALTH"

TERMS=$(curl -fsS "$COORD/v1/terms" 2>&1)
expect "GET /v1/terms serves the file"      'LobbyBaz'            "$TERMS"
refuse "and not the misconfigured notice"   'not been configured' "$TERMS"

# The thing D45 insists on: somebody who has installed nothing and signed up
# for nothing can still see what is going on.
BROWSE=$(curl -fsS -X POST -H 'Content-Type: application/json' -d '{}' "$COORD/v1/sync" 2>&1)
expect "an anonymous sync is allowed"       '"rooms"'             "$BROWSE"
refuse "and carries nothing personal"       '"friends"'           "$BROWSE"

say ""
say "=== the app ==="
# APPDATA is redirected so this test cannot overwrite the developer's own
# session file - which holds a real player id, and on this machine may hold a
# real signed-in session.
export APPDATA="$(cygpath -w "$WORK" 2>/dev/null || echo "$WORK")"
"$WORK/lobbyapp.exe" -no-browser -url-only -listen 127.0.0.1:0 > "$WORK/app.log" 2>&1 &
APP_PID=$!

APP_URL=""
for _ in $(seq 1 40); do
  APP_URL=$(head -1 "$WORK/app.log" 2>/dev/null)
  case "$APP_URL" in http://*) break ;; esac
  sleep 0.25
done

case "$APP_URL" in
  http://*) ok "the app printed its address" ;;
  *)        bad "the app printed its address"; say "$(cat "$WORK/app.log")"; exit 1 ;;
esac

APP=${APP_URL%%/?t=*}
TOK=${APP_URL##*t=}

# call METHOD PATH [BODY]
call() {
  if [ $# -ge 3 ]; then
    curl -sS -X "$1" -H "X-Lobby-Token: $TOK" -H 'Content-Type: application/json' \
      -d "$3" "$APP$2" 2>&1
  else
    curl -sS -X "$1" -H "X-Lobby-Token: $TOK" "$APP$2" 2>&1
  fi
}

say ""
say "=== what a new player does ==="
STATE=$(call GET "/api/state")
expect "the app has state before anyone signs in"  '"player_id"'   "$STATE"
expect "and knows this server has accounts"        '"accounts":true' "$STATE"
expect "and has nobody signed in"                  '"named":false' "$STATE"

APPTERMS=$(call GET "/api/terms")
expect "the app proxies the terms"                 'LobbyBaz'      "$APPTERMS"
refuse "and not the misconfigured notice"          'not been configured' "$APPTERMS"

VER=$(printf '%s' "$HEALTH" | python -c "import sys,json;print(json.load(sys.stdin)['terms_version'])")
SIGNUP=$(call POST /api/auth/signup \
  "{\"username\":\"smoketester\",\"display_name\":\"Smoke Tester\",\"password\":\"correct horse battery\",\"terms_version\":\"$VER\"}")
expect "signing up succeeds"                       '"ok":true'     "$SIGNUP"

STATE=$(call GET "/api/state")
expect "the app is now signed in"                  '"named":true'  "$STATE"
expect "under the name that was chosen"            'Smoke Tester'  "$STATE"

# The account id, not the installation's random id: this is the whole point of
# D53, and the one thing a session file could plausibly get wrong.
PID=$(printf '%s' "$STATE" | python -c "import sys,json;print(json.load(sys.stdin)['player_id'])")
case "$PID" in
  p_*) bad "the player id became the account id (still $PID)" ;;
  "")  bad "the player id became the account id (empty)" ;;
  *)   ok  "the player id became the account id" ;;
esac

ROOM=$(call POST /api/rooms/create '{"name":"Smoke Room"}')
expect "a signed-in player can open a room"        '"ok":true'     "$ROOM"

STATE=$(call GET "/api/state")
expect "and is in it"                              '"is_host":true' "$STATE"
ROOM_ID=$(printf '%s' "$STATE" | python -c "import sys,json;print(json.load(sys.stdin)['room_id'])")
# The app has to know whether this account is on the terms currently in
# force, or it cannot ask somebody to accept the new ones.
expect "the app knows the terms were accepted"     '"terms_accepted":true' "$STATE"

SYNC=$(curl -sS -X POST -H 'Content-Type: application/json' -d '{}' "$COORD/v1/sync" 2>&1)
expect "the room is visible to a stranger"         'Smoke Room'    "$SYNC"


say ""
say "=== a player's own launch options ==="
# The parser lives in protocol/launch and both ends use it. This proves the
# app's half: the field accepts what Dota players actually type, refuses the
# console commands LobbyBaz sets itself, and the answer survives a poll (D65).
OPTS=$(call POST /api/launchoptions '{"options":"-novid -high -language english"}')
expect "ordinary engine flags are accepted"        '"ok":true'     "$OPTS"

STATE=$(call GET "/api/state")
expect "and state carries them back"  '"launch_options":"-novid -high -language english"' "$STATE"

BADOPT=$(call POST /api/launchoptions '{"options":"-novid +exec evil.cfg"}')
refuse "a console command among them is refused"   '"ok":true'     "$BADOPT"
expect "and says why, in words"                    'console command' "$BADOPT"

NOTFLAG=$(call POST /api/launchoptions '{"options":"+connect 1.2.3.4:27015"}')
refuse "so is one on its own"                      '"ok":true'     "$NOTFLAG"

STATE=$(call GET "/api/state")
expect "and neither overwrote the good one" '"launch_options":"-novid -high -language english"' "$STATE"

EMPTYOPT=$(call POST /api/launchoptions '{"options":""}')
expect "and they can be cleared again"             '"ok":true'     "$EMPTYOPT"

say ""
say "=== which notifications this PC wants ==="
# The tray process reads these off /api/state on every poll, so what matters
# is that state answers with all five, resolved rather than null: a null there
# means "none of them" to the tray, and an installation that had never been
# asked would go quiet (D66).
STATE=$(call GET "/api/state")
expect "a fresh installation wants all of them"    '"room_opens":true'   "$STATE"
expect "including the two it already had"          '"match_starts":true' "$STATE"

OFF=$(call POST /api/notifications \
  '{"room_full":true,"match_starts":true,"room_opens":false,"friend_online":false,"tunnel_drops":true}')
expect "the set can be saved"                      '"ok":true'     "$OFF"

STATE=$(call GET "/api/state")
expect "and state reports what was switched off"   '"room_opens":false'  "$STATE"
expect "and what was left on"                      '"tunnel_drops":true' "$STATE"

say ""
say "=== what the page actually draws ==="
# Everything above this point proves the HTTP layer. None of it opens the
# page, and a single stray brace in app.js gives the player a blank window
# with every check above still green.
#
# Chrome is driven headless, in a throwaway profile inside the temp
# directory, so the developer's own browser and its sessions are untouched.
# Two things are asserted, and the second is the interesting one:
#
#   - the interface drew, and drew live data - the room hosted a moment ago
#     appears in the list, which can only happen if the page fetched state
#     and rendered it;
#   - the console said nothing at all. That catches uncaught exceptions, and
#     it also catches i18n's "missing key" warning, which is the failure a
#     translated interface actually has (D44).
CHROME=""
for c in "/c/Program Files/Google/Chrome/Application/chrome.exe"          "/c/Program Files (x86)/Google/Chrome/Application/chrome.exe"          "$LOCALAPPDATA/Google/Chrome/Application/chrome.exe"          "/c/Program Files (x86)/Microsoft/Edge/Application/msedge.exe"; do
  [ -x "$c" ] && { CHROME="$c"; break; }
done

if [ -z "$CHROME" ]; then
  say "  WARN  skipping - no Chrome or Edge found to render with"
else
  PROFILE=$(cygpath -w "$WORK" 2>/dev/null || echo "$WORK")
fi

# draw TAG URL WANTED - render one page and check it, or say why not.
draw() {
  if [ -z "$CHROME" ]; then return 0; fi
  "$CHROME" --headless --disable-gpu --no-first-run --no-default-browser-check     --user-data-dir="$PROFILE\chrome" --virtual-time-budget=8000     --enable-logging=stderr --v=0 --dump-dom "$2"     > "$WORK/$1.html" 2> "$WORK/$1.err"

  local dom
  dom=$(cat "$WORK/$1.html" 2>/dev/null)
  if [ "$(printf '%s' "$dom" | wc -c)" -gt 4000 ]; then
    ok "$1: the page renders"
  else
    bad "$1: the page renders"
  fi
  expect "$1: and drew live data" "$3" "$dom"

  # An element the interface left empty is a key nobody wrote a string for.
  local blank
  blank=$(printf '%s' "$dom" | grep -c "data-t=\"[a-z0-9._]*\"></" || true)
  if [ "$blank" -eq 0 ]; then
    ok "$1: every translated element has words in it"
  else
    bad "$1: $blank translated elements are empty"
  fi

  local noise
  noise=$(grep ":CONSOLE:" "$WORK/$1.err" 2>/dev/null)
  if [ -z "$noise" ]; then
    ok "$1: the console said nothing"
  else
    bad "$1: the console complained"
    printf '%s
' "$noise" | sed "s/^/        /" | head -10
  fi
}

draw player "$APP_URL" "Smoke Room"
if [ -n "$CHROME" ]; then
  # An ordinary player must not be offered the moderation tools. The
  # coordinator refuses them anyway; showing an entry that always says no is
  # a question somebody asks instead of playing.
  PTAB=$(grep -o '<button[^>]*id="modtab"' "$WORK/player.html" 2>/dev/null | head -1)
  case "$PTAB" in
    *hidden*) ok "an ordinary player is not offered the moderation tools" ;;
    *)        bad "an ordinary player is offered the moderation tools" ;;
  esac

  # The chat is docked along the bottom and always present, open when the app
  # starts and minimising to its tab strip when the player says so (D56, D58).
  # It was a panel in the rail before, which is why the rail had no room for
  # friends. That it reopens itself on an incoming message is proved by
  # scripts/chatcheck.sh, which needs two polls and a live page to see it.
  DOM=$(cat "$WORK/player.html" 2>/dev/null)
  DOCK=$(grep -o '<section[^>]*id="chatdock"[^>]*>' "$WORK/player.html" 2>/dev/null | head -1)
  case "$DOCK" in
    "") bad "the chat dock is missing" ;;
    *collapsed*) bad "the chat dock starts minimised: $DOCK" ;;
    *)  ok "the chat is docked along the bottom, and open" ;;
  esac
  expect "with a way to minimise it" 'id="chatmin"' "$DOM"
  expect "with a tab for the lobby"   'data-chat="lobby"' "$DOM"
  expect "and one for the room"       'data-chat="room"'  "$DOM"
  expect "and a way to start another" 'id="chatadd"'      "$DOM"
fi

say ""
say "=== a second player, and the friend graph ==="
# T7 built friends, blocks and private messages on the coordinator. Nothing
# had ever exercised them from the app, which is where they are used.
#
# The second app gets its own APPDATA, because a session file is per
# installation and two players sharing one would be one player.
mkdir -p "$WORK/b"
APPDATA="$(cygpath -w "$WORK/b" 2>/dev/null || echo "$WORK/b")" \
  "$WORK/lobbyapp.exe" -no-browser -url-only -listen 127.0.0.1:0 > "$WORK/appb.log" 2>&1 &
APP_B_PID=$!

APP_B_URL=""
for _ in $(seq 1 40); do
  APP_B_URL=$(head -1 "$WORK/appb.log" 2>/dev/null)
  case "$APP_B_URL" in http://*) break ;; esac
  sleep 0.25
done
case "$APP_B_URL" in
  http://*) ok "the second app started" ;;
  *)        bad "the second app started"; say "$(cat "$WORK/appb.log")" ;;
esac
APP_B=${APP_B_URL%%/?t=*}
TOK_B=${APP_B_URL##*t=}

callb() {
  if [ $# -ge 3 ]; then
    curl -sS -X "$1" -H "X-Lobby-Token: $TOK_B" -H 'Content-Type: application/json' \
      -d "$3" "$APP_B$2" 2>&1
  else
    curl -sS -X "$1" -H "X-Lobby-Token: $TOK_B" "$APP_B$2" 2>&1
  fi
}

B_SIGNUP=$(callb POST /api/auth/signup \
  "{\"username\":\"smokefriend\",\"display_name\":\"Smoke Friend\",\"password\":\"a different long one\",\"terms_version\":\"$VER\"}")
expect "the second player signs up"          '"ok":true'      "$B_SIGNUP"

B_STATE=$(callb GET "/api/state")
B_ID=$(printf '%s' "$B_STATE" | python -c "import sys,json;print(json.load(sys.stdin)['player_id'])")

FOUND=$(callb GET "/api/players/find?username=smoketester")
expect "one player can find the other by name" "$PID"         "$FOUND"

REQ=$(callb POST /api/friends "{\"action\":\"request\",\"target_id\":\"$PID\"}")
expect "a friend request is sent"            '"ok":true'      "$REQ"

ACC=$(call POST /api/friends "{\"action\":\"accept\",\"target_id\":\"$B_ID\"}")
expect "and accepted"                        '"ok":true'      "$ACC"

FRIENDS=$(call GET "/api/state")
expect "the friend appears in the rail"      'Smoke Friend'   "$FRIENDS"

DM=$(call POST /api/friends/messages "{\"target_id\":\"$B_ID\",\"send\":\"hello from the smoke test\"}")
expect "a private message is accepted"       'hello from the smoke test' "$DM"

DM_B=$(callb POST /api/friends/messages "{\"target_id\":\"$PID\"}")
expect "and arrives at the other end"        'hello from the smoke test' "$DM_B"

say ""
say "=== the door on a room ==="
# T6 built four doors and an MMR floor on the coordinator (D41). Until now no
# host could choose one: the app created every room public and had no control
# for changing it, so the padlock the lobby draws could never appear.
DOOR=$(call POST /api/rooms/privacy '{"privacy":"password","password":"open sesame"}')
expect "a host can put a password on their room"  '"needs_password":true' "$DOOR"

NOPASS=$(callb POST /api/rooms/join "{\"room_id\":\"$ROOM_ID\"}")
refuse "and a stranger without it is refused"     '"ok":true'    "$NOPASS"

WITHPASS=$(callb POST /api/rooms/join "{\"room_id\":\"$ROOM_ID\",\"password\":\"open sesame\"}")
expect "and one with it gets in"                  '"ok":true'    "$WITHPASS"

LEFT=$(callb POST /api/rooms/leave '{}')
expect "and can leave again"                      '"ok":true'    "$LEFT"

EMPTY=$(call POST /api/rooms/privacy '{"privacy":"password","password":""}')
refuse "a password door with no password is refused" '"needs_password"' "$EMPTY"

FLOOR=$(call POST /api/rooms/privacy '{"privacy":"public","min_mmr":3000}')
expect "a host can set an MMR floor"              '"min_mmr":3000' "$FLOOR"

INVONLY=$(call POST /api/rooms/privacy '{"privacy":"invite"}')
expect "a host can make a room invite-only"       '"privacy":"invite"' "$INVONLY"

UNINVITED=$(callb POST /api/rooms/join "{\"room_id\":\"$ROOM_ID\"}")
refuse "an uninvited player is refused"           '"ok":true'    "$UNINVITED"

# One word, two things: tell them to come, and let them through the door.
# Doing only the first is how somebody is invited and then refused (D41).
INVITE=$(call POST /api/friends/invite "{\"target_id\":\"$B_ID\"}")
expect "inviting a friend opens the door to them" '"ok":true'    "$INVITE"

INVITED=$(callb POST /api/rooms/join "{\"room_id\":\"$ROOM_ID\"}")
expect "and then they get in"                     '"ok":true'    "$INVITED"

LEFT=$(callb POST /api/rooms/leave '{}')
expect "and can leave again"                      '"ok":true'    "$LEFT"

BACK=$(call POST /api/rooms/privacy '{"privacy":"public"}')
expect "and open the door again"                  '"privacy":"public"' "$BACK"

say ""
say "=== picking a side ==="
# Which slot you sit in is which team you are on: 0-4 are Radiant, 5-9 Dire.
# Clicking an empty seat is therefore how a player picks a side, and it
# replaced a dropdown that could disagree with the seat.
REJOIN=$(callb POST /api/rooms/join "{\"room_id\":\"$ROOM_ID\"}")
expect "the second player is back in the room"    '"ok":true'    "$REJOIN"

MOVED=$(callb POST /api/rooms/slot '{"slot":7}')
expect "a player can move to a free seat"         '"ok":true'    "$MOVED"

SEATED=$(call GET "/api/state")
expect "and the room says they are sitting there" "\"slot\":7" "$SEATED"

# The host picks a side like anybody else (D64), and the address the room is
# reached at follows them. This is the assertion that matters: after the move,
# the other player must be pointed at where the host actually is, or Dota
# connects to an empty address and nobody can tell why.
HOSTMOVE=$(call POST /api/rooms/slot '{"slot":4}')
expect "the host can pick a side too"             '"ok":true'    "$HOSTMOVE"

HOSTAT=$(call GET "/api/state")
expect "and the room seats them there"            '"slot":4'     "$HOSTAT"

HOSTIP=$(printf '%s' "$HOSTAT" | grep -o '"host_ip":"[^"]*"' | head -1 | cut -d'"' -f4)
MATEVIEW=$(callb GET "/api/state")
MATEHOST=$(printf '%s' "$MATEVIEW" | grep -o '"host_ip":"[^"]*"' | head -1 | cut -d'"' -f4)
if [ -n "$HOSTIP" ] && [ "$HOSTIP" = "$MATEHOST" ]; then
  ok "and everybody is told the host's new address"
else
  bad "the host moved to $HOSTIP but the other player is still sent to $MATEHOST"
fi

# The seat the host got up from is an ordinary seat now, not a reserved one.
FREED=$(callb POST /api/rooms/slot '{"slot":0}')
expect "the seat the host left can be taken"      '"ok":true'    "$FREED"

# The one seat the host still cannot have: the match runs on their machine.
HOSTWATCH=$(call POST /api/rooms/spectate "{\"room_id\":\"$ROOM_ID\"}")
refuse "the host cannot watch their own room"     '"ok":true'    "$HOSTWATCH"

OFFEND=$(callb POST /api/rooms/slot '{"slot":10}')
refuse "there is no eleventh seat"                '"ok":true'    "$OFFEND"

# Five seats below the two teams, for people who want to watch rather than
# play (D59). They were a strip of text with no way to become one.
#
# Watching and playing are two doors into the same room, so a player takes
# their playing seat with them when they go to watch: they get up first.
STANDUP=$(callb POST /api/rooms/leave '{}')
expect "a player gets up from their playing seat" '"ok":true'    "$STANDUP"
# Joining, watching and rejoining all draw on the same bucket, and a refusal
# spends a token exactly as an acceptance does. Let it refill, or the next
# line fails as "going too fast" and reads like a broken feature.
sleep 4
WATCH=$(callb POST /api/rooms/spectate "{\"room_id\":\"$ROOM_ID\"}")
expect "a player can take a seat to watch from"   '"ok":true'    "$WATCH"

WATCHING=$(callb GET "/api/state")
expect "and the room seats them as an observer"   '"spectator":true' "$WATCHING"
expect "in the observers' own range"              '"seat":"observer"' "$WATCHING"

# Their own distance from the relay, reported on their heartbeat and read
# back beside their seat (D59). Nobody else can measure it for them.
PINGED=$(callb GET "/api/state")
case "$PINGED" in
  *'"relay_ms"'*) ok "a seat carries the player's own ping when they have one" ;;
  *) ok "no ping is claimed for a player who never measured one" ;;
esac

BACK=$(callb POST /api/rooms/leave '{}')
expect "and they can get up again"                '"ok":true'    "$BACK"
# The coordinator throttles joining, and this section has just used three
# doors in a row. Waiting is correct: a smoke test that needed the limit
# lifted would be testing a server nobody runs.
sleep 3
REJOIN2=$(callb POST /api/rooms/join "{\"room_id\":\"$ROOM_ID\"}")
expect "and sit back down to play"                '"ok":true'    "$REJOIN2"

# A locked room is a match already running. Changing team halfway through it
# puts a player on the wrong team inside Dota, which nothing here can undo.
LOCK=$(call POST /api/rooms/status '{"status":"locked_in_game"}')
expect "the host can start the match"             '"ok":true'    "$LOCK"

MIDGAME=$(callb POST /api/rooms/slot '{"slot":3}')
refuse "seats do not move during a match"         '"ok":true'    "$MIDGAME"

UNLOCK=$(call POST /api/rooms/status '{"status":"open"}')
expect "and the host can open it again"           '"ok":true'    "$UNLOCK"

GONE=$(callb POST /api/rooms/leave '{}')
expect "the second player leaves again"           '"ok":true'    "$GONE"

say ""
say "=== the host leaving ==="
# The owner's report, D70: they left a room and it was still sitting in the
# lobby afterwards, open, and they could walk back into it as its host. The
# grace period is for a host who disappeared, not for one who pressed Leave.
HOSTOUT=$(call POST /api/rooms/leave '{}')
expect "the host can leave their own room"        '"ok":true'    "$HOSTOUT"

sleep 3
LOBBY=$(call GET "/api/state")
refuse "and the room goes with them"              "$ROOM_ID"     "$LOBBY"

BACKIN=$(call POST /api/rooms/join "{\"room_id\":\"$ROOM_ID\"}")
refuse "and nobody can walk back into it"         '"ok":true'    "$BACKIN"

# Left until here because signing out forgets which room this installation
# was in, and the door checks above need it to still know.
SIGNOUT=$(call POST /api/auth/signout '{}')
expect "signing out succeeds"                     '"ok":true'    "$SIGNOUT"

SIGNIN=$(call POST /api/auth/signin '{"username":"smoketester","password":"correct horse battery"}')
expect "signing back in succeeds"                 '"ok":true'    "$SIGNIN"

WRONG=$(call POST /api/auth/signin '{"username":"smoketester","password":"not the password"}')
refuse "a wrong password does not"                '"ok":true'    "$WRONG"

# The one lever somebody has when they think another person knows their
# password. The sign-up screen says plainly that a forgotten one cannot be
# recovered, which makes this the whole of account security.
sleep 5
BADOLD=$(call POST /api/auth/password '{"current":"not it","next":"a brand new long one"}')
refuse "changing a password needs the old one"    '"ok":true'    "$BADOLD"

# The coordinator throttles authentication to five attempts and then one
# every five seconds, which is the whole defence against somebody guessing a
# password. This section makes seven, so it waits between them. Waiting is
# the correct thing to do: a smoke test that needed the limit lifted would be
# testing a server nobody runs.
sleep 5
CHANGED=$(call POST /api/auth/password '{"current":"correct horse battery","next":"a brand new long one"}')
expect "and works with it"                        '"ok":true'    "$CHANGED"

STILLIN=$(call GET "/api/state")
expect "the window stays signed in through it"    '"signed_in":true' "$STILLIN"

sleep 5
OLDPW=$(call POST /api/auth/signin '{"username":"smoketester","password":"correct horse battery"}')
refuse "the old password stops working"           '"ok":true'    "$OLDPW"

sleep 5
NEWPW=$(call POST /api/auth/signin '{"username":"smoketester","password":"a brand new long one"}')
expect "and the new one works"                    '"ok":true'    "$NEWPW"

say ""
say "=== moderation ==="
# T8's roles, sanctions, labels, banners and audit log had the same shape of
# gap accounts had: built on the coordinator, reachable from nothing that
# ships. This is the first thing that walks them from the app.
#
# The head admin is named on the command line at deployment (D47), so the
# coordinator is restarted to appoint one - which also tests that a session
# survives a restart, because the app is expected to still be signed in
# afterwards. The app is restarted with it, because a role is cached for two
# minutes on purpose and nobody is appointed twice in one sitting.
kill "$APP_PID" 2>/dev/null; APP_PID=""
kill "$COORD_PID" 2>/dev/null; COORD_PID=""
sleep 1

"$WORK/coordinator.exe" \
  -listen "127.0.0.1:$PORT" \
  -db "$WORK/smoke.db" \
  -relay-pub "$WORK/relay.pub" \
  -terms-file docs/terms-en.md \
  -head-admin "$PID" \
  -tick 1s >> "$WORK/coordinator.log" 2>&1 &
COORD_PID=$!
for _ in $(seq 1 40); do
  curl -fsS "$COORD/healthz" >/dev/null 2>&1 && break
  sleep 0.25
done

APPDATA="$(cygpath -w "$WORK" 2>/dev/null || echo "$WORK")" \
  "$WORK/lobbyapp.exe" -no-browser -url-only -listen 127.0.0.1:0 > "$WORK/app2.log" 2>&1 &
APP_PID=$!
APP_URL=""
for _ in $(seq 1 40); do
  APP_URL=$(head -1 "$WORK/app2.log" 2>/dev/null)
  case "$APP_URL" in http://*) break ;; esac
  sleep 0.25
done
APP=${APP_URL%%/?t=*}
TOK=${APP_URL##*t=}

STATE=$(call GET "/api/state")
expect "the session survived both restarts"  '"signed_in":true' "$STATE"
expect "and the account is now head admin"   '"role":"head_admin"' "$STATE"

REC=$(call GET "/api/admin/player?username=smokefriend")
expect "staff can read a player's record"    'Smoke Friend'   "$REC"

BAN=$(call POST /api/admin/sanction \
  "{\"target_id\":\"$B_ID\",\"kind\":\"mute\",\"reason\":\"smoke test\",\"minutes\":15}")
expect "a sanction is applied"               '"kind":"mute"'  "$BAN"

NOREASON=$(call POST /api/admin/sanction \
  "{\"target_id\":\"$B_ID\",\"kind\":\"mute\",\"reason\":\"\",\"minutes\":15}")
refuse "an unexplained one is refused"       '"kind"'         "$NOREASON"

REC=$(call GET "/api/admin/player?username=smokefriend")
expect "the record shows it"                 'smoke test'     "$REC"
expect "and says what they are barred from"  '"muted":true'   "$REC"

SID=$(printf '%s' "$REC" | python -c "import sys,json;print(json.load(sys.stdin)['sanctions'][0]['id'])")
LIFT=$(call POST /api/admin/sanction/lift "{\"sanction_id\":\"$SID\",\"target_id\":\"$B_ID\"}")
expect "it can be lifted"                    '"ok":true'      "$LIFT"

REC=$(call GET "/api/admin/player?username=smokefriend")
refuse "and the mute is gone"                '"muted":true'   "$REC"
expect "while the record of it remains"      'smoke test'     "$REC"

LOG=$(call GET "/api/admin/log?subject=$B_ID")
# A sanction is logged under its own kind - "mute", not "sanction" - so the
# log reads as what happened rather than as a category.
expect "the audit log recorded the mute"     '"action":"mute"' "$LOG"
expect "and recorded the lift"               '"action":"lift"' "$LOG"

AD=$(call POST /api/admin/banners \
  '{"title":"Smoke announcement","body":"posted by the smoke test","active":true}')
expect "an announcement can be posted"       'Smoke announcement' "$AD"

# Read from the coordinator rather than from the second app: the strip is
# cached for five minutes there on purpose, and an announcement that appeared
# instantly would mean that cache was not working.
ADS=$(curl -fsS "$COORD/v1/banners" 2>&1)
expect "and everybody can read it"           'Smoke announcement' "$ADS"

APPOINT=$(call POST /api/admin/staff "{\"target_id\":\"$B_ID\",\"grant\":true}")
expect "the head admin can appoint an admin"  '"ok":true'      "$APPOINT"

# Read from the head admin's own state, not the new admin's: a role is
# cached for two minutes on every client, so somebody appointed while their
# window is open sees their tools within two minutes rather than instantly.
# That is the right trade for a list every signed-in client would otherwise
# refetch constantly, and it is worth knowing about.
A_STATE=$(call GET "/api/state")
expect "and the staff list says so"           '"role":"admin"' "$A_STATE"

REVOKE=$(call POST /api/admin/staff "{\"target_id\":\"$B_ID\",\"grant\":false}")
expect "and can take it back"                 '"ok":true'      "$REVOKE"

NOTSTAFF=$(callb GET "/api/admin/player?username=smoketester")
refuse "a player without a role cannot"      'Smoke Tester'   "$NOTSTAFF"

say ""
say "=== what a moderator sees ==="
# The same page again, now signed in as the head admin. The moderation entry
# is drawn from the role the state reply carries, so this is the only check
# that the whole chain - staff list, role, hidden toolbar entry, panel -
# agrees with itself.
draw moderator "$APP_URL" "Smoke Friend"
if [ -n "$CHROME" ]; then
  MODDOM=$(cat "$WORK/moderator.html" 2>/dev/null)
  MODTAB=$(printf '%s' "$MODDOM" | grep -o '<button[^>]*id="modtab"' | head -1)
  case "$MODTAB" in
    "")        bad "the moderation entry is missing entirely" ;;
    *hidden*)  bad "the moderation entry is still hidden from a head admin" ;;
    *)         ok "the moderation entry is in the toolbar" ;;
  esac
  expect "and the staff panel is drawn"  "Head admin"  "$MODDOM"
fi

say ""
if [ "$FAIL" -eq 0 ]; then
  say "RESULT: the path a new player walks works end to end"
else
  say "RESULT: PROBLEMS FOUND"
  say ""
  say "--- coordinator log ---"; tail -20 "$WORK/coordinator.log"
  say "--- app log ---";         tail -20 "$WORK/app.log"
fi
exit "$FAIL"
