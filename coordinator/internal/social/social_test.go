package social

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"lobbybaz/coordinator/internal/account"
	"lobbybaz/coordinator/internal/store"
)

func rig(t *testing.T, names ...string) (*Store, *sql.DB, map[string]string) {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	acc := account.New(db)
	ids := map[string]string{}
	for _, n := range names {
		a, err := acc.SignUp(n, n, "a long enough password", at(0))
		if err != nil {
			t.Fatalf("signing up %s: %v", n, err)
		}
		ids[n] = a.ID
	}
	return New(db), db, ids
}

func at(min int) time.Time {
	return time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC).Add(time.Duration(min) * time.Minute)
}

func TestARequestBecomesAFriendshipOnlyWhenAnswered(t *testing.T) {
	s, _, id := rig(t, "reza", "ali")

	if err := s.Request(id["reza"], id["ali"], at(0)); err != nil {
		t.Fatal(err)
	}
	// One row is not a friendship. A friends-only room must not admit
	// somebody who merely asked.
	if yes, _ := s.AreFriends(id["reza"], id["ali"]); yes {
		t.Fatal("an unanswered request counted as a friendship")
	}

	in, err := s.Incoming(id["ali"])
	if err != nil {
		t.Fatal(err)
	}
	if len(in) != 1 || in[0].AccountID != id["reza"] || !in[0].Incoming {
		t.Fatalf("ali's inbox = %+v", in)
	}
	// And reza sees it as sent, not received - the two need different buttons.
	out, _ := s.Outgoing(id["reza"])
	if len(out) != 1 || out[0].Incoming {
		t.Fatalf("reza's outbox = %+v", out)
	}

	if err := s.Accept(id["ali"], id["reza"], at(1)); err != nil {
		t.Fatal(err)
	}
	if yes, _ := s.AreFriends(id["reza"], id["ali"]); !yes {
		t.Fatal("accepting did not make them friends")
	}
	// Both sides see it.
	for _, who := range []string{"reza", "ali"} {
		f, _ := s.Friends(id[who])
		if len(f) != 1 {
			t.Errorf("%s has %d friends, want 1", who, len(f))
		}
	}
	if in, _ := s.Incoming(id["ali"]); len(in) != 0 {
		t.Error("the request is still in the inbox after being accepted")
	}
}

// Sending a request to somebody whose request is already sitting in your own
// inbox is what a person does when they have not noticed it. Treating it as
// an acceptance is what they meant.
func TestRequestingSomebodyWhoAlreadyAskedAcceptsInstead(t *testing.T) {
	s, _, id := rig(t, "reza", "ali")
	if err := s.Request(id["reza"], id["ali"], at(0)); err != nil {
		t.Fatal(err)
	}
	if err := s.Request(id["ali"], id["reza"], at(1)); err != nil {
		t.Fatal(err)
	}
	if yes, _ := s.AreFriends(id["reza"], id["ali"]); !yes {
		t.Fatal("the crossing requests did not become a friendship")
	}
}

func TestAcceptingNothingIsAnError(t *testing.T) {
	s, _, id := rig(t, "reza", "ali")
	if err := s.Accept(id["reza"], id["ali"], at(0)); !errors.Is(err, ErrNoRequest) {
		t.Fatalf("got %v, want ErrNoRequest", err)
	}
}

func TestDecliningLeavesNoTrace(t *testing.T) {
	s, _, id := rig(t, "reza", "ali")
	_ = s.Request(id["reza"], id["ali"], at(0))
	if err := s.Decline(id["ali"], id["reza"]); err != nil {
		t.Fatal(err)
	}
	if in, _ := s.Incoming(id["ali"]); len(in) != 0 {
		t.Error("the request survived being declined")
	}
	if out, _ := s.Outgoing(id["reza"]); len(out) != 0 {
		t.Error("the sender still sees a pending request")
	}
	// And it can be asked again. Declining is not blocking.
	if err := s.Request(id["reza"], id["ali"], at(5)); err != nil {
		t.Fatal(err)
	}
	if in, _ := s.Incoming(id["ali"]); len(in) != 1 {
		t.Error("a second request did not arrive")
	}
}

// A friendship one person has left is not a friendship. Leaving the other row
// behind would show them a friend who cannot see them.
func TestRemovingAFriendIsMutual(t *testing.T) {
	s, _, id := rig(t, "reza", "ali")
	_ = s.Request(id["reza"], id["ali"], at(0))
	_ = s.Accept(id["ali"], id["reza"], at(1))

	if err := s.Remove(id["reza"], id["ali"]); err != nil {
		t.Fatal(err)
	}
	for _, who := range []string{"reza", "ali"} {
		if f, _ := s.Friends(id[who]); len(f) != 0 {
			t.Errorf("%s still has a friend after the friendship ended", who)
		}
	}
	if yes, _ := s.AreFriends(id["reza"], id["ali"]); yes {
		t.Fatal("they are still friends")
	}
}

func TestNobodyCanBefriendThemselves(t *testing.T) {
	s, _, id := rig(t, "reza")
	if err := s.Request(id["reza"], id["reza"], at(0)); !errors.Is(err, ErrSelf) {
		t.Fatalf("got %v, want ErrSelf", err)
	}
	if yes, _ := s.AreFriends(id["reza"], id["reza"]); yes {
		t.Fatal("somebody is their own friend, which would let them into their own friends-only room by a second path")
	}
}

// --- blocking -----------------------------------------------------------

func TestBlockingEndsTheFriendshipAndSilencesTheRequest(t *testing.T) {
	s, _, id := rig(t, "reza", "pest")
	_ = s.Request(id["reza"], id["pest"], at(0))
	_ = s.Accept(id["pest"], id["reza"], at(1))

	if err := s.Block(id["reza"], id["pest"], at(2)); err != nil {
		t.Fatal(err)
	}
	if yes, _ := s.AreFriends(id["reza"], id["pest"]); yes {
		t.Fatal("blocking left the friendship standing")
	}
	if f, _ := s.Friends(id["pest"]); len(f) != 0 {
		t.Fatal("the blocked person still has the blocker as a friend")
	}

	// The block is silent: the request is accepted and dropped. Returning an
	// error would tell somebody they had been blocked, which turns blocking
	// into a message and gives a determined person a reason to make another
	// account.
	if err := s.Request(id["pest"], id["reza"], at(3)); err != nil {
		t.Fatalf("the refused request reported an error: %v", err)
	}
	if in, _ := s.Incoming(id["reza"]); len(in) != 0 {
		t.Fatal("a blocked person's request reached the inbox")
	}
}

func TestABlockWorksInBothDirections(t *testing.T) {
	s, _, id := rig(t, "reza", "pest")
	_ = s.Block(id["reza"], id["pest"], at(0))

	// The blocker cannot reach the blocked either. Being able to would let
	// somebody block a person and then keep messaging them.
	if err := s.Request(id["reza"], id["pest"], at(1)); err != nil {
		t.Fatal(err)
	}
	if in, _ := s.Incoming(id["pest"]); len(in) != 0 {
		t.Fatal("the blocker reached the person they blocked")
	}
}

func TestUnblockingDoesNotRestoreTheFriendship(t *testing.T) {
	s, _, id := rig(t, "reza", "ali")
	_ = s.Request(id["reza"], id["ali"], at(0))
	_ = s.Accept(id["ali"], id["reza"], at(1))
	_ = s.Block(id["reza"], id["ali"], at(2))

	if err := s.Unblock(id["reza"], id["ali"]); err != nil {
		t.Fatal(err)
	}
	if yes, _ := s.AreFriends(id["reza"], id["ali"]); yes {
		t.Fatal("unblocking silently restored a friendship")
	}
	// It has to be asked for again, which is the honest thing: unblocking is
	// not forgiving.
	if err := s.Request(id["reza"], id["ali"], at(3)); err != nil {
		t.Fatal(err)
	}
	if in, _ := s.Incoming(id["ali"]); len(in) != 1 {
		t.Fatal("a request after unblocking did not arrive")
	}
}

func TestBlockedListsWhoWasBlocked(t *testing.T) {
	s, _, id := rig(t, "reza", "amir", "bita")
	_ = s.Block(id["reza"], id["amir"], at(0))
	_ = s.Block(id["reza"], id["bita"], at(1))
	got, err := s.Blocked(id["reza"])
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("blocked = %v, want two", got)
	}
	if yes, _ := s.IsBlocked(id["reza"], id["amir"]); !yes {
		t.Error("IsBlocked says no")
	}
	if yes, _ := s.IsBlocked(id["amir"], id["reza"]); yes {
		t.Error("a block was reported in the wrong direction")
	}
}

// --- private messages ---------------------------------------------------

func TestPrivateMessagesGoBetweenFriendsOnly(t *testing.T) {
	s, _, id := rig(t, "reza", "ali", "stranger")
	_ = s.Request(id["reza"], id["ali"], at(0))
	_ = s.Accept(id["ali"], id["reza"], at(1))

	if _, err := s.Send(id["reza"], id["stranger"], "hello", at(2)); !errors.Is(err, ErrNotFriends) {
		t.Fatalf("a stranger was messageable: %v", err)
	}
	if _, err := s.Send(id["reza"], id["ali"], "سلام", at(2)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(id["ali"], id["reza"], "hello back", at(3)); err != nil {
		t.Fatal(err)
	}

	msgs, err := s.Conversation(id["reza"], id["ali"], 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("conversation = %d messages, want 2", len(msgs))
	}
	if msgs[0].Body != "سلام" {
		t.Errorf("first message = %q", msgs[0].Body)
	}
	// Both sides see the same conversation, whichever way round they ask.
	other, _ := s.Conversation(id["ali"], id["reza"], 0, 100)
	if len(other) != 2 {
		t.Fatalf("the other side sees %d messages", len(other))
	}

	// The cursor carries only what is new, which is what a poll needs.
	fresh, _ := s.Conversation(id["reza"], id["ali"], msgs[0].ID, 100)
	if len(fresh) != 1 || fresh[0].Body != "hello back" {
		t.Fatalf("after the cursor = %+v", fresh)
	}
}

// A message sent to somebody who is offline has to still be there when they
// come back, or "message a friend" means "message a friend who is currently
// looking at the app".
func TestMessagesSurviveARestart(t *testing.T) {
	s, db, id := rig(t, "reza", "ali")
	_ = s.Request(id["reza"], id["ali"], at(0))
	_ = s.Accept(id["ali"], id["reza"], at(1))
	if _, err := s.Send(id["reza"], id["ali"], "are you playing tonight?", at(2)); err != nil {
		t.Fatal(err)
	}

	// A second Store over the same database is what a restart looks like from
	// the data's point of view.
	again := New(db)
	msgs, err := again.Conversation(id["ali"], id["reza"], 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("the message did not survive: %+v", msgs)
	}
}

func TestUnreadCountsAndClears(t *testing.T) {
	s, _, id := rig(t, "reza", "ali")
	_ = s.Request(id["reza"], id["ali"], at(0))
	_ = s.Accept(id["ali"], id["reza"], at(1))
	for i := 0; i < 3; i++ {
		if _, err := s.Send(id["reza"], id["ali"], "hello", at(2+i)); err != nil {
			t.Fatal(err)
		}
	}

	unread, err := s.Unread(id["ali"])
	if err != nil {
		t.Fatal(err)
	}
	if unread[id["reza"]] != 3 {
		t.Fatalf("unread = %v, want 3 from reza", unread)
	}
	// Reading my own outbox is not unread mail.
	if mine, _ := s.Unread(id["reza"]); len(mine) != 0 {
		t.Fatalf("the sender has unread mail from themselves: %v", mine)
	}

	if err := s.MarkRead(id["ali"], id["reza"], at(10)); err != nil {
		t.Fatal(err)
	}
	if unread, _ := s.Unread(id["ali"]); len(unread) != 0 {
		t.Fatalf("unread after reading = %v", unread)
	}
}

func TestAMessageToSomebodyWhoBlockedYouIsAcceptedAndDropped(t *testing.T) {
	s, _, id := rig(t, "reza", "pest")
	_ = s.Request(id["reza"], id["pest"], at(0))
	_ = s.Accept(id["pest"], id["reza"], at(1))
	_ = s.Block(id["reza"], id["pest"], at(2))

	// No error: an error would tell the pest they had been blocked.
	if _, err := s.Send(id["pest"], id["reza"], "let me in", at(3)); err != nil {
		t.Fatalf("the dropped message reported an error: %v", err)
	}
	msgs, _ := s.Conversation(id["reza"], id["pest"], 0, 100)
	if len(msgs) != 0 {
		t.Fatalf("a blocked person's message was delivered: %+v", msgs)
	}
}

func TestEmptyAndOverlongMessagesAreRefused(t *testing.T) {
	s, _, id := rig(t, "reza", "ali")
	_ = s.Request(id["reza"], id["ali"], at(0))
	_ = s.Accept(id["ali"], id["reza"], at(1))

	if _, err := s.Send(id["reza"], id["ali"], "   ", at(2)); !errors.Is(err, ErrEmptyMessage) {
		t.Errorf("whitespace was accepted: %v", err)
	}
	// Counted in characters, so a Persian message is measured the way a
	// person would measure it.
	long := strings.Repeat("ع", MaxMessage+1)
	if _, err := s.Send(id["reza"], id["ali"], long, at(2)); !errors.Is(err, ErrMessageTooLong) {
		t.Errorf("an overlong message was accepted: %v", err)
	}
	if _, err := s.Send(id["reza"], id["ali"], strings.Repeat("ع", MaxMessage), at(2)); err != nil {
		t.Errorf("exactly the limit was refused: %v", err)
	}
}

// --- invitations --------------------------------------------------------

func TestInvitingAFriendToARoom(t *testing.T) {
	s, _, id := rig(t, "reza", "ali", "stranger")
	_ = s.Request(id["reza"], id["ali"], at(0))
	_ = s.Accept(id["ali"], id["reza"], at(1))

	if err := s.InviteToRoom(id["reza"], id["stranger"], "r_abc", at(2)); !errors.Is(err, ErrNotFriends) {
		t.Errorf("a stranger was invitable: %v", err)
	}
	if err := s.InviteToRoom(id["reza"], id["ali"], "r_abc", at(2)); err != nil {
		t.Fatal(err)
	}

	got, err := s.Invitations(id["ali"])
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RoomID != "r_abc" || got[0].FromID != id["reza"] {
		t.Fatalf("invitations = %+v", got)
	}

	if err := s.MarkInvitationsSeen(id["ali"], at(3)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Invitations(id["ali"]); len(got) != 0 {
		t.Fatal("the badge did not clear")
	}
}

func TestAFriendsListHasACeiling(t *testing.T) {
	// The ceiling exists so one account cannot turn the friend table into a
	// denial of service by adding everybody. It is not a target.
	if MaxFriends < 50 {
		t.Fatalf("MaxFriends = %d, low enough to be a real limitation", MaxFriends)
	}
}
