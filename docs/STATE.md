# Project state

Updated when a task completes. `bash scripts/check.sh` is the ground truth;
this file is a convenience index, not an authority.

## Current phase

Sub-project 1: network core. **The app is downloadable and installs
itself.** Relay and coordinator are deployed; the Windows service, desktop
app and installer all work on the development PC; Dota 2 launches and its
listen server comes up. What remains is Task 16 itself - the real two-PC
match - which needs a second machine and a person at each.

**To watch the product while it is being changed:** run `bash scripts/live.sh`,
open the address it prints once, and leave that window open. It serves the
interface from disk and tells the page when a file changed, so every edit to
the stylesheet, the page, the scripts or the strings shows up by itself within
two seconds. Go changes are rebuilt and the app restarted at the same address.
The address is the same every run — the token is kept in `scripts/.live-token`,
which is gitignored — so the window can be pinned and left for days.

**To look at the product on this PC:** run `./scripts/try.sh`. It builds a
coordinator and an app, starts both on loopback, fills the lobby so there is
something to see, and opens it. No installer, no server, no tunnel — so the
room screen will say the network service is not running, and everything else
is real. Ctrl-C stops it and deletes the throwaway database.

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

## How to know it works

| Command | What it proves | Cost |
|---|---|---|
| `bash scripts/check.sh` | every module builds, vets, passes its own tests; the front-end JS and string files parse; no secret is tracked; the Rust shell compiles | seconds |
| `./scripts/try.sh` | nothing — it runs the product. A coordinator and an app on this PC, a lobby seeded with four players and three rooms, opened in the browser. Ctrl-C deletes everything it made | ~40s |
| `bash scripts/preview.sh <name>` | nothing — it photographs. Boots a real coordinator and app on throwaway data, seeds four players and three rooms with different doors, and drives headless Chrome through every screen into `scripts/shots/<name>/` | ~40s |
| `bash scripts/live.sh` | nothing — it **is** the product, running, at one fixed address, updating itself. Front-end edits appear in the open window within two seconds; Go edits are rebuilt and the app restarted at the same address. Open it once and leave it | ~60s to start, then stays up |
| `bash scripts/chatcheck.sh` | that a private message arriving **opens the chat dock by itself and gives the sender a tab** — driven live over the DevTools Protocol, because the dock reacts to a change between two polls and a single snapshot can never show it | ~50s |
| `bash scripts/smoke.sh` | a real coordinator with accounts switched on and **two** real apps: browsing without an account, the terms, signing up, hosting a room, signing out and back in, a wrong password refused, the friend graph and a private message between the two, then a head admin appointed across a restart who sanctions, lifts, labels, closes and announces — and the page rendered in headless Chrome as both a player and a moderator | ~60s |

**Design work needs to be looked at.** `check.sh` proves the CSS parses and
`smoke.sh` proves the page renders with a quiet console; neither can tell you
the room list is ugly or that half the window is empty. `preview.sh` is the
answer to that, and it is how the interface was redesigned on 2026-08-25.

**Some behaviour only exists over time.** The chat dock opens itself when a
message turns up in a tab nobody is reading — a change between one poll and
the next. Every one-shot check in the ladder sees it minimised whatever the
code does, which is what `chatcheck.sh` exists for: it drives the live page,
has a second account write to it, and looks again. The tone it plays is the
one thing still unasserted; a headless browser has no audio device, and the
code path that would complain about it is covered by `smoke.sh`'s rule that
the console must say nothing.

**The interface is rendered, not just parsed.** The last section of
`smoke.sh` drives headless Chrome in a throwaway profile at the running app
and asserts three things: the page drew, it drew the room that was hosted
seconds earlier — which can only happen if it fetched live state and rendered
it — and the console said nothing at all. That last one is the useful one: it
catches uncaught exceptions and it catches i18n's "missing key" warning, which
is the failure mode a translated interface actually has (D44). The developer's
own browser is never touched.

`check.sh` cannot see the seam that matters most. Accounts (T5) and the app's
sign-in screen (T11) were written weeks apart, and every unit test in both
passed the whole time they could not talk to each other. `smoke.sh` is the
test that would have noticed. It builds the app with the coordinator's address
linked in, redirects `APPDATA` so it cannot touch the developer's own session
file, and deletes everything it made. **Neither ever contacts
87.107.110.199.**

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
| T4 kick escalation 1/3/5/7 min (D39) | **done** — live block in memory, durable record in `kick_events` (D52) |
| T5 accounts (D37) | **done** — and **switched on in production 2026-08-26** (D60) |
| T6 room privacy (D41) | **done** — all four doors, plus an MMR floor |
| T7 friends (D41) | **done** — graph, blocks, private chat, invites, presence |
| T8 roles and moderation (D43, D47) | **done** — roles, sanctions, labels, banners, audit log |
| T9 interface built for translation (D44) | **done** — lookup, logical layout, and a test that enforces both |
| T10 the new lobby (D42) | **done** — renders clean under `smoke.sh` |
| T11 desktop application (D45) | **done** — shell builds and runs; bundle not yet installed anywhere |
| T12 tournaments (D48) | **done as specified** — an honest placeholder, not the feature |
| T13 terms of use | **done** — served, readable, versioned; **text needs the owner's sign-off** |
| T14 moderation panel | **done** — T8's tools, reachable from the product at last |
| T15 the door, host side | **done** — a host can finally make a private room |
| T16 password change, terms re-acceptance | **done** |
| T17 visual redesign | **done** — looked at, at 1440 and at 1366 |
| T18 a live window that updates itself | **done** — `scripts/live.sh`, one address, kept for days |
| T19 the owner's design, adopted (D58) | **done** — looked at, at 1440 and at 1366 |
| T20 the owner's answers, and the last unwired routes (D59) | **done** — ping, last seen, watching seats, invitations |
| T21 deployed to production with accounts on (D60) | **done** — build 2026.08.26-0846, verified over the real internet |

**The interface was redesigned on 2026-08-25** at the owner's request: the
old one was flat, grey and mostly empty space. What changed, beyond colour
and spacing:

- **Faces.** Nobody uploads a picture — asking would be one more thing between
  installing and finding a game — so a person is drawn from their initials on
  a colour derived from their **account id**. Same person, same colour on
  every machine; two players called Pudge still differ; renaming yourself does
  not change your face.
- **Ten seats drawn as ten marks.** Somebody scanning the lobby is asking "is
  there room for me", and a bar answers that in the time the eye takes to pass
  over it. `7/10` makes them read and subtract. The number stays underneath.
- **The host's latency as a signal meter**, three rising bars plus the number.
  Still labelled as the host's, everywhere, for the reason in D54.
- **The room screen is two teams**, Radiant and Dire, in the game's own
  colours with a count each. Ten slots in one list hid the only structural
  fact about a room: which five you would be joining.
- **A stripe down the inline edge of each room row**, coloured by whether this
  player can actually get in. It is the only part of a row that can be read
  without looking directly at it, so it carries the fact that decides
  everything else.
- The stage fills the window, so three rooms look like a lobby with three
  rooms rather than a page that failed to load.

All of it stays inside D44: logical properties only, every string through the
lookup, and the tests that enforce both still pass.

**A password can be changed, and terms can move.** Changing a password is its
own dialog off the profile card and needs the current one — a session left
open on a shared PC must not be enough to lock somebody out of their own
account. The coordinator ends every other session and reissues this one, so
the window that made the change stays signed in. And when the terms change,
everybody who agreed to the old ones now sees a strip offering to show them
the new ones and record their agreement; the coordinator has always reported
`terms_accepted` for exactly this and nothing had ever read it.

**A host can choose the door now.** T6 built public, password, friends-only
and invite-only rooms plus an MMR floor, and no host could pick any of them:
every room the app made was public. The create form and the room screen now
both carry the door, the password box appears only for the door that uses one,
and the floor is a number beside it. Inviting a friend to an invite-only room
opens its door to them as well as telling them to come — doing only the second
is how somebody gets invited and then refused.

**Moderators can moderate from the app now.** T8 built roles, sanctions,
labels, banners and an audit log, and until today the only way to use any of
it was a hand-written request from a developer's PC. The Moderation entry
appears in the toolbar for staff and for nobody else: look a player up by
name, see what they are barred from and every sanction and staff action
against them, mute/timeout/ban with a reason and a duration, lift one, label
them, close a room or hand it to somebody else in it, post announcements, and
— for the head admin alone — appoint and remove admins. Hiding the entry is a
courtesy; the coordinator refuses every call behind it without a role.

**A head admin is appointed once, at deployment**: `-head-admin <account id>`
on the coordinator (D47). Until somebody holds that role nobody can appoint
moderators, and the coordinator says so in its startup log. After that the
head admin appoints admins from the app — from a player's record, where they
have just read what that person has done, rather than from an abstract list.

One thing to know: **a role is cached for two minutes on every client**, so
somebody appointed while their window is open sees their tools within two
minutes rather than at once. That is deliberate. The alternative is every
signed-in client asking who the staff are every few seconds, which at 500
players is a constant load for an answer that changes about once a month.

**There are terms now, and they need reading.** The sign-up screen asks people
to accept terms of use, so there is something to accept: `docs/terms-en.md`,
served by the coordinator at `GET /v1/terms` and shown by a "Read the terms"
link beside the checkbox. It is **engineering's draft, not legal advice**, and
wants the owner's eyes before anybody signs up against it. Two operational
notes: the coordinator needs `-terms-file docs/terms-en.md` (without it, it
honestly says it is misconfigured rather than inventing an agreement), and
editing the text means bumping `TermsVersion` in the API package — that
constant is what makes an existing acceptance stale.

**There is a desktop window, and there is a sign-in (D45, D55).**
`desktop/` is a Tauri shell that starts the Go binary, reads the loopback
address off its first line and points a webview at it — a window rather than a
browser tab, a tray icon, and notifications that arrive while the window is
hidden. Everything the product does stays in the Go client; D55 says why.
Build it with `./scripts/build-desktop.sh`. It is **not** wired into
`publish.sh` on purpose: that installer is the only distribution channel and it
works, and replacing it with one nobody has installed would risk the working
thing for the unproven one.

**Accounts are now reachable from the app**, which is what was missing. The
gate has three shapes and picks one from `GET /healthz`: a typed name on a
server without accounts, sign in, or create an account. The session is stored
and attached to every call; the password never is. **The lobby is browsable
before any of that** — `POST /v1/sync` no longer needs a session, and without
one answers with the room list, the lobby chat and the online count and
nothing belonging to a person.

**The `-db` flip happened on 2026-08-26** (D60). It had been blocked until
T11 shipped a client that could sign in; the owner made the call the day the
first full build went up, which is the cheapest moment it will ever be — the
cost is that everybody must create an account and a forgotten password cannot
be recovered (D37), and that cost grows with every install.

**The lobby is a place now (D42).** A permanent toolbar down one side —
Lobby, Room, Tournaments, Diagnostics, and a connection light that is the
permanent answer to "am I on the room's network". The room list carries the
host, their own description of the room, the door, the player count, the MMR
floor or average, and the host's latency. Search and four filter chips run
against the list already on screen, so typing is instant rather than waiting
for the next poll. Friends rail and a tabbed collapsible chat down the other
side; announcements across the top. Tournaments and the party chat tab ship
saying they are not built — a dead link is worse than a sentence, and the
toolbar will not move when they arrive.

**Nobody has looked at it in a browser.** Every test passes, including three
new ones that fail the build if the renderer reaches for an element, class or
data attribute the markup does not have — which is the failure this kind of
interface actually has. But rendering is not tested by any of that. It wants
`./scripts/publish.sh` and one of the two test PCs.

**The lobby's latency column is real, and it is the host's (D54).** The relay
now answers a keepalive with a keepalive carrying the same sequence number, so
a host can time its own round trip to the relay; the number is smoothed,
reported on each sync, kept by the coordinator only from the player who
actually hosts that room, and served as `host_relay_ms`. Zero means not
measured yet, never excellent. **The relay must be redeployed before any of
this produces a number** — until then the column is empty, which is correct
but is not the same as working. Rooms also gained a host-written
`description`, the other D42 field the room list needed. The same change
closed a gap in T7: the client library never actually sent `in_game`, so the
friends rail's in-game light had a server that understood it and no client
that said it.

**The interface is built for translation, and a test keeps it that way.**
Every string the player reads is a key in `lobbyapp/ui/strings/en.json`,
resolved by `lobbyapp/ui/i18n.js`: `data-t="some.key"` in markup, `t("some.key")`
in the renderer. The layout uses logical properties, so setting `dir="rtl"`
flips it with no second stylesheet. `lobbyapp/ui_test.go` fails the build on
text typed into markup or into `app.js`, on a missing or unused key, on a
language whose keys do not match English, on a placeholder lost in
translation, on any hard-coded `left`/`right`, and on a strings file that did
not make it into the embedded filesystem. Only English ships (D44) — adding
Persian is a second JSON file and one line in `i18n.js`. Error text from the
coordinator and the net service is still English wherever it appears: those
sentences are written in Go and translating them is a separate job.

**Moderation exists, and the head admin is set at deployment.** Pass
`-head-admin <account-id>` to the coordinator once; there is exactly one, and
only they appoint or remove admins (D47). Every role, sanction and label is a
row with an author and a timestamp, and every action is written to an audit log
readable by admin or by subject. A ban drops every session and empties the
seat; a timeout bars joining; a mute bars chat and nothing else.

**Friends exist.** Requests, accepts, removals, blocks, durable private
messages, room invitations, and presence including a real in-game light
reported by the player's own service. A friends-only room now consults the
real graph. Blocking is silent by design — a blocked person's message is
accepted and dropped, because an error would tell them they had been blocked.

**Rooms have doors.** Public, password, friends-only, invite-only, plus an
optional MMR floor (D41). The door is checked server-side from the server's own
records — a client supplies only the password it was told to type. Staff walk
past the door; nobody walks past a kick block. Friends-only rooms refuse
everybody but the host until T7 lands the friend graph.

**Accounts are switched on in production** since 2026-08-26 (D60). The
coordinator runs with `-db /var/lib/finallobby/db/lobby.db` and
`-terms-file /etc/finallobby/terms-en.md`, so it has usernames, Argon2id
passwords, sessions, durable MMR and terms acceptance, and the session — not
the request body — decides who a request is from (D53). **There is no head
admin yet:** `-head-admin` takes an account id and none existed when the
server was deployed. Setting one is a single restart once somebody has signed
up, and until then nobody can appoint a moderator.

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

## The interface, as of 2026-08-26

The owner supplied a working HTML mock on 2026-08-26 — `Gaming Matchmaking
App Redesign/LobbyBaz.dc.html` — and the front end now follows it (D58).
That file is the reference for the look; this section is the reference for
what is actually wired to it.

- **One palette, defined once.** Every colour is a token on `:root` at the
  top of `lobbyapp/ui/app.css`. Nothing below that block names a colour, so
  the next reskin is one edit. No web font: fonts.googleapis.com is not
  reachable from Iran, and a stylesheet waiting on it is a blank screen.
- **The room list is a sortable table.** Every heading sorts, ascending and
  descending; `sortKeyOf` / `sortRooms` / `toggleSort` / `drawSortHeads` in
  `app.js`. The number columns hide themselves as the window narrows so the
  room's name never has to wrap.
- **Six filter chips and a search box**, both applied locally against the
  list already on screen, so typing is instant instead of waiting for the
  next poll. `visible()` in `app.js`, `setFilter()` for the chips.
- **Getting from a seat into a game is three numbered steps with one button
  under them** — take a seat, join the room's network, start Dota.
  `drawStepper` / `step` in `app.js`, `.stepper` in `app.css`. The button
  always says the next thing to do rather than everything that could be done.
- **The host's controls are a dialog** (`#roomsetgate`), opened by "Room
  settings" on the room screen: the door, the MMR floor, the description,
  admission and the game mode. Everyone else's room screen is seats.
- **Inviting is its own dialog** (`#invitegate`) and does both halves of the
  word: tells the friend, and lets them through the door.
- **Creating a room is a dialog** (`#creategate`) where the door is chosen
  before the room exists (D41). A password is a tick-box on an otherwise
  open door rather than a fourth kind of door.
- **Diagnostics is not a toolbar entry.** It is one button on the settings
  screen, under the three network facts it explains. `renderSettings` and
  `renderDiag` in `app.js`, `#screen-settings` in `index.html`.
- **The chat is a dock along the bottom** (D56), now **open when the app
  starts** (D58) and minimising to its tab strip when asked. It reopens
  itself and plays a tone when a message arrives. Every conversation is a
  tab, private ones included. Each line carries a time in the reader's own
  zone. `renderChatDock` is the engine, `.chatdock` the appearance.
- **The friends list owns the entire right rail**, grouped by where people
  are: in a room, online, offline. A friend row is itself the button that
  opens a conversation.
- **Clicking an empty seat moves you into it** (D57). `Room.Move` in the
  coordinator, `POST /v1/rooms/{id}/slot`, `POST /api/rooms/slot` in the app.
- **Every seat carries that player's own ping** (D59), reported on their
  heartbeat and dropped rather than shown when it goes stale. It is theirs,
  not the reader's: everybody in a room reaches everybody else through the
  relay, so a poor number on one seat is a poor game for that person alone.
- **Five watching seats sit below the two teams** (D59) and are taken by
  clicking, exactly like a playing seat. The admins' three seats are a
  separate range and are not drawn.
- **The friends rail answers where somebody is**: in a room (named, when the
  room is one this player can see), online, in game, or offline with when
  they were last here. The last of those survives a restart —
  `accounts.last_seen_at`, written on a timer rather than on the heartbeat.
- **Room invitations are the first thing in the rail** (D59), above the
  friends themselves.
- **The search box and the filter chips exist on the lobby and nowhere else**
  (D59). They act on the room list, and on the other screens they were
  controls that could not do anything.
- **The window gives ground from the sides inward.** Breakpoints at 1440px
  (header) and 1180px (room columns). Checked at 1366x768, the commonest
  laptop this will run on.

Nothing in the mock that the server has no notion of was faked. The owner has
since settled which of those inventions they wanted (D59): per-player ping,
last-seen times and the friends' online / in-a-room / in-game statuses are
built; regions, Steam links, a games-hosted count, a game mode advertised on
a room and a Watch button on a running match are not, and will not be.

`bash scripts/preview.sh <name>` photographs all of it; `SHOTS` may be set to
a JSON array of `[name, script]` pairs to photograph something else, and
`WIDE`/`TALL` change the window size.

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

**Answered 2026-08-26** (D59), all by the owner reviewing the adopted design:
a room does **not** advertise a game mode — the host switches it in Room
settings and the app hands it to Dota on launch; there is **no** Watch button,
permanently — a room is joinable or full, and in game or open, and there is no
fifth thing to offer; regions and Steam links are **not** wanted; per-player
ping, last-seen times and the online / in-a-room / in-game statuses **are**,
and are built; and the five watching seats are **seats in the room** below the
two teams rather than a spectator feature of their own.

**Answered 2026-08-24:** who the admins are (D47 — a role the head admin
grants), whether Tournaments is real (D48 — yes), and when the dedicated
server is bought (D49 — when the product is ready to deploy).
