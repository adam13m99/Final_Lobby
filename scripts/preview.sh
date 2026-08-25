#!/usr/bin/env bash
# Photograph the interface, on a lobby that looks like a lobby.
#
#   bash scripts/preview.sh <name>          # 1440x820
#   WIDE=1366 TALL=768 bash scripts/preview.sh small
#
# Writes one PNG per screen into scripts/shots/<name>/.
#
# Design work needs to be looked at, and nothing else here can look at
# anything: check.sh proves the CSS parses, smoke.sh proves the page renders
# and the console is quiet, and neither can tell you the room list is ugly or
# that half the window is empty. This boots a real coordinator and a real app
# on a throwaway database, seeds four players and three rooms with different
# doors, and drives headless Chrome through every screen.
#
# Loopback only, throwaway database, redirected APPDATA, its own Chrome
# profile. Never touches the live server or the developer's own session.
set -uo pipefail
cd /c/Users/Mcc/Desktop/Final_Lobby
. scripts/env.sh

TAG="${1:-shot}"
SP="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT="$SP/shots/$TAG"; mkdir -p "$OUT"
WORK=$(mktemp -d)
CHROME="/c/Program Files/Google/Chrome/Application/chrome.exe"

cleanup() {
  [ -n "${CH_PID:-}" ]    && kill "$CH_PID" 2>/dev/null
  [ -n "${APP_PID:-}" ]   && kill "$APP_PID" 2>/dev/null
  [ -n "${COORD_PID:-}" ] && kill "$COORD_PID" 2>/dev/null
  sleep 1; rm -rf "$WORK" 2>/dev/null
}
trap cleanup EXIT

port() { python -c "import socket;s=socket.socket();s.bind(('127.0.0.1',0));print(s.getsockname()[1]);s.close()"; }

PORT=$(port); CDP=$(port)
COORD="http://127.0.0.1:$PORT"
python -c "import secrets;print(secrets.token_hex(32))" > "$WORK/relay.pub"

(cd coordinator && go build -o "$WORK/coordinator.exe" ./cmd/coordinator) || exit 1
(cd lobbyapp && go build -ldflags "-X lobbybaz/client/build.Coordinator=$COORD" -o "$WORK/lobbyapp.exe" .) || exit 1

"$WORK/coordinator.exe" -listen "127.0.0.1:$PORT" -db "$WORK/p.db" \
  -relay-pub "$WORK/relay.pub" -terms-file docs/terms-en.md -tick 1s \
  > "$WORK/coord.log" 2>&1 &
COORD_PID=$!
for _ in $(seq 1 40); do curl -fsS "$COORD/healthz" >/dev/null 2>&1 && break; sleep .25; done
VER=$(curl -fsS "$COORD/healthz" | python -c "import sys,json;print(json.load(sys.stdin)['terms_version'])")

# --- seed -----------------------------------------------------------------
# Every host is a real account with a real session, because the coordinator
# ignores anything else (D53).
signup() {
  curl -sS -X POST -H 'Content-Type: application/json' \
    -d "{\"username\":\"$1\",\"display_name\":\"$2\",\"password\":\"a long enough one\",\"device\":\"preview\",\"accept_terms_version\":\"$VER\"}" \
    "$COORD/v1/auth/signup"
}
sess() { printf '%s' "$1" | python -c "import sys,json;print(json.load(sys.stdin)['session'])"; }
pid()  { printf '%s' "$1" | python -c "import sys,json;print(json.load(sys.stdin)['player_id'])"; }

as() { # as <session> <METHOD> <path> [body]
  local s="$1" m="$2" p="$3"; shift 3
  if [ $# -ge 1 ]; then
    curl -sS -X "$m" -H "X-LobbyBaz-Session: $s" -H 'Content-Type: application/json' -d "$1" "$COORD$p"
  else
    curl -sS -X "$m" -H "X-LobbyBaz-Session: $s" "$COORD$p"
  fi
}

A=$(signup shadowfiend "Shadow Fiend");  SA=$(sess "$A"); PA=$(pid "$A")
B=$(signup juggernaut  "Juggernaut");    SB=$(sess "$B"); PB=$(pid "$B")
C=$(signup pudge       "Pudge");         SC=$(sess "$C"); PC=$(pid "$C")
sleep 5
D=$(signup lina        "Lina");          SD=$(sess "$D"); PD=$(pid "$D")

for s in "$SA:4200" "$SB:2600" "$SC:3100" "$SD:5400"; do
  as "${s%%:*}" POST /v1/me "{\"mmr\":${s##*:}}" > /dev/null
done

R1=$(as "$SA" POST /v1/rooms '{"name":"Ranked 5v5 - no feeders","privacy":"public","min_mmr":3000}')
R2=$(as "$SB" POST /v1/rooms '{"name":"Casual all pick, everyone welcome","privacy":"public"}')
R3=$(as "$SC" POST /v1/rooms '{"name":"Turbo - quick games","privacy":"password","password":"hunter2"}')
rid() { printf '%s' "$1" | python -c "import sys,json;print(json.load(sys.stdin)['room_id'])" 2>/dev/null || printf '%s' "$1" | python -c "import sys,json;d=json.load(sys.stdin);print(d.get('room',{}).get('id',''))"; }
RID1=$(rid "$R1"); RID2=$(rid "$R2")

as "$SA" POST "/v1/rooms/$RID1/description" '{"description":"Bring a mic. Two slots left for offlane and support."}' >/dev/null
as "$SB" POST "/v1/rooms/$RID2/description" '{"description":"First time playing? Join this one."}' >/dev/null
as "$SD" POST "/v1/rooms/$RID1/join" '{}' >/dev/null
as "$SC" POST "/v1/rooms/$RID1/join" '{}' >/dev/null

for m in "$SA:Anyone up for a ranked game?" "$SD:joining now" "$SB:my room is open, all pick" "$SC:need one more for turbo"; do
  as "${m%%:*}" POST /v1/chat "{\"channel\":\"lobby\",\"text\":\"${m#*:}\"}" >/dev/null
done

# --- the app --------------------------------------------------------------
export APPDATA="$(cygpath -w "$WORK")"
"$WORK/lobbyapp.exe" -no-browser -url-only -listen 127.0.0.1:0 > "$WORK/app.log" 2>&1 &
APP_PID=$!
APP_URL=""
for _ in $(seq 1 40); do
  APP_URL=$(head -1 "$WORK/app.log" 2>/dev/null)
  case "$APP_URL" in http://*) break ;; esac
  sleep .25
done
APP=${APP_URL%%/?t=*}; TOK=${APP_URL##*t=}
call() { curl -sS -X "$1" -H "X-Lobby-Token: $TOK" -H 'Content-Type: application/json' ${3:+-d "$3"} "$APP$2"; }

sleep 6
call POST /api/auth/signup "{\"username\":\"you\",\"display_name\":\"You\",\"password\":\"a long enough one\",\"terms_version\":\"$VER\"}" >/dev/null
call POST /api/profile '{"nick":"You","mmr":3800}' >/dev/null
call POST /api/friends "{\"action\":\"request\",\"target_id\":\"$PA\"}" >/dev/null
as "$SA" POST /v1/friends "{\"target_id\":\"$(call GET /api/state | python -c 'import sys,json;print(json.load(sys.stdin)["player_id"])')\",\"action\":\"accept\"}" >/dev/null
call POST /api/friends "{\"action\":\"request\",\"target_id\":\"$PD\"}" >/dev/null
call POST "/api/rooms/join" "{\"room_id\":\"$RID2\"}" >/dev/null

echo "app: $APP_URL"
echo "rooms: $RID1 $RID2"

# --- photograph -----------------------------------------------------------
"$CHROME" --headless=new --disable-gpu --no-first-run --no-default-browser-check \
  --remote-debugging-port="$CDP" --user-data-dir="$(cygpath -w "$WORK")\chrome" \
  --window-size=$WIDE,820 about:blank > "$WORK/chrome.log" 2>&1 &
CH_PID=$!
sleep 3

export SHOTS='[["lobby",""],["room","show(\"room\")"],["mod","show(\"mod\")"],["checks","show(\"checks\")"],["profile","$(\"mebtn\").click()"]]'
export WIDE="${WIDE:-1440}"
node "$SP/preview-shots.js" "$APP_URL" "$(cygpath -w "$OUT")" "$CDP"
echo "wrote $OUT"
