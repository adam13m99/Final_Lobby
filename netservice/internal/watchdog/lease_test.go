package watchdog_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"lobbybaz/netservice/internal/watchdog"
)

func TestRevokedVerdictTearsDownImmediately(t *testing.T) {
	var torn atomic.Bool
	check := func(context.Context) (watchdog.Verdict, error) {
		return watchdog.VerdictRevoked, nil
	}
	w := watchdog.New(check, 10*time.Millisecond, time.Hour, func(string) { torn.Store(true) })

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	if !torn.Load() {
		t.Fatal("revoked lease did not tear down the tunnel")
	}
}

func TestOutageDoesNotExtendTheLease(t *testing.T) {
	var torn atomic.Bool
	check := func(context.Context) (watchdog.Verdict, error) {
		return watchdog.VerdictUnreachable, errors.New("coordinator down")
	}
	// Local expiry of 80ms: an unreachable coordinator must not keep the
	// tunnel alive past it.
	w := watchdog.New(check, 10*time.Millisecond, 80*time.Millisecond, func(string) { torn.Store(true) })

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	if !torn.Load() {
		t.Fatal("tunnel survived past local expiry during an outage - must fail closed")
	}
}

func TestValidChecksKeepTunnelAlive(t *testing.T) {
	var torn atomic.Bool
	check := func(context.Context) (watchdog.Verdict, error) {
		return watchdog.VerdictValid, nil
	}
	w := watchdog.New(check, 10*time.Millisecond, 100*time.Millisecond, func(string) { torn.Store(true) })

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	if torn.Load() {
		t.Fatal("healthy lease was torn down")
	}
}

func TestRecoveryAfterBriefOutageKeepsTunnelAlive(t *testing.T) {
	// A short coordinator blip is common and must not end a match in
	// progress. Only sustained silence past local expiry should.
	var calls atomic.Int32
	var torn atomic.Bool
	check := func(context.Context) (watchdog.Verdict, error) {
		if n := calls.Add(1); n >= 2 && n <= 4 {
			return watchdog.VerdictUnreachable, errors.New("blip")
		}
		return watchdog.VerdictValid, nil
	}
	w := watchdog.New(check, 10*time.Millisecond, 200*time.Millisecond, func(string) { torn.Store(true) })

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	w.Run(ctx)

	if torn.Load() {
		t.Fatal("a three-tick coordinator blip ended the session")
	}
}

func TestTeardownReasonDistinguishesRevocationFromExpiry(t *testing.T) {
	var reason atomic.Pointer[string]
	capture := func(r string) { reason.Store(&r) }

	revoked := watchdog.New(
		func(context.Context) (watchdog.Verdict, error) { return watchdog.VerdictRevoked, nil },
		10*time.Millisecond, time.Hour, capture)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	revoked.Run(ctx)

	if r := reason.Load(); r == nil || *r != "authorisation revoked" {
		t.Fatalf("reason = %v, want \"authorisation revoked\"", r)
	}
}
