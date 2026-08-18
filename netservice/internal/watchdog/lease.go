// Package watchdog enforces network authorisation locally.
//
// It runs inside the Windows service, not the desktop app, so a closed or
// tampered-with UI cannot keep a revoked player connected. The service owns
// the adapter; the UI only asks it for things.
package watchdog

import (
	"context"
	"time"
)

// Verdict is the coordinator's answer about a lease.
type Verdict int

const (
	VerdictValid Verdict = iota
	VerdictRevoked
	VerdictUnreachable
)

// Checker asks the coordinator whether the lease is still good.
type Checker func(ctx context.Context) (Verdict, error)

// Watchdog tears the tunnel down when authorisation ends.
type Watchdog struct {
	check       Checker
	interval    time.Duration
	localExpiry time.Duration
	onTeardown  func(reason string)
}

// New builds a watchdog. interval is how often the coordinator is asked;
// localExpiry is how long the tunnel may survive without a positive answer.
func New(check Checker, interval, localExpiry time.Duration, onTeardown func(reason string)) *Watchdog {
	return &Watchdog{check: check, interval: interval, localExpiry: localExpiry, onTeardown: onTeardown}
}

// Run polls until ctx is done or the lease ends.
//
// It fails closed. A coordinator outage never extends authorisation - it
// only delays the explicit answer until local expiry runs out. The opposite
// choice, treating "cannot ask" as "still allowed", would mean anyone who
// can black-hole the coordinator gets an unrevokable session.
func (w *Watchdog) Run(ctx context.Context) {
	lastValid := time.Now()
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		verdict, _ := w.check(ctx)
		switch verdict {
		case VerdictValid:
			lastValid = time.Now()
		case VerdictRevoked:
			w.onTeardown("authorisation revoked")
			return
		case VerdictUnreachable:
			// Deliberately no lastValid update.
		}

		if time.Since(lastValid) > w.localExpiry {
			w.onTeardown("lease expired locally")
			return
		}
	}
}
