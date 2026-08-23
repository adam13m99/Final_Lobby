# Two-PC acceptance test

**This is the gate for the whole network core.** Nothing in sub-project 2
starts until a real Dota 2 match has run between two physical PCs over our
relay.

- **Date run:**
- **PC A (host):**
- **PC B (client):**
- **Run by:**

---

## Setting up, on each PC

Open this link and press the button:

    http://87.107.110.199:7001/d/<key>/

The key is printed by `./scripts/publish.sh`. It is not in this file on
purpose, so a copy of the repository does not hand out the download.

Run the file that downloads. Say yes to the one Windows permission prompt.
Pick a name — use a different one on each PC. That is the whole setup:
nothing to copy, no address to type, no access code to paste.

- [ ] Both PCs installed and showing the lobby
- [ ] Both show **service running** in the top right
- [ ] Both list the same rooms
- **Observed:**

If a newer build has been published since you installed, the app says so and
offers it. Take it — otherwise the two machines are running different code
and any difference between them means nothing.

---

## The machine checks itself

**Do this before anything else, on both PCs.** Open **Diagnostics** and press
**Run checks**. It tests the server, the service, Dota, the tunnel and
whether the other player is reachable, and sends the results to the server.

Run it again after both PCs have joined a room and connected — that is when
the last two checks have something to measure.

The results can be read from the development machine, so nobody has to read
numbers down a telephone:

```bash
curl -s -H "Authorization: Bearer $TOKEN" http://87.107.110.199:7001/v1/diag
```

- [ ] All checks pass on PC A
- [ ] All checks pass on PC B
- **Anything that failed, and what it said:**

Everything below assumes these passed. If they did not, fix that first —
nothing further will work and the failure is more informative than any of it.

---

## What still needs two people

These are the things no check can answer.

### 1. A real Dota 2 match — the actual gate

On **PC A**: create a room, press **Connect**, pick a game mode, press
**Launch Dota 2**. Wait for the map to finish loading — about 20 seconds on
the development machine.

On **PC B**: join that room, press **Connect**, then **Launch Dota 2**.

- [ ] **A real Dota 2 match started with both players in it**
- **What each screen showed, and how long it took:**
- **In-game ping shown for PC B:**
- **Host's own ping (expected 0 — the host runs the match):**

If it fails, record exactly what Dota said. The error text matters more than
anything else in this document.

### 2. Real bandwidth

While the match runs, watch Task Manager → Performance → the **Final Lobby**
adapter on both PCs.

Every capacity and cost figure we have rests on roughly 1.2 Mbps each way per
ten-player game. Two players will use far less; what matters is the
per-player rate, which we can multiply up.

- **PC A (host) sent / received:**
- **PC B (client) sent / received:**

### 3. Open question — can a stranger take an abandoned slot?

**Nobody knows the answer, and the dynamic-room design depends on it.**

With the match running, on **PC B**: leave the Dota match, abandoning the
slot. In the app, press **Leave room**.

On **PC A**: press **Reopen for a replacement**.

On **PC B**: the room should reappear in the lobby marked *Needs a player*.
Join it, connect, and launch.

- [ ] **Can a player rejoin an abandoned slot in a running match?**
- **Answer (yes / no):**
- **What Dota did:**

Either answer is useful. A "no" means the product rules need adjusting, and
far better to learn it now than after launch.

### 4. Kick, and the 5-minute block

With PC B in the room, on **PC A** press **Kick** next to their name.

On **PC B**: nobody touches anything. Within about 30 seconds the app should
say they are no longer in the room and the tunnel pill should go out on its
own.

Then try to rejoin. Expected: refused. Wait 5 minutes: allowed.

- [ ] Kicked player loses the room and the tunnel without doing anything
- **How long that took:**
- [ ] Rejoin refused inside 5 minutes
- [ ] Rejoin allowed after 5 minutes

**Known hole, accepted for this test:** identity is a name and a local ID
with no password, so a kicked player who reinstalls comes back as somebody
new. Do not test that; we already know. It must be closed before real
players (see D31).

### 5. Surviving a network drop

With both connected, **unplug PC B's network cable, or turn off its Wi-Fi,
for 20 seconds**, then restore it.

Expected: the tunnel returns with the same address, without anyone pressing
anything, and Dota reconnects on its own.

- [ ] Tunnel restored automatically
- [ ] **Same virtual address as before**
- [ ] Dota reconnected without a manual rejoin
- **How long the whole recovery took:**

### 6. Chat, and the two screens

Small, but it is what makes the thing usable and it has never been tried by
two people at once.

- [ ] Lobby chat reaches the other PC
- [ ] Room chat reaches the other PC and stays out of the lobby
- [ ] A player inside a room can go back to the Lobby screen and return
      without leaving the room
- [ ] Joins, kicks and lock changes announce themselves in the room
- **Observed:**

---

## If something goes wrong

**"Service not running".** Open PowerShell as Administrator and run
`Start-Service FinalLobbyNet`. If there is no such service, run the
installer again from the link.

**Connect fails.** From `C:\Program Files\Final Lobby` run
`.\lobbycli.exe probe`. It tests the relay alone with everything else out of
the way. If the probe succeeds but Connect does not, the fault is the
adapter rather than the network.

**Dota starts but cannot find the host's game.** Check the diagnostics first
— can PC B reach the host? If it can and Dota still cannot connect, the
fault is in how Dota is launched, not the network. Record the host address
the app shows and what PC B's Dota says.

**Logs.** The service writes to the Windows Event Log:

```powershell
Get-EventLog -LogName Application -Source FinalLobbyNet -Newest 40
```

The app prints to its own window; leave it open and read it there.

---

## Already verified, so the session is not spent on it

On the development machine, 2026-08-19 and 2026-08-23:

- Download link, one-file install, service registration, firewall rule,
  desktop shortcut, and the Add or Remove Programs entry
- An unprivileged user creating the virtual adapter with **no UAC prompt**
- Self-update across three consecutive published builds: detected,
  downloaded, hash-verified, installed, app came back each time
- Creating a room, connecting the tunnel (0.6 s), and the whole UI flow
- **Dota 2 launching and its listen server reaching `ss_active` in ~17 s**
- Room isolation: Windows installs a route for the room's own `/28` and
  nothing else
- Kick revoking the ticket, so the relay refuses the kicked player
- **The complete data path**: a ping from Windows through the adapter, the
  tunnel and the relay to another peer in the same room, 0% loss at 4 ms
- Rooms, chat, MMR, spectator seats and the diagnostics upload, against the
  live server

---

## Result

- **Did a real Dota 2 match run between the two PCs?**
- **Answer to the open question in case 3:**
- **Measured per-player bandwidth:**
- **What broke, and what we are doing about it:**
