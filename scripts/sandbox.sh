# A complete LobbyBaz on this PC, on data nobody has to care about.
#
# Sourced, never run: scripts/try.sh opens it in a browser and leaves it up,
# scripts/preview.sh photographs it and throws it away. Both need the same
# thing first - a coordinator, an app pointed at it, and a lobby with enough
# in it to look like a lobby.
#
# SAFETY. Everything here is local and disposable:
#   - both processes bind 127.0.0.1 on ports the OS picks, so nothing is
#     reachable from outside this machine;
#   - the database is a temporary file, deleted on exit;
#   - APPDATA is redirected, so the developer's own signed-in session is not
#     touched or overwritten;
#   - the relay key is a throwaway. No tunnel is opened and the real key
#     never leaves the server.
# It never contacts 87.107.110.199.

# Extra arguments for the app, used by scripts/live.sh to pass -dev-ui.
SANDBOX_APP_ARGS=()

sandbox_port() {
  python -c "import socket;s=socket.socket();s.bind(('127.0.0.1',0));print(s.getsockname()[1]);s.close()"
}

sandbox_cleanup() {
  [ -n "${APP_PID:-}" ]   && kill "$APP_PID"   2>/dev/null
  [ -n "${COORD_PID:-}" ] && kill "$COORD_PID" 2>/dev/null
  sleep 1
  # Windows keeps the SQLite file open for a moment after the process dies.
  rm -rf "${WORK:-/nonexistent}" 2>/dev/null
}

# sandbox_build - compile the two binaries this needs, into the temp dir.
# PORT and APP_PORT may be set before calling, which is how scripts/live.sh
# keeps one address working for hours across restarts. Left unset, the
# operating system picks both and nothing outside this run can guess them.
sandbox_build() {
  WORK=${WORK:-$(mktemp -d)}
  PORT=${PORT:-$(sandbox_port)}
  COORD="http://127.0.0.1:$PORT"
  python -c "import secrets;print(secrets.token_hex(32))" > "$WORK/relay.pub"

  echo "building..."
  (cd coordinator && go build -o "$WORK/coordinator.exe" ./cmd/coordinator) || return 1
  # The app is stamped with the sandbox coordinator's address the same way a
  # published build is stamped with the real one, so nobody types an address.
  (cd lobbyapp && go build -ldflags "-X lobbybaz/client/build.Coordinator=$COORD" \
    -o "$WORK/lobbyapp.exe" .) || return 1
}

# sandbox_coordinator - start it with accounts and terms switched on.
sandbox_coordinator() {
  "$WORK/coordinator.exe" -listen "127.0.0.1:$PORT" -db "$WORK/lobby.db" \
    -relay-pub "$WORK/relay.pub" -terms-file docs/terms-en.md -tick 1s \
    ${1:+-head-admin "$1"} > "$WORK/coordinator.log" 2>&1 &
  COORD_PID=$!
  for _ in $(seq 1 40); do
    curl -fsS "$COORD/healthz" >/dev/null 2>&1 && return 0
    sleep .25
  done
  echo "the coordinator did not start:" >&2
  cat "$WORK/coordinator.log" >&2
  return 1
}

# sandbox_app - start the app and read back the address it printed.
sandbox_app() {
  export APPDATA="$(cygpath -w "$WORK" 2>/dev/null || echo "$WORK")"
  "$WORK/lobbyapp.exe" -no-browser -url-only -listen "127.0.0.1:${APP_PORT:-0}"     "${SANDBOX_APP_ARGS[@]}" > "$WORK/app.log" 2>&1 &
  APP_PID=$!
  for _ in $(seq 1 40); do
    APP_URL=$(head -1 "$WORK/app.log" 2>/dev/null)
    case "$APP_URL" in http://*) break ;; esac
    sleep .25
  done
  case "$APP_URL" in
    http://*) ;;
    *) echo "the app did not start:" >&2; cat "$WORK/app.log" >&2; return 1 ;;
  esac
  APP=${APP_URL%%/?t=*}
  TOK=${APP_URL##*t=}
}

# --- talking to the two of them -------------------------------------------

# call METHOD PATH [BODY] - the app's own API, as the page uses it.
call() {
  if [ $# -ge 3 ]; then
    curl -sS -X "$1" -H "X-Lobby-Token: $TOK" -H 'Content-Type: application/json' \
      -d "$3" "$APP$2"
  else
    curl -sS -X "$1" -H "X-Lobby-Token: $TOK" "$APP$2"
  fi
}

# as SESSION METHOD PATH [BODY] - the coordinator directly, as one of the
# seeded players. The session decides who the request is from (D53), so every
# seeded host is a real account rather than a name in a body.
as() {
  local s="$1" m="$2" p="$3"; shift 3
  if [ $# -ge 1 ]; then
    curl -sS -X "$m" -H "X-LobbyBaz-Session: $s" -H 'Content-Type: application/json' \
      -d "$1" "$COORD$p"
  else
    curl -sS -X "$m" -H "X-LobbyBaz-Session: $s" "$COORD$p"
  fi
}

jfield() { python -c "import sys,json;print(json.load(sys.stdin).get('$1',''))"; }

sandbox_signup() {
  curl -sS -X POST -H 'Content-Type: application/json' \
    -d "{\"username\":\"$1\",\"display_name\":\"$2\",\"password\":\"a long enough one\",\"device\":\"sandbox\",\"accept_terms_version\":\"$VER\"}" \
    "$COORD/v1/auth/signup"
}

# sandbox_seed - four players, three rooms with three different doors, and
# some chat. An empty lobby tells you nothing about whether the lobby works.
sandbox_seed() {
  VER=$(curl -fsS "$COORD/healthz" | jfield terms_version)

  local a b c d
  a=$(sandbox_signup shadowfiend "Shadow Fiend"); SA=$(printf '%s' "$a" | jfield session)
  b=$(sandbox_signup juggernaut  "Juggernaut");   SB=$(printf '%s' "$b" | jfield session)
  c=$(sandbox_signup pudge       "Pudge");        SC=$(printf '%s' "$c" | jfield session)
  # The coordinator throttles signups to five and then one every five
  # seconds, which is the whole defence against password guessing. Waiting is
  # correct: a sandbox that needed the limit lifted would not be the product.
  sleep 5
  d=$(sandbox_signup lina        "Lina");         SD=$(printf '%s' "$d" | jfield session)
  PA=$(printf '%s' "$a" | jfield player_id)
  PD=$(printf '%s' "$d" | jfield player_id)

  as "$SA" POST /v1/me '{"mmr":4200}' >/dev/null
  as "$SB" POST /v1/me '{"mmr":2600}' >/dev/null
  as "$SC" POST /v1/me '{"mmr":3100}' >/dev/null
  as "$SD" POST /v1/me '{"mmr":5400}' >/dev/null

  local r1 r2
  r1=$(as "$SA" POST /v1/rooms '{"name":"Ranked 5v5 - no feeders","privacy":"public","min_mmr":3000}')
  r2=$(as "$SB" POST /v1/rooms '{"name":"Casual all pick, everyone welcome","privacy":"public"}')
  as "$SC" POST /v1/rooms '{"name":"Turbo - quick games","privacy":"password","password":"hunter2"}' >/dev/null
  RID1=$(printf '%s' "$r1" | jfield room_id)
  RID2=$(printf '%s' "$r2" | jfield room_id)

  as "$SA" POST "/v1/rooms/$RID1/description" '{"description":"Bring a mic. Two slots left for offlane and support."}' >/dev/null
  as "$SB" POST "/v1/rooms/$RID2/description" '{"description":"First time playing? Join this one."}' >/dev/null
  as "$SD" POST "/v1/rooms/$RID1/join" '{}' >/dev/null
  as "$SC" POST "/v1/rooms/$RID1/join" '{}' >/dev/null

  as "$SA" POST /v1/chat '{"channel":"lobby","text":"Anyone up for a ranked game?"}' >/dev/null
  as "$SD" POST /v1/chat '{"channel":"lobby","text":"joining now"}' >/dev/null
  as "$SB" POST /v1/chat '{"channel":"lobby","text":"my room is open, all pick"}' >/dev/null
  as "$SC" POST /v1/chat '{"channel":"lobby","text":"need one more for turbo"}' >/dev/null
}

# sandbox_you - sign the app in as a player with friends and a room, so the
# screens that need an account have something to show.
sandbox_you() {
  sleep 6
  call POST /api/auth/signup "{\"username\":\"you\",\"display_name\":\"You\",\"password\":\"a long enough one\",\"terms_version\":\"$VER\"}" >/dev/null
  call POST /api/profile '{"nick":"You","mmr":3800}' >/dev/null
  YOU=$(call GET /api/state | jfield player_id)
  call POST /api/friends "{\"action\":\"request\",\"target_id\":\"$PA\"}" >/dev/null
  as "$SA" POST /v1/friends "{\"target_id\":\"$YOU\",\"action\":\"accept\"}" >/dev/null
  call POST /api/friends "{\"action\":\"request\",\"target_id\":\"$PD\"}" >/dev/null
  call POST /api/rooms/join "{\"room_id\":\"$RID2\"}" >/dev/null
}
