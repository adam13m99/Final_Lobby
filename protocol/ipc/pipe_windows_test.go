//go:build windows

package ipc_test

import (
	"context"
	"testing"
	"time"

	"lobbybaz/protocol/ipc"
)

func TestPipeRoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pipe := uniquePipe()
	handled := make(chan ipc.Request, 4)
	go func() {
		_ = ipc.ListenOn(ctx, pipe, func(_ context.Context, req ipc.Request) ipc.Response {
			handled <- req
			return ipc.Response{State: "connected", VirtualIP: "10.87.0.2", Connected: true}
		}, testLogger())
	}()

	// The readiness ping is handled too, so drain it before asserting on
	// the real request.
	waitForPipe(t, pipe)
	drain(handled)

	resp, err := ipc.CallOn(ctx, pipe, ipc.Request{Op: ipc.OpStatus})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.State != "connected" || resp.VirtualIP != "10.87.0.2" || !resp.Connected {
		t.Fatalf("unexpected response: %+v", resp)
	}
	select {
	case got := <-handled:
		if got.Op != ipc.OpStatus {
			t.Fatalf("handler saw op %q", got.Op)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler never ran")
	}
}

func TestCallFailsCleanlyWhenServiceAbsent(t *testing.T) {
	// Nothing listens on this name. The error must say so plainly rather
	// than hanging, because this is what every user sees when they forget
	// to install the service.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := ipc.CallOn(ctx, uniquePipe(), ipc.Request{Op: ipc.OpPing}); err == nil {
		t.Fatal("expected an error when no service is listening")
	}
}

func TestHandlerErrorsReachTheClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pipe := uniquePipe()
	go func() {
		_ = ipc.ListenOn(ctx, pipe, func(_ context.Context, _ ipc.Request) ipc.Response {
			return ipc.Response{Err: "no room joined"}
		}, testLogger())
	}()
	waitForPipe(t, pipe)

	resp, err := ipc.CallOn(ctx, pipe, ipc.Request{Op: ipc.OpLaunch})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Err != "no room joined" {
		t.Fatalf("error did not survive the round trip: %+v", resp)
	}
}

func TestSecondListenerOnTheSamePipeSaysServiceAlreadyRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pipe := uniquePipe()
	go func() {
		_ = ipc.ListenOn(ctx, pipe, func(context.Context, ipc.Request) ipc.Response {
			return ipc.Response{}
		}, testLogger())
	}()
	waitForPipe(t, pipe)

	// Windows reports this as "Access is denied", which sends people
	// looking for a permissions problem that does not exist.
	err := ipc.ListenOn(ctx, pipe, func(context.Context, ipc.Request) ipc.Response {
		return ipc.Response{}
	}, testLogger())
	if err == nil {
		t.Fatal("a second listener bound the same pipe")
	}
	if !contains(err.Error(), "already running") {
		t.Fatalf("error was %q; it should say the service is already running", err)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) &&
		(haystack == needle || len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
