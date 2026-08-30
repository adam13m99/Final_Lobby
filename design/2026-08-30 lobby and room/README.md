# Handoff: LobbyBaz — Lobby & Room redesign

## Overview
LobbyBaz is a desktop client that puts players in Iran into the same Dota 2 match when Valve matchmaking is unreachable: one player's PC hosts, a relay carries traffic between the players in a room. This package covers the two main screens:

1. **Lobby** — the room browser: filters, a sortable room table, chat dock, friends panel.
2. **Room** — the seat board: room identity + network facts, Radiant/Dire seats with Observers, chat dock.

The redesign keeps the existing app's information architecture and control positions and changes the visual treatment: darker grounds, accent gradients and left edges, a slow sweeping light line, soft glows, faded-end rules, and tinted status pills. Copy is rewritten (approved) — keep it verbatim unless product asks otherwise.

Companion package `design_handoff_lobbybaz_auth_settings/` covers Settings, Sign in, Create account and Terms; the shared chrome is specified in both — implement it once.

## About the Design Files
The files in this bundle are **design references created in HTML** — a prototype of the intended look and behavior, not production code to copy. The task is to **recreate these designs in the app's existing environment** (Electron renderer / React / whatever LobbyBaz already uses) with its established components, styling approach and routing. If no such environment exists yet, pick the most appropriate framework and implement there.

The prototype is a single streaming component that renders both screens with a switcher strip pinned at the top. **That strip is review scaffolding — delete it.** In production, Lobby and Room are routes.

## Fidelity
**High-fidelity.** Final colors, typography, spacing, radii, motion and copy. Recreate pixel-closely using the codebase's own primitives. Every value derives from the Nocturne token sheet (see Design Tokens); prefer wiring to equivalent tokens already in the app over hard-coding hexes.

---

## Shared app chrome

Root: `height: 100vh; overflow: hidden; display: flex`, font Inter, base 13px, text `--color-text` (#e9e9ed). Background is a tinted radial rather than flat: `radial-gradient(1200px 520px at 22% -6%, color-mix(in srgb, var(--color-accent) 11%, var(--color-bg)), var(--color-bg) 62%)`. The prototype adds `padding-top: 34px` for the switcher — drop it.

**Left rail** — 56px fixed, `border-right: 1px solid rgba(233,233,237,0.07)`, ground `linear-gradient(to bottom, color-mix(in srgb, var(--color-accent) 7%, color-mix(in srgb, var(--color-bg) 84%, #000)), color-mix(in srgb, var(--color-bg) 84%, #000) 42%)`, padding `10px 0 12px`.
- Brand tile: 32px, radius 9px, "LB" 11.5px/700, color `--color-accent-200`, background accent 18%, border accent 42%, `box-shadow: 0 0 18px color-mix(in srgb, var(--color-accent) 26%, transparent)`, 14px bottom margin.
- Nav items (Lobby, Room, Events): 44px wide, 7px vertical padding, radius 9px, 16px Phosphor icon over a 9.5px label, gap 3px, item gap 6px. Idle: text 50%. Active: color accent-200, background accent 16%, `box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--color-accent) 32%, transparent), 0 0 16px color-mix(in srgb, var(--color-accent) 14%, transparent)`.
- Spacer, then Settings (same shape, idle).
- **Status stack** at the bottom, 9px/1.2 centered text 45%: a 20px circle (background rgba(78,201,138,0.12), border rgba(78,201,138,0.28)) holding a 6px #4ec98a dot with `0 0 7px` glow, label "service / running" in #7fdcab; a 22px hairline `rgba(233,233,237,0.10)`; then a 6px #4ec98a dot with a 2.6s blink and label "tunnel / on".

**Top bar** — 44px, `border-bottom: 1px solid rgba(233,233,237,0.07)`, background `color-mix(in srgb, var(--color-bg) 90%, #000)`, padding `0 14px`. Contains only the search field: `flex: 0 1 320px; min-width: 150px`, a 13px magnifier at `left: 10px` in text 38%, `.input` with `padding: 6px 10px 6px 29px`, 12px, placeholder "Search rooms or hosts". Everything else was deliberately moved out (status → rail, identity → friends panel).

**Alert banner** (below the top bar, both screens) — `padding: 9px 14px`, radius `--radius-md`, `background: linear-gradient(96deg, rgba(229,139,139,0.13), rgba(229,139,139,0.045) 60%, transparent)`, border rgba(229,139,139,0.26), a 2px left edge `linear-gradient(to bottom, transparent, #e58b8b, transparent)`, a 14px #e58b8b warning glyph, text 12.5px #efb9b9 "lease expired locally", and a right-aligned "Retry" at 11.5px. Render only when a lease/connection error is live.

**Right panel** — 228px, `border-left: 1px solid rgba(233,233,237,0.07)`, ground `linear-gradient(to bottom, color-mix(in srgb, var(--color-accent) 6%, color-mix(in srgb, var(--color-bg) 88%, #000)), color-mix(in srgb, var(--color-bg) 88%, #000) 34%)`, column flex.
- **Profile row** at the top (the app's only identity affordance): `padding: 11px 13px`, `border-bottom: 1px solid rgba(233,233,237,0.07)`, `background: linear-gradient(100deg, color-mix(in srgb, var(--color-accent) 13%, transparent), transparent 72%)` (hover → 20%), a 2px accent left edge, a 32px avatar (radius 9px, #4ec98a, #0f1a14 initials 11px/600, `box-shadow: 0 0 0 3px rgba(78,201,138,0.10)`), name 12.5px/600 over "no MMR set" 10.5px text 46%, and a 13px chevron in text 34%. Opens the profile/settings.
- **Friends** below, `padding: 11px 13px`: title 13.5px/600 with a 23px "+" square (radius `--radius-sm`, border rgba(233,233,237,0.10); hover border accent 45%, color accent-200, accent glow); `.input` "Their username" + `.btn.btn-primary` "Find"; the error line "no player has that username - check the spelling" 11.5px #efb9b9; a faded-end hairline; then the empty state 11.5px text 45% "No friends yet. Add somebody by their username."

**Chat dock** — bottom of the centre column, `margin: 10px 14px 12px`, radius `--radius-lg`, border rgba(233,233,237,0.07), background `color-mix(in srgb, var(--color-bg) 88%, #000)`, `overflow: hidden`.
- Tab row `padding: 7px 10px`, washed `linear-gradient(to right, color-mix(in srgb, var(--color-accent) 8%, transparent), transparent 50%)`, bottom border rgba(233,233,237,0.06). Active "Lobby" pill: 11.5px, `4px 11px`, radius 999px, accent-200 on accent 16% with `inset 0 0 0 1px` accent 28%. Idle "Room"/"Party": text 52%, hover rgba(233,233,237,0.05). A 22px "+" circle. Right: "1 in the lobby" 11px text 42%.
- Message line 12px: timestamp text 32%, the word "system" in `--color-accent-300`, body text 55%.
- Composer: `.input` "Message #lobby" + `.btn.btn-primary` "Send" (background accent 12%, hover 24%), 8px gap.

---

## Screen 1 — Lobby

One panel fills the content area: radius `--radius-lg`, `background: linear-gradient(180deg, color-mix(in srgb, var(--color-accent) 6%, var(--color-surface)), var(--color-surface) 26%)`, border rgba(233,233,237,0.08), `overflow: hidden`, entrance `lbRise` 300ms.

**Panel header** `padding: 12px 16px` — "Rooms" 17px/500, then "1 of 1 shown" 11.5px text 45%; right "sorted by players, highest first" 11.5px text 42% and `.btn.btn-primary` "+ Create room" (12px, `7px 15px`, background accent 14%, `box-shadow: 0 0 22px` accent 22%, `lbPulse` 3.6s). Decorations: a 16%-wide 1px accent-200 light sweeping the top edge (`lbSweep` 5.2s linear infinite) and a blurred accent halo at `left: -60px; top: -70px` (220×180, `blur(26px)`, `lbGlow` 6s).

**Faded rule**, then the **filter row** — `padding: 9px 16px 8px`, horizontally scrollable, 3px gap. Chips: 11.5px, `5px 10px`, radius 7px. Active: accent-200 on accent 20% with `inset 0 0 0 1px` accent 32%. Idle: text 52%. Order: All · Has space · Not started (5px #4ec98a dot) · In game (5px #e58b8b dot) · My MMR · Friends. These live here, directly above the column headers — not in the top bar.

**Column header row** — `padding: 9px 16px`, background rgba(233,233,237,0.02), top border rgba(233,233,237,0.05), labels 10px uppercase letter-spacing 0.13em text 38%. Columns and flex bases, used identically by the header and the rows:
| Column | Flex |
|---|---|
| Room | `1 1 220px; min-width: 190px` |
| Players ↓ (accent-300 when the active sort) | `0 1 118px; min-width: 84px`, right-aligned |
| MMR | `0 1 74px; min-width: 40px` |
| Host ping | `0 1 92px; min-width: 62px` |
| Status | `0 0 96px` |

**Room row** — `padding: 11px 16px`, 16px gap, hairline borders rgba(233,233,237,0.05), `cursor: pointer`. The row you are in gets `background: linear-gradient(96deg, color-mix(in srgb, var(--color-accent) 12%, transparent), transparent 58%)` (hover → 20%) and a 2px accent left edge; other rows are transparent with the same hover.
- Room cell: a 28px avatar (radius 8px, `linear-gradient(140deg, #b06ec4, #8a5ec9)`, #f5f4ff initials, `0 0 14px rgba(176,110,196,0.35)`) + name 13.5px/600 (nowrap, ellipsis) over a meta line: an "OPEN" tag (9.5px, letter-spacing 0.1em, `2px 6px`, radius 4px, #7fdcab on rgba(78,201,138,0.12), border rgba(78,201,138,0.24)), the host name in text 48%, and "You are here" in accent-300. The meta line clips inside its own cell.
- Players: "2/10" 13px/500 then a 10-pip bar — 4×12px pips, radius 1px; filled `--color-accent` with `0 0 6px` accent glow, empty rgba(233,233,237,0.10).
- MMR: 12.5px text 68%.
- Host ping: a 5-bar signal meter (3px wide, heights 4/6/8/10/12px, radius 1px) followed by "37 ms" — both vertically centered with `line-height: 1` so the number and bars share a midline. Color by quality: #7fdcab good, #e5c78b fair, #e58b8b poor; unlit bars rgba(233,233,237,0.14).
- Status: `.btn.btn-primary` "Open" (12px, `6px 16px`, background accent 13%, hover 24%, **no glow**) — or "Join" as `.btn.btn-secondary` for rooms you are not in — plus a 7px status dot (#4ec98a open, `0 0 8px` glow).

---

## Screen 2 — Room

**Header band** — `padding: 10px 14px`, radius `--radius-md`, `background: linear-gradient(100deg, color-mix(in srgb, var(--color-accent) 13%, var(--color-surface)), var(--color-surface) 54%)`, border accent 22%, a 20%-wide sweeping top light (`lbSweep` 4.8s), wraps at narrow widths. Two tiers:

*Tier 1 (actions):* `.btn.btn-secondary` "← Lobby" · 36px room avatar (radius 10px, the violet gradient, `0 0 16px rgba(176,110,196,0.32)`) · room name 19px/500 with `letter-spacing: -0.015em` (nowrap, ellipsis) + the "OPEN" tag · then right-aligned: the **(i)** guide toggle, "Invite", and the primary action.
- **(i)**: 22px circle, italic 11px/600. Idle: text 55%, border rgba(233,233,237,0.16), transparent. Active: accent-200, border accent 50%, background accent 18%.
- **Primary action** mirrors GameRanger: **Join Game** when you are a participant, **Create Game** when you are the host. 12.5px, `7px 18px`, background accent 15% (hover 26%), `box-shadow: 0 0 12px` accent 14% — a restrained glow, no pulse.

*Tier 2 (facts):* a faded-end rule at `rgba(233,233,237,0.20)`, then a **stat strip** on a recessed panel — `padding: 9px 12px`, radius `--radius-sm`, `background: rgba(6,7,12,0.55)`, `box-shadow: inset 0 0 0 1px rgba(233,233,237,0.07)`. Cells (`padding-right: 18px`) separated by 1px vertical hairlines (26px tall, `linear-gradient(to bottom, transparent, rgba(233,233,237,0.26), transparent)`, 18px right margin). Each cell: label 9.5px uppercase letter-spacing 0.12em text 58%, value 13px/500 full-strength text, nowrap + ellipsis.
- Host — "Deploy Check"
- Players — "2 / 10" (the "/ 10" in text 40%)
- Your address — "10.87.0.7"
- Host address — "10.87.0.2"
- Room network — a 6px #4ec98a dot with glow, "connected" in #7fdcab, and a "37 ms" pill (11px, `2px 7px`, radius 999px, #7fdcab on rgba(78,201,138,0.10))

**Guide strip** (collapsed by default, toggled by the (i)) — `padding: 11px 15px`, radius `--radius-md`, `background: color-mix(in srgb, var(--color-accent) 8%, rgba(13,15,24,0.85))`, border accent 22%. A kicker "HOW A ROOM WORKS" 9.5px accent-300, then three numbered steps inline at 11.5px text 60% — each number a 15px accent-200 circle on accent 22%: (1) Take a seat on Radiant or Dire. (2) Join the room's network. (3) Dota opens on this PC and finds the host. A right-aligned "Hide". *This replaced three large step cards that dominated the screen; the flow is now discoverable but not always-on.*

**Team boards** — two equal columns, 8px gap, each radius `--radius-md`, `background: color-mix(in srgb, #0b0d14 46%, var(--color-surface))`, border rgba(233,233,237,0.09) (deliberately darker than `--color-surface` for contrast against the page).
- Board header `padding: 9px 13px`, washed with its team color — `linear-gradient(to right, rgba(78,201,138,0.16), transparent 66%)` for Radiant, `rgba(229,139,139,0.16)` for Dire — bottom border rgba(233,233,237,0.06). A 7px team swatch (radius 2px, #4ec98a / #e58b8b, `0 0 7px` glow), the team name 11px/600 uppercase letter-spacing 0.14em, and a right count "1/5 · 0/2 obs" (the obs part at text 28%).
- **Occupied seat**: `padding: 8px 13px`, a tinted wash + 2px left edge in the occupant's color (violet `rgba(176,110,196,0.11)` / `#b06ec4` for the host, accent 16% / `--color-accent` for you), seat number 11px text 40% in a 13px gutter, a 24px avatar (radius 7px), name 12.5px/600 with "(you)" in 400/text 45%, and a sub-line 11px text 46% ("Host · no MMR set").
- **Empty seat**: same row metrics, label **"Empty"** at 12.5px text 62%, hover `background: color-mix(in srgb, var(--color-accent) 8%, transparent)`, hairline separators. No per-row button — clicking the row takes the seat. (An earlier "Take it" pill was removed as redundant.)
- **Observers** sub-section, at the bottom of each board, visually separated: a header band `padding: 7px 13px 5px; margin-top: 4px`, `background: rgba(11,13,20,0.5)`, `border-top: 1px solid rgba(233,233,237,0.11)` — a 6px `--color-accent-600` swatch, "OBSERVERS" 9.5px/600 uppercase text 45%, right count "0/2". Then two rows on a recessed ground `rgba(11,13,20,0.32)`, `padding: 7px 13px`, labelled "Empty" at 11.5px text 46%, ids O1/O2 (Radiant) and O3/O4 (Dire). *These replaced a separate full-width "Watching" panel; observers now belong to a side.*

**Footer** — `padding: 9px 14px`, radius `--radius-md`, background rgba(233,233,237,0.02), border rgba(233,233,237,0.07): "Radiant vs Dire · 10 seats · 4 observers" 11.5px text 42%, spacer, `.btn.btn-secondary` "Leave room" tinted #e58b8b with border rgba(229,139,139,0.34), hover rgba(229,139,139,0.10).

The room content sits on its own darker stage: `radial-gradient(720px 260px at 14% 0%, color-mix(in srgb, var(--color-accent) 9%, #0d0f18), #0b0d14 68%)` with `inset 0 0 0 1px rgba(233,233,237,0.05)`, and scrolls if the window is short.

---

---

## What changed from the current app

Structural and behavioral deltas against `current-app/lobby.png` and `current-app/rooms.png`, so you can review this as a diff rather than re-reading the whole spec. Everything not listed here kept its position and purpose.

### Moved
| Element | Was | Now | Why |
|---|---|---|---|
| Profile ("Arman Mcc / no MMR set") | Top bar, far right | Top of the Friends panel, as its own accented row with a chevron | Identity belongs with the social column, not with search; the top bar had four unrelated things competing in 44px |
| "service running" | Top bar pill | Bottom of the left rail, above "tunnel on" | It is machine state, not navigation. Sits with the other daemon indicator so both read at a glance |
| Filter chips (All, Has space, Not started, In game, My MMR, Friends) | Top bar, inline after search | Inside the Rooms panel, directly above the Player / MMR / Host ping headers | Filters and the columns they filter now read as one control surface; they were previously far from the data and easy to miss |
| Host network facts (your address, host address, latency) | Room footer, as a run-on line | Room header, as a labelled stat strip beside the room identity | These are the first thing a player checks when a match will not start; they were the last thing on screen |

### Removed
- **"1 online"** — the count is already implicit in the lobby ("1 in the lobby") and the room's Players cell.
- **The three step cards** ("Take a seat", "Join the room's network", "Start Dota") — they occupied a full band permanently to teach a flow you learn once. Replaced by a small **(i)** in the room header that expands the same three steps as one line.
- **The "Watching" panel** — a separate full-width block for spectators. Replaced by a two-row **Observers** sub-section at the bottom of each team board, so observers belong to a side.
- **Per-seat "Take it" buttons** — the whole row is the target; the pill was redundant and added visual noise to eight rows.

### Renamed / re-labelled
- Empty seat label: **"Sit here" → "Empty"** (states the seat's condition rather than instructing; the affordance is the hover).
- Room primary action: **"Join the network" → "Join Game"**, promoted next to Invite in the header, with **"Create Game"** as the host variant. This follows GameRanger's convention, where the same slot reads Join or Create depending on role.
- Room footer summary: now "Radiant vs Dire · 10 seats · 4 observers".

### Visual treatment (no layout change)
- Page ground is a tinted radial instead of flat `--color-bg`; the rail and Friends panel carry a faint accent wash at the top.
- Accent **gradient left edges** mark "this one is yours or active": your room row, the seated rows, the profile row.
- A slow **sweeping 1px light** across the Rooms and Room header top edges, plus one blurred accent **halo** behind the Rooms title. One pulse only, on Create room.
- **Faded-end rules** (transparent → hairline → transparent) replace flat borders as section separators, per Nocturne.
- Room screen sits on a **near-black stage** with the boards darkened below `--color-surface` and borders lifted, so team color washes read strongly.
- Status colors are consistent throughout: #4ec98a/#7fdcab healthy, #e5c78b fair, #e58b8b problem. Dots carry a soft glow.
- Other players' avatars use a violet gradient (#b06ec4 → #8a5ec9); yours stays green — you can find yourself in a list without reading names.

### Fixed from the old screens
- **Ping meter**: number and bars sat on different baselines and the meter had 3 bars. Now 5 bars, vertically centered against the number with a shared midline.
- **Room name overflow**: long names bled into the Players column. The name cell now has a real floor and clips with an ellipsis; the meta line clips inside its own cell.
- **Top bar crowding at narrow widths**: the identity cluster could run under the Friends panel. Search and the chip group now shrink (the chips scroll horizontally); nothing else does.
- **Glow discipline**: Open/Join were as loud as the primary CTA. Open has no glow, Join/Create Game has a restrained 12px glow and no pulse. Exactly one pulsing element per screen.

## Interactions & Behavior
- **Lobby → Room**: clicking a row (or Open/Join) enters that room. Back via "← Lobby".
- **Filters** are multi-select except "All", which clears the others. The chip row scrolls horizontally when it cannot fit; it must never be clipped without a scroll affordance.
- **Sorting** is by the header cells; the active one is accent-300 with a direction arrow. Default: players descending.
- **Guide strip** toggles from the (i) and from its own "Hide"; remember the last state per user (it should stay closed for returning users).
- **Seats**: clicking an empty seat takes it; clicking your own seat vacates it. Observers behave the same against the 2-per-side cap. Only the host can start/create the game.
- **Primary action** is a single button whose identity depends on role: host → Create Game; participant → Join Game. Disable with a reason when the room is mid-game or you have no seat.
- **Hover states**: neutral chrome tints to `rgba(233,233,237,0.05)`; accent controls tint via `color-mix` of the accent (rows 12%→20%, seats +8%, buttons 15%→26%). Keyboard focus is `outline: 2px solid var(--color-accent); outline-offset: 2px` — never the browser default.
- **Motion inventory** (decorative loops are pure CSS and must survive re-render): `lbRise` entrance 300ms ease; `lbSweep` 4.8–5.2s linear infinite (header light lines); `lbGlow` 6s ease-in-out infinite (panel halo); `lbBlink` 2.6s (tunnel dot); `lbPulse` 3.6s (Create room only). Respect `prefers-reduced-motion`: drop the loops, keep the entrance as a fade.
- **Responsive**: desktop from ~1000px. The header band, room rows and step content all wrap; the room column scrolls. Below ~900px collapse the team boards to one column; below ~760px drop the friends panel.

## State Management
Local UI state only:
- `screen`: `"lobby" | "room"` — routing in production, not a switcher.
- `guide`: boolean, the (i) strip; default false, persist per user.
- `hosting`: boolean — drives Join Game vs Create Game. In production this derives from the room's host id, not a flag.
- Filter selection, sort column + direction, search text.

Data the screens read: room list (id, name, host name + initials, host avatar color, players seated / capacity, MMR filter, host ping ms, status, whether you are in it); for the open room — host, your address, host address, relay latency, network state, seats per side with occupant name/MMR/role, observer seats; your identity (name, MMR); lobby chat messages; friends list; service + tunnel state; any lease error.

## Design Tokens
From the Nocturne token sheet (`nocturne-styles.css`, `:root`) — use the variables, not the literals, wherever the app already has them.

Colors: `--color-bg` #161826 · `--color-surface` #232532 · `--color-text` #e9e9ed · `--color-accent` #9184d9. Accent ramp: 100 #f5f4ff, 200 #e7e5fe, 300 #d2cefd, 400 #b5abfc, 500 #968ae0, 600 #796cbf, 700 #5d5294, 800 #423a6a, 900 #2b2741. Non-token literals used deliberately: #4ec98a / #7fdcab (healthy status, your avatar), #0f1a14 (initials on that green), #e58b8b / #efb9b9 (Dire, destructive, errors), #e5c78b (fair ping), #b06ec4 → #8a5ec9 (other players' avatars), #0b0d14 / #0d0f18 / rgba(6,7,12,…) (the darker room grounds). Hairlines are `rgba(233,233,237,0.05–0.26)`; tinted fills are `color-mix` of accent or text.

Spacing (density 0.70x): `--space-1` 2.8 · `--space-2` 5.6 · `--space-3` 8.4 · `--space-4` 11.2 · `--space-6` 16.8 · `--space-8` 22.4px.

Radii: `--radius-sm` 4 · `--radius-md` 8 · `--radius-lg` 14px; 7–10px on avatars and tiles, 999px on pills.

Shadows: `--shadow-sm` `0 0 0 1px #3f424d` · `--shadow-md` `0 0 0 1px #595d6c, 0 6px 18px rgba(0,0,0,0.55)` · `--shadow-lg` `0 0 0 1px #9397ab, 0 16px 40px rgba(0,0,0,0.65)`. The accent glows (`0 0 12–22px color-mix(accent …)`) and inset hairlines are intentional one-offs.

Type: Inter throughout (`--font-heading` / `--font-body`), headings at weight 500 — do not bolden past it. Scale in use: 19 (room name), 17 (panel title), 13.5, 13, 12.5, 12, 11.5, 11, 10.5, 10, 9.5 (uppercase kickers, letter-spacing 0.12–0.14em).

## Assets
No images. Icons are inline SVG paths from **Phosphor Icons** (phosphoricons.com) at 13–16px, `fill: currentColor` — rows (Lobby), circle (Room), crown (Events), gear (Settings), magnifier (search), warning triangle, caret-right. Replace with the app's existing Phosphor import rather than pasting paths. Fonts load from Google Fonts (Inter 400/500/600/700); use the app's bundled Inter if it has one.

## Files
- `LobbyBaz Lobby + Room v2.dc.html` — the prototype: both screens, the shared chrome, every inline style and the interaction logic. Read this alongside the README; it is the source of truth for anything the README leaves ambiguous.
- `nocturne-styles.css` — the Nocturne token sheet and component layer (`.btn`, `.input`, `.card`, `.tag`, `.table`, `.dialog`) the prototype composes with. Map these to the app's equivalents.
- `current-app/*.png` — screenshots of the screens being replaced: `lobby`, `rooms`, plus `settings`, `signin`, `create_account`, `terms` for context.

## Out of scope / notes for the implementer
- The **top switcher strip** is review scaffolding. Delete it.
- **Observers** (2 per side) and the **guide strip** are proposals, not existing features — confirm with the product owner before wiring. Both are removable without disturbing the layout.
- The room row's ping quality thresholds are illustrative; use the app's real bands.
- Sign in, Create account, Settings and Terms are specified in `design_handoff_lobbybaz_auth_settings/`. The rail, top bar, chat dock and friends panel are shared — build them once from this document (it is the newer of the two).
