package api

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// Limiter is a per-client token bucket.
//
// The product owner asked for this explicitly: endpoints that create rooms,
// join them, or issue tickets are cheap for a client to call and expensive
// for us to serve, and a room-join loop is the obvious way to grief the
// platform.
type Limiter struct {
	rate  float64 // tokens added per second
	burst float64 // maximum tokens held

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewLimiter allows burst requests immediately and rate per second after.
func NewLimiter(rate, burst float64) *Limiter {
	return &Limiter{rate: rate, burst: burst, buckets: make(map[string]*bucket)}
}

// Allow reports whether a request from key may proceed.
func (l *Limiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &bucket{tokens: l.burst - 1, last: now}
		return true
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Purge drops buckets untouched for a while, so the map cannot grow without
// bound as clients come and go.
func (l *Limiter) Purge(now time.Time, idle time.Duration) {
	l.mu.Lock()
	for k, b := range l.buckets {
		if now.Sub(b.last) > idle {
			delete(l.buckets, k)
		}
	}
	l.mu.Unlock()
}

// clientKey identifies the caller for rate-limiting. The remote address is
// the only thing a client cannot trivially forge here; X-Forwarded-For is
// deliberately ignored because nothing in front of this is trusted yet.
func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
