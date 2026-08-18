//go:build windows

package ipc_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"finallobby/protocol/ipc"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// waitForPipe blocks until the service pipe answers, so tests do not race
// the listener's startup.
func waitForPipe(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		_, err := ipc.Call(ctx, ipc.Request{Op: ipc.OpPing})
		cancel()
		if err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("pipe never became available")
}

// drain empties a channel of anything buffered so far.
func drain(ch chan ipc.Request) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}
