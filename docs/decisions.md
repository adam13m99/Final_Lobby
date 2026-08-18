# Decisions and their reasons

Why things are the way they are. **Read before "improving" anything here** —
each of these was chosen against a specific failure we watched happen in a
predecessor project.

Format: the decision, what we rejected, and why the rejected option is worse.

---

## D1 — Drop broadcast and multicast entirely; do not merely scope them

**Rejected:** forwarding broadcast only to members of the sender's room.

DotaIranConnect forwarded every player's LAN-discovery broadcast to every
connected player. That is O(n²) and it is why the service degraded past
roughly 1500 concurrent players. `dota-iran-lobby-v3` fixed it by scoping
fanout to the room, cutting recipients from ~1500 to ~9.

We go further because we can: the coordinator knows the host's virtual IP and
hands it to each client, so clients connect with `+connect <ip>:27015` and
Dota never performs LAN discovery at all. Zero recipients beats nine, and it
removes the failure class rather than shrinking it.

A config flag re-enables room-scoped multicast as a safety valve. It defaults
off and stays off unless Task 16 proves some Dota flow needs it.

## D2 — Unreliable datagrams, never a reliable-ordered stream

**Rejected:** KCP, which `dota-iran-lobby-v3` used in production.

KCP is reliable and ordered. A single lost packet head-of-line-blocks every
packet behind it until retransmission. Game traffic would rather lose a packet
than wait for it. This is structural, not a tuning problem — no `nodelay`
setting fixes it, and the FEC they added only masks it.

## D3 — One writer goroutine per peer, bounded drop-oldest queue

**Rejected:** spawning a goroutine per forwarded packet, as the ancestor did.

That spawns a goroutine per packet — at 1500 players roughly a million per
second — and it actively reorders game traffic, since goroutine scheduling
order is not send order. Goroutine count must scale with players, not packet
rate. Overflow evicts the oldest packet because newer game state supersedes
older.

## D4 — Noise NK, not IK

**Rejected:** Noise IK, which the spec originally named.

IK requires the initiator to hold a static keypair the responder already
trusts. Our clients authenticate with a short-lived coordinator ticket, not a
pre-shared key. NK fits exactly: the client knows the relay's static public
key, and the ticket travels inside the encrypted handshake payload. Same
security properties for our threat model, far less key management.

This also fixes an ancestor weakness where the accept packet was sent in
plaintext because the client had not yet built its decryption context.

## D5 — Custom encrypted UDP overlay, not WireGuard

**Rejected:** WireGuard, which `IranLobby360` used.

Two reasons. WireGuard's handshake has a fixed, well-known DPI fingerprint,
which matters a great deal on Iranian networks. And IranLobby360 required
players to install the official WireGuard client separately — a serious
adoption tax for a consumer app that should be a single installer.

Note that the ancestor's own stated reason for dropping WireGuard (that peers
must know each other's keys and endpoints in advance) is **wrong** —
hub-and-spoke needs only the relay's endpoint, as IranLobby360 itself
demonstrates. The right reasons are the two above.

## D6 — Windows service, not a localhost HTTP bridge

**Rejected:** `dota-iran-lobby-v3`'s model, where a web page drove an agent
listening on localhost as Administrator.

That required elevation on every use and created a genuine remote-code-execution
surface they had to patch after shipping. An installer-managed service means
players see **no UAC prompt when joining a room** — the most visible everyday
experience difference — and the attack surface disappears.

## D7 — Host-as-server; there is no dedicated Dota server

The host's PC runs the match, exactly as GameRanger and DotaIranConnect did.
Confirmed by observation: the host's ping is always zero in those apps.

The relay carries packets; it does not run the game. It therefore does **not**
reduce the host's upload burden — Dota sends a separate personalised stream to
each client, so the host uploads nine streams either way. What the relay buys
is reachability without port-forwarding, room isolation, and better routing
when two Iranian ISPs route poorly to each other.

## D8 — Network core first, gated on a real Dota match

**Rejected:** building the control plane and social layer first.

`IranLobby360` built a Laravel control plane, coordinator, lease watchdog and
security model over roughly two months, with genuinely excellent engineering
discipline — and never launched Dota programmatically once. Its entire game
integration is a config file of nulls. Nothing here proceeds past sub-project 1
until a real match runs between two physical PCs.

## D9 — Relay binds UDP 443 only

TCP 443 on the shared development server belongs to an unrelated live SNI
proxy business with real users. UDP and TCP 443 are separate namespaces, so
the relay coexists — verified free on 2026-08-18. Binding TCP 443 there would
take down someone else's revenue.

## D10 — Per-room /28 subnets with a deterministic host address

Room isolation then holds in three independent places: relay routing refuses
cross-room packets, each client's routing table contains only its own /28, and
the coordinator revokes leases server-side on leave. Any one failing does not
leak traffic between rooms.

The host always occupying the same offset is what makes D1 possible — clients
can be told where to connect instead of discovering it.
