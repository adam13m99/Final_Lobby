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

Everything needed is in the bundle. Build it on the development machine with:

```bash
./scripts/bundle.sh
```

That produces `dist/FinalLobby-test/` containing two files and a `setup.txt`
with the real addresses and token. **Copy that whole folder to PC B** — on a
USB stick, over your home network, however is easiest.

There is no installer and no driver to download. The network driver is built
into `netservice.exe` and writes itself out on first run.

### Step 1 — Install the service (once, needs Administrator)

On PC B, right-click PowerShell → **Run as Administrator**, then:

```powershell
cd <the folder you copied>
.\netservice.exe install
```

Expected: `Final Lobby network service installed and started.`

This is **the only time PC B needs Administrator**. Creating a virtual network
adapter is a privileged operation; doing it once at install is what lets a
player join a room later with no UAC prompt at all.

Check it took:

```powershell
Get-Service FinalLobbyNet
```

Expected: `Running`.

- [ ] Service installed and running
- **Observed:**

### Step 2 — Configure the client (normal window, no Administrator)

Close the admin window. Open a **normal** PowerShell:

```powershell
cd <the folder you copied>
.\lobbycli.exe setup -coordinator http://87.107.110.199:7001 `
    -token <token from setup.txt> -player bob -nick Bob
```

**Use a different `-player` name from PC A.** Two players cannot share an
identity.

Expected: `Saved. You are "bob" talking to http://87.107.110.199:7001`

If instead it says the coordinator is not answering, PC B cannot reach the
server — check its internet connection before going further. Nothing below
will work until this line succeeds.

- [ ] Client configured, coordinator reachable
- **Observed:**

### Step 3 — Confirm both PCs see the same world

On **both** PCs:

```powershell
.\lobbycli.exe rooms
```

Both should print the same list of rooms.

- [ ] Both PCs list the same rooms
- **Observed:**

---

## Part 2 — The acceptance cases

Run these in order. Each builds on the last.

### 1. No UAC prompt when joining

After the one-time install in Step 1, neither PC should ever show a
Windows permission prompt again — not on connect, not on launch.

- [ ] No UAC prompt appeared on either PC during the whole test
- **Observed:**

### 2. A room, and two addresses in the same /28

On **PC A**:

```powershell
.\lobbycli.exe create -name "Acceptance Test"
```

Note the room ID it prints. On **PC B**:

```powershell
.\lobbycli.exe join <room-id>
```

Both should report addresses in the same `10.87.x.x/28`, and both should
agree on the same host address.

- [ ] Both players are in one `/28`, and agree who the host is
- **PC A address:**
- **PC B address:**
- **Subnet:**

### 3. The tunnel comes up on both

On **both** PCs:

```powershell
.\lobbycli.exe connect
```

Expected within a few seconds: `Connected. You are 10.87.x.x in room ...`

Then on both:

```powershell
.\lobbycli.exe status
```

Expected: `tunnel connected`, an adapter named `Final Lobby`, your address.

- [ ] Both tunnels connected
- **Observed:**

### 4. They can actually reach each other

From **PC B**, ping PC A's virtual address:

```powershell
ping <PC A's address>
```

This is the first moment the two machines genuinely talk through the relay.
**Record the latency** — it is the number that decides whether the game feels
good.

- [ ] PC B can reach PC A's virtual address
- **Latency (min/avg/max):**
- **Packet loss:**

If this fails, stop and note it. Nothing after this can work, and the fault
is in the network core rather than in Dota.

### 5. A real Dota 2 match — the actual gate

On **PC A** (the host):

```powershell
.\lobbycli.exe play -mode 1
```

Dota 2 should start and load a lobby. Wait until the map has loaded.

Then on **PC B**:

```powershell
.\lobbycli.exe play -mode 1
```

Dota 2 on PC B should connect to PC A's game.

- [ ] **A real Dota 2 match started with both players in it**
- **Observed (what each screen showed, how long it took):**
- **In-game ping shown for PC B:**
- **Host's own ping (expected 0 — the host runs the match):**

This case is the whole point. If it fails, record exactly what Dota showed —
the error text matters more than anything else in this document.

### 6. Measure the real bandwidth

While the match is running, watch the network use on both PCs — Task Manager
→ Performance → the `Final Lobby` adapter, or Resource Monitor.

**Record it.** Every capacity and cost estimate we have rests on an assumption
of roughly 1.2 Mbps in and out per ten-player game; this is where we find out
whether that is right.

- **PC A (host) sent / received:**
- **PC B (client) sent / received:**
- **Number of players in the match:**

### 7. Confirm the readiness markers

The service watches Dota's `console.log` to know when the host's server is
up, looking for `Server started` or `Host_NewGame`. **Those strings were never
verified against the current Dota build.**

On PC A, open:

```
C:\Program Files (x86)\Steam\steamapps\common\dota 2 beta\game\dota\console.log
```

Search for both strings.

- [ ] At least one marker appears when the host's match starts
- **Which marker(s) appeared, exact text:**
- **If neither: what line does mark the server coming up?**

### 8. Open question — can a stranger take an abandoned slot?

**This is the unverified behaviour the whole dynamic-room design depends on,
and nobody knows the answer.**

With the match running, on **PC B**: leave the game (abandon the slot).

On **PC A**:

```powershell
.\lobbycli.exe open
```

Now have PC B join as a *different* player:

```powershell
.\lobbycli.exe setup -player carol -nick Carol
.\lobbycli.exe join <room-id>
.\lobbycli.exe connect
.\lobbycli.exe play -mode 1
```

- [ ] **Can a different account take over the abandoned slot in a running match?**
- **Answer (yes / no):**
- **What Dota did:**

Either answer is fine. A "no" means the product rules need adjusting, and it
is far better to learn it now than after launch.

### 9. Locked rooms refuse newcomers

On **PC A**:

```powershell
.\lobbycli.exe lock
```

On **PC B**, try to join the room again.

Expected: refused, with a message about the room being locked and in game.

- [ ] A locked room refuses a join
- **Exact message shown:**

### 10. Kick, and the 5-minute block

On **PC A**:

```powershell
.\lobbycli.exe kick bob
```

Immediately on **PC B**: check `status`, then try to rejoin.

Expected: the tunnel drops within about 30 seconds without PC B doing
anything, and rejoining is refused.

Wait 5 minutes and try again. Expected: allowed.

- [ ] Kicked player loses the tunnel on their own
- **How long until the tunnel dropped:**
- [ ] Rejoin refused inside 5 minutes
- [ ] Rejoin allowed after 5 minutes
- **Observed:**

### 11. Surviving a network drop

With both connected, **unplug PC B's network cable (or turn off its Wi-Fi)
for 20 seconds**, then reconnect it.

Expected: the tunnel comes back **with the same virtual address**, without
anyone running a command, and Dota reconnects on its own.

- [ ] Tunnel restored automatically
- [ ] **Same virtual address as before**
- [ ] Dota reconnected without a manual rejoin
- **How long the whole recovery took:**
- **Observed:**

### 12. Leaving really removes access

On **PC B**:

```powershell
.\lobbycli.exe leave
```

Then try to ping PC A's virtual address again.

Expected: no reply at all. The adapter should be gone from PC B's network
settings.

- [ ] Ping fails after leaving
- [ ] The `Final Lobby` adapter is gone
- **Observed:**

### 13. Host leaves — the room closes after 2 minutes

On **PC A**:

```powershell
.\lobbycli.exe leave
```

From PC B, run `.\lobbycli.exe rooms` immediately, then again after 2 minutes.

- [ ] The room disappears roughly 2 minutes after the host leaves
- **Observed:**

### 14. A voluntary leaver can return

Have PC B rejoin a room it left voluntarily (not one it was kicked from).

- [ ] Immediate rejoin allowed
- **Observed:**

---

## If something goes wrong

**`lobbycli` says the service is not running.**
On that PC: `Get-Service FinalLobbyNet`. If it is stopped,
`Start-Service FinalLobbyNet` from an Administrator window. If it is not
installed, redo Part 1 Step 1.

**`connect` fails or times out.**
Check the relay directly, which bypasses everything else:

```powershell
.\lobbycli.exe probe
```

If the probe succeeds but `connect` does not, the problem is the adapter, not
the network. If the probe also fails, that PC cannot reach the relay.

**Dota starts but the client cannot find the host's game.**
Check that PC B can ping PC A's virtual address (case 4). If the ping works
but Dota does not connect, the fault is in how Dota is launched, not the
network — record the exact address `lobbycli status` shows as the host, and
what PC B's Dota reports.

**Nothing works on one PC and everything works on the other.**
Run `.\lobbycli.exe status` on both and record both outputs side by side. The
difference is the answer.

**Collecting logs.** The service logs to the Windows Event Log under
`FinalLobbyNet`. From an Administrator window:

```powershell
Get-EventLog -LogName Application -Source FinalLobbyNet -Newest 40
```

For a run started manually with `.\netservice.exe run`, the log is simply
whatever it printed in that window.

---

## Result

- **Did a real Dota 2 match run between the two PCs?**
- **Answer to the open question in case 8:**
- **Measured per-player bandwidth:**
- **What broke, and what we are doing about it:**
