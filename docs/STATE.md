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
| 16 | Physical two-PC acceptance test | **in progress** — first session 2026-08-23 found and fixed the expiring-ticket bug (D36); no Dota match run yet | |

## Completed outside the plan

| What | Commit |
|---|---|
| Stub coordinator: rooms, tickets, rate limiting | `b1f5a1b` |
| Windows service, named-pipe IPC, test CLI | `cc8e395` |
| Prototype desktop app, installer, bundle | `1a986e1` |
| Design spec | `d727a18` |
| Implementation plan | `06e6a18` |
| Git sync through server tunnel (GitHub is DPI-blocked locally) | `dd28c05` |

## First two-PC session, 2026-08-23

Both machines installed from the link and reached the lobby. One created a
room, the other joined, and neither could connect: **the ticket a player
receives on joining expires after ten minutes, and nothing renewed it until
after the tunnel was already up.** Any pair of people who spend more than ten
minutes arranging a match hit it every time. See D36.

Fixed by minting the ticket at Connect instead of at join
(`POST /v1/rooms/{id}/connect`), deployed and published as `2026.08.23-1739`.
Verified by connecting successfully with a deliberately invalid stored
ticket, which failed reliably before the change.

Still to run, and needing a person at each machine: the real Dota 2 match,
bandwidth measurement, the abandoned-slot question, kick timing, network-drop
recovery, and chat across two screens.

## Open questions

1. **Can a new player take over an abandoned slot in a running Dota LAN match?**
   Reconnecting your own dropped player works; a different person filling the
   slot is unverified. The dynamic room flow depends on it. Answered by Task 16.
2. Real per-player bandwidth — estimated, not measured. Task 15/16.
3. Wintun redistribution licence — the DLL is embedded in the binary
   (`netservice/internal/adapter/bin/wintun.dll`, v0.14.1, Authenticode
   signature verified as WireGuard LLC). Confirm redistribution terms before
   shipping a public installer.
