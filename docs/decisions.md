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

> **The grace period is gone — see D84 (2026-08-31).** A host who leaves or
> drops closes the room immediately. The half of this decision that still
> stands, and stands more firmly than ever, is the other half: the match
> ending does nothing to the room.

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

## D74 - a virtual address belongs to the player, not to the seat

**2026-08-30.** The owner, on the live build: *"when i change a seat, it
disconnects and connects to network, and this method is not smooth and UX
friendly."*

They are describing a tunnel teardown, and it was not a bug in the sense of
something written wrong. It was the direct and correct consequence of a design
choice, taken in D57 and left alone since, that nobody had looked at again.

**What it was.** A player's virtual IP was `roomBase + 2 + slotIndex`. The
address was a function of the seat. So `Move` genuinely did change a player's
address, which genuinely did invalidate the ticket naming the old one, which is
why D57 revoked on every successful move: anti-spoof compares the inner source
IP against the session's assigned address and would otherwise drop everything
that player sent. The app then had to bring the tunnel back up, synchronously,
inside the click handler - up to twenty-five seconds of being disconnected
because somebody wanted to play Dire instead of Radiant.

**What was wrong with it.** Nothing about the tunnel depends on the seat.
`ticket.Claims` carries `PlayerID`, `RoomID` and `VirtualIP`, and has never
carried a slot. The seat is a fact about who is on which team; the address is a
fact about which machine to send packets to. Deriving one from the other was
convenient arithmetic and nothing more, and it welded a cosmetic action to a
network action.

**What it is now.** `Room.Addr` is an array of player ids indexed by address
offset. A player takes the lowest free index when they join, keeps it for as
long as they are in the room whatever seat they sit in, and releases it when
they leave. `AddressOf(playerID)` is the only way to ask; `SlotOf` answers the
unrelated question. `Move` touches neither `Addr` nor any ticket, and the
`RevokePlayerRoom` call in `moveSlot` and the `tunnelUp` in the app's
`takeSlot` are both gone. Changing seats is now a write to a list on the
server, one sync tick, and nothing else.

The host is not special here. Their address is theirs by the same rule, so D64
survives intact - the room's `HostIP` is `AddressOf(HostID)` rather than a
function of `HostSlot` - and a host who crashes reconnects holding the address
every client in the room is already sending to.

**The invariant to hold onto, and the tests that hold it.** *An address is
stable for the lifetime of a membership.* Four tests in `room/store_test.go`
state it directly, including one that watches a host keep their address through
the whole grace window, and `smoke.sh` reads a player's `virtual_ip` before and
after a real seat move over the real API and refuses to pass if the two differ.
The oldest of those tests, `TestTheHostTakesTheRoomAddressWithThem`, had to be
rewritten: it asserted the old behaviour, and a test that asserts what you have
decided to stop doing is not evidence, it is inertia.

## D75 - the reconciler that appended, and the rung that would have caught it

**2026-08-30.** The owner: *"after creating a room my room kept duplicating the
lobby."* Reproduced immediately - three rooms in the server's answer, five rows
on the screen, growing by one every poll.

**The bug.** D73 replaced the lobby's wholesale redraw with a per-row
reconciler: keep a map of the rows already drawn, match incoming rooms by id,
rebuild only the rows whose signature moved. The map is drained as it goes and
whatever is left in it at the end is removed. The drain was missing on one
path. A row that was found and reused was never deleted from the map, so it was
still there at the end, so it was removed - and then the room was appended
again on the next poll, and the poll after that. The room the player had just
created was the one at the front of the list, which is why it was the one that
multiplied.

The fix is four lines. What is worth writing down is that this class of bug had
now reached the owner three times: the chat glitching while they typed (D71),
the panels rebuilt by a ping that moves on its own (D73), and this. Three
reports, three different symptoms, one shape - **the renderer is correct on the
first draw and wrong on the second** - and every rung of the harness looked at
the first draw.

**So there is now a rung that looks at the second.** `scripts/uicheck.sh` boots
the same throwaway sandbox as the rest of the ladder, drives the real page over
the DevTools Protocol, and pushes fabricated state through the renderer
repeatedly. It asserts eight things: that a lobby of three rooms draws three
rows through five signature changes and not eight; that a poll returning
identical data leaves the existing rows physically untouched (each row is
branded before the poll and the brand must still be there after); that a ping
moving repaints a number rather than replacing the card; the same three for the
room's fourteen seat cards; that the friends rail survives a no-op poll; and
that every dialog closes with Escape. A ninth check reads the console and fails
on anything in it.

**It was verified by putting the bugs back.** All three, one at a time. It
reported "8 rows in the document for 3 rooms", then "the row was thrown away
and rebuilt by a poll that changed nothing", then "a number that moves by
itself rebuilt the whole row". A check nobody has watched fail is not yet a
check - the same lesson as the end of D73, learned again on the same day, which
is how it earned an entry of its own.

An earlier attempt put this assertion in `smoke.sh` instead, counting rows in a
single render. It passed on the broken code. It was deleted rather than kept,
because a green check that cannot go red is worse than no check: it is a claim.

**Where it sits.** `scripts/verify.sh` is now the one command - check, smoke,
uicheck, chatcheck, termscheck, cheapest first, one verdict, and it keeps going
after a failure so a single run says everything that is wrong. `verify.sh fast`
is the unit rung alone for the middle of a change. `preview.sh`, `try.sh` and
`live.sh` stay outside it: two of them produce something only a person can
grade, and the third talks to the live server.

## D76 - the accounts database is copied hourly; the registry forgets people

**2026-08-30.** Two findings from reading the coordinator as a thing that has
to run unattended for months rather than as a thing that has to pass its tests.

**There was no backup of anything.** `/var/lib/finallobby/db/lobby.db` holds
every account there has ever been: usernames, Argon2id hashes that nobody can
recover, the friend graph, the moderation record, and who accepted which
version of the terms. Nothing anywhere held a second copy of it. A corrupt
file, a bad migration or a fat-fingered `rm` and the product restarts from zero
accounts with no way to tell anybody why.

`store.Backup` writes one with `VACUUM INTO`, hourly, keeping twenty-four.
Deliberately not `cp`: the database runs in WAL mode, so the file on disk is
only part of the truth at any instant, and a copy taken while the coordinator
is writing **opens cleanly and is wrong** - the worst kind of backup, because
it looks like one. `VACUUM INTO` asks SQLite for a consistent copy, needs
nothing installed on the server, and writes a file that is already compacted.
The first copy is taken a minute after start rather than an hour, so a rebuilt
server has one before it has a night. The test opens a copy and compares its
schema version to the live one, because a file of nonzero size is not evidence.

Running without `-backup-dir` is still allowed - it has to be, for the sandbox
and every test - and logs a warning that says what is at stake rather than the
name of a missing flag.

**The registry only ever grew.** `player.Registry` gains an entry for everybody
who connects and had no way to lose one. Over a month that is every player who
has ever opened the app, held in memory, in a process designed to run for
months. Nothing reads a player who has gone home: presence, the friends rail
and last-seen times all come from the accounts table, which is on disk and is
the durable copy. `Registry.Sweep` forgets anybody silent for two hours, on the
timer that already expires rooms and purges tickets.

**Except anybody sitting in a room**, whatever their last-seen time says. A
player who has been quiet for an hour and is still holding a seat is a host in
their grace window, or somebody with Dota in the foreground and the app behind
it. Forgetting them blanks their name on nine other screens. That exception is
the whole reason `Sweep` takes a `keep` set rather than just a timestamp.

**And one thing about deploying, found while doing the above.** The unit now
names `/var/lib/finallobby/backups` under `ReadWritePaths`, and systemd refuses
to start a service whose `ReadWritePaths` points at a directory that is not
there. `deploy.sh` created directories on the relay path only, so a rebuilt
server would have come back with a coordinator that would not start.
`deploy_coordinator` now prepares its own host - the unix user and every
directory the unit names - the same way `deploy_relay` always has.

## D77 - the lease renewal the service was never allowed to make

**2026-08-30.** The owner: *"i have to keep Join network after a while being in
the room, why does it disconnect? Room network / not connected / join. and also
the error at the top shows for lease expiry which it shouldn't happen."*

Every match on the live product was ending three minutes after it started.

**The chain.** `POST /v1/lease/renew` was registered behind `signedIn`. With
accounts on - which is production, since D60 - `signedIn` refuses a request
carrying no `X-LobbyBaz-Session` header. The only caller is the watchdog inside
the Windows service, which holds a ticket and the shared bearer token and has
never held a session: sessions belong to the desktop app, and the service
outlives it and runs as LocalSystem. So the coordinator answered 401 to every
lease check, thirty seconds apart, for every player in every room.

The client was not wrong about any of it. `leaseChecker` reads 401 as
`VerdictUnreachable` on purpose, because without an answer it genuinely cannot
tell a valid lease from a revoked one, and claiming valid would mean anybody
who can black-hole the coordinator gets an unrevokable session. The watchdog
then failed closed after its three-minute local expiry, exactly as designed.
Every part behaved correctly and the product did not work.

Confirmed against the live server before changing anything: a POST to
`/v1/lease/renew` with the bearer token and no session returned **401**.

**The fix is the route.** Renewal is `s.limited` now: bearer token, rate limit,
no session. The ticket is the credential here, the same as on
`/internal/validate-ticket` - thirty-two random bytes naming one player in one
room, revocable the instant anybody is kicked or a room closes. Whoever holds
it already holds the tunnel, so renewing grants nothing they do not have. There
is a comment above the route saying so, because the change looks like a
loosening and is not one.

**Why nothing caught it.** `TestLeaseRenewKeepsALongMatchAlive` has existed
since the watchdog did, and it passes. It runs on `newHarness`, a coordinator
with **no account database**, where `signedIn` is a deliberate no-op. The test
was green for the entire time the feature was dead in production, and it will
stay green forever, because it is testing a configuration nobody runs.

This is the third time this repository has shipped a subsystem that passed its
own tests and no real caller could reach. The rule that came out of the first
two was *build the client's door in the same commit*. The rule this one adds
is narrower and sharper: **a test that exercises a code path through a
configuration production does not use has not tested production.** The account
database is not a detail of the environment; it changes which requests are
allowed.

So the door is now knocked on the way the service knocks on it, twice:

- `TestTheServiceRenewsALeaseWithoutASession` in `auth_test.go` - the
  accounts-on rig, a real ticket, no session header. It fails with the
  production 401 when the route is put back behind `signedIn`; that was
  checked, not assumed.
- a section in `smoke.sh` that signs an account up over the real coordinator
  API, hosts a room, and renews the ticket with no session over HTTP. It also
  fails on the old route, and it says what the failure costs: *"every match
  ends three minutes in"*.

Two more tests came with them: an unknown ticket must answer 200 with
`valid:false` rather than an error status, because the watchdog reads any
non-200 as "cannot tell" and would wait three minutes to act on a clear no; and
a revoked ticket must stop renewing at once, which is the property `signedIn`
was never providing and the ticket table always was.

**The second half: the message.** The owner also said the error should not be
there, and they were right twice over. `w.check(ctx)`'s error was discarded -
`verdict, _ :=` - so a coordinator refusing us looked identical to a dead
network for three minutes and then reported itself as *"lease expired
locally"*. That sentence is true about our own timer and says nothing about
what happened, which is why the search started at the network and not at a
route table. The watchdog now:

- logs every unanswered check at Warn with the error, how many in a row, and
  how long is left before it tears down - so the failure is visible in the
  service log within thirty seconds instead of invisible for three minutes;
- keeps the last error and names it in the teardown reason, so what reaches
  the app is `lease expired locally: lease check unauthorised`.

And the app stops showing the service's internal wording to a player.
`tunnelErrorKey` maps the reasons to `err.tunnel_revoked`, `err.tunnel_lease`
and `err.tunnel_stopped`; the raw text still travels for the log and the
diagnostics upload, and is still the fallback for a reason nothing recognises.

That created a real gap in the i18n guard, which only ever read `index.html`
and `app.js`: a key named in Go was invisible to both halves of it - it looked
unused to one test and undefined to nothing at all. `keysUsed` now reads
`server.go` too. Both tests were then checked against a Go-named key that does
not exist and a catalogue key nothing names, and both still fail.

**The third half: it comes back by itself.** Even with the lease fixed, a
tunnel that goes down mid-match stayed down until somebody noticed a banner and
pressed a button - on a screen they were not looking at, because they were
inside Dota. `recoverTunnel` reconnects on its own, and is deliberately narrow:
only after a lease expiry, never after `authorisation revoked`, which is a kick
and not something an app should argue with; only while the coordinator still
says this player is seated, which `pull` has already cleared otherwise, so the
loop ends by itself; and once every thirty seconds at most, with one attempt in
flight at a time, because status is polled every couple of seconds.

**What was deliberately not changed.** The three-minute local expiry and the
fail-closed policy stay as they are. They are the reason a revoked player
cannot keep playing by unplugging their network cable, and nothing about this
bug was their fault. Whether three minutes is the right number for a domestic
network that drops is a product question and belongs to the owner, not to this
entry.

## D78 - the fifteen-second write timeout on a thirteen-megabyte file

**2026-08-31.** The owner: *"An update (2026.08.30-2033) could not be
downloaded: the download was interrupted: unexpected EOF."*

**The coordinator's `http.Server` carried `WriteTimeout: 15 * time.Second`.**
That is one deadline covering an entire response, armed when the request
headers are read. It is exactly right for an API answering in milliseconds,
and it was also being applied to the installer - thirteen megabytes of it -
so anybody who could not pull the whole file at roughly 900 KB/s had their
connection cut mid-body. A Go client copying against a promised
Content-Length calls that `unexpected EOF`, which is the message the owner
read.

Reproduced against the live server, deliberately, before anything was
changed: three unthrottled downloads from this PC completed in 13.0, 15.2 and
13.1 seconds - the second one inside the margin by luck. Throttled to 300
KB/s with `curl --limit-rate`, the same download stopped dead at **7,693,328
of 12,960,256 bytes**.

The audience for this product is on Iran's domestic network. Almost nobody on
it can sustain 900 KB/s to a box in Tehran. **This was not only the update
failing; it is very likely that most first-time downloads of the installer
have been failing since the download page existed**, and the ones that
succeeded did so because a browser retried with a Range request without
telling anybody.

**The fix, server side.** The download handler now sets its own write deadline
with `http.NewResponseController`, and only for the installer:
`InstallerWriteWindow` is thirty minutes, about 7 KB/s across the whole file.
Slower than any connection this product is usable on, and still bounded, so a
client that opens the download and stops reading cannot hold a connection open
for ever. The server-wide fifteen seconds stays for everything else, which is
where it belongs.

**The fix, client side.** The server's timeout was the cause here, but it is
not the last interruption that will ever happen: thirteen megabytes across a
domestic Iranian route is not a transfer that either completes or fails
cleanly, it is one that gets interrupted. `selfupdate.Download` used to throw
away every byte it had on the first error and report failure. It now resumes -
four attempts, two seconds apart, asking for `bytes=<have>-` each time. The
coordinator serves the installer through `http.ServeContent`, which honours
Range already, so this needed nothing new on the server.

Two details of that worth keeping:

- **A server that ignores the Range and answers 200 must start the file
  again**, or the first attempt's bytes end up glued in front of the second's.
  This is handled, and the test for it caught a real bug in the first version
  of the code: the caller emptied the file *after* the fetch had already
  refilled it, deleting exactly the bytes it had just gone and got.
- **The hash is computed by re-reading the finished file**, not accumulated as
  the bytes arrive. With resumption they do not all pass through one stream,
  and a hash assembled across attempts is one nobody can check by hand.

**The test.** `TestASlowDownloadIsNotCutOff` runs a real `http.Server` with a
real `WriteTimeout` - `httptest`'s default has none, and would have proved
nothing - and reads the body in bites with pauses between them.

It also had to be written twice. The first version used a one-megabyte
payload and passed without the fix, because a megabyte disappears into the
kernel's socket buffer and Go's own: the server finished writing before the
slow client had read any of it, and no deadline was ever crossed. **A test of
a timeout has to make the writer actually block.** At thirty-two megabytes it
does, and it fails without the fix with the owner's own words - *the download
was interrupted after 14,647,808 of 33,600,000 bytes: unexpected EOF*.

That is the second time in two days that a check had to be watched failing
before it could be believed (D75), and the second time the first version of it
was a claim rather than a check.

## D79 - the gallery is a seat like any other, and the actions moved into the facts

**2026-08-31.** Four things from the owner, one of them much larger than it
looks:

> all players including the host can switch positions to Watchers/observers as
> well
> remove the text "Radiant vs Dire · 10 seats · 4 watching"
> move Invite / Room settings / Create Game to the same box as the [facts]
> with correct alignments

### The gallery

Two rules were in the way, and both were the host's alone. `JoinObserver`
refused `r.HostID` outright, on the grounds that the match runs on the host's
machine so they cannot go and watch it. And watching was not a seat you could
*move* to at all: it was a different door. A player who wanted to watch had to
leave the room and come back in through `JoinObserver`, and a watcher who
wanted to play had to leave again.

Both are now gone. `Move` takes a `watching` flag and treats the four seats in
the gallery as destinations like the ten on the boards. Anybody seated may go
either way, the host included. The host's PC goes on running the listen server
whether or not they are playing in the match on it, which is an ordinary thing
to want - somebody hosting for nine friends and sitting it out.

A moderator still cannot. Their seat is reserved outside both areas, and the
whole point of the reservation is that a full match plus a full gallery can
never keep them out; letting them vacate it to sit down and play would hand it
back to the room.

### The part that was not asked for and had to happen anyway

**A watcher's address used to be derived from their seat.** Players drew from
`Room.Addr`, a pool that follows the person (D74); watchers were addressed by
`ipam.ObserverIP(roomIndex, seat)` out of a range of their own at `.12-.15`.

So a naive implementation of what the owner asked for would have changed
somebody's virtual IP the moment they moved into the gallery - invalidating
the ticket that names it, and dropping their tunnel. **That is precisely the
bug the owner reported two days ago and D74 exists to remove.** It would have
come back wearing a different hat, on a feature request that says nothing
about addressing.

So the pool covers both. `ipam.MemberSlots` is fourteen - a full match plus a
full gallery - and `ipam.MemberIP` indexes `.2` upward across what used to be
two ranges. Nothing moved: the player and observer ranges were already
adjacent, which is the only reason one pool across both was possible without
renumbering a single address. Moderators keep `.17-.19` and `AdminIP`;
`SlotIP` and `ObserverIP` remain, because the layout they describe has not
changed and the tests that pin it are written in their terms.

`Store.Membership` now sends watchers through `membershipFor` like players, and
`membershipFor` reports the seat kind it actually finds rather than the
hardcoded `SeatPlayer` it used to return - which was true while only players
came through it and became a lie the moment watchers did.

The invariant, restated so it covers everything: **an address belongs to the
person for as long as they are in the room, whatever seat they are sitting
in.** `TestGoingToWatchDoesNotChangeYourAddress` states it, and was checked
against the old observer addressing: it fails with *going to watch moved the
address from 10.87.0.3 to 10.87.0.13 - the tunnel would drop*.
`smoke.sh` walks the same path over the real API, for a player and for the
host, and the host's is the one that matters most - the address every other
client in the room is sending to must not move because the host went to watch.

One more piece of bookkeeping: `HostWatching` records which array `HostSlot`
indexes. A host whose PC dies while they are watching comes back to watching;
without it the grace window would quietly put them on a team.

### The band

The room's three actions were in the title row and the five facts were below a
rule, with the width between them empty. They are on one recessed panel now,
facts leading and actions at the trailing edge, centred against the cells.

The thing that starts a match is now beside the cell that says why it will
not, which is the argument for the move rather than the tidiness. `#roomstats`
is emptied and refilled on every redraw, so the buttons are its sibling and
never its children - putting them inside would have deleted them twice a
second.

`room.foot` - "Radiant vs Dire · 10 seats · 4 watching" - is deleted. It
described the screen it was printed on: two boards, ten seats and four
watching seats, all of them visible directly above it.

### One thing the owner may want to decide later

Ten playing seats and a host who may be in none of them means a room can now
sit at nine players plus a watching host and look full to nobody. Nothing
breaks - the match runs on their PC either way - but "Players 9/10" with the
host in the gallery is a state that did not exist before, and whether the
lobby should say something about it is a product question, not a technical
one.

## D80 - the game mode belongs to the room, and Create Game became Start Game

**2026-08-31.** Four things from the owner:

> change "Create Game" to "Start Game"
> Add gamemode to the host creation options and also add the mode selected by
> the host to the [facts band]
> map dota gamemode 1,2,3,4, etc. search and use the actual Mode Name instead
> of number
> these modes will be wired to the Start Game, so when host starts the game
> instead of simple map dota gamemode 1, they get the selected mode

### What was already there, and why it did not count

A game mode dropdown existed. It sat in Room settings, it was read at the
instant the host clicked the button - `mode: Number($("mode").value)` inside
the click handler - and it was stored nowhere and shown to nobody.

That is not a room's game mode. It is a number in one person's browser. Nine
people who joined to play Captains Mode had no way of finding out what they
had joined; the host's own window was the only thing that remembered, so a
reconnect forgot it; and nothing but that one click ever read it, which is why
it survived four months without anybody noticing it was not connected to
anything.

The mode is a property of the room now. The host sets it when they open the
room or in Room settings, the coordinator stores it, every screen in the room
draws it, and the host's own PC asks the coordinator for it at the moment of
launch rather than trusting the page to say.

### One list, in protocol/

The mode list existed twice before this change - a `map[int]string` in
`netservice/internal/dota` and six `<option>` elements in `index.html` - and
they already disagreed: the map had Ability Draft and Turbo, the menu did not;
the map said "Captain's Mode", the menu said "Captains Mode".

It is `protocol/gamemode` now, and everything reads it: the coordinator
validates against it, the Windows service builds `gamemode N` from it, the CLI
prints it, and `lobbyapp/mode_test.go` binds the menu in the markup to it by
id and by key, because that one cannot be shared - every option is translated
through the same catalogue as every other label, so the menu has to be markup
and the service validating a command line has no browser in it.

The IDs are Valve's `DOTA_GameMode` enum and are wire values that reach a real
Dota command line, taken from
`SteamDatabase/GameTracking-Dota2/Protobufs/dota_shared_enums.proto`. Twelve of
the twenty-six are offered: the ones ten people on a host's own listen server
can actually play. The tutorial, the event modes, the coaches' challenge and
everything that needs Valve's matchmaking to mean anything are not in a menu
here, and `TestAModeTheServiceWouldRefuseIsRefusedAtTheDoor` names the tutorial
specifically.

### Where it is refused

Three rules, all server-side, all with a reason that is not tidiness:

- **A mode we do not offer is refused when it is set**, not when Dota starts.
  Accepting it would push the failure to the moment the host presses Start
  Game, where the only thing on screen is "rejected argument".
- **Not during a match.** The mode is fixed on the command line when Dota
  starts, so changing it mid-match would change every screen in the room and
  nothing about the game anybody is in. The room survives the match ending
  (D40), so a host who wants a different game finishes this one first, which
  costs them nothing.
- **Host only**, like the description and the door. Everybody reads it; one
  person chooses it.

Creating a room is the exception: a mode we do not offer is logged and the
room opens with the default. Losing somebody's room over a dropdown would be
the larger surprise, and they can fix it in Room settings a second later.

### The launch reads the room, not the page

`launchDota` no longer takes a mode from the request body. It asks the
coordinator what the room is playing, and only when this PC is the host -
a joining client is told an address, not a game, so nobody else pays for the
round trip.

Asked rather than remembered, because the page's copy is one poll old. Asked
rather than sent up, because a page can send whatever it likes and "what game
is this room playing" is not a question one player's PC gets to answer for the
room. If the coordinator cannot be reached the default is used rather than
refusing to start: by that point the tunnel is up and the ticket is issued,
and a coordinator away for a moment should not stop ten people playing.

### This reverses half of D59

D59 recorded, from the owner: *a room does not advertise a game mode - the
host switches it in Room settings and the app hands it to Dota on launch.*

The half about Room settings stands. The half about not advertising it is
reversed, by the same owner, for the room's own screen: the mode is in the
facts band beside Host, Players and the addresses, where everybody in the room
reads it. **The lobby list still does not carry it** - that surface was not
asked about and D59's answer for it is untouched.

### Start Game

`room.go.create` reads "Start Game". The word was "Create Game", from
GameRanger, and it was wrong in the one way that matters: by the time this
button is live the room is created, the ten of them are sitting in it, and the
thing about to happen is a match starting.

## D81 - the watching host: their name, their side, and the green row

**2026-08-31.** Four things from the owner, straight off a live build:

> when i move to observers, the "Host / Arman Mcc" turns to
> "Host / a_3427029f79090d91d0de7c18"
> yes the lobby list show each room's selected mode
> the observers are not counted in the 9/10, so the +1 space left should be the
> one that is one dire or radiant, the host can make the game as observer,
> jointeam -spec was the command i guess
> remove the "You are here" and replace it with a green colored highlighted
> room inside the lobby, for the room that the user is in

Three of the four are consequences of D79 that D79 did not follow through.

### The host's name

`view` read the host's name inside its loop over the **playing** slots:

```go
if m.IsHost { v.HostNick = m.Nick }
```

True for as long as a host had to be in one. D79 let them sit in the gallery,
and from that moment the loop never ran for them, `HostNick` stayed empty, and
the room fell through to a fallback written for a different case entirely - a
host inside their grace window, seated nowhere, where the account id really is
better than nothing. So the owner moved to watch and every screen in the room
started calling them `a_3427029f79090d91d0de7c18`.

The name is now looked up from the host's id directly, whichever seat they are
in, and the fallback stays for the case it was written for. The gallery seat
also carries `IsHost` now, which it never did - so the host's watching seat is
drawn as the host's rather than as an anonymous watcher's.

### `+jointeam spec`

The owner is right about the command and right about why it matters.

A host's PC runs the listen server whether or not they are playing on it. But
`myTeam()` put anybody it could not place on Radiant, so a host who sat down to
watch still started Dota **as a player** - occupying one of the ten playing
slots in a match they were not in. Nine people, and a tenth place taken by
somebody spectating.

A watching seat is a side of its own now: `myTeam()` returns `spec`, and the
button is live for a watcher instead of switched off. The host in the gallery
still starts the match, because their machine is still the server; everybody
else in the gallery gets **Watch Game** and joins with the same `+jointeam
spec`. All ten playing slots stay for the ten people who came to play, which
is the whole point and the answer to the question D79 left open.

`validTeams` already accepted `spec`. The interface was the only thing that
never sent it.

### The mode in the lobby list

D80 put the mode on every room and drew it inside the room. The owner has now
asked for it in the lobby list too - which reverses the second half of D59
completely, so that answer is now spent.

It is its own element beside the status badge rather than one more entry in
the run-on meta line, and the reason is in the CSS the line already carries:
that line *is allowed to run out of space and be cut*. A mode somebody is
scanning the lobby for cannot be the thing that gets cut.

`sandbox.sh` now seeds its three rooms with three different modes. Seeding
them all with the default would let a lobby that printed one room's mode
against every row pass every check that looks at the list.

### The green row

"You are here" was a span at the end of the meta line - the one that is
allowed to be cut. The row is green instead: a green wash, a green edge, a
green room name. Green rather than the accent because green is already this
app's word for *on, and yours* - the network dot, the OPEN badge, your own
status - and because it answers the question from across the screen instead of
at the end of a sentence.

It does break the "three places, one idea" pattern from D68, where your seat,
your name and your room all wore the accent. Two of the three still do. The
owner asked for the third by colour.

## D82 - one person, one room, and the review that found four more

**2026-08-31.** From the owner, off the live build:

> 1- i can create multiple rooms, which is not correct.
> 2- a user can only make one room at a time.
> 3- a user can only join one room at a time.
> 4- review all the relations and logics and inspect the different flows, so
> bugs like this wont exist and are found.

### Why nothing caught it

Every function was correct about the room it was handed. `Room.Join` refused a
second seat in the same room; `Store` refused a room id that did not exist;
the interface refused a second *join*, and had done since D68 - the Join
button on every row goes grey with "you are already in a room".

Nothing owned the sentence **"a person is in one room"**, so nothing enforced
it, and Create was the one door that had never had a guard put on it by hand.

That is the whole shape of the bug, and it is why item 4 was the right thing
to ask for. This was not a broken function. It was a rule with no home.

### The damage was not a stray row

A room closes when its **host** goes offline (D40, D70). That is a fact about
a person, checked against the player registry - so a host with two rooms is
online for both, and the one they walked away from **never closes**. It sits
in the lobby looking joinable, takes players, and leaves them waiting for
somebody who is never coming. The lobby had a permanent haunted room in it for
every time anybody pressed Create twice.

### RoomOf, derived and never indexed

`Store.RoomOf(playerID)` is the sentence, and it scans the open rooms rather
than keeping a map from player to room.

A map would be a second record of a fact the rooms already hold, kept in step
by hand across Create, three kinds of join, Leave, Kick, Close, the host's
grace window and the sweep that deletes a room outright. It would drift, and
it would drift the way an address derived from a seat drifted (D74) and the
way a game mode kept in one window drifted (D80): silently, on the path
nobody remembered to update.

The cost is paid only when somebody opens or enters a room - never on a poll,
never per packet. At the 2048-room ceiling it is a few tens of thousands of
string compares.

A host inside their grace window is still in their room, deliberately: they
still hold the seat and the address every client is connecting to, the room is
alive and still theirs for the next minute, and the thing they should be doing
is going back to it. When the window closes the room does too, and they are
free on the same tick.

### An app must never be told only "no"

The refusal names the room. Without that, somebody whose own window has lost
track - a cleared session, a reinstall, a crash between joining and saving -
is told "you are already in another room", cannot see which, and cannot get
out of it.

So `sync` now answers `in_room_id`: the room the **server** has this player in.
The app takes that answer over its own belief, and takes the whole membership
with it rather than the id alone - `Refresh` mints a fresh ticket for somebody
a room already holds a seat for, so the adopted room is one that can actually
be connected to rather than one that merely appears on screen.

It is only ever adopted, never used to clear: an empty answer to a request
that crossed a join in flight would throw somebody out of the room they just
entered. And the guard against that race is exactness rather than a timer -
adopt only if the app still believes what it believed when it asked.

### The four the review turned up

Item 4, done as invariants rather than as functions. Each is a sentence that
must hold across every path, in `coordinator/internal/room/audit_test.go`.

**A room could be handed over half way.** `SetHost` moved `HostSlot` and left
`HostWatching` behind. Those two are one fact - which array the number indexes
- and they are read on exactly one path, the expensive one: a host whose PC
dies comes back to "the seat they left". So a room handed from a watching host
to a playing one would put its new host, on their next crash, in the gallery
of the match running on their own machine.

**A host could kick themselves.** It emptied their seat, dropped the address
every client was connecting to, started the grace countdown and barred them
from their own room for a minute - four things nobody asked for, from one
misdirected click. Leave is the way out and it ends the room deliberately.

**Anybody could read any room's chat.** Writing into a room you are not in has
been refused since the chat was built, with the comment "anyone who learns a
room ID can heckle a match they are not part of". Reading was not - and every
room's id is in the lobby list handed to every client on every poll, so
"learns a room ID" was one field away for anyone signed in. `sync` handed over
the conversation, including whatever a host had just typed to let their
friends in. The room stays visible; the conversation inside it does not.

**The same scan existed three times.** `whereabouts`, for the friends rail,
was a third hand-written copy - and it copied every Room in the process to
answer a question about a string. It calls `RoomOf` now.

Two things were checked and found sound, and are pinned so they stay that way:
every seated person holds exactly one address and no two hold the same one
across every seat move, leave and rejoin; and the host always holds an address,
so `hostAddr` can always say where the room is reached.

### One thing this review found that is not fixed

**`Store.JoinAdmin` has no caller.** The reserved moderator seat outside the
ten playing slots is a product rule, it is built, it is tested - and no route
reaches it. It is the recurring shape of bug in this repository, which is a
tested subsystem the interface cannot get to, and it is left alone here on
purpose: what a moderator entering a room should be able to see and do is a
product question, not an engineering one, and it is with the owner.

Note that the one-room rule applies to that seat too, so when it is built, a
moderator will have to leave their own room to go and moderate another.
That is worth the owner knowing before it is designed.

### The smoke check that was deleted

The chat leak was covered in `smoke.sh` first, and the check passed against
the unfixed server: the app throws its own copy of a room's conversation away
the moment it leaves the room, so nothing at the app's own door can see what
the coordinator sent. It was deleted rather than kept, and the reason is in
`smoke.sh` where the check used to be. The api-package test talks to the
coordinator directly and does fail without the guard.

## D83 - the notices stopped sitting on top of each other

**2026-08-31.** From the owner, off the live build:

> - review and inspect the postions and overlapping of the notfications and
>   errors. (bug one)
> - some errors are not shown properly (bug two)

Two reports, and they turned out to be two different faults that produce the
same experience: something goes wrong and the person is not told.

### Bug one: four notices in one grid area

The shell is a CSS grid with a single `strip` row between the header and the
stage. Four elements were each given `grid-area: strip` directly:

- `#banner`, the error and trouble strip,
- `#termsmoved`, the terms-have-changed prompt,
- `#update`, the new-version prompt,
- `#banners`, the staff announcements.

Grid stacks items that share an area. Measured in the running page with every
notice raised at once:

```
termsmoved top=66 h=50 | banners top=54 h=62 | banner top=66 h=50 | update top=66 h=50
```

Three of them occupy the identical fifty pixels. The one drawn last wins, and
the others are behind it, invisible and still taking clicks nowhere. An error
raised while an update was available could not be read at all - which is also
half of bug two, from a completely different cause.

They are wrapped in one `#strips` flex column now, ordered most urgent first:
error, terms, update, announcements. `uicheck` raises all four and asserts
that no two rectangles intersect and that the column is above the stage.

### Bug two: a poll erased what you had just been told

`render()` runs on every poll, about twice a second, and ended with:

```js
banner(trouble, ...)   // trouble is "" when nothing is wrong
```

`act()` - which every button goes through - reported failures into the same
place:

```js
catch (e) { banner(e.message); }
```

So an error from something the person had just pressed lived in the variable
that the next poll overwrote. Two seconds later it was gone. The person saw a
flash of red and no explanation, and the app looked like it had ignored them.

The strip now has two sources and they do not overwrite each other:

- **standing** - the condition the app keeps rediscovering by itself: the
  service is down, the tunnel tore, the room is gone. Rewritten every poll,
  and an empty one means the condition ended.
- **report** - the reply to something somebody pressed. Cleared by the next
  action, never by a poll.

`report` wins when both are set, because it is the more recent answer to the
more specific question. `uicheck` raises a report, polls twice, and asserts it
is still on screen.

### And the create dialog said it twice

`createform`'s submit handler wrote the failure into the dialog's own error
line **and** rethrew it, so `act` raised the top strip as well - behind the
dialog overlay, where it could not be seen until the dialog closed, and then
read as a fresh unexplained failure. It reports once now, in the dialog the
person is looking at.

## D84 - a host who goes takes the room with them, immediately

**2026-08-31.** From the owner:

> if a host leaves a room or gets discounnected, the room should be forced
> closed, no grace time.

This reverses D40's grace period, which was two minutes, then one.

### What the grace was for, and why it goes

The argument for it was reconnection: a host whose PC crashed mid-match could
be back inside a minute, and the room, the addresses and the nine people in it
would still be there. It was real, and it worked.

What it cost is what the owner has been watching: nine people in a room with a
host who is not coming back, unable to tell the difference between that and a
host who is. They wait, because leaving might be premature. A minute of that
is a long time, and the *usual* case is not the crash - it is somebody who
closed the app.

A host who does come back opens another room in a couple of seconds, and the
people who wanted to play with them are still in the lobby. That is the trade,
and the owner made it.

### What it means in the code

Everything a room can die of goes through `Room.Close` now, with no timer:

- the host presses Leave room - already immediate since D70,
- the host's app quits or their machine drops - `SeeHost` closes on the tick
  that notices, where it used to start a countdown,
- an admin shuts the room.

Gone with it: `HostGracePeriod`, `HostGraceUntil`, `HostSeenAway`,
`Room.Tick` (which existed only to expire the grace), `Room.HostAway()`, the
`host_away` view status and its badge, and the whole host-returning branch in
`Room.Join` - a host used to be readmitted to a locked room, without the
password, onto the exact seat they left. None of that has anything to reach
any more.

`HostGraceUntil` did double duty as the clock the store lingered a dead room
by, and that job is real and stays: a closed room is kept for
`ClosedRoomLinger` (five minutes) so its members can read why it ended rather
than finding it gone. It is called `ClosedAt` now, which is what it always
meant.

### The one delay that is left, and it is not a grace

The coordinator concludes a host is offline after `api.OnlineWindow` of
silence - thirty seconds. A room does not close because one heartbeat was
late, and a blip shorter than that never reaches the room at all. That is a
detection floor, not a grace: nothing is waiting for the host to come back,
the coordinator simply does not yet know they have gone.

**The owner should know the number.** Thirty seconds of silence, then the room
closes on the next tick. Lowering it makes a brief hiccup on a domestic line
kill a healthy room; the number can be changed, and it is a product call.

## D85 - a moderator leaves the room they are in to go and moderate

**2026-08-31.** From the owner, answering the question D82 left open:

> yes a moderator should leave their room to moderate

This confirms the behaviour the one-room rule already produced rather than
changing it, and it is written down because the alternative reading is the
tempting one: the moderator seat is *reserved*, outside the ten playing slots,
so that a full match and a full gallery can never keep staff out - and it is
easy to argue from there that staff should be exempt from one-room too.

They are not. A moderator sitting in two rooms is a person the lobby cannot
describe: the friends rail, the Join buttons and the sync response all answer
"which room is this person in" with one string. The seat is about capacity,
not about being in two places.

So `Store.JoinAdmin` refusing a seated moderator is the answer and not a bug
to route around. The refusal names the room they are in, which is what the app
needs in order to offer them the one action that helps.

Half of D82's open question is still open: **what a moderator can see and do
once they are inside a room** is undecided, and `JoinAdmin` still has no
caller.

## D86 - the room boards, back to the Nocturne mock

**2026-09-03.** The owner handed over the original design canvas -
`Gaming Matchmaking App Redesign/LobbyBaz.dc.html`, the Nocturne mock this
interface was reskinned from on 2026-08-25 (D42) - and asked for the product
to be pulled back to it:

> background colors, buttons colors, no glow, no flashy buttons, no laser
> moving on top, same contrast as the mock
> [...] same for rooms but only for the seats and the background color and
> contrast levels, no need to change anything related to top sections of the
> rooms, including Room info, start, etc.

The ask covered the lobby as well. After seeing both screens redrawn the
owner kept the lobby as it was and took only the room:

> nevermind the new design on homepage, just apply the Rooms requriments
> adjustments
> revert back the Lobby changes if u have done any

**So the lobby is untouched and stays as it is** - it keeps the light that
travels its top edge, the halo behind its title, the pulse on Create room,
the green row for the room you are in, and the strength of its status greens.
A later agent reading this decision should not "finish the job" by applying
any of what follows to the lobby: it was applied, looked at, and reversed.

### What changed on the room screen

The palette was never wrong. `--bg`, `--panel`, `--accent` and the rest are
the mock's own values and have been since D42. What had drifted was the
boards, which had grown a language of their own:

- A **board** sat on `--board` (`#191b25`, a shade darker than every other
  panel) behind a heavy `--line2` inside line, because the coloured washes in
  its header needed a darker ground to read against. The washes are gone with
  D86, so the ground went with them: a board is `--panel` with a `--line`
  ring, like every other panel in the product.
- A **seat** was a full-width row separated from its neighbour by a hairline,
  with a two-pixel `--host-mark` violet edge at its leading end meaning
  "somebody who is not you". Ten of those under a washed header was a board
  made of lines. A seat is now what the mock draws: a 38px row with an 8px
  radius, lying 6px inside its board, saying "empty", "taken" or "yours" with
  a one-pixel ring - `--seat-ring`, `--seat-ring-on`, `--accent-ring`.
- The **Join Game** button lost its glow, which was the last one on the
  screen. The mock's own primary action is a border and a wash.
- The room band lost the light travelling its top edge.

`--board`, `--host-mark` and `--host-wash` have nothing left that reads them
and are deleted.

### Two things the mock has that the room did not take

- **`Sit here` versus `Empty`.** A copy change nobody asked for.
- **The mock's boards have no watching seats.** They predate D59. The two
  watching seats on each board stay, in the same rounded-row idiom at 30px.

### The trap this left behind

Focus used to be an inset `box-shadow` on `.room`, `.slot.takeable` and
`.friend.clickable`. A seat now carries its own ring in that same property,
at the same specificity, and the two took turns winning by source order - so
a seat you had tabbed to showed no focus at all. Focus is an
`outline` with `outline-offset: -2px` now, which draws in the same place and
cannot be beaten by a background rule. **Nothing else in the stylesheet uses
`outline`, and nothing else should.**

### What proves it

Two checks in `uicheck`, both watched failing first:

- *a seat is a rounded row with daylight round it, not a band across the
  board* - measures every seat on the first board against the board's own
  rectangle, and against the seat above it.
- *nothing inside a room animates itself* - walks `#screen-room` and asserts
  every computed `animation-name` is `none`. It is scoped to the room on
  purpose; the lobby's decorations are the owner's and are meant to stay.

## D87 - a dialog that could not be answered

**2026-09-03.** From the owner, on the live product:

> u just broke the app, i cant make a room.
> [...] m creation window is broken

### It was not the change it arrived with

T41 had shipped minutes earlier, so the first job was to find out whether it
was the cause. It was not, and the way that was settled is worth keeping:
the create dialog was photographed on the commit before T41 and on T41
itself, in the same sandbox, and the two pictures were subtracted. **Zero
differing pixels inside the dialog.** T41's diff is the room boards, one
decorative span and a focus rule; nothing it touched is on this screen.

Do not take that as "the report was wrong". The dialog was broken, and had
been for a long time; the owner had simply not hit the window size that
shows it before.

### The bug

`.gate` is a fixed overlay that centres its card with `place-items: center`
and does not scroll. `.gate-card` had no `max-height`. A card taller than the
viewport is therefore centred on it and hangs off **both** ends - the title
above the top edge, and the Cancel and Create buttons below the bottom one -
with no scrollbar anywhere to reach them. The dialog opens, looks almost
right, and cannot be answered. From the outside that is a Create room button
that does nothing.

This was not a corner. Three numbers meet in the middle of a supported
configuration:

- the app's own window minimum is **640px** tall (`tauri.conf.json`),
- Windows display scaling at 125% or 150% divides the CSS viewport again, so
  a 800px window can present as 640 or 533,
- the room-creation dialog with its password row showing is about **690px**.

A card now may never exceed the height of the window it is centred in. It is
a flex column: the head and the foot are fixed, and `.gate-body` scrolls. The
buttons cannot leave the screen.

### And the password field was labelled "none"

`door.password.placeholder` is the string shown *inside* the box when no
password is set. It was also being used as the field's **label**, in the
create dialog and again in Room settings - so the field above the "Ask for a
password as well" tick was captioned "none". A new `door.password.label`
says "Password", and the placeholder keeps its one honest use.

### What proves it

`uicheck`, *the create dialog can still be answered in a short window*: it
opens the dialog in its tallest state, shrinks the overlay to 480px, and
asserts the head and the foot are both inside it. Watched failing on the
shipped stylesheet first, with the right words - *the buttons are outside the
window; the title is above the top edge*.

**The lesson for the harness.** Every screenshot this project takes is
1440x820, and `preview.sh` has taken `WIDE`/`TALL` all along. A bug that only
exists below a certain height cannot be seen by a rung that only ever looks
at one height. When a change is about layout, photograph it small as well.

## D88 - Joining lands you in the room, the door is a mark, and two numbers are hidden (2026-09-03)

Five things the owner asked for after an evening of manual QA on the live
product. They are one decision because they are one sitting, and because four
of the five are the same complaint: **the lobby row was doing too much work
and saying too little.**

### Joining a room puts you in the room

`joinRoom` fired the request and stopped. You stayed in the lobby, looking at
a list in which one row had quietly become yours - the same row, a slightly
different green, its button now reading *Open*. Every other way into a room
already went in: accepting an invitation from the friends rail called
`show("room")`, and creating a room does too. Only the two obvious ones - the
row itself and a friend's Join button - did not.

The one thing anybody wants a second after joining is to see who is in there.

### The door is a mark, not words at the end of a line that gets cut

*Password*, *Friends only* and *Invite only* were three phrases appended to
the room's meta line, after the host's name and the description. That line is
the run-on line: it is explicitly allowed to run out of room and be
ellipsised (D81 keeps the things people scan for in their own elements for
exactly this reason). So on a narrow window, or under a long description, the
first fact to disappear was that the room wanted a password - and a player
would click Join and be surprised by a prompt.

Three marks now sit beside the game-mode badge, in their own element, never
cut: a **padlock** for a password, an **outlined person** for friends-only, an
**envelope** for invite-only. A room can want both a password and membership,
so they are not exclusive and both are drawn.

**They are drawn in the stylesheet, not typed as characters.** The same
reason the search magnifier is: a padlock glyph comes from whichever font the
machine happens to have, and on Windows it arrives as a full-colour emoji in
a product that is otherwise entirely monochrome. None of the three uses
`transform`, so there is nothing to mirror in Persian (rule 14).

Three shapes were tried and thrown away before these, which is worth
recording so nobody spends the evening again:

- **Two overlapping circles** for friends-only needed the front circle filled
  in the row's own colour to cut the one behind it - and the stylesheet does
  not know what colour the row is. On the green row you are in, it was wrong.
- **Two circles side by side** merged at twelve pixels into a single capsule
  that read as a letter of the alphabet.
- **A shallow flap floating inside the envelope** read as a dropdown with a
  caret in it. The flap has to hang from the envelope's top inner edge and
  span its full width before it reads as a letter.

The words are still in the catalogue and still shown - as the mark's `title`
and its `aria-label` - which is also why the i18n rule that forbids unused
keys stays satisfied.

### MMR off the lobby, your own address off the room

Hidden, **not removed** - the owner was explicit. The MMR column's cell, its
sortable heading and the sort itself are all still built; one rule hides them
and the grid template lost one column. Bringing it back is putting
`var(--w-mmr)` into the two templates and deleting the rule. The same for
*Your address* on the room screen: the cell is still filled from
`state.virtual_ip`, and the hairline that would otherwise be left dangling in
front of it goes with it via `:has()`.

Each stat cell now carries its own key as a class - `room.stat.you` becomes
`stat-room-stat-you` - which is what lets a cell be hidden from the
stylesheet without the renderer knowing anything about it.

### What proves it

Three new `uicheck` checks, each watched failing on its own broken version
and green on the other three:

- *joining a room ends up inside the room* - stubs `api` and `show` and
  asserts the request goes out and the screen changes when it returns. Stubs
  rather than a real join, because a real join would seat the sandbox player
  in a second room and one person is in one room at a time (D82), which would
  poison every check that runs after it.
- *every door a room can have is a mark, and none of them is cut off* - sets
  all three doors, asserts each mark is drawn, is at least 8x8, has a title,
  is inside its row, and is **not** inside the line that gets ellipsised.
- *MMR is off the lobby and your address is off the room, hidden but still
  built* - asserts each element still exists and is invisible, that the
  heading grid and the row grid still have the same number of columns, and
  that no hairline is left hanging where the address used to be.

That last check is the reason this is a check at all rather than a deletion:
if somebody later tidies the hidden cells away, it goes red and tells them
the cells are deliberate.

**A harness fix came with it.** The runner now passes `awaitPromise` to
`Runtime.evaluate`. Every check written before this answers with a plain
object and is unaffected, but without it a check could only ever see what the
page does *before its first await* - which is exactly where "and then it
showed you the room" happens.

## D89 - Rooms you can move between, a door you can read down, and the rail that had never hidden (2026-09-03)

Four things the owner asked for after the T44 build went up, and one bug
underneath them that turns out to be the reason the app has looked wrong on a
small window for as long as anyone has looked at it on one.

### You can join another room without leaving the first one yourself

**One person is in one room at a time (D82) and that has not changed.** The
coordinator still refuses a join from somebody who is already seated, and it
is right to. What changed is who does the leaving. The interface used to
switch every Join button off and put "Leave your current room first" in a
tooltip, which is an application telling somebody to go and do a thing it
could perfectly well do for them. It now does it: leave, then join, then show
you the room. GameRanger worked this way and it is most of why its lobby felt
free to move around in.

Every way into a room goes through one function now - `enterRoom` - so the
lobby row, the button on it, a friend's room and an accepted invitation all
behave the same. The invitation path in particular was switched off while you
were anywhere else, which meant a friend could invite you and you would watch
the invitation sit there unusable.

**The host is asked first, and only the host.** Leaving a room you host closes
it at once, for everybody in it, with no grace (D84). Doing that to nine other
people because somebody clicked the wrong row is not a thing to do quietly. A
player who is merely sitting in a room loses nothing by moving and is not
stopped to be told so.

That question needed somewhere to live, so there is now one small dialog,
`askGate`, that asks a yes/no question and answers with a promise. Not
`window.confirm`: in a desktop shell that is a different typeface, a different
pair of buttons and a different window. Not a bespoke card per question
either - that is how an interface ends up with four dialogs that behave four
ways. Escape, the backdrop and Cancel all mean no.

Creating a room is still switched off while you are in one. The owner asked
about joining; this is the same question about a different button and is
theirs to answer.

### The door is a column

T44 moved the door out of the meta line, which is allowed to run out of space
and be cut, and put it beside the game-mode badge. That fixed the thing that
was actually wrong. It left a smaller one: beside the badge, the mark lands in
a different place on every row, so it can only be read one room at a time. In
a column it reads down the list, which is how somebody actually scans for "a
room I can walk into". It sits between the ping and the Join button, next to
the live dot, where the owner asked for it.

The heading is the one heading in that row that does not sort. There is no
order to put three unlike doors in that anybody would ask for, and a heading
that looks like a button and does nothing when pressed is worse than one that
plainly is not.

### The friends rail folds away

A chevron in the Friends header sends it out to the inline-end; a chevron in
the top bar brings it back. The choice is remembered in browser storage,
because a preference that has to be set again on every start is not one.

It slides rather than vanishing. The mechanism is worth naming because it
costs nothing: the shell's third grid column animates between `var(--rail)`
and `0px`, and `#rail` is given its own `width` so that it keeps its size
while its track shrinks and slides out past the edge of the shell instead of
being squashed on the way. **No `transform`**, so there is nothing to mirror in
Persian (frontend rule 14) - under `dir="rtl"` the grid flips on its own and
the rail leaves to the left.

### The rail had never once hidden, and that is why the app looked broken small

`@media (max-width: 1100px) { #rail { display: none } }` was written at line
172. `#rail { display: flex }` is at line 1166. Same specificity, later wins:
the rule had never fired in its life. This is frontend rule 13 - never write a
media query above the rule it narrows - and it had been sitting there since
the rail was built.

What it did instead was worse than not working. The same media query dropped
the shell to two named columns, `"bar head" "bar stage"...`, with no `rail`
area in them. A rail that was still `display: flex` therefore had a
`grid-area: rail` that named nothing, fell out to auto-placement, and landed
in an **implicit track in the bottom-right corner of the window**. At 1024x640
- the app's own window minimum - the rail sat in the bottom corner, the room
list was squeezed into a third of the width, and the room screen's seat boards
were pushed off the bottom. That is the whole of the owner's "the app should
be responsive".

The fix is one token. The shell keeps its three named columns always and the
third is allowed to be nought: `--rail-w`, written once into
`grid-template-columns`, narrowed by the media query below it and overridden
by `#shell.rail-shut` above the media query on specificity - which is what
lets somebody who has opened the rail keep it open. Below 1100px there is no
room and no argument. Because the areas never change, the rail can never again
be left without one to sit in.

The chat dock also gives up more height below 700px, so that at the 640px
window minimum the seat boards get more than three rows of ten.

### Every status in the lobby was painted green

`statusClass` has always emitted `badge locked` and `badge replace`. **Neither
class had a single line of styling.** `.badge.game` and `.badge.shut` existed
and were written for class names nothing emits, and a hundred lines above the
room list sat `.state`, `.state.game`, `.state.shut` - a second copy of the
same idea that nothing has ever used. So "In game", "Closed" and "Needs a
player" were all drawn in the green that means open.

In game is now the same red as the live dot on the same row, so a row says one
thing twice rather than two things once. Closed is grey. Needs a player is
amber. `closed` and `locked_in_game` stopped sharing a class, which mattered
the moment the classes had colours.

And the label used to climb out of its own background on a narrow window - the
screenshot the owner sent. The box has a fixed 17px height, so a label that
wraps does not make it taller, it stands out of it. Two things stop it, and
either alone is enough: `flex: none` keeps the badge from being squeezed by
the flex line it sits in, and `white-space: nowrap` keeps the text on one line
if it is.

### What proves it

Five new `uicheck` checks and one amended, each watched failing on its own
broken version and green on the others:

- *being in a room does not stop you joining another one*
- *joining another room leaves the one you are in first* - asserts the two
  requests go out in that order.
- *a host is asked before their own room is closed under them* - asserts the
  dialog opens, that nothing has been sent yet, and that Cancel sends nothing.
- *the friends rail folds away and comes back* - asserts it ends up past the
  edge of the shell at full width rather than squashed, that something on
  screen brings it back, that it returns to the pixel it left from, and -
  caught 90ms in - that it **moved** rather than jumped. A transition that has
  been dropped looks identical once it has finished.
- *a room in a match says so in red, on one line* - asserts the badge is the
  same colour as the dot beside it, that it is not the colour of an open room,
  and that squeezed to 420px the label does not stand out of its box.
- *every door a room can have is a mark, and none of them is cut off* now also
  asserts the mark is a column of the row rather than something inside it.

**One falsification is worth recording as a near miss.** The wrapping check
passed with `white-space: nowrap` deleted, and passed again with `flex: none`
deleted, and only went red when both were gone - which is correct, because
either one alone prevents it. The first version of the check measured the
badge's height, which a fixed-height box can never change; it had been green
against every broken variant. Measure the overflow, not the box.
