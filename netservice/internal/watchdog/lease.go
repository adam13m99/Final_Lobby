// Package watchdog enforces network authorisation locally.
//
// It runs inside the Windows service, not the desktop app, so a closed or
// tampered-with UI cannot keep a revoked player connected. The service owns
// the adapter; the UI only asks it for things.
package watchdog

import (
	"context"
	"io"
	"log/slog"
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
	log         *slog.Logger
	onTeardown  func(reason string)
}

// New builds a watchdog. interval is how often the coordinator is asked;
// localExpiry is how long the tunnel may survive without a positive answer.
//
// log may be nil in tests. It is not optional anywhere real: an answer this
// thing cannot understand is the only warning that exists before a match ends
// three minutes later, and for weeks there was nowhere for that warning to go
// (D77).
func New(check Checker, interval, localExpiry time.Duration, log *slog.Logger, onTeardown func(reason string)) *Watchdog {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Watchdog{check: check, interval: interval, localExpiry: localExpiry, log: log, onTeardown: onTeardown}
}

// Run polls until ctx is done or the lease ends.
//
// It fails closed. A coordinator outage never extends authorisation - it
// only delays the explicit answer until local expiry runs out. The opposite
// choice, treating "cannot ask" as "still allowed", would mean anyone who
// can black-hole the coordinator gets an unrevokable session.
func (w *Watchdog) Run(ctx context.Context) {
	lastValid := time.Now()
	var lastErr error
	unanswered := 0

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		verdict, err := w.check(ctx)
		switch verdict {
		case VerdictValid:
			if unanswered > 0 {
				w.log.Info("lease check recovered", "missed", unanswered)
			}
			lastValid = time.Now()
			lastErr = nil
			unanswered = 0
		case VerdictRevoked:
			w.log.Info("lease revoked by the coordinator")
			w.onTeardown("authorisation revoked")
			return
		case VerdictUnreachable:
			// Deliberately no lastValid update.
			//
			// It is said out loud instead. This branch cannot tell a
			// coordinator that is down from one that is refusing us, and
			// treating both as "cannot tell" is right - but staying silent
			// about it was not. A misconfigured lease check looked exactly
			// like a bad network for three minutes and then reported itself
			// as an expiry, which is the symptom and not the cause.
			lastErr = err
			unanswered++
			w.log.Warn("lease check did not answer",
				"err", err, "in a row", unanswered,
				"tearing down in", (w.localExpiry - time.Since(lastValid)).Round(time.Second))
		}

		if time.Since(lastValid) > w.localExpiry {
			w.log.Error("lease expired locally, tearing the tunnel down",
				"last good", time.Since(lastValid).Round(time.Second), "err", lastErr)
			w.onTeardown(expiryReason(lastErr))
			return
		}
	}
}

// expiryReason names the cause when there is one.
//
// "lease expired locally" on its own is a true statement about our own state
// and no help to anybody: it describes the timer running out, not what stopped
// the answers arriving. The one that actually happened - the coordinator
// answering 401 because the route wanted a session the service does not have
// - was invisible in it.
func expiryReason(lastErr error) string {
	if lastErr == nil {
		return "lease expired locally"
	}
	return "lease expired locally: " + lastErr.Error()
}
