//go:build windows

package ipc_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"finallobby/protocol/ipc"
)

var pipeSeq atomic.Int64

// uniquePipe gives each test its own pipe, so the suite passes whether or
// not the real service is installed and running on this machine. Using the
// production pipe name here made the tests fail the moment the service was
// installed, which is exactly when they most need to work.
func uniquePipe() string {
	return fmt.Sprintf(`\\.\pipe\finallobby-test-%d-%d`, os.Getpid(), pipeSeq.Add(1))
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// waitForPipe blocks until the named pipe answers, so tests do not race the
// listener's startup.
func waitForPipe(t *testing.T, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		_, err := ipc.CallOn(ctx, name, ipc.Request{Op: ipc.OpPing})
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
