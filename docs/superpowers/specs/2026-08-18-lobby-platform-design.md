# Final Lobby — Platform Design

**Date:** 2026-08-18
**Status:** Awaiting CEO review
**Supersedes:** nothing. Greenfield build informed by two ancestor projects.

---

## 1. What we are building

A GameRanger-style lobby platform for Dota 2, built for players inside Iran who
cannot reach Valve's matchmaking servers and may be limited to the domestic
network entirely.

Players open one desktop app, browse or create a room, and play a real Dota 2
match with the other people in that room. One player's PC hosts the match; our
domestic relay carries the traffic between players so that connections work at
all and route better than they would peer to peer.

The product is not a VPN and not a matchmaking service. It is a room-based
social lobby with a private game network attached to each room.

### 1.1 Ancestry

Two prior projects were reviewed in full. Both are referenced throughout.

| Project | What it got right | Why it is not the starting point |
|---|---|---|
| `dota-iran-lobby-v3` | Working, production-proven Dota 2 connection. Room-scoped packet routing. Self-contained Windows agent. | Reliable-ordered transport unsuitable for game traffic; goroutine-per-packet forwarding; browser-to-localhost bridge running as Administrator. |
| `IranLobby360` | Excellent lease, revocation and fail-closed security discipline. No-UAC service model. Honest engineering documentation. | Never launched Dota 2 once. Its entire game integration is a placeholder file of nulls. Control plane was built for months before the core was proven. |

Their common ancestor, **DotaIranConnect**, broadcast every player's discovery
traffic to every connected player. That is an O(n²) fanout and it is why the
service degraded past roughly 1500 concurrent players. Eliminating that failure
mode is a primary design goal, addressed in section 5.3.

---

## 2. Constraints

These are fixed inputs, not decisions.

- **Network:** must work with domestic Iranian connectivity only. No dependency
  on Steam, Google, Cloudflare, GitHub, STUN servers, or any international host
  at runtime.
- **Infrastructure:** one domestic VPS at launch. Unlimited traffic volume
  available; port speed to be provisioned per section 9.
- **Physical test capacity:** two Windows PCs. One host, one client. All
  real-Dota verification happens at this scale; everything larger is simulated.
- **Platform:** Windows desktop only.
- **Game:** Dota 2 only, with game-specific logic isolated so further titles are
  configuration rather than rework.

---

## 3. Product rules

Decided by the CEO. These are requirements, not proposals.

### 3.1 Rooms

- A room is created by a player, who becomes its **host**.
- Rooms may optionally have a **password**.
- Rooms may optionally be **friends-only** or **invite-only**.
- Hosts may set an **MMR minimum** or an **MMR range** for admission.
- Room state is dynamic, never a one-way trip. A room moves between
  `Open`, `Locked – In Game`, and back to `Open To New Players` at the host's
  discretion, so a slot abandoned mid-match can be refilled.
- **While a room is `Locked – In Game`, no new player may join.** Admission
  reopens only when the host explicitly switches the room to
  `Open To New Players`. Locked is the default state once a match begins.

### 3.2 Room lifecycle

| Event | Behaviour |
|---|---|
| Host leaves or crashes | Match ends. Room closes after a **2-minute timer**, which doubles as a grace period for the host to reconnect and save the room. |
| Player leaves voluntarily | May rejoin freely, subject to rate limits. |
| Player is kicked | Blocked from **that room** for **5 minutes**, enforced server-side. |
| Player disconnects unexpectedly | Slot is held and the tunnel silently restores; see section 5.6. |

### 3.3 Roles

- **Host** — controls their own room.
- **Admin** — platform staff. Holds a **reserved spectator slot in every room**,
  outside the ten playing slots, so a full match is never blocked by admin
  presence. Admins can moderate rooms individually.

### 3.4 MMR

Self-declared, with friction. A player sets their MMR and may change it only
**once per week**, with changes visible to others. No Steam verification —
it is unreachable and would be a hard international dependency. This stops
casual misrepresentation without building moderation tooling we do not yet need.

### 3.5 Social layer

Global chat, friends, invites, profiles and ratings are in scope for the product,
sequenced after the network core is proven (section 8).

---

## 4. Architecture

Four components. Each is independently testable and separately deployable.

```
   Desktop App (Tauri, unelevated)
        │  named pipe, caller identity verified
        ▼
   net-service (Windows service, Go)  ──► Wintun virtual adapter ──► Dota 2
        │  encrypted UDP :443
        ▼
   relay (Linux, Go)  ◄── internal API ──  control-plane (Linux)
                                                  │
                                          PostgreSQL + Redis
```

**Why the service, and not repo 1's model.** Repo 1 ran a localhost HTTP server
as Administrator that the web page drove. That required elevation on every use
and created a genuine remote-code-execution surface they had to patch after the
fact. Installing a service once means players never see a UAC prompt when they
join a room, which is the single most visible day-to-day experience difference.

---

## 5. Network core

This is sub-project one. It carries all the technical risk and is built first.

### 5.1 Virtual addressing

The platform owns `10.87.0.0/16`. Each active room is allocated a **`/28`**,
giving 4096 concurrent rooms of 14 usable addresses each.

Within a room's `/28`:

| Address | Purpose |
|---|---|
| `.0` | network |
| `.1` | reserved (relay/gateway) |
| `.2` | **host** — always deterministic |
| `.3` – `.11` | playing slots 2–10 |
| `.12` – `.14` | spectator / admin slots |
| `.15` | broadcast |

The host's address being deterministic is what makes section 5.3 possible.

### 5.2 Dota 2 connection

Unchanged from repo 1, which is production-proven:

- **Host:** `+name <nick> +sv_lan 1 +map dota gamemode <id> +jointeam <team>`
- **Client:** `+name <nick> +connect <host_vip>:27015 +jointeam <team>`

Host readiness is detected by tailing Dota's own `console.log`, as repo 1 does.
Launch arguments remain strictly allowlisted and validated — an unvalidated
launch path is a code-execution vector.

All game-specific values live in a versioned per-game config module so a second
title is data, not a rewrite.

### 5.3 Eliminating the broadcast failure mode

Because the control plane already knows the host's virtual IP and hands it
directly to each client, **Dota never needs LAN discovery.** Clients connect to a
known address.

Therefore the relay **forwards unicast only. Broadcast and multicast are dropped
by default.**

This is stronger than repo 1's fix. Repo 1 scoped multicast to room members,
reducing fanout from ~1500 recipients to ~9. Dropping it entirely reduces it to
zero and removes the failure class permanently rather than shrinking it.

Room-scoped multicast remains available behind a configuration flag as a safety
valve, off unless the physical test proves some Dota flow requires it.

### 5.4 Transport

Unreliable encrypted datagrams over UDP port 443.

- **Not KCP.** Repo 1 used KCP, a reliable ordered stream. A single lost packet
  head-of-line-blocks every packet behind it. Game traffic would rather drop a
  packet than wait for it; this is the wrong trade and it is structural, not
  tunable.
- **Handshake:** Noise IK (X25519 + HKDF) via an established library. The relay's
  static public key ships in the client. This also fixes repo 1's plaintext
  accept packet, which was sent unencrypted because the client had not yet built
  its decryption context.
- **Data:** ChaCha20-Poly1305 per session, sequence numbers, replay window.
- **MTU:** Wintun set to 1300 so encrypted packets never fragment.
- **No custom cryptography** anywhere.
- Transport sits behind an interface so obfuscation can be swapped if filtering
  behaviour changes.

### 5.5 Relay data plane

Single Go process, single UDP socket, no durable state — everything it knows is
pushed from the control plane, so it can be restarted or replaced freely.

Per packet: decrypt, verify, route, re-encrypt under the destination's session
key, enqueue.

Three rules, all mandatory:

1. **Anti-spoof.** The inner source IP must equal the session's assigned virtual
   IP. Anything else is dropped. Without this, any player can impersonate any
   other.
2. **Room scoping.** The destination must be in the sender's room. Cross-room
   traffic is dropped, never routed.
3. **Bounded per-peer queues.** One long-lived writer goroutine per peer reading
   a ring buffer of 256 packets, drop-oldest on overflow.

Rule 3 replaces repo 1's `go c.send(pktCopy)` per forwarded packet. That pattern
both reorders game traffic and, at 1500 players, would spawn on the order of a
million goroutines per second. Goroutine count must scale with players, not with
packet rate.

Per-peer drop counters are exported — sustained drops are the earliest signal of
a player with a failing connection, and feed player-facing connection quality.

### 5.6 Reconnect

The largest experience lever on unstable Iranian connections.

A player's virtual IP is **sticky** for their account-and-room pairing for a
120-second window. On a drop the Wintun adapter is never torn down; the service
silently re-handshakes and is reassigned the same address, so Dota's own
reconnect resumes as though nothing happened. The control plane holds the slot
for the same window.

This aligns with the 2-minute host grace period in section 3.2.

### 5.7 Revocation

Leave, kick and room-close trigger a synchronous relay revoke. Independently,
the Windows service runs a lease watchdog that **fails closed** — if it cannot
renew authorization it tears the tunnel down itself, without waiting for
instruction and without needing the desktop app to be running.

This is repo 2's strongest contribution. It means a player who left genuinely
cannot reach their former room even if their client is closed, stale or hostile.

Room isolation therefore holds in three independent places: relay routing, the
client's own routing table containing only its `/28`, and server-side lease
revocation. Any one failing does not leak traffic between rooms.

### 5.8 Rate limiting

Token buckets per account and per source IP on room creation, join, leave and
authentication. The 5-minute kick block is enforced server-side and cannot be
bypassed by reconnecting or by creating a new session.

---

## 6. Control plane

Accounts, sessions, rooms, membership, slots, admission rules, reconnect
reservations, and relay authorization.

- **Identity:** username and password, no email or SMS — both are unreliable
  domestically and would gate signup on a third party. Device-bound rotating
  session tokens, stored in Windows Credential Manager.
- **Room state machine:** explicit and auditable. `Open` → `Locked – In Game` →
  `Open To New Players` → `Closed`, with the transitions in section 3.2.
- **Relay authority:** the control plane is the only thing that grants network
  access. The relay never decides membership; it enforces what it is told.

---

## 7. Desktop client

Tauri, running unelevated. Lobby browser, room view, chat, Dota launch, and
auto-update. Talks to `net-service` over a named pipe with caller-identity
verification.

Auto-update is required — a platform serving a community that cannot easily
reach international download sites must be able to fix itself. Updates are
signature-verified and deferred while the player is in a room.

**Terms and conditions.** The installer presents terms which the user must
accept before installation proceeds. Acceptance is recorded with the account on
first login (version of terms, timestamp) so consent is auditable and can be
re-prompted when terms change. The installer ships **unsigned** for now
(section 11), so the terms screen is also where we set expectations about the
Windows SmartScreen warning players will see.

---

## 8. Sequencing

Ordered by risk, highest first.

1. **Network core, plus a minimal room harness.** Wintun adapter, transport,
   relay, addressing, room routing, reconnect, revocation — *and* the smallest
   possible lobby needed to exercise it: create a room, join a room, see who is
   in it, launch. The network layer cannot be tested without somewhere to put
   two players, so this harness is part of sub-project one rather than a
   dependency on sub-project two. It is deliberately throwaway-grade UI over
   real room logic; the room state machine it exercises is the real one and
   carries forward.
   Ends with a real Dota 2 match between the two physical PCs.
   *Nothing else starts until a game runs.*
2. **Control plane.** Accounts, rooms, slots, admission, lifecycle rules.
3. **Desktop client.** Lobby and room UI, launch integration, auto-update.
4. **Social layer.** Global chat, friends, invites, profiles, ratings, and the
   moderation tooling they require.

Repo 2's failure was inverting this order — extensive control plane and security
work while the game integration remained a file of nulls. The physical Dota test
is the first gate here, not the last.

---

## 9. Capacity and cost

Traffic is the dominant cost of this business. The server is cheap; the bytes
are not.

Every byte crosses the relay twice. The host sends a personalised stream to each
of the nine other players, and each sends back. That is approximately **1.2 Mbps
in and 1.2 Mbps out per 10-player game.**

### Target: 500 concurrent players

The launch target was reduced from 1500 to **500 concurrent players** on
2026-08-18, an owner decision. It is a realistic first-year figure and it is
a load we can actually verify with the hardware available, rather than
estimate.

**Measured 2026-08-18** against the real relay, 500 synthetic players at
Dota's packet rate (~30k packets/second in total):

| | Measured |
|---|---|
| Packet loss | **0.000%** |
| Median latency added by the relay | **2.8 ms** |
| 99th percentile | 69 ms |
| Throughput | **47.8 Mbps in, 47.8 Mbps out** |
| Relay CPU | 1.6 of 4 cores |
| Kernel packet drops | 0 |

That is measurement, not estimate, and it leaves roughly 2.5x CPU headroom
on a 4-core box. Full method and caveats in `loadtest/README.md`.

Real Dota packets vary in size; at the larger end the same player count
lands nearer **100 Mbps per direction**. A **1 Gbps symmetric port** remains
the recommendation — it is rarely more expensive than 100 Mbps and removes
the uplink as a variable entirely.

For reference, if the platform later grows to 1500 players, the same
measurement scales to roughly 150 Mbps per direction, and the relay would
need the batched-syscall work described in `loadtest/README.md` to sustain
the packet rate.

These are engineering estimates until section 10.1 confirms them.

**Cost lever, not built now:** every pair of players who can reach each other
directly is traffic that never touches our paid uplink. Direct peer-to-peer is a
cost optimisation as much as a latency one. We are relay-first — repo 2 proved
that perfecting the direct path before shipping kills momentum — but the
path-selection seam is designed in now so it can be enabled later without
restructuring.

### 9.1 Current server — survey of 2026-08-18

`87.107.110.199` (MobinHost, `vm37438-55060.mobinhost.com`), Ubuntu 24.04.

| Resource | Value |
|---|---|
| CPU | 4 vCPU |
| RAM | 7.8 GB (3.3 GB free, 7.0 GB available) |
| Disk | 59 GB total, 42 GB free |
| Uplink | **Unknown** — virtio reports `-1`; must be confirmed with the provider |

**This box is running a live, unrelated production service.** It is not idle:

- **CoreDNS** on `:53` TCP/UDP — the DNS half of an SNI proxy service.
- **nginx** with a `stream` block SNI-routing **TCP 443** to upstreams for
  Binance, EA and other international services, plus internal `8443`/`9443`.
- **WireGuard `wg1`** on UDP `51821` with an active peer (handshake seen
  1m52s before survey).
- Docker installed, **no containers running** — the old IranLobby360 Laravel
  stack is stopped, though its nginx site files remain.

**Port availability for us:** **UDP 443 is free.** nginx occupies *TCP* 443;
TCP and UDP are separate namespaces, so the relay can take UDP 443 without
touching the proxy service. WireGuard already holds UDP 51821.

**Recommendation:** use this server for the two-PC development and measurement
phase only, and move to a dedicated box before public launch. Rationale in
section 9.2.

### 9.2 Why the game platform should not share this box at launch

Coexistence is technically possible — the ports do not collide — but it is the
wrong call beyond testing:

1. **Capacity.** 4 vCPU and 7.8 GB must already serve the proxy business. Our
   relay at ~350 Mbps with per-session encryption, plus PostgreSQL, Redis and
   the API, does not fit alongside it.
2. **Blast radius.** An nginx reload error, a relay crash, or an OOM kill takes
   down a revenue-generating proxy service that has nothing to do with gaming.
3. **Contended uplink.** The proxy already consumes unknown bandwidth on an
   unknown port speed. Our 350 Mbps peak lands on whatever is left.
4. **Entanglement.** A consumer gaming platform and an international SNI proxy
   have different exposure profiles and should not share an IP or a fate.

Nothing on this server was modified during the survey.

---

## 10. Verification

### 10.1 Bandwidth measurement — works within the 2-PC limit

Two PCs are sufficient. With one host and one client we measure exactly what a
single player costs: the stream down to them and the stream back up. Because the
host sends a separate stream per client, a ten-player game is very close to nine
times that measured figure.

Scaling to 500 needs no Dota at all. Once the per-player rate is known, we
generate 500 synthetic peers producing that exact traffic pattern against the
relay and observe whether it holds.

**Real Dota for the rate; simulated load for the scale.**

### 10.2 Test strategy

- Unit tests on packet parsing, anti-spoof, and routing decisions.
- Loopback harness: relay plus N simulated peers asserting cross-room isolation
  and measuring forwarding latency under load.
- Soak test at 1500 synthetic peers proving the ancestor's failure mode is gone.
- **Physical two-PC Dota match as the acceptance gate for sub-project one.**

---

## 11. Open questions

Tracked explicitly. None block starting work.

1. **Can a new player join an already-running Dota LAN match to fill an abandoned
   slot?** Reconnecting one's *own* dropped player is known to work. A *different*
   person taking over an empty slot is unverified — neither ancestor project
   tested it. The CEO's dynamic room flow (section 3.1) depends on this.
   **Verify on the two physical PCs early.** If Dota disallows it, the
   recruit-a-replacement flow needs a different shape and we must know before
   building around it.
2. **Real per-player bandwidth.** Section 9 figures are estimates. Section 10.1
   resolves this.
3. **Wintun licensing** for redistribution must be confirmed before shipping an
   installer.
4. **Uplink port speed.** The VPS reports `-1` for NIC speed (virtio, not
   introspectable). MobinHost must confirm the provisioned port speed against
   the ~350 Mbps peak requirement in section 9.

**Resolved:** installer code-signing. Shipping unsigned is accepted until the
project is complete. Revisit before public launch — SmartScreen warnings cost
installs.

---

## 12. Explicitly not doing

Voice chat, ~~tournaments~~, guilds, mobile, multi-game at launch, anti-cheat
drivers, game binary modification, custom Dota server binaries, public internet
matchmaking, permanent room networks, cross-room visibility.

> **Superseded 2026-08-24 — tournaments.** The owner has since decided
> tournaments are a real feature (D48), with an entry in the lobby toolbar
> (D42). Nothing is being designed yet and none should be until accounts, the
> room work and the new lobby are in — but the room and account models are
> being built knowing that scheduled multi-room competition is coming, because
> that is far cheaper to allow for than to retrofit. The rest of this list
> stands.

There is no Dota 2 dedicated server in this design. The host's PC is the game
server, as it was in GameRanger and in DotaIranConnect. This was confirmed by
observing that the host's ping is always zero in the ancestor apps.
