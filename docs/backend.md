# LobbyBaz — the back end

> **For an agent picking this up.** Read `CLAUDE.md` first (the hard rules),
> then `docs/STATE.md` (what is actually built), then this.
> `docs/decisions.md` is the long-form reasoning behind every rule here —
> when this file says "because D42", that is where the argument lives.
>
> Ground truth is `bash scripts/check.sh`, never a summary. Including this one.

## What the back end is for

A Dota 2 match is a LAN game. One player's PC hosts it; the others must
appear to be on the same local network. Inside Iran they are not — they are
behind different ISPs, most behind CGNAT, and Valve's own matchmaking is
unreachable. The back end's whole job is to make ten scattered machines look
like ten machines on one LAN, and to keep track of who is playing with whom.

Two processes do it, and they are deliberately separate:

- **The relay** moves game packets. It is on the hot path of a live match, so
  it holds no database, makes no decision it can avoid, and never blocks.
- **The coordinator** decides things. Who is in which room, who may come in,
  what address each player gets, who is banned. It is never on the path of a
  game packet.

A third process, the **netservice**, runs on each player's Windows PC. It is
back-end code in every sense — a privileged service with no interface — even
though it ships inside the client, and it is documented here for that reason.

## The shape, end to end

```
   Player's PC                          Server (87.107.110.199)
   ───────────                          ───────────────────────

   Dota 2
     │  UDP to 10.87.x.y
     ▼
   Wintun adapter  ──┐
     │               │  netservice (Windows service, SYSTEM)
     │               │    · owns the adapter
     ▼               │    · Noise NK, ChaCha20-Poly1305
   tunnel client ────┘    · holds a ticket, renews a lease
     │
     │  UDP 443, encrypted, unreliable datagrams
     ▼
   ┌─────────────────────────────┐        ┌────────────────────────┐
   │  relay                      │ ─────▶ │  coordinator           │
   │   · one socket, one reader  │  is    │   · rooms and seats    │
   │   · one writer per peer     │ this   │   · addresses (ipam)   │
   │   · route.Decide per packet │ ticket │   · tickets            │
   │   · no database at all      │ good?  │   · accounts, friends  │
   └─────────────────────────────┘        │   · moderation         │
                                          │   · SQLite             │
   lobbyapp (the desktop app) ───────────▶│   HTTP on TCP 7001     │
       HTTP, one poll every 2s            └────────────────────────┘
```

**The relay binds UDP 443 only, never TCP 443.** TCP 443 on that box belongs
to an unrelated live business — nginx SNI routing with real paying customers.
This is the single most important operational fact in the project.

## Module map

Nine Go modules in one workspace (`go.work`), with a workspace-level
`vendor/`. `scripts/env.sh` sets `GOPROXY=https://goproxy.cn,direct` and
`GOTOOLCHAIN=local`, because the usual proxy is unreachable from here.

| Module | What it is | Runs where |
|---|---|---|
| `protocol/` | Wire format, crypto, named-pipe IPC. No I/O policy. | shared |
| `relay/` | The packet mover. | server |
| `coordinator/` | Every decision, and the only database. | server |
| `netservice/` | Windows service: adapter, tunnel, launching Dota. | player's PC |
| `client/` | The Go library the app and CLI reach the coordinator through. | player's PC |
| `lobbyapp/` | The desktop app: a local HTTP server plus the interface. | player's PC |
| `lobbycli/` | A command-line client. Still the fastest way to poke the coordinator. | anywhere |
| `installer/` | Builds the one self-extracting `.exe`. | build machine |
| `loadtest/` | Simulated peers, because physical test capacity is two PCs. | anywhere |

`protocol/` is imported by both sides of the wire, so a change there is a
change to both. Everything else depends downward only.

## The relay

`relay/internal/server/server.go` is the whole of it: one UDP socket, one
reader goroutine, and **one long-lived writer goroutine per connected peer**
draining a bounded queue.

**This is the rule the predecessor died on.** DotaIranConnect spawned a
goroutine per forwarded packet; at target scale that is roughly a million
goroutines a second, and it reordered game traffic as a side effect.
Goroutine count here scales with *players*, never with *packet rate*. Any
change that introduces a goroutine per packet is a regression, however
elegant it looks.

**Routing is a pure function.** `route.Decide` in
`relay/internal/route/decision.go` takes the authenticated sender and the
inner packet and returns Drop, Forward or Fanout. It is pure so that the two
rules which must never regress are directly testable:

- **Anti-spoof.** The inner source IP must equal the virtual IP that session
  was issued. A packet claiming to be somebody else is dropped, not corrected.
- **Room isolation.** A packet may only reach a peer in the sender's own room.
  Rooms cannot see or reach each other.

**Broadcast and multicast are dropped, not scoped.** Clients are told the
host's address directly, so Dota never needs LAN discovery. Carrying
broadcast is what collapsed the ancestor above ~1500 players. `Fanout` exists
and is reachable only behind an option nothing enables.

**Unreliable datagrams, never a reliable-ordered stream.** No KCP, no TCP
fallback, no retransmit layer. One lost packet must not head-of-line-block
the packets behind it — that is the difference between a dropped frame and a
freeze. `relay/internal/sendq` is bounded and **drops the oldest** on
overflow rather than blocking the producer, for the same reason: game traffic
prefers a lost packet to a late one.

**No database, no accounts.** The relay knows sessions, virtual IPs and room
ids, all of which arrive inside a validated ticket. A ticket it does not
recognise is checked with `POST /internal/validate-ticket` and the answer
cached briefly.

**Keepalives echo their sequence number.** A keepalive is answered with a
keepalive carrying the same sequence back, which is what lets a client measure
its own round trip (D54). That field is already in the clear in every header,
so there is no new packet type, no payload, and no table of outstanding probes
to expire or leak. Same size in as out, so it is not an amplifier. The reply
is written straight from the reader rather than pushed onto the peer's send
queue, which carries inner packets and has nothing to do with keepalives.

## The wire

`protocol/wire` — a fixed 14-byte header on every datagram: version, type,
session id, sequence. The sequence travels in the clear because it seeds the
AEAD nonce. Five types: handshake init, handshake response, data, keepalive,
disconnect.

`protocol/crypto` — **Noise NK** handshake, **ChaCha20-Poly1305** for data.
**No custom cryptography**, ever. NK means the client knows the relay's static
public key in advance (it is stamped into the build) and the relay does not
need the client's — which is right, because clients are authorised by ticket,
not by key.

`/etc/finallobby/relay.key` **must never be regenerated.** Every shipped
client carries the matching public key baked in, so a new key locks out every
installed copy. `scripts/deploy.sh` generates only when the file is absent,
which is why it is safe to run repeatedly.

## Addressing

`coordinator/internal/ipam` — the platform owns `10.87.0.0/16` and gives each
room a **/27**: 32 addresses, of which `.1` is the relay, `.2–.11` are the ten
player slots, `.12–.16` five observers, `.17–.19` three admin seats, and the
rest spare. 2048 rooms fit, forty times the 500-player launch target.

**An address belongs to the player, not to the seat** (D74). `Room.Addr` is
an array of player ids indexed by address; a player takes a free index when
they join, keeps it for as long as they are in the room whatever seat they are
sitting in, and gives it back when they leave. `Room.AddressOf(playerID)` is
the only way to ask.

This is a deliberate reversal. Until 2026-08-30 the address was derived from
the slot index, so changing seats changed a player's IP, which invalidated the
ticket naming it, which meant the coordinator revoked on every move (D57) and
the app tore the tunnel down and rebuilt it — up to twenty-five seconds of
being disconnected because somebody wanted to play Dire. Nothing about the
tunnel actually depends on the seat: `ticket.Claims` carries `PlayerID`,
`RoomID` and `VirtualIP` and has never carried a slot. Moving seats is now a
change to a list on the server and nothing else, and `Move` deliberately does
not touch `Addr`.

**The host's address is theirs the same way** (D64, restated under D74). Every
membership's `HostIP` comes from `AddressOf(HostID)`, so the host picks a side
like anybody else, slot 0 is an ordinary Radiant seat once they get up, and a
host who crashed comes back holding the address every client is already
sending to. Until 2026-08-29 the host was nailed to slot 0 because slot 0's
address *was* the room's address, which made the person who opened a room to
play Dire the only person who could not sit there.

The one seat the host cannot take is a watching seat: the match runs on their
machine, and `JoinObserver` refuses `HostID` before anything else.

It was a /28 until 2026-08-24. Sixteen addresses held thirteen seats with
every one spoken for, and the room is eighteen seats (D38), so the block
doubled. The eleven spare addresses are deliberate: that resize touched every
layer of the stack, and the next seat-count change should not.

## The coordinator

One binary, `coordinator/cmd/coordinator`, serving HTTP on TCP 7001. Its
packages:

| Package | Holds |
|---|---|
| `api/` | Every HTTP route. Split by subject: `lobby`, `auth`, `social`, `moderation`, `privacy`, `terms`, `download`, `ratelimit`. |
| `room/` | The room itself: `state.go` is one room's rules, `store.go` is all of them under a mutex, `privacy.go` is the door. |
| `ipam/` | Addresses. |
| `ticket/` | The short-lived credential the relay checks. |
| `player/` | The live registry: who is online, their nick, MMR, in-game flag, relay ping. In memory. |
| `account/` | Usernames, Argon2id passwords, sessions, terms acceptance, last-seen. SQLite. |

The terms themselves are a **file**, not a compiled-in string: `-terms-file`
points at `/etc/finallobby/terms-en.md`, which `deploy.sh coordinator` uploads
from `docs/terms-en.md`. `api.TermsVersion` names the version in force, and
bumping it re-prompts everybody — so bumping it without shipping the text it
names asks the world to re-accept the terms they already had.
| `social/` | Friends, blocks, private messages, room invitations. SQLite. |
| `moderation/` | Roles, sanctions, labels, banners, the audit log. SQLite. |
| `chat/` | The lobby and room channels. In memory, cursor-based. |
| `store/` | The database itself and its migrations. |
| `secret/` | Room password hashing. |

**In memory versus on disk.** Rooms, tickets and chat are in memory and die
with the process — a coordinator restart is a reconnect, not an outage, and
at a few hundred players that is the right trade. Accounts, friendships,
sanctions and kick history are in SQLite because they must outlive it: a ban
that a restart forgets is not a ban.

### The room state machine

`open` → `locked_in_game` → (`open_to_new_players`) → `closed`.

- **`locked_in_game` means the match is running.** No new player may join and
  no seat may move; a player who changes team mid-match is on the wrong team
  inside Dota and nothing here can undo it.
- **`open_to_new_players`** is the host explicitly reopening a running match
  to replace somebody who left.
- **The host leaving ends the room** after a **one-minute** grace, which
  doubles as their own chance to reconnect (D40 — it was two; a room whose
  host has genuinely gone should not hold nine people staring at it). It sits
  inside the 120-second sticky-address window, so a host who returns in time
  keeps their address and the room never noticed.
- **The match ending does nothing to the room.** The players stay together —
  ten people finish and want to play again, which is the normal case, not a
  problem to recover from.
- **A kicked player is barred from that room** for one minute, then three,
  five, seven — escalating per kick from that room (D39). Deliberately short
  at the start: most kicks are an argument rather than an abuser, and a minute
  ends the re-join fight without ruining somebody's evening. The live block is
  in memory with the room; every kick is also written to `kick_events`, which
  is the part still true when a moderator looks a month later (D52), and it is
  what makes the escalation survive a deployment.
- **Room ids are random and non-reusable.** They used to repeat after a
  restart, and anything keyed by one could attach itself to the wrong room.

### The four doors, plus a floor

`room/privacy.go`: `public`, `friends`, `invite`, `password` — plus a
`min_mmr` floor that applies to all four. The door is chosen when the room is
created, not afterwards (D41): a room opened public and locked a second later
is a second in which anybody can walk in.

A kick block is checked **before** the door and is never bypassed — not by a
password, not by an invitation, not by being staff. It is enforced against
identity, not role.

### Tickets

`ticket/` — opaque random strings looked up in a table, **not signed blobs**.
The relay already asks about every ticket it has not seen recently, so signing
would buy nothing and would cost instant revocation: a signed ticket is valid
until it expires no matter what we later decide, whereas a row in a table can
be deleted the moment somebody is kicked.

### Sessions decide identity

`X-LobbyBaz-Session` on the request. **The session decides who a call is
from; a `player_id` in the body is ignored** (D53). Before that, a client
could act as anybody by typing their id. Handlers still accept the field
because the CLI predates sessions, but `s.actor(r, body.PlayerID)` overrides
it whenever a session is present.

In front of all of it is a shared bearer token (`Authorization: Bearer …`,
from `/etc/finallobby/api.token`) which gates the whole player API. That is
transport-level gatekeeping, not identity.

### Rate limits

`api/ratelimit.go` is a token bucket, `NewLimiter(rate per second, burst)`.
The five in use, from `http.go`:

| Limiter | Rate, burst | Guards |
|---|---|---|
| `limitAuth` | 0.2/s, 5 | Sign-up, sign-in, password change. Five attempts then one every five seconds — **the entire defence against password guessing**, since there is no recovery and so no reset flow to abuse either. |
| `limitJoin` | 0.5/s, 5 | Joining, spectating, connecting. |
| `limitChat` | 1/s, 10 | Both channels and private messages. |
| `limitManage` | 2/s, 15 | Creating and running a room. |
| `limitRead` | 5/s, 30 | Listing and reading. |

Ticket validation is deliberately **not** limited: throttling the relay would
throttle the game.

Both `smoke.sh` and the sandbox seed sleep between calls because of the auth
limiter. A test that needed the limit lifted would be testing a server nobody
runs.


### The database

SQLite via `modernc.org/sqlite` — pure Go, so there is no cgo and the build
stays a single static binary.

**Migrations are an ordered list, and the index is the version number.**
`store/db.go` concatenates several lists in a fixed order (`migrations`,
`socialMigrations`, `moderationMigrations`, `presenceMigrations`) and runs
each once. **Never edit a migration that has shipped, and never insert one in
the middle** — the position *is* the version, so inserting renumbers
everything after it and re-runs the wrong scripts on a live database. Add a
new entry at the end, or a new list appended after the others.

The live database is at `/var/lib/finallobby/db/lobby.db`, currently at
schema 6.

**It is copied hourly and twenty-four copies are kept**, in
`/var/lib/finallobby/backups`, by `runBackups` in `main.go` (D76). Losing that
one file loses every account there has ever been — usernames, the password
hashes nobody can recover, the friend graph, the moderation record and who
accepted which version of the terms — and until 2026-08-30 nothing anywhere
held a second copy of it. The copy is taken with `VACUUM INTO` rather than
`cp`, because the database runs in WAL mode and a file copied while the
coordinator is writing opens cleanly and is wrong, which is worse than having
no copy at all. The first copy is taken a minute after start rather than an
hour, so a rebuilt server has one before it has a night. Starting without
`-backup-dir` is allowed and logs a warning that says what is at stake.

Rooms, by contrast, are in memory and are not backed up and never will be.
They are worth about a minute each and a restart is the only thing that ends
them; see the restart cost under **Running it**.

### The polling endpoint

`POST /v1/sync` is the client's heartbeat and its only polling call. It
returns the whole screen in one request — profile, room list with members,
the room you are in, both chat channels since your cursor, the online count.
One request per tick instead of five for the same screen.

It also carries three things *up*: `in_game` (only the player's own service
knows Dota is running, because it launched it and watches its log),
`relay_ms` (only their machine can measure it), and the room they believe
they are in.

**The lobby is browsable without a session.** Sync with no session answers
with the room list, the lobby chat and the online count, and nothing
belonging to a person (D45). Asking somebody to sign up before they can see
whether anybody is playing is how an install gets abandoned.

### Leases, and who is allowed to renew one

A ticket is good for ten minutes and the watchdog inside the Windows service
renews it every thirty seconds. Three things about that are easy to get wrong,
and two of them already have been:

- **`POST /v1/lease/renew` is not behind `signedIn`, on purpose.** The service
  has no session and never will. The ticket is the credential (D77).
- **It answers 200 with `valid:false` for a bad ticket**, never an error
  status. The watchdog reads any non-200 as "cannot tell" and waits out its
  three-minute local expiry before acting, so a status code turns a clear no
  into a slow one.
- **The watchdog fails closed and that is deliberate.** A coordinator it cannot
  reach never extends authorisation; it only delays the explicit answer until
  local expiry. Treating "cannot ask" as "still allowed" would hand an
  unrevokable session to anybody who can black-hole the coordinator.

### Presence and latency

- `player.Registry` holds `LastSeen`, `InGame`, `RelayMillis` and `RelayAt`
  in memory, updated on every heartbeat.
- `accounts.last_seen_at` is the copy that survives a restart, written by
  `flushPresence` in `main.go` **once a minute**, not on the heartbeat: a
  thousand players polling every two seconds would be five hundred writes a
  second for a number nobody reads more finely than "2h ago".
- The live value wins when it exists, because the stored copy is always
  coarser.
- A relay measurement is **dropped rather than shown** once older than the
  presence window. Zero means *no reading*, never *instant*, and the two must
  never render the same.
- The registry **forgets anybody silent for two hours** who is not sitting in
  a room, swept on the same timer that expires rooms and purges tickets. It
  was otherwise the one structure in the coordinator that only ever grew, in a
  process meant to run for months. Anybody holding a seat is kept whatever
  their last-seen time says — a host in their grace window has been quiet on
  purpose, and forgetting them blanks their name on nine other screens.

### Serving the download

**`WriteTimeout` is fifteen seconds and must never apply to the installer.**
It is a single deadline over a whole response, armed when the headers are
read, so on a thirteen megabyte file it silently caps every download at
whatever can be pushed in fifteen seconds - about 900 KB/s, which on Iran's
domestic network is nobody. `downloadFile` sets its own deadline with
`http.NewResponseController` for that one response; the server-wide one stays
for the API, where it belongs (D78). Anything large added to this server later
needs the same treatment, and will fail the same silent way if it does not
get it: the client sees a truncated body, and the server logs nothing at all.


The coordinator also serves the installer, from `-dist-dir`, under an
unguessable path segment from `-download-key-file`. That is the only thing in
front of it, because a browser cannot send a bearer token. It reuses TCP 7001
— a port we already own — so there is no second listener and no firewall
change.

## The netservice (on the player's PC)

`netservice/` — a Windows service running as SYSTEM. It exists because
creating a network adapter needs privileges the app must not have.

**The split is the whole security design.** The predecessor ran a
*privileged* agent on localhost, so any web page a player visited could drive
it as Administrator. Here the privileged half has no HTTP surface at all: it
listens on a named pipe (`protocol/ipc`), and the five operations it accepts
are `status`, `connect`, `disconnect`, `launch`, `ping`.

- `internal/adapter` — Wintun. The DLL (v0.14.1, WireGuard LLC, Authenticode
  verified) is embedded in the binary.
- `internal/tunnel` — the Noise session and the packet pump. **The adapter is
  never torn down on a blip**: a player whose link drops keeps the same
  virtual address and the same open adapter, so Dota's own reconnect resumes.
  Recreating it would drop Dota's socket and end the match for them.
- `internal/watchdog` — renews the lease with the coordinator and tears the
  tunnel down when it is revoked. **It lives in the service, not the app**, so
  a closed or tampered-with interface cannot keep a revoked player connected.
- `internal/dota` — finds Steam, finds Dota, launches it with the right flags.
  **Its allowlist is two-sided (D65).** Console commands — the `+` kind — are
  a closed list, because `+connect`, `+map`, `gamemode` and `+jointeam` are
  how a seat becomes a slot in a match. Engine flags — the `-` kind — accept
  anything `protocol/launch.IsPlayerFlag` accepts, because the player types
  their own and no useful list of them exists. The player's text is parsed
  here as well as in the app: this is the process boundary.
- `internal/firewall` — the one rule the adapter needs.

## Running it

| Script | Does |
|---|---|
| `scripts/verify.sh [fast]` | **Start here.** Every rung a machine can grade, cheapest first, one verdict. `fast` is the unit rung alone. |
| `scripts/check.sh` | Every module builds, vets, tests; the front end parses. **Ground truth.** |
| `scripts/smoke.sh` | A real coordinator on a throwaway database and two real apps walked through the whole path. |
| `scripts/uicheck.sh` | The rendered page driven through repeated polls: nothing duplicated, nothing rebuilt, nothing lost (D75). |
| `scripts/ship.sh` | **Both of the next two, in order.** Everything changed reaches the live server (D62). |
| `scripts/deploy.sh relay\|coordinator\|status\|logs` | Build and install on the server. `coordinator` also ships `docs/terms-en.md`. |
| `scripts/publish.sh` | Build the installer, upload it, print the link. |
| `scripts/build.sh` | Build one thing without deploying it. |
| `scripts/git-sync.sh push\|pull` | Git through the server, because GitHub is DPI-blocked here. |

Deploying is idempotent and safe to repeat: uploads are checksum-verified on
the far end (an upload can fail silently when the target is locked by a
running process — D21), the relay key is generated only when absent, the unix
user and every directory the unit needs are created if missing, and
`publish.sh` says out loud that nginx, CoreDNS and the relay are still up
before it finishes.

It is not, however, free. **Rooms live in the coordinator's memory and
nowhere else**, so restarting it closes every open room and drops everybody in
one back to the lobby mid-match. `ship.sh` asks the live server how many rooms
are open before it starts and says so; on a quiet server that line reads "no
rooms open" and you can stop reading.

## The room watches its host (D69, D70)

Three facts about a room come from outside it, and all three are new enough to
be worth naming here.

- **`Store.Tick` asks the player registry about every room's host**, through
  the lookup installed by `WatchHosts` in `cmd/coordinator/main.go`. It runs
  under the store's lock, so it must stay a lookup and nothing more.
- **A host in a match locks their room** for as long as Dota is running on
  their PC. Nobody presses anything - the host is in the game, not in the app.
  The room's stored `Status` does not change; `view()` reports
  `locked_in_game`, and `Room.Admits()` and `Room.Move` enforce it. The one
  override is a host who explicitly reopened the room to new players, which
  lets people **in** and still does not let seats **move**.
- **Leaving and vanishing are different events.** `Store.Leave` closes the
  room outright when the leaver is its host; `Room.SeeHost` starts the D40
  grace when the host simply stops answering. `Room.Leave` on its own is the
  second of the two - it vacates a seat and starts the timer - so anything
  new that wants the first must call `Close`.

The trap: `HostGraceUntil` does double duty. It is the countdown while a room
is alive and the linger clock once it is dead, which is why `Close` sets it
rather than leaving it zero. A closed room with a zero grace is swept out of
the store on the next tick, before anybody has read why it ended.

## Where to be careful

0. **Ship it.** `./scripts/ship.sh` after the commit. Deploying the server
   without republishing the app leaves every installed copy on the old
   interface, and nothing about the server looks wrong (D62).
1. **UDP 443 only.** Never bind TCP 443, never touch nginx or CoreDNS.
2. **Never regenerate the relay key.** Every installed client carries the
   public half.
3. **Never edit or insert a shipped migration.**
4. **Never make a goroutine per packet.**
5. **Never add a reliable-ordered transport.**
6. **Never trust a `player_id` in a body when a session is present.**
7. **Never let a player's text reach a `+` argument.** `protocol/launch`
   refuses those and `dota.ValidateArgs` refuses them again. A player who
   could type `+connect` could point their own client somewhere else and then
   report the room as broken (D65).
8. **Never assume a room notices anything about its host.** It cannot see
   whether they are online or in a match; both arrive through `SeeHost`, from
   the registry, on a tick. A rule that depends on either belongs beside those
   two, not in the handler that happens to have noticed (D69, D70).
9. **Never derive an address from a seat.** A virtual IP belongs to the
   player for as long as they are in the room; `Move` must not touch `Addr`,
   and nothing that changes a seat may revoke a ticket. The moment those two
   are coupled, every seat change becomes a reconnection (D74).
10. **Never let a restart be casual.** Rooms are in memory. Deploying the
   coordinator closes every open one; `ship.sh` counts them first and says so.
11. **Never let a response big enough to take seconds inherit `WriteTimeout`.**
   It is one deadline over the whole response and it cuts the connection
   mid-body with nothing in the log. `downloadFile` is the pattern (D78).
12. **Never put a route behind `signedIn` without asking who calls it.** The
   Windows service calls `/v1/lease/renew` and has no session, and cannot get
   one: sessions belong to the desktop app and the service outlives it. With
   accounts on, that guard answered it 401 and every match ended three minutes
   later (D77). The ticket is the credential on that route, the way it is on
   `/internal/validate-ticket`.
13. **Never trust a test that runs without the account database to tell you
   what production does.** `newHarness` has no accounts, so `signedIn` is a
   no-op inside it and every route looks open. Anything about who may call
   what belongs in `newAuthRig` or in `smoke.sh`, both of which have accounts
   on, like the live server.
14. **Never discard the error from a check you fail closed on.** The watchdog
   threw away the reason its lease check did not answer, so a refused request
   and a dead network were the same event for three minutes and then reported
   themselves as an expiry. Say it out loud, and name the cause in whatever
   reaches the player's screen.
15. **Never commit secrets.** `github_token_admin.txt` and
   `mobinhost_server_1.txt` are gitignored; the download key and API token
   live only on the server. Verify before every commit.
