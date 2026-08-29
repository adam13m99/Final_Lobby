package room_test

import (
	"errors"
	"testing"
	"time"

	"lobbybaz/coordinator/internal/room"
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
	slot, err := r.Join(room.Anyone("p2"), t0)
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
	if _, err := r.Join(room.Anyone("p2"), t0); !errors.Is(err, room.ErrRoomLocked) {
		t.Fatalf("err = %v, want ErrRoomLocked", err)
	}
}

func TestHostCanReopenLockedRoomForReplacements(t *testing.T) {
	r := newRoom(t)
	_, _ = r.Join(room.Anyone("p2"), t0)
	_ = r.SetStatus("host-1", room.StatusLocked, t0)
	r.Leave("p2", t0) // abandons mid-match

	if err := r.SetStatus("host-1", room.StatusOpenToNew, t0); err != nil {
		t.Fatal(err)
	}
	slot, err := r.Join(room.Anyone("p3"), t0)
	if err != nil {
		t.Fatalf("replacement could not join: %v", err)
	}
	if slot != 1 {
		t.Fatalf("replacement got slot %d, want the vacated slot 1", slot)
	}
}

func TestOnlyHostChangesStatus(t *testing.T) {
	r := newRoom(t)
	_, _ = r.Join(room.Anyone("p2"), t0)
	if err := r.SetStatus("p2", room.StatusLocked, t0); !errors.Is(err, room.ErrNotHost) {
		t.Fatalf("err = %v, want ErrNotHost", err)
	}
}

// D39: the block escalates 1, 3, 5, 7... minutes. The first is short on
// purpose - most kicks are an argument, not an abuser, and a minute ends the
// re-join fight without taking somebody's evening. Escalation is what deals
// with the person who keeps coming back.
func TestKickBlockEscalates(t *testing.T) {
	for n, want := range map[int]time.Duration{
		1: 1 * time.Minute,
		2: 3 * time.Minute,
		3: 5 * time.Minute,
		4: 7 * time.Minute,
	} {
		if got := room.KickBlockFor(n); got != want {
			t.Errorf("KickBlockFor(%d) = %v, want %v", n, got, want)
		}
	}
	// Defensive: a count below one must not produce a negative block, which
	// would bar nobody and silently disable the whole mechanism.
	if got := room.KickBlockFor(0); got != time.Minute {
		t.Errorf("KickBlockFor(0) = %v, want 1m", got)
	}
}

func TestFirstKickBlocksForOneMinute(t *testing.T) {
	r := newRoom(t)
	_, _ = r.Join(room.Anyone("p2"), t0)
	if err := r.Kick("host-1", "p2", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Join(room.Anyone("p2"), t0.Add(30*time.Second)); !errors.Is(err, room.ErrKickBlocked) {
		t.Fatalf("at 30s err = %v, want ErrKickBlocked", err)
	}
	if _, err := r.Join(room.Anyone("p2"), t0.Add(time.Minute+time.Second)); err != nil {
		t.Fatalf("after the 1-minute block expired, join failed: %v", err)
	}
}

// The count is per player per room, so somebody kicked repeatedly from one
// room is not punished in another - and two different pests do not share a
// tally.
func TestRepeatedKicksBlockForLonger(t *testing.T) {
	r := newRoom(t)

	kickAt := func(at time.Time) {
		t.Helper()
		if _, err := r.Join(room.Anyone("pest"), at); err != nil {
			t.Fatalf("rejoin before kick failed: %v", err)
		}
		if err := r.Kick("host-1", "pest", at); err != nil {
			t.Fatal(err)
		}
	}

	kickAt(t0)
	second := t0.Add(2 * time.Minute)
	kickAt(second)
	// Second kick: three minutes, so two is still barred.
	if _, err := r.Join(room.Anyone("pest"), second.Add(2*time.Minute)); !errors.Is(err, room.ErrKickBlocked) {
		t.Fatalf("second kick should block for 3min, got %v at 2min", err)
	}
	third := second.Add(3*time.Minute + time.Second)
	kickAt(third)
	// Third kick: five minutes.
	if _, err := r.Join(room.Anyone("pest"), third.Add(4*time.Minute)); !errors.Is(err, room.ErrKickBlocked) {
		t.Fatalf("third kick should block for 5min, got %v at 4min", err)
	}
	if _, err := r.Join(room.Anyone("pest"), third.Add(5*time.Minute+time.Second)); err != nil {
		t.Fatalf("third block should have expired at 5min: %v", err)
	}

	// A different person starts at one minute, not at five.
	_, _ = r.Join(room.Anyone("newcomer"), third)
	if err := r.Kick("host-1", "newcomer", third); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Join(room.Anyone("newcomer"), third.Add(time.Minute+time.Second)); err != nil {
		t.Fatalf("a first-time kick must only block for a minute: %v", err)
	}
}

func TestOnlyHostMayKick(t *testing.T) {
	r := newRoom(t)
	_, _ = r.Join(room.Anyone("p2"), t0)
	_, _ = r.Join(room.Anyone("p3"), t0)
	if err := r.Kick("p2", "p3", t0); !errors.Is(err, room.ErrNotHost) {
		t.Fatalf("err = %v, want ErrNotHost", err)
	}
	if r.Slots[2] != "p3" {
		t.Fatal("a non-host kick removed the target anyway")
	}
}

func TestPlayerWhoLeftMayRejoinImmediately(t *testing.T) {
	r := newRoom(t)
	_, _ = r.Join(room.Anyone("p2"), t0)
	r.Leave("p2", t0)
	if _, err := r.Join(room.Anyone("p2"), t0.Add(time.Second)); err != nil {
		t.Fatalf("voluntary leaver could not rejoin: %v", err)
	}
}

// D40: one minute, not two. GameRanger's behaviour but friendlier - a room
// whose host has genuinely gone should not hold nine people staring at it.
func TestHostDepartureClosesRoomAfterOneMinute(t *testing.T) {
	r := newRoom(t)
	_, _ = r.Join(room.Anyone("p2"), t0)
	r.Leave("host-1", t0)

	r.Tick(t0.Add(30 * time.Second))
	if r.Status == room.StatusClosed {
		t.Fatal("room closed before the 1-minute grace expired")
	}
	r.Tick(t0.Add(time.Minute + time.Second))
	if r.Status != room.StatusClosed {
		t.Fatalf("status = %q, want Closed after grace expiry", r.Status)
	}
}

// D40: the host's absence is the only thing that ends a room. A match
// finishing leaves everyone where they are, because the ten people who just
// played are usually the ten who want to play again.
func TestAFinishedMatchLeavesTheRoomAlone(t *testing.T) {
	r := newRoom(t)
	_, _ = r.Join(room.Anyone("p2"), t0)
	if err := r.SetStatus("host-1", room.StatusLocked, t0); err != nil {
		t.Fatal(err)
	}

	// An hour of match, then nothing happens to the room on its own.
	for i := 1; i <= 60; i++ {
		r.Tick(t0.Add(time.Duration(i) * time.Minute))
	}
	if r.Status == room.StatusClosed {
		t.Fatal("the room closed while its host was still present")
	}
	if len(r.Occupants()) != 2 {
		t.Fatalf("occupants = %v, want the host and p2 still seated", r.Occupants())
	}

	// And the host can open it again for the next game.
	if err := r.SetStatus("host-1", room.StatusOpen, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Join(room.Anyone("p3"), t0.Add(time.Hour)); err != nil {
		t.Fatalf("a new player could not join the reopened room: %v", err)
	}
}

func TestHostReturnWithinGraceSavesRoom(t *testing.T) {
	r := newRoom(t)
	r.Leave("host-1", t0)
	if _, err := r.Join(room.Anyone("host-1"), t0.Add(30*time.Second)); err != nil {
		t.Fatalf("host could not reclaim room: %v", err)
	}
	r.Tick(t0.Add(3 * time.Minute))
	if r.Status == room.StatusClosed {
		t.Fatal("room closed despite the host returning within grace")
	}
}

func TestHostReturnsToSlotZeroSoTheAddressIsUnchanged(t *testing.T) {
	r := newRoom(t)
	_, _ = r.Join(room.Anyone("p2"), t0)
	r.Leave("host-1", t0)
	slot, err := r.Join(room.Anyone("host-1"), t0.Add(10*time.Second))
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
	_, _ = r.Join(room.Anyone("p2"), t0)
	_ = r.SetStatus("host-1", room.StatusLocked, t0)
	r.Leave("host-1", t0)

	if _, err := r.Join(room.Anyone("host-1"), t0.Add(20*time.Second)); err != nil {
		t.Fatalf("host locked out of their own match: %v", err)
	}
}

func TestRoomFullRejectsEleventhPlayer(t *testing.T) {
	r := newRoom(t)
	for i := 2; i <= 10; i++ {
		if _, err := r.Join(room.Anyone(string(rune('a'+i))), t0); err != nil {
			t.Fatalf("player %d could not join: %v", i, err)
		}
	}
	if _, err := r.Join(room.Anyone("overflow"), t0); !errors.Is(err, room.ErrRoomFull) {
		t.Fatalf("err = %v, want ErrRoomFull", err)
	}
}

func TestClosedRoomRejectsEverything(t *testing.T) {
	r := newRoom(t)
	_ = r.SetStatus("host-1", room.StatusClosed, t0)
	if _, err := r.Join(room.Anyone("p2"), t0); !errors.Is(err, room.ErrRoomClosed) {
		t.Fatalf("err = %v, want ErrRoomClosed", err)
	}
}

// --- changing seats ------------------------------------------------------

// Which slot you sit in is which team you are on, so moving between them is
// how a player picks a side without leaving and rejoining until the numbers
// come out right.
func TestAPlayerCanMoveToAFreeSlot(t *testing.T) {
	r := newRoom(t)
	if _, err := r.Join(room.Anyone("p2"), t0); err != nil {
		t.Fatal(err)
	}
	if err := r.Move("p2", 7); err != nil {
		t.Fatal(err)
	}
	if r.Slots[1] != "" {
		t.Errorf("the old slot still holds %q", r.Slots[1])
	}
	if r.Slots[7] != "p2" {
		t.Errorf("slot 7 = %q, want p2", r.Slots[7])
	}
}

func TestMovingOntoSomebodyElseIsRefused(t *testing.T) {
	r := newRoom(t)
	r.Join(room.Anyone("p2"), t0)
	r.Join(room.Anyone("p3"), t0)
	if err := r.Move("p2", 2); !errors.Is(err, room.ErrSlotTaken) {
		t.Fatalf("err = %v, want ErrSlotTaken", err)
	}
}

// The host picks a side like everybody else (D64). They used to be nailed to
// slot 0 because slot 0 was the address clients connected to; the address now
// follows them, so the seat is theirs to choose.
func TestTheHostMovesLikeAnybodyElse(t *testing.T) {
	r := newRoom(t)
	if _, err := r.Join(room.Anyone("p2"), t0); err != nil {
		t.Fatal(err)
	}
	if err := r.Move("host-1", 7); err != nil {
		t.Fatalf("the host moving to Dire: %v", err)
	}
	if r.Slots[7] != "host-1" {
		t.Errorf("slot 7 = %q, want the host", r.Slots[7])
	}
	if r.Slots[0] != "" {
		t.Errorf("the host's old slot still holds %q", r.Slots[0])
	}
	if r.HostSlot != 7 {
		t.Errorf("HostSlot = %d, want 7 - the address every client is told", r.HostSlot)
	}
	// And the seat they left is an ordinary seat now, not a reserved one.
	if err := r.Move("p2", 0); err != nil {
		t.Fatalf("taking the seat the host left: %v", err)
	}
	if r.HostSlot != 7 {
		t.Errorf("somebody else's move changed HostSlot to %d", r.HostSlot)
	}
}

// The one seat the host still cannot take. The match runs on their machine.
func TestTheHostCannotWatchTheirOwnRoom(t *testing.T) {
	r := newRoom(t)
	if _, err := r.JoinObserver(room.Anyone("host-1"), t0); !errors.Is(err, room.ErrHostCannotWatch) {
		t.Fatalf("err = %v, want ErrHostCannotWatch", err)
	}
}

// A host who crashed comes back to the seat they were in, not to the lowest
// free one - their address is derived from it, and in the meantime somebody
// else may have taken the seat they started in.
func TestAReturningHostReclaimsTheirOwnSeat(t *testing.T) {
	r := newRoom(t)
	if err := r.Move("host-1", 6); err != nil {
		t.Fatal(err)
	}
	r.Join(room.Anyone("p2"), t0)
	r.Leave("host-1", t0)
	if r.HostGraceUntil.IsZero() {
		t.Fatal("the host left and no grace timer started")
	}
	slot, err := r.Join(room.Anyone("host-1"), t0)
	if err != nil {
		t.Fatal(err)
	}
	if slot != 6 {
		t.Errorf("the host came back to slot %d, want 6", slot)
	}
	if !r.HostGraceUntil.IsZero() {
		t.Error("the host is back and the room is still counting down")
	}
}

// A locked room is a match in progress. Changing team halfway through it puts
// a player on the wrong team inside Dota, which nothing here can undo.
func TestSeatsDoNotChangeDuringAMatch(t *testing.T) {
	r := newRoom(t)
	r.Join(room.Anyone("p2"), t0)
	if err := r.SetStatus("host-1", room.StatusLocked, t0); err != nil {
		t.Fatal(err)
	}
	if err := r.Move("p2", 6); !errors.Is(err, room.ErrRoomLocked) {
		t.Fatalf("err = %v, want ErrRoomLocked", err)
	}
}

func TestMovingOutOfRangeIsRefused(t *testing.T) {
	r := newRoom(t)
	r.Join(room.Anyone("p2"), t0)
	if err := r.Move("p2", 10); !errors.Is(err, room.ErrNoSuchSlot) {
		t.Fatalf("err = %v, want ErrNoSuchSlot", err)
	}
}

func TestSomebodyNotInTheRoomCannotTakeASeat(t *testing.T) {
	r := newRoom(t)
	if err := r.Move("stranger", 5); !errors.Is(err, room.ErrNotMemberOfRoom) {
		t.Fatalf("err = %v, want ErrNotMemberOfRoom", err)
	}
}
