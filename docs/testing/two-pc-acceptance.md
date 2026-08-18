# Two-PC acceptance test

**This is the gate for the whole network core.** Nothing in sub-project 2
starts until a real Dota 2 match has run between two physical PCs over our
relay.

Fill in what actually happened, including failures. A failure recorded here
is worth more than a tick — it is the only way we find out what a real player
will hit. Write the observation, not a verdict.

- **Date run:**
- **PC A (host):**
- **PC B (client):**
- **Run by:**

---

## Part 1 — Setting up the second PC

Build the bundle on the development machine:

```bash
./scripts/bundle.sh
```

That produces `dist/FinalLobby-test/` — one folder, five files. **Copy the
whole folder to PC B** on a USB stick or over your home network.

There is no driver to download and nothing to fetch from the internet. The
virtual network driver is built into the program and appears on first run.

### Step 1 — Run the installer (needs Administrator, once)

On PC B, right-click **`install.ps1`** and choose **Run with PowerShell**.
Say yes if Windows asks for permission.

If right-click does not offer that, open PowerShell as Administrator and run:

```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1
```

It should finish with `Setup finished.` and a **Final Lobby** shortcut on the
desktop. It copies the program into your user folder, registers the
background network service, and opens the firewall for hosting.

**This is the only time PC B needs Administrator.** Creating a virtual
network adapter is privileged; doing it once here is what lets a player join
a room later with no permission prompt at all.

- [ ] Installer finished without errors
- [ ] Desktop shortcut created
- **Observed:**

### Step 2 — Open the app and set it up

Double-click **Final Lobby** on the desktop. A black window opens and stays
open — that is the app itself, so leave it alone — and your browser opens on
the setup screen.

Fill in the **server address** and **access code** from `setup.txt`, and pick
a **player name**. Use a different player name from PC A.

- [ ] Setup screen accepted the details
- [ ] The room list appeared afterwards
- **Observed:**

If it says the server is not answering, PC B cannot reach the server. Fix
that before going further; nothing below will work.

### Step 3 — Confirm both PCs see the same world

Both PCs should show the same rooms, and both should show **service running**
in the top right.

- [ ] Both PCs list the same rooms
- [ ] Both show "service running"
- **Observed:**

---

## Part 2 — The acceptance cases

Run these in order; each builds on the last. Everything is done in the app
unless a case says otherwise.

### 1. No permission prompts after setup

After the one-time installer, neither PC should ever show a Windows
permission prompt again — not on Connect, not on Launch.

- [ ] No UAC prompt appeared on either PC during the whole test
- **Observed:**

### 2. A room, and two addresses in the same /28

On **PC A**: type a room name and click **Create room**.

On **PC B**: the room appears in the list. Click **Join**.

Both now show *Your address* and *Host address*. They should differ, sit in
the same `10.87.x.x/28`, and both PCs should name the same host address.

- [ ] Both players are in one `/28`, and agree who the host is
- **PC A address:**
- **PC B address:**

### 3. The tunnel comes up on both

On both PCs click **Connect**. Within a few seconds the pill at the top right
should read **tunnel connected**, and *Adapter* should show `Final Lobby`.

- [ ] Both tunnels connected
- **How long each took:**

### 4. They can actually reach each other

On **PC B**, open a normal PowerShell window and ping PC A's address:

```powershell
ping <PC A's address>
```

This is the first moment the two machines genuinely talk through the relay.
**Record the latency** — it decides whether the game feels good.

- [ ] PC B can reach PC A's virtual address
- **Latency (min/avg/max):**
- **Packet loss:**

If this fails, stop and record it. Nothing after it can work, and the fault
is in our network rather than in Dota.

### 5. A real Dota 2 match — the actual gate

On **PC A**: pick a game mode and click **Launch Dota 2**. Wait until the map
has finished loading — about 20 seconds on the development machine.

Then on **PC B**: click **Launch Dota 2**. It should connect to PC A's game.

- [ ] **A real Dota 2 match started with both players in it**
- **Observed (what each screen showed, how long it took):**
- **In-game ping shown for PC B:**
- **Host's own ping (expected 0 — the host runs the match):**

This case is the whole point. If it fails, record exactly what Dota showed:
the error text matters more than anything else in this document.

### 6. Measure the real bandwidth

While the match runs, watch network use on both PCs — Task Manager →
Performance → the `Final Lobby` adapter.

**Record it.** Every capacity and cost figure rests on roughly 1.2 Mbps each
way per ten-player game. Two players will use far less; what matters is the
per-player rate, which we can multiply up.

- **PC A (host) sent / received:**
- **PC B (client) sent / received:**

### 7. Readiness markers — already answered, confirm they hold

Resolved on 2026-08-19 by launching a real host game on the development PC.
The plan's guesses (`Server started`, `Host_NewGame`) appear **nowhere** in a
real log. The real sequence is:

```
[Networking] Network socket 'server' opened on port 27015
[Server] SV:  Spawn Server: dota
[Server] CNetworkGameServerBase::SetServerState (ss_loading -> ss_active)
```

The code now looks for `Spawn Server: dota` followed by `ss_loading ->
ss_active`. On PC A, open:

```
C:\Program Files (x86)\Steam\steamapps\common\dota 2 beta\game\dota\console.log
```

- [ ] Both lines appear when the host's match starts
- **If the strings differ on this build, record them exactly:**

### 8. Open question — can a stranger take an abandoned slot?

**Nobody knows the answer, and the dynamic-room design depends on it.**

With the match running, on **PC B**: leave the Dota match, abandoning the
slot. In the app, click **Leave room**.

On **PC A**: click **Reopen for a replacement**.

On **PC B**: close the app, delete `%APPDATA%\FinalLobby\lobbycli.json` so it
forgets who you are, reopen the app, and set up as a *different* player name.
Then join the room, connect, and launch.

- [ ] **Can a different account take over the abandoned slot in a running match?**
- **Answer (yes / no):**
- **What Dota did:**

Either answer is useful. A "no" means the product rules need adjusting, and
far better to learn it now than after launch.

### 9. Locked rooms refuse newcomers

On **PC A**: click **Lock — match starting**.

On **PC B**: the room should show as locked and offer no Join button.

- [ ] A locked room cannot be joined
- **Observed:**

### 10. Kick, and the 5-minute block

With PC B in the room, on **PC A** click **Kick** next to their name.

On **PC B**: watch the tunnel pill. It should drop **on its own** within
about 30 seconds — nobody touches PC B.

Then try to rejoin. Expected: refused. Wait 5 minutes and try again:
allowed.

- [ ] Kicked player loses the tunnel without doing anything
- **How long until the tunnel dropped:**
- [ ] Rejoin refused inside 5 minutes
- [ ] Rejoin allowed after 5 minutes
- **Observed:**

### 11. Surviving a network drop

With both connected, **unplug PC B's network cable, or turn off its Wi-Fi,
for 20 seconds**, then restore it.

Expected: the tunnel returns **with the same address**, without anyone
clicking anything, and Dota reconnects on its own.

- [ ] Tunnel restored automatically
- [ ] **Same virtual address as before**
- [ ] Dota reconnected without a manual rejoin
- **How long the whole recovery took:**
- **Observed:**

### 12. Leaving really removes access

On **PC B**: click **Leave room**. Then ping PC A's address again.

Expected: no reply, and the `Final Lobby` adapter gone from Windows' network
connections.

- [ ] Ping fails after leaving
- [ ] The adapter is gone
- **Observed:**

### 13. Host leaves — the room closes after 2 minutes

On **PC A**: click **Leave room**.

On **PC B**: watch the room list. The room should disappear about 2 minutes
later.

- [ ] Room closes roughly 2 minutes after the host leaves
- **Observed:**

### 14. A voluntary leaver can return

Have PC B rejoin a room it left voluntarily — not one it was kicked from.

- [ ] Immediate rejoin allowed
- **Observed:**

---

## If something goes wrong

**The app says "service not running".**
Open PowerShell as Administrator and run `Start-Service FinalLobbyNet`. If it
says there is no such service, run `install.ps1` again.

**Connect fails or times out.**
Use the diagnostic tool, which tests the relay alone with everything else out
of the way. From `%LOCALAPPDATA%\FinalLobby`:

```powershell
.\lobbycli.exe probe
```

If the probe succeeds but Connect does not, the problem is the adapter rather
than the network. If the probe also fails, that PC cannot reach the relay at
all.

**Dota starts but the client cannot find the host's game.**
Check case 4 first — can PC B ping PC A's address? If the ping works and Dota
still cannot connect, the fault is in how Dota is launched, not the network.
Record the host address the app shows and what PC B's Dota reports.

**Everything works on one PC and nothing on the other.**
Run `.\lobbycli.exe status` on both and record both outputs side by side. The
difference is the answer.

**Collecting logs.** The service writes to the Windows Event Log:

```powershell
Get-EventLog -LogName Application -Source FinalLobbyNet -Newest 40
```

The app prints to its own black window; leave it open and read it there.

---

## What has already been verified on one PC

So the two-PC session is spent on what genuinely needs two machines, these
were confirmed on the development machine on 2026-08-19:

- The installer, service registration, firewall rule and desktop shortcut
- An unprivileged user creating the virtual adapter with **no UAC prompt**
- Creating a room, connecting the tunnel, and the app's whole UI flow
- **Dota 2 launching and its listen server reaching `ss_active` in ~17s**
- The readiness markers in `console.log` (case 7)
- Room isolation on the client: Windows installs a route for the room's own
  `/28` and nothing else
- Kick revoking the ticket, so the relay refuses the kicked player

What genuinely needs two machines: cases 4, 5, 6, 8, 10, 11.

---

## Result

- **Did a real Dota 2 match run between the two PCs?**
- **Answer to the open question in case 8:**
- **Measured per-player bandwidth:**
- **What broke, and what we are doing about it:**
