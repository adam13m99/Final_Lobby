# Project state

Updated when a task completes. `bash scripts/check.sh` is the ground truth;
this file is a convenience index, not an authority.

## Current phase

Sub-project 1: network core. **Not started — blocked on toolchain.**

## Blockers

| Blocker | Detail | Owner |
|---|---|---|
| Go not installed | `go version` fails on the dev PC. Required for every task. Needs Go 1.23+. Note that golang.org may be unreachable; use a domestic mirror or install through the server tunnel. | dev machine setup |
| `make` not installed | Plan Task 1 assumed it. Superseded by `scripts/check.sh`, which needs only bash. | resolved by design |
| Uplink port speed unknown | MobinHost has not confirmed the server's port speed. Not blocking test-phase work. | product owner |

## Task ledger

Plan: `docs/superpowers/plans/2026-08-18-network-core.md`

| # | Task | Status | Commit |
|---|---|---|---|
| 1 | Repo scaffolding and Go workspace | not started | |
| 2 | Packet framing and codec | not started | |
| 3 | Virtual IP allocation | not started | |
| 4 | Routing decision (anti-spoof, room scope, broadcast drop) | not started | |
| 5 | Bounded per-peer send queue | not started | |
| 6 | Session encryption with replay protection | not started | |
| 7 | Noise NK handshake | not started | |
| 8 | Session and room membership tables | not started | |
| 9 | Relay server assembly | not started | |
| 10 | Room state machine | not started | |
| 11 | Windows Wintun adapter | not started | |
| 12 | Tunnel client with sticky reconnect | not started | |
| 13 | Fail-closed lease watchdog | not started | |
| 14 | Dota 2 launch with argument allowlist | not started | |
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
3. Wintun redistribution licence — confirm before shipping an installer.
