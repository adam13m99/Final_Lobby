package room

import (
	"errors"
	"testing"
	"time"
)

// D41: all four kinds of room ship. These tests are the door, one kind at a
// time - each must admit the people it is for and refuse everybody else.

func TestAPublicRoomAdmitsAnybody(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	if got, _ := s.Get(r.ID); got.Privacy != PrivacyPublic {
		t.Fatalf("a new room is %q, want it public by default", got.Privacy)
	}
	if _, err := s.Join(r.ID, Anyone("stranger"), when()); err != nil {
		t.Fatalf("a stranger was refused from a public room: %v", err)
	}
}

func TestAPasswordRoomAdmitsOnlyThoseWhoKnowIt(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	if err := s.SetPrivacy(r.ID, "host", PrivacyPassword, "open sesame", 0, when()); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Join(r.ID, Anyone("stranger"), when()); err != ErrNeedRoomPassword {
		t.Errorf("no password gave %v, want ErrNeedRoomPassword", err)
	}
	if _, err := s.Join(r.ID, Applicant{ID: "stranger", Password: "open sesam"}, when()); err != ErrWrongRoomPassword {
		t.Errorf("a wrong password gave %v, want ErrWrongRoomPassword", err)
	}
	if _, err := s.Join(r.ID, Applicant{ID: "friend", Password: "open sesame"}, when()); err != nil {
		t.Errorf("the right password was refused: %v", err)
	}
}

// The password is hashed, and the room type keeps the hash unexported so it
// cannot be serialised into a room view by somebody adding a field.
func TestARoomPasswordIsNeverStoredInTheClear(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	if err := s.SetPrivacy(r.ID, "host", PrivacyPassword, "open sesame", 0, when()); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(r.ID)
	if !got.HasPassword() {
		t.Fatal("the room does not report having a password")
	}
	if got.passwordHash == "open sesame" {
		t.Fatal("the password is stored as typed")
	}
	if got.passwordHash == "" {
		t.Fatal("no hash was stored")
	}
}

func TestAPasswordRoomCannotBeCreatedWithoutAPassword(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	// A password room with no password is a public room that looks locked,
	// which is worse than either.
	if err := s.SetPrivacy(r.ID, "host", PrivacyPassword, "", 0, when()); err != ErrPasswordRequired {
		t.Fatalf("got %v, want ErrPasswordRequired", err)
	}
}

// Leaving the old hash behind would mean flipping back to "password" silently
// restores a password nobody remembers setting.
func TestLeavingPasswordModeForgetsThePassword(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	if err := s.SetPrivacy(r.ID, "host", PrivacyPassword, "open sesame", 0, when()); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPrivacy(r.ID, "host", PrivacyPublic, "", 0, when()); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(r.ID); got.HasPassword() {
		t.Fatal("the room kept its password after becoming public")
	}
	if err := s.SetPrivacy(r.ID, "host", PrivacyPassword, "", 0, when()); err != ErrPasswordRequired {
		t.Fatalf("going back to password mode reused the old password: %v", err)
	}
}

func TestAFriendsOnlyRoomAdmitsOnlyTheHostsFriends(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	if err := s.SetPrivacy(r.ID, "host", PrivacyFriends, "", 0, when()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(r.ID, Anyone("stranger"), when()); err != ErrFriendsOnly {
		t.Errorf("a stranger got %v, want ErrFriendsOnly", err)
	}
	if _, err := s.Join(r.ID, Applicant{ID: "mate", Friend: true}, when()); err != nil {
		t.Errorf("a friend was refused: %v", err)
	}
}

func TestAnInviteOnlyRoomAdmitsOnlyThoseTheHostNamed(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	if err := s.SetPrivacy(r.ID, "host", PrivacyInvite, "", 0, when()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(r.ID, Anyone("stranger"), when()); err != ErrInviteOnly {
		t.Errorf("an uninvited player got %v, want ErrInviteOnly", err)
	}

	if err := s.Invite(r.ID, "host", "guest", when()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(r.ID, Anyone("guest"), when()); err != nil {
		t.Errorf("an invited player was refused: %v", err)
	}

	// Only the host's invitation opens the door. Otherwise one person getting
	// in would be enough to let the rest of their group in.
	if err := s.Invite(r.ID, "guest", "guests-mate", when()); err != ErrNotHost {
		t.Errorf("a guest invited somebody: %v", err)
	}
	if _, err := s.Join(r.ID, Anyone("guests-mate"), when()); err != ErrInviteOnly {
		t.Errorf("a guest's invitation worked: %v", err)
	}
}

func TestAnInvitationCanBeWithdrawn(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	_ = s.SetPrivacy(r.ID, "host", PrivacyInvite, "", 0, when())
	_ = s.Invite(r.ID, "host", "guest", when())

	if err := s.Uninvite(r.ID, "host", "guest"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(r.ID, Anyone("guest"), when()); err != ErrInviteOnly {
		t.Errorf("a withdrawn invitation still worked: %v", err)
	}
}

func TestTheMMRFloorIsCheckedBeforeAnySecret(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	if err := s.SetPrivacy(r.ID, "host", PrivacyPassword, "open sesame", 3000, when()); err != nil {
		t.Fatal(err)
	}

	// Told the useful thing first: a password was never going to help.
	low := Applicant{ID: "beginner", MMR: 900, Password: "open sesame"}
	if _, err := s.Join(r.ID, low, when()); err != ErrMMRTooLow {
		t.Errorf("got %v, want ErrMMRTooLow", err)
	}
	ok := Applicant{ID: "veteran", MMR: 4500, Password: "open sesame"}
	if _, err := s.Join(r.ID, ok, when()); err != nil {
		t.Errorf("a qualified player was refused: %v", err)
	}
}

func TestAnMMRFloorAppliesToAPublicRoomToo(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	if err := s.SetPrivacy(r.ID, "host", PrivacyPublic, "", 3000, when()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(r.ID, Applicant{ID: "beginner", MMR: 900}, when()); err != ErrMMRTooLow {
		t.Errorf("got %v, want ErrMMRTooLow", err)
	}
	if _, err := s.Join(r.ID, Applicant{ID: "veteran", MMR: 3000}, when()); err != nil {
		t.Errorf("exactly the floor was refused: %v", err)
	}
}

// The door applies to the observer gallery too. A friends-only room whose
// observer seats anybody can take is not a friends-only room.
func TestTheDoorAppliesToObserversAsWell(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	_ = s.SetPrivacy(r.ID, "host", PrivacyPassword, "open sesame", 0, when())

	if _, err := s.JoinObserver(r.ID, Anyone("nosy"), when()); err != ErrNeedRoomPassword {
		t.Errorf("an observer walked past the padlock: %v", err)
	}
	if _, err := s.JoinObserver(r.ID, Applicant{ID: "mate", Password: "open sesame"}, when()); err != nil {
		t.Errorf("an observer with the password was refused: %v", err)
	}
}

// An admin is called in precisely when something has gone wrong in a room
// they were never invited to. A moderator who can be locked out by a password
// is not a moderator.
func TestStaffAreNotStoppedByTheDoor(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	_ = s.SetPrivacy(r.ID, "host", PrivacyInvite, "", 5000, when())

	if _, err := s.JoinAdmin(r.ID, "admin", when()); err != nil {
		t.Fatalf("an admin was refused from a private room: %v", err)
	}
}

// ...but the kick block is enforced against identity, not against role, and
// no credential lifts it.
func TestNoCredentialLiftsAKickBlock(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	_, _ = s.Join(r.ID, Anyone("pest"), when())
	if err := s.Kick(r.ID, "host", "pest", when()); err != nil {
		t.Fatal(err)
	}
	_ = s.SetPrivacy(r.ID, "host", PrivacyInvite, "", 0, when())
	_ = s.Invite(r.ID, "host", "pest", when())

	if _, err := s.Join(r.ID, Anyone("pest"), when()); err != ErrKickBlocked {
		t.Errorf("an invitation lifted a kick block: %v", err)
	}
	if _, err := s.JoinAdmin(r.ID, "pest", when()); err != ErrKickBlocked {
		t.Errorf("being staff lifted a kick block: %v", err)
	}
}

// Changing the door never evicts anybody. Somebody who joined a public room
// and is now in a friends-only one stays: they were let in legitimately, and
// throwing them out because the host changed a setting is the kind of
// surprise that makes a host afraid to touch settings.
func TestChangingTheDoorDoesNotEvictAnybody(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	if _, err := s.Join(r.ID, Anyone("stranger"), when()); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPrivacy(r.ID, "host", PrivacyFriends, "", 5000, when()); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(r.ID)
	if _, _, seated := got.SlotOf("stranger"); !seated {
		t.Fatal("changing the door evicted somebody already seated")
	}
}

func TestOnlyTheHostChangesTheDoor(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	_, _ = s.Join(r.ID, Anyone("p2"), when())
	if err := s.SetPrivacy(r.ID, "p2", PrivacyPublic, "", 0, when()); err != ErrNotHost {
		t.Fatalf("got %v, want ErrNotHost", err)
	}
}

func TestNonsenseSettingsAreRefused(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	if err := s.SetPrivacy(r.ID, "host", Privacy("secret-handshake"), "", 0, when()); err != ErrBadPrivacy {
		t.Errorf("got %v, want ErrBadPrivacy", err)
	}
	for _, bad := range []int{-1, MaxMinMMR + 1} {
		if err := s.SetPrivacy(r.ID, "host", PrivacyPublic, "", bad, when()); err != ErrBadMinMMR {
			t.Errorf("min MMR %d gave %v, want ErrBadMinMMR", bad, err)
		}
	}
}

// A host who crashed mid-match does not come back to the room - there is no
// room to come back to (D84).
//
// This test used to assert the opposite, and the reasoning was sound while a
// grace existed: the host was not asked for their own password, was not
// measured against their own MMR floor, and could walk through a locked door
// into the match running on their own PC. All of that hung on the room still
// being there a few seconds later, and it no longer is.
func TestTheHostCannotReclaimTheRoomTheyDroppedOutOf(t *testing.T) {
	s := NewStore()
	r, _, _ := s.Create("host", "test", when())
	_ = s.SetPrivacy(r.ID, "host", PrivacyPassword, "open sesame", 9000, when())
	if err := s.SetStatus(r.ID, "host", StatusLocked, when()); err != nil {
		t.Fatal(err)
	}
	// Losing the host - a crash, a cut line - rather than the host deciding
	// to go. Both end the room now; this is the one nobody pressed.
	s.rooms[r.ID].Leave("host", when())

	if s.rooms[r.ID].Status != StatusClosed {
		t.Fatalf("status = %q, want closed the moment the host was lost",
			s.rooms[r.ID].Status)
	}
	back := when().Add(30 * time.Second)
	if _, err := s.Join(r.ID, Anyone("host"), back); !errors.Is(err, ErrRoomClosed) {
		t.Fatalf("the host walked back into a closed room (err = %v)", err)
	}
}
