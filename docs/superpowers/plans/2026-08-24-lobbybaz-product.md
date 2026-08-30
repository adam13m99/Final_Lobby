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

## T4 — Kick escalation *(D39)* — **done** *(the persistence box was answered by D52, not built)*

- [x] Block is 1, 3, 5, 7… minutes — first offence 1, then +2 each time
- [x] Count is per player per room — **and deliberately does not survive a
      coordinator restart.** This box asked for the opposite; D52 examined it
      and reversed it. A block belongs to a room, a restart ends every room,
      so a persisted block would key into a room that no longer exists and bar
      nobody — or, back when room IDs were reused, key into a *different* room
      and bar an innocent person from one they had never entered. What is
      durable is the `kick_events` record, which is what a moderator reads
      months later (T8). Room IDs became sixteen random hex characters in the
      same change and are never reused.
- [x] Tests: escalation sequence

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

## T7 — Friends *(D41)* — **done 2026-08-24**

- [x] Add, remove, block — `coordinator/internal/social/`
- [x] Private chat, durable, friends only
- [x] Invite to room
- [x] Online/offline, and in-game/not-in-game — reported by the player's own
      service, which knows because it launched Dota and watches its log
- [x] Tests: request/accept/remove, invite reaching the right person, and the
      block rules

Notes for whoever picks this up:

- **A friendship is two directed rows.** One row is "A asked B"; two rows are
  a friendship. That is what makes a pending request expressible, and it makes
  removal one deletion rather than a search for whichever ordering was stored.
- **A block is not the absence of a friendship.** It is its own row,
  one-directional, and it outranks everything. Several endpoints therefore
  return 200 for something that did not happen — a blocked person's message or
  request is accepted and dropped. That is not a bug: an error would tell them
  they had been blocked, which turns blocking into a message of its own.
- **"In game" is reported, never inferred.** A room can be locked while its
  host is still on the hero screen. `POST /v1/sync` carries `in_game` from the
  service; the coordinator never guesses it from room state.
- Private messages are durable; lobby and room chat are not. A message to
  somebody offline has to still be there when they come back.
- `api.Friends`, the seam T6 left, is now filled by `social.Store`. Note
  `friendsOrNil` in `cmd/coordinator`: a typed nil in an interface is not nil,
  and passing the pointer straight through would panic the first time somebody
  opened a friends-only room on a coordinator with no database.

## T8 — Roles and moderation *(D43, D47)* — **done 2026-08-24**

- [x] Role grants are records with an author and a timestamp, not booleans
- [x] Exactly one head admin; only they appoint or remove admins
- [x] Kick (T4), ban, mute, timeout — and each one actually stops what it
      says: a ban drops every session and empties the seat, a timeout bars
      joining, a mute bars chat and nothing else
- [x] Close a room, change its host
- [x] Player labels, from a fixed set
- [x] Banner strip: add, remove, edit, and prepare-before-publishing
- [x] Every admin action attributed to the admin who took it
- [x] Tests: an admin cannot appoint another admin; every action is attributed

Notes for whoever picks this up:

- **The head admin is bootstrapped with `-head-admin <account-id>`, once, at
  deployment.** D47: a self-service path to the most privileged role in the
  system is a door with no purpose. Running it twice for the same person is
  harmless; a second, different head admin is refused.
- **Nothing is a boolean on an account.** A grant, a ban, a label — each is a
  row with an author and a timestamp, and lifting one stamps it rather than
  deleting it. A moderation table you can erase by undoing things is not a
  record.
- **Staff cannot be used against each other.** An ordinary admin cannot
  sanction another admin or the head admin; only the head admin can. Otherwise
  one compromised or angry admin could remove the team.
- **Banner text must be rendered as text, never as HTML** — it is content one
  person writes and everybody's client displays. Links are restricted to http
  and https server-side, because this is a desktop application and a
  `javascript:` link in it is a way to run something on a player's machine.
- Changing a room's host is a slot swap: the host is always slot 0, because
  slot 0 is the address every client was told to connect to. Both swapped
  players change address, so both tickets are revoked and reissued at Connect.
  **It does not rescue a match in progress** — the Dota server was on the old
  host's PC.
- The *Noob* label ships because the owner asked for it. The concern recorded
  in D43 stands, and removing it is a one-line change to
  `moderation.KnownLabels` — which is why the set lives on the server and the
  client asks for it.

## T9 — Interface built for translation *(D44)* — done

- [x] All user-facing text through a lookup, none typed into markup
- [x] Layout in logical properties so direction flips on its own
- [x] No hard-coded left/right anywhere
- [x] English is the only language shipped; Persian is a file and a switch

**Notes for whoever picks this up.**

The lookup is `lobbyapp/ui/i18n.js` and the catalogue is
`lobbyapp/ui/strings/en.json`. In markup, `data-t="some.key"` fills an
element's text and `data-t-placeholder` / `data-t-title` / `data-t-aria-label`
fill those attributes; in the renderer, `t("some.key", {name: value})`.
Nothing draws until the catalogue has loaded, so a screen never flashes its
keys and corrects itself.

**The rules are enforced, not merely written down.** `lobbyapp/ui_test.go`
fails the build on text typed into the markup or into `app.js`, on a key that
does not exist, on a catalogue entry nothing uses, on a language missing a key
another language has, on a placeholder that did not survive translation, on
any hard-coded `left`/`right`, and on a strings file that did not make it into
the embedded filesystem. That last one matters: `go:embed` skips files quietly
and the failure would otherwise appear only on an installed copy, as a blank
window. Each guard was checked by breaking the thing it guards and watching it
fail.

Two limits worth knowing before T10:

- **Server-side error text is not translated.** Messages from the coordinator
  and the net service arrive already written in English and pass straight to
  the banner. Translating them means translating them in Go, at their source.
  It is a separate surface and a later decision, not an oversight.
- **Block-axis physical properties are allowed on purpose** — `top`, `bottom`,
  `border-bottom`, `margin-block`. Right-to-left flips the inline axis only;
  banning the rest would be noise that teaches people to ignore the test.

Adding Persian is now: copy `strings/en.json` to `strings/fa.json`, translate
the values, and uncomment the `fa` line in `i18n.js`. Every test above starts
guarding it the moment the file exists.

## T10 — The new lobby *(D42)* — done

- [x] Room list: host, description, minimum MMR, player count, status, and the
      host's relay latency labelled as the host's, not the player's
- [x] Friends rail down the right
- [x] Tabbed collapsible chat: lobby, friends, party
- [x] Profile, top right
- [x] Left toolbar: Lobby, Room, Tournaments, Profile, connection status
- [x] Filter and search
- [x] Banner strip

**Notes for whoever picks this up.**

The screen is a CSS grid: toolbar, stage, rail. It is written in logical
properties, so the toolbar is a grid *column* rather than a positioned
element and moves to the other side on its own under `dir="rtl"`.

**The latency column was the part that needed inventing**, and it is written
up as D54. Short version: nobody in the lobby can ping a room they have not
joined, so the column is the *host's* round trip to the relay, which the relay
now measures by echoing keepalives. It is labelled in the header and again on
every cell, because a player who reads it as their own ping blames the wrong
thing. Zero is drawn as unknown, never as excellent. **The relay must be
redeployed before the column shows anything.**

**Two entries ship honest rather than absent.** Tournaments and the party chat
tab both say they are not built. A dead link is worse than a sentence, and
putting them in now means the toolbar and the chat do not move when they
arrive.

**The friends rail degrades on purpose.** A coordinator with no database has
no accounts and therefore no friends list — which is what the live server is
running right now. The rail says "this server has no friends list yet" and the
lobby carries on. It is not an error the player caused.

**The friends list and the announcement strip are cached** (`lobbyapp/social.go`),
five seconds and five minutes, with a minute's sulk after a failure. The lobby
polls every two seconds because rooms fill in seconds; a friends list does not,
and an announcement changes about once a week.

**Three new tests guard the thing this interface is most exposed to:** it draws
by reaching into the document by name, so a renamed element does not raise an
error — it silently stops being filled in, and the first sign is a blank space
on the screen. `ui_test.go` now fails the build when the renderer reaches for an
id, a class, or a data attribute the markup does not have.

**Not verified in a browser.** Everything here passes its tests, but no human
has looked at the rendered page. That needs `./scripts/publish.sh` and one of
the two test PCs.

## T11 — Desktop application *(D45)* — done

- [x] Tauri window replacing the browser page
- [x] Minimise to tray
- [x] Notify when a room fills or a host starts
- [x] Browse the lobby before signing up; account asked for at join

**Notes for whoever picks this up.**

`desktop/` is a Tauri shell and nothing more: it starts the Go binary with
`-url-only`, reads the loopback address off its first line, and points a
webview at it. Why it is a shell rather than a rewrite is D55 — briefly, the
Go client is the only code here that has actually carried a Dota match, and
what a browser page genuinely cannot do is exactly the small thing the shell
adds.

Build it with `./scripts/build.sh desktop`. Since D67 the installer ships
it; this plan predates that, and the sentence below is left as written so the
history reads honestly. It is **not** wired into
`publish.sh` yet, and that is deliberate: publish.sh is the owner's only
distribution channel and the installer it makes is known to work. Swapping it
for one nobody has installed on a real machine risks the thing that works to
ship the thing that is not yet proven. Install the shell by hand on a test PC
first.

**Browse-before-signup needed a server change, not just a client one.** With
accounts on, `POST /v1/sync` used to require a session, so a new install could
not see the lobby at all. It is now open: without a session it answers with
the room list, the lobby chat and the online count, and nothing that belongs
to a person - no presence recorded, no room, no room chat. See `Server.browse`.

**The gate has three shapes**, chosen from what the server says it can do at
`GET /healthz`: a typed name (no accounts on this server), sign in, or create
an account. The live coordinator still runs without `-db`, so today it is the
first of those - and the app must keep working that way, which is why the
capability is asked for rather than assumed.

**The session is stored** in the session file and attached to every call, so
nobody types a password twice a day. The password itself is never written
down. Signing out ends the session on the server as well, because the case
where signing out matters is a machine somebody has just walked away from.

**What was verified:** sign-up, sign-in, room creation, description, sign-out
and anonymous browsing all exercised end to end against a real coordinator
with `-db`; the shell launches, spawns its sidecar without a console window,
and the webview polls the local server. **What was not:** nobody has looked at
the rendered window, and the NSIS bundle has never been built or installed.

## T12 — Tournaments *(D48)* — done, as specified

Not designed yet, and deliberately not built here. The room and account models
are built knowing that a tournament match is a room somebody else created at a
time nobody in it chose. The toolbar entry ships pointing at an honest
"coming soon" rather than a dead link.

That is what shipped: `#screen-tournaments` in `lobbyapp/ui/index.html` says
what it is for and that it is not built yet, and the toolbar entry beside
Lobby, Room and Diagnostics will not move when the feature arrives. Closing
this task is not a claim that tournaments exist; it is the claim that the
placeholder the plan asked for is in place and honest. The feature itself
needs its own brainstorm — open question 6 in `docs/STATE.md`.

## T13 — The terms somebody is asked to accept *(unplanned, 2026-08-25)*

T11's sign-up screen asks for a tick against "I accept the terms of use", and
until now there were no terms anywhere: not on the server, not in the repo, not
on screen. A checkbox against a document nobody can read is worse than no
checkbox, and the account record stores an `accept_terms_version` against it.

- `docs/terms-en.md` — the text, in plain language. **Engineering's draft, not
  legal advice**; it opens saying so, and needs the owner's sign-off.
- `coordinator/internal/api/terms.go` — `GET /v1/terms` serves
  `{"version", "text"}`, reading the file once and caching it. The path comes
  from `-terms-file`; **without that flag the server serves a placeholder
  saying it is misconfigured** rather than inventing an agreement.
- `client/lobby` — `Terms()`; `lobbyapp` — `GET /api/terms`, cached for two
  minutes like the other read-only lookups.
- The UI — a "Read the terms" link beside the checkbox opening a dialog that
  renders the text into a `<pre>` as **textContent, never markup**, so the
  words on screen cannot differ from the words on file.

Changing the text means bumping `TermsVersion`, which is what makes existing
acceptances stale. Deploying it means passing `-terms-file` to the coordinator.

## T14 — The moderation panel *(unplanned, 2026-08-25)*

T8 built roles, sanctions, labels, banners and an audit log on the
coordinator. Nothing that ships could reach any of it: the only way to ban
somebody was a hand-written request from a developer's machine. That is the
same shape of gap accounts had before T11 — a whole subsystem with no door —
and it fails the same way. A moderator who cannot moderate from the product
moderates from a chat window instead, and nothing they do there is written
down.

- `lobbyapp/admin.go` — the app's half. Player lookup by username (staff are
  told "smurf_1234 is ruining games", never an account id), sanction, lift,
  label, close a room, hand a room to somebody else in it, post and delete
  announcements, appoint and remove admins, read the audit log.
- **The app knows whether to draw the tools by reading the staff list**, which
  any signed-in account may read, and matching its own id against it. The
  coordinator has no "what am I" endpoint for roles and does not need one:
  hiding the entry is a courtesy to people who are not staff, never a defence
  against them. Every call behind it is refused server-side without a role.
- **A reason travels with every action**, refused client-side as well as
  server-side. The audit log is read months later by somebody who was not
  there, and "banned" with nothing beside it cannot be reviewed or appealed.
- Zero minutes means a sanction that does not expire. The form makes that a
  choice rather than what an empty field happens to produce.
- The staff panel is drawn for the head admin alone (D47).

Covered end to end by `scripts/smoke.sh`, which appoints a head admin,
sanctions somebody, lifts it, checks both actions reached the audit log,
posts an announcement, and renders the page as both an ordinary player and a
moderator to confirm the entry is hidden from one and offered to the other.

## T15 — The door, from the host's side *(unplanned, 2026-08-25)*

T6 built four doors and an MMR floor on the coordinator (D41). The lobby drew
padlocks for them and asked for a password when joining. No host could ever
make such a room: the app created every room public and had no control for
changing one. The same gap again, and the most visible of the three — a
player asking for a private game had nowhere to make one.

- `POST /api/rooms/create` now carries the door, so a private room is private
  from the moment it exists. Opening it public and locking it a second later
  is a second in which anybody can walk in.
- `POST /api/rooms/privacy` changes the door on a room already open.
- The create form and the room screen both carry a door select, a password box
  that appears only for the door that uses one, and an MMR floor.
- The password is never filled back in from the server, because the server
  never sends it: an empty box means "leave it as it is".
- **One word, two things.** Inviting a friend now also opens an invite-only
  room's door to them. Doing only the notification produced the worst outcome
  available: somebody asked to come and then refused at the door, with the
  host certain they had invited them.

`scripts/smoke.sh` covers all of it with two players: a password door refusing
and then admitting, a password door with no password refused, an MMR floor,
and an invite-only room refusing somebody and admitting them after an
invitation.

## T16 — A password somebody can change, and terms that can move *(unplanned, 2026-08-25)*

Two more things the client library could do and no screen offered.

**Changing a password.** The sign-up screen says plainly that a forgotten
password cannot be reset, which makes this the whole of what a person can do
when they think somebody else knows theirs. It is its own dialog, reached from
the profile card, and it requires the current password: a session left open on
a shared PC must not be enough to lock the owner out of their own account. The
coordinator ends every other session and issues this one a new token, which
the app stores, so the window that made the change stays signed in.

**Terms that changed.** `TermsVersion` is a constant, `HasAcceptedTerms` is a
query, and the coordinator has always returned `terms_accepted` on the account
"so the client can decide whether to show" a prompt. No client ever did. Now a
strip appears — signed in, a version in force, this account not on it — with
both halves of what the person needs: read them, then accept them.

The coordinator throttles authentication to five attempts and then one every
five seconds. `smoke.sh` waits rather than asking for the limit to be lifted:
a test that needed it lifted would be testing a server nobody runs.

The render check earned its place twice here. It caught a password form with
no username field beside it — real, because password managers then save the
new password against the wrong account — fixed with a hidden field carrying
`autocomplete="username"`. It caught the same complaint about the room
password box, where the right answer was the opposite: a room password is a
shared secret typed by everybody who joins, not a credential, so
`autocomplete="off"` is both the fix and the honest description.

---

## Open, and not blocking

- The abandoned-slot question — needs two PCs and a person at each.
- Ten-player bandwidth — needs a real game with more than two players.
- Uplink port speed — waiting on MobinHost.
- A refused handshake is silent; the client can only report a timeout.
