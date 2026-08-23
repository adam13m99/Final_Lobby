package sendq_test

import (
	"context"
	"testing"
	"time"

	"lobbybaz/relay/internal/sendq"
)

func TestPreservesOrder(t *testing.T) {
	q := sendq.New(4)
	defer q.Close()
	for _, b := range []byte{1, 2, 3} {
		if dropped := q.Push([]byte{b}); dropped {
			t.Fatalf("unexpected drop pushing %d", b)
		}
	}
	ctx := context.Background()
	for _, want := range []byte{1, 2, 3} {
		got, err := q.Pop(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got[0] != want {
			t.Fatalf("got %d, want %d - ordering broken", got[0], want)
		}
	}
}

func TestDropsOldestWhenFull(t *testing.T) {
	q := sendq.New(2)
	defer q.Close()
	q.Push([]byte{1})
	q.Push([]byte{2})
	if dropped := q.Push([]byte{3}); !dropped {
		t.Fatal("expected a drop when pushing into a full queue")
	}
	if q.Drops() != 1 {
		t.Fatalf("Drops() = %d, want 1", q.Drops())
	}
	// Oldest (1) should be gone; 2 then 3 remain.
	ctx := context.Background()
	first, _ := q.Pop(ctx)
	second, _ := q.Pop(ctx)
	if first[0] != 2 || second[0] != 3 {
		t.Fatalf("got %d,%d want 2,3 - wrong entry evicted", first[0], second[0])
	}
}

func TestPushCopiesCallerBuffer(t *testing.T) {
	q := sendq.New(2)
	defer q.Close()
	buf := []byte{42}
	q.Push(buf)
	buf[0] = 99 // caller reuses its buffer
	got, err := q.Pop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 42 {
		t.Fatalf("got %d, want 42 - queue aliased the caller's buffer", got[0])
	}
}

func TestPopUnblocksOnContextCancel(t *testing.T) {
	q := sendq.New(2)
	defer q.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := q.Pop(ctx); err == nil {
		t.Fatal("expected error when context expires")
	}
}
