//go:build windows

package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// pipeSDDL restricts the pipe to SYSTEM, local Administrators, and
// interactive users - the person actually sitting at the machine. Remote
// users and service accounts get nothing.
//
// Interactive users are allowed because the desktop client runs as the
// player, not as an administrator: the whole point of the service is that
// joining a room shows no UAC prompt.
const pipeSDDL = "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;IU)"

// Handler answers one request.
type Handler func(ctx context.Context, req Request) Response

// Listen serves the service's named pipe until ctx is cancelled.
func Listen(ctx context.Context, h Handler, log *slog.Logger) error {
	return ListenOn(ctx, PipeName, h, log)
}

// ListenOn is Listen on an arbitrary pipe name. Tests use it with a unique
// name so they neither collide with an installed service nor depend on one
// being absent.
func ListenOn(ctx context.Context, name string, h Handler, log *slog.Logger) error {
	l, err := winio.ListenPipe(name, &winio.PipeConfig{
		SecurityDescriptor: pipeSDDL,
		MessageMode:        false,
		InputBufferSize:    16 << 10,
		OutputBufferSize:   16 << 10,
	})
	if err != nil {
		// Windows reports "Access is denied" when a pipe of this name
		// already exists and belongs to someone else, which in practice
		// always means a second copy of the service is running. Saying that
		// plainly saves the next person a confused half hour.
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return fmt.Errorf("ipc: the LobbyBaz service is already running (pipe %s is taken)", name)
		}
		return fmt.Errorf("ipc: listen %s: %w", name, err)
	}
	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()

	log.Info("ipc listening", "pipe", name)
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Warn("ipc accept failed", "err", err)
			continue
		}
		go serveConn(ctx, conn, h, log)
	}
}

func serveConn(ctx context.Context, conn net.Conn, h Handler, log *slog.Logger) {
	defer conn.Close()
	// A client that connects and says nothing must not hold a goroutine
	// forever.
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 16<<10), 16<<10)
	enc := json.NewEncoder(conn)

	for scanner.Scan() {
		var req Request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			_ = enc.Encode(Response{Err: "malformed request"})
			return
		}
		resp := h(ctx, req)
		if err := enc.Encode(resp); err != nil {
			return
		}
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}
	if err := scanner.Err(); err != nil {
		log.Debug("ipc connection ended", "err", err)
	}
}

// Call sends one request to a running service and returns its reply.
func Call(ctx context.Context, req Request) (Response, error) {
	return CallOn(ctx, PipeName, req)
}

// CallOn is Call against an arbitrary pipe name.
func CallOn(ctx context.Context, name string, req Request) (Response, error) {
	timeout := 10 * time.Second
	conn, err := winio.DialPipeContext(ctx, name)
	if err != nil {
		return Response{}, fmt.Errorf("ipc: the LobbyBaz service is not running: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}
