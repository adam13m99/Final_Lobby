package api

import (
	"net/http"
	"testing"
	"time"
)

// A friends list that says "offline" and nothing else cannot answer the
// question people ask it - is it worth waiting for them. The registry has
// always known when somebody was last here, but only for as long as the
// process lived, so every deployment forgot even that. The answer is stored
// now and read back beside the name.
func TestAnOfflineFriendCarriesWhenTheyWereLastHere(t *testing.T) {
	g := newAuthRig(t)
	rezaID, reza := g.register("reza", "a long enough password")
	aliID, ali := g.register("ali", "a long enough password")

	if r := g.act(reza, aliID, "request"); r.code != http.StatusOK {
		t.Fatalf("request: %d %v", r.code, r.body)
	}
	if r := g.act(ali, rezaID, "accept"); r.code != http.StatusOK {
		t.Fatalf("accept: %d %v", r.code, r.body)
	}
	// Ali signed in a moment ago, so the registry has him as here now and
	// the rail says "online" rather than a time. Forget him, the way a
	// restarted coordinator would: from here on the only record of when he
	// was around is the one in the database.
	g.players.Forget(aliID)

	// Nobody has written one yet, so there is no last-seen time to give.
	// Nothing is the honest answer, and it must not be the epoch.
	one := g.friendsOf(reza, "friends")[0].(map[string]any)
	if _, present := one["last_seen"]; present {
		t.Errorf("a friend nobody has ever seen carried a last-seen time: %v", one)
	}

	// Ali was here yesterday.
	yesterday := time.Date(2026, 8, 23, 9, 30, 0, 0, time.UTC)
	if err := g.acc.RecordSeen(map[string]time.Time{aliID: yesterday}); err != nil {
		t.Fatalf("recording presence: %v", err)
	}

	one = g.friendsOf(reza, "friends")[0].(map[string]any)
	if online, _ := one["online"].(bool); online {
		t.Error("a friend last seen yesterday was reported as online")
	}
	got, _ := one["last_seen"].(string)
	when, err := time.Parse(time.RFC3339Nano, got)
	if err != nil {
		t.Fatalf("last_seen came back as %q: %v", got, err)
	}
	if !when.Equal(yesterday) {
		t.Errorf("last_seen = %v, want %v", when, yesterday)
	}
}

// Somebody you blocked is not somebody whose whereabouts you get to watch,
// and when they were last online is whereabouts.
func TestABlockedPersonsLastSeenIsNotReported(t *testing.T) {
	g := newAuthRig(t)
	_, reza := g.register("reza", "a long enough password")
	aliID, _ := g.register("ali", "a long enough password")
	g.players.Forget(aliID)

	if err := g.acc.RecordSeen(map[string]time.Time{
		aliID: time.Date(2026, 8, 23, 9, 30, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("recording presence: %v", err)
	}
	if r := g.act(reza, aliID, "block"); r.code != http.StatusOK {
		t.Fatalf("block: %d %v", r.code, r.body)
	}

	blocked := g.friendsOf(reza, "blocked")
	if len(blocked) != 1 {
		t.Fatalf("blocked list = %v", blocked)
	}
	one := blocked[0].(map[string]any)
	if _, present := one["last_seen"]; present {
		t.Errorf("a blocked person's last-seen time was reported: %v", one)
	}
}
