package store

import (
	"testing"
	"time"
)

func TestKicksAreRecordedAndCounted(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	k := NewKicks(db)

	base := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= 3; i++ {
		if err := k.Record("r_abc", "a_host", "a_pest", i,
			time.Duration(i)*time.Minute, base.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatalf("kick %d: %v", i, err)
		}
	}
	// Somebody else's kick must not be counted against the pest.
	if err := k.Record("r_abc", "a_host", "a_other", 1, time.Minute, base); err != nil {
		t.Fatal(err)
	}

	n, err := k.TimesKicked("a_pest", base)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("TimesKicked = %d, want 3", n)
	}
	// The window is what makes this useful: "three times ever" and "three
	// times this week" are different facts about a person.
	if n, _ := k.TimesKicked("a_pest", base.Add(150*time.Minute)); n != 1 {
		t.Errorf("TimesKicked since 2.5h = %d, want 1", n)
	}
}

func TestKickHistoryReadsNewestFirst(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	k := NewKicks(db)

	base := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= 3; i++ {
		if err := k.Record("r_abc", "a_host", "a_pest", i,
			time.Duration(i)*time.Minute, base.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := k.History("a_pest", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("history = %d rows, want 3", len(got))
	}
	if got[0].KickNumber != 3 {
		t.Errorf("first row is kick %d, want the most recent (3)", got[0].KickNumber)
	}
	// The escalation that was applied is part of the record, so the log
	// explains itself without anyone having to recompute it.
	if got[0].BlockedFor != 3*time.Minute {
		t.Errorf("blocked for %v, want 3m", got[0].BlockedFor)
	}
	if got[0].RoomID != "r_abc" || got[0].ActorID != "a_host" {
		t.Errorf("row = %+v", got[0])
	}
}
