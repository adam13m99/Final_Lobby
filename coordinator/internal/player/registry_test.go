package player

import (
	"testing"
	"time"
)

func now() time.Time { return time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC) }

func TestSeenCreatesThenUpdates(t *testing.T) {
	r := NewRegistry()
	p, err := r.Seen("p1", "Arash", now())
	if err != nil {
		t.Fatal(err)
	}
	if p.Nick != "Arash" || p.FirstSeen != now() {
		t.Fatalf("unexpected first sighting: %+v", p)
	}

	later := now().Add(time.Hour)
	p, err = r.Seen("p1", "Arash the Great", later)
	if err != nil {
		t.Fatal(err)
	}
	if p.FirstSeen != now() {
		t.Errorf("FirstSeen moved: %v", p.FirstSeen)
	}
	if p.LastSeen != later || p.Nick != "Arash the Great" {
		t.Errorf("not updated: %+v", p)
	}
}

func TestNicksInAnyScript(t *testing.T) {
	// Most of our players type Persian. A nick check that counts bytes, or
	// insists on ASCII, would reject the majority of real names.
	for _, nick := range []string{"آرش", "Дмитрий", "Arash", "a b"} {
		if _, err := CleanNick(nick); err != nil {
			t.Errorf("rejected a legitimate name %q: %v", nick, err)
		}
	}
}

func TestNicksThatMustBeRejected(t *testing.T) {
	for _, nick := range []string{"", " ", "x", "  x  ", "wildly too long a name here", "bad\nname", "bell\x07"} {
		if _, err := CleanNick(nick); err == nil {
			t.Errorf("accepted %q", nick)
		}
	}
}

func TestNickIsTrimmed(t *testing.T) {
	got, err := CleanNick("  Arash  ")
	if err != nil || got != "Arash" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestFirstMMRIsFree(t *testing.T) {
	r := NewRegistry()
	_, _ = r.Seen("p1", "Arash", now())
	p, err := r.SetMMR("p1", 3500, now())
	if err != nil {
		t.Fatal(err)
	}
	if p.MMR != 3500 {
		t.Fatalf("got %d", p.MMR)
	}
}

func TestMMRCannotChangeTwiceInAWeek(t *testing.T) {
	r := NewRegistry()
	_, _ = r.Seen("p1", "Arash", now())
	if _, err := r.SetMMR("p1", 3500, now()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetMMR("p1", 6000, now().Add(6*24*time.Hour)); err != ErrMMRTooSoon {
		t.Fatalf("expected the weekly limit to bite, got %v", err)
	}
	if _, err := r.SetMMR("p1", 6000, now().Add(8*24*time.Hour)); err != nil {
		t.Fatalf("after a week it must be allowed: %v", err)
	}
}

func TestResendingTheSameMMRIsNotAChange(t *testing.T) {
	// The client sends its whole profile when the player edits their name.
	// If echoing back an unchanged MMR counted as a change, renaming would
	// fail for six days out of seven.
	r := NewRegistry()
	_, _ = r.Seen("p1", "Arash", now())
	if _, err := r.SetMMR("p1", 3500, now()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetMMR("p1", 3500, now().Add(time.Hour)); err != nil {
		t.Fatalf("re-sending the same MMR must be allowed: %v", err)
	}
}

func TestMMRBounds(t *testing.T) {
	r := NewRegistry()
	_, _ = r.Seen("p1", "Arash", now())
	for _, bad := range []int{-1, MaxMMR + 1} {
		if _, err := r.SetMMR("p1", bad, now()); err != ErrBadMMR {
			t.Errorf("accepted %d", bad)
		}
	}
}

func TestMMRForUnknownPlayer(t *testing.T) {
	r := NewRegistry()
	if _, err := r.SetMMR("ghost", 3000, now()); err != ErrNotFound {
		t.Fatalf("got %v", err)
	}
}

func TestOnlineCountsRecentPlayersOnly(t *testing.T) {
	r := NewRegistry()
	_, _ = r.Seen("p1", "Arash", now())
	_, _ = r.Seen("p2", "Sara", now().Add(-10*time.Minute))
	if got := r.Online(2*time.Minute, now()); got != 1 {
		t.Fatalf("got %d online, want 1", got)
	}
}

func TestLookupSkipsStrangers(t *testing.T) {
	r := NewRegistry()
	_, _ = r.Seen("p1", "Arash", now())
	got := r.Lookup([]string{"p1", "", "nobody"})
	if len(got) != 1 {
		t.Fatalf("got %d entries: %+v", len(got), got)
	}
}
