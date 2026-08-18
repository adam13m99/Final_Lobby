//go:build windows

// Command netservice is the Windows service that owns the virtual network
// adapter and the tunnel.
//
// It is a service rather than something the desktop app starts, because
// creating a network adapter needs Administrator rights. Installing once, at
// install time, is what lets a player join a room with no UAC prompt - the
// most visible everyday difference from the predecessor platform.
//
//	netservice install     register and start the service (needs admin)
//	netservice uninstall   stop and remove it (needs admin)
//	netservice run         run in the foreground, for development
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"

	"finallobby/netservice/internal/agent"
	"finallobby/protocol/ipc"
)

const (
	serviceName = "FinalLobbyNet"
	displayName = "Final Lobby Network Service"
	description = "Manages the Final Lobby virtual network adapter and relay tunnel."
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cmd := "auto"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	switch cmd {
	case "install":
		if err := install(); err != nil {
			fmt.Fprintln(os.Stderr, "install failed:", err)
			os.Exit(1)
		}
		fmt.Println("Final Lobby network service installed and started.")
	case "uninstall":
		if err := uninstall(); err != nil {
			fmt.Fprintln(os.Stderr, "uninstall failed:", err)
			os.Exit(1)
		}
		fmt.Println("Final Lobby network service removed.")
	case "run":
		runForeground(log)
	case "auto":
		isService, err := svc.IsWindowsService()
		if err != nil {
			log.Error("cannot tell whether we are a service", "err", err)
			os.Exit(1)
		}
		if isService {
			runAsService(log)
			return
		}
		runForeground(log)
	default:
		fmt.Fprintf(os.Stderr, "usage: %s [install|uninstall|run]\n", filepath.Base(os.Args[0]))
		os.Exit(2)
	}
}

// runForeground is the development path: no service manager, Ctrl-C to stop.
func runForeground(log *slog.Logger) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a := agent.New(log)
	defer a.Disconnect("service stopping")

	log.Info("running in the foreground", "pipe", ipc.PipeName)
	if err := ipc.Listen(ctx, a.Handle, log); err != nil {
		log.Error("ipc listener stopped", "err", err)
		os.Exit(1)
	}
}

// --- Windows service plumbing ------------------------------------------

type service struct {
	log *slog.Logger
}

func (s *service) Execute(_ []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a := agent.New(s.log)
	go func() {
		if err := ipc.Listen(ctx, a.Handle, s.log); err != nil {
			s.log.Error("ipc listener stopped", "err", err)
		}
	}()

	changes <- svc.Status{State: svc.Running, Accepts: accepted}

	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			changes <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			changes <- svc.Status{State: svc.StopPending}
			// Tearing the adapter down on stop matters: a leftover adapter
			// with a stale address confuses the next session and shows up in
			// the player's network settings as a dead connection.
			a.Disconnect("service stopping")
			cancel()
			return false, 0
		}
	}
	return false, 0
}

func runAsService(log *slog.Logger) {
	if elog, err := eventlog.Open(serviceName); err == nil {
		defer elog.Close()
		_ = elog.Info(1, "Final Lobby network service starting")
	}
	if err := svc.Run(serviceName, &service{log: log}); err != nil {
		log.Error("service failed", "err", err)
		os.Exit(1)
	}
}

func install() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return err
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager (are you an Administrator?): %w", err)
	}
	defer m.Disconnect()

	if existing, err := m.OpenService(serviceName); err == nil {
		existing.Close()
		return fmt.Errorf("%s is already installed; run uninstall first", serviceName)
	}

	s, err := m.CreateService(serviceName, exe, mgr.Config{
		DisplayName:  displayName,
		Description:  description,
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
	})
	if err != nil {
		return err
	}
	defer s.Close()

	// Best effort: the service works without an event-log source, it just
	// logs less usefully.
	_ = eventlog.InstallAsEventCreate(serviceName,
		eventlog.Error|eventlog.Warning|eventlog.Info)

	if err := s.Start(); err != nil {
		return fmt.Errorf("service created but would not start: %w", err)
	}
	return nil
}

func uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the service manager (are you an Administrator?): %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("%s is not installed", serviceName)
	}
	defer s.Close()

	if status, err := s.Control(svc.Stop); err == nil {
		deadline := time.Now().Add(15 * time.Second)
		for status.State != svc.Stopped && time.Now().Before(deadline) {
			time.Sleep(300 * time.Millisecond)
			if status, err = s.Query(); err != nil {
				break
			}
		}
	}
	if err := s.Delete(); err != nil {
		return err
	}
	_ = eventlog.Remove(serviceName)
	return nil
}
