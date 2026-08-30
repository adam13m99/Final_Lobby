# Handoff: LobbyBaz — Settings, Sign in, Create account, Terms redesign

## Overview
LobbyBaz is a desktop client that puts players into the same Dota 2 match when Valve matchmaking is unreachable: one player's PC hosts, a relay carries traffic between the players in a room. Four screens were redesigned so they match the existing Lobby and Room screens and read better:

1. **Settings** — full app chrome, restructured, plus three new sections proposed (Game, Audio & voice, Notifications).
2. **Sign in** — modal card over the blurred app, deliberately "flashy" for a gamer audience.
3. **Create account** — same card, second tab, 2x2 field grid.
4. **Terms of use** — modal with a read-progress bar and Accept gated on scrolling to the end.

Copy was rewritten (the product owner approved a free rewrite). It is plain-language and matter-of-fact; keep it verbatim unless product asks otherwise.

## About the Design Files
The files in this bundle are **design references created in HTML** — a prototype of the intended look and behavior, not production code to copy. The task is to **recreate these designs in the app's existing environment** (React/Vue/Electron renderer/whatever LobbyBaz already uses) with its established components, styling approach and routing. If no such environment exists yet, pick the most appropriate framework for the project and implement there.

The prototype is a single streaming component file that renders all four screens with a switcher strip at the top; that strip is scaffolding for review and must NOT ship.

## Fidelity
**High-fidelity.** Final colors, typography, spacing, radii, motion and copy. Recreate pixel-closely using the codebase's own primitives. Every value comes from the Nocturne design-system token sheet (see Design Tokens); prefer wiring to the equivalent tokens already in the app over hard-coding hexes.

---

## Shared app chrome
Present on Settings; sits blurred behind the auth and terms modals.

Root: `height: 100vh; overflow: hidden; display: flex;` background `--color-bg` (#161826), text `--color-text` (#e9e9ed), font Inter, base size 13px. In the prototype the root also carries `padding-top: 38px` for the review switcher — drop that in production.

**Left rail** — 60px fixed, `border-right: 1px solid rgba(233,233,237,0.07)`, background `color-mix(in srgb, var(--color-bg) 82%, #000)`, column flex, 10px top / 12px bottom padding.
- Brand tile: 34x34, radius 9px, text "LB" 12px/700, color `--color-accent-200` (#e7e5fe), background accent at 16% alpha, border accent at 38%. 12px bottom margin.
- Nav items (Lobby, Room, Events): 48px wide, 7px vertical padding, radius 9px, 17px Phosphor icon above a 10px label, gap 3px; idle color = text at 52% alpha; hover `background: rgba(233,233,237,0.05)`, color full text. Item gap 10px.
- Spacer, then the active **Settings** item: same shape, color `--color-accent-200`, background accent 14%, border accent 30%.
- Tunnel status: 6px dot #4ec98a with a 2.6s blink, plus a two-line 9px caption "tunnel / connected" in text at 45%.

**Top bar** — 46px, `border-bottom: 1px solid rgba(233,233,237,0.07)`, background `color-mix(in srgb, var(--color-bg) 92%, #000)`, 16px side padding, right-aligned cluster with 10px gap:
- "service running" pill: 11px, 3px/9px padding, radius 999px, color #7fdcab, background rgba(78,201,138,0.10), border rgba(78,201,138,0.28).
- "1 online" pill: same metrics, color text 62%, border rgba(233,233,237,0.10).
- User chip: 26px avatar (radius 7px, #4ec98a ground, #0f1a14 initials 10px/600) + two lines: name 12px/600, "no MMR set" 10px in text 48%. Hover `rgba(233,233,237,0.05)`, radius 999px.

**Right panel (Friends)** — 232px, `border-left: 1px solid rgba(233,233,237,0.07)`, same darkened ground as the rail, 12px/14px padding. Title "Friends" 14px/600 with a 24px "+" square button (radius `--radius-sm`, border rgba(233,233,237,0.10); hover border accent 40%, color accent-200). Below: `.input` "Their username" + `.btn.btn-primary` "Find", 6px gap. Empty state paragraph 11.5px, line-height 1.5, text 45%: "No friends yet. Add somebody by their username and they show up here when they are online."

**Chat dock** — bottom of the centre column, 16px side margin, 14px bottom, radius `--radius-lg`, border rgba(233,233,237,0.07), background `color-mix(in srgb, var(--color-bg) 88%, #000)`.
- Tab row (8px/10px padding, bottom border rgba(233,233,237,0.06)): active "Lobby" pill = 11.5px, 4px/10px, radius 999px, color accent-200 on accent 14%; "Room"/"Party" idle text 52%, hover rgba(233,233,237,0.05). Right side: "1 in the lobby" 11px, text 42%.
- Message line: 12px; timestamp in text 35%, body in text 55%.
- Composer: `.input` placeholder "Message #lobby" + `.btn.btn-primary` "Send", 8px gap.

---

## Screen 1 — Settings
**Purpose**: identity, network health, game/audio/notification preferences, version + terms.

Scroll area padding `26px 30px 22px`; content `max-width: 1080px`.

**Page header** — row, bottom padding 14px, `border-bottom: 1px solid rgba(233,233,237,0.07)`.
- H1 "Settings": 26px, weight 500, letter-spacing -0.02em.
- Sub: 12.5px, text 58%, max-width 620px — "Who the server thinks you are, what the network is doing, and the checks that explain it when nothing is happening."
- Right, baseline-aligned: "build 2026.08.26-0846" 10px, uppercase, letter-spacing 0.12em, text 38%.

**Card grid** — `display: grid; grid-template-columns: 1.35fr 1fr; gap: 14px; margin-top: 18px; align-items: start`.

1. **Identity banner** (full width) — radius `--radius-lg`, padding 18px/20px, `background: linear-gradient(100deg, color-mix(in srgb, var(--color-accent) 12%, var(--color-surface)), var(--color-surface) 58%)`, border accent 22%, and a 2px left edge `linear-gradient(to bottom, transparent, var(--color-accent), transparent)`. Contents: 54px avatar (radius 12px, #4ec98a, initials 17px/600 #0f1a14, `box-shadow: 0 0 0 4px rgba(78,201,138,0.12)`); name 17px/600 with a "host capable" tag (10px, 2px/7px, radius 999px, accent-200 text, accent 34% border); meta line 12px text 55% — "signed in as arman13m99 · no MMR set · changeable once a week"; right cluster of three `.btn.btn-secondary` at 12px — "Edit profile", "Change password", "Sign out" (sign out tinted #e58b8b with border rgba(229,139,139,0.34)).
2. **Network** — `.card`-equivalent: background `--color-surface` (#232532), border rgba(233,233,237,0.08), radius `--radius-lg`, padding 18px/20px. Kicker "NETWORK" 10px uppercase letter-spacing 0.14em text 45%; right "last checked 12:14" 11px text 45%. Three stat tiles (`grid-template-columns: repeat(3,1fr); gap: 10px`), each 11px/12px padding, radius `--radius-md`, background rgba(233,233,237,0.03), border rgba(233,233,237,0.06): label 10.5px text 48% over value 14px/500 — Adapter "LobbyBaz"; Relay "1 ms" + "good" in #7fdcab; Service 6px #4ec98a dot + "running". Footer row: `.btn.btn-primary` "Run checks" + note 11.5px text 50% — "Three checks: adapter present, relay reachable, host port open." Clicking Run checks reveals a 2px indeterminate bar (track rgba(233,233,237,0.08), 40%-wide accent fill sweeping, 1.1s ease-in-out infinite) for 1800ms.
3. **Game** — kicker "GAME". Field "Dota 2 location": `.input` (12px) prefilled `C:\\Program Files\\Steam\\steamapps\\common\\dota 2 beta` + `.btn.btn-secondary` "Browse". Field "Launch options": `.input` value "-console -novid". Note 11.5px text 50% — "Found on this PC. LobbyBaz opens the game when a room starts."
4. **Audio & voice** — kicker. Row: "Push to talk" 13px over "Hold to open the room channel" 11px text 48%; right a keycap — 5px/11px, radius `--radius-sm`, monospace 11.5px, color accent-200, border accent 30%, background accent 10%, label "Caps Lock". Then a faded divider (`height: 1px; linear-gradient(to right, transparent, rgba(233,233,237,0.12) 48px, rgba(233,233,237,0.12) calc(100% - 48px), transparent)`) — the Nocturne rule treatment. Then "Input level" 12.5px and an 8-bar meter (22px tall, 3px gap, radius 2px, heights 40/70/100/55/30/18/12/10%, colors stepping accent-600 → accent-500 → accent → accent-600 → accent-700 → accent-800 → accent-800 → accent-900). Caption 11.5px text 50% — "Microphone (Realtek) · speaking now".
5. **Notifications** — kicker, then three rows (11px vertical padding, 1px rgba(233,233,237,0.06) separators, none after the last): title 13px + description 11px text 48%, right a 34x19 toggle (radius 999px, 2px padding; ON track = accent 42%, knob 15px accent-200, knob right; OFF track = rgba(233,233,237,0.10), knob text 45%, knob left). Rows: "A room I can join opens" / "Matches your MMR filter"; "A friend comes online" / "Desktop toast, no sound"; "The tunnel drops" / "Always on while in a room". All three default ON, all clickable.
6. **About strip** (full width) — row, 14px/20px padding, radius `--radius-lg`, border rgba(233,233,237,0.07), background rgba(233,233,237,0.02), 30px gap: kicker "ABOUT" (text 40%), "Version 2026.08.26-0846" and "Not made by or connected to Valve Corporation" both 12.5px text 62%, spacer, then a right-aligned link "Read the terms" (12.5px, `--color-accent-300`) that opens the Terms modal.

---

## Screens 2 & 3 — Sign in / Create account
**Purpose**: authenticate or register. Shown as a modal over the app, which stays visible but blurred — this is the "flashy" surface, so keep the motion.

**Backdrop**: `position: absolute; inset: 0; overflow: auto; display: flex; justify-content: center; align-items: flex-start; padding: 30px 30px 70px`, background `color-mix(in srgb, var(--color-bg) 76%, #000)`, `backdrop-filter: blur(7px)`. The card carries `margin: auto` so it centres when it fits and scrolls when the window is short (this matters: the Create form is tall).

**Card**: 420px wide.
- Ambient glow behind it: absolutely positioned `inset: -120px -60px auto`, height 220px, `border-radius: 50%`, `background: radial-gradient(closest-side, color-mix(in srgb, var(--color-accent) 30%, transparent), transparent)`, `filter: blur(28px)`, opacity animating 0.35 → 0.8 → 0.35 over 5.5s ease-in-out infinite, `pointer-events: none`.
- Gradient border: outer wrapper radius 18px, `padding: 1px`, `background: linear-gradient(150deg, color-mix(in srgb, var(--color-accent) 55%, transparent), rgba(233,233,237,0.07) 42%, color-mix(in srgb, var(--color-accent) 28%, transparent))`, `box-shadow: 0 26px 70px rgba(0,0,0,0.6)`, `overflow: hidden`.
- Sweeping light: a 34%-wide, 1px-tall bar on the wrapper's top edge, `linear-gradient(to right, transparent, var(--color-accent-200), transparent)`, translating -100% → 200% over 3.4s linear infinite.
- Inner surface: radius 17px, padding `26px 26px 24px`, `background: linear-gradient(180deg, color-mix(in srgb, var(--color-accent) 8%, var(--color-surface)), var(--color-bg) 78%)`.
- Entrance: `opacity 0 → 1`, `translateY(14px) scale(0.985) → none`, 420ms `cubic-bezier(0.22,0.8,0.3,1)`.

**Card header**: 30px "LB" tile (radius 8px, accent-200 on accent 18%, border accent 40%, 11px/700) + wordmark "LOBBYBAZ" 15px/600, letter-spacing 0.22em, uppercase; right a live status "relay up · 1 ms" 10.5px #7fdcab with a 5px blinking #4ec98a dot. Below: an asymmetric rule — `height: 1px; linear-gradient(to right, color-mix(in srgb, var(--color-accent) 45%, transparent), rgba(233,233,237,0.10) 40%, transparent)`, margin `18px 0 16px`.

**Tabs**: 3px-padded track, radius 11px, background rgba(233,233,237,0.04), border rgba(233,233,237,0.07). Each tab flex:1, 8px/10px padding, radius 9px, 12.5px, 140ms background+color transition. Active: background accent 20%, color accent-200, `box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--color-accent) 34%, transparent)`. Idle: text 52%.

**Sign in body**: H2 "Back in the lobby" 20px/500; sub 12.5px text 55% — "Same name the other players saw last time." Field labels 10.5px uppercase letter-spacing 0.06em text 46%. Username `.input` (13.5px, 10px/12px padding) prefilled "arman13m99". Password label row also carries a right-aligned hint 10.5px text 38% — "no reset — hope you wrote it down". Submit: `.btn.btn-primary.btn-block`, 11px padding, 13.5px, letter-spacing 0.03em, background accent 12%, `box-shadow: 0 0 24px color-mix(in srgb, var(--color-accent) 22%, transparent)`, hover background accent 22%. Footer, centred, 11.5px text 42% — "1 player online · 1 room open right now" (live numbers).

**Create account body**: H2 "Pick your name" 20px/500; sub — "This is what the other nine players see when you sit down." Fields in a `1fr 1fr` grid, 12px gap: Username (prefilled "arman13m99"), Display name (placeholder "Arman Mcc"), Password, MMR (label suffix "(optional)" in normal case, placeholder "e.g. 3200"). Warning callout: 10px/12px padding, radius `--radius-md`, background rgba(214,166,88,0.08), border rgba(214,166,88,0.26), a 15px #d6a658 warning glyph, text 11.5px/1.5 #e0c294 — "A forgotten password cannot be reset. Write it down somewhere you will still have it next week." Then a 12.5px checkbox row (15px box, `accent-color: var(--color-accent)`, 9px gap) — "I accept the terms of use." with the last words linking to the Terms modal. Submit button as above, label "Create account".

---

## Screen 4 — Terms of use
**Purpose**: read and accept the current terms version. Same backdrop as auth.

Panel 660px wide, `max-height: 82vh`, column flex, radius `--radius-lg`, background `--color-surface`, `--shadow-lg`, entrance `lbRise` 320ms.
- **Header** `16px 22px 14px`: H2 "Terms of use" 18px/500 over "Version 2026-08-24 · about a two minute read" 11px text 45%; right "{pct}% read" 11px text 45% and a `.btn.btn-secondary` "Close".
- **Progress bar**: 2px track rgba(233,233,237,0.07) with a fill `linear-gradient(to right, var(--color-accent-600), var(--color-accent))` whose width is the scroll percentage, `transition: width 90ms linear`.
- **Body**: scrollable, padding `20px 22px 26px`, 13px/1.65, color text 78%. Opens with a callout paragraph — 11px/13px padding, radius `--radius-md`, background rgba(233,233,237,0.03), `border-left: 2px solid var(--color-accent-600)`, 12px text 58%: "Plain language, not legal advice. If LobbyBaz ever takes money or grows past friends of friends, have a lawyer read this properly." Then h3 sections at 14px/600 full-text color, 6px below, paragraphs 16px apart: **What LobbyBaz is**, **What we handle**, **What we do not do**, **Hosting a room**, **Fair play**, **Your account**, **Changes**. Full copy is in the prototype file — carry it over verbatim.
- **Footer**: 14px/22px, `border-top: 1px solid rgba(233,233,237,0.08)`, background `color-mix(in srgb, var(--color-bg) 60%, var(--color-surface))`. Left status: before the end, "Scroll to the end to accept." 11.5px text 45%; at the end, "Read to the end." in #7fdcab. Right: `.btn.btn-secondary` "Not now" and `.btn.btn-primary` "Accept and continue" — opacity 0.45 and inert until read, opacity 1 and active at the end.

---

## Interactions & Behavior
- **Auth tabs** switch between Sign in and Create account in place; no remount, no re-animation of the card.
- **Read gating**: on the terms body's scroll event, `pct = round(scrollTop / (scrollHeight - clientHeight) * 100)`, clamped to 100 when there is nothing to scroll. `atBottom = pct >= 98` (the 2% slack absorbs sub-pixel rounding). Accept is a no-op until `atBottom`.
- **Accepting** closes the modal, returns to the caller (Settings, or the sign-up form with the checkbox now ticked) and resets progress. "Not now" / "Close" return without accepting.
- **Terms links** — the About strip's "Read the terms" and the sign-up checkbox's "terms of use" — both open the modal; both must `preventDefault`.
- **Run checks** shows the indeterminate bar for 1800ms, then hides it. In production, drive it from the real check results and replace the three stat tiles' values on completion; surface failures per-tile rather than as one banner.
- **Notification toggles** flip optimistically.
- **Hover states**: every rail item, tab, chip and pill has one — a `rgba(233,233,237,0.05)` tint for neutral chrome, an accent `color-mix` tint for accent controls. Pressed states go one ramp step past the base (accent-400 on this dark ground). Keyboard focus is `outline: 2px solid var(--color-accent); outline-offset: 2px` — never the browser default.
- **Motion inventory** (all decorative loops are pure CSS and must survive re-render): `lbRise` entrance 420ms/320ms cubic-bezier(0.22,0.8,0.3,1); `lbSweep` 3.4s linear infinite (card top light) and 1.1s ease-in-out infinite (check bar); `lbGlow` 5.5s ease-in-out infinite (card halo); `lbBlink` 2.4–2.6s ease-in-out infinite (status dots). Respect `prefers-reduced-motion`: drop the loops, keep the entrance as a fade.
- **Responsive**: the prototype targets a desktop window from ~1000px up. Below ~900px, collapse the Settings card grid to one column; below ~760px, drop the Friends panel (it is already behind a flag). The modals are already `max-width: 100%` and scroll.

## State Management
Local UI state only — nothing here needs a store:
- `screen`: which of the four surfaces is showing. In production this is routing/modal state, not a switcher: Settings is a route; Sign in / Create account is the unauthenticated route; Terms is a modal openable from either.
- `authTab`: `"signin" | "create"`.
- `readPct`: number 0–100, derived from scroll; `atBottom` derived from it.
- `checking`: boolean during Run checks (timer cleared on unmount).
- `notifRooms`, `notifFriends`, `notifTunnel`: booleans, default true.

Data the screens read: current user (username, display name, MMR, avatar initials + color), relay latency, service state, tunnel state, adapter name, last-check time, online count, open-room count, app version, current terms version and whether the user has accepted it, game install path, launch options, push-to-talk key, input device + level.

## Design Tokens
From the Nocturne token sheet (`styles.css`, `:root`) — use the variables, not the literals, wherever the app already has them.

Colors: `--color-bg` #161826 · `--color-surface` #232532 · `--color-text` #e9e9ed · `--color-accent` #9184d9. Accent ramp: 100 #f5f4ff, 200 #e7e5fe, 300 #d2cefd, 400 #b5abfc, 500 #968ae0, 600 #796cbf, 700 #5d5294, 800 #423a6a, 900 #2b2741. Non-token literals used deliberately: #4ec98a / #7fdcab (healthy status, and the avatar ground carried over from the current app), #0f1a14 (initials on that green), #e58b8b (destructive text), #d6a658 / #e0c294 (warning). Hairlines are `rgba(233,233,237,0.06–0.10)`; tinted fills are `color-mix` of accent or text.

Spacing (density 0.70x): `--space-1` 2.8 · `--space-2` 5.6 · `--space-3` 8.4 · `--space-4` 11.2 · `--space-6` 16.8 · `--space-8` 22.4px.

Radii: `--radius-sm` 4 · `--radius-md` 8 · `--radius-lg` 14px. Card-specific: 17/18px on the auth card, 9–12px on rail tiles and avatars, 999px on pills.

Shadows: `--shadow-sm` `0 0 0 1px #3f424d` · `--shadow-md` `0 0 0 1px #595d6c, 0 6px 18px rgba(0,0,0,0.55)` · `--shadow-lg` `0 0 0 1px #9397ab, 0 16px 40px rgba(0,0,0,0.65)`. The auth card's `0 26px 70px rgba(0,0,0,0.6)` and the glow shadows are intentional one-offs.

Type: Inter for both headings and body (`--font-heading` / `--font-body`), headings at weight 500 — do not bolden past it; hierarchy is size and space. Scale in use: 26 (page H1), 20 (modal H2), 18, 17, 15, 14, 13.5, 13, 12.5, 12, 11.5, 11, 10.5, 10 (uppercase kickers, letter-spacing 0.12–0.14em).

## Assets
No images. Icons are inline SVG paths from **Phosphor Icons** (phosphoricons.com) at 15–17px, `fill: currentColor` — Lobby (rows), Room (circle), Events (crown), Settings (gear), warning triangle. Replace with the app's existing Phosphor import rather than pasting paths. Fonts load from Google Fonts (Inter 400/500/600/700); use the app's bundled Inter if it has one.

## Files
- `LobbyBaz Redesign.dc.html` — the prototype: all four screens, the shared chrome, every inline style, the full terms copy, and the interaction logic (screen/tab state, scroll gating, toggles, check simulation). Read this alongside the README; it is the source of truth for anything the README leaves ambiguous.
- `_ds/nocturne-.../styles.css` — the Nocturne token sheet and component layer (`.btn`, `.input`, `.card`, `.tag`, `.table`, `.dialog`) the prototype composes with. Map these to the app's equivalents.
- `uploads/*.png` — screenshots of the current app: `settings`, `terms`, `signin`, `create_account` (the screens being replaced) and `lobby`, `rooms` (the screens the redesign matches — use these to confirm the chrome details).

## Out of scope / notes for the implementer
- The **top switcher strip** in the prototype is review scaffolding. Delete it.
- **Game**, **Audio & voice** and **Notifications** are proposed sections, not existing features. Confirm with the product owner before wiring; the layout survives dropping any of them.
- Terms copy is a rewrite pending the owner's read. Bump the terms version whenever it changes, which re-prompts every user — that is the point of the version field.
