# LobbyBaz — the front end

> **For an agent picking this up.** Read `CLAUDE.md` first (the hard rules),
> then `docs/STATE.md` (what is actually built), then this.
> `docs/decisions.md` carries the reasoning — when this file says "because
> D42", the argument is there. `docs/backend.md` is the other half.
>
> **You cannot judge this by reading it.** Run `bash scripts/live.sh`, open
> the address once, and leave the window open — it reloads itself as you
> edit. Every design mistake in this project's history was found by looking
> at the running page and none by reading the markup.

## What it is

One HTML page, one stylesheet, one script, served from loopback by the
desktop app. No framework, no build step, no bundler, no npm. Editing
`app.css` and reloading is the whole development loop.

```
lobbyapp/
  main.go            binds 127.0.0.1 on a random port, mints a token, opens a browser
  server.go          the /api/* routes the page talks to; embeds ui/ into the binary
  ui/
    index.html       every screen, all of it inert markup with data-t keys
    app.css          the whole appearance (~1000 lines, tokens at the top)
    app.js           the whole behaviour (~2000 lines)
    i18n.js          key lookup and writing direction
    strings/en.json  every word a player reads
  ui_test.go         sixteen Go tests that enforce the rules below
```

**Why a local web page rather than a native window.** It is the fastest thing
to change, it renders the same on every Windows version, and — this is the
part that matters — it is *not* the mistake the predecessor made. That was a
**privileged** agent listening on localhost, so any web page a player visited
could drive it as Administrator. This process has exactly the rights the
player already has, binds to `127.0.0.1` on a random port, and requires a
random token that only the page it opened knows. `server.go`'s guard also
refuses any request with a cross-origin `Origin` header. A hostile page gains
nothing it could not get by running a program as the user.

## The one architectural rule

**The page is a renderer.** It polls one endpoint every two seconds, draws
what came back, and posts actions. It holds no state of its own beyond:

- which screen is showing
- which filter and sort are set
- what the player is halfway through typing
- which chat tab is open

Everything else lives in `state`, which is whatever `GET /api/state` last
said. A refresh loses nothing, and there is no second copy of the truth to
drift out of step with the server.

The practical consequence: **when a value is wrong on screen, the bug is
almost never in the renderer.** Check what `/api/state` actually returned
first.

## The screens

```
┌────┬──────────────────────────────────────────┬──────────┐
│    │  header: search + filters + you          │          │
│ t  ├──────────────────────────────────────────┤  rail    │
│ o  │  banners (staff announcements, updates)  │          │
│ o  ├──────────────────────────────────────────┤ friends  │
│ l  │                                          │ grouped  │
│ b  │  stage: one of five screens              │ by where │
│ a  │    lobby / room / events                 │ they are │
│ r  │    settings / moderation                 │          │
│    │                                          │          │
│    ├──────────────────────────────────────────┤          │
│    │  chat dock (tabs + log + box)            │          │
└────┴──────────────────────────────────────────┴──────────┘
```

The shell is one CSS grid in `app.css` — `grid-template-areas` naming `bar`,
`head`, `strip`, `stage`, `chat`, `rail`. The toolbar and the rail are
permanent; only `stage` changes.

| Screen | Element | Drawn by |
|---|---|---|
| Lobby | `#screen-lobby` | `renderRooms` → `roomCard` |
| Room | `#screen-room` | `renderRoom` → `teamColumn` / `watchColumn` / `slotCard` |
| Events | `#screen-tournaments` | static — an honest "not yet" (D48) |
| Settings | `#screen-settings` | `renderSettings` + `renderDiag` |
| Moderation | `#screen-mod` | `renderMod` — hidden unless the session holds a role |

Dialogs are separate: create a room, room settings, invite, profile,
password, the front door and the terms. All of them plain `.gate` overlays
toggled by a class. Two carry `.gate.blur`, which blurs the application behind
them rather than merely darkening it: the sign-in card (`#namegate`) and the
terms (`#termsgate`). Those two stand in front of the whole product rather
than in front of one screen, and D61 says why they are allowed to look it.

**The front door** (`#namegate`) is one card with three shapes, switched in
place by `gateMode`: a typed name when the server has no accounts, signing in,
and creating one. Switching tabs must not replay the entrance animation, so
nothing is remounted — classes are toggled and that is all. It carries two
live numbers, drawn by `drawGateStatus` at open and again on every poll while
it is open.

**The terms** (`#termsgate`) are markdown from the coordinator, rendered by
`drawTerms` into real elements — never `innerHTML`; that text is a file
somebody edits and editing it must not reach the page. `termsRead` turns the
scroll position into a percentage and a verdict, and Accept is inert below
98%. `openTerms(purpose)` takes one of three: `signup` ticks the checkbox on
the way out, `accept` records the acceptance against the account, `read`
shows no Accept button at all.

## The rules the tests enforce

`lobbyapp/ui_test.go` holds sixteen tests. They are not style checks — each
one is a failure this kind of interface actually has, turned into a build
error. They run inside `scripts/check.sh`.

### No user-facing text outside the catalogue (D44)

Every word a player reads has a key in `strings/en.json`.

- In markup: `data-t="room.leave"` fills the element's text.
  `data-t-placeholder`, `data-t-title`, `data-t-aria-label` fill those.
- In script: `t("room.leave")`, never a literal.

Enforced from four directions: every key used must exist; every key defined
must be used; no sentence may be typed into the markup; and nothing a player
reads may be assigned a literal in JS. That last one catches
`e.textContent = "Loading"` — the most direct way to bypass the catalogue and
the easiest to do by accident.

**Why so strict, for a product with one language.** The owner chose English
first and Persian later. Persian is right-to-left, and retrofitting direction
into a finished layout is the expensive path because it touches every screen.
Doing it from the start costs nothing today. Adding Persian is then one file
plus one line in `LANGUAGES` — a week instead of a month.

### No hard-coded left or right (D44)

**Logical properties only**, in CSS and in JS:

| Never | Always |
|---|---|
| `margin-left` | `margin-inline-start` |
| `padding-right` | `padding-inline-end` |
| `text-align: left` | `text-align: start` |
| `border-bottom` | `border-block-end` |
| `top` / `left` | `inset-block-start` / `inset-inline-start` |

The layout then flips on its own when `dir` changes, with no second
stylesheet. `TestTheLayoutHasNoHardCodedDirection` fails the build otherwise.

### The renderer may only reach for things that exist

Three tests parse `app.js` for `$("some-id")`, `querySelector(".some-class")`
and `dataset.someAttr`, and check each against `index.html`. This is the
failure this kind of interface actually has — a renamed element that nothing
notices until a player opens the screen — and it is why they exist.

The practical effect: **if you rename an id in the markup, the Go test suite
tells you.** If you add `$("newthing")` to the script, add the element first.

## The stylesheet

`app.css`, a thousand-odd lines, and the shape matters more than the length:

**Every colour is a token on `:root`, defined once.** Nothing below that
block names a colour. The palette is the "Nocturne" one the owner drew
(D58) — dark slate grounds, a muted violet accent, green for Radiant and red
for Dire. The next reskin is one block, not nine hundred lines.

**No web font.** The mock named one; `fonts.googleapis.com` is not reachable
from inside Iran, and a stylesheet waiting on it is a blank screen for as
long as the DPI takes to give up. A system stack renders instantly.

**The window gives ground from the sides inward.** Three breakpoints, in the
order the space is given up: at **1440px** the header tightens so the last
filter chip is not sliced in half; at **1180px** the room list drops its
number columns (`.rcol-hide`); at **1100px** the friends rail goes entirely
and the shell becomes two columns. **The room's name is the last thing
allowed to wrap**, because it is the one thing the row exists to show. Check
any layout change at **1366×768** — the commonest laptop this will run on.

## The script

`app.js`, read top to bottom, is in this order:

| Section | Functions |
|---|---|
| plumbing | `api`, `act`, `banner`, `needName`, `el`, `esc` |
| portraits | `avatar`, `initials`, `hueOf` |
| render | `render` — the one function the poll calls |
| room list | `visible`, `sortRooms`, `toggleSort`, `renderRooms`, `roomCard`, the cells |
| room | `renderRoom`, `drawStepper`, `drawDoor`, `teamColumn`, `watchColumn`, `slotCard` |
| friends rail | `renderFriends`, `invitationRows`, `whereabouts`, `ago`, `friendRow` |
| chat dock | `renderChatDock`, `drawTabs`, `drawLog`, `openDock`, `notice`, `ping` |
| settings | `renderSettings`, `renderDiag` |
| the door | `gateMode`, `drawGateStatus` |
| the terms | `openTerms`, `termsRead`, `drawTerms`, `inline` |
| moderation | `renderMod`, `lookUp`, `renderRecord`, `drawSanctions`, `drawByThem` |
| screens | `show` |
| events | every handler, wired once at load |
| poll | `refresh`, the two-second interval |

`render(s)` is the only entry point from the poll. It calls each section's
renderer in turn. Adding a screen means: markup in `index.html`, a
`renderX(s)` function, a call from `render`, and strings in `en.json`.

**`act(fn)`** wraps every action: it disables the interface, awaits, refreshes,
re-enables, and shows a banner on failure. Use it for anything that changes
server state — never call `api()` bare from a click handler.

## Pieces worth understanding before changing them

### The chat dock (D56, D58)

Modelled on Dota 2's own, which means a specific list of behaviours, not a
vague resemblance: docked along the bottom, always present, **open when the
app starts**, minimising to its tab strip when asked, opening itself and
playing a tone when a message arrives, and **every conversation a tab** —
private ones included, so starting one does not cover the lobby with a
dialog.

Each line is a time gutter, a name, and the words. The clock is the reader's
own zone and to the minute: a chat log is read for order and recency, and the
second something was said has never been the question.

Two traps, both already fallen into:

- **The unread ring must only fire when a count grows.** `notice()` compares
  signatures for logs; `noticeDM()` is separate and only flags on growth.
  Reading a conversation takes its count to zero, which is a change — the
  first version rang on the way down.
- **Behaviour that only exists over time cannot be tested one-shot.** The
  dock opens on a *change between two polls*. `scripts/chatcheck.sh` drives
  the live page over the Chrome DevTools Protocol to prove it. The tone
  itself stays unasserted — headless has no audio device — and the guard is
  `smoke.sh`'s rule that the console must say nothing.

### The room screen

Three numbered steps with one button under them: take a seat → join the
room's network → start Dota. It replaced a row of buttons that were sometimes
disabled, and the commonest failure in the two-PC test was two players in a
room, neither on its network, with nothing on screen saying which of the
three things had not happened. **The button always says the next thing to do**,
never everything that could be done.

- **Clicking an empty seat moves you into it** (D57). Which slot you sit in
  *is* which team you are on — 1–5 Radiant, 6–10 Dire — so the old team
  dropdown was a second place for the same fact to live, free to disagree
  with the seat.
- **`canTakeSeat` mirrors the coordinator's refusals exactly.** A card that
  invites a click and then shows an error is worse than one that does not
  invite it.
- **The host picks a side like anybody else** (D64). They used to be refused
  every seat on the screen, which made the person who opened a room to play
  Dire the only person who could not sit there. The one seat still refused is
  a watching seat — the match runs on their machine — and the coordinator
  refuses that too, because relaxing a client guard over an unguarded server
  path is how this project's recurring bug happens in reverse.
- **Five watching seats sit below the two teams** (D59) and are taken the
  same way. The admins' three seats are a separate range and are not drawn —
  `Member.Seat` distinguishes them, and dropping that field would put a
  moderator in an occupied watcher's chair.
- **Room settings is a dialog**, `.hostonly`, opened from the top right. The
  door, the MMR floor, the description, admission and the game mode. The
  coordinator refuses every one of them from anybody else, so hiding them is
  a courtesy that keeps the screen honest, not a defence.

### The room list

Every heading sorts, both directions. A room list is read for one thing at a
time — who has space, who is closest, who is at my level — and which one it
is changes between one glance and the next.

Search and the six filter chips run **locally**, against the list already on
screen, so typing is instant instead of waiting for the next poll. They exist
on the lobby and nowhere else: `show()` hides them elsewhere, because on the
other screens they were controls that could not do anything.

### The friends rail

Grouped by where people are — in a room, online, offline — because that is
the only question it answers: who can I play with right now. A flat list
sorted by presence made the reader work it out every time.

**Room invitations are the first thing in it**, above the friends themselves:
an invitation is time-limited in a way a friend is not, and the room it names
is filling up while it is ignored.

**The row itself is the button** that opens a conversation. A "Message"
button beside every name cost the rail most of its width.

### Portraits

Nobody uploads a picture — asking would be one more thing between installing
and finding a game. A person is drawn from their initials on a colour derived
from their **account id**, so the same person is the same colour on every
machine, two players called Pudge still differ, and renaming yourself does not
change your face.

### Safety in the renderer

Everything a player or a staff member typed is put on screen with
`textContent`, never `innerHTML`. `renderAds` is the one that matters most —
staff type an announcement and every other client displays it, which is the
exact shape of a stored scripting hole — and links are followed only when the
server said the scheme was http or https.

## The window it runs in

`desktop/` is a **Tauri (Rust) shell** and deliberately a thin one (D45, D55).
It starts the Go binary, reads the loopback address and token from its first
line of output, and points a webview at it. Everything the product does lives
in Go.

That split was a real decision. The obvious alternative was rewriting the
client in Rust, which would have put the one proven part of the system — the
Go client that has actually carried a Dota match between two PCs — back to
zero, and split one set of bugs into two. What a browser page genuinely cannot
do is exactly what the shell provides: a window that is not a browser tab, a
tray icon, and notifications that arrive while the window is hidden.

Consequences worth knowing:

- **`-url-only` prints the address on the first line and nothing before it.**
  The shell parses that line. Anything logged ahead of it breaks the window —
  which has happened, when a dev-mode notice was printed too early. Log to
  stderr, after the URL.
- **`scripts/check.sh` runs `cargo check`, not a full build.** A full build
  links a webview and takes minutes; what breaks in practice is the Rust. If
  `cargo` is absent it says so and carries on — the Rust toolchain is not
  required to work on this project, and most tasks never touch it.
- `desktop/dist/index.html` is a placeholder. The real interface is served
  over loopback by the Go binary; nothing in `dist/` is what a player sees.

## The room screen, after D68

Worth knowing before touching it, because three of its pieces are not where
an obvious reading would put them:

- **The five facts in the band are the diagnostic.** They replaced a
  three-step panel, and the network cell in particular carries the job that
  panel existed for: saying which of "seated / on the network / in the game"
  this PC has actually done. It is also the control - the join and leave links
  live in it. Do not reduce it to a decoration.
- **`/api/playnow` chains connect and launch on the server, on purpose.** The
  tunnel says "connecting" before the handshake finishes; doing this in the
  page would race. If you need a variant, add it in Go beside `waitForTunnel`,
  not in `app.js`.
- **The watching seats belong to a board.** Two on Radiant (O1, O2), two on
  Dire (O3, O4), numbered in their own range. `OBS_PER_SIDE` and
  `ipam.ObserverSlots` must agree; the coordinator is the one that enforces
  it.

## What a room says about its host

Two of a room's statuses are not stored anywhere - the coordinator derives
them from what the host's own machine is doing (D69) - and the page reads
them like any other:

- **`locked_in_game`** now also means "the host is in a match", with
  `host_in_game` saying which of the two it is. Seats stop being clickable
  (`canTakeSeat` already refused a locked room) and `drawNetBanner` says so in
  words, because a screen that has silently stopped responding to clicks is
  indistinguishable from a broken one.
- There was a second, **`host_away`**, for a room counting down because its
  host had stopped answering. It is gone with the grace period (D84): a host
  the coordinator cannot see closes the room, so the room a reader would have
  seen labelled that way is `closed`. Do not reintroduce the label without
  reintroducing the state.

The thing not to reintroduce: the room's one button must **not** be disabled
because the room is locked. The nine people already seated in it are exactly
the people who now need to press Join Game. Locking decides who may come in.

## Notifications live in the shell, not the page

`desktop/src/main.rs` polls `/api/state` every five seconds on its own thread
and raises five desktop notifications from it (D45, D66): your room filling
up, the host starting the match, a joinable room opening, a friend coming
online, and the tunnel dropping under you.

**None of this is in `app.js`, and it must not move there.** The page stops
running when the window is closed to the tray, which is the only time a
notification is worth raising.

The page's whole part in it is the Notifications card in Settings: five
checkboxes named for the fields of `session.Notify`, saved as a complete set
to `POST /api/notifications`, read back from `state.notify`. The switches are
read by the tray on every poll, so turning one off stops it immediately rather
than at the next restart.

## The API the page talks to

`lobbyapp/server.go` serves `/api/*` on loopback. It is not a proxy to the
coordinator — it is a narrower interface over it, holding the session, the
config file and the named pipe to the service, so the page never handles a
credential or knows a server address.

`GET /api/state` is the poll and returns everything: profile, room list, the
room you are in, both chat channels, friends, banners, the service's view of
the tunnel and the adapter, update status, diagnostics.

The rest are actions, grouped: `rooms/*` (create, join, leave, slot,
spectate, status, kick, privacy, describe, invite), `auth/*`, `friends/*`,
`admin/*`, and `connect` / `disconnect` / `play` / `diagnose` / `update`.

**Keep the page's calls and the app's routes in step.** An audit found four
routes with nothing calling them and one call to a route that did not exist
(D59). It is worth re-running after any batch of interface work:

```bash
python - <<'PY'
import io,re
js=io.open("lobbyapp/ui/app.js",encoding="utf-8").read()
go=io.open("lobbyapp/server.go",encoding="utf-8").read()
calls=set(re.findall(r'api\("(/api/[^"?]+)', js))
routes=set(re.findall(r'mux\.HandleFunc\("(?:GET|POST) (/api/[^"]+)"', go))
print("no route:", sorted(calls-routes))
print("never called:", sorted(routes-calls))
PY
```

`/api/rooms/invite` is uncalled on purpose — it is the raw door grant, and
`/api/friends/invite` is the compound gesture the interface wants: tell them
to come *and* let them through. Doing only the first is how somebody is
invited and then refused (D41).

## Looking at it

| Command | What it gives you |
|---|---|
| `bash scripts/live.sh` | **The one to use.** A whole LobbyBaz on a fixed address, in its own window, that reloads itself within two seconds of any edit to the page, the stylesheet, the scripts or the strings. Go changes rebuild and restart at the same address. Leave it open for days. |
| `bash scripts/try.sh` | The same sandbox, opened once, deleted on Ctrl-C. |
| `bash scripts/preview.sh <name>` | The same sandbox photographed. One PNG per screen into `scripts/shots/<name>/`. |
| `bash scripts/uicheck.sh` | Drives the live page over CDP through repeated polls to prove the renderer does not duplicate, rebuild or lose things. **The rung that catches the bugs the owner reports.** |
| `bash scripts/chatcheck.sh` | Drives the live page over CDP to prove the dock opens on an incoming message. |
| `bash scripts/termscheck.sh` | Drives the live page over CDP to prove the terms gate opens, refuses, and then allows. |
| `bash scripts/verify.sh` | All of the gradeable ones above, plus `check.sh` and `smoke.sh`, in one command with one verdict. |

All of them are loopback-only, on a throwaway database, with `APPDATA`
redirected so your own signed-in session is untouched. None contacts the live
server.

`preview.sh` takes `SHOTS` (a JSON array of `[name, script]` pairs, the
script run in the page before the shutter) and `WIDE`/`TALL`:

```bash
SHOTS='[["room","show(\"room\")"],["create","$(\"btn-create\").click()"]]' \
  bash scripts/preview.sh mine
WIDE=1366 TALL=768 bash scripts/preview.sh small
```

## Changing something — the loop

1. `bash scripts/live.sh`, open the address, leave it open.
2. Edit. The window reloads itself.
3. **Look at it.** At 1440 and at 1366.
4. **Watch it change.** Leave it open for a minute and look again. Rows that
   multiply, cards that flicker, a field that drops what you were typing —
   none of those exist in the first render and all of them exist in the
   second. This is the step that gets skipped, and skipping it is how every
   interface bug the owner has reported reached them.
5. `bash scripts/verify.sh` — everything a machine can grade, one verdict.
   `bash scripts/verify.sh fast` mid-change if the full run is too slow; the
   sixteen interface tests are in that one.
6. `scripts/preview.sh` and look at the pictures.
7. `STATE.md`, a decision entry if the reasoning is worth keeping, one commit
   naming the task, `./scripts/git-sync.sh push`.
8. `./scripts/ship.sh`, so the owner can look at it (D62).

## Where to be careful

1. **Never type a word a player reads into the markup or the script.** Key it.
   That includes words arriving from Go. The Windows service reports a
   teardown as "lease expired locally", which is accurate, untranslated, and
   no use to anybody; `tunnelErrorKey` in `server.go` maps the reasons it
   knows to keys, and `keysUsed` in `ui_test.go` reads `server.go` so a key
   named there is checked like any other (D77).
2. **Never write `left`, `right`, `top` or `bottom`.** Logical properties.
3. **Never use `innerHTML`** for anything a person typed.
4. **Never name a colour below the token block.**
5. **Never link an external font, script or stylesheet.** Nothing outside the
   binary is reachable from Iran.
6. **Never keep a second copy of server state in the page.**
7. **Never call `api()` bare from a handler** — wrap it in `act()`.
8. **Never judge a change by reading it.** Photograph it, or open it.
9. **Never put motion on a working screen.** The sign-in card and the terms
   are the two exceptions, they are exceptions for a reason that does not
   generalise (D61), and every loop on them is dropped under
   `prefers-reduced-motion`.
10. **Never draw a control for something that does not exist.** A toggle that
    does nothing teaches somebody the product lies, and they will not find out
    which half was true.
11. **Never write a settings field from a poll while somebody is typing in
    it.** State arrives every couple of seconds; a field rewritten mid-word is
    a field that cannot be edited. `#set-opts` is the pattern: write it only
    when `document.activeElement` is something else.
12. **Never rebuild a panel that has not changed.** `redraw(node, sig)` is
    the guard and every list on the screen uses it; a container emptied and
    refilled twice a second loses its scroll position, its hover and the
    click that was landing on it (D71). Its companion rule is the more
    dangerous one: **the signature must name every input the panel draws
    from**, including the ones that are not in its argument. A signature that
    misses one is a panel that silently stops updating.

    The same rule one level down, for the rows inside a list: reconcile by
    key, replace only the rows whose signature moved, and never append without
    first taking the old row out of the map you are reconciling against —
    forgetting that is what put the owner's room in the lobby five times
    (D75). A number that moves on its own is painted into the node that is
    already there and kept out of the signature entirely, which is what
    `LIVE_KEYS` is for (D73). `bash scripts/uicheck.sh` is what proves all of
    this, and it is the only rung that can.
13. **Never write a media query above the rule it narrows**, and never write
    the same declaration twice. Same specificity means the later one wins, so
    the first is either dead or is silently deciding something four hundred
    lines away (D72).
14. **Never add a `transform` without asking what it does in Persian.** It is
    the one exception to the logical-properties rule, because there is
    nothing logical to write instead: give it a `[dir="rtl"]` companion.
15. **Never put a number that moves by itself into a render signature.**
    A relay ping arrives fresh on every poll; a signature carrying one is a
    guard that is never true, which is worse than no guard because it looks
    like one. Drop it with `steady` and paint it into a leaf of its own
    (D73).
16. **Never let a control decide something the room needs to know.** The game
    mode was a dropdown in the host's own Room settings, read at the instant
    they clicked the button, stored nowhere and shown to nobody - so the nine
    people who joined to play it had no way of finding out, and a reconnect
    forgot it. If two people need to agree on something, it is the room's and
    it goes through the coordinator; the control on screen only proposes it
    (D80). The test for it: could somebody else in this room be surprised by
    what this widget decided?
17. **Never let a line that may be cut carry a fact somebody chooses on.**
    `.room-meta .rest` is deliberately allowed to run out of space and be
    ellipsised — the room name is not. "You are here" and, briefly, the game
    mode lived at the end of it. Anything a player scans the lobby *for* goes
    in its own `flex: none` element beside the status badge, or into the row's
    own colour (D81).
18. **Never let the interface be the only place a rule lives, and never let
    it enforce half of one.** The Join button had said "you are already in a
    room" since D68 while the coordinator allowed it, and the Create button
    beside it said nothing at all - so the rule was enforced in one place, on
    one of its two doors, by the half of the system a player can bypass
    (D82). The server enforces; the interface stops offering what would be
    refused, on **every** door.
19. **Focus is the one thing in this stylesheet drawn with `outline`, and it
    has to stay that way.** A room row, a takeable seat and a friend all show
    the keyboard with `outline: 2px solid ...; outline-offset: -2px`, which
    draws inside the element exactly where the old inset `box-shadow` did. It
    moved there because a seat now carries its own ring in `box-shadow` at the
    same specificity, so the two took turns winning by source order and a seat
    you had tabbed to showed nothing at all (D86). Anything new that wants a
    ring uses `box-shadow`; nothing new uses `outline`.
20. **The lobby and the room are not one design any more, and that is
    deliberate.** The owner took the room boards back to the Nocturne mock on
    2026-09-03, looked at the lobby redrawn beside them, and kept the lobby as
    it was (D86). So the lobby keeps its travelling light, its halo, the pulse
    on Create room and its brighter status greens, and the room has none of
    them. Do not "make them consistent" in either direction without asking.
21. **Never let a dialog grow taller than the window.** `.gate` centres its
    card and does not scroll, so a card with no ceiling hangs off the top
    *and* the bottom with nothing to scroll to reach it - and the buttons are
    at the bottom (D87). `.gate-card` is capped at the window height with a
    fixed head and foot and a scrolling `.gate-body`; anything new that goes
    in a dialog goes inside that body. The numbers that make this real rather
    than theoretical: the app's own window minimum is 640px tall, and Windows
    display scaling divides the CSS viewport again.
22. **Photograph it small.** `preview.sh` has always taken `WIDE` and `TALL`
    and every shot anybody took was 1440x820, which is why the dialog above
    was broken for weeks in plain sight. A layout change is not looked at
    until it has been looked at short: `WIDE=1100 TALL=560 bash
    scripts/preview.sh <name>`.
23. **A fact people scan for gets its own element; only prose goes on the
    run-on line.** The room meta line is allowed to run out of room and be
    cut (D81). *Password* and *Invite only* were words on the end of it, so
    the first thing a narrow window hid was the reason a click would be
    refused (D88). Marks now, beside the badge, outside the ellipsis. And
    when a mark is the answer, **draw it in the stylesheet**: a glyph comes
    from whichever font the machine has, and on Windows the obvious ones
    arrive as colour emoji in a product with no other colour in it.
24. **Hidden is not removed.** Two things the owner asked to hide - MMR on
    the lobby, your own address in a room - are still built, still filled,
    and hidden by one rule each (D88). A `uicheck` check asserts they still
    exist *and* are invisible, so tidying them away goes red. When you hide a
    cell in a list of cells, take its separator with it: `:has(+ .the-cell)`.
25. **A named grid area that disappears strands whatever was assigned to
    it.** Rule 13's cost, made concrete: the narrow media query dropped the
    shell to `"bar head" "bar stage"...` with no `rail` area, and a `#rail`
    whose `display: none` had been overridden by a later rule therefore had a
    `grid-area` naming nothing, fell out to auto-placement, and landed in an
    implicit track in the corner of the window (D89). The shell now keeps all
    three named columns at every width and varies one token, `--rail-w`. If
    you must hide a column, give it a width of nought - never take its area
    away.
26. **A class the renderer emits must have a rule, and there must be exactly
    one place that rule could be.** `statusClass` emitted `badge locked` and
    `badge replace` for months and neither existed; `.badge.game`,
    `.badge.shut` and a whole parallel `.state` block existed for names
    nothing emits (D89). Two copies of one idea is how a class ends up styled
    in the copy nobody uses. When you add a status, grep for the class name in
    both files before you write either half.
27. **Measure the overflow, not the box.** A badge with a fixed height whose
    label wraps does not get taller - the text climbs out of it. A check that
    measured the height was green against every broken version of the
    stylesheet (D89). `scrollHeight - clientHeight` is the measurement.
28. **Never leave a change on this PC.** `./scripts/ship.sh` after the commit
    (D62). The owner tests on the live product.
