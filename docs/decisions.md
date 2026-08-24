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
