# Final Lobby — agent context

Read this file, then `docs/STATE.md`, before touching anything.

## What this is

A GameRanger-style Dota 2 lobby for players inside Iran, who cannot reach
Valve matchmaking and may be limited to the domestic network. One player's PC
hosts the match; our domestic relay carries traffic between players.

- **Spec:** `docs/superpowers/specs/2026-08-18-lobby-platform-design.md`
- **Plan:** `docs/superpowers/plans/2026-08-18-network-core.md`
- **Progress:** `docs/STATE.md`
- **Decisions and their reasons:** `docs/decisions.md`

## Resume ritual — mandatory

Before writing code, every session:

1. Read `docs/STATE.md`.
2. Run `bash scripts/check.sh`. **Trust its output over any summary**,
   including one describing this conversation. A summary is prose that can be
   misread; the tests are ground truth.
3. `git log --oneline -15` to see what actually landed.

Finishing a task means: tests pass, `STATE.md` updated, one commit naming the
task number, pushed via `./scripts/git-sync.sh push`.

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
- **Server `87.107.110.199` runs a live, unrelated SNI proxy business**
  (CoreDNS on 53, nginx on TCP 443) with real users. Development and testing
  only. Do not disturb it. A dedicated server is bought before launch.
- **Physical test capacity is two PCs**, one host one client. Anything larger
  is simulated via `loadtest/`.

## Product rules (owner decisions — ask, never assume)

- Host leaves or crashes: match ends, room closes after a 2-minute grace that
  doubles as the host's chance to reconnect.
- Kicked player: barred from that room for 5 minutes, enforced server-side.
- Player who left voluntarily: may rejoin freely.
- While a room is `Locked - In Game`, **no new player may join** until the host
  explicitly reopens it to new players.
- Admins hold a reserved **spectator** slot outside the ten playing slots.
- MMR is self-declared, changeable once per week.
- Terms accepted at install, recorded against the account.

The product owner is non-technical. Bring product decisions to them with
options and trade-offs; make technical decisions yourself.
