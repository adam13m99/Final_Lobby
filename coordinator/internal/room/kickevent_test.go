package room

import (
	"testing"
	"time"
)

// D52: a kick is recorded as something that happened. The block itself stays
// in memory with the room, because that is where the room is.
func TestKicksAreReportedForRecording(t *testing.T) {
	s := NewStore()
	var got []KickEvent
	s.OnKick(func(e KickEvent) { got = append(got, e) })

	r, _, _ := s.Create("host", "test", when())
	if _, err := s.Join(r.ID, Anyone("pest"), when()); err != nil {
		t.Fatal(err)
	}
	if err := s.Kick(r.ID, "host", "pest", when()); err != nil {
		t.Fatal(err)
	}

	// The escalation is visible in the second kick, which is the whole reason
	// the number is in the record.
	later := when().Add(2 * time.Minute)
	if _, err := s.Join(r.ID, Anyone("pest"), later); err != nil {
		t.Fatal(err)
	}
	if err := s.Kick(r.ID, "host", "pest", later); err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 {
		t.Fatalf("recorded %d kicks, want 2", len(got))
	}
	if got[0].KickNumber != 1 || got[0].BlockedFor != KickBlockFirst {
		t.Errorf("first kick = %+v", got[0])
	}
	if got[1].KickNumber != 2 || got[1].BlockedFor != KickBlockFor(2) {
		t.Errorf("second kick = %+v", got[1])
	}
	if got[0].TargetID != "pest" || got[0].ActorID != "host" || got[0].RoomID != r.ID {
		t.Errorf("first kick = %+v", got[0])
	}
}

func TestARefusedKickIsNotRecorded(t *testing.T) {
	s := NewStore()
	recorded := 0
	s.OnKick(func(KickEvent) { recorded++ })

	r, _, _ := s.Create("host", "test", when())
	if _, err := s.Join(r.ID, Anyone("pest"), when()); err != nil {
		t.Fatal(err)
	}
	// Only the host may kick. An attempt that fails must leave no trace, or
	// the log would show kicks that never happened.
	if err := s.Kick(r.ID, "pest", "host", when()); err != ErrNotHost {
		t.Fatalf("got %v, want ErrNotHost", err)
	}
	if recorded != 0 {
		t.Fatalf("recorded %d kicks, want none", recorded)
	}
}

// Room IDs are never reused, so anything keyed by one - a kick record, a chat
// log, a tournament result - can never attach itself to the wrong room.
func TestRoomIDsAreNeverReused(t *testing.T) {
	s := NewStore()
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		r, _, err := s.Create("host", "test", when())
		if err != nil {
			t.Fatal(err)
		}
		if seen[r.ID] {
			t.Fatalf("room ID %s was handed out twice", r.ID)
		}
		seen[r.ID] = true
		if err := s.Leave(r.ID, "host", when()); err != nil {
			t.Fatal(err)
		}
		s.Tick(when().Add(2 * HostGracePeriod))
	}
}
