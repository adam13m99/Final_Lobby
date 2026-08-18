# Project state

Updated when a task completes. `bash scripts/check.sh` is the ground truth;
this file is a convenience index, not an authority.

## Current phase

Sub-project 1: network core. **In progress — Tasks 1-11 landed. Relay deployed; Wintun adapter proven on real hardware.**

## Blockers

| Blocker | Detail | Owner |
|---|---|---|
| ~~Go not installed~~ | Resolved 2026-08-18. Go 1.26.6 extracted to `C:\Users\Mcc\sdk\go` (no admin rights needed), fetched from the Aliyun mirror and verified against go.dev's own SHA-256. `scripts/env.sh` puts it on PATH for every script. See decisions D11. | resolved |
| ~~`make` not installed~~ | Replaced by `scripts/build.sh`. See decisions D12. | resolved |
| Uplink port speed unknown | MobinHost has not confirmed the server's port speed. Not blocking test-phase work. | product owner |
| Race detector unavailable locally | `go test -race` needs cgo and there is no C compiler on the dev PC. Run it on the Linux server, which has one. Not yet scripted. | open |
| ~~Relay not deployable~~ | Resolved 2026-08-18. `scripts/deploy.sh` builds, uploads and restarts it under systemd. Live on UDP 443 at 87.107.110.199, verified reachable from the dev PC at 4-8 ms. | resolved |

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
| 15 | Load test harness | not started | |
| 16 | Physical two-PC acceptance test | not started | |

## Completed outside the plan

| What | Commit |
|---|---|
| Design spec | `d727a18` |
| Implementation plan | `06e6a18` |
| Git sync through server tunnel (GitHub is DPI-blocked locally) | `dd28c05` |

## Open questions

1. **Can a new player take over an abandoned slot in a running Dota LAN match?**
   Reconnecting your own dropped player works; a different person filling the
   slot is unverified. The dynamic room flow depends on it. Answered by Task 16.
2. Real per-player bandwidth — estimated, not measured. Task 15/16.
3. Wintun redistribution licence — the DLL is embedded in the binary
   (`netservice/internal/adapter/bin/wintun.dll`, v0.14.1, Authenticode
   signature verified as WireGuard LLC). Confirm redistribution terms before
   shipping a public installer.
