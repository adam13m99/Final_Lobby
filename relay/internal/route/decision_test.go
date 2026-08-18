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
	// Sender owns .2 but claims to be .3 - impersonation attempt.
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
