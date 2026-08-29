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

**A player's virtual IP is derived from their slot index.** This is why moving
seats revokes the ticket (D57): after a move the address the old ticket names
is not theirs, and anti-spoof would drop everything they sent. The coordinator
revokes on a successful move and the app reconnects in the same action.

**The room's address follows the host, not the other way round** (D64).
`Room.HostSlot` is the seat the host is sitting in; every membership derives
its `HostIP` from it, and `Move` maintains it. The host therefore picks a side
like anybody else, slot 0 is an ordinary Radiant seat once they get up, and a
host who crashed comes back to the seat they left rather than the lowest free
one. Until 2026-08-29 the host was nailed to slot 0 because slot 0's address
*was* the room's address, which made the person who opened a room to play Dire
the only person who could not sit there.

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

### Serving the download

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
| `scripts/check.sh` | Every module builds, vets, tests; the front end parses. **Ground truth.** |
| `scripts/smoke.sh` | A real coordinator on a throwaway database and two real apps walked through the whole path. |
| `scripts/ship.sh` | **Both of the next two, in order.** Everything changed reaches the live server (D62). |
| `scripts/deploy.sh relay\|coordinator\|status\|logs` | Build and install on the server. `coordinator` also ships `docs/terms-en.md`. |
| `scripts/publish.sh` | Build the installer, upload it, print the link. |
| `scripts/build.sh` | Build one thing without deploying it. |
| `scripts/git-sync.sh push\|pull` | Git through the server, because GitHub is DPI-blocked here. |

Deploying is idempotent and safe to repeat: uploads are checksum-verified on
the far end (an upload can fail silently when the target is locked by a
running process — D21), the relay key is generated only when absent, and
`publish.sh` says out loud that nginx, CoreDNS and the relay are still up
before it finishes.

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
8. **Never commit secrets.** `github_token_admin.txt` and
   `mobinhost_server_1.txt` are gitignored; the download key and API token
   live only on the server. Verify before every commit.
