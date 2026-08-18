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

## D11 — Go toolchain installed per-user; modules fetched via goproxy.cn and vendored

**Rejected:** the official installer from go.dev, and `proxy.golang.org`.

Verified on 2026-08-18 from this connection: `go.dev` itself resolves and
serves, but `dl.google.com` — where every binary download redirects — answers
a synthetic **404** for valid files, and `proxy.golang.org` is blackholed
entirely. Google geo-blocks Iranian addresses on both. `golang.google.cn`,
USTC and Tsinghua mirrors are also unreachable.

What works: `mirrors.aliyun.com/golang` for the toolchain archive and
`goproxy.cn` for modules. The toolchain zip was verified against the SHA-256
published by go.dev itself, which *is* reachable — so a mirror never has to be
trusted, only used for transport.

Go lives in `C:\Users\Mcc\sdk\go`, extracted from the archive rather than
installed by MSI, so no administrator rights were needed. `scripts/env.sh`
finds it regardless of shell PATH, and every script sources that.

Modules are vendored and committed. goproxy.cn is a single point of failure we
do not control; vendoring means losing it costs us nothing.

## D12 — `scripts/build.sh` replaces the plan's Makefile

`make` is not installed and is not worth a dependency for five build lines.
The script cross-compiles Linux server binaries from Windows and skips
components whose source has not been written yet, so it stays usable from
Task 1 onward rather than only once everything exists.

## D13 — `protocol/` is its own module; wire and crypto are not relay-internal

**Rejected:** the plan's layout, which put `wire` and `crypto` under
`relay/internal/`.

Go forbids importing another module's `internal/` packages, and the Windows
net-service, the test CLI and the load generator all need to speak the same
wire format and run the same handshake. Leaving them under `relay/internal`
would have forced either a duplicate implementation on the client side - two
copies of a packet parser that must agree exactly - or a fake "client" that
lives inside the relay module.

`finallobby/protocol` now holds `wire` and `crypto`. The relay keeps `route`,
`sendq` and `server` private to itself.

## D14 — Every datagram carries a wire header, handshakes included

**Rejected:** the plan's sketch, which guessed at the packet kind by trying to
parse a data header and falling back to "this must be a handshake".

A Noise handshake message begins with a random ephemeral public key, so
roughly one in 65,536 of them would have parsed as a valid data header and
been misrouted. Prefixing handshake packets with a real header, typed
`TypeHandshakeInit` or `TypeHandshakeResp`, makes the dispatch exact.

## D15 — The relay assigns the session ID in the encrypted handshake reply

The plan's client had no way to learn its session ID, so it addressed packets
with session 0 and the relay could not find it. The Noise reply payload now
carries a `wire.Accept`: session ID, virtual IP and room. It is encrypted, so
a passive observer cannot map sessions to virtual addresses.

## D16 — ufw on the shared server drops everything except what is named

Discovered 2026-08-18 while smoke-testing: our packets reached the server -
confirmed with tcpdump - but ufw dropped them before the relay saw them. Only
`22/tcp` was allowed. We added `443/udp` and nothing else. TCP, nginx, CoreDNS
and the WireGuard rules were left exactly as they were.

Any future port we need must be opened explicitly. Assume nothing is open.
