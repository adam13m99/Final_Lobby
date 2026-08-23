# Two-PC acceptance test

**This is the gate for the whole network core.** Nothing in sub-project 2
starts until a real Dota 2 match has run between two physical PCs over our
relay.

- **Date run:** 2026-08-23
- **PC A (host):** PC1, virtual address 10.87.0.2
- **PC B (client):** PC2, virtual address 10.87.0.3
- **Run by:** the owner, one person at each machine
- **Build:** `2026.08.23-1739`

**Result: a real Dota 2 match ran between two physical PCs over the relay.**
The gate for the whole network core is passed.

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

- [x] **A real Dota 2 match started with both players in it** — 2026-08-23

Done by hand rather than with the **Launch Dota 2** button: the host started
Dota, ran `map dota gamemode 2` at the console, and PC2 joined with
`connect 10.87.0.2`. Both players were in the same match and played.

Relay counters during the match, which is the independent confirmation that
the traffic really crossed our own network rather than some other path:

```
peers=2  forwarded=13162  dropped_queue=0  write_errors=0  auth_failed=0
```

- **In-game ping shown for PC B:** not recorded
- **Host's own ping:** not recorded

**The first attempt failed, and the reason matters more than the success.**
Nobody had connected the tunnel. Both players were in the room, the host
launched Dota and hosted a game, and PC2's `connect 10.87.0.2` went nowhere
because no machine held that address — the virtual adapter did not exist on
either PC. Nothing on either screen said so. See "Connect is a step people do
not know exists" below.

### 2. Real bandwidth

While the match runs, watch Task Manager → Performance → the **LobbyBaz**
adapter on both PCs.

Every capacity and cost figure we have rests on roughly 1.2 Mbps each way per
ten-player game. Two players will use far less; what matters is the
per-player rate, which we can multiply up.

Measured on PC A's **LobbyBaz** adapter during the match, over a 10-second
sample, with **two** players connected:

| Direction | Rate |
|---|---|
| PC A sent (host to one client) | **115 kbps** |
| PC A received (one client to host) | **42 kbps** |

**Read this as a floor, not an answer.** A two-player game carries far fewer
entities than a ten-player one, so the per-client rate will rise. Taking the
per-client figures at face value, a full game costs the host roughly
9 x 115 kbps = **1.04 Mbps out** and 9 x 42 kbps = **378 kbps in**, against
the 1.2 Mbps each way the capacity model assumes. The model is the right
order of magnitude; it is not yet confirmed at ten players.

- **Still to measure:** the same numbers in a game with more than two players.

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
`Start-Service LobbyBazNet`. If there is no such service, run the
installer again from the link.

**Connect fails, and the app says the tunnel did not come up.** This was
the expiring-ticket bug (D36), fixed in build `2026.08.23-1739`. If it
happens on a build at or after that one, it is something new — say so, and
get the relay's own view before guessing:

```bash
journalctl -u relay -n 5 --no-pager
```

`handshake_rejected` climbing as you press Connect means the relay is
refusing the ticket rather than the packets going missing.

**Connect fails.** From `C:\Program Files\LobbyBaz` run
`.\lobbycli.exe probe`. It tests the relay alone with everything else out of
the way. If the probe succeeds but Connect does not, the fault is the
adapter rather than the network.

**Dota starts but cannot find the host's game.** Check the diagnostics first
— can PC B reach the host? If it can and Dota still cannot connect, the
fault is in how Dota is launched, not the network. Record the host address
the app shows and what PC B's Dota says.

**Logs.** The service writes to the Windows Event Log:

```powershell
Get-EventLog -LogName Application -Source LobbyBazNet -Newest 40
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

## Connect is a step people do not know exists

The single biggest finding of the session, and it is a design fault rather
than a mistake by whoever was testing.

Being in a room and being on the room's network are two different states, and
only the second one carries any traffic. The app never says so. The room
screen shows a small "tunnel off" pill, which is not a thing anyone reads
while arranging a match with somebody on the phone.

The **Launch Dota 2** button is correctly greyed out until the tunnel is up,
so people who use it are safe. Anyone who starts Dota themselves — which is
what a Dota player naturally does — gets no warning at all, and the failure
surfaces minutes later as a connection error inside Dota, three layers away
from its cause.

Left as it is, every new player loses their first evening to this.

## Result

- **Did a real Dota 2 match run between the two PCs?** **Yes** — 2026-08-23,
  build `2026.08.23-1739`, confirmed independently by the relay forwarding
  13,162 packets between two peers with no drops and no write errors.
- **Answer to the open question in case 3:** not yet run.
- **Measured per-player bandwidth:** 115 kbps out / 42 kbps in per client at
  two players; see the caveat in section 2.
- **What broke, and what we are doing about it:**
  1. Tickets expired while players waited in the room, so Connect could never
     succeed. Fixed the same day — D36, build `2026.08.23-1739`.
  2. Nothing tells a player they must press Connect. Open; see above.
  3. A relay that refuses a handshake stays silent, so the app can only
     report a timeout. Open; see D36.
