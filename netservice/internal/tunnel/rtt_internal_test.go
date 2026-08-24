package tunnel

import (
	"testing"
	"time"
)

// The lobby's latency column is only as honest as this arithmetic, and the
// number reaches a player as advice about which room to join. These are the
// cases where a wrong answer would be believed.

func TestTheFirstReadingIsTakenAsIs(t *testing.T) {
	c := New(Config{})
	c.started = time.Now().Add(-time.Second)

	// Sent 40ms ago, in a client that has been up for a second.
	c.timeKeepalive(uint64(time.Second - 40*time.Millisecond))

	if got := c.RTT(); got < 35*time.Millisecond || got > 45*time.Millisecond {
		t.Errorf("first reading came out as %v, want about 40ms", got)
	}
}

func TestNoReadingYetIsZeroRatherThanExcellent(t *testing.T) {
	if got := New(Config{}).RTT(); got != 0 {
		t.Errorf("an unmeasured client reports %v, want 0 so the interface can say so", got)
	}
}

// A path that has genuinely broken produces a sample no average should carry.
// Letting one in would leave a room advertising 900ms for the next minute
// after the path recovered.
func TestAnAbsurdSampleIsRefused(t *testing.T) {
	c := New(Config{})
	c.started = time.Now().Add(-time.Hour)
	c.timeKeepalive(uint64(time.Hour - 40*time.Millisecond))
	settled := c.RTT()

	// An echo whose sequence puts it ten seconds in the past: the path stalled
	// that long, or the packet is not ours.
	c.timeKeepalive(uint64(time.Hour - 10*time.Second))
	if c.RTT() != settled {
		t.Errorf("an absurd sample moved the reading to %v, want it left at %v", c.RTT(), settled)
	}
}

// A sequence we never sent means an old session's packet or a forgery.
// Negative time is the giveaway.
func TestAnEchoFromTheFutureIsRefused(t *testing.T) {
	c := New(Config{})
	c.started = time.Now()
	c.timeKeepalive(uint64(time.Hour))
	if c.RTT() != 0 {
		t.Errorf("an echo from the future was believed: %v", c.RTT())
	}
}

// Smoothing is the point: one bad millisecond on the Wi-Fi must not make the
// column jump, but a path that really changed must be followed.
func TestTheReadingSettlesOnAChangedPath(t *testing.T) {
	c := New(Config{})
	c.started = time.Now().Add(-time.Hour)
	base := time.Hour

	sample := func(d time.Duration) { c.timeKeepalive(uint64(base - d)) }

	sample(20 * time.Millisecond)
	if got := c.RTT(); got > 25*time.Millisecond {
		t.Fatalf("settled at %v, want about 20ms", got)
	}

	// One spike must barely move it.
	sample(200 * time.Millisecond)
	if got := c.RTT(); got > 90*time.Millisecond {
		t.Errorf("a single spike moved the reading to %v - it is not smoothing", got)
	}

	// A path that stays bad must be followed within a minute of keepalives,
	// which at fifteen seconds apart is four samples.
	for i := 0; i < 20; i++ {
		sample(200 * time.Millisecond)
	}
	if got := c.RTT(); got < 180*time.Millisecond {
		t.Errorf("the reading stuck at %v after the path stayed bad - it is too slow to follow", got)
	}
}
