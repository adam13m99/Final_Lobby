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

cleanup() {
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

SYNC=$(curl -sS -X POST -H 'Content-Type: application/json' -d '{}' "$COORD/v1/sync" 2>&1)
expect "the room is visible to a stranger"         'Smoke Room'    "$SYNC"

SIGNOUT=$(call POST /api/auth/signout '{}')
expect "signing out succeeds"                      '"ok":true'     "$SIGNOUT"

SIGNIN=$(call POST /api/auth/signin '{"username":"smoketester","password":"correct horse battery"}')
expect "signing back in succeeds"                  '"ok":true'     "$SIGNIN"

WRONG=$(call POST /api/auth/signin '{"username":"smoketester","password":"not the password"}')
refuse "a wrong password does not"                 '"ok":true'     "$WRONG"

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
  "$CHROME" --headless --disable-gpu --no-first-run --no-default-browser-check     --user-data-dir="$PROFILE\chrome" --virtual-time-budget=8000     --enable-logging=stderr --v=0 --dump-dom "$APP_URL"     > "$WORK/dom.html" 2> "$WORK/chrome.err"

  DOM=$(cat "$WORK/dom.html" 2>/dev/null)
  if [ "$(printf '%s' "$DOM" | wc -c)" -gt 4000 ]; then
    ok "the page renders"
  else
    bad "the page renders"
  fi
  expect "and drew the room list from live state"  "Smoke Room"  "$DOM"

  # An element the interface left empty is a key nobody wrote a string for.
  BLANK=$(printf '%s' "$DOM" | grep -c "data-t=\"[a-z0-9._]*\"></" || true)
  if [ "$BLANK" -eq 0 ]; then
    ok "every translated element has words in it"
  else
    bad "$BLANK translated elements are empty"
  fi

  NOISE=$(grep ":CONSOLE:" "$WORK/chrome.err" 2>/dev/null)
  if [ -z "$NOISE" ]; then
    ok "the console said nothing"
  else
    bad "the console complained"
    printf '%s
' "$NOISE" | sed "s/^/        /" | head -10
  fi
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
