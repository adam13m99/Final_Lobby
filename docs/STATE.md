# Project state

Updated when a task completes. `bash scripts/check.sh` is the ground truth;
this file is a convenience index, not an authority.

## Current phase

Sub-project 1: network core. **The app is downloadable and installs
itself.** Relay and coordinator are deployed; the Windows service, desktop
app and installer all work on the development PC; Dota 2 launches and its
listen server comes up. What remains is Task 16 itself - the real two-PC
match - which needs a second machine and a person at each.

**To test on a second PC:** run `./scripts/publish.sh`. It builds, stamps
the server details into the binaries, uploads, and prints a link. Open that
link on each PC, run the file, allow one prompt, pick a name. Then follow
`docs/testing/two-pc-acceptance.md`.

Publishing again during a session is how fixes reach both machines: an
installed copy notices on its next launch and offers the new build.

Diagnostics are run by the app, not by hand, and uploaded. Read both
machines' results from here with:

```bash
curl -s -H "Authorization: Bearer $TOKEN" http://87.107.110.199:7001/v1/diag
```

## Blockers

| Blocker | Detail | Owner |
|---|---|---|
| ~~Go not installed~~ | Resolved 2026-08-18. Go 1.26.6 extracted to `C:\Users\Mcc\sdk\go` (no admin rights needed), fetched from the Aliyun mirror and verified against go.dev's own SHA-256. `scripts/env.sh` puts it on PATH for every script. See decisions D11. | resolved |
| ~~`make` not installed~~ | Replaced by `scripts/build.sh`. See decisions D12. | resolved |
| Uplink port speed unknown | MobinHost has not confirmed the server's port speed. Deferred by the owner until the product works. | product owner |
| ~~No second machine for load generation~~ | Resolved differently: the owner set the target to 500 concurrent players (2026-08-18), which is verifiable on this box. Measured 0.000% loss, 2.8 ms median, 1.6 of 4 cores used. No second VPS needed. | resolved |
| Data plane not using batched syscalls | One `sendto`/`recvfrom` per packet caps the relay near 30k pps, which covers the 500-player target with headroom but not much beyond it. `recvmmsg`/`sendmmsg` batching is the standard fix. Deferred: not needed at the current target. | deferred |
| Race detector unavailable locally | `go test -race` needs cgo and there is no C compiler on the dev PC. Run it on the Linux server, which has one. Not yet scripted. | open |
| ~~Relay not deployable~~ | Resolved 2026-08-18. `scripts/deploy.sh` builds, uploads and restarts it under systemd. Live on UDP 443 at 87.107.110.199, verified reachable from the dev PC at 4-8 ms. | resolved |

## Test client (plan: `docs/superpowers/plans/2026-08-23-test-client.md`)

Built 2026-08-23 after the owner rejected the copy-a-folder test method.

| # | Task | Status |
|---|---|---|
| 1 | Download endpoint on the coordinator | **done** |
| 2 | Server details stamped into the binary at build time | **done** |
| 3 | One-file self-elevating installer | **done** |
| 4 | Self-update | **done** — verified over three consecutive builds |
| 5 | Players and self-declared MMR | **done** |
| 6 | Lobby chat and room chat | **done** |
| 7 | Room membership with names and MMR | **done** |
| 8 | Admin spectator seat | **done** |
| 9 | Lobby and Room screens, switchable | **done** |
| 10 | Diagnostics that report themselves to the server | **done** |

## Task ledger

Plan: `docs/superpowers/plans/2026-08-18-network-core.md`

| # | Task | Status | Commit |
|---|---|---|---|
| 1 | Repo scaffolding and Go workspace | **done** | |
| 2 | Packet framing and codec | **done** | |
| 3 | Virtual IP allocation | **done** | |
| 4 | Routing decision (anti-spoof, room scope, broadcast drop) | **done** | |
| 5 | Bounded per-peer send queue | **done** | |
| 6 | Session encryption with replay protection | **done** | |
| 7 | Noise NK handshake | **done** | |
| 8 | Session and room membership tables | **done** | |
| 9 | Relay server assembly | **done** | |
| 10 | Room state machine | **done** | |
| 11 | Windows Wintun adapter | **done** | |
| 12 | Tunnel client with sticky reconnect | **done** | |
| 13 | Fail-closed lease watchdog | **done** | |
| 14 | Dota 2 launch with argument allowlist | **done** | |
| 15 | Load test harness | **done** | |
| 16 | Physical two-PC acceptance test | **gate passed** — a real Dota 2 match ran between two PCs over the relay, 2026-08-23, build `2026.08.23-1739`. Remaining cases (abandoned slot, kick timing, drop recovery) still to run | |

## Completed outside the plan

| What | Commit |
|---|---|
| Stub coordinator: rooms, tickets, rate limiting | `b1f5a1b` |
| Windows service, named-pipe IPC, test CLI | `cc8e395` |
| Prototype desktop app, installer, bundle | `1a986e1` |
| Design spec | `d727a18` |
| Implementation plan | `06e6a18` |
| Git sync through server tunnel (GitHub is DPI-blocked locally) | `dd28c05` |

## First two-PC session, 2026-08-23 — the gate is passed

**A real Dota 2 match ran between two physical PCs over our relay.** That was
the condition for the whole network core, and for starting sub-project 2.

Independently confirmed on the relay rather than taken on trust: `peers=2`,
**13,162 packets forwarded**, `dropped_queue=0`, `write_errors=0`,
`auth_failed=0`.

Two things had to be fixed or understood to get there:

1. **Tickets expired while players waited in the room** (D36). A ticket is
   good for ten minutes and was issued at join, while the watchdog that
   renews it only started once the tunnel was already up. Any pair of people
   who spent more than ten minutes arranging a match hit it every time.
   Fixed by minting the ticket when Connect is pressed; published as
   `2026.08.23-1739`.
2. **Nobody knew Connect was a step.** The first attempt failed with both
   players in the room, Dota hosting, and no tunnel on either machine. This
   is a design fault, not a testing mistake, and it is still open — see
   `docs/testing/two-pc-acceptance.md`.

First real bandwidth measurement, host side, two players: **115 kbps out,
42 kbps in per client.** Extrapolated to ten players that is ~1.04 Mbps out
and ~378 kbps in for the host, against the 1.2 Mbps each way the capacity
model assumes. A two-player game is lighter than a full one, so treat this as
a floor that supports the model rather than confirming it.

Still needing a person at each machine: the abandoned-slot question, kick
timing, network-drop recovery, and chat across two screens.

## Product direction settled, 2026-08-24

The owner answered the full decision suite. Recorded as **D37-D46** in
`docs/decisions.md`; the questions and their answers in the owner's own words
are in `docs/product-decisions-2026-08-23.md`.

The headline choices, and what they change:

| Decision | Consequence for the build |
|---|---|
| Username + password, with the seams for email/SMS (D37) | Closes D31. First real schema. |
| 18 seats per room: 10 players, 5 observers, 3 admins (D38) | **/28 no longer fits. Rooms move to /27**, ceiling 4096 -> 2048. |
| Kick blocks escalate 1, 3, 5, 7 minutes (D39) | First room state that must be persisted across restarts. |
| Room dies 1 minute after the host; a finished match does not close it (D40) | Replaces the 2-minute rule and the locked-after-match flow. |
| All four room privacies, friends before launch (D41) | Friends system moves ahead of launch, not after. |
| The lobby becomes a place: friends rail, tabbed chat, toolbar, ads (D42) | Largest single piece of new front-end work. |
| Admin powers, including player labels (D43) | Needs a named human before it means anything. |
| English first, Persian later (D44) | Layout built direction-agnostic now so Persian stays cheap. |
| Tauri, tray, browse-before-signup (D45) | The prototype browser page is retired. |
| Renamed **LobbyBaz** (D46) | Touches service name, install path, adapter, module paths, docs. |

Two of these contradict what is already built or written, and are called out
in their entries rather than left to be discovered: **the /27 migration**
(D38) and **Tournaments**, which section 12 of the spec lists as explicitly
out of scope (D42).

## LobbyBaz build, started 2026-08-24

Plan: `docs/superpowers/plans/2026-08-24-lobbybaz-product.md`. Tasks tick
there; this is the summary.

| Task | State |
|---|---|
| T1 rename to LobbyBaz (D46) | **done** — `77d8a0d` |
| T2 rooms move to a /27, 18 seats (D38) | **done** |
| T3 room lifecycle: 1-minute host grace (D40) | **done** |
| T4 kick escalation 1/3/5/7 min (D39) | **done** — as events, not live blocks (D52) |
| T5 accounts (D37) | **done** — server and client library; **not switched on yet, see below** |
| T6 room privacy (D41) | not started |
| T7 friends (D41) | not started |
| T8 roles and moderation (D43, D47) | not started |
| T9 interface built for translation (D44) | not started |
| T10 the new lobby (D42) | not started |
| T11 desktop application (D45) | not started |

**Accounts exist but are not switched on.** The coordinator takes a `-db`
file; with one it has usernames, Argon2id passwords, sessions, durable MMR and
terms acceptance, and the session — not the request body — decides who a
request is from (D53). Without one it behaves exactly as it did during the
two-PC test. **The server is still running without `-db`,** because no shipped
client has a sign-in screen yet; turning it on before T10 would lock both test
PCs out of their own lobby. Flipping it is one flag in
`coordinator.service`, and it is T10's job.

**A kick is now two things.** The block that bars a kicked player for one,
three, five minutes lives in memory with the room, and ends when the room
does. Every kick is also written to `kick_events` — who, by whom, from where,
how long — which is the part still true when a moderator looks a month later
(D52). Room IDs became random and non-reusable in the same change; the old
ones repeated after a restart, and anything keyed by one could have attached
itself to the wrong room.

**"Spectator" is now two things.** An observer is an ordinary player choosing
to watch; an admin is staff. They have separate seat counts, separate address
ranges, and different rules: an observer may not enter a locked room, an admin
may. `POST /v1/rooms/{id}/spectate` seats an **observer**; the admin seat is
reached separately and arrives with T8.

## Open questions

1. **Can a new player take over an abandoned slot in a running Dota LAN match?**
   Reconnecting your own dropped player works; a different person filling the
   slot is unverified. The dynamic room flow depends on it. Answered by Task 16.
2. Real per-player bandwidth at a **full** game — first measurement taken
   2026-08-23 at two players (115 kbps out / 42 kbps in per client), which
   supports the 1.2 Mbps model but does not confirm it. Needs a game with
   more than two players.
3. Wintun redistribution licence — the DLL is embedded in the binary
   (`netservice/internal/adapter/bin/wintun.dll`, v0.14.1, Authenticode
   signature verified as WireGuard LLC). Confirm redistribution terms before
   shipping a public installer.
4. **Uplink port speed.** Open since 2026-08-18. The last unknown in the
   capacity plan, and every server cost figure rests on it. **Waiting on
   MobinHost** — the Persian letter to send them is
   `docs/mobinhost-port-speed-letter.md`.
5. A refused handshake is silent, so the client can only report a timeout.
   That is what turned the D36 ticket bug into an hour of investigation.
   Engineering; needs a protocol change.
6. **What a tournament actually is** — brackets, scheduling, prizes,
   registration, who runs one. D48 settles that it is a real feature; its
   shape is undecided and needs its own brainstorm. Not before the account
   system, the room work and the new lobby.

**Answered 2026-08-24:** who the admins are (D47 — a role the head admin
grants), whether Tournaments is real (D48 — yes), and when the dedicated
server is bought (D49 — when the product is ready to deploy).
