# Final Lobby — product decision suite

**For:** the product owner
**From:** engineering
**Date:** 2026-08-23
**Status:** **answered by the owner, 2026-08-24.** Recorded as D37-D46 in
`docs/decisions.md`, which is the durable record; this file is kept as the
original questions and the owner's own words.

**Still unanswered:** 3.4 who the admins are - 5.3 uplink port speed from
MobinHost - 5.6 when to buy the dedicated server. Plus one raised by the
answers themselves: whether Tournaments (3.6) is a real feature or a
placeholder, given the spec lists it as out of scope.

---

## Why you are being asked now

The network core is finished. A real Dota 2 match ran between two PCs over our
own relay on 2026-08-23, which was the gate for everything else. Nothing was
allowed to start until that worked, and now it has.

What exists today is a **test harness**, not the product. It was built to prove
the network, and it succeeded. It has a name-only sign-in, one list of rooms,
ten slots, two chat channels and a Launch button. Everything below is about
turning that into something real players use.

**How to answer:** each decision has options and a recommendation. Tick one per
decision, or write your own. Where I say *"already in the spec"*, that means you
decided it in August and I am only confirming it still holds — those are quick.
Where I say *"new"*, nobody has decided it yet.

You do not have to answer all of it at once. **Part 1 blocks real players. Part
5 needs answers from you or from MobinHost, not from me.** Parts 2–4 can follow.

A note on my role: I make the technical calls myself and do not bring you those.
Everything in this document is a question about *what the product is*, which is
yours. Where a choice has a technical cost I have said so plainly, and where I
think one option is clearly right I have said that too rather than pretending
they are balanced.

---

# Part 1 — Identity and accounts

**This is the one that blocks real players.** Everything else can ship late.

Right now a player is a name they typed plus an ID generated at install. There
is no password. That was a deliberate decision for the two-PC test (D31), and
its consequence is written down: **a kicked player who reinstalls comes back as
somebody new.** The five-minute kick block, room ownership, and declared MMR all
rest on nothing that a determined person has to respect.

For two people testing, that costs nothing. For a public lobby it means
moderation does not work.

### Decision 1.1 — What is an account? *(spec says username + password)*

- [ ] **A. Username and password. No email, no SMS.** *(recommended — this is
      what the spec already says)*
  Works entirely on the domestic network with no third party involved. Nothing
  to verify, nothing that can be blocked, no delivery costs.
  *Cost:* people forget passwords and we have no email to reset them with. We
  need an answer for that — see 1.2.

- [ ] **B. Phone number with SMS verification.**
  Much harder to create throwaway accounts, so bans actually bite. Familiar to
  Iranian users from other services.
  *Cost:* an SMS provider is a dependency that can fail, cost money per message,
  and be blocked. It also puts a real identifier on a gaming account, which some
  players will refuse. Signup stops working the day the provider does.

- [ ] **C. Invite codes only — existing players invite new ones.**
  Very strong against abuse, and gives a controlled early community.
  *Cost:* it caps growth deliberately. Wrong for a platform whose value comes
  from having enough people online to fill rooms.

- [ ] **D. Keep it as it is — a name, no password.**
  Nothing to build. Zero friction to start playing.
  *Cost:* kicks, bans, room ownership and MMR are all unenforceable. I would not
  recommend launching this way, but it is a legitimate choice if the first
  audience is a small trusted group.

**My recommendation: A.** It matches the spec, has no external dependency, and
is what a domestic-only product can actually rely on. B is the right upgrade
later if abuse becomes a real problem — but build it when you have the problem,
not before.

**Your answer:**
Lets go with Username and password but add the foundation and architucture for email and SMS, so if i could manage them we could easily add them.

### Decision 1.2 — Password recovery *(new)*

If you choose A above, this needs an answer, because we have no email.

- [ ] **A. A recovery code shown once at signup, which the player saves.**
      *(recommended)* Costs nothing, works offline, no third party.
      *Cost:* people lose them, and then the account is genuinely gone.
- [ ] **B. Admin-assisted reset — the player contacts staff.**
      Humane and simple at small scale.
      *Cost:* it is a support workload that grows with the player count, and it
      is itself a target for social engineering.
- [ ] **C. No recovery. A lost password means a new account.**
      Honest and free.
      *Cost:* the player loses their MMR, friends and history.

**Your answer:**
availabile if SMS or Email are availailbe else none.
### Decision 1.3 — Do existing test identities carry over? *(new)*

- [ ] **A. No. Everyone signs up fresh when accounts arrive.** *(recommended —
      there are two of them and they are yours)*
- [ ] **B. Yes, migrate the name and MMR into a new account.**

**Your answer:**
go with A.
### Decision 1.4 — Terms and conditions *(already in the spec)*

The spec says terms are shown at install and acceptance is recorded against the
account with a version and timestamp, so it is auditable and can be re-asked
when terms change. **I am assuming this still holds.**

One thing I need from you: **somebody has to write the terms.** That is a legal
text about acceptable behaviour, data we hold, and what gets you banned. I can
draft a plain-language starting point, but it should not ship without you
reading it and ideally without a lawyer seeing it.

- [ ] Confirmed, and I will supply the terms text
- [ ] Confirmed, and I want engineering to draft something for me to review
- [ ] Changed — describe:

**Your answer:**
I Let u to engineer and draft something.
---

# Part 2 — Rooms and admission

The room rules you set in August are built and working. This part is mostly
confirming them, plus the parts of the spec that were written but never built.

### Decision 2.1 — Room privacy *(spec says password, friends-only, invite-only — none built)*

The spec promises all three. None exist yet; every room is currently public.
Which do you want at launch?

- [ ] **A. Public rooms and password rooms only.** *(recommended)*
      Covers the real need — "my friends and I want a private game" — with one
      simple concept everyone already understands from GameRanger.
- [ ] **B. All three: public, password, friends-only, invite-only.**
      Matches the spec exactly.
      *Cost:* friends-only and invite-only both depend on a friends system
      (Decision 3.2) that does not exist. This ties the room work to the social
      work and delays both.
- [ ] **C. Public rooms only at launch.**
      Fastest, and maximises the chance that a room has enough people to start.
      *Cost:* no way to play privately, which people will ask for immediately.

**My recommendation: A.** A password is one text field and no dependencies.
Friends-only becomes easy once friends exist, and can be added then.

**Your answer:**
Go with B.
### Decision 2.2 — MMR-based admission *(spec says minimum or range — not built)*

- [ ] **A. Host can set a minimum MMR.** *(recommended)*
      One number. Covers the actual complaint, which is much weaker players
      joining a serious game.
- [ ] **B. Host can set a minimum and a maximum.**
      Lets strong players find equal games too.
      *Cost:* marginally more UI; genuinely useful; not much downside.
- [ ] **C. No MMR gating. Show MMR and let hosts kick.**
      Simplest, and keeps rooms filling.
      *Cost:* pushes the work onto hosts and makes kicking routine.

**Worth knowing before you choose:** MMR is self-declared and changeable once a
week. A gate on a number players choose themselves is a social signal, not a
real barrier. That is fine — it filters the honest majority — but it should not
be sold as a guarantee.

**Your answer:**
Go with A.
### Decision 2.3 — Room size *(new — currently always 10)*

Every room is ten playing slots. Dota is also played 1v1, 3v3 and 5v5 privately.

- [ ] **A. Always 10.** *(recommended for launch)* Simple, and it is the game
      everyone is here for.
- [ ] **B. Host picks the size when creating the room** (1v1, 3v3, 5v5, 5v5 full).
      *Cost:* small technical cost, real UI cost, and it splits your player base
      across more room types — which hurts when the population is small.

**My recommendation: A now, B once there are enough players that rooms fill
easily.** Splitting a small population is the fastest way to make the lobby feel
empty.

**Your answer:**
Room size is 10 Main Slots + up to 5 observers + 3 admin slots.
### Decision 2.4 — Who can kick? *(already decided: host only)*

Today only the host can kick, and a kicked player is barred from that room for
five minutes. Admins hold a spectator slot but no kick power in the code yet.

- [ ] **A. Host only, plus admins can kick anyone anywhere.** *(recommended)*
- [ ] **B. Host only. Admins moderate by other means.**
- [ ] **C. Host, plus a majority vote of the room.**
      *Cost:* vote-kick is reliably abused to bully people. I would avoid it.

**Your answer:**
go with A.
### Decision 2.5 — Is five minutes the right kick block? *(already decided: 5 min)*

The block is per-room and server-enforced. It stops the immediate re-join fight.
It does not stop somebody determined, especially while identity is name-only.

- [ ] **A. Keep 5 minutes.** *(recommended)*
- [ ] **B. Longer — the rest of the session, or 1 hour.**
- [ ] **C. Host chooses: 5 minutes or permanent for that room.**

**Your answer:**
First is 1 minute, second is 3 minutes ,third is 5 minutes, etc + 2 minutes.
### Decision 2.6 — What happens when the host leaves? *(already decided: room closes after 2 min)*

Today the match ends and the room closes after a two-minute grace period, which
doubles as the host's chance to reconnect.

The alternative is **host migration** — someone else's PC takes over. I want to
be straight with you: **in a host-as-server design, migration means the Dota
match ends anyway.** The game is running on that person's computer. Migration
would preserve the *room* — the people and the chat — not the match.

- [ ] **A. Keep it: match ends, room closes after 2 minutes.** *(recommended)*
- [ ] **B. Room survives and passes to another player; the match still ends.**
      Nicer socially — the group stays together and can start again.
      *Cost:* moderate engineering, and it may promise more than it delivers,
      since players will read "host migration" as "the game continues".

**Your answer:**
Room Gets Closed after a 1 minute timer after either when the host leaves / timeouts / crashes. (Gameranger behaviour but more friendly)
if the match ends , nothing will happen to the room.
### Decision 2.7 — Can ordinary players spectate? *(already decided: admins only)*

Spectator seats currently exist for admins only, outside the ten playing slots.

- [ ] **A. Admins only.** *(recommended for launch)*
- [ ] **B. Anyone can spectate a room.**
      *Cost:* each spectator is real relay bandwidth, which is your dominant
      running cost. Spectating is also how people scout and grief.

**Your answer:**
Yes as i said above up to 5 observers.
---

# Part 3 — Lobby and social

The spec puts the social layer last, after the network and the control plane.
I think that ordering is right. These questions are about what "last" contains.

### Decision 3.1 — Global chat *(exists today)*

There is one lobby chat channel that everybody in the platform shares, plus a
separate chat inside each room. Both work.

- [ ] **A. Keep one global lobby chat.** *(recommended)*
      It is what makes an empty lobby feel alive, and it is already built.
      *Cost:* it needs moderation the day it has real users. See 3.4.
- [ ] **B. No global chat. Room chat only.**
      Nothing to moderate.
      *Cost:* the lobby becomes a vending machine rather than a place.
- [ ] **C. Global chat, but only for players who have played a match.**
      Cuts drive-by spam at very little cost.

**Your answer:**
Keep Global Chat.
### Decision 3.2 — Friends *(spec says yes, not built)*

- [ ] **A. Build friends after launch, once people have someone to add.**
      *(recommended)*
- [ ] **B. Build friends before launch** — needed if you chose friends-only
      rooms in 2.1.
- [ ] **C. No friends list at all.**

**Your answer:**
Go with B and build the firends system >> Add , Remove, PV Chat, Invite to Lobby, Online/Offline Status, In Game / Not In Game Status (just like game ranger features)
### Decision 3.3 — Player profiles and ratings *(spec says yes, not built)*

What should one player see about another?

- [ ] **A. Name, declared MMR, and matches played on the platform.**
      *(recommended)* Honest, cheap, and hard to weaponise.
- [ ] **B. The above plus a thumbs-up/thumbs-down reputation from teammates.**
      *Cost:* reputation systems get used as revenge. They need appeals, which
      is a support workload.
- [ ] **C. Nothing beyond a name and MMR.**

**Your answer:**
Go with A.
### Decision 3.4 — Moderation *(new — nothing exists)*

The day you have global chat and real users, you need a way to deal with people.
Currently there is no report button, no ban, no mute, and no admin tooling
beyond the reserved spectator seat.

- [ ] **A. Report button, plus admin mute and ban.** *(recommended — this is the
      minimum that lets you run a public chat)*
- [ ] **B. The above plus a word filter on chat.**
      *Cost:* filters are easy to evade and annoy real users; useful anyway as a
      first line.
- [ ] **C. Nothing at launch; handle it by hand.**
      *Cost:* viable only while you personally know most of the players.

**And a question only you can answer: who are the admins?** The role exists in
the design but no real person is named. Moderation tooling is worthless without
somebody whose job it is.

**Your answer:**
Admin/Moderators can >> Kick, Ban , Mute, Timeout, Mark Players as different statuses like (Fake MMR, Verified, Pro Player, Noob, etc.), Manager Rooms (Close, Change Host), There will be Slide Banners On the app lateron when deicidng the front, and admins can add, remove, edit banners. 
### Decision 3.5 — Language *(new — nobody has decided this, and it matters)*

**The app is entirely in English today.** Every button, every message. Your
players are in Iran.

This is not a small detail. It affects the layout of every screen, because
Persian is written right-to-left, and retrofitting that later is genuinely
expensive.

- [ ] **A. Persian only.** *(recommended)*
      It is who the product is for. One language to write, test and maintain.
      Right-to-left layout designed in from the start rather than bolted on.
- [ ] **B. Persian and English, player chooses.**
      Helps if you later serve Iranians abroad.
      *Cost:* every piece of text written twice, forever, and both layouts
      tested.
- [ ] **C. English only.**
      Nothing to do.
      *Cost:* a meaningful fraction of your audience will find it harder to use,
      and it signals the product is not really for them.

**My recommendation: A, decided now rather than later.** If you want English
too, say so now — supporting both is much cheaper to design in than to add.

**Your answer:**
we go with English Only First, when the app is lunched and succesfull we add persian later.
### Decision 3.6 — What does the lobby show first? *(new)*

Today it is one flat list of rooms in creation order.

- [ ] **A. Rooms that are open and have space, most full first.** *(recommended
      — a room with 8 of 10 players is the one most likely to actually start)*
- [ ] **B. Newest rooms first.**
- [ ] **C. Rooms nearest your MMR first.**

**Your answer:**
Lobby has:
1- List of rooms with : Host Name, Room Description, Minimum MMR, Room Count players , Room Status (in Game, not in game), Ping
2- Friends List on the right side of the app. (Current Lobby chat positon)
3- Lobby Chat / Friends Chat (a button to show and unshow just like the dota 2 main menu chat, which is at the middle below, the same system that has the Party, lobby, friends chat in dota 2, i want it here, that has tabs to switch)
4- Profile (Top Right as icon)
5- Always visible Toolbar on left side which has: Lobby , Room , Tournoments, Profile, palyer Connection status
6- Rooms Filter and Search
7- Top Banners and ADS section
---

# Part 4 — The app, its interface and how it feels

Today the app opens a page in your web browser. That was a deliberate shortcut
for testing, and it should not ship that way.

### Decision 4.1 — What the app actually is *(spec says Tauri)*

- [ ] **A. A proper desktop window (Tauri), as the spec says.** *(recommended)*
      Looks and behaves like an application: its own window, its own icon, no
      browser address bar, no risk of the player closing the wrong tab.
      Small download. Same web technology inside, so nothing built so far is
      wasted.
- [ ] **B. Keep opening a browser tab.**
      Zero work.
      *Cost:* it does not feel like a product. Players will close the tab and
      think they closed the app. A browser update can change how it looks.
- [ ] **C. Electron instead of Tauri.**
      More familiar to more developers.
      *Cost:* roughly 100 MB heavier per download, on connections where that
      matters.

**My recommendation: A.** This is the single biggest change in how the product
*feels*, and the work is modest because the screens already exist.

**Your answer:**
Go with A.
### Decision 4.2 — Visual direction *(new — pick a lane and I will design to it)*

The test harness is dark with green and red status colours. That was chosen for
legibility while debugging, not as a brand.

- [ ] **A. Dark, gaming-native, close to what Dota players expect.**
      *(recommended)* Familiar, hides the fact that this is a small product, and
      is what the audience already lives in.
- [ ] **B. Clean and light, closer to a normal desktop application.**
      Feels trustworthy and calm; unusual for a game launcher.
- [ ] **C. Strongly branded around Final Lobby as its own identity** — a logo, a
      colour, a personality of its own.
      *Cost:* needs a designer and a name decision (4.5).

**Your answer:**
Go wtih A.
### Decision 4.3 — What a new player does, in order *(new)*

I need you to confirm the first two minutes. My proposal:

> Download from the link → run the installer → accept the terms → **create an
> account** → set a name and MMR → land in the lobby → join a room → **you are
> on the room's network automatically** → press Launch Dota 2.

- [ ] **A. As above.** *(recommended)*
- [ ] **B. Let people browse the lobby before creating an account**, and only
      ask when they try to join a room.
      Lower barrier — they see the place is alive before committing.
      *Cost:* more to build, and anonymous browsers still cost server capacity.
- [ ] **C. Something else — describe:**

**Your answer:**
Go with B.
### Decision 4.4 — Does the app stay out of the way? *(new)*

While waiting for a room to fill, the player is sitting in front of our window
doing nothing.

- [ ] **A. Minimise to the system tray, and notify when the room fills or the
      host starts.** *(recommended)* This is what GameRanger did and it is why
      people left it running.
- [ ] **B. Ordinary window, no tray, no notifications.**
- [ ] **C. Tray, but no notifications.**

**Your answer:**
Go with A.
### Decision 4.5 — The name *(new)*

"Final Lobby" is the working name in the code and the installer. Nothing depends
on it staying — but the longer it stays, the more places it appears.

- [ ] **A. Keep Final Lobby.**
- [ ] **B. Change it to:**

**Your answer:**
Change it to: LobbyBaz
---

# Part 5 — Open questions I owe you

These are mine, not yours — except where marked. I am listing them so nothing
sits in my head unrecorded.

### 5.1 Can a stranger take over an abandoned slot mid-match? — **blocks a design you already chose**

Your dynamic-room rule says a host can reopen a locked room so an abandoned slot
can be refilled. **Nobody knows whether Dota allows it.** Reconnecting your *own*
dropped player works. A *different* person taking the slot is unverified, and
neither ancestor project tested it.

If the answer is no, the reopen-for-a-replacement flow needs a different shape,
and we should find out before building around it.

**Needs:** twenty minutes with both PCs and a person at each. I can guide it.

### 5.2 Real bandwidth in a full game — **your costs depend on this**

Measured on 2026-08-23 with two players: 115 kbps out and 42 kbps in per client.
Extrapolated to ten that is roughly 1.04 Mbps out for the host, against the
1.2 Mbps the capacity model assumes. **That supports the model without
confirming it** — a two-player game is much lighter than a full one.

Every server cost figure you have rests on this number.

**Needs:** one game with more than two players.

### 5.3 Uplink port speed — **only you can get this**

The server reports its network speed as unknown, and the provider has never
confirmed it. Our measured 500-player load is 47.8 Mbps each way, and real Dota
packets could push that towards 100 Mbps.

**Needs:** you or someone at MobinHost confirming the provisioned port speed.
This has been open since 18 August. It is the last unknown in the capacity plan.

### 5.4 Wintun redistribution licence — **before any public installer**

We embed a third-party network driver (Wintun, by the WireGuard authors) inside
our installer. The signature is verified and the version is pinned, but **nobody
has confirmed we are allowed to redistribute it** in a product we ship publicly.

**Needs:** ten minutes reading their licence, before the first public download.
Low risk, but it is the kind of thing that is embarrassing to discover late.

### 5.5 A refused connection says nothing useful — **engineering, mine to fix**

When the relay refuses a connection it stays silent, so the app can only report
"the tunnel did not come up". That is what turned tonight's ticket bug into an
hour of investigation instead of a minute. The relay should say why, and the app
should repeat it.

**Needs:** a protocol change. Worth doing before real players, not urgent
tonight.

### 5.6 The dedicated server — **your call on timing**

Final Lobby currently shares a box with your `nati-filter` business. There is no
port conflict and nothing we run touches their configuration — I have verified
this repeatedly. But two real risks remain that no amount of care removes:
we compete for one uplink, and we share one IP address, so filtering aimed at
either of us hits both.

The spec says a dedicated server before public launch. **I still recommend
that.** The question is when you buy it.

### 5.7 Race-condition testing — **engineering, mine**

Our concurrency tests cannot run on this Windows PC because they need a C
compiler. They should run on the Linux server. Not yet scripted.

---

# Summary — what I need from you

**Blocking real players:**
1.1 account type · 1.2 password recovery · 1.4 who writes the terms

**Shapes what gets built next, needed soon:**
3.5 language · 4.1 desktop app or browser · 2.1 room privacy

**Needed from you, not from me:**
5.3 uplink port speed from MobinHost · 3.4 who the admins are · 5.6 when to buy
the dedicated server

**Everything else can wait** until the three above are answered.

---

*Answer in this file directly, or tell me your choices and I will record them in
`docs/decisions.md` with the reasoning, the way every other decision on this
project is recorded.*
