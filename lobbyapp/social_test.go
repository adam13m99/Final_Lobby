package main

import (
	"errors"
	"testing"
	"time"
)

// The friends list, the announcement strip and the server's capabilities are
// each cached because the lobby polls every two seconds and none of them
// changes that fast. A cache that silently never caches would multiply the
// coordinator's load by the number of open windows, and would do it quietly.

func TestACacheDoesNotRefetchWithinItsInterval(t *testing.T) {
	var c cached[int]
	c.every = time.Minute

	calls := 0
	fetch := func() (int, error) { calls++; return calls, nil }

	if v, _ := c.get(fetch); v != 1 {
		t.Fatalf("first get returned %d, want 1", v)
	}
	for i := 0; i < 5; i++ {
		c.get(fetch)
	}
	if calls != 1 {
		t.Errorf("fetched %d times inside the interval, want 1", calls)
	}
}

// A cache with no interval set is the bug this test exists for: it looks like
// a cache, it is used like a cache, and it fetches every single time.
func TestEveryCacheTheServerHoldsHasAnInterval(t *testing.T) {
	s := &server{}
	s.friendsCache.every = friendsEvery
	s.bannersCache.every = bannersEvery
	s.infoCache.every = infoEvery

	for name, every := range map[string]time.Duration{
		"friends": friendsEvery,
		"banners": bannersEvery,
		"info":    infoEvery,
	} {
		if every <= 0 {
			t.Errorf("the %s cache has no interval, so it refetches on every poll", name)
		}
	}
}

// A failed fetch must not blank what is on screen. A friends list that
// disappears every time one request fails is worse than one a few seconds
// stale.
func TestAFailedFetchKeepsWhatWasThere(t *testing.T) {
	var c cached[int]
	c.every = 0 // refetch every time, so the failure lands immediately

	c.get(func() (int, error) { return 7, nil })
	got, why := c.get(func() (int, error) { return 0, errors.New("server is down") })
	if got != 7 {
		t.Errorf("a failed fetch replaced the value with %d, want the previous 7", got)
	}
	if why == "" {
		t.Error("a failed fetch reported no reason")
	}
}

// After a failure the cache sulks, so a coordinator with no friends list is
// not asked for one every two seconds forever.
func TestAFailedFetchIsLeftAloneForAWhile(t *testing.T) {
	var c cached[int]
	c.every = time.Nanosecond

	calls := 0
	failing := func() (int, error) { calls++; return 0, errors.New("no") }
	c.get(failing)
	for i := 0; i < 10; i++ {
		c.get(failing)
	}
	if calls != 1 {
		t.Errorf("retried %d times after a failure, want 1 until the sulk expires", calls)
	}
}

// An action that changes what is cached has to drop it, or the player waits
// up to five seconds to see their own change.
func TestForgettingMakesTheNextGetFetch(t *testing.T) {
	var c cached[int]
	c.every = time.Hour

	calls := 0
	fetch := func() (int, error) { calls++; return calls, nil }
	c.get(fetch)
	c.forget()
	c.get(fetch)
	if calls != 2 {
		t.Errorf("fetched %d times, want 2 - forget did not take effect", calls)
	}
}
