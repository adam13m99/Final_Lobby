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

func TestOnlyHostMayKick(t *testing.T) {
	r := newRoom(t)
	_, _ = r.Join("p2", t0)
	_, _ = r.Join("p3", t0)
	if err := r.Kick("p2", "p3", t0); !errors.Is(err, room.ErrNotHost) {
		t.Fatalf("err = %v, want ErrNotHost", err)
	}
	if r.Slots[2] != "p3" {
		t.Fatal("a non-host kick removed the target anyway")
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

func TestHostReturnsToSlotZeroSoTheAddressIsUnchanged(t *testing.T) {
	r := newRoom(t)
	_, _ = r.Join("p2", t0)
	r.Leave("host-1", t0)
	slot, err := r.Join("host-1", t0.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if slot != 0 {
		t.Fatalf("returning host got slot %d, want 0 - clients are already "+
			"connecting to the slot 0 address", slot)
	}
}

func TestLockedRoomStillAdmitsTheReturningHost(t *testing.T) {
	r := newRoom(t)
	_, _ = r.Join("p2", t0)
	_ = r.SetStatus("host-1", room.StatusLocked, t0)
	r.Leave("host-1", t0)

	if _, err := r.Join("host-1", t0.Add(20*time.Second)); err != nil {
		t.Fatalf("host locked out of their own match: %v", err)
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
