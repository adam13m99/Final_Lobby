// Package sendq provides a bounded, drop-oldest packet queue.
//
// Each connected peer owns one Queue drained by exactly one long-lived
// writer goroutine. Goroutine count therefore scales with the number of
// players, never with packet rate, and packet ordering per peer is
// preserved. Game traffic prefers a dropped packet over a delayed one, so
// overflow evicts the oldest entry rather than blocking the producer.
package sendq

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// ErrClosed is returned by Pop after Close.
var ErrClosed = errors.New("sendq: queue closed")

// Queue is a fixed-capacity FIFO of packet buffers. Safe for concurrent use.
type Queue struct {
	mu     sync.Mutex
	notify chan struct{}
	items  [][]byte
	cap    int
	closed bool
	drops  atomic.Uint64
}

// New returns a Queue holding at most capacity packets.
func New(capacity int) *Queue {
	if capacity < 1 {
		capacity = 1
	}
	return &Queue{
		notify: make(chan struct{}, 1),
		items:  make([][]byte, 0, capacity),
		cap:    capacity,
	}
}

// Push copies pkt into the queue. It reports whether a packet was dropped
// to make room. Push never blocks.
func (q *Queue) Push(pkt []byte) (dropped bool) {
	cp := make([]byte, len(pkt))
	copy(cp, pkt)

	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return false
	}
	if len(q.items) == q.cap {
		q.items = q.items[1:] // evict oldest
		dropped = true
		q.drops.Add(1)
	}
	q.items = append(q.items, cp)
	q.mu.Unlock()

	select {
	case q.notify <- struct{}{}:
	default: // a wakeup is already pending
	}
	return dropped
}

// Pop returns the oldest packet, blocking until one arrives, the context is
// done, or the queue is closed.
func (q *Queue) Pop(ctx context.Context) ([]byte, error) {
	for {
		q.mu.Lock()
		if q.closed {
			q.mu.Unlock()
			return nil, ErrClosed
		}
		if len(q.items) > 0 {
			item := q.items[0]
			q.items = q.items[1:]
			q.mu.Unlock()
			return item, nil
		}
		q.mu.Unlock()

		select {
		case <-q.notify:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Drops returns the cumulative number of packets evicted. Sustained drops
// are the earliest signal of a peer with a failing connection.
func (q *Queue) Drops() uint64 { return q.drops.Load() }

// Close releases waiters. Further Push calls are no-ops.
func (q *Queue) Close() {
	q.mu.Lock()
	q.closed = true
	q.items = nil
	q.mu.Unlock()
	select {
	case q.notify <- struct{}{}:
	default:
	}
}
