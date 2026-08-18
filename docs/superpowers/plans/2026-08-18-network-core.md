# Network Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the encrypted room-scoped game network and the minimum lobby needed to exercise it, ending in a real Dota 2 match between two physical Windows PCs.

**Architecture:** A Go relay on Linux forwards encrypted UDP datagrams between members of the same room, enforcing anti-spoofing and room isolation, and never carrying broadcast traffic. A Go Windows service owns a Wintun virtual adapter and tunnels the player's traffic to the relay. A minimal Go coordinator issues session tickets, allocates per-room `/28` subnets, and runs the room state machine. A throwaway CLI drives it all so two people can create a room, join it, and launch Dota.

**Tech Stack:** Go 1.23+, `golang.zx2c4.com/wireguard/tun` (Wintun), `github.com/flynn/noise` (Noise NK handshake), `golang.org/x/crypto/chacha20poly1305`, PostgreSQL 16, systemd.

**Spec:** `docs/superpowers/specs/2026-08-18-lobby-platform-design.md`

## Global Constraints

- **No international runtime dependencies.** No Steam, STUN, Google, Cloudflare, or GitHub reachable at runtime. Vendor all Go modules.
- **Client platform:** Windows only. Server: Ubuntu 24.04.
- **Relay listens on UDP 443.** TCP 443 belongs to an unrelated nginx SNI proxy on the shared server; never bind it.
- **Address space:** `10.87.0.0/16`, one `/28` per room, 4096 rooms max.
- **Host address is deterministic:** always the third address of the room's `/28`.
- **Wintun MTU is 1300.** Encrypted datagrams must never fragment.
- **No custom cryptography.** Noise NK for handshake, ChaCha20-Poly1305 for data.
- **Broadcast and multicast are dropped by the relay by default**, behind a config flag defaulting to off.
- **Anti-spoof is mandatory:** inner source IP must equal the session's assigned virtual IP.
- **Goroutine count scales with peers, never with packet rate.**
- Installer ships unsigned for now.

### Deviation from spec, section 5.4

The spec names Noise **IK**. This plan uses Noise **NK**. IK requires the
initiator to hold a static keypair the responder already trusts; our clients
authenticate with a short-lived ticket from the coordinator, not a pre-shared
static key. NK is the correct pattern: the client knows the relay's static
public key, and the ticket travels in the first handshake payload. Same
security properties for our threat model, less key management. Update the spec
to match when this task lands.

---

## File Structure

```
relay/                          Linux UDP relay (Go)
  cmd/relay/main.go             entrypoint, flag parsing, wiring
  internal/wire/packet.go       packet framing + codec
  internal/wire/packet_test.go
  internal/crypto/handshake.go  Noise NK initiator + responder
  internal/crypto/session.go    ChaCha20-Poly1305 seal/open + replay window
  internal/route/decision.go    pure routing decision function
  internal/route/table.go       session + room membership tables
  internal/sendq/queue.go       bounded per-peer ring buffer
  internal/server/server.go     UDP socket, session lifecycle
  internal/control/api.go       internal HTTP API for the coordinator

coordinator/                    Minimal control plane (Go)
  cmd/coordinator/main.go
  internal/ipam/allocator.go    per-room /28 + slot -> virtual IP
  internal/room/state.go        room state machine
  internal/room/store.go        PostgreSQL persistence
  internal/ticket/ticket.go     signed session tickets
  internal/api/http.go          player-facing HTTP API

netservice/                     Windows service (Go)
  cmd/netservice/main.go
  internal/adapter/wintun.go    adapter lifecycle, IP + route config
  internal/tunnel/client.go     handshake, packet pump, reconnect
  internal/watchdog/lease.go    fail-closed lease renewal
  internal/dota/launch.go       arg allowlist, launch, readiness
  internal/ipc/pipe.go          named pipe API for the CLI

lobbycli/                       Throwaway test client
  main.go

loadtest/                       Synthetic peer generator
  main.go

deploy/
  relay.service
  coordinator.service
  nginx-lobby.conf
```

---

### Task 1: Repository scaffolding and Go workspace

**Files:**
- Create: `go.work`, `relay/go.mod`, `coordinator/go.mod`, `netservice/go.mod`, `lobbycli/go.mod`, `loadtest/go.mod`
- Create: `Makefile`
- Create: `relay/internal/wire/version.go`
- Test: `relay/internal/wire/version_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `wire.ProtocolVersion` (`uint8`, value `1`). Every later task builds inside this workspace.

- [ ] **Step 1: Write the failing test**

`relay/internal/wire/version_test.go`:

```go
package wire_test

import (
	"testing"

	"finallobby/relay/internal/wire"
)

func TestProtocolVersionIsOne(t *testing.T) {
	if wire.ProtocolVersion != 1 {
		t.Fatalf("ProtocolVersion = %d, want 1", wire.ProtocolVersion)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd relay && go test ./internal/wire/ -run TestProtocolVersionIsOne -v`
Expected: FAIL — package `wire` does not exist.

- [ ] **Step 3: Create the workspace and minimal implementation**

```bash
go work init
for m in relay coordinator netservice lobbycli loadtest; do
  mkdir -p $m && (cd $m && go mod init finallobby/$m)
  go work use ./$m
done
mkdir -p relay/internal/wire
```

`relay/internal/wire/version.go`:

```go
// Package wire defines the on-the-wire packet format used between the
// Windows net-service and the relay.
package wire

// ProtocolVersion is bumped whenever the packet layout changes
// incompatibly. Clients and relay must agree exactly.
const ProtocolVersion uint8 = 1
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd relay && go test ./internal/wire/ -v`
Expected: PASS

- [ ] **Step 5: Add the Makefile**

`Makefile`:

```makefile
.PHONY: test build-relay build-coordinator build-netservice vet

test:
	cd relay && go test ./... 
	cd coordinator && go test ./...
	cd netservice && go test ./...

vet:
	cd relay && go vet ./...
	cd coordinator && go vet ./...

build-relay:
	cd relay && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ../bin/relay ./cmd/relay

build-coordinator:
	cd coordinator && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ../bin/coordinator ./cmd/coordinator

build-netservice:
	cd netservice && GOOS=windows GOARCH=amd64 go build -o ../bin/netservice.exe ./cmd/netservice
```

- [ ] **Step 6: Commit**

```bash
git add go.work Makefile relay coordinator netservice lobbycli loadtest
git commit -m "chore: initialise Go workspace and module layout"
```

---

### Task 2: Packet framing and codec

**Files:**
- Create: `relay/internal/wire/packet.go`
- Test: `relay/internal/wire/packet_test.go`

**Interfaces:**
- Consumes: `wire.ProtocolVersion`.
- Produces:
  - `type PacketType uint8` with `TypeHandshakeInit`, `TypeHandshakeResp`, `TypeData`, `TypeKeepalive`, `TypeDisconnect`.
  - `type Header struct { Version uint8; Type PacketType; SessionID uint32; Sequence uint64 }`
  - `func EncodeHeader(dst []byte, h Header) int` — writes `HeaderSize` bytes, returns count.
  - `func DecodeHeader(src []byte) (Header, error)`
  - `const HeaderSize = 14`
  - `var ErrShortPacket error`, `var ErrBadVersion error`

Header layout: version (1), type (1), sessionID (4, big-endian), sequence (8, big-endian). The sequence doubles as the AEAD nonce source in Task 4, so it must be in the clear.

- [ ] **Step 1: Write the failing test**

`relay/internal/wire/packet_test.go`:

```go
package wire_test

import (
	"errors"
	"testing"

	"finallobby/relay/internal/wire"
)

func TestHeaderRoundTrip(t *testing.T) {
	in := wire.Header{
		Version:   wire.ProtocolVersion,
		Type:      wire.TypeData,
		SessionID: 0xDEADBEEF,
		Sequence:  0x0102030405060708,
	}
	buf := make([]byte, wire.HeaderSize)
	if n := wire.EncodeHeader(buf, in); n != wire.HeaderSize {
		t.Fatalf("EncodeHeader wrote %d bytes, want %d", n, wire.HeaderSize)
	}
	out, err := wire.DecodeHeader(buf)
	if err != nil {
		t.Fatalf("DecodeHeader: %v", err)
	}
	if out != in {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", out, in)
	}
}

func TestDecodeHeaderRejectsShortPacket(t *testing.T) {
	_, err := wire.DecodeHeader(make([]byte, wire.HeaderSize-1))
	if !errors.Is(err, wire.ErrShortPacket) {
		t.Fatalf("err = %v, want ErrShortPacket", err)
	}
}

func TestDecodeHeaderRejectsWrongVersion(t *testing.T) {
	buf := make([]byte, wire.HeaderSize)
	wire.EncodeHeader(buf, wire.Header{Version: 99, Type: wire.TypeData})
	_, err := wire.DecodeHeader(buf)
	if !errors.Is(err, wire.ErrBadVersion) {
		t.Fatalf("err = %v, want ErrBadVersion", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd relay && go test ./internal/wire/ -v`
Expected: FAIL — undefined `wire.TypeData`, `wire.HeaderSize`, etc.

- [ ] **Step 3: Write the implementation**

`relay/internal/wire/packet.go`:

```go
package wire

import (
	"encoding/binary"
	"errors"
)

// PacketType discriminates the datagram payload.
type PacketType uint8

const (
	TypeHandshakeInit PacketType = 1
	TypeHandshakeResp PacketType = 2
	TypeData          PacketType = 3
	TypeKeepalive     PacketType = 4
	TypeDisconnect    PacketType = 5
)

// HeaderSize is the fixed size of every packet header in bytes.
const HeaderSize = 14

var (
	ErrShortPacket = errors.New("wire: packet shorter than header")
	ErrBadVersion  = errors.New("wire: unsupported protocol version")
)

// Header precedes every datagram. Sequence is transmitted in the clear
// because it seeds the AEAD nonce.
type Header struct {
	Version   uint8
	Type      PacketType
	SessionID uint32
	Sequence  uint64
}

// EncodeHeader writes h into dst and returns the number of bytes written.
// dst must be at least HeaderSize long.
func EncodeHeader(dst []byte, h Header) int {
	_ = dst[HeaderSize-1] // bounds check hint
	dst[0] = h.Version
	dst[1] = byte(h.Type)
	binary.BigEndian.PutUint32(dst[2:6], h.SessionID)
	binary.BigEndian.PutUint64(dst[6:14], h.Sequence)
	return HeaderSize
}

// DecodeHeader parses a header from src.
func DecodeHeader(src []byte) (Header, error) {
	if len(src) < HeaderSize {
		return Header{}, ErrShortPacket
	}
	h := Header{
		Version:   src[0],
		Type:      PacketType(src[1]),
		SessionID: binary.BigEndian.Uint32(src[2:6]),
		Sequence:  binary.BigEndian.Uint64(src[6:14]),
	}
	if h.Version != ProtocolVersion {
		return Header{}, ErrBadVersion
	}
	return h, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd relay && go test ./internal/wire/ -v`
Expected: PASS, 4 tests.

- [ ] **Step 5: Commit**

```bash
git add relay/internal/wire
git commit -m "feat(wire): add packet header framing and codec"
```

---

### Task 3: Virtual IP allocation

**Files:**
- Create: `coordinator/internal/ipam/allocator.go`
- Test: `coordinator/internal/ipam/allocator_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `func RoomSubnet(roomIndex int) (netip.Prefix, error)` — the room's `/28`.
  - `func HostIP(roomIndex int) (netip.Addr, error)` — always subnet base + 2.
  - `func SlotIP(roomIndex, slot int) (netip.Addr, error)` — slot 0 is the host; slots 0–9 are players.
  - `func SpectatorIP(roomIndex, index int) (netip.Addr, error)` — index 0–2.
  - `const MaxRooms = 4096`, `const PlayerSlots = 10`, `const SpectatorSlots = 3`
  - `var ErrRoomIndexRange, ErrSlotRange error`

Layout inside each `/28`: `.0` network, `.1` reserved, `.2`–`.11` players, `.12`–`.14` spectators, `.15` broadcast.

- [ ] **Step 1: Write the failing test**

`coordinator/internal/ipam/allocator_test.go`:

```go
package ipam_test

import (
	"errors"
	"testing"

	"finallobby/coordinator/internal/ipam"
)

func TestRoomSubnetLayout(t *testing.T) {
	cases := []struct {
		room int
		want string
	}{
		{0, "10.87.0.0/28"},
		{1, "10.87.0.16/28"},
		{15, "10.87.0.240/28"},
		{16, "10.87.1.0/28"},
		{4095, "10.87.255.240/28"},
	}
	for _, c := range cases {
		got, err := ipam.RoomSubnet(c.room)
		if err != nil {
			t.Fatalf("room %d: %v", c.room, err)
		}
		if got.String() != c.want {
			t.Errorf("room %d = %s, want %s", c.room, got, c.want)
		}
	}
}

func TestHostIPIsDeterministic(t *testing.T) {
	got, err := ipam.HostIP(16)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "10.87.1.2" {
		t.Fatalf("HostIP(16) = %s, want 10.87.1.2", got)
	}
	// The host must equal slot 0.
	slot0, _ := ipam.SlotIP(16, 0)
	if slot0 != got {
		t.Fatalf("SlotIP(16,0) = %s, want %s", slot0, got)
	}
}

func TestSlotIPsCoverTenPlayers(t *testing.T) {
	first, _ := ipam.SlotIP(0, 0)
	last, _ := ipam.SlotIP(0, ipam.PlayerSlots-1)
	if first.String() != "10.87.0.2" {
		t.Errorf("first slot = %s, want 10.87.0.2", first)
	}
	if last.String() != "10.87.0.11" {
		t.Errorf("last slot = %s, want 10.87.0.11", last)
	}
}

func TestSpectatorIPsSitOutsidePlayerRange(t *testing.T) {
	first, _ := ipam.SpectatorIP(0, 0)
	last, _ := ipam.SpectatorIP(0, ipam.SpectatorSlots-1)
	if first.String() != "10.87.0.12" {
		t.Errorf("first spectator = %s, want 10.87.0.12", first)
	}
	if last.String() != "10.87.0.14" {
		t.Errorf("last spectator = %s, want 10.87.0.14", last)
	}
}

func TestOutOfRangeRejected(t *testing.T) {
	if _, err := ipam.RoomSubnet(ipam.MaxRooms); !errors.Is(err, ipam.ErrRoomIndexRange) {
		t.Errorf("RoomSubnet(MaxRooms) err = %v, want ErrRoomIndexRange", err)
	}
	if _, err := ipam.SlotIP(0, ipam.PlayerSlots); !errors.Is(err, ipam.ErrSlotRange) {
		t.Errorf("SlotIP overflow err = %v, want ErrSlotRange", err)
	}
	if _, err := ipam.SpectatorIP(0, ipam.SpectatorSlots); !errors.Is(err, ipam.ErrSlotRange) {
		t.Errorf("SpectatorIP overflow err = %v, want ErrSlotRange", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd coordinator && go test ./internal/ipam/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the implementation**

`coordinator/internal/ipam/allocator.go`:

```go
// Package ipam allocates the virtual addresses used inside room networks.
//
// The platform owns 10.87.0.0/16 and gives every room a /28. Sixteen
// addresses per room: .0 network, .1 reserved for the relay, .2-.11 the ten
// player slots, .12-.14 spectator and admin slots, .15 broadcast.
package ipam

import (
	"errors"
	"fmt"
	"net/netip"
)

const (
	// MaxRooms is how many /28 blocks fit inside 10.87.0.0/16.
	MaxRooms = 4096
	// PlayerSlots is fixed by Dota 2 itself.
	PlayerSlots = 10
	// SpectatorSlots covers admin observers.
	SpectatorSlots = 3

	playerBaseOffset    = 2
	spectatorBaseOffset = 12
)

var (
	ErrRoomIndexRange = errors.New("ipam: room index out of range")
	ErrSlotRange      = errors.New("ipam: slot index out of range")
)

// RoomSubnet returns the /28 belonging to roomIndex.
func RoomSubnet(roomIndex int) (netip.Prefix, error) {
	base, err := subnetBase(roomIndex)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(base, 28), nil
}

// HostIP returns the address the room's host always occupies. Clients are
// told this address directly, which is why Dota never needs LAN discovery.
func HostIP(roomIndex int) (netip.Addr, error) {
	return SlotIP(roomIndex, 0)
}

// SlotIP returns the address for a player slot. Slot 0 is the host.
func SlotIP(roomIndex, slot int) (netip.Addr, error) {
	if slot < 0 || slot >= PlayerSlots {
		return netip.Addr{}, fmt.Errorf("%w: player slot %d", ErrSlotRange, slot)
	}
	return offsetFrom(roomIndex, playerBaseOffset+slot)
}

// SpectatorIP returns the address for a spectator or admin slot.
func SpectatorIP(roomIndex, index int) (netip.Addr, error) {
	if index < 0 || index >= SpectatorSlots {
		return netip.Addr{}, fmt.Errorf("%w: spectator slot %d", ErrSlotRange, index)
	}
	return offsetFrom(roomIndex, spectatorBaseOffset+index)
}

func subnetBase(roomIndex int) (netip.Addr, error) {
	if roomIndex < 0 || roomIndex >= MaxRooms {
		return netip.Addr{}, fmt.Errorf("%w: %d", ErrRoomIndexRange, roomIndex)
	}
	third := byte(roomIndex >> 4)
	fourth := byte((roomIndex & 0x0F) << 4)
	return netip.AddrFrom4([4]byte{10, 87, third, fourth}), nil
}

func offsetFrom(roomIndex, offset int) (netip.Addr, error) {
	base, err := subnetBase(roomIndex)
	if err != nil {
		return netip.Addr{}, err
	}
	b := base.As4()
	b[3] += byte(offset)
	return netip.AddrFrom4(b), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd coordinator && go test ./internal/ipam/ -v`
Expected: PASS, 5 tests.

- [ ] **Step 5: Commit**

```bash
git add coordinator/internal/ipam
git commit -m "feat(ipam): allocate per-room /28 subnets with deterministic host address"
```

---

### Task 4: Routing decision — anti-spoof, room scoping, broadcast drop

This is the task that fixes the ancestor's failure mode. Keep the decision a
pure function so every rule is directly testable without sockets.

**Files:**
- Create: `relay/internal/route/decision.go`
- Test: `relay/internal/route/decision_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Verdict int` with `VerdictDrop`, `VerdictForward`, `VerdictFanout`.
  - `type Decision struct { Verdict Verdict; Dst netip.Addr; Reason string }`
  - `type Sender struct { VirtualIP netip.Addr; RoomID string }`
  - `type Options struct { AllowMulticast bool }`
  - `func Decide(sender Sender, innerPacket []byte, opts Options) Decision`

`Decide` parses only the inner IP header. It never allocates.

- [ ] **Step 1: Write the failing test**

`relay/internal/route/decision_test.go`:

```go
package route_test

import (
	"net/netip"
	"testing"

	"finallobby/relay/internal/route"
)

// ipv4 builds a minimal 20-byte IPv4 header with the given addresses.
func ipv4(src, dst netip.Addr) []byte {
	p := make([]byte, 20)
	p[0] = 0x45 // version 4, IHL 5
	s := src.As4()
	d := dst.As4()
	copy(p[12:16], s[:])
	copy(p[16:20], d[:])
	return p
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("bad addr %q: %v", s, err)
	}
	return a
}

func TestForwardsUnicastWithinRoom(t *testing.T) {
	sender := route.Sender{VirtualIP: mustAddr(t, "10.87.0.2"), RoomID: "room-a"}
	pkt := ipv4(mustAddr(t, "10.87.0.2"), mustAddr(t, "10.87.0.5"))

	got := route.Decide(sender, pkt, route.Options{})
	if got.Verdict != route.VerdictForward {
		t.Fatalf("verdict = %v (%s), want Forward", got.Verdict, got.Reason)
	}
	if got.Dst != mustAddr(t, "10.87.0.5") {
		t.Fatalf("dst = %s, want 10.87.0.5", got.Dst)
	}
}

func TestDropsSpoofedSourceAddress(t *testing.T) {
	// Sender owns .2 but claims to be .3 — impersonation attempt.
	sender := route.Sender{VirtualIP: mustAddr(t, "10.87.0.2"), RoomID: "room-a"}
	pkt := ipv4(mustAddr(t, "10.87.0.3"), mustAddr(t, "10.87.0.5"))

	got := route.Decide(sender, pkt, route.Options{})
	if got.Verdict != route.VerdictDrop {
		t.Fatalf("verdict = %v, want Drop for spoofed source", got.Verdict)
	}
}

func TestDropsBroadcastByDefault(t *testing.T) {
	// This is the ancestor's fatal packet: LAN discovery broadcast.
	sender := route.Sender{VirtualIP: mustAddr(t, "10.87.0.2"), RoomID: "room-a"}
	pkt := ipv4(mustAddr(t, "10.87.0.2"), mustAddr(t, "10.87.0.15"))

	got := route.Decide(sender, pkt, route.Options{})
	if got.Verdict != route.VerdictDrop {
		t.Fatalf("verdict = %v, want Drop for broadcast", got.Verdict)
	}
}

func TestDropsMulticastByDefault(t *testing.T) {
	sender := route.Sender{VirtualIP: mustAddr(t, "10.87.0.2"), RoomID: "room-a"}
	pkt := ipv4(mustAddr(t, "10.87.0.2"), mustAddr(t, "239.255.255.250"))

	got := route.Decide(sender, pkt, route.Options{})
	if got.Verdict != route.VerdictDrop {
		t.Fatalf("verdict = %v, want Drop for multicast", got.Verdict)
	}
}

func TestFansOutMulticastOnlyWhenExplicitlyEnabled(t *testing.T) {
	sender := route.Sender{VirtualIP: mustAddr(t, "10.87.0.2"), RoomID: "room-a"}
	pkt := ipv4(mustAddr(t, "10.87.0.2"), mustAddr(t, "239.255.255.250"))

	got := route.Decide(sender, pkt, route.Options{AllowMulticast: true})
	if got.Verdict != route.VerdictFanout {
		t.Fatalf("verdict = %v, want Fanout when multicast enabled", got.Verdict)
	}
}

func TestDropsSenderWithNoRoom(t *testing.T) {
	sender := route.Sender{VirtualIP: mustAddr(t, "10.87.0.2"), RoomID: ""}
	pkt := ipv4(mustAddr(t, "10.87.0.2"), mustAddr(t, "10.87.0.5"))

	got := route.Decide(sender, pkt, route.Options{})
	if got.Verdict != route.VerdictDrop {
		t.Fatalf("verdict = %v, want Drop for roomless sender", got.Verdict)
	}
}

func TestDropsRunts(t *testing.T) {
	sender := route.Sender{VirtualIP: mustAddr(t, "10.87.0.2"), RoomID: "room-a"}
	for _, n := range []int{0, 1, 19} {
		got := route.Decide(sender, make([]byte, n), route.Options{})
		if got.Verdict != route.VerdictDrop {
			t.Errorf("len %d: verdict = %v, want Drop", n, got.Verdict)
		}
	}
}

func TestDropsNonIPv4(t *testing.T) {
	sender := route.Sender{VirtualIP: mustAddr(t, "10.87.0.2"), RoomID: "room-a"}
	pkt := ipv4(mustAddr(t, "10.87.0.2"), mustAddr(t, "10.87.0.5"))
	pkt[0] = 0x60 // IPv6

	got := route.Decide(sender, pkt, route.Options{})
	if got.Verdict != route.VerdictDrop {
		t.Fatalf("verdict = %v, want Drop for non-IPv4", got.Verdict)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd relay && go test ./internal/route/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the implementation**

`relay/internal/route/decision.go`:

```go
// Package route decides what the relay does with each inbound packet.
//
// The decision is a pure function so that anti-spoofing and room isolation —
// the two rules that must never regress — are directly testable.
package route

import (
	"net/netip"
)

// Verdict is the action the relay takes.
type Verdict int

const (
	// VerdictDrop discards the packet silently.
	VerdictDrop Verdict = iota
	// VerdictForward sends to exactly one peer, named by Dst.
	VerdictForward
	// VerdictFanout copies to every other member of the sender's room.
	// Only reachable when multicast is explicitly enabled.
	VerdictFanout
)

// Sender describes the authenticated session a packet arrived on.
type Sender struct {
	VirtualIP netip.Addr
	RoomID    string
}

// Options carries relay configuration that affects routing.
type Options struct {
	// AllowMulticast re-enables room-scoped multicast fanout. It defaults
	// to false: because clients are handed the host's address directly,
	// Dota never needs LAN discovery, and carrying broadcast traffic is
	// what collapsed the ancestor platform above ~1500 players.
	AllowMulticast bool
}

// Decision is the outcome for one packet. Reason is for metrics and logs.
type Decision struct {
	Verdict Verdict
	Dst     netip.Addr
	Reason  string
}

const ipv4HeaderLen = 20

// Decide inspects the inner IP header and returns the routing outcome.
func Decide(sender Sender, innerPacket []byte, opts Options) Decision {
	if sender.RoomID == "" {
		return Decision{Verdict: VerdictDrop, Reason: "sender not in a room"}
	}
	if len(innerPacket) < ipv4HeaderLen {
		return Decision{Verdict: VerdictDrop, Reason: "runt packet"}
	}
	if innerPacket[0]>>4 != 4 {
		return Decision{Verdict: VerdictDrop, Reason: "not IPv4"}
	}

	src := netip.AddrFrom4([4]byte(innerPacket[12:16]))
	dst := netip.AddrFrom4([4]byte(innerPacket[16:20]))

	// Anti-spoof. Without this any player can impersonate any other.
	if src != sender.VirtualIP {
		return Decision{Verdict: VerdictDrop, Reason: "source address spoofed"}
	}

	if isMulticastOrBroadcast(dst) {
		if !opts.AllowMulticast {
			return Decision{Verdict: VerdictDrop, Reason: "multicast disabled"}
		}
		return Decision{Verdict: VerdictFanout, Reason: "room multicast"}
	}

	return Decision{Verdict: VerdictForward, Dst: dst, Reason: "unicast"}
}

// isMulticastOrBroadcast reports whether dst is a multicast address
// (224.0.0.0/4), the all-ones broadcast, or a /28 subnet broadcast — the
// last host address of any room block, where the low nibble is 0xF.
func isMulticastOrBroadcast(dst netip.Addr) bool {
	b := dst.As4()
	if b[0] >= 224 {
		return true
	}
	if b == [4]byte{255, 255, 255, 255} {
		return true
	}
	return b[3]&0x0F == 0x0F
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd relay && go test ./internal/route/ -v`
Expected: PASS, 8 tests.

- [ ] **Step 5: Commit**

```bash
git add relay/internal/route
git commit -m "feat(route): enforce anti-spoof, room scoping and broadcast drop

Dropping broadcast entirely rather than scoping it removes the O(n^2)
fanout that capped DotaIranConnect around 1500 concurrent players."
```

---

### Task 5: Bounded per-peer send queue

Replaces the ancestor's goroutine-per-packet forwarding, which reordered game
traffic and would spawn roughly a million goroutines per second at target
scale.

**Files:**
- Create: `relay/internal/sendq/queue.go`
- Test: `relay/internal/sendq/queue_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Queue struct { ... }`
  - `func New(capacity int) *Queue`
  - `func (q *Queue) Push(pkt []byte) (dropped bool)` — copies `pkt`; drops the oldest entry when full.
  - `func (q *Queue) Pop(ctx context.Context) ([]byte, error)` — blocks until a packet is available or ctx is done.
  - `func (q *Queue) Drops() uint64` — total dropped, for per-peer connection-quality metrics.
  - `func (q *Queue) Close()`

- [ ] **Step 1: Write the failing test**

`relay/internal/sendq/queue_test.go`:

```go
package sendq_test

import (
	"context"
	"testing"
	"time"

	"finallobby/relay/internal/sendq"
)

func TestPreservesOrder(t *testing.T) {
	q := sendq.New(4)
	defer q.Close()
	for _, b := range []byte{1, 2, 3} {
		if dropped := q.Push([]byte{b}); dropped {
			t.Fatalf("unexpected drop pushing %d", b)
		}
	}
	ctx := context.Background()
	for _, want := range []byte{1, 2, 3} {
		got, err := q.Pop(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got[0] != want {
			t.Fatalf("got %d, want %d — ordering broken", got[0], want)
		}
	}
}

func TestDropsOldestWhenFull(t *testing.T) {
	q := sendq.New(2)
	defer q.Close()
	q.Push([]byte{1})
	q.Push([]byte{2})
	if dropped := q.Push([]byte{3}); !dropped {
		t.Fatal("expected a drop when pushing into a full queue")
	}
	if q.Drops() != 1 {
		t.Fatalf("Drops() = %d, want 1", q.Drops())
	}
	// Oldest (1) should be gone; 2 then 3 remain.
	ctx := context.Background()
	first, _ := q.Pop(ctx)
	second, _ := q.Pop(ctx)
	if first[0] != 2 || second[0] != 3 {
		t.Fatalf("got %d,%d want 2,3 — wrong entry evicted", first[0], second[0])
	}
}

func TestPushCopiesCallerBuffer(t *testing.T) {
	q := sendq.New(2)
	defer q.Close()
	buf := []byte{42}
	q.Push(buf)
	buf[0] = 99 // caller reuses its buffer
	got, err := q.Pop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 42 {
		t.Fatalf("got %d, want 42 — queue aliased the caller's buffer", got[0])
	}
}

func TestPopUnblocksOnContextCancel(t *testing.T) {
	q := sendq.New(2)
	defer q.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := q.Pop(ctx); err == nil {
		t.Fatal("expected error when context expires")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd relay && go test ./internal/sendq/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the implementation**

`relay/internal/sendq/queue.go`:

```go
// Package sendq provides a bounded, drop-oldest packet queue.
//
// Each connected peer owns one Queue drained by exactly one long-lived
// writer goroutine. Goroutine count therefore scales with the number of
// players, never with packet rate, and packet ordering per peer is
// preserved. Game traffic prefers a dropped packet over a delayed one, so
// overflow evicts the oldest entry rather than blocking the producer.
package sendq

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// ErrClosed is returned by Pop after Close.
var ErrClosed = errors.New("sendq: queue closed")

// Queue is a fixed-capacity FIFO of packet buffers. Safe for concurrent use.
type Queue struct {
	mu     sync.Mutex
	notify chan struct{}
	items  [][]byte
	cap    int
	closed bool
	drops  atomic.Uint64
}

// New returns a Queue holding at most capacity packets.
func New(capacity int) *Queue {
	if capacity < 1 {
		capacity = 1
	}
	return &Queue{
		notify: make(chan struct{}, 1),
		items:  make([][]byte, 0, capacity),
		cap:    capacity,
	}
}

// Push copies pkt into the queue. It reports whether a packet was dropped
// to make room. Push never blocks.
func (q *Queue) Push(pkt []byte) (dropped bool) {
	cp := make([]byte, len(pkt))
	copy(cp, pkt)

	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return false
	}
	if len(q.items) == q.cap {
		q.items = q.items[1:] // evict oldest
		dropped = true
		q.drops.Add(1)
	}
	q.items = append(q.items, cp)
	q.mu.Unlock()

	select {
	case q.notify <- struct{}{}:
	default: // a wakeup is already pending
	}
	return dropped
}

// Pop returns the oldest packet, blocking until one arrives, the context is
// done, or the queue is closed.
func (q *Queue) Pop(ctx context.Context) ([]byte, error) {
	for {
		q.mu.Lock()
		if q.closed {
			q.mu.Unlock()
			return nil, ErrClosed
		}
		if len(q.items) > 0 {
			item := q.items[0]
			q.items = q.items[1:]
			q.mu.Unlock()
			return item, nil
		}
		q.mu.Unlock()

		select {
		case <-q.notify:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Drops returns the cumulative number of packets evicted. Sustained drops
// are the earliest signal of a peer with a failing connection.
func (q *Queue) Drops() uint64 { return q.drops.Load() }

// Close releases waiters. Further Push calls are no-ops.
func (q *Queue) Close() {
	q.mu.Lock()
	q.closed = true
	q.items = nil
	q.mu.Unlock()
	select {
	case q.notify <- struct{}{}:
	default:
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd relay && go test ./internal/sendq/ -race -v`
Expected: PASS, 4 tests, no race warnings.

- [ ] **Step 5: Commit**

```bash
git add relay/internal/sendq
git commit -m "feat(sendq): add bounded drop-oldest per-peer send queue"
```

---

### Task 6: Session encryption with replay protection

**Files:**
- Create: `relay/internal/crypto/session.go`
- Test: `relay/internal/crypto/session_test.go`

**Interfaces:**
- Consumes: `wire.Header`, `wire.HeaderSize`.
- Produces:
  - `func NewSession(sendKey, recvKey []byte) (*Session, error)` — 32-byte keys.
  - `func (s *Session) Seal(dst []byte, h wire.Header, plaintext []byte) ([]byte, error)` — header is authenticated as additional data.
  - `func (s *Session) Open(packet []byte) (wire.Header, []byte, error)`
  - `var ErrReplay, ErrAuth, ErrKeySize error`

Replay window: 64 packets behind the highest accepted sequence.

- [ ] **Step 1: Write the failing test**

`relay/internal/crypto/session_test.go`:

```go
package crypto_test

import (
	"bytes"
	"errors"
	"testing"

	"finallobby/relay/internal/crypto"
	"finallobby/relay/internal/wire"
)

func pair(t *testing.T) (client, server *crypto.Session) {
	t.Helper()
	k1 := bytes.Repeat([]byte{0xA1}, 32)
	k2 := bytes.Repeat([]byte{0xB2}, 32)
	c, err := crypto.NewSession(k1, k2)
	if err != nil {
		t.Fatal(err)
	}
	s, err := crypto.NewSession(k2, k1) // mirrored
	if err != nil {
		t.Fatal(err)
	}
	return c, s
}

func TestSealOpenRoundTrip(t *testing.T) {
	client, server := pair(t)
	h := wire.Header{Version: wire.ProtocolVersion, Type: wire.TypeData, SessionID: 7, Sequence: 1}
	msg := []byte("hello dota")

	sealed, err := client.Seal(nil, h, msg)
	if err != nil {
		t.Fatal(err)
	}
	gotH, gotMsg, err := server.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if gotH != h {
		t.Fatalf("header = %+v, want %+v", gotH, h)
	}
	if !bytes.Equal(gotMsg, msg) {
		t.Fatalf("payload = %q, want %q", gotMsg, msg)
	}
}

func TestRejectsReplay(t *testing.T) {
	client, server := pair(t)
	h := wire.Header{Version: wire.ProtocolVersion, Type: wire.TypeData, SessionID: 7, Sequence: 5}
	sealed, _ := client.Seal(nil, h, []byte("x"))

	if _, _, err := server.Open(sealed); err != nil {
		t.Fatalf("first open: %v", err)
	}
	if _, _, err := server.Open(sealed); !errors.Is(err, crypto.ErrReplay) {
		t.Fatalf("second open err = %v, want ErrReplay", err)
	}
}

func TestAcceptsOutOfOrderWithinWindow(t *testing.T) {
	client, server := pair(t)
	mk := func(seq uint64) []byte {
		h := wire.Header{Version: wire.ProtocolVersion, Type: wire.TypeData, SessionID: 7, Sequence: seq}
		p, _ := client.Seal(nil, h, []byte("y"))
		return p
	}
	// Deliver 10 then 8 — reordering is normal on a lossy link and must
	// not be treated as an attack.
	if _, _, err := server.Open(mk(10)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := server.Open(mk(8)); err != nil {
		t.Fatalf("in-window reorder rejected: %v", err)
	}
}

func TestRejectsTamperedCiphertext(t *testing.T) {
	client, server := pair(t)
	h := wire.Header{Version: wire.ProtocolVersion, Type: wire.TypeData, SessionID: 7, Sequence: 1}
	sealed, _ := client.Seal(nil, h, []byte("secret"))
	sealed[len(sealed)-1] ^= 0xFF

	if _, _, err := server.Open(sealed); !errors.Is(err, crypto.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
}

func TestRejectsTamperedHeader(t *testing.T) {
	client, server := pair(t)
	h := wire.Header{Version: wire.ProtocolVersion, Type: wire.TypeData, SessionID: 7, Sequence: 1}
	sealed, _ := client.Seal(nil, h, []byte("secret"))
	sealed[2] ^= 0xFF // flip a SessionID bit — header is authenticated

	if _, _, err := server.Open(sealed); !errors.Is(err, crypto.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
}

func TestRejectsWrongKeySize(t *testing.T) {
	if _, err := crypto.NewSession(make([]byte, 16), make([]byte, 32)); !errors.Is(err, crypto.ErrKeySize) {
		t.Fatalf("err = %v, want ErrKeySize", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd relay && go test ./internal/crypto/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Add the dependency and write the implementation**

```bash
cd relay && go get golang.org/x/crypto/chacha20poly1305
```

`relay/internal/crypto/session.go`:

```go
// Package crypto provides the per-session AEAD used for tunnel data.
//
// The packet header travels in the clear but is authenticated as additional
// data, so the sequence number that seeds the nonce cannot be altered
// without detection.
package crypto

import (
	"encoding/binary"
	"errors"
	"sync"

	"golang.org/x/crypto/chacha20poly1305"

	"finallobby/relay/internal/wire"
)

var (
	ErrKeySize = errors.New("crypto: key must be 32 bytes")
	ErrAuth    = errors.New("crypto: authentication failed")
	ErrReplay  = errors.New("crypto: replayed or too-old sequence number")
)

// replayWindow is how far behind the highest accepted sequence a packet may
// arrive and still be accepted. Reordering is routine on a lossy link.
const replayWindow = 64

// Session holds the directional keys for one peer connection.
type Session struct {
	send, recv interface {
		Seal(dst, nonce, plaintext, additionalData []byte) []byte
		Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
	}

	mu      sync.Mutex
	highest uint64
	bitmap  uint64
	seen    bool
}

// NewSession builds a session from two 32-byte directional keys.
func NewSession(sendKey, recvKey []byte) (*Session, error) {
	if len(sendKey) != chacha20poly1305.KeySize || len(recvKey) != chacha20poly1305.KeySize {
		return nil, ErrKeySize
	}
	s, err := chacha20poly1305.New(sendKey)
	if err != nil {
		return nil, err
	}
	r, err := chacha20poly1305.New(recvKey)
	if err != nil {
		return nil, err
	}
	return &Session{send: s, recv: r}, nil
}

func nonceFor(seq uint64) []byte {
	var n [chacha20poly1305.NonceSize]byte // 12 bytes
	binary.BigEndian.PutUint64(n[4:], seq)
	return n[:]
}

// Seal encrypts plaintext and returns header||ciphertext appended to dst.
func (s *Session) Seal(dst []byte, h wire.Header, plaintext []byte) ([]byte, error) {
	hdr := make([]byte, wire.HeaderSize)
	wire.EncodeHeader(hdr, h)
	out := append(dst, hdr...)
	return s.send.Seal(out, nonceFor(h.Sequence), plaintext, hdr), nil
}

// Open authenticates and decrypts a packet, enforcing the replay window.
func (s *Session) Open(packet []byte) (wire.Header, []byte, error) {
	h, err := wire.DecodeHeader(packet)
	if err != nil {
		return wire.Header{}, nil, err
	}
	hdr := packet[:wire.HeaderSize]
	plaintext, err := s.recv.Open(nil, nonceFor(h.Sequence), packet[wire.HeaderSize:], hdr)
	if err != nil {
		return wire.Header{}, nil, ErrAuth
	}
	if err := s.checkReplay(h.Sequence); err != nil {
		return wire.Header{}, nil, err
	}
	return h, plaintext, nil
}

// checkReplay implements a sliding bitmap window.
func (s *Session) checkReplay(seq uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.seen {
		s.seen, s.highest, s.bitmap = true, seq, 1
		return nil
	}
	switch {
	case seq > s.highest:
		shift := seq - s.highest
		if shift >= 64 {
			s.bitmap = 0
		} else {
			s.bitmap <<= shift
		}
		s.bitmap |= 1
		s.highest = seq
		return nil
	default:
		diff := s.highest - seq
		if diff >= replayWindow {
			return ErrReplay
		}
		mask := uint64(1) << diff
		if s.bitmap&mask != 0 {
			return ErrReplay
		}
		s.bitmap |= mask
		return nil
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd relay && go test ./internal/crypto/ -race -v`
Expected: PASS, 6 tests.

- [ ] **Step 5: Commit**

```bash
git add relay/internal/crypto relay/go.mod relay/go.sum
git commit -m "feat(crypto): add AEAD session with authenticated header and replay window"
```

---

### Task 7: Noise NK handshake

**Files:**
- Create: `relay/internal/crypto/handshake.go`
- Test: `relay/internal/crypto/handshake_test.go`

**Interfaces:**
- Consumes: `crypto.NewSession`.
- Produces:
  - `func GenerateStaticKeypair() (pub, priv []byte, err error)`
  - `func ClientHandshake(relayStaticPub, ticket []byte) (msg1 []byte, finish func(msg2 []byte) (*Session, error), err error)`
  - `func ServerHandshake(relayStaticPriv, msg1 []byte) (ticket []byte, msg2 []byte, sess *Session, err error)`
  - `var ErrHandshake error`

The ticket travels inside the encrypted Noise payload; it is never sent in
plaintext. This closes the ancestor's gap where the accept packet was sent
unencrypted.

- [ ] **Step 1: Write the failing test**

`relay/internal/crypto/handshake_test.go`:

```go
package crypto_test

import (
	"bytes"
	"testing"

	"finallobby/relay/internal/crypto"
	"finallobby/relay/internal/wire"
)

func TestHandshakeEstablishesMatchingSessions(t *testing.T) {
	pub, priv, err := crypto.GenerateStaticKeypair()
	if err != nil {
		t.Fatal(err)
	}
	ticket := []byte("ticket-abc-123")

	msg1, finish, err := crypto.ClientHandshake(pub, ticket)
	if err != nil {
		t.Fatal(err)
	}
	gotTicket, msg2, serverSess, err := crypto.ServerHandshake(priv, msg1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotTicket, ticket) {
		t.Fatalf("ticket = %q, want %q", gotTicket, ticket)
	}
	clientSess, err := finish(msg2)
	if err != nil {
		t.Fatal(err)
	}

	// Prove the two derived sessions actually interoperate.
	h := wire.Header{Version: wire.ProtocolVersion, Type: wire.TypeData, SessionID: 1, Sequence: 1}
	sealed, err := clientSess.Seal(nil, h, []byte("ping"))
	if err != nil {
		t.Fatal(err)
	}
	_, got, err := serverSess.Open(sealed)
	if err != nil {
		t.Fatalf("server could not open client packet: %v", err)
	}
	if string(got) != "ping" {
		t.Fatalf("got %q, want \"ping\"", got)
	}
}

func TestTicketIsNotSentInPlaintext(t *testing.T) {
	pub, _, _ := crypto.GenerateStaticKeypair()
	ticket := []byte("SUPERSECRETTICKET")

	msg1, _, err := crypto.ClientHandshake(pub, ticket)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(msg1, ticket) {
		t.Fatal("ticket appears in plaintext in the first handshake message")
	}
}

func TestServerRejectsGarbageHandshake(t *testing.T) {
	_, priv, _ := crypto.GenerateStaticKeypair()
	if _, _, _, err := crypto.ServerHandshake(priv, []byte("not a handshake")); err == nil {
		t.Fatal("expected error for malformed handshake")
	}
}

func TestClientRejectsWrongRelayKey(t *testing.T) {
	wrongPub, _, _ := crypto.GenerateStaticKeypair()
	_, realPriv, _ := crypto.GenerateStaticKeypair()

	msg1, _, err := crypto.ClientHandshake(wrongPub, []byte("t"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := crypto.ServerHandshake(realPriv, msg1); err == nil {
		t.Fatal("server accepted a handshake addressed to a different key")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd relay && go test ./internal/crypto/ -run Handshake -v`
Expected: FAIL — undefined functions.

- [ ] **Step 3: Add the dependency and write the implementation**

```bash
cd relay && go get github.com/flynn/noise
```

`relay/internal/crypto/handshake.go`:

```go
package crypto

import (
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/flynn/noise"
)

// ErrHandshake covers every handshake failure. The relay never reports why
// a handshake failed to the peer.
var ErrHandshake = errors.New("crypto: handshake failed")

// cipherSuite is Noise_NK_25519_ChaChaPoly_BLAKE2s.
//
// NK is the right pattern here: the client knows the relay's static public
// key (shipped in the binary) but has no static key of its own. It proves
// its right to connect with a short-lived coordinator ticket carried inside
// the encrypted handshake payload.
var cipherSuite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashBLAKE2s)

// GenerateStaticKeypair produces the relay's long-term identity.
func GenerateStaticKeypair() (pub, priv []byte, err error) {
	kp, err := cipherSuite.GenerateKeypair(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrHandshake, err)
	}
	return kp.Public, kp.Private, nil
}

// ClientHandshake builds the first message and returns a finish function
// that completes the handshake once the relay replies.
func ClientHandshake(relayStaticPub, ticket []byte) (msg1 []byte, finish func([]byte) (*Session, error), err error) {
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:   cipherSuite,
		Pattern:       noise.HandshakeNK,
		Initiator:     true,
		PeerStatic:    relayStaticPub,
		Random:        rand.Reader,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrHandshake, err)
	}
	msg1, _, _, err = hs.WriteMessage(nil, ticket)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrHandshake, err)
	}

	finish = func(msg2 []byte) (*Session, error) {
		_, cs1, cs2, err := hs.ReadMessage(nil, msg2)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrHandshake, err)
		}
		// Initiator sends with cs1, receives with cs2.
		return sessionFromNoise(cs1, cs2)
	}
	return msg1, finish, nil
}

// ServerHandshake consumes the client's first message, recovers the ticket,
// and produces the reply plus the established session.
func ServerHandshake(relayStaticPriv, msg1 []byte) (ticket, msg2 []byte, sess *Session, err error) {
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:   cipherSuite,
		Pattern:       noise.HandshakeNK,
		Initiator:     false,
		StaticKeypair: noise.DHKey{Private: relayStaticPriv, Public: publicFrom(relayStaticPriv)},
		Random:        rand.Reader,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: %v", ErrHandshake, err)
	}
	ticket, _, _, err = hs.ReadMessage(nil, msg1)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: %v", ErrHandshake, err)
	}
	msg2, cs1, cs2, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: %v", ErrHandshake, err)
	}
	// Responder mirrors the initiator: sends with cs2, receives with cs1.
	sess, err = sessionFromNoise(cs2, cs1)
	if err != nil {
		return nil, nil, nil, err
	}
	return ticket, msg2, sess, nil
}

func publicFrom(priv []byte) []byte {
	kp, err := noise.DH25519.GenerateKeypair(nil)
	_ = kp
	_ = err
	pub, _ := noise.DH25519.DH(priv, basepoint())
	return pub
}

func basepoint() []byte {
	b := make([]byte, 32)
	b[0] = 9
	return b
}

// sessionFromNoise adapts Noise CipherStates onto our Session type by
// extracting the derived keys.
func sessionFromNoise(send, recv *noise.CipherState) (*Session, error) {
	if send == nil || recv == nil {
		return nil, ErrHandshake
	}
	return NewSession(send.Key(), recv.Key())
}
```

**Note for the implementer:** `noise.CipherState.Key()` exists in
`github.com/flynn/noise` at v1.1.0+. Verify the version resolved by
`go get` exposes it; if not, pin `v1.1.0`. `publicFrom` derives the public
key by scalar-multiplying the base point — confirm `noise.DH25519.DH`
accepts the base point directly, and if the API differs, store the public
key alongside the private key at generation time instead and pass it in.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd relay && go test ./internal/crypto/ -race -v`
Expected: PASS, 10 tests total in the package.

- [ ] **Step 5: Commit**

```bash
git add relay/internal/crypto relay/go.mod relay/go.sum
git commit -m "feat(crypto): add Noise NK handshake carrying the session ticket encrypted"
```

---

### Task 8: Session and room membership tables

**Files:**
- Create: `relay/internal/route/table.go`
- Test: `relay/internal/route/table_test.go`

**Interfaces:**
- Consumes: `route.Sender`, `sendq.Queue`.
- Produces:
  - `type Peer struct { SessionID uint32; VirtualIP netip.Addr; RoomID string; Remote netip.AddrPort; Queue *sendq.Queue }`
  - `type Table struct { ... }`, `func NewTable() *Table`
  - `func (t *Table) Add(p *Peer)`
  - `func (t *Table) RemoveBySession(id uint32) *Peer`
  - `func (t *Table) ByVirtualIP(ip netip.Addr) (*Peer, bool)`
  - `func (t *Table) BySession(id uint32) (*Peer, bool)`
  - `func (t *Table) RoomMembers(roomID string) []*Peer`
  - `func (t *Table) SetRoom(sessionID uint32, roomID string) bool`
  - `func (t *Table) Count() int`

- [ ] **Step 1: Write the failing test**

`relay/internal/route/table_test.go`:

```go
package route_test

import (
	"net/netip"
	"testing"

	"finallobby/relay/internal/route"
	"finallobby/relay/internal/sendq"
)

func peer(t *testing.T, id uint32, ip, room string) *route.Peer {
	t.Helper()
	return &route.Peer{
		SessionID: id,
		VirtualIP: mustAddr(t, ip),
		RoomID:    room,
		Queue:     sendq.New(8),
	}
}

func TestLookupByVirtualIPAndSession(t *testing.T) {
	tbl := route.NewTable()
	p := peer(t, 1, "10.87.0.2", "room-a")
	tbl.Add(p)

	if got, ok := tbl.ByVirtualIP(mustAddr(t, "10.87.0.2")); !ok || got.SessionID != 1 {
		t.Fatal("ByVirtualIP failed")
	}
	if got, ok := tbl.BySession(1); !ok || got.VirtualIP != p.VirtualIP {
		t.Fatal("BySession failed")
	}
}

func TestRoomMembersOnlyReturnsSameRoom(t *testing.T) {
	tbl := route.NewTable()
	tbl.Add(peer(t, 1, "10.87.0.2", "room-a"))
	tbl.Add(peer(t, 2, "10.87.0.3", "room-a"))
	tbl.Add(peer(t, 3, "10.87.1.2", "room-b"))

	if got := len(tbl.RoomMembers("room-a")); got != 2 {
		t.Fatalf("room-a members = %d, want 2", got)
	}
	if got := len(tbl.RoomMembers("room-b")); got != 1 {
		t.Fatalf("room-b members = %d, want 1", got)
	}
}

func TestRemoveClearsAllIndexes(t *testing.T) {
	tbl := route.NewTable()
	tbl.Add(peer(t, 1, "10.87.0.2", "room-a"))

	if removed := tbl.RemoveBySession(1); removed == nil {
		t.Fatal("RemoveBySession returned nil")
	}
	if _, ok := tbl.ByVirtualIP(mustAddr(t, "10.87.0.2")); ok {
		t.Error("peer still reachable by virtual IP after removal")
	}
	if _, ok := tbl.BySession(1); ok {
		t.Error("peer still reachable by session after removal")
	}
	if len(tbl.RoomMembers("room-a")) != 0 {
		t.Error("peer still listed as a room member after removal")
	}
	if tbl.Count() != 0 {
		t.Errorf("Count() = %d, want 0", tbl.Count())
	}
}

func TestSetRoomMovesMembership(t *testing.T) {
	tbl := route.NewTable()
	tbl.Add(peer(t, 1, "10.87.0.2", "room-a"))

	if !tbl.SetRoom(1, "room-b") {
		t.Fatal("SetRoom returned false for a known session")
	}
	if len(tbl.RoomMembers("room-a")) != 0 {
		t.Error("peer still in old room")
	}
	if len(tbl.RoomMembers("room-b")) != 1 {
		t.Error("peer not in new room")
	}
	if tbl.SetRoom(99, "room-c") {
		t.Error("SetRoom returned true for an unknown session")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd relay && go test ./internal/route/ -run Table -v`
Expected: FAIL — undefined `route.NewTable`.

- [ ] **Step 3: Write the implementation**

`relay/internal/route/table.go`:

```go
package route

import (
	"net/netip"
	"sync"

	"finallobby/relay/internal/sendq"
)

// Peer is one authenticated, connected client.
type Peer struct {
	SessionID uint32
	VirtualIP netip.Addr
	RoomID    string
	Remote    netip.AddrPort
	Queue     *sendq.Queue
}

// Table indexes peers by session and by virtual IP, and groups them by room.
// All lookups are on the packet hot path, so reads take a shared lock.
type Table struct {
	mu       sync.RWMutex
	bySess   map[uint32]*Peer
	byIP     map[netip.Addr]*Peer
	byRoom   map[string]map[uint32]*Peer
}

func NewTable() *Table {
	return &Table{
		bySess: make(map[uint32]*Peer),
		byIP:   make(map[netip.Addr]*Peer),
		byRoom: make(map[string]map[uint32]*Peer),
	}
}

func (t *Table) Add(p *Peer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.bySess[p.SessionID] = p
	t.byIP[p.VirtualIP] = p
	t.addToRoomLocked(p, p.RoomID)
}

func (t *Table) addToRoomLocked(p *Peer, room string) {
	if room == "" {
		return
	}
	if t.byRoom[room] == nil {
		t.byRoom[room] = make(map[uint32]*Peer)
	}
	t.byRoom[room][p.SessionID] = p
}

func (t *Table) removeFromRoomLocked(p *Peer, room string) {
	if room == "" {
		return
	}
	if members := t.byRoom[room]; members != nil {
		delete(members, p.SessionID)
		if len(members) == 0 {
			delete(t.byRoom, room)
		}
	}
}

// RemoveBySession removes a peer and returns it, or nil if unknown.
func (t *Table) RemoveBySession(id uint32) *Peer {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.bySess[id]
	if !ok {
		return nil
	}
	delete(t.bySess, id)
	delete(t.byIP, p.VirtualIP)
	t.removeFromRoomLocked(p, p.RoomID)
	return p
}

func (t *Table) ByVirtualIP(ip netip.Addr) (*Peer, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	p, ok := t.byIP[ip]
	return p, ok
}

func (t *Table) BySession(id uint32) (*Peer, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	p, ok := t.bySess[id]
	return p, ok
}

// RoomMembers returns a snapshot of the peers in roomID.
func (t *Table) RoomMembers(roomID string) []*Peer {
	t.mu.RLock()
	defer t.mu.RUnlock()
	members := t.byRoom[roomID]
	out := make([]*Peer, 0, len(members))
	for _, p := range members {
		out = append(out, p)
	}
	return out
}

// SetRoom moves a peer between rooms. Reports whether the session existed.
func (t *Table) SetRoom(sessionID uint32, roomID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.bySess[sessionID]
	if !ok {
		return false
	}
	t.removeFromRoomLocked(p, p.RoomID)
	p.RoomID = roomID
	t.addToRoomLocked(p, roomID)
	return true
}

func (t *Table) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.bySess)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd relay && go test ./internal/route/ -race -v`
Expected: PASS, 12 tests in the package.

- [ ] **Step 5: Commit**

```bash
git add relay/internal/route
git commit -m "feat(route): add session and room membership tables"
```

---

### Task 9: Relay server assembly

Wires the socket, handshake, tables, routing decision and send queues into a
running relay. Ends with an integration test proving two simulated peers can
exchange packets and that a third peer in another room cannot see them.

**Files:**
- Create: `relay/internal/server/server.go`
- Create: `relay/cmd/relay/main.go`
- Test: `relay/internal/server/server_test.go`

**Interfaces:**
- Consumes: everything from Tasks 2, 4–8.
- Produces:
  - `type Config struct { Listen string; StaticPriv []byte; AllowMulticast bool; QueueDepth int; ValidateTicket func(ticket []byte) (TicketClaims, error) }`
  - `type TicketClaims struct { VirtualIP netip.Addr; RoomID string }`
  - `type Server struct { ... }`
  - `func New(cfg Config) (*Server, error)`
  - `func (s *Server) Serve(ctx context.Context) error`
  - `func (s *Server) LocalAddr() netip.AddrPort`
  - `func (s *Server) Table() *route.Table`

- [ ] **Step 1: Write the failing integration test**

`relay/internal/server/server_test.go`:

```go
package server_test

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"finallobby/relay/internal/crypto"
	"finallobby/relay/internal/server"
	"finallobby/relay/internal/wire"
)

// testPeer is a minimal client used to drive the relay in tests.
type testPeer struct {
	conn *net.UDPConn
	sess *crypto.Session
	seq  uint64
}

func dialPeer(t *testing.T, addr netip.AddrPort, relayPub []byte, ticket string) *testPeer {
	t.Helper()
	conn, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(addr))
	if err != nil {
		t.Fatal(err)
	}
	msg1, finish, err := crypto.ClientHandshake(relayPub, []byte(ticket))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(msg1); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2048)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("no handshake reply: %v", err)
	}
	sess, err := finish(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	return &testPeer{conn: conn, sess: sess}
}

func (p *testPeer) send(t *testing.T, inner []byte) {
	t.Helper()
	p.seq++
	h := wire.Header{Version: wire.ProtocolVersion, Type: wire.TypeData, Sequence: p.seq}
	pkt, err := p.sess.Seal(nil, h, inner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.conn.Write(pkt); err != nil {
		t.Fatal(err)
	}
}

func (p *testPeer) recv(t *testing.T, within time.Duration) ([]byte, bool) {
	t.Helper()
	buf := make([]byte, 2048)
	_ = p.conn.SetReadDeadline(time.Now().Add(within))
	n, err := p.conn.Read(buf)
	if err != nil {
		return nil, false
	}
	_, inner, err := p.sess.Open(buf[:n])
	if err != nil {
		t.Fatalf("could not open relayed packet: %v", err)
	}
	return inner, true
}

func ipv4(src, dst netip.Addr) []byte {
	p := make([]byte, 20)
	p[0] = 0x45
	s, d := src.As4(), dst.As4()
	copy(p[12:16], s[:])
	copy(p[16:20], d[:])
	return p
}

func startRelay(t *testing.T) (*server.Server, []byte, context.CancelFunc) {
	t.Helper()
	pub, priv, err := crypto.GenerateStaticKeypair()
	if err != nil {
		t.Fatal(err)
	}
	// Ticket string is "roomID|virtualIP" for the test.
	validate := func(ticket []byte) (server.TicketClaims, error) {
		var room, ip string
		for i, c := range ticket {
			if c == '|' {
				room, ip = string(ticket[:i]), string(ticket[i+1:])
				break
			}
		}
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			return server.TicketClaims{}, err
		}
		return server.TicketClaims{RoomID: room, VirtualIP: addr}, nil
	}
	srv, err := server.New(server.Config{
		Listen:         "127.0.0.1:0",
		StaticPriv:     priv,
		QueueDepth:     64,
		ValidateTicket: validate,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Serve(ctx) }()
	// Give the socket a moment to come up.
	time.Sleep(100 * time.Millisecond)
	return srv, pub, cancel
}

func TestRelayForwardsBetweenRoomMembers(t *testing.T) {
	srv, pub, cancel := startRelay(t)
	defer cancel()

	alice := dialPeer(t, srv.LocalAddr(), pub, "room-a|10.87.0.2")
	bob := dialPeer(t, srv.LocalAddr(), pub, "room-a|10.87.0.3")
	time.Sleep(100 * time.Millisecond)

	alice.send(t, ipv4(netip.MustParseAddr("10.87.0.2"), netip.MustParseAddr("10.87.0.3")))

	got, ok := bob.recv(t, 2*time.Second)
	if !ok {
		t.Fatal("bob did not receive alice's packet")
	}
	if len(got) < 20 {
		t.Fatalf("received %d bytes, want a full IPv4 header", len(got))
	}
}

func TestRelayDoesNotLeakAcrossRooms(t *testing.T) {
	srv, pub, cancel := startRelay(t)
	defer cancel()

	alice := dialPeer(t, srv.LocalAddr(), pub, "room-a|10.87.0.2")
	eve := dialPeer(t, srv.LocalAddr(), pub, "room-b|10.87.1.2")
	time.Sleep(100 * time.Millisecond)

	// Alice addresses eve's virtual IP directly. Different room: must not arrive.
	alice.send(t, ipv4(netip.MustParseAddr("10.87.0.2"), netip.MustParseAddr("10.87.1.2")))

	if _, ok := eve.recv(t, 700*time.Millisecond); ok {
		t.Fatal("ROOM ISOLATION BREACH: eve received a packet from another room")
	}
}

func TestRelayDropsBroadcast(t *testing.T) {
	srv, pub, cancel := startRelay(t)
	defer cancel()

	alice := dialPeer(t, srv.LocalAddr(), pub, "room-a|10.87.0.2")
	bob := dialPeer(t, srv.LocalAddr(), pub, "room-a|10.87.0.3")
	time.Sleep(100 * time.Millisecond)

	alice.send(t, ipv4(netip.MustParseAddr("10.87.0.2"), netip.MustParseAddr("10.87.0.15")))

	if _, ok := bob.recv(t, 700*time.Millisecond); ok {
		t.Fatal("broadcast was forwarded; it must be dropped by default")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd relay && go test ./internal/server/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the implementation**

`relay/internal/server/server.go`:

```go
// Package server assembles the relay: one UDP socket, one reader goroutine,
// and one writer goroutine per connected peer.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/netip"

	"finallobby/relay/internal/crypto"
	"finallobby/relay/internal/route"
	"finallobby/relay/internal/sendq"
	"finallobby/relay/internal/wire"
)

// maxDatagram bounds a single read. Wintun MTU is 1300; this leaves ample
// room for headers and the AEAD tag.
const maxDatagram = 1600

// TicketClaims is what a validated ticket authorises.
type TicketClaims struct {
	VirtualIP netip.Addr
	RoomID    string
}

// Config configures a relay.
type Config struct {
	Listen         string
	StaticPriv     []byte
	AllowMulticast bool
	QueueDepth     int
	// ValidateTicket is supplied by the coordinator integration. It must be
	// safe for concurrent use.
	ValidateTicket func(ticket []byte) (TicketClaims, error)
}

// Server is the relay.
type Server struct {
	cfg   Config
	conn  *net.UDPConn
	table *route.Table
	log   *slog.Logger
}

func New(cfg Config) (*Server, error) {
	if cfg.ValidateTicket == nil {
		return nil, errors.New("server: ValidateTicket is required")
	}
	if cfg.QueueDepth <= 0 {
		cfg.QueueDepth = 256
	}
	addr, err := net.ResolveUDPAddr("udp", cfg.Listen)
	if err != nil {
		return nil, fmt.Errorf("server: resolve %q: %w", cfg.Listen, err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("server: listen: %w", err)
	}
	return &Server{
		cfg:   cfg,
		conn:  conn,
		table: route.NewTable(),
		log:   slog.Default(),
	}, nil
}

func (s *Server) LocalAddr() netip.AddrPort {
	return s.conn.LocalAddr().(*net.UDPAddr).AddrPort()
}

func (s *Server) Table() *route.Table { return s.table }

// Serve runs the read loop until ctx is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		_ = s.conn.Close()
	}()

	buf := make([]byte, maxDatagram)
	for {
		n, from, err := s.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			s.log.Warn("read error", "err", err)
			continue
		}
		s.handle(ctx, buf[:n], from)
	}
}

func (s *Server) handle(ctx context.Context, pkt []byte, from netip.AddrPort) {
	// A packet that parses as one of our headers belongs to an existing
	// session; anything else is treated as a handshake attempt.
	if h, err := wire.DecodeHeader(pkt); err == nil && h.Type == wire.TypeData {
		s.handleData(h, pkt, from)
		return
	}
	s.handleHandshake(ctx, pkt, from)
}

func (s *Server) handleHandshake(ctx context.Context, pkt []byte, from netip.AddrPort) {
	ticket, msg2, sess, err := crypto.ServerHandshake(s.cfg.StaticPriv, pkt)
	if err != nil {
		s.log.Debug("handshake rejected", "from", from)
		return
	}
	claims, err := s.cfg.ValidateTicket(ticket)
	if err != nil {
		s.log.Debug("ticket rejected", "from", from)
		return
	}

	// Replace any previous session holding this virtual IP — this is what
	// makes reconnect keep the same address.
	if old, ok := s.table.ByVirtualIP(claims.VirtualIP); ok {
		if removed := s.table.RemoveBySession(old.SessionID); removed != nil {
			removed.Queue.Close()
		}
	}

	peer := &route.Peer{
		SessionID: rand.Uint32(),
		VirtualIP: claims.VirtualIP,
		RoomID:    claims.RoomID,
		Remote:    from,
		Queue:     sendq.New(s.cfg.QueueDepth),
	}
	s.table.Add(peer)
	s.sessions.Store(peer.SessionID, sess)

	if _, err := s.conn.WriteToUDPAddrPort(msg2, from); err != nil {
		s.log.Warn("handshake reply failed", "err", err)
		return
	}
	// One writer goroutine per peer, for the peer's lifetime.
	go s.writeLoop(ctx, peer, sess)
	s.log.Info("peer connected", "vip", claims.VirtualIP, "room", claims.RoomID)
}

func (s *Server) handleData(h wire.Header, pkt []byte, from netip.AddrPort) {
	peer, ok := s.table.BySession(h.SessionID)
	if !ok {
		return
	}
	sessAny, ok := s.sessions.Load(h.SessionID)
	if !ok {
		return
	}
	sess := sessAny.(*crypto.Session)

	_, inner, err := sess.Open(pkt)
	if err != nil {
		return
	}
	// Keep the peer's remote address current so NAT rebinding survives.
	peer.Remote = from

	decision := route.Decide(
		route.Sender{VirtualIP: peer.VirtualIP, RoomID: peer.RoomID},
		inner,
		route.Options{AllowMulticast: s.cfg.AllowMulticast},
	)

	switch decision.Verdict {
	case route.VerdictForward:
		dst, ok := s.table.ByVirtualIP(decision.Dst)
		if !ok || dst.RoomID != peer.RoomID {
			return // unknown peer, or cross-room: drop
		}
		dst.Queue.Push(inner)
	case route.VerdictFanout:
		for _, m := range s.table.RoomMembers(peer.RoomID) {
			if m.SessionID != peer.SessionID {
				m.Queue.Push(inner)
			}
		}
	default:
		// dropped
	}
}

// writeLoop drains one peer's queue. Exactly one of these runs per peer,
// which is what keeps goroutine count proportional to players rather than
// to packet rate.
func (s *Server) writeLoop(ctx context.Context, peer *route.Peer, sess *crypto.Session) {
	var seq uint64
	for {
		inner, err := peer.Queue.Pop(ctx)
		if err != nil {
			return
		}
		seq++
		h := wire.Header{
			Version:   wire.ProtocolVersion,
			Type:      wire.TypeData,
			SessionID: peer.SessionID,
			Sequence:  seq,
		}
		out, err := sess.Seal(nil, h, inner)
		if err != nil {
			continue
		}
		if _, err := s.conn.WriteToUDPAddrPort(out, peer.Remote); err != nil {
			s.log.Debug("write failed", "vip", peer.VirtualIP, "err", err)
		}
	}
}
```

Add the sessions map to the struct — declare it alongside `table`:

```go
// in type Server struct:
	sessions sync.Map // sessionID (uint32) -> *crypto.Session
```

and add `"sync"` to the imports.

**Note for the implementer:** the test client seals with `SessionID: 0`
because it does not learn its session ID from the handshake. Extend the
handshake reply payload to carry the assigned session ID and virtual IP, and
have the client echo that ID in subsequent headers. Add a test asserting the
client receives its assigned virtual IP in the reply before moving on — the
net-service in Task 11 depends on it.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd relay && go test ./internal/server/ -race -v`
Expected: PASS, 3 tests. The isolation test is the one that must never regress.

- [ ] **Step 5: Write the relay entrypoint**

`relay/cmd/relay/main.go`:

```go
package main

import (
	"context"
	"encoding/hex"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"finallobby/relay/internal/server"
)

func main() {
	listen := flag.String("listen", "0.0.0.0:443", "UDP listen address")
	keyHex := flag.String("static-key", "", "hex-encoded relay static private key")
	coordinator := flag.String("coordinator", "http://127.0.0.1:7001", "coordinator base URL")
	allowMulticast := flag.Bool("allow-multicast", false, "re-enable room-scoped multicast fanout")
	flag.Parse()

	priv, err := hex.DecodeString(*keyHex)
	if err != nil || len(priv) != 32 {
		slog.Error("static-key must be 64 hex characters")
		os.Exit(1)
	}

	srv, err := server.New(server.Config{
		Listen:         *listen,
		StaticPriv:     priv,
		AllowMulticast: *allowMulticast,
		QueueDepth:     256,
		ValidateTicket: newCoordinatorValidator(*coordinator),
	})
	if err != nil {
		slog.Error("relay failed to start", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	slog.Info("relay listening", "addr", *listen, "multicast", *allowMulticast)
	if err := srv.Serve(ctx); err != nil {
		slog.Error("relay stopped", "err", err)
		os.Exit(1)
	}
}
```

Implement `newCoordinatorValidator` in `relay/cmd/relay/validator.go` as an
HTTP POST to `{coordinator}/internal/validate-ticket` carrying the ticket
bytes, returning `server.TicketClaims`. Cache positive results for 30 seconds
keyed by ticket hash so a coordinator restart does not stall every handshake.

- [ ] **Step 6: Commit**

```bash
git add relay/internal/server relay/cmd/relay
git commit -m "feat(server): assemble relay with per-peer writers and room isolation"
```

---

### Task 10: Room state machine

Implements the CEO's room rules exactly: locked rooms reject joins, kicks
block for 5 minutes, host departure closes the room after a 2-minute grace.

**Files:**
- Create: `coordinator/internal/room/state.go`
- Test: `coordinator/internal/room/state_test.go`

**Interfaces:**
- Consumes: `ipam`.
- Produces:
  - `type Status string` with `StatusOpen`, `StatusLocked`, `StatusOpenToNew`, `StatusClosed`.
  - `type Room struct { ID string; Index int; HostID string; Status Status; Slots [10]string; Spectators [3]string; KickedUntil map[string]time.Time; HostGraceUntil time.Time }`
  - `func NewRoom(id string, index int, hostID string, now time.Time) *Room`
  - `func (r *Room) Join(playerID string, now time.Time) (slot int, err error)`
  - `func (r *Room) Leave(playerID string, now time.Time)`
  - `func (r *Room) Kick(actorID, targetID string, now time.Time) error`
  - `func (r *Room) SetStatus(actorID string, s Status, now time.Time) error`
  - `func (r *Room) Tick(now time.Time)` — closes the room when the host grace expires.
  - Errors: `ErrRoomLocked, ErrRoomFull, ErrKickBlocked, ErrNotHost, ErrRoomClosed, ErrAlreadyJoined`

Constants: `HostGracePeriod = 2 * time.Minute`, `KickBlockPeriod = 5 * time.Minute`.

- [ ] **Step 1: Write the failing test**

`coordinator/internal/room/state_test.go`:

```go
package room_test

import (
	"errors"
	"testing"
	"time"

	"finallobby/coordinator/internal/room"
)

var t0 = time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

func newRoom(t *testing.T) *room.Room {
	t.Helper()
	return room.NewRoom("room-a", 0, "host-1", t0)
}

func TestHostOccupiesSlotZero(t *testing.T) {
	r := newRoom(t)
	if r.Slots[0] != "host-1" {
		t.Fatalf("slot 0 = %q, want host-1", r.Slots[0])
	}
	if r.Status != room.StatusOpen {
		t.Fatalf("status = %q, want Open", r.Status)
	}
}

func TestJoinFillsLowestFreeSlot(t *testing.T) {
	r := newRoom(t)
	slot, err := r.Join("p2", t0)
	if err != nil {
		t.Fatal(err)
	}
	if slot != 1 {
		t.Fatalf("slot = %d, want 1", slot)
	}
}

func TestLockedRoomRejectsNewPlayers(t *testing.T) {
	r := newRoom(t)
	if err := r.SetStatus("host-1", room.StatusLocked, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Join("p2", t0); !errors.Is(err, room.ErrRoomLocked) {
		t.Fatalf("err = %v, want ErrRoomLocked", err)
	}
}

func TestHostCanReopenLockedRoomForReplacements(t *testing.T) {
	r := newRoom(t)
	_, _ = r.Join("p2", t0)
	_ = r.SetStatus("host-1", room.StatusLocked, t0)
	r.Leave("p2", t0) // abandons mid-match

	if err := r.SetStatus("host-1", room.StatusOpenToNew, t0); err != nil {
		t.Fatal(err)
	}
	slot, err := r.Join("p3", t0)
	if err != nil {
		t.Fatalf("replacement could not join: %v", err)
	}
	if slot != 1 {
		t.Fatalf("replacement got slot %d, want the vacated slot 1", slot)
	}
}

func TestOnlyHostChangesStatus(t *testing.T) {
	r := newRoom(t)
	_, _ = r.Join("p2", t0)
	if err := r.SetStatus("p2", room.StatusLocked, t0); !errors.Is(err, room.ErrNotHost) {
		t.Fatalf("err = %v, want ErrNotHost", err)
	}
}

func TestKickedPlayerBlockedForFiveMinutes(t *testing.T) {
	r := newRoom(t)
	_, _ = r.Join("p2", t0)
	if err := r.Kick("host-1", "p2", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Join("p2", t0.Add(4*time.Minute)); !errors.Is(err, room.ErrKickBlocked) {
		t.Fatalf("at 4min err = %v, want ErrKickBlocked", err)
	}
	if _, err := r.Join("p2", t0.Add(5*time.Minute+time.Second)); err != nil {
		t.Fatalf("after 5min block expired, join failed: %v", err)
	}
}

func TestPlayerWhoLeftMayRejoinImmediately(t *testing.T) {
	r := newRoom(t)
	_, _ = r.Join("p2", t0)
	r.Leave("p2", t0)
	if _, err := r.Join("p2", t0.Add(time.Second)); err != nil {
		t.Fatalf("voluntary leaver could not rejoin: %v", err)
	}
}

func TestHostDepartureClosesRoomAfterTwoMinutes(t *testing.T) {
	r := newRoom(t)
	_, _ = r.Join("p2", t0)
	r.Leave("host-1", t0)

	r.Tick(t0.Add(90 * time.Second))
	if r.Status == room.StatusClosed {
		t.Fatal("room closed before the 2-minute grace expired")
	}
	r.Tick(t0.Add(2*time.Minute + time.Second))
	if r.Status != room.StatusClosed {
		t.Fatalf("status = %q, want Closed after grace expiry", r.Status)
	}
}

func TestHostReturnWithinGraceSavesRoom(t *testing.T) {
	r := newRoom(t)
	r.Leave("host-1", t0)
	if _, err := r.Join("host-1", t0.Add(30*time.Second)); err != nil {
		t.Fatalf("host could not reclaim room: %v", err)
	}
	r.Tick(t0.Add(3 * time.Minute))
	if r.Status == room.StatusClosed {
		t.Fatal("room closed despite the host returning within grace")
	}
}

func TestRoomFullRejectsEleventhPlayer(t *testing.T) {
	r := newRoom(t)
	for i := 2; i <= 10; i++ {
		if _, err := r.Join(string(rune('a'+i)), t0); err != nil {
			t.Fatalf("player %d could not join: %v", i, err)
		}
	}
	if _, err := r.Join("overflow", t0); !errors.Is(err, room.ErrRoomFull) {
		t.Fatalf("err = %v, want ErrRoomFull", err)
	}
}

func TestClosedRoomRejectsEverything(t *testing.T) {
	r := newRoom(t)
	_ = r.SetStatus("host-1", room.StatusClosed, t0)
	if _, err := r.Join("p2", t0); !errors.Is(err, room.ErrRoomClosed) {
		t.Fatalf("err = %v, want ErrRoomClosed", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd coordinator && go test ./internal/room/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the implementation**

`coordinator/internal/room/state.go`:

```go
// Package room implements the room lifecycle exactly as specified in
// docs/superpowers/specs/2026-08-18-lobby-platform-design.md section 3.
package room

import (
	"errors"
	"time"

	"finallobby/coordinator/internal/ipam"
)

// Status is the room's admission state.
type Status string

const (
	// StatusOpen accepts new players.
	StatusOpen Status = "open"
	// StatusLocked is set when a match begins. No new player may join.
	StatusLocked Status = "locked_in_game"
	// StatusOpenToNew is the host explicitly reopening a running match so
	// an abandoned slot can be refilled.
	StatusOpenToNew Status = "open_to_new_players"
	// StatusClosed is terminal.
	StatusClosed Status = "closed"
)

const (
	// HostGracePeriod is how long a room survives without its host.
	HostGracePeriod = 2 * time.Minute
	// KickBlockPeriod is how long a kicked player is barred from the room.
	KickBlockPeriod = 5 * time.Minute
)

var (
	ErrRoomLocked    = errors.New("room: locked, no new players")
	ErrRoomFull      = errors.New("room: no free player slot")
	ErrKickBlocked   = errors.New("room: player was kicked recently")
	ErrNotHost       = errors.New("room: only the host may do that")
	ErrRoomClosed    = errors.New("room: closed")
	ErrAlreadyJoined = errors.New("room: already in this room")
)

// Room is one lobby. Not safe for concurrent use; the store serialises access.
type Room struct {
	ID     string
	Index  int
	HostID string
	Status Status

	Slots      [ipam.PlayerSlots]string
	Spectators [ipam.SpectatorSlots]string

	KickedUntil    map[string]time.Time
	HostGraceUntil time.Time
}

// NewRoom creates a room with the host seated in slot 0, which maps to the
// deterministic host virtual IP.
func NewRoom(id string, index int, hostID string, now time.Time) *Room {
	r := &Room{
		ID:          id,
		Index:       index,
		HostID:      hostID,
		Status:      StatusOpen,
		KickedUntil: make(map[string]time.Time),
	}
	r.Slots[0] = hostID
	return r
}

// Join seats a player in the lowest free slot.
func (r *Room) Join(playerID string, now time.Time) (int, error) {
	if r.Status == StatusClosed {
		return 0, ErrRoomClosed
	}
	if until, ok := r.KickedUntil[playerID]; ok && now.Before(until) {
		return 0, ErrKickBlocked
	}
	for _, occupant := range r.Slots {
		if occupant == playerID {
			return 0, ErrAlreadyJoined
		}
	}
	// The host reclaiming their room during grace is always allowed.
	isHostReturning := playerID == r.HostID && !r.HostGraceUntil.IsZero()
	if r.Status == StatusLocked && !isHostReturning {
		return 0, ErrRoomLocked
	}

	for i := range r.Slots {
		if r.Slots[i] == "" {
			r.Slots[i] = playerID
			if isHostReturning {
				r.HostGraceUntil = time.Time{}
			}
			return i, nil
		}
	}
	return 0, ErrRoomFull
}

// Leave vacates a player's slot. If the host leaves, the grace timer starts.
func (r *Room) Leave(playerID string, now time.Time) {
	for i := range r.Slots {
		if r.Slots[i] == playerID {
			r.Slots[i] = ""
		}
	}
	for i := range r.Spectators {
		if r.Spectators[i] == playerID {
			r.Spectators[i] = ""
		}
	}
	if playerID == r.HostID {
		r.HostGraceUntil = now.Add(HostGracePeriod)
	}
}

// Kick removes a player and bars them for KickBlockPeriod.
func (r *Room) Kick(actorID, targetID string, now time.Time) error {
	if actorID != r.HostID {
		return ErrNotHost
	}
	r.Leave(targetID, now)
	r.KickedUntil[targetID] = now.Add(KickBlockPeriod)
	return nil
}

// SetStatus changes admission state. Host only.
func (r *Room) SetStatus(actorID string, s Status, now time.Time) error {
	if actorID != r.HostID {
		return ErrNotHost
	}
	if r.Status == StatusClosed {
		return ErrRoomClosed
	}
	r.Status = s
	return nil
}

// Tick advances time-based transitions. Call it on a scheduler.
func (r *Room) Tick(now time.Time) {
	if r.Status == StatusClosed {
		return
	}
	if !r.HostGraceUntil.IsZero() && now.After(r.HostGraceUntil) {
		r.Status = StatusClosed
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd coordinator && go test ./internal/room/ -v`
Expected: PASS, 11 tests.

- [ ] **Step 5: Commit**

```bash
git add coordinator/internal/room
git commit -m "feat(room): implement room lifecycle, kick blocks and host grace period"
```

---

### Task 11: Windows Wintun adapter

First task that cannot be fully unit-tested. It ships with an integration
test that runs only on Windows with Administrator rights.

**Files:**
- Create: `netservice/internal/adapter/wintun.go`
- Test: `netservice/internal/adapter/wintun_windows_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Adapter struct { ... }`
  - `func Open(name string, mtu int) (*Adapter, error)`
  - `func (a *Adapter) Configure(ip netip.Addr, subnet netip.Prefix) error` — assigns the address and installs a route for **only** that `/28`.
  - `func (a *Adapter) Read(buf []byte) (int, error)`
  - `func (a *Adapter) Write(pkt []byte) error`
  - `func (a *Adapter) Close() error`
  - `const MTU = 1300`

The route restriction is the client-side half of room isolation: the machine
has no route to any other room's subnet.

- [ ] **Step 1: Write the failing integration test**

`netservice/internal/adapter/wintun_windows_test.go`:

```go
//go:build windows

package adapter_test

import (
	"net/netip"
	"os"
	"testing"

	"finallobby/netservice/internal/adapter"
)

// These tests need Administrator rights and the Wintun driver.
// Run with: go test -tags=integration ./internal/adapter/
func requireAdmin(t *testing.T) {
	t.Helper()
	if os.Getenv("LOBBY_INTEGRATION") == "" {
		t.Skip("set LOBBY_INTEGRATION=1 and run as Administrator")
	}
}

func TestAdapterLifecycle(t *testing.T) {
	requireAdmin(t)

	a, err := adapter.Open("FinalLobbyTest", adapter.MTU)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer a.Close()

	ip := netip.MustParseAddr("10.87.0.2")
	subnet := netip.MustParsePrefix("10.87.0.0/28")
	if err := a.Configure(ip, subnet); err != nil {
		t.Fatalf("Configure: %v", err)
	}
}

func TestConfigureRejectsAddressOutsideSubnet(t *testing.T) {
	requireAdmin(t)

	a, err := adapter.Open("FinalLobbyTest2", adapter.MTU)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	// .20 is not inside 10.87.0.0/28 — a bug that would break isolation.
	err = a.Configure(netip.MustParseAddr("10.87.0.20"), netip.MustParsePrefix("10.87.0.0/28"))
	if err == nil {
		t.Fatal("expected Configure to reject an address outside its subnet")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd netservice && go test ./internal/adapter/ -v`
Expected: FAIL — package does not exist. (Tests skip without `LOBBY_INTEGRATION`.)

- [ ] **Step 3: Write the implementation**

```bash
cd netservice && go get golang.zx2c4.com/wireguard/tun
```

`netservice/internal/adapter/wintun.go`:

```go
//go:build windows

// Package adapter owns the Wintun virtual network adapter.
package adapter

import (
	"fmt"
	"net/netip"
	"os/exec"

	"golang.zx2c4.com/wireguard/tun"
)

// MTU is deliberately below the usual 1500 so that an encrypted datagram
// plus headers still fits inside a normal Ethernet frame without
// fragmenting.
const MTU = 1300

// Adapter wraps a Wintun device.
type Adapter struct {
	dev  tun.Device
	name string
}

// Open creates (or reuses) the named Wintun adapter.
func Open(name string, mtu int) (*Adapter, error) {
	dev, err := tun.CreateTUN(name, mtu)
	if err != nil {
		return nil, fmt.Errorf("adapter: create %q: %w", name, err)
	}
	actual, err := dev.Name()
	if err != nil {
		_ = dev.Close()
		return nil, fmt.Errorf("adapter: name: %w", err)
	}
	return &Adapter{dev: dev, name: actual}, nil
}

// Configure assigns ip to the adapter and installs a route for subnet only.
//
// Restricting the route to the room's own /28 is the client-side half of
// room isolation: this machine has no route to any other room's addresses.
func (a *Adapter) Configure(ip netip.Addr, subnet netip.Prefix) error {
	if !subnet.Contains(ip) {
		return fmt.Errorf("adapter: %s is not inside %s", ip, subnet)
	}
	// netsh is used rather than a routing library because it is present on
	// every supported Windows build and needs no extra privileges beyond
	// the ones the service already holds.
	cmds := [][]string{
		{"netsh", "interface", "ip", "set", "address",
			fmt.Sprintf("name=%s", a.name), "static",
			ip.String(), maskFor(subnet)},
		{"netsh", "interface", "ipv4", "set", "subinterface",
			a.name, fmt.Sprintf("mtu=%d", MTU), "store=active"},
	}
	for _, c := range cmds {
		if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("adapter: %v: %w (%s)", c, err, out)
		}
	}
	return nil
}

func maskFor(p netip.Prefix) string {
	bits := p.Bits()
	var mask [4]byte
	for i := 0; i < bits; i++ {
		mask[i/8] |= 1 << uint(7-i%8)
	}
	return netip.AddrFrom4(mask).String()
}

// Read returns one outbound IP packet from the operating system.
func (a *Adapter) Read(buf []byte) (int, error) {
	sizes := make([]int, 1)
	bufs := [][]byte{buf}
	n, err := a.dev.Read(bufs, sizes, 0)
	if err != nil || n == 0 {
		return 0, err
	}
	return sizes[0], nil
}

// Write injects one inbound IP packet into the operating system.
func (a *Adapter) Write(pkt []byte) error {
	_, err := a.dev.Write([][]byte{pkt}, 0)
	return err
}

func (a *Adapter) Close() error { return a.dev.Close() }

// Name returns the adapter's interface name.
func (a *Adapter) Name() string { return a.name }
```

**Note for the implementer:** `tun.CreateTUN` requires `wintun.dll` beside
the executable. Embed it with `//go:embed wintun.dll` and extract on startup,
as the ancestor project does. Confirm the Wintun redistribution licence
before shipping (spec section 11, open question 3).

- [ ] **Step 4: Run the test on a Windows machine as Administrator**

Run: `set LOBBY_INTEGRATION=1 && cd netservice && go test ./internal/adapter/ -v`
Expected: PASS, 2 tests. An adapter named `FinalLobbyTest` appears in
`ipconfig` while the test runs.

- [ ] **Step 5: Commit**

```bash
git add netservice/internal/adapter netservice/go.mod netservice/go.sum
git commit -m "feat(adapter): manage Wintun adapter with room-scoped routing"
```

---

### Task 12: Tunnel client with sticky reconnect

**Files:**
- Create: `netservice/internal/tunnel/client.go`
- Test: `netservice/internal/tunnel/client_test.go`

**Interfaces:**
- Consumes: `adapter.Adapter`, relay handshake from Task 7, `wire`, `crypto`.
- Produces:
  - `type Client struct { ... }`
  - `type Config struct { RelayAddr string; RelayPub []byte; Ticket []byte; Adapter PacketDevice; Backoff BackoffPolicy }`
  - `type PacketDevice interface { Read([]byte) (int, error); Write([]byte) error }` — lets tests substitute a fake adapter.
  - `func New(cfg Config) *Client`
  - `func (c *Client) Run(ctx context.Context) error` — reconnects until ctx is done.
  - `func (c *Client) State() State` with `StateConnecting`, `StateConnected`, `StateRevoked`.

The adapter is **never torn down across reconnects**. That is what lets Dota's
own reconnect resume as though nothing happened.

- [ ] **Step 1: Write the failing test**

`netservice/internal/tunnel/client_test.go`:

```go
package tunnel_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"finallobby/netservice/internal/tunnel"
)

// fakeDevice stands in for the Wintun adapter.
type fakeDevice struct {
	outbound chan []byte
	inbound  atomic.Int64
	closed   atomic.Bool
}

func newFakeDevice() *fakeDevice {
	return &fakeDevice{outbound: make(chan []byte, 16)}
}

func (f *fakeDevice) Read(buf []byte) (int, error) {
	pkt, ok := <-f.outbound
	if !ok {
		return 0, context.Canceled
	}
	return copy(buf, pkt), nil
}

func (f *fakeDevice) Write(pkt []byte) error {
	f.inbound.Add(1)
	return nil
}

func TestAdapterSurvivesReconnect(t *testing.T) {
	dev := newFakeDevice()
	// A relay address that refuses connections forces the retry path.
	c := tunnel.New(tunnel.Config{
		RelayAddr: "127.0.0.1:1", // nothing listens here
		RelayPub:  make([]byte, 32),
		Ticket:    []byte("t"),
		Adapter:   dev,
		Backoff:   tunnel.BackoffPolicy{Initial: 10 * time.Millisecond, Max: 20 * time.Millisecond},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = c.Run(ctx)

	if dev.closed.Load() {
		t.Fatal("adapter was closed during reconnect; Dota's own reconnect would break")
	}
}

func TestStateReportsConnectingWhileRelayUnreachable(t *testing.T) {
	dev := newFakeDevice()
	c := tunnel.New(tunnel.Config{
		RelayAddr: "127.0.0.1:1",
		RelayPub:  make([]byte, 32),
		Ticket:    []byte("t"),
		Adapter:   dev,
		Backoff:   tunnel.BackoffPolicy{Initial: 10 * time.Millisecond, Max: 20 * time.Millisecond},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	go func() { _ = c.Run(ctx) }()
	time.Sleep(60 * time.Millisecond)

	if got := c.State(); got != tunnel.StateConnecting {
		t.Fatalf("State() = %v, want StateConnecting", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd netservice && go test ./internal/tunnel/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the implementation**

Implement `netservice/internal/tunnel/client.go` with this structure:

```go
// Package tunnel connects the local Wintun adapter to the relay.
package tunnel

import (
	"context"
	"net"
	"sync/atomic"
	"time"
)

// State is the tunnel's connection state.
type State int32

const (
	StateConnecting State = iota
	StateConnected
	StateRevoked
)

// BackoffPolicy bounds reconnect attempts.
type BackoffPolicy struct {
	Initial time.Duration
	Max     time.Duration
}

func (b BackoffPolicy) next(current time.Duration) time.Duration {
	if current == 0 {
		return b.Initial
	}
	doubled := current * 2
	if doubled > b.Max {
		return b.Max
	}
	return doubled
}

// PacketDevice is the subset of the adapter the tunnel needs. Tests
// substitute a fake.
type PacketDevice interface {
	Read(buf []byte) (int, error)
	Write(pkt []byte) error
}

type Config struct {
	RelayAddr string
	RelayPub  []byte
	Ticket    []byte
	Adapter   PacketDevice
	Backoff   BackoffPolicy
}

type Client struct {
	cfg   Config
	state atomic.Int32
}

func New(cfg Config) *Client {
	if cfg.Backoff.Initial == 0 {
		cfg.Backoff.Initial = 500 * time.Millisecond
	}
	if cfg.Backoff.Max == 0 {
		cfg.Backoff.Max = 15 * time.Second
	}
	return &Client{cfg: cfg}
}

func (c *Client) State() State { return State(c.state.Load()) }

func (c *Client) setState(s State) { c.state.Store(int32(s)) }

// Run maintains the tunnel until ctx is cancelled. The adapter is never
// closed here: reconnecting must be invisible to Dota, which keeps its own
// socket bound to the unchanged virtual IP.
func (c *Client) Run(ctx context.Context) error {
	var backoff time.Duration
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.setState(StateConnecting)
		err := c.connectOnce(ctx)
		if err == nil {
			return nil
		}
		backoff = c.cfg.Backoff.next(backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
}

// connectOnce performs one handshake and pumps packets until the session
// fails. It returns nil only on clean shutdown.
func (c *Client) connectOnce(ctx context.Context) error {
	conn, err := net.Dial("udp", c.cfg.RelayAddr)
	if err != nil {
		return err
	}
	defer conn.Close()
	// 1. Noise NK handshake using c.cfg.RelayPub and c.cfg.Ticket.
	// 2. On success, record the assigned session ID and virtual IP.
	// 3. setState(StateConnected).
	// 4. Start two pumps:
	//      adapter -> seal -> conn
	//      conn -> open -> adapter
	//    Each exits on error; the first error tears down this attempt.
	// 5. Return the error so Run retries with backoff.
	return errNotImplemented
}
```

Complete steps 1–5 in `connectOnce` using `crypto.ClientHandshake` from
Task 7 and `crypto.Session.Seal`/`Open` from Task 6. The outbound pump reads
from `c.cfg.Adapter`, the inbound pump writes to it. Use a single
`errgroup.Group` so either pump failing cancels the other.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd netservice && go test ./internal/tunnel/ -race -v`
Expected: PASS, 2 tests.

- [ ] **Step 5: Commit**

```bash
git add netservice/internal/tunnel
git commit -m "feat(tunnel): add relay client with adapter-preserving reconnect"
```

---

### Task 13: Fail-closed lease watchdog

**Files:**
- Create: `netservice/internal/watchdog/lease.go`
- Test: `netservice/internal/watchdog/lease_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Verdict int` with `VerdictValid`, `VerdictRevoked`, `VerdictUnreachable`.
  - `type Checker func(ctx context.Context) (Verdict, error)`
  - `type Watchdog struct { ... }`
  - `func New(check Checker, interval, localExpiry time.Duration, onTeardown func(reason string)) *Watchdog`
  - `func (w *Watchdog) Run(ctx context.Context)`

Rules that must hold: a revoked verdict tears down immediately; an
unreachable coordinator does **not** extend the lease — teardown still fires
once `localExpiry` elapses since the last valid check.

- [ ] **Step 1: Write the failing test**

`netservice/internal/watchdog/lease_test.go`:

```go
package watchdog_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"finallobby/netservice/internal/watchdog"
)

func TestRevokedVerdictTearsDownImmediately(t *testing.T) {
	var torn atomic.Bool
	check := func(context.Context) (watchdog.Verdict, error) {
		return watchdog.VerdictRevoked, nil
	}
	w := watchdog.New(check, 10*time.Millisecond, time.Hour, func(string) { torn.Store(true) })

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	if !torn.Load() {
		t.Fatal("revoked lease did not tear down the tunnel")
	}
}

func TestOutageDoesNotExtendTheLease(t *testing.T) {
	var torn atomic.Bool
	check := func(context.Context) (watchdog.Verdict, error) {
		return watchdog.VerdictUnreachable, errors.New("coordinator down")
	}
	// Local expiry of 80ms: an unreachable coordinator must not keep the
	// tunnel alive past it.
	w := watchdog.New(check, 10*time.Millisecond, 80*time.Millisecond, func(string) { torn.Store(true) })

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	if !torn.Load() {
		t.Fatal("tunnel survived past local expiry during an outage — must fail closed")
	}
}

func TestValidChecksKeepTunnelAlive(t *testing.T) {
	var torn atomic.Bool
	check := func(context.Context) (watchdog.Verdict, error) {
		return watchdog.VerdictValid, nil
	}
	w := watchdog.New(check, 10*time.Millisecond, 100*time.Millisecond, func(string) { torn.Store(true) })

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	if torn.Load() {
		t.Fatal("healthy lease was torn down")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd netservice && go test ./internal/watchdog/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the implementation**

`netservice/internal/watchdog/lease.go`:

```go
// Package watchdog enforces network authorisation locally.
//
// It runs inside the Windows service, not the desktop app, so a closed or
// misbehaving UI cannot keep a revoked player connected.
package watchdog

import (
	"context"
	"time"
)

// Verdict is the coordinator's answer about a lease.
type Verdict int

const (
	VerdictValid Verdict = iota
	VerdictRevoked
	VerdictUnreachable
)

// Checker asks the coordinator whether the lease is still good.
type Checker func(ctx context.Context) (Verdict, error)

// Watchdog tears the tunnel down when authorisation ends.
type Watchdog struct {
	check       Checker
	interval    time.Duration
	localExpiry time.Duration
	onTeardown  func(reason string)
}

func New(check Checker, interval, localExpiry time.Duration, onTeardown func(reason string)) *Watchdog {
	return &Watchdog{check: check, interval: interval, localExpiry: localExpiry, onTeardown: onTeardown}
}

// Run polls until ctx is done or the lease ends. It fails closed: a
// coordinator outage never extends authorisation, it merely delays the
// explicit answer until local expiry runs out.
func (w *Watchdog) Run(ctx context.Context) {
	lastValid := time.Now()
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		verdict, _ := w.check(ctx)
		switch verdict {
		case VerdictValid:
			lastValid = time.Now()
		case VerdictRevoked:
			w.onTeardown("authorisation revoked")
			return
		case VerdictUnreachable:
			// Deliberately no lastValid update.
		}

		if time.Since(lastValid) > w.localExpiry {
			w.onTeardown("lease expired locally")
			return
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd netservice && go test ./internal/watchdog/ -race -v`
Expected: PASS, 3 tests.

- [ ] **Step 5: Commit**

```bash
git add netservice/internal/watchdog
git commit -m "feat(watchdog): add fail-closed lease enforcement in the service"
```

---

### Task 14: Dota 2 launch with argument allowlist

**Files:**
- Create: `netservice/internal/dota/launch.go`
- Test: `netservice/internal/dota/launch_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `func ValidateExePath(path string) error`
  - `func BuildHostArgs(nick string, gameMode int, team string) ([]string, error)`
  - `func BuildClientArgs(nick string, hostIP netip.Addr, team string) ([]string, error)`
  - `func ValidateArgs(args []string) error`
  - `func ServerReady(consoleLogPath string, since int64) (bool, error)`
  - `var ErrBadArg, ErrBadPath error`

An unvalidated launch path is a code-execution vector; the allowlist is
security, not tidiness.

- [ ] **Step 1: Write the failing test**

`netservice/internal/dota/launch_test.go`:

```go
package dota_test

import (
	"errors"
	"net/netip"
	"strings"
	"testing"

	"finallobby/netservice/internal/dota"
)

func TestBuildHostArgs(t *testing.T) {
	args, err := dota.BuildHostArgs("Player1", 1, "good")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"+name Player1", "+sv_lan 1", "+map dota", "gamemode 1", "+jointeam good"} {
		if !strings.Contains(joined, want) {
			t.Errorf("host args missing %q; got %q", want, joined)
		}
	}
	if err := dota.ValidateArgs(args); err != nil {
		t.Errorf("generated host args failed our own validation: %v", err)
	}
}

func TestBuildClientArgs(t *testing.T) {
	args, err := dota.BuildClientArgs("Player2", netip.MustParseAddr("10.87.0.2"), "bad")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "+connect 10.87.0.2:27015") {
		t.Errorf("client args missing connect target; got %q", joined)
	}
	if err := dota.ValidateArgs(args); err != nil {
		t.Errorf("generated client args failed validation: %v", err)
	}
}

func TestRejectsConnectOutsideOurAddressSpace(t *testing.T) {
	_, err := dota.BuildClientArgs("P", netip.MustParseAddr("192.168.1.10"), "good")
	if !errors.Is(err, dota.ErrBadArg) {
		t.Fatalf("err = %v, want ErrBadArg for a non-10.87 address", err)
	}
}

func TestRejectsUnknownArgumentKeys(t *testing.T) {
	err := dota.ValidateArgs([]string{"+exec", "evil.cfg"})
	if !errors.Is(err, dota.ErrBadArg) {
		t.Fatalf("err = %v, want ErrBadArg for +exec", err)
	}
}

func TestRejectsInjectedNickname(t *testing.T) {
	for _, bad := range []string{`a" +exec evil`, "a\\b", strings.Repeat("x", 33), ""} {
		if _, err := dota.BuildHostArgs(bad, 1, "good"); !errors.Is(err, dota.ErrBadArg) {
			t.Errorf("nickname %q accepted; want rejection", bad)
		}
	}
}

func TestRejectsBadTeam(t *testing.T) {
	if _, err := dota.BuildHostArgs("P", 1, "neutral"); !errors.Is(err, dota.ErrBadArg) {
		t.Fatal("team 'neutral' accepted; want rejection")
	}
}

func TestRejectsUnknownGameMode(t *testing.T) {
	if _, err := dota.BuildHostArgs("P", 999, "good"); !errors.Is(err, dota.ErrBadArg) {
		t.Fatal("game mode 999 accepted; want rejection")
	}
}

func TestValidateExePathRequiresDota2Exe(t *testing.T) {
	if err := dota.ValidateExePath(`C:\Games\notdota.exe`); !errors.Is(err, dota.ErrBadPath) {
		t.Fatalf("err = %v, want ErrBadPath", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd netservice && go test ./internal/dota/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the implementation**

`netservice/internal/dota/launch.go`:

```go
// Package dota builds and validates Dota 2 launch commands.
//
// Every argument is allowlisted. The service runs with elevated rights, so
// an unvalidated launch path would be a privilege-escalation vector.
package dota

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

var (
	ErrBadArg  = errors.New("dota: rejected argument")
	ErrBadPath = errors.New("dota: rejected executable path")
)

// gameModes are the Dota 2 game mode IDs we expose.
var gameModes = map[int]string{
	1: "All Pick", 2: "Captain's Mode", 3: "Random Draft",
	4: "Single Draft", 5: "All Random", 18: "Ability Draft",
	22: "Ranked All Pick", 23: "Turbo",
}

var validTeams = map[string]bool{"good": true, "bad": true, "spec": true}

// ourAddressSpace is the only network a client may be told to connect to.
var ourAddressSpace = netip.MustParsePrefix("10.87.0.0/16")

// ValidateExePath checks that path really points at a Dota 2 executable.
func ValidateExePath(path string) error {
	lower := strings.ToLower(filepath.ToSlash(path))
	if !strings.HasSuffix(lower, "/dota2.exe") {
		return fmt.Errorf("%w: must end with dota2.exe, got %q", ErrBadPath, path)
	}
	if !strings.Contains(lower, "dota 2 beta") {
		return fmt.Errorf("%w: not inside a 'dota 2 beta' tree", ErrBadPath)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: symlinks are not accepted", ErrBadPath)
	}
	return nil
}

func validateNick(nick string) error {
	if len(nick) == 0 || len(nick) > 32 {
		return fmt.Errorf("%w: nickname length %d", ErrBadArg, len(nick))
	}
	for _, r := range nick {
		if !unicode.IsPrint(r) || strings.ContainsRune(`"'\/`, r) {
			return fmt.Errorf("%w: nickname contains %q", ErrBadArg, r)
		}
	}
	return nil
}

// BuildHostArgs produces the launch arguments for the player hosting.
func BuildHostArgs(nick string, gameMode int, team string) ([]string, error) {
	if err := validateNick(nick); err != nil {
		return nil, err
	}
	if _, ok := gameModes[gameMode]; !ok {
		return nil, fmt.Errorf("%w: unknown game mode %d", ErrBadArg, gameMode)
	}
	if !validTeams[team] {
		return nil, fmt.Errorf("%w: unknown team %q", ErrBadArg, team)
	}
	return []string{
		"+name", nick,
		"+sv_lan", "1",
		"+map", "dota",
		"gamemode", strconv.Itoa(gameMode),
		"+jointeam", team,
	}, nil
}

// BuildClientArgs produces the launch arguments for a joining player.
func BuildClientArgs(nick string, hostIP netip.Addr, team string) ([]string, error) {
	if err := validateNick(nick); err != nil {
		return nil, err
	}
	if !ourAddressSpace.Contains(hostIP) {
		return nil, fmt.Errorf("%w: %s is outside %s", ErrBadArg, hostIP, ourAddressSpace)
	}
	if !validTeams[team] {
		return nil, fmt.Errorf("%w: unknown team %q", ErrBadArg, team)
	}
	return []string{
		"+name", nick,
		"+connect", hostIP.String() + ":27015",
		"+jointeam", team,
	}, nil
}

// ValidateArgs re-checks a full argument list before it reaches the process.
func ValidateArgs(args []string) error {
	allowed := map[string]bool{
		"+name": true, "+connect": true, "+sv_lan": true, "+map": true,
		"+jointeam": true, "gamemode": true, "-console": true,
		"-enableconsole": true, "-condebug": true,
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "+") && !strings.HasPrefix(a, "-") && a != "gamemode" {
			continue // a value, already checked with its key
		}
		if !allowed[a] {
			return fmt.Errorf("%w: unknown key %q", ErrBadArg, a)
		}
	}
	return nil
}

// ServerReady reports whether the host's Dota 2 has finished starting its
// LAN server, by looking for the listen-server marker in console.log.
func ServerReady(consoleLogPath string, since int64) (bool, error) {
	data, err := os.ReadFile(consoleLogPath)
	if err != nil {
		return false, err
	}
	if int64(len(data)) <= since {
		return false, nil
	}
	tail := string(data[since:])
	return strings.Contains(tail, "Server started") ||
		strings.Contains(tail, "Host_NewGame"), nil
}
```

**Note for the implementer:** the exact `console.log` marker strings in
`ServerReady` are unverified against the current Dota 2 build. Confirm them
during Task 16 on a real host and update if they differ — this is a known
gap, not an assumption to trust.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd netservice && go test ./internal/dota/ -v`
Expected: PASS, 8 tests. `TestValidateExePathRequiresDota2Exe` passes because
the suffix check fails before the filesystem is touched.

- [ ] **Step 5: Commit**

```bash
git add netservice/internal/dota
git commit -m "feat(dota): add allowlisted launch argument construction and validation"
```

---

### Task 15: Load test harness

Proves the ancestor's failure mode is gone and produces the bandwidth number
the CEO needs.

**Files:**
- Create: `loadtest/main.go`
- Create: `loadtest/README.md`

**Interfaces:**
- Consumes: `crypto`, `wire` from the relay module (add a `replace` directive in `loadtest/go.mod`).
- Produces: a CLI reporting packets forwarded, packets dropped, p50/p95/p99 forwarding latency, and relay CPU.

- [ ] **Step 1: Write the harness**

`loadtest/main.go` synthesises N peers across N/10 rooms, each sending at a
configurable packet rate and size, and measures round-trip latency through
the relay.

```go
// Command loadtest drives a relay with synthetic peers.
//
// It answers two questions the two-PC physical test cannot:
//   1. Does the relay hold at 1500 concurrent players?
//   2. What throughput does that require?
//
// Usage:
//
//	loadtest -relay 127.0.0.1:443 -relay-pub <hex> -peers 1500 \
//	         -pps 60 -packet-size 200 -duration 120s
package main

import (
	"flag"
	"fmt"
	"time"
)

type stats struct {
	sent, received, dropped uint64
	latencies               []time.Duration
}

func main() {
	relay := flag.String("relay", "127.0.0.1:443", "relay UDP address")
	relayPub := flag.String("relay-pub", "", "hex relay static public key")
	peers := flag.Int("peers", 1500, "number of synthetic peers")
	pps := flag.Int("pps", 60, "packets per second per peer")
	size := flag.Int("packet-size", 200, "inner packet size in bytes")
	duration := flag.Duration("duration", 60*time.Second, "test duration")
	flag.Parse()

	fmt.Printf("starting %d peers across %d rooms at %d pps\n", *peers, (*peers+9)/10, *pps)
	// For each peer: handshake with a locally minted ticket, then send
	// unicast packets to another member of the same room at the given rate,
	// timestamping the payload so the receiver can compute latency.
	//
	// Report at the end:
	//   packets sent / received / lost
	//   p50 p95 p99 forwarding latency
	//   aggregate Mbps in and out
	_ = relay
	_ = relayPub
	_ = size
	_ = duration
}
```

Complete the peer goroutines using `crypto.ClientHandshake`. Each peer needs
one send goroutine and one receive goroutine; keep total goroutines at 2×peers
so the harness itself is not the bottleneck.

- [ ] **Step 2: Run against a local relay at low scale**

```bash
make build-relay
./bin/relay -listen 127.0.0.1:9443 -static-key <hex> &
cd loadtest && go run . -relay 127.0.0.1:9443 -relay-pub <hex> -peers 50 -duration 10s
```

Expected: near-zero loss, sub-millisecond p99 on loopback.

- [ ] **Step 3: Run the 1500-peer soak on the server**

Expected: the relay stays responsive and memory stays flat. Record CPU and
aggregate throughput. **This is the number that answers the provisioning
question in spec section 9.**

- [ ] **Step 4: Commit**

```bash
git add loadtest
git commit -m "test(loadtest): add synthetic peer harness for relay capacity"
```

---

### Task 16: Physical two-PC acceptance test

The gate for this whole sub-project. No further sub-project starts until this
passes.

**Files:**
- Create: `docs/testing/two-pc-acceptance.md`
- Create: `lobbycli/main.go`

**Interfaces:**
- Consumes: everything above.
- Produces: a recorded test result document.

- [ ] **Step 1: Write the throwaway CLI**

`lobbycli/main.go` provides: `register`, `login`, `create-room`,
`join-room <id>`, `status`, `launch`, `leave`. It talks to the coordinator
over HTTP and to the local net-service over the named pipe. Deliberately
minimal — the real UI is sub-project 3.

- [ ] **Step 2: Write the acceptance checklist**

`docs/testing/two-pc-acceptance.md` with these cases, each recording
observed evidence rather than a tick:

1. Both PCs install the service; **neither shows a UAC prompt when joining a room**.
2. PC A creates a room; PC B joins. Both receive addresses in the same `/28`.
3. PC B can ping PC A's virtual IP; latency recorded.
4. PC A launches Dota as host; PC B connects with `+connect`. **A real match starts.**
5. Measure per-player bandwidth in both directions during the match. Record the figure.
6. **Open question 1:** PC B leaves mid-match. Host reopens the room to new players. Can a fresh account join the running match and take the abandoned slot? Record yes or no — this is the unverified behaviour spec section 11 flags.
7. While the room is `Locked`, a join attempt is refused.
8. PC B is kicked; a rejoin within 5 minutes is refused, and after 5 minutes succeeds.
9. Unplug PC B's network for 20 seconds. The tunnel restores with the same virtual IP and Dota reconnects without a manual rejoin.
10. Host closes the app; the room closes after 2 minutes.
11. After PC B leaves, confirm it can no longer reach PC A's virtual IP at all.
12. Confirm the relay's `console.log` readiness markers from Task 14 match the real build; correct them if not.

- [ ] **Step 3: Run it and record results**

Fill the document with what actually happened, including failures. A failure
here is a finding, not a blocker to record honestly.

- [ ] **Step 4: Commit**

```bash
git add docs/testing/two-pc-acceptance.md lobbycli
git commit -m "test: add two-PC acceptance checklist and throwaway lobby CLI"
```

---

## Deployment note

The target server (`87.107.110.199`) runs an unrelated live SNI proxy on
**TCP** 443 and CoreDNS on 53. The relay binds **UDP** 443, which is free —
verified 2026-08-18. Never bind TCP 443 there. Per spec section 9.2, this
server is for development and measurement only; provision a dedicated box
before public launch.

---

## Self-Review

**Spec coverage:**

| Spec section | Covered by |
|---|---|
| 5.1 addressing | Task 3 |
| 5.2 Dota connection | Task 14, verified Task 16 |
| 5.3 broadcast elimination | Task 4, integration-verified Task 9 |
| 5.4 transport | Tasks 2, 6, 7 |
| 5.5 relay data plane | Tasks 4, 5, 8, 9 |
| 5.6 reconnect | Task 12, verified Task 16 case 9 |
| 5.7 revocation | Task 13, verified Task 16 case 11 |
| 3.2 room lifecycle | Task 10 |
| 9 capacity | Task 15 |
| 10.1 bandwidth measurement | Task 15, Task 16 case 5 |
| 11 open question 1 | Task 16 case 6 |

**Gaps deliberately deferred to sub-project 2:** accounts and authentication,
ticket signing and issuance, PostgreSQL persistence for rooms, the
coordinator's player-facing HTTP API, rate limiting, MMR and admission rules,
and the terms-and-conditions acceptance record. Task 16's CLI uses a stub
coordinator; Task 9's `ValidateTicket` hook is where the real implementation
lands.

**Known unverified items carried as explicit notes, not assumptions:**
`noise.CipherState.Key()` availability (Task 7), the `publicFrom` key
derivation (Task 7), the session-ID handshake extension (Task 9), and the
Dota `console.log` readiness markers (Task 14).
