//go:build windows

package ipc_test

import (
	"context"
	"testing"
	"time"

	"finallobby/protocol/ipc"
)

func TestPipeRoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handled := make(chan ipc.Request, 4)
	go func() {
		_ = ipc.Listen(ctx, func(_ context.Context, req ipc.Request) ipc.Response {
			handled <- req
			return ipc.Response{State: "connected", VirtualIP: "10.87.0.2", Connected: true}
		}, testLogger())
	}()

	// Give the listener a moment to bind the pipe. The readiness ping is
	// handled too, so drain it before asserting on the real request.
	waitForPipe(t)
	drain(handled)

	resp, err := ipc.Call(ctx, ipc.Request{Op: ipc.OpStatus})
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
	// Nothing is listening in this test; the error must say so plainly
	// rather than hanging, because this is what every user sees when they
	// forget to install the service.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := ipc.Call(ctx, ipc.Request{Op: ipc.OpPing}); err == nil {
		t.Fatal("expected an error when no service is listening")
	}
}

func TestHandlerErrorsReachTheClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = ipc.Listen(ctx, func(_ context.Context, _ ipc.Request) ipc.Response {
			return ipc.Response{Err: "no room joined"}
		}, testLogger())
	}()
	waitForPipe(t)

	resp, err := ipc.Call(ctx, ipc.Request{Op: ipc.OpLaunch})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Err != "no room joined" {
		t.Fatalf("error did not survive the round trip: %+v", resp)
	}
}
