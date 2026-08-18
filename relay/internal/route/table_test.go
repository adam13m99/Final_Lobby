package route_test

import (
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
