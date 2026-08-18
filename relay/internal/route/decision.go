// Package route decides what the relay does with each inbound packet.
//
// The decision is a pure function so that anti-spoofing and room isolation -
// the two rules that must never regress - are directly testable.
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
// (224.0.0.0/4), the all-ones broadcast, or a /28 subnet broadcast - the
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
