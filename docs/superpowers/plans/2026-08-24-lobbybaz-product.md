# LobbyBaz product build — implementation plan

**Goal:** turn the proven network core into the product the owner specified in
D37–D49.

**Spec:** `docs/superpowers/specs/2026-08-18-lobby-platform-design.md`
**Decisions:** `docs/decisions.md`, D37–D49
**Owner's own words:** `docs/product-decisions-2026-08-23.md`

**Status:** in progress, started 2026-08-24. Tick tasks here as they land.

---

## How to pick this up

1. Read `docs/STATE.md`.
2. Run `bash scripts/check.sh`. Trust it over any prose, including this file.
3. `git log --oneline -20`.
4. Find the first unticked task below and continue.

Every task ends with: tests pass, `STATE.md` updated, one commit naming the
task, pushed with `./scripts/git-sync.sh push`.

---

## Global constraints

Carried from `CLAUDE.md` and unchanged by any of this:

- Broadcast and multicast are dropped, never scoped.
- Unreliable datagrams, never a reliable-ordered stream.
- Goroutine count scales with players, never with packet rate.
- Anti-spoof is mandatory: inner source IP equals the assigned virtual IP.
- Relay binds UDP 443 only. Never TCP 443.
- No custom cryptography.
- Never commit secrets.

---

## Sequencing, and why this order

**T1 first because it gets more expensive every hour it waits** — every file
added before the rename is another file to rename. **T2 before anything that
touches rooms**, because seat count is load-bearing. **T5 before T6–T8**,
because privacy, friends and admin roles are all meaningless without a real
account to attach them to.

The front-end (T9–T11) comes last deliberately: it is the largest single
piece, and building it against a room model still in flux would mean building
it twice.

---

## T1 — Rename the product to LobbyBaz *(D46)* — **DONE**

- [x] Go module paths `finallobby/*` → `lobbybaz/*` across all 9 modules
- [x] Windows service `FinalLobbyNet` → `LobbyBazNet`
- [x] Install directory `Final Lobby` → `LobbyBaz`
- [x] Virtual adapter name → `LobbyBaz`
- [x] Per-user config directory → `LobbyBaz`
- [x] Installer filename → `LobbyBaz-Setup.exe`
- [x] UI strings, window title, shortcut, Add/Remove Programs entry
- [x] Installer removes the old service and old directory on upgrade
- [x] Docs and scripts

**Deliberately not renamed: server-side paths.** `/etc/finallobby`,
`/var/lib/finallobby`, `/opt/finallobby`, the `finallobby` unix user and the
systemd unit names stay as they are. They are invisible to players, and
renaming them means moving `relay.key` — which cannot be regenerated, because
every installed client carries its public half — on a box that also runs the
owner's live unrelated business. The risk is real and the payoff is zero.
Recorded as **D50**.

**The upgrade path is the part that can go wrong.** Two machines have
`Final Lobby` installed with a registered `FinalLobbyNet` service. The new
installer must stop and delete both, not sit beside them, or those machines
end up with two services fighting over one virtual adapter.

## T2 — Rooms move to a /27 *(D38)* — **DONE**

- [x] `ipam`: `/28` → `/27`, `MaxRooms` 4096 → 2048
- [x] Seat layout: `.2`–`.11` players, `.12`–`.16` observers, `.17`–`.19` admins
- [x] `ObserverSlots = 5`, `AdminSlots = 3`, replacing `SpectatorSlots = 3`
- [x] Room state machine carries both seat kinds separately
- [x] Tests: full room of 18, boundary addresses, room index 2047

## T3 — Room lifecycle *(D40)* — **DONE**

- [x] `HostGracePeriod` 2 min → 1 min
- [x] A finished match no longer closes or locks the room
- [x] Tests: match ends and the room survives with everyone still seated

## T4 — Kick escalation *(D39)* — **rules done, persistence pending**

- [x] Block is 1, 3, 5, 7… minutes — first offence 1, then +2 each time
- [ ] Count is per player per room and survives a coordinator restart
- [x] Tests: escalation sequence *(restart-survival test lands with T5, which brings the database)*

## T5 — Accounts *(D37)* — **done 2026-08-24**

- [x] Username and password, Argon2id — `coordinator/internal/account/password.go`,
      parameters encoded into every hash so the cost can be raised later
      without invalidating anybody's password
- [x] `contact_methods` table from the first migration, empty — the seam for
      email and SMS, so neither is a schema change later
- [x] Recovery exists only where a verified contact method exists; signup says
      so plainly (`can_recover_password` in every auth reply)
- [x] Sessions: device-bound rotating tokens — only the SHA-256 hash is
      stored, expiry slides on use, a password change ends every device
- [x] Terms acceptance recorded with version and timestamp; an account cannot
      come into existence without it
- [x] Tests: signup, login, wrong password, session rotation, no-recovery path
- [x] **Beyond the checklist:** the session is now what decides who a request
      is from (D53). It closes a hole that predated accounts — anybody could
      act as anybody by typing their ID.
- [x] The kick escalation T4 deferred: stored as events, not as live blocks,
      and room IDs made non-reusable so nothing can key into the wrong room
      (D52)

**Not done, deliberately:** the coordinator on the server still runs *without*
`-db`, because no shipped client can sign in yet. Turning accounts on before
T10 lands the sign-in screen would lock the owner's two test PCs out of their
own lobby. The switch is one flag; flip it in T10.

## T6 — Room privacy *(D41)* — **done 2026-08-24**

- [x] Public, password, friends-only, invite-only —
      `coordinator/internal/room/privacy.go`
- [x] Minimum MMR admission, read from the account row and never from the
      request *(D42 room list shows it)*
- [x] Tests: each privacy kind admits and refuses correctly, at the room
      layer and over HTTP

Notes for whoever picks this up:

- The door is one function, `Room.knock`. Everything it checks except the
  typed password is established by the coordinator: MMR from the account row,
  friendship from the friend graph, invitation from the room's own list. A
  client can lie about none of it.
- **The password hash is unexported** (`passwordHash`), so a room cannot be
  serialised with its password in it by somebody adding a field to a view.
- Staff walk past the door; nobody walks past a kick block.
- Changing the door never evicts anybody already seated.
- The friend graph is an interface, `api.Friends`, filled by T7. Without one,
  a friends-only room refuses everybody but its host — it refuses rather than
  quietly admitting everybody.

## T7 — Friends *(D41)*

- [ ] Add, remove, block
- [ ] Private chat
- [ ] Invite to room
- [ ] Online/offline, and in-game/not-in-game — the service already knows
      whether Dota is running, so this is surfacing a signal we have
- [ ] Tests: request/accept/remove, invite reaching the right person

## T8 — Roles and moderation *(D43, D47)*

- [ ] Role grants are records with an author and a timestamp, not booleans
- [ ] Exactly one head admin; only they appoint or remove admins
- [ ] Kick, ban, mute, timeout
- [ ] Close a room, change its host
- [ ] Player labels
- [ ] Banner strip: add, remove, edit
- [ ] Every admin action attributed to the admin who took it
- [ ] Tests: an admin cannot appoint another admin; every action is attributed

## T9 — Interface built for translation *(D44)*

- [ ] All user-facing text through a lookup, none typed into markup
- [ ] Layout in logical properties so direction flips on its own
- [ ] No hard-coded left/right anywhere
- [ ] English is the only language shipped; Persian is a file and a switch

## T10 — The new lobby *(D42)*

- [ ] Room list: host, description, minimum MMR, player count, status, and the
      host's relay latency labelled as the host's, not the player's
- [ ] Friends rail down the right
- [ ] Tabbed collapsible chat: lobby, friends, party
- [ ] Profile, top right
- [ ] Left toolbar: Lobby, Room, Tournaments, Profile, connection status
- [ ] Filter and search
- [ ] Banner strip

## T11 — Desktop application *(D45)*

- [ ] Tauri window replacing the browser page
- [ ] Minimise to tray
- [ ] Notify when a room fills or a host starts
- [ ] Browse the lobby before signing up; account asked for at join

## T12 — Tournaments *(D48)*

Not designed yet, and deliberately not built here. The room and account models
are built knowing that a tournament match is a room somebody else created at a
time nobody in it chose. The toolbar entry ships pointing at an honest
"coming soon" rather than a dead link.

---

## Open, and not blocking

- The abandoned-slot question — needs two PCs and a person at each.
- Ten-player bandwidth — needs a real game with more than two players.
- Uplink port speed — waiting on MobinHost.
- A refused handshake is silent; the client can only report a timeout.
