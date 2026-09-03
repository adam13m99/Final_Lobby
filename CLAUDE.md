# LobbyBaz — agent context

> **The product is called LobbyBaz.** Renamed by the owner on 2026-08-24
> (D46); "Final Lobby" was a working title. **The folder, the git repo and the
> GitHub remote are all still `Final_Lobby`** — that mismatch is deliberate and
> nothing should be renamed to fix it. Server-side paths (`/etc/finallobby`,
> `/opt/finallobby`, the `finallobby` unix user, the systemd unit names) also
> keep the old name on purpose — see D50.

Read this file, then `docs/STATE.md`, before touching anything.

## What this is

A GameRanger-style Dota 2 lobby for players inside Iran, who cannot reach
Valve matchmaking and may be limited to the domestic network. One player's PC
hosts the match; our domestic relay carries traffic between players.

- **Spec:** `docs/superpowers/specs/2026-08-18-lobby-platform-design.md`
- **Plan:** `docs/superpowers/plans/2026-08-18-network-core.md`
- **Progress:** `docs/STATE.md`
- **Decisions and their reasons:** `docs/decisions.md`
- **How the server works:** `docs/backend.md`
- **How the interface works:** `docs/frontend.md`

The last two are the orientation documents. Read whichever half you are about
to touch — they explain the structure, name the traps, and say which rules
exist because something already went wrong.

## Resume ritual — mandatory

Before writing code, every session:

1. Read `docs/STATE.md`.
2. Run `bash scripts/verify.sh fast` (seconds) to know where you stand.
   **Trust its output over any summary**, including one describing this
   conversation. A summary is prose that can be misread; the tests are ground
   truth.
3. `git log --oneline -15` to see what actually landed.
4. Read `docs/backend.md` or `docs/frontend.md`, whichever half the task is
   in. Both end with a short "where to be careful" list; that list is the
   cheapest thing to read in the whole repository.

## The two commands

Everything else is detail. If you remember nothing else about this repository,
remember these:

- **`bash scripts/verify.sh`** — the whole harness, cheapest rung first, one
  verdict at the end. `fast` runs the unit rung only. It keeps going after a
  failure so one run tells you everything that is wrong. Every rung binds
  loopback and uses a throwaway database; none can reach the live server.
- **`./scripts/ship.sh`** — the server, the terms text, the relay, and the app
  republished as an installer that installed copies upgrade themselves to.

The rungs inside `verify.sh` — `check`, `smoke`, `uicheck`, `chatcheck`,
`termscheck` — can each be run alone while you work, and the header of
`scripts/verify.sh` says in one line what each proves and why it exists. Three
things stay outside it because no machine can grade them: `preview.sh` and
`try.sh`, which produce pictures and a window for a person to look at, and
`live.sh`, which is the whole product at one fixed address that reloads itself
as you edit. **None of the three touches the live server** — every one of them
is loopback, on a throwaway database. Four scripts reach `87.107.110.199`:
`deploy.sh`, `publish.sh`, `ship.sh` — and `qa-lobby.sh`, which is the odd one
out and is described below.

Finishing a task means: `bash scripts/verify.sh` green, `STATE.md` updated,
one commit naming the task number, pushed via `./scripts/git-sync.sh push`,
**and shipped with `./scripts/ship.sh`** so the live server and the published
installer match the working tree.

**Ship every change.** The owner tests on the live product, not on this PC, so
a change that only exists here cannot be looked at by the person who asked for
it (D62). `./scripts/ship.sh` is the whole job in one command: the coordinator
and the terms text it serves, the relay, and the desktop app republished as an
installer that installed copies upgrade themselves to. Running `deploy.sh`
alone is the easy mistake and the invisible one - the server is healthy, the
API is new, and every installed copy is still showing last week's interface.

## Hard rules — do not "improve" these

These were chosen deliberately. Reversing them reintroduces bugs that killed
the predecessor platform. Reasons are in `docs/decisions.md`.

- **Broadcast and multicast are dropped, not scoped.** Clients are told the
  host's address directly, so Dota never needs LAN discovery. Carrying
  broadcast is what collapsed the ancestor above ~1500 players.
- **Unreliable datagrams, never KCP or any reliable-ordered stream.** One lost
  packet must not head-of-line-block the packets behind it.
- **Goroutine count scales with players, never with packet rate.** One
  long-lived writer per peer reading a bounded queue.
- **Anti-spoof is mandatory:** inner source IP must equal the session's
  assigned virtual IP.
- **Relay binds UDP 443 only. Never TCP 443** — that belongs to an unrelated
  live proxy service on the shared server.
- **No custom cryptography.** Noise NK handshake, ChaCha20-Poly1305 data.
- **Never commit secrets.** `github_token_admin.txt` and
  `mobinhost_server_1.txt` are gitignored. Verify before every commit.

## Environment gotchas

- **GitHub is DPI-blocked from this PC.** TCP connects, then stalls. Use
  `./scripts/git-sync.sh push|pull`, which tunnels through the server.
- **Server `87.107.110.199` runs a live, unrelated business** — the
  `nati-filter` control plane: WireGuard on UDP 51821, nginx SNI routing on
  TCP 443 fed from inside that tunnel, CoreDNS, PostgreSQL, and a reverse
  tunnel to Paris. Real users. Development and testing only; do not disturb
  it. **We hold UDP 443 and TCP 7001, and nothing else.** A dedicated server
  is bought before launch. Survey before assuming: it changes.
- **Physical test capacity is two PCs**, one host one client. Anything larger
  is simulated via `loadtest/`.
- **To look at the product, run `./scripts/try.sh`.** A whole LobbyBaz on
  loopback with a seeded lobby, opened in the browser, deleted on Ctrl-C. It
  is the only way to click through the interface without publishing, and
  `./scripts/preview.sh <name>` is the same sandbox photographed instead.
- **To give the owner something to click on, run `./scripts/qa-lobby.sh up`.**
  Two dozen test players and six test rooms — every door, a nearly full one,
  a locked one — on the **live** server, so manual QA has somebody to play
  with. Three things about it are not optional to know: the rooms live only
  while its heartbeat keeps running on this PC (a host who goes quiet closes
  their room, D84), a coordinator restart wipes them because rooms are in
  memory, so **ship first and build the lobby after**, and the accounts it
  creates are permanent — there is no API that deletes a player. They are all
  named `qa_*`. `./scripts/qa-lobby.sh down` empties the rooms.
  `bash scripts/qa-lobby-selftest.sh` rehearses the whole thing on loopback,
  and is what to run after changing it.
- **To put a build on a test PC, run `./scripts/publish.sh`.** It stamps the
  server details into the binaries, uploads one installer, and prints a
  link. Installed copies pick up later builds themselves. Never go back to
  copying a folder.

## Product rules (owner decisions — ask, never assume)

- Host leaves, quits or drops off the network: the room closes **at once,
  with no grace** (D84, revised from D40's one minute and the two before it).
  The only delay is detection — thirty seconds of silence before the
  coordinator calls a host offline — and that is not a window to come back in.
  **The match ending does nothing to the room** — the players stay together,
  which is the normal case: ten people finish and want to play again.
- Kicked player: barred from that room for **1 minute, then 3, 5, 7** —
  escalating per kick from that room (D39, revised from a flat five).
  Enforced server-side, and never lifted by a password or an invitation.
- Player who left voluntarily: may rejoin freely.
- While a room is `Locked - In Game`, **no new player may join** until the host
  explicitly reopens it to new players.
- Admins hold a reserved **spectator** slot outside the ten playing slots,
  and one person is in one room at a time (D82) — so **a moderator leaves the
  room they are in to go and moderate another** (D85). The staff seat is about
  capacity, not about being in two places.
- MMR is self-declared, changeable once per week.
- Terms accepted at install, recorded against the account.

The product owner is non-technical. Bring product decisions to them with
options and trade-offs; make technical decisions yourself.
