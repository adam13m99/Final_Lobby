# Test client: download, self-update, lobby UI

**Goal:** One file, downloaded from a URL on the server, that installs itself
and gives two people a working GameRanger-style lobby to test with.

**Why this exists:** the first attempt shipped a folder to copy by USB, a
`setup.txt` holding the API token in plaintext, a setup screen asking a
non-technical user to paste a server address, and a 14-case checklist filled
in by hand. Every fix meant reinstalling on two machines. That is not a
testing method.

**Spec:** `docs/superpowers/specs/2026-08-18-lobby-platform-design.md`

## Owner decisions taken 2026-08-23

- **Identity: name only for now.** No password. A stable player ID is
  generated at install and kept locally. Known and accepted consequence:
  the 5-minute kick block can be dodged by reinstalling. Must be fixed
  before real players; not before this test.
- **Screens: Lobby and Room, switchable.** A player inside a room can go
  back to the lobby without leaving the room.
- **Chat in both.** Lobby chat and room chat.
- **Room shows ten slots plus the admin spectator slot.**
- **Player list shows declared MMR.**

## Server constraint

`87.107.110.199` now also runs the `nati-filter` control plane: WireGuard on
UDP 51821, a Python API on 127.0.0.1:8080, Postgres on 127.0.0.1:5432, and a
reverse tunnel to Paris. Public TCP 443 is no longer accepted from the
internet at all; that traffic arrives over WireGuard and is SNI-routed by
nginx from inside the tunnel.

**We add no new listening port and no new firewall rule.** The download is
served by the coordinator on TCP 7001, which is already ours and already
open. Nothing in this plan goes near nginx, WireGuard, or Postgres.

---

## Task 1 - Download endpoint

Coordinator serves the installer and a version manifest from an unguessable
path, plus a plain landing page with a download button.

- `GET /d/{key}/` - landing page
- `GET /d/{key}/FinalLobby-Setup.exe` - the installer
- `GET /d/{key}/version.json` - `{version, sha256, size, url}`

Unauthenticated by necessity (a browser fetches it), so the path segment is
the secret. Rate-limited. Files come from `/var/lib/finallobby/dist/`.

## Task 2 - Baked configuration

Coordinator URL, access token, relay address and relay public key are stamped
into the binary at build time via `-ldflags`. Deletes the setup screen,
`setup.txt`, and every manual step in Part 1 of the old checklist.

## Task 3 - One-file installer

`FinalLobby-Setup.exe` replaces `install.ps1`. Self-elevates once, installs
the service, opens the firewall, writes the shortcut, starts the app.

## Task 4 - Self-update

On launch the app fetches `version.json`. Newer version means download,
verify SHA-256, swap, relaunch. Without this, iterating during a test
session costs a reinstall on two machines per fix.

## Task 5 - Players and MMR

Server-side player registry: ID, nick, declared MMR, first seen, last seen.
MMR changeable once per week, enforced server-side.

## Task 6 - Chat

Lobby chat and per-room chat. In-memory ring buffer per channel, polled by
the client. Rate-limited per player.

## Task 7 - Room membership view

Room detail returns every seated player: slot, nick, MMR, host flag, plus
the spectator slot. This is what the lobby shows before you join.

## Task 8 - Spectator slot

Join as spectator, outside the ten playing slots. Already modelled in
`ipam.SpectatorIP`; needs the API and UI.

## Task 9 - Lobby and Room screens

Two screens, switchable without leaving the room. Lobby: room list with
players and MMR, plus lobby chat. Room: ten slot cards, spectator slot,
room chat, host controls, connect and launch.

## Task 10 - Diagnostics that report themselves

One button runs the checks the old checklist asked a human to run: server
reachable, tunnel up, ping the other player, throughput. Results post to
the coordinator so they can be read from the development machine instead
of being read aloud over a phone.

The only thing left for a human is whether a real Dota match runs.
