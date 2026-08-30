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

## D17 — wintun.dll is embedded in the binary, not downloaded or installed

wintun.net is unreachable from Iranian networks, so a player could not fetch
the driver even if we asked them to, and asking them to install a driver by
hand is an adoption tax we refuse to pay.

The DLL is committed at `netservice/internal/adapter/bin/wintun.dll` and
embedded with `go:embed`. At startup the service writes it next to its own
executable, which is where Wintun's loader looks. An existing copy of the
same size is left alone, because it may be loaded and locked by the running
service.

Provenance: version 0.14.1, obtained through the server (which does have
international access) and verified locally by its Authenticode signature -
`CN=WireGuard LLC`, issued by DigiCert EV Code Signing CA, signature status
Valid. That check is stronger than comparing against a hash published on a
site we cannot reach.

## D18 — The relay reads on one goroutine per CPU, not one goroutine total

Measured 2026-08-18. A single reader goroutine pinned one core and capped the
relay near 50k packets per second, because every datagram costs a ChaCha20
decrypt before it can be routed. At 1500 synthetic players that produced 43%
loss and multi-second latency while three of four cores sat idle.

Concurrent reads on one UDP socket are safe; the kernel hands each datagram
to exactly one waiter. Reader count scales with CPUs - a small fixed number -
so the rule that goroutines never scale with packet rate still holds.

## D19 — The relay sets an 8 MB socket buffer and says so when the kernel refuses

The kernel default of 208 KB holds roughly ten milliseconds of traffic at
load. `netstat -su` on the dev server showed 1,084,680 receive-buffer errors:
packets the relay never saw and could not count.

The relay now requests 8 MB in both directions and logs a warning naming
`net.core.rmem_max` when the kernel grants less - silent truncation of a
buffer request is exactly the kind of invisible failure that wastes days.

`net.core.rmem_max` was raised to 16 MB on the dev server via
`/etc/sysctl.d/99-finallobby.conf`. Only the ceiling changed; per-socket
defaults are untouched, so nginx, CoreDNS and WireGuard behave exactly as
before.

## D20 — Peer count is not the scaling limit; packet rate is

The measurement that matters, from `loadtest/README.md`:

- 1500 peers at 12 pps: **zero** loss, 613 µs median, all 1500 handshakes
  succeeded, no queue drops, no routing drops.
- 300 peers at 300 pps: 47% loss.

Same relay, same box. The relay does not care how many players exist; it
cares how many packets per second arrive. This is the evidence that the
ancestor's collapse-as-players-join failure is designed out rather than
merely postponed.

It also redirects future optimisation. Work that reduces per-player cost is
not worth doing; work that reduces per-packet cost - batched syscalls above
all - is where the remaining headroom is.

## D21 — Always verify an upload by checksum

A `pscp` upload silently failed because the target file was locked by the
running process, and the next twenty minutes were spent testing an old
binary against a new expectation. Deployments now compare checksums on both
ends.

## D22 — Launch target is 500 concurrent players, not 1500

Owner decision, 2026-08-18, taken when a second VPS for load generation was
ruled out on cost.

This is a better decision than it looks like a compromise. 500 is a realistic
first-year figure, and - unlike 1500 - it is a load we can *measure* on the
hardware we actually have rather than estimate. Measured the same day, with
the relay given CPU priority to approximate a dedicated box:

| | Measured at 500 players |
|---|---|
| Packet loss | 0.000% |
| Median added latency | 2.8 ms |
| Throughput | 47.8 Mbps each direction |
| Relay CPU | 1.6 of 4 cores |
| Kernel drops | 0 |

Roughly 2.5x headroom. The batched-syscall work in D20 is therefore deferred
rather than dropped: it is what unlocks the next tier, and it is not needed
for this one.

## D23 — The IPC contract lives in `protocol/`, like the wire format

Same reason as D13: the test client - and later the desktop app - must speak
the service's command protocol, and Go forbids importing another module's
`internal/` packages. `protocol/ipc` holds the request and response types,
the named-pipe listener and the client dialer.

## D24 — Named pipe with an explicit ACL, not a localhost port

The pipe is created with `D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;IU)`: full
control for SYSTEM and local Administrators, read and write for interactive
users - the person physically at the machine.

Interactive users are allowed deliberately, because the desktop client runs
as the player rather than as an administrator; that is the whole point of the
service existing (D6). What matters is that a *web page* cannot reach a named
pipe, which is exactly how the predecessor's localhost HTTP bridge became a
remote-code-execution hole.

The service still validates everything it is asked to do. The client names a
room; it never names an executable or an address.

## D25 — Rate limits are tiered by risk, not applied uniformly

Found by using the CLI: a host locking the room, reopening it and kicking a
player within a few seconds - entirely normal play - hit the throttle meant
for room creation, and the app appeared to ignore them.

Three tiers now. Creating or joining a room stays tight (0.5/s, burst 5): it
allocates addresses and issues tickets, and it is what a griefer would
automate. Managing a room you already host is 2/s with burst 15. Reading is
5/s with burst 30.

## D26 — The no-UAC promise is verified, not assumed

Checked on 2026-08-19 with a de-elevated Basic User token
(`runas /trustlevel:0x20000`): an unprivileged user ran `lobbycli connect`,
and a Wintun adapter was created and addressed with **no elevation prompt**.

That is the design working as intended - the privileged work happens inside
the LocalSystem service, and the pipe ACL grants interactive users the right
to ask for it (D24). It is also the single most visible everyday difference
from the predecessor, which demanded elevation every time a player joined a
room, so it is worth a standing check rather than an assumption.

## D27 — The prototype UI is a local web page served by a process in the player's session

Rejected: Tauri, which the spec names. It needs a Rust toolchain and a Node
build, both large downloads over a connection that blocks a lot of the
internet, and the point of this build is to get two people playing tonight -
not to spend the evening on toolchains. Tauri remains the right choice for
sub-project 3.

Also rejected: a native Windows GUI. Every reasonable Go option needs cgo,
and there is no C compiler on this machine (see D18's neighbours - the same
constraint blocks the race detector).

`lobbyapp` serves a small page on 127.0.0.1 on a random port and opens the
browser at it. Pure Go, no toolchain, and the UI is easy to change.

**This is not the mistake D6 warns about.** That was a *privileged* agent on
localhost, so any web page a player visited could drive it as Administrator.
This process has exactly the rights the player already has, requires a random
per-run token, and refuses cross-origin callers. A hostile page gains nothing
it could not get by running a program as the user. The privileged half stays
in the service, behind the named pipe.

## D28 — The installer must survive a half-removed install

Found by running it: after uninstalling, the service entry lingered - Windows
keeps one registered until every handle closes - while its executable was
already gone. The installer then tried to run the missing binary to clean up,
and stopped with a path error.

It now handles the service and its executable disappearing independently,
falls back to `sc.exe delete`, and waits for the registration to clear before
creating the new one. A second install attempt is exactly the state a
frustrated person reaches, so it has to work.

## D29 — The data path is proven end to end without a second PC

Built `loadtest -echo-peer`: a synthetic peer that joins a room like any
other client and answers ICMP echo requests. It exists so the whole path can
be exercised from one machine.

Measured 2026-08-19. A ping from Windows travels into the Wintun adapter,
through the encrypted tunnel to the relay in Iran, out to the peer, and all
the way back:

| | Result |
|---|---|
| 32-byte packets, warm | 10/10 replies, 0% loss, 4 ms average |
| 900-byte packets | 6/6 replies, 0% loss, 5 ms average |
| First packet after connecting | lost, consistently |

That first lost packet is a cold-start artifact - the relay has not yet seen
where the peer is. Noted rather than chased: a game client retries, and it
costs one round trip at connect time.

What this retires: everything between the two ends. The only thing left that
genuinely needs a second machine is Dota's own client-to-host connection.

## D30 — The relay expires peers that go silent

Found on 2026-08-20 from the relay's own counters: it reported five connected
peers a day after the machines behind them had been shut down. Nothing ever
removed them.

A session was only ever dropped when a client politely sent a disconnect, or
when someone reconnected onto the same virtual address. A client that
crashes, is killed, or loses power does none of that - so its session, its
claimed address and its writer goroutine lived forever.

It is the shape of leak that stays invisible in testing and shows up after a
month of uptime, and it quietly breaks the rule in D3 that goroutines scale
with players: they were scaling with every player who had *ever* connected.

Peers now record when we last heard from them, and a reaper drops anything
silent for 90 seconds - six missed keepalives, since clients send one every
15. A quiet player who is watching rather than moving still sends keepalives,
and a test covers exactly that case so the timeout can never be tightened
into disconnecting real players.

## D31 — Identity stays a name with no password, for the test only

**Decided by the product owner, 2026-08-23.**

A player is a chosen name plus an ID generated at install. There is no
password and no account.

The consequence is understood and accepted: **a kicked player who reinstalls
comes back as somebody new**, so the five-minute block, room ownership and
declared MMR all rest on nothing an attacker has to respect. For two people
testing on two machines that costs nothing. For real players it is a hole,
and it must be closed before they arrive.

Recorded here so that nobody later reads the kick-block tests as evidence
that kicking works against someone who does not want to be kicked.

## D32 — The app is downloaded from a link, not copied on a stick

The first test build shipped a folder containing three executables, two
PowerShell scripts and a `setup.txt` holding the API token in plaintext. It
had to be carried to the second machine, right-clicked, and then a server
address and access code typed into a setup screen. Every fix repeated all of
it on both machines.

Now there is one link, one file, one permission prompt and a name to choose.
The server address, access code, relay address and relay key are stamped into
the binary at link time, so there is nothing to mistype and no token sitting
in a text file on two PCs.

**The download is served by the coordinator on TCP 7001**, which we already
own and which is already open. A separate web server would need a new
listening port and a new firewall rule; a vhost on the box's nginx would mean
editing a configuration that an unrelated live business depends on. Neither
is worth it to serve one file.

A browser cannot send a bearer token, so the download cannot sit behind the
API's authentication. The unguessable path segment is the secret instead.
That is weak, deliberately: the worst case is a stranger downloading a test
build of a lobby for a game they cannot reach. It goes away with real
accounts.

## D33 — The app updates itself, and "different" is what triggers it

Without self-update, every fix during a test session costs a reinstall on
every machine being tested, which in practice means the session stops while
somebody walks to the other PC.

The app compares its own build stamp against the published manifest and
downloads anything **different** — not anything newer. Version strings here
are build stamps, not an ordered series of releases, and if a bad build goes
out mid-session then replacing it has to reach the test machines exactly as
readily as a fix does.

The downloaded file is checked against the manifest hash before anything will
execute it, and a failed check leaves nothing behind on disk.

Installing an update asks for Windows permission once. That is a deliberate
limit on the no-UAC promise, which covers **playing** — connecting, joining,
launching Dota — and not replacing the program's own files. Every Windows
application behaves this way and pretending otherwise would mean giving a
user-writable directory to something the system account executes.

## D34 — The machine runs the acceptance checks, not a person

The first acceptance document was fourteen numbered cases asking someone to
read values off a screen and write them down, on two machines, and then read
them aloud to whoever was fixing things.

The app now has a Diagnostics button that checks the server, the service,
Dota, the tunnel and the other player, and posts the results to the
coordinator. Both machines' runs can be read from one place.

What is deliberately **not** automated is whether a real Dota 2 match ran.
No check can answer that, and a green tick claiming otherwise would be worse
than no tick at all. That stays a human observation, and it is the gate.

## D35 — Nothing we add goes near the other business on this box

Re-confirmed 2026-08-23, after the owner rebuilt the `nati-filter` control
plane on the same server.

That stack now holds WireGuard on UDP 51821, a Python control plane on
127.0.0.1:8080, PostgreSQL on 127.0.0.1:5432, an nginx stream router on TCP
443 fed from inside the WireGuard tunnel, and a reverse SSH tunnel to Paris.
Public TCP 443 is no longer accepted from the internet at all.

We hold **UDP 443** and **TCP 7001**, and nothing else. UDP 443 and TCP 443
are different sockets and coexist without either side knowing. The download
went onto 7001 rather than taking a new port precisely to keep that count at
two. `scripts/publish.sh` checks nginx, CoreDNS and the relay are still
running after every publish and prints the result, rather than assuming it.

## D36 — A ticket is minted when you press Connect, not when you join

Found 2026-08-23, during the first two-PC session, and it had already cost
the owner an evening.

One PC created a room, the other joined, and the two people then did what
two people always do: talked to each other about what to try next. Some
minutes later both pressed **Connect** and neither could reach the host. The
app said only that the tunnel had not come up within fifteen seconds.

The cause was a lifetime mismatch nobody had noticed because every test until
then had been done by one person in a hurry:

- A ticket is valid for **10 minutes** (`ticket.Lifetime`).
- It was issued when a player **joined** the room.
- The watchdog that renews it only starts running **after** the tunnel is up.
- So a player waiting in a room held a ticket that quietly died, and Connect
  could never succeed again.
- `Join` refuses a player who is already seated, so the only escape was to
  leave the room and rejoin it — which nobody would guess.

A room full of people arranging a match sits open far longer than ten
minutes. The old arrangement therefore failed for **exactly the case the
product exists to serve**, and worked only in the artificial case of joining
and connecting within the same minute.

The fix is that `POST /v1/rooms/{id}/connect` returns fresh connect info to
any player already seated, and both the app and the CLI call it every time
Connect is pressed. It costs one extra request per connect and removes the
whole class of failure.

Deliberately **not** done: lengthening the ticket lifetime. Ten minutes is
short on purpose — it bounds how long a kicked player could keep playing if
every other check failed (see the ticket package comment). Making tickets
last longer would trade a real security property for a bug that is better
fixed at its cause.

**Still open, and worth fixing before real players:** a relay that refuses a
handshake stays silent, so the client can only report a timeout. "The tunnel
did not come up" was true and useless — it named a symptom three layers away
from the cause. The relay should say no, and the app should repeat what it
said.

---

# Product decisions of 2026-08-24

The owner answered the full decision suite in
`docs/product-decisions-2026-08-23.md`. These entries record what was chosen
and what it costs. Where an answer changes something already built, the
consequence is written down rather than left to be discovered later.

## D37 — Accounts are a username and a password, built so email and SMS can be added

**Owner decision.** A player signs up with a username and a password. No
email, no SMS, no third party — all three are unreliable domestically, and any
of them would gate signup on something that can be blocked.

The owner asked specifically for the **foundation and architecture** to
support email and SMS later, so they can be switched on without rework. That
shapes the schema now: an account carries a set of *verified contact methods*
from the first migration, empty for everyone, rather than having contact
columns bolted on afterwards.

Password recovery follows the same rule: **recovery exists only where a
verified contact method exists.** With none, a lost password means a lost
account, and the signup screen must say so plainly rather than let people find
out at the worst moment.

This closes D31, which was explicitly a test-only arrangement.

Existing test identities do **not** carry over. There are two, and they are
ours.

## D38 — A room seats 18, which no longer fits in a /28

**Owner decision:** ten playing slots, **up to five observers**, and **three
admin slots**. Eighteen seats.

**This does not fit the addressing we built.** A /28 is sixteen addresses:
`.0` network, `.1` relay, `.2`-`.11` the ten players, `.12`-`.14` three
spectators, `.15` broadcast. Thirteen seats, and every address is already
spoken for.

Eighteen seats plus network, relay and broadcast needs twenty-one addresses,
so each room moves to a **/27** — thirty usable. The layout becomes `.0`
network, `.1` relay, `.2`-`.11` players, `.12`-`.16` observers, `.17`-`.19`
admins, `.20`-`.30` spare, `.31` broadcast.

**The cost is the room ceiling: 4096 becomes 2048**, because a /27 is twice
the size of a /28 and `10.87.0.0/16` is fixed. At the 500-player launch target
that is fifty concurrent rooms, so 2048 is forty times what is needed; even at
1500 players it is 150. The ceiling is not a real constraint, and the spare
addresses in each block leave room to raise the seat count again without
another migration.

Deliberately **not** done: widening beyond `10.87.0.0/16`. A larger private
range risks colliding with whatever the player's own home network already
uses, and 2048 rooms is well past any horizon we can see.

## D39 — Kick blocks escalate: 1, 3, 5, 7 minutes, and so on

**Owner decision.** The first kick from a room bars a player for one minute,
the second for three, the third for five, each subsequent kick adding two more.

The first block is now *shorter* than the five minutes it replaces, which is
the point: most kicks are an argument rather than an abuser, and a one-minute
cool-off ends the re-join fight without punishing somebody for the evening.
Escalation is what deals with the person who keeps coming back.

The count is per player per room and must **survive a coordinator restart**,
or escalation resets on every deployment and means nothing. That makes it the
first piece of room state that has to be persisted rather than held in memory,
and it lands with the control plane.

Worth stating plainly: escalation only bites once D37 is in. While identity is
a name generated at install, a determined person reinstalls with a clean count.

## D40 — The room outlives the match; it does not outlive the host

**Owner decision**, replacing the earlier two-minute rule.

- Host leaves, times out or crashes: the room closes after **one minute**.
- The match ending does **nothing** to the room. The players stay together.

This is GameRanger's behaviour and it is better than what we had. Previously a
finished match left the room locked, waiting for the host to reopen it — which
treats the end of a game as a problem to recover from. It is the normal case:
ten people finish, and want to play again.

The one minute still doubles as the host's window to reconnect and save the
room. It sits inside the 120-second sticky-address window from the spec, so a
host who returns in time keeps their address and the room never noticed they
were gone.

## D41 — All four kinds of room, and friends ship before launch

**Owner decision:** public, password, friends-only and invite-only, as the
original spec promised — and the friends system is built **before** launch
rather than after, with add, remove, private chat, invite to lobby,
online/offline, and in-game/not-in-game status.

This resolves the dependency rather than dodging it. Friends-only and
invite-only rooms are meaningless without a friends list, so choosing all four
room types is choosing to build friends first. The owner chose both, and chose
them consistently.

The in-game indicator is worth calling out as nearly free: the service already
knows whether Dota is running, because it launched it and watches its log.
That signal exists today and only needs surfacing.

## D42 — The lobby is a place, not a list

**Owner decision**, specified in detail:

1. Room list showing host name, description, minimum MMR, player count, status
   (in game / not in game) and **ping**.
2. Friends list down the right side, where the lobby chat sits today.
3. Chat with tabs — lobby, friends, party — modelled on Dota 2's own main-menu
   chat, and collapsible the same way.
4. Profile, top right.
5. A permanent left toolbar: Lobby, Room, **Tournaments**, Profile, connection
   status.
6. Room filter and search.
7. A banner and advertising strip along the top.

**Two of these need resolving before they are built.**

**Ping cannot mean what it appears to mean.** Rooms are isolated from each
other by design, and that isolation is enforced in three independent places, so
a player sitting in the lobby has no path to the host of a room they have not
joined and cannot measure a round trip to them. What *is* measurable, and
genuinely more useful, is the **host's own latency to the relay** — the relay
observes it already, and it differs meaningfully between rooms, because a host
on a poor connection makes a poor game for everyone who joins them. That is
what the column will show, labelled so nobody reads it as their own ping.

**Tournaments contradicts the spec.** Section 12 lists tournaments among the
things explicitly not being built. A toolbar entry is cheap; a tournament
system is not. Recorded as an open question rather than assumed either way.

## D43 — What an admin can do

**Owner decision.** Kick, ban, mute, time out. Manage rooms: close one, change
its host. Manage the banner strip: add, remove, edit. And mark a player with a
visible status — the examples given were *Fake MMR*, *Verified*, *Pro Player*
and *Noob*.

Two notes, neither blocking.

Changing a room's host is host migration under another name. D40 says a room
dies a minute after its host does; an admin reassigning the host is the escape
hatch for when that is the wrong outcome. The two are consistent, and it is one
mechanism rather than two.

**A public *Noob* label is a moderation tool pointed at the player.** *Fake
MMR*, *Verified* and *Pro Player* each describe something checkable and defend
the honest majority. *Noob* is an insult carrying staff authority, and it is
the screenshot that circulates. Recommendation: keep the mechanism, which is
genuinely useful, and let the labels that ship be ones a moderator could
defend. The owner's call — flagged here rather than quietly dropped.

Still unanswered, and it matters: **who the admins actually are.** The tooling
is worth nothing without a named person whose job it is.

## D44 — English first, Persian later — but the layout is built for it now

**Owner decision**, against the recommendation, with a clear reason: ship,
prove the product works, then translate.

The recommendation was Persian first, because the audience is in Iran. The
owner's reasoning is sound, and this is their call to make.

**What engineering does about it is not optional.** Persian is written
right-to-left, and retrofitting direction into a finished layout is the
expensive path, because it touches every screen. So the interface is built
direction-agnostic from the start: text through a lookup rather than typed into
the markup, layout in logical properties that flip on their own, and no
hard-coded left or right anywhere. Nothing about the English product changes.
Adding Persian later becomes a translation file and a switch, instead of a
rebuild.

That is the difference between "later" costing a week and costing a month, and
it costs nothing today.

## D45 — A real desktop application, in the tray, that lets you look before you sign up

**Owner decisions**, three that fit together:

- **Tauri**, as the spec always said. Its own window and icon, no browser
  chrome, no tab to close by mistake. The screens already written carry over.
- **Minimise to the tray, and notify** when a room fills or a host starts. This
  is why people left GameRanger running, and a lobby is worth nothing when
  nobody is sitting in it.
- **Browse before signing up.** A new player sees the lobby and its rooms
  first, and is asked for an account only when they try to join one.

The third carries a cost the owner accepted: anonymous browsers consume server
capacity and are a rate-limiting problem in their own right. It buys the thing
that matters more — somebody who downloads the app meets a living place rather
than a signup form.

## D46 — The product is called LobbyBaz

**Owner decision.** "Final Lobby" was a working title.

The rename is not cosmetic. It reaches the install directory, the Windows
service name, the virtual adapter's name, the desktop shortcut, the entry in
Add or Remove Programs, the installer filename, the Go module paths, and every
document in the repository. It also has to *upgrade* the two existing installs
rather than sit beside them: the installer must remove the old service and the
old directory, the way it already handles the earlier per-user layout.

Best done in one deliberate pass, and soon, while the number of places the old
name appears is still small.

## D47 — Admin is a role the head admin grants, not a flag on an account

**Owner decision, 2026-08-24.** The owner is the head admin. Admins are people
they appoint, and the appointment can be withdrawn.

That is a sentence with real consequences for the schema, and getting it wrong
is the kind of thing that is painful to unpick later:

- **Roles are granted, so they are records, not booleans.** A grant has who
  gave it, to whom, and when — and a withdrawal has the same. Without that
  history, "who made this person an admin?" has no answer, which is exactly the
  question asked after something goes wrong.
- **There is exactly one head admin, and it is not an ordinary admin.** Only
  the head admin appoints and removes. An admin cannot appoint another admin,
  or the role spreads and cannot be pulled back.
- **Every admin action is attributed.** A ban, a mute, a label, a closed room,
  a changed host, an edited banner — each records which admin did it. Powers
  like D43's without an audit trail are how a moderation team loses the trust
  of its players, and there is no way to reconstruct the trail after the fact.

The head admin is bootstrapped at deployment rather than through the app: an
account promoted directly in the database, once. A self-service path to the
most privileged role in the system is a door with no purpose.

## D48 — Tournaments is a real feature, and the spec is now wrong about it

**Owner decision, 2026-08-24.** The Tournaments entry in the left toolbar
(D42) is a real feature, not a placeholder.

**Section 12 of the design spec lists tournaments under "explicitly not
doing".** That line is now out of date and the spec should say so rather than
contradict the product.

No design work is being done on it yet, and none should be until the account
system, the room work and the new lobby are in. Recording it now does two
things: it stops the toolbar entry shipping as a dead link nobody planned, and
it means the room and account models get built by somebody who knows that
scheduled multi-room competition is coming. Those two facts change how a room
is modelled — a tournament match is a room that somebody else created, at a
time nobody in it chose — and that is much cheaper to allow for than to
retrofit.

What a tournament actually *is* here — brackets, scheduling, prizes,
registration, who runs it — is undecided and needs its own brainstorm.

## D49 — The dedicated server is bought when the product is ready to deploy

**Owner decision, 2026-08-24.** Not before.

This matches the spec, which has always said a dedicated box before public
launch, and it is the right economic call: paying for a second server through
months of development buys nothing, because development load is negligible and
the current box is measurably idle.

What it means in practice is that **the move is part of launch, not a
precursor to it.** The risks of sharing — one uplink, one IP address, so
filtering aimed at either side hits both — are acceptable while the only users
are us, and unacceptable the moment real players arrive. So the migration has
to be rehearsed rather than improvised on the day:

- Every server-side piece is already a systemd unit deployed by script, and
  `scripts/deploy.sh` takes a host. Nothing is configured by hand, which is
  what makes this a rehearsal rather than a rebuild.
- The one piece of state that cannot be regenerated is `/etc/finallobby/relay.key`.
  Clients carry the matching public key, so it moves with the platform or every
  installed copy stops working.
- The relay's address is stamped into client binaries at build time, so the new
  server's address must be published to installed copies *before* the old one
  goes away. The self-update path already carries this; the ordering is what
  matters and it is easy to get backwards.

Recorded now so that none of the above is discovered on the day.

## D50 — The rename stops at the server's filesystem

**Engineering decision, 2026-08-24**, taken while implementing D46.

Everything a player can see is now LobbyBaz: the Windows service, the install
directory, the virtual adapter, the shortcut, the Add or Remove Programs
entry, the installer filename, the per-user config directory, every string in
the interface, and all nine Go module paths.

**The server keeps the old name.** `/etc/finallobby`, `/opt/finallobby`,
`/var/lib/finallobby`, the `finallobby` unix user, and the `relay.service` and
`coordinator.service` unit names are unchanged.

Three reasons, in order of weight:

1. **`/etc/finallobby/relay.key` cannot be regenerated.** Every installed
   client carries its public half, baked in at build time. Moving it is a file
   operation that either works or silently breaks every copy of the app in
   existence, and the failure would not show up until somebody tried to
   connect.
2. **The box also runs the owner's live, unrelated business.** Renaming a
   system user and rewriting systemd units on a machine serving real customers
   of something else is risk taken on their behalf, not ours.
3. **The payoff is zero.** No player, and no future engineer reading the
   deploy scripts, is misled by a directory name. It is internal.

The ldflags stamping paths **did** have to change — `-X finallobby/client/build.X`
became `-X lobbybaz/client/build.X` — because a stale ldflags path does not
error. Go accepts it, sets nothing, and ships a binary with no server address
and no version. That is the one part of this rename that fails silently, which
is why it is called out here.

`installer/rename_test.go` pins both halves: the legacy names must **not** be
renamed, or machines running a pre-rename build end up with two installs
racing to create the same virtual adapter; and the current names must have
moved, or the "upgrade" would collide with what it is replacing rather than
replace it.

Revisit when the platform moves to its own server (D49). That migration
rebuilds the filesystem layout anyway, and is the natural moment to make the
names match.

---

## D52 — A kick is a live block *and* a durable event, and they are stored differently

**2026-08-24, technical.**

T4 left a note: the escalating kick block (D39) has to survive a coordinator
restart, "or the escalation resets on every deployment and means nothing." The
first draft of the schema followed that literally, with a `room_kicks` table
holding room, player, count, and blocked-until.

It would have been dead weight, and briefly dangerous. Two facts kill it:

1. **A block belongs to a room, and rooms live in memory.** A coordinator
   restart ends every room. On the next start the persisted block would key
   into a room that no longer exists, and it would bar nobody from anything.
2. **Room IDs were being reused.** They were `r<unix-seconds mod 100000>-<n>`
   with `n` restarting from zero on every launch. So the persisted block could
   key into a *different* room that happened to take the same ID, and bar an
   innocent person from a room they had never been in.

So the storage splits along the line the data actually falls on:

- **The live block stays in memory**, with the room it belongs to. When the
  room ends, so does the block. That is correct rather than a limitation: the
  block exists to stop a re-join fight in a room that is currently happening.
- **Every kick is written down as an event** — `kick_events`: room, actor,
  target, which kick it was, how long it barred them, when. This is the part
  that is still true months later, and it is what a moderator needs when
  somebody says "he keeps doing this" (T8).

Recording never blocks the kick. If the write fails it is logged and the kick
proceeds; a moderation record that could stop a host removing a griefer would
be worse than a missing row.

**Room IDs are now sixteen random hex characters and are never reused.** That
is a fix worth having on its own: chat logs, kick records, admin actions (T8)
and tournament results (T12) are all keyed by room ID, and every one of them
would have been corruptible by ID reuse.

---

## D53 — The session decides who you are; `player_id` in a request body is ignored

**2026-08-24, technical, following from D37.**

Until now every request carried the player's own ID in its body and the
coordinator believed it. Anybody who could reach the API could act as anybody
else by typing their ID: kick from a room they did not host, chat as them,
take their seat, change their declared MMR. That was acceptable when the only
two clients were the owner's own PCs, and it is not acceptable with real
players.

With accounts enabled:

- A session token travels in `X-LobbyBaz-Session`. Not a cookie — the client
  is a desktop application, not a browser, and there is no origin to scope a
  cookie to.
- Every acting endpoint (create, join, leave, kick, status, spectate,
  connect, sync, chat, profile) requires a valid session and takes the
  actor from it. The body's `player_id` is **ignored**, not rejected: a
  mismatch is far more likely to be an old client than an attack, and
  refusing it would break every installed copy on the day the coordinator
  is upgraded.
- **Reading the room list stays open.** D45 wants somebody who just installed
  the app to see what is going on before deciding whether to sign up. A lobby
  that is empty until you have an account looks dead.

A coordinator started without `-db` runs exactly as before: no accounts, and
the body's `player_id` taken at face value. That mode exists for the loadtest
harness, which drives the API with thousands of generated identities and has
no business signing each of them up. **It is not a mode to run players on**,
and the coordinator logs a warning saying so at startup.

The escalating sign-in limiter is the tightest of the four tiers: a guess
every five seconds, five in hand, per address. Somebody who mistypes their
password twice never notices it; somebody working a word list gets roughly
seventeen thousand attempts a day, against an Argon2id hash costing 64 MiB.

## D54 — The lobby's latency column is the host's, and the relay measures it by echoing

D42 asked for a **ping** column on every room and flagged that it could not
mean what it appears to mean. This is how it was resolved.

**A player browsing the lobby cannot ping anything.** Rooms are isolated from
each other and that isolation is enforced in three independent places, so
there is no path from somebody in the lobby to the host of a room they have
not joined. Any number presented as "your ping to this room" would be
invented.

**What is real, and more useful, is the host's own distance from the relay.**
Every packet in a match passes through the relay, so the host's leg of that
path is in the round trip of every other player in their room. A host on a bad
connection makes a bad game for the nine people who join them, and that is
exactly the thing worth knowing before choosing a room. It is shown labelled
as the host's latency; a player who reads it as their own ping will blame the
wrong thing when a game plays badly.

**The relay did not measure anything, so it does now.** It replies to a
keepalive with a keepalive, carrying back the same sequence number. The
sequence field is in the clear in every header already, so this needs no new
packet type and no payload. The client puts the moment of sending into that
field and subtracts when it comes back, which means there is no table of
outstanding probes to keep, expire or leak.

Three things about that echo were deliberate:

- **It is written from the reader, not pushed onto the peer's send queue.**
  That queue carries inner packets for the peer's writer to seal, and a
  keepalive has nothing to seal. The rule this relay is built on is that
  goroutines scale with players and never with packet rate; a fourteen-byte
  write adds neither.
- **The reply is the same size as the request,** so it is not an amplifier.
- **The reading is smoothed,** a quarter weight per sample, and samples outside
  nought to five seconds are refused. One bad millisecond on a home Wi-Fi
  must not make a column jump between 30 and 300, and a genuinely broken path
  must not poison the average for minutes after it recovers.

**The number is self-reported by the host's own machine,** and the coordinator
keeps it only from the player who actually hosts that room. Self-reporting is
acceptable here: the only person a host could mislead is somebody deciding
whether to join their room, and the lie is exposed in the first minute of
play.

**Zero means not measured yet, not excellent.** Every layer preserves that
distinction, because collapsing it would make every room nobody has measured
look like the best room in the lobby.

**Deployment note:** the echo is a relay change. Until the relay on the server
is redeployed, hosts measure nothing and the column is empty everywhere — which
is the correct thing for it to show, but it is not the same as the feature
being broken.

## D55 — The desktop app is a shell around the Go client, not a rewrite

D45 asked for a real desktop application: a window rather than a browser tab,
a tray icon, and notifications. Tauri was chosen there. What that decision did
not settle is **how much of the product moves into it**, and the answer is:
none of it.

`desktop/` is a Tauri shell that starts the existing Go binary, asks it which
loopback address and token it is serving on, and points a webview at it. The
lobby, the tunnel, accounts, moderation and the launcher all stay exactly
where they are.

**Why not rewrite the client in Rust:**

- The Go client is the only code here that has actually carried a Dota match
  between two PCs. Rewriting it would set the one proven part of the system
  back to zero, in exchange for nothing a player could see.
- The same binary is what the Windows service talks to and what runs headless
  for load testing. One implementation means one set of bugs.
- What a browser page genuinely cannot do is precisely what the shell adds: a
  window that is not a tab, a tray icon, and notifications that arrive while
  the window is hidden. That is a small, well-bounded amount of code.

**Three consequences worth knowing.**

**The address is read back, not agreed in advance.** The Go binary is started
with `-url-only`, prints its address on the first line, and the shell reads
it. A fixed port would eventually collide with something else on a player's
machine, and there would be no good way to recover. The port comes from the
operating system and the token is fresh each run, so nothing else on the
machine has a fixed thing to guess.

**Notifications are raised in Rust, not in the page.** They exist for the case
where the window is hidden in the tray — and a hidden window is not running
the page. A player who is watching the lobby can see a room fill with their
own eyes. Both notifications are edge-triggered against the previous poll:
level-triggering would fire every five seconds for as long as the condition
held, which is how a player learns to turn notifications off.

**Closing the window hides it rather than quitting.** Quitting is in the tray
menu. Somebody who closes the lobby while a friend is filling a room should
still hear about it; that is the entire reason the tray is there. The Go
process is killed when the shell genuinely exits, because a lobby server left
running without a window is a held port and an unwatched tunnel.

**The splash screen is deliberately wordless.** It is bundled inside the Rust
binary and cannot reach the string catalogue the real interface uses (D44), so
rather than hard-code one language's sentence where the lookup cannot see it,
it shows the mark and nothing else.

## D56 — The chat is a dock along the bottom, and every conversation is a tab

Asked for on 2026-08-25, with Dota 2's own main menu as the reference.

The chat used to be a panel sharing the right rail with the friends list. It
had three problems at once, and they were the same problem seen three ways.

**The rail could not hold both.** Friends took a share of the height and the
chat took the rest, so a short friends list left a hole and a long one left a
chat four lines tall. Neither half was ever the right size.

**A private message opened a dialog over the lobby.** Talking to somebody is
not a thing you stop doing other things to do. Answering a friend meant
covering the room list, and closing the dialog meant losing the conversation.

**Nothing announced itself.** A message that arrives in a panel nobody is
looking at has not been delivered.

**What it is now.** A bar across the bottom of the window, always present,
minimised to its tab strip until it is used. Lobby, Room and Party are fixed
tabs; every private conversation opens another, the way a browser opens a tab,
with a × on it. One input box serves whichever tab is open, and it wears a
label naming the person when — and only when — the words are going to one
person rather than to a room.

**It opens itself, and it makes a sound.** Anything arriving in a tab the
reader is not looking at lights that tab, opens the dock and plays a short
tone. Three details matter and each is a bug avoided:

- **The first sighting of a tab is never announced.** That is the backlog that
  was already there when the window opened, and announcing it would make every
  start-up ring.
- **A conversation is counted, not signed.** Reading a conversation sets its
  unread count back to zero, so "different from last time" would ring on the
  way down as well as on the way up. Only a count that grew is a message that
  arrived.
- **The audio device is created from a real click or keystroke, never before.**
  A browser asked to make noise before anybody has touched the page refuses and
  says so in the console — and `smoke.sh` fails on a console that says
  anything (D44).

**A friend with unread messages gets a tab whether or not anybody opened one.**
Somebody writing to you is the thing most worth interrupting for.

**Party is present and does nothing, on purpose.** Parties are not built. A tab
that says so is better than a chat whose tabs move once they are.

**The friends list now owns the whole rail**, which is what the request asked
for and what the rail was always the wrong shape for.

## D57 — Your seat is your team

Also 2026-08-25: *"a player can switch slot in a room by clicking the slot."*

Slots 0-4 are Radiant and 5-9 are Dire — that is how the game divides them,
and how the room screen has drawn them since D42. Picking a side was
nonetheless a dropdown in the action bar, filled in separately from the seat,
and the two could disagree. Changing which five you were joining meant leaving
the room and rejoining until the numbers came out right.

Clicking an empty seat now moves you into it, and **the dropdown is gone**. One
fact, one place.

**The refusals are the same on both sides of the wire**, so a card only invites
a click when the click would work:

- **Slot 0 belongs to the host for the room's whole life.** Nobody moves into
  it and the host does not move out — the client, the relay and the room list
  all read the host out of slot 0.
- **A locked room is a match already running.** A player who changes team
  halfway through is a player on the wrong team inside Dota, and nothing here
  can undo that.
- **A taken seat is taken.** No swapping, no bumping.

**Moving throws your ticket away.** A player's virtual IP is derived from their
slot, so after a move the ticket they are holding names an address that is no
longer theirs — and the relay's anti-spoof check would drop everything they
sent. The coordinator revokes on a successful move and the app reconnects as
part of the same action, but only for a player who was already connected:
somebody picking a side before anybody has pressed Connect should not have a
tunnel brought up under them.

**The game mode moved to the host's row.** It is a property of the match the
host's own copy of Dota creates; a client sending one changed nothing and
suggested it might. The action bar is now Connect / Disconnect / Launch for
everybody, the host's controls in their own row beneath, and Leave last — and
it stays pinned to the bottom of the room screen, because with the chat dock
open it otherwise sat below ten seats where nobody saw it.

---

## D58 — Nocturne: the interface the owner drew, adopted whole

**2026-08-26 (T19).**

The owner supplied a working HTML mock — `Gaming Matchmaking App Redesign/
LobbyBaz.dc.html` — and asked for the front end to follow it. It is now the
reference for the look; this file stays the reference for the behaviour.

**What changed, and why it was worth changing:**

- **One dark palette, defined once as tokens.** Every colour the interface
  uses is a `--custom-property` on `:root`. Nothing below reaches for a hex
  code, so the next reskin is one block rather than nine hundred lines.
- **No web font.** The mock names one; fonts.googleapis.com is not reachable
  from inside Iran, and a stylesheet that waits on it is a blank screen for
  however long the DPI takes to give up. A system stack renders instantly and
  looks the same on the two PCs this is tested on.
- **The room list is a table, and every heading sorts it.** A player reads
  the lobby for one thing at a time — who has space, who is closest, who is
  at my level — and which one changes between one glance and the next. The
  columns shed themselves in that order as the window narrows, so the room's
  name never gives ground.
- **Getting into a game is three numbered steps with one button under
  them.** It replaced a row of buttons that were sometimes disabled. The
  commonest failure in the two-PC test was two players in a room, neither on
  its network, with nothing on the screen saying which of the three things
  had not happened.
- **The host's controls are a dialog, not a screen.** They are opened perhaps
  once per room, and they were costing everyone else a third of the room
  screen.
- **Diagnostics is not a toolbar entry any more.** It is one button on the
  settings screen, immediately under the three network facts it explains.
- **The chat dock now starts open** rather than minimised. D56 chose Dota 2's
  chat as the model and then started it collapsed, which is not what Dota
  does; the mock shows it open, and with the room list now filling its panel
  there is space for it. It still minimises to the tab strip when asked and
  still reopens itself on an incoming message — `scripts/chatcheck.sh` proves
  the second half over a live page.

**Adopted the design, not the invented features.** The mock shows things the
server has no notion of: a game mode stored on a room, "EU West" and "fra-02"
regions, a Steam link, games-hosted counts, per-player ping, last-seen times.
None of them were faked. Two are worth the owner's decision and are recorded
in `docs/STATE.md` as open questions rather than silently dropped:

- The mock puts a **Watch** button on rooms that are in game. The owner said
  earlier, explicitly, that there is no Spectate button in the lobby. The
  earlier instruction won; the button is not there.
- The mock stores a **game mode** on the room and shows it in the room's
  meta line and in the create dialog. Today the mode is the host's own, sent
  to Dota when they launch (D57), and nothing about a room advertises it.

**"Tournaments" became "Events" in the toolbar**, because at 72px the longer
word does not fit and the mock had already made the choice. Nothing behind
it changed: the screen id, the key namespace and the honest "not yet" are
all as they were.

---

## D59 — The owner's answers to the mock, and the wiring behind them

**2026-08-26 (T20).** The owner reviewed the adopted design (D58) and settled
the questions it raised. Their answers, and what each one cost:

**No regions, no Steam link.** Both were the mock's inventions and neither is
wanted. Nothing to build; open question 7's first half is closed.

**Per-player ping, yes.** A player's own round trip to the relay now travels
with their seat. Only their machine can measure it — everyone in a room
reaches everyone else through the relay, and nobody in the lobby has a path
to anybody they have not joined — so it is reported on the heartbeat and read
back on `memberView.relay_ms`. It is dropped rather than shown when it is
older than the presence window: a stale reading displayed as current is the
number somebody will blame a bad game on. Zero means *no reading*, never
*instant*, and the two must never render the same.

**Last-seen times, yes.** The player registry has always known when somebody
was last here, but only for as long as the process lived, so every
deployment forgot it. `accounts.last_seen_at` is the copy that survives, and
it is written **on a timer, not on the heartbeat**: a thousand players polling
every two seconds would be five hundred SQLite writes a second for a number
nobody reads more finely than to the minute. The live registry value wins when
it has one, because the stored copy is always the coarser of the two. It is
suppressed for anybody online (the answer is "now") and for anybody blocked —
when somebody was last around is whereabouts, and a blocked person's
whereabouts are not yours to watch.

**Game mode stays in the room, not on the room.** The host switches it in
Room settings and the app hands it to Dota when they launch; the coordinator
stores nothing about it and no room advertises one. It is a local game and
the app automates the rest. Open question 7 is closed: **no**.

**There is no Watch button.** A room is *joinable or full*, and *in game or
open* — two axes, four states, and no fifth thing to offer. Open question 8
is closed: **no**, permanently, not "not yet".

**Five watching seats, below the two teams.** Not a spectator build: five
more seats in the room, drawn and taken exactly like a playing seat. What was
there before was a strip of text reading "nobody is spectating" with no way
to become one — the seats existed on the coordinator since T5 and had never
had a door on the client. This is the same structural bug the project keeps
finding, and the fix is the same: build the door in the commit that builds
the room. The admins' three seats are a separate range and are not drawn;
`Member.Seat` had to be added to the client library, or a moderator would
have appeared in a watcher's chair — and, because the two ranges are numbered
separately, in one that was already occupied.

**The search box and the filter chips belong to the lobby alone.** They acted
on the room list and on nothing else, and leaving them across the top of the
room, events and settings screens put controls there that could not do
anything. That is a question every player asks once and gets no answer to.

**Four routes the page never called.** An audit of every `/api/…` the page
asks for against every route the app serves found the invite dialog calling
an endpoint that does not exist, and three that existed with nothing behind
them:

- **Room invitations were never drawn.** The coordinator has stored them
  since T7 and the app has fetched them since T11; being invited to a room
  looked exactly like not being invited to one. They are the first thing in
  the rail now, above the friends themselves, because the room they name is
  filling up while they are ignored.
- **Disconnect had no button.** Getting off a room's network without leaving
  the room is the first thing to try when Dota will not find the host, and
  the only way to do it was to leave and come back. It is on the network
  step now, and not offered to the host — their machine *is* the game.
- **The audit log could only be read one way round.** The record showed what
  was done *to* somebody; `GET /api/admin/log?actor=` answers what they have
  *done*, which is the question a head admin reviewing an admin is actually
  asking. Shown for staff only: a player's answer is always empty, and an
  empty panel is a question.
- `POST /api/rooms/invite` stays uncalled on purpose. It is the raw door
  grant; `/api/friends/invite` is the compound gesture the interface wants —
  tell them to come *and* let them through — and doing only the first is how
  somebody is invited and then refused (D41). Withdrawing an invitation has
  no interface yet and would need the coordinator to list a room's invitees.

**The network banner stopped repeating the stepper.** A green line reading
"connected" directly under a green tick reading "connected" spent two of the
room screen's scarcest inches telling somebody what they had just read. It is
kept for one case the three steps cannot express: the reason a connection was
refused.

---

## D60 — Accounts switched on in production

**2026-08-26 (T21).** The owner asked for the new build on the live server so
they could test it, and chose to turn accounts on at the same time.

`coordinator.service` now carries `-db /var/lib/finallobby/db/lobby.db` and
`-terms-file /etc/finallobby/terms-en.md`. Everything that needed a database
started working at once: sign-up and sign-in, sessions, Argon2id passwords,
durable MMR with its once-a-week rule, the friend graph, private messages,
room invitations, last-seen times, roles and the whole moderation record.
Without it the server had been what it was during the two-PC test — type a
name and play — and most of T19 and T20 would have been invisible.

**Why now rather than later.** T11 shipped a client that can sign in, which is
what had been blocking it: turning accounts on before that would have locked
both test PCs out of their own lobby. The cost of the flip is that everybody
must create an account and there is no password recovery (D37), and that cost
grows with every person who installs the app. Today it is two test PCs.

**The database lives at `/var/lib/finallobby/db/`, not beside the installer.**
The unit has `ProtectSystem=strict` and `ReadOnlyPaths=/var/lib/finallobby`,
so the published installer cannot be written by the service that serves it.
The database needs the opposite, and SQLite needs the *directory* writable
rather than the file — the write-ahead log and the shared-memory index are
created beside it. `ReadWritePaths=/var/lib/finallobby/db` wins on the
subpath, so the installer directory stays read-only and the database does
not.

**The relay was redeployed too.** It had been running the 23 August build,
which predates the keepalive echo that makes latency measurable (D54). The
per-player ping the owner asked for would have shown nothing without it.
`/etc/finallobby/relay.key` was **not** regenerated — the deploy script only
generates when the file is absent, and the public key on the server is still
`1e0779…`, which is what shipped clients carry.

**There is no head admin yet.** `-head-admin` takes an account id, and no
account existed when the flag would have been set. It is one restart once the
owner has signed up, and until then nobody can appoint a moderator — which is
correct: an unattended server that grants itself staff is worse than one with
none.

**A `deploycheck` account and one room were created over the real internet**
to prove the path end to end: sign-up against the live terms, a room hosted,
and a ping reported on the heartbeat coming back on the right seat. The room
closes on the host grace. The account is harmless and can be sanctioned or
ignored.

---

## D61 — The front door is allowed to be flashy

**2026-08-28 (T23).** The owner handed over a second mock — *App screens
redesign request* — covering the four screens the Nocturne pass (D58) had not
touched: settings, sign in, create account and the terms. It was adopted.

Everything else in this product is a dense working screen somebody reads in
two seconds, and the whole stylesheet is written against that: small steps
between surfaces, one accent, nothing that moves. The sign-in card breaks all
of it — a halo that breathes, a lit gradient border, a bar of light crossing
the top edge, and a card that rises as it appears.

**That is right, and it is right for a reason that does not generalise.** The
gate is shown perhaps twice in an installation's life, it is the first thing
anybody ever sees of LobbyBaz, and nothing on it competes for attention with
anything else. Motion costs nothing where there is nothing to read. The same
treatment on the room list would cost a player the two seconds they spend
choosing a room, which is the only thing that screen is for.

So: **the door may be flashy, the rooms may not.** Every loop is decorative,
every one of them is dropped under `prefers-reduced-motion`, and none of them
carries information — the states they sit beside are all readable without
them.

**Settings gained a second column and lost a card head.** It was a column of
three narrow cards down the side of a 1440px window, which reads as an
unfinished page rather than a short one. It is now a page header with the
build number in it, an identity banner in the accent — the one place the
product says your own name back to you — and a two-column grid.

**The terms are gated on being scrolled.** Accept does nothing until the text
has reached the end, with two per cent of slack for sub-pixel rounding, and a
bar under the header says how far through the reader is. Consent to a wall of
text nobody scrolled is not consent, and the gate costs an honest reader about
four seconds. The banner that used to offer *Accept* beside *Read them* now
offers only the modal: it was a button for agreeing to something without
looking at it, three inches from the machinery built to prevent exactly that.

**The terms are rendered, not dumped.** They arrive as markdown because a
markdown file is what the owner edits, and the page now turns the handful of
shapes that file uses into elements. It builds nodes rather than markup: that
text is a document somebody types into, and typing into it must never reach
the page.

**Three sections in the mock were not built: Game got half, Audio & voice and
Notifications got none.** They are features, not decoration, and the mock's
README says so itself. Dota's location is real — the service already finds it,
and it now reports the path rather than only a yes — so the Game card shows
where the game is and says what that means. Launch options, push-to-talk, an
input meter and three notification toggles are not real, and a switch that
does nothing is worse than an absent one: it teaches somebody the product
lies. They are the owner's to ask for.

**The terms text moved to 2026-08-28.** The old opening was a note addressed
to the product owner, and every player who signed up read it. It is now the
mock's line — plain language, not legal advice — and two things the old text
was missing came across from the mock: what hosting costs the person who does
it, and that the relay carries Dota and nothing else. Bumping the version
re-prompts everybody, which is the point.

---

## D62 — Every change goes to the server in the same breath as the commit

**2026-08-28 (T23).** The owner asked, in as many words, that every change we
make reach the server so they can test it live.

They are not going to run `try.sh`. They have the installed app and the live
lobby, and that is the product as far as they are concerned. A change that
exists only in this repository is a change they cannot see, cannot judge and
cannot approve, which makes the whole loop — build, look, decide — run at the
speed of somebody remembering to deploy.

**`scripts/ship.sh` is the whole job in one command**, and the finish ritual in
`CLAUDE.md` now names it beside the commit and the push:

1. `deploy.sh all` — the coordinator binary **and the terms text it serves**,
   then the relay.
2. `publish.sh` — the desktop app, stamped, uploaded, and published as the
   installer that installed copies upgrade themselves to.

**Step 2 is the one that gets forgotten**, and it is invisible when it is: the
server is healthy, the API is the new one, and every installed copy is still
drawing last week's interface. Nothing in the health check catches it, because
nothing is wrong with the server.

**The terms file now travels with the coordinator.** It was placed by hand
during D60 and `deploy.sh` never touched it again, so editing `docs/terms-en.md`
changed nothing that any player could read. Bumping `TermsVersion` without
shipping the text it names is how everybody gets re-prompted to accept the old
terms.

**What ship.sh does not do is decide.** It never runs on its own, it is not
wired into a hook, and it is not safe to run half-way: the box carries an
unrelated live business, so both scripts inside it stay the audited ones that
touch UDP 443 and TCP 7001 and say out loud at the end whether the neighbours
are still up.

---

## D63 — The head admin is named, not numbered, and a bad name is loud rather than fatal

**2026-08-28 (T24).** The owner signed up and gave a username: `arman13m99`.
`-head-admin` took an account id, and nobody has ever read an account id off a
screen. It now takes **either**: a username first, an account id second, and a
refusal if it is neither.

**The old flag inserted the grant without looking.** A typo made an account
that does not exist the head admin, silently — and it could not be corrected,
because the second attempt fails with `ErrHeadAdminSet`. That is a one-keystroke
way to lock the product out of ever having staff.

**It lives in the unit file, permanently.** That is what makes a rebuilt server
come back with its staff intact, and it is idempotent: a restart with the same
account changes nothing. But it also means the flag is read on every start of a
live service — so an unresolvable name **no longer exits**. It logs an error
and the coordinator carries on. A lobby full of real players must not go down
because somebody was renamed. Anyone can read the answer out of the journal:

    level=INFO msg="head admin" who=arman13m99 account=a_3427029f79090d91d0de7c18

`arman13m99` has held the role since 2026-08-28 23:24 server time. D47 is
otherwise unchanged: there is still no self-service path to it.

---

## D64 — The address follows the host, so the host can pick a side

**2026-08-29 (T25).** The owner asked that the host be able to switch positions
in a room like everybody else. They were the only person who could not.

**Why they could not.** Slot 0 was the host's for the room's whole life,
because slot 0's virtual IP *was* the room's address: `ipam.HostIP(index)`
returned `SlotIP(index, 0)`, every joiner was handed that constant, and Dota
was launched with `+connect` pointing at it. Moving the host would have moved
their address out from under nine people mid-lobby. So the one person who had
opened a room in order to play Dire was the one person who could not sit in
it — and could not be moved there by anyone either.

**The fix is one sentence: the address is a property of who is hosting, not of
which chair they are in.** `Room.HostSlot` says where the host sits, every
membership derives `HostIP` from it, and `Move` maintains it. `ipam.HostIP` is
deleted rather than left as a helper whose name states a rule the room no
longer has.

Everything else about a seat move already worked, for the nine other people in
the room: the coordinator revokes the mover's ticket because their virtual IP
came from their slot, and the app brings the tunnel back up as part of the
move rather than leaving the player to work it out. The host now uses the same
path. Nobody else's address changes when the host moves, so nobody else needs
a new ticket — they re-read `host_ip` on the next poll, and a room cannot be
locked and moved at the same time, so no match can be running when it happens.

**Three things fell out of it.**

**`SetHost` no longer swaps.** Transferring a room used to move the new host
into slot 0 and whoever was there into the new host's seat, because slot 0 was
the address. That silently moved two people between teams, which was never
what transferring a room meant. Now the new host keeps their seat, `HostSlot`
follows them, and no ticket is revoked at all.

**A returning host reclaims the seat they left**, not the lowest free one.
`Join` used to hand out the first empty slot, which was slot 0 for a host
coming back within the grace window — and would have been the wrong slot the
moment somebody else took it while they were away. That was a latent bug
before this change: a joiner filling slot 0 during the grace window would have
pointed the whole room at the wrong machine.

**The host cannot watch their own room**, and that is now enforced on the
server. It used to be true by accident: the client refused the host *every*
seat, so the observer gallery was covered by the same blanket. Relaxing the
playing seats left the gallery exposed, so `JoinObserver` refuses `HostID`
outright — before the already-seated check, so it holds during the grace
window too, and so the error names the real reason.

Slot 0 is an ordinary Radiant seat now. Anybody may take it once the host has
got up.

---

## D65 — a player may type their own launch options; the plus space stays ours

**2026-08-29.** The owner was shown three proposals the redesign mock had
drawn but the product did not have, and answered them one by one. This is the
first: *"1- ... Want it? yes iw want it"*.

Every Dota player has a line of launch options they carry between machines —
`-novid` to skip the intro, `-high` for process priority, `-nod3d9ex`,
`-language english`, a window size. LobbyBaz builds the command line itself,
so before today that line had nowhere to go, and the answer "you cannot use
your own settings here" is a reason to keep using something else.

The risk is real and worth naming, because it decides the shape of the rule. A
Dota command line has two halves:

- **Console commands, beginning with `+`.** `+connect` names the server,
  `+map` and `gamemode` choose the match, `+jointeam` chooses the side,
  `+sv_lan` decides whether Valve is involved at all. These *are* LobbyBaz:
  they are how a seat in a room becomes a slot in a game.
- **Engine flags, beginning with `-`.** These turn knobs in the player's own
  client. None of them names a server, a map or a team.

**So the plus half is a closed list and the hyphen half is open.** A player's
text is parsed by `protocol/launch`: every token must be a well-formed engine
flag (one hyphen, then lowercase letters, digits or underscores, at most 24
characters) or a plain value belonging to the flag before it. A `+` token is
refused outright, with a message that says why rather than "invalid input". So
is a first token that is not a flag, because then the player has typed
something they did not mean. Two hundred characters, twenty-four words.

**The rule lives in `protocol/` because both ends apply it.** The app checks
the text when it is saved, so the mistake is answered beside the field the
player is looking at rather than four clicks later when the match will not
start. The service checks it again in `dota.ValidateArgs`, because that is the
process boundary and it does not trust the app. Two copies of a rule like this
drift, and the half that drifts is always the one nobody is testing.

**The options go on the command line last**, after `-condebug`, so nothing a
player types can displace an argument the room needs.

**They are stored in the installation's own session file and never sent to the
coordinator.** They describe a graphics card, not a person. The server has no
use for them and is not told.

`ValidateArgs` is weaker than it was for hyphen flags, deliberately: it now
accepts any token `launch.IsPlayerFlag` accepts rather than a list of nine. No
useful list of Dota engine flags exists to keep, and keeping a bad one would
mean rejecting the working setting somebody has used for a decade.

---

## D66 — five notifications, five switches, raised by the tray

**2026-08-29.** The third of the owner's three answers to the redesign mock's
proposals: *"3- Notifications — three toggles (a room opens, a friend comes
online, the tunnel drops) ... yes i want it"*.

Two notifications already existed, from D45: your room filling up, and the
host starting the match. They were raised by the Tauri shell rather than the
page, **because the page is not running when the window is closed to the
tray, and closed to the tray is exactly when a notification is worth
anything.** The three new ones join them there for the same reason. A room
opening in the lobby is only news to somebody who is not looking at the lobby.

**All five are switchable, and all five are shown on one card.** The owner
asked for three switches; the two that already existed went on the same card,
because a screen headed "Notifications" that omits two of the notifications
the product actually sends is a worse lie than one that lists a switch doing
nothing.

**The switches are stored per installation and the coordinator is never
told.** Which interruptions somebody wants is about the machine in front of
them, not about who they are — the same reasoning as the launch options
(D65), and the same place: `session.Config`.

**`Config.Notify` is a pointer, and nil means all of them.** A config file
written before this field existed decodes to nil. Had the zero struct been the
answer, every player who upgraded would have silently lost the two
notifications they already had, and nothing would have looked wrong. The tray
applies the mirror image of the same rule: a `notify` object missing from
`/api/state` means an older app server is answering, so the two old
notifications are on and the three new ones are off — the behaviour that
server actually had.

**Every one is edge-triggered against the previous poll, and the first poll
only establishes what is already true.** Level-triggering would fire every
five seconds for as long as the condition held; announcing the first poll
would greet a player who opened the app with one notification per room in the
lobby and one per friend already online. Both are how somebody learns to
ignore notifications, or to uninstall.

**Three details that are not obvious:**

- **A room is only remembered while it is joinable.** One that fills up and
  empties again is a fresh chance to play, and saying so is the point.
- **"A room opens" is suppressed while you are in a room.** Somebody sitting
  in a lobby of their own is not shopping for another one.
- **"The tunnel dropped" only fires while you are in a room**, and only on the
  connected-to-disconnected edge. Outside a room there is nothing to lose, and
  "not connected yet" is a different event from "dropped".

---

## D67 — the installer ships the window, and the shortcut points at it

**2026-08-30.** The owner: *"currently when i lunch the lobbybaz, opens a cmd
terminal then a browser page, what i need to see is the app only, no terminal,
no browser, the app itself."*

Everything needed to fix this had existed since D45 and none of it was
shipped. `desktop/` is a Tauri shell: a real window, a tray icon, and the
notifications D66 built. It starts `lobbyapp.exe` behind itself with
`-url-only -no-browser` and `CREATE_NO_WINDOW`, and points a webview at the
address it prints. It built. It ran. It was never put in the installer.

`scripts/build-desktop.sh` said so, in a comment, deliberately: *"This is
deliberately NOT wired into scripts/publish.sh yet ... swapping it for one
nobody has installed on a real machine would risk the thing that currently
works."* That caution was reasonable when written and wrong a week later. The
thing that "currently worked" was a desktop shortcut pointing at
`lobbyapp.exe` - a console application that opens a browser tab. So the owner
double-clicked LobbyBaz and got a command window and a browser, and every
report they gave about the product was a report about a browser tab.

**This is the same recurring bug as the observer seats and the invitations,
one layer further out.** A subsystem built, tested and finished, with nothing
in front of it that a person can reach. Every test passed the whole time,
because every test was about the half that worked.

**What changed:**

- `scripts/build.sh` builds `bin/lobbybaz.exe` with cargo as part of
  `installer` and `all`, and packs it into the payload beside the other three.
  A missing cargo is a **failure**, not a skip: an installer without the shell
  reinstates the console window while looking like a successful build.
- The installer writes four executables instead of three. The desktop
  shortcut, the Add-or-Remove-Programs icon and the post-update relaunch all
  name `shellExe`, and stopping an old install kills the window before the
  server, because `taskkill /F` skips the shutdown that would have stopped the
  server itself.
- `installer/payload_test.go` asserts that every listed component is really
  inside the binary. `writeComponent` did already report a missing payload -
  on the player's machine, halfway through an install, after the service was
  registered.

**The fix reaches installed copies through the ordinary update.** They fetch
the new installer, which rewrites the shortcut on the Public Desktop and
relaunches the window. Nobody has to download anything by hand.

`lobbyapp.exe` keeps its console and its browser: that is exactly right for
`scripts/try.sh`, `scripts/live.sh` and the smoke test, which drive it
headless or in a developer's own browser. What was wrong was never the
program - it was which program the shortcut named.

---

## D68 — the lobby and the room, from the owner's third handoff

**2026-08-30.** `design_handoff_lobbybaz_lobby_room/` - a rendered prototype, a
token sheet, and a README that argues its own changes as a diff against
screenshots of the shipped app. The third handoff of this kind, and the most
specific. Adopted.

The information architecture did not change. What moved, and why the owner's
reasons are worth keeping:

**Four things left the top bar.** It carried search, six filter chips, two
status pills and the player's identity in 44px, and four unrelated things in
one strip is four things nobody reads.

- **The filters went down into the room list**, directly above the columns
  they filter. They were a screen's width from the data they act on.
- **The status pills went to the foot of the left rail.** They are machine
  state, not navigation, and they now sit with the tunnel light they belong
  beside.
- **The player's identity went to the top of the friends panel**, as a row
  with an accent edge and a chevron. Identity belongs with the social column.
- **"N online" was deleted.** The lobby already says "5 in the lobby" and the
  room says "2 / 10"; a third count of the same population is a number to
  reconcile rather than a fact.

**The room's network facts moved from the bottom of the screen to the top.**
They were a run-on line - `10.87.0.7 · 10.87.0.2 · 37 ms` - with nothing
saying which address was which, in the last place anybody looks. They are a
labelled five-cell strip in the room's header band now: host, players, your
address, host address, room network. These are the first things somebody
checks when a match will not start.

**The three step cards became one line behind an (i).** They held a permanent
band of the screen teaching a flow you learn once. The strip is closed by
default and remembered per installation.

**That is only safe because the stat strip took over the stepper's real job.**
The stepper existed because the commonest failure in the two-PC test was two
players in a room, neither on its network, with nothing on screen saying which
step had not happened. The network cell says exactly that, permanently, and it
is also the control: it carries the join and leave links that were buried in
step two.

**One action, and it does both halves (owner's decision).** Create Game when
the room is yours, Join Game when it is not, in the same place either way -
GameRanger's convention, which is the thing our players already know. It
brings the tunnel up **and** opens Dota, because those were two deliberate
clicks and the second was the one people forgot.

**The chaining lives on the server, in `/api/playnow`, and that is the whole
of why it is safe.** The tunnel reports "connecting" the moment the service
accepts it; the Noise handshake finishes some time later. A page firing
`/api/connect` and `/api/play` back to back would hand Dota a host address
this PC could not yet route to, and the failure would look like a broken room
rather than a race. `waitForTunnel` polls the service until it is connected,
for at most twenty-five seconds, and names the two things that actually cause
a failure when it gives up.

**Four watching seats, two per board (owner's decision).** They were five, in
a full-width panel of their own below the two teams. Five cannot be split
evenly between two sides, and the owner chose the mock's arrangement over the
extra seat. `ipam.ObserverSlots` is 4; **`adminBaseOffset` is now pinned at 17
rather than derived**, so dropping a seat from the gallery did not move every
moderator's address down by one. The gap at `.16` is deliberate and cheap -
eleven of a room's thirty-two addresses were already spare.

**"Sit here" became "Empty".** A label should say what a seat is, not what to
do with it; the affordance is the row lighting up under the pointer. Eight
rows each reading "Sit here" is the instruction printed eight times.

**Two things were adopted in spirit rather than to the letter.**

The mock draws every other player's avatar in one violet. That would undo the
thing faces are for here - a colour derived from the account id, so the same
person is the same colour on every screen and two players called Pudge are
told apart. **So other people keep their own colour and your own face is
always the same green**, which is the half the mock was actually buying: you
can find yourself in a list of ten without reading a name. The greens are
reserved in `hueOf` so nobody else's hash can land on one.

The mock's "clicking your own seat vacates it" was not built. There is no
state in this product for being in a room without a seat; the nearest real
thing is leaving the room, which has its own button and says so.

**The room footer is stuck to the bottom of the stage.** A band, ten seats,
four watching seats and a footer come to about thirty pixels more than a
1366x768 laptop has, and thirty pixels is enough to hide Leave room
completely. The boards scroll under it.

---

## D69 - a host in a match locks their own room, without pressing anything

**2026-08-30.** From the owner's live test: *"there should be statuses that
checks the host, if he is in game then users cant change places and they can
only leave the room."*

The room already had the right status. `locked_in_game` has existed since T5,
it stops new players joining and it stops seats moving, and the product rule
behind it is the owner's own. What it did not have was anybody to press it.
**The host is the one person in the room who cannot**: at the moment it
matters they are in Dota, not in this window.

So the coordinator observes it instead. Every client's service already reports
whether Dota is running on its PC - it launched the game and watches its log -
and the registry has stored that since D41 for the friends rail. `Store.Tick`
now folds the host's own flag into their room on every tick, and a room whose
host is in a match is locked for as long as that is true.

Three consequences, all deliberate:

- **Seats do not move while the host is playing**, for anybody, including the
  host. Changing team halfway through a match puts a player on the wrong side
  inside Dota, and nothing here can undo that.
- **Nobody new joins**, unless the host explicitly reopened the room. That
  control - `open_to_new_players` - is for refilling a slot somebody
  abandoned mid-match, and it would be useless if being in the match
  cancelled it. It overrides the automatic lock for joining and not for
  moving.
- **It lets go on its own** when the match ends. The room outlives the game
  (D40): the ten people who just played are the ten who want to play again.

The status is derived in the view rather than written into the room, so a room
that is `open` and whose host is in a match reads as `locked_in_game` to every
client at once - the lobby list, the room screen, the CLI - and reverts with
no second write to undo.

**What this fixed on the client, and it was not the obvious half.** The room's
one button used to refuse to work in a locked room for anybody who was not yet
on its network. That was written when the only way to lock a room was the host
pressing Lock, and it made the ordinary flow impossible the moment locking
became automatic: the host presses Create Game, the room locks because they
are now in a match, and the nine people who were about to press Join Game find
the button disabled. Locking decides who may **come in**. They are already in.

---

## D70 - a host who leaves closes the room; a host who vanishes gets the minute

**2026-08-30.** From the same live test: *"i left a room but still there was
the room left on the lobby and i could join it as the host again."*

Exactly right, and it was two bugs wearing each other's clothes.

D40 says a room closes a minute after its host goes, and that minute is the
host's chance to reconnect and save the match. The implementation started that
timer in one place only: `Room.Leave`, which is what the Leave room button
calls. So the two cases were precisely the wrong way round.

- **A host who pressed Leave** got the grace period. Their room stayed in the
  lobby for a minute, labelled open, joinable, and they could walk straight
  back into it as its host. That is what the owner saw.
- **A host who crashed** got nothing at all. Nothing calls `Leave` when a
  machine goes away, so the timer never started, and the room stayed in the
  lobby for as long as the coordinator ran. The case D40 was actually written
  for was the case that never worked.

They are different events and they are separated now.

**Leaving is a decision.** `Store.Leave` closes the room there and then when
the person leaving is its host: it goes out of the lobby immediately, every
ticket in it is revoked, and its chat is dropped. Nobody is left staring at a
room whose host has told them they are done.

**Disappearing is not.** `Store.Tick` asks the player registry, once per tick
and per room, whether the host has been heard from inside the presence window.
A host who has not starts the minute; a host who comes back inside it stops
it, and their seat was never vacated, so there is nothing to reclaim. Ninety
seconds of total silence ends a room - thirty to be counted absent, sixty of
grace - which is well inside the sticky-address window, so a host who returns
keeps their address and the room never noticed.

**A room in its grace window says so.** It reads `host_away` in the lobby and
in the room screen, drawn as "Host away". It stays joinable on purpose: the
host coming back **is** a join, and it is the only thing that saves the room.

The cost is that an admin can no longer rescue a room whose host left on
purpose - `SetHost` refuses a closed room. That is the right way round. The
escape hatch exists for a host who disappeared, and that case still has its
minute.

---

## D71 - the page only redraws what changed

**2026-08-30.** From the same live test: *"whenever i type something in chat,
the app glitches even when i change places in the room."*

Every list on the screen was emptied and refilled on every poll - the lobby,
the friends rail, the twenty seat cards, the five stat cells, the chat tab
strip, the announcement strip, the moderator's three tables - whether or not
anything in them had changed. Twice a second, forever.

Two seconds is short enough to see. A scrolled list jumped back to the top
under the reader, because emptying a tall container clamps its scroll offset
and refilling it does not put it back. A hovered row lost its highlight and
took it again. And a click that arrived on the wrong side of a rebuild landed
on an element that had ceased to exist half a frame earlier, which is why it
looked worst exactly when somebody was doing two things at once: typing into
the chat while moving seat.

The chat log has never had this problem, because `drawLog` has compared a
signature since it was written and the comment above it says why. That guard
is now a two-line function every panel uses.

The rule it comes with: **the signature must name every input the panel draws
from**, not just the argument it was handed. A seat card is drawn from the
room's members, but also from who this player is and from whether the room
will accept a seat change at all; a stat cell mostly from this PC rather than
from the room. A signature that misses one of those is a panel that stops
updating, which is a worse bug than the one being fixed and a much quieter
one.

Not everything is guarded, and the line is deliberate: a panel that sets
`textContent` on an element it did not create is left alone. Writing the same
string twice is invisible and costs nothing. It is destroying and rebuilding
subtrees that people are pointing at, scrolling and typing into that had to
stop.

---

## D72 - the owner's stylesheet pass, adopted

**2026-08-30.** The owner delivered `app.css` and `app.js` read line by line
against the D68 components, with the findings written up in
`docs/2026-08-30-ui-fixes.md`. Seventeen defects, no markup and no strings
touched. Adopted whole; the write-up is the record and is not repeated here.

Four of them are worth naming, because each is a rule rather than a fix:

**A media query written above the rule it overrides does nothing.** The
`max-width: 1440px` block sat before the base `.chip` rule. Same specificity,
so the later declaration won at every width and the laptop adaptation had
never once applied. Moved below. Anything narrowing a base rule has to be
written after it.

**A declaration written twice is a rule somebody will read and believe.** Four
pairs - `.slot-name`, `.room-ping`, `#screen-room` and `@keyframes blink` -
where the first was dead and the second was the truth. The blink one had been
quietly deciding how the rail's tunnel light pulsed from four hundred lines
away, in the front-door section.

**A comment that describes behaviour has to be checked against it.** The
lobby's decorations carry a comment saying both are dropped whole under
`prefers-reduced-motion`. The media query listed neither, nor the Create room
pulse, nor the tunnel blink. A comment that is wrong is worse than no comment:
the next person reads it instead of the rule.

**Two rules were not direction-agnostic**, in a file whose own header says
every rule is. Both are transforms, which have no logical form: the sweeping
light travelled off the panel immediately under `dir="rtl"`, and the
magnifier's handle pointed into its own lens. Each has an explicit RTL rule
now. The general point holds - **a transform is the exception to D44 and needs
a `[dir="rtl"]` companion**, because there is nothing logical to write instead.

Beyond the defects it added two things the interface did not have:

- **Escape and a backdrop click close a dialog.** Six of them had exactly one
  way out. Escape presses the dialog's own close control rather than hiding
  it, so each still does its own cleanup. The name gate is excluded on
  purpose: there is no application behind it to go back to.
- **Room rows, empty seats and friend rows are reachable from the keyboard.**
  All three are whole-row targets, which is correct - the row is what a player
  aims at - and all three were `div`s with an `onclick`, so none had a tab
  stop, a role or a key. `pressable()` gives them exactly those three.

**One thing it changes that the owner should look at.** D68's chat dock was
212px; the owner asked for a fifth more on 2026-08-30 and got 254. This pass
gives 60px of that back below 820px of viewport - which is every screen the
product is actually used on - and the room screen fits a 1366x768 laptop
completely for the first time: ten seats, four watching seats, the footer, no
scrolling. The two cannot both be had at that height. The room won, which is
the defensible way round, and the owner has been told.

---

## D73 - the exactness pass, and what it found under D71

**2026-08-30.** Round two of the owner's stylesheet pass, appended to
`docs/2026-08-30-ui-fixes.md`. Mostly colour, and one finding that matters more
than the rest of it put together.

**D71 did not fix the flicker. It moved it.** Guarding each panel on a
signature is right, but three of those signatures carried a *live measurement*:
`renderRooms` stringified whole room objects including `host_relay_ms`,
`drawStats` carried `state.relay_ms`, and the seat boards carried each member's
`relay_ms`. Relay pings arrive fresh on every two-second poll, so all three
signatures changed on every poll and every one of those nodes was thrown away
and rebuilt exactly as often as before. The guard was there and it was never
once true.

Three changes, and the first is the rule:

- **A signature must not contain a number that moves by itself.** A `steady`
  replacer drops `relay_ms` and `host_relay_ms` from every signature that is
  built.
- **A number that moves by itself is painted into a leaf of its own.**
  `paintRoomPing`, `paintSeatPings` and the `.mspill` in the stat strip write
  the new value into an existing node. A new measurement is a new number, not
  a new board.
- **The lobby reconciles per row.** Rows are matched by room id, only rows
  whose own signature changed are rebuilt, and only rows that actually moved
  are re-inserted. One room gaining a player used to rebuild all forty.

**The strip that could not be put away.** `lease expired locally` comes from
`netservice/internal/watchdog/lease.go` and the service goes on reporting it,
correctly - a tunnel that tore down has not come back. Nothing was latched. What
was missing is what a message like that needs and never had: **something to
press, and a way to put it away.** The strip carries a Reconnect button (only
for `tunnel_error`, the one of the five a player can act on) and a dismiss, and
a dismissal is remembered by the exact sentence - so it stays down while the
condition is unchanged and comes back up when the message changes.

**The status colours had been muted into greys.** `--good` was a sage at
`#a9cbb5`. Every status in this product is drawn at six to eight pixels, and at
that size a desaturated colour is not a quiet colour, it is a grey one.
Restored to the design's values, with the soft and line tints made translucent
because a status pill in here sits on four different grounds and an opaque tint
matches exactly one of them.

**Two tests were widened, and this is the part to read before changing them
back.** `TestTheRendererOnlySelectsClassesThatExist` and
`TestTheRendererOnlyReadsDataAttributesThatExist` ask whether a name the script
uses also exists in the markup. That is the right question only for names the
markup is supposed to supply. A class the renderer creates (`.slot`) and an
attribute it stamps on a node it created (`data-seat`, `data-room`, `data-msg`)
are answered by the script, not by the document, and both tests were failing on
them. They now also gather what the script itself makes: classes from
`className =`, `classList.*` and `el(tag, "...")`, and data attributes from any
`dataset.x =` or `delete ....dataset.x`. `sig` no longer needs its special
case, because it is caught by the same rule that catches the other three.

Both were checked against a deliberately wrong selector afterwards, and both
still fail on one. **A guard that has been widened and not re-tested is a guard
that has been removed.**
