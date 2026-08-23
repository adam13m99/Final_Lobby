package chat

import (
	"strings"
	"testing"
	"time"
)

func at() time.Time { return time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC) }

func TestPostThenReadFromTheStart(t *testing.T) {
	b := NewBoard()
	if _, err := b.Post(Lobby, "p1", "Arash", "anyone for a game?", at()); err != nil {
		t.Fatal(err)
	}
	got := b.Since(Lobby, 0)
	if len(got) != 1 || got[0].Text != "anyone for a game?" || got[0].Nick != "Arash" {
		t.Fatalf("got %+v", got)
	}
}

func TestCursorOnlyReturnsWhatIsNew(t *testing.T) {
	b := NewBoard()
	first, _ := b.Post(Lobby, "p1", "Arash", "one", at())
	_, _ = b.Post(Lobby, "p2", "Sara", "two", at())

	got := b.Since(Lobby, first.ID)
	if len(got) != 1 || got[0].Text != "two" {
		t.Fatalf("got %+v", got)
	}
	if got := b.Since(Lobby, 999); len(got) != 0 {
		t.Fatalf("a cursor ahead of the channel must return nothing, got %+v", got)
	}
}

func TestChannelsDoNotLeakIntoEachOther(t *testing.T) {
	b := NewBoard()
	_, _ = b.Post(Lobby, "p1", "Arash", "in the lobby", at())
	_, _ = b.Post("r1", "p1", "Arash", "in the room", at())

	if got := b.Since(Lobby, 0); len(got) != 1 || got[0].Text != "in the lobby" {
		t.Fatalf("lobby got %+v", got)
	}
	if got := b.Since("r1", 0); len(got) != 1 || got[0].Text != "in the room" {
		t.Fatalf("room got %+v", got)
	}
}

func TestHistoryIsCapped(t *testing.T) {
	b := NewBoard()
	for i := 0; i < History+50; i++ {
		if _, err := b.Post(Lobby, "p1", "Arash", "spam", at()); err != nil {
			t.Fatal(err)
		}
	}
	if got := b.Since(Lobby, 0); len(got) != History {
		t.Fatalf("kept %d messages, want %d", len(got), History)
	}
}

func TestIDsKeepRisingAcrossTrimming(t *testing.T) {
	// The client's cursor is an ID. If trimming reset or reused IDs, a
	// client would either re-read old messages forever or miss new ones.
	b := NewBoard()
	for i := 0; i < History+10; i++ {
		_, _ = b.Post(Lobby, "p1", "Arash", "x", at())
	}
	msgs := b.Since(Lobby, 0)
	for i := 1; i < len(msgs); i++ {
		if msgs[i].ID <= msgs[i-1].ID {
			t.Fatalf("IDs went backwards at %d: %d then %d", i, msgs[i-1].ID, msgs[i].ID)
		}
	}
	if b.Cursor(Lobby) != msgs[len(msgs)-1].ID {
		t.Errorf("cursor disagrees with the last message")
	}
}

func TestSystemMessages(t *testing.T) {
	b := NewBoard()
	m := b.System("r1", "Sara joined", at())
	if !m.System || m.PlayerID != "" {
		t.Fatalf("got %+v", m)
	}
}

func TestNewlinesBecomeSpacesRatherThanErrors(t *testing.T) {
	got, err := Clean("two\nlines")
	if err != nil {
		t.Fatal(err)
	}
	if got != "two lines" {
		t.Fatalf("got %q", got)
	}
}

func TestEmptyAndOversizedAreRejected(t *testing.T) {
	if _, err := Clean("   "); err != ErrEmpty {
		t.Errorf("blank accepted: %v", err)
	}
	if _, err := Clean(strings.Repeat("x", MaxTextRunes+1)); err != ErrTooLong {
		t.Errorf("oversized accepted: %v", err)
	}
	if _, err := Clean(strings.Repeat("ب", MaxTextRunes)); err != nil {
		t.Errorf("a full-length Persian message must fit: %v", err)
	}
}

func TestDropForgetsAChannel(t *testing.T) {
	b := NewBoard()
	_, _ = b.Post("r1", "p1", "Arash", "hello", at())
	b.Drop("r1")
	if got := b.Since("r1", 0); len(got) != 0 {
		t.Fatalf("still there: %+v", got)
	}
}
