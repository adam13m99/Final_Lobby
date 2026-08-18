package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"finallobby/protocol/crypto"
	"finallobby/relay/internal/server"
)

func main() {
	listen := flag.String("listen", "0.0.0.0:443", "UDP listen address (UDP only - TCP 443 belongs to another service)")
	keyHex := flag.String("static-key", "", "hex-encoded relay static private key, or @/path/to/file")
	coordinator := flag.String("coordinator", "http://127.0.0.1:7001", "coordinator base URL")
	allowMulticast := flag.Bool("allow-multicast", false, "re-enable room-scoped multicast fanout (see docs/decisions.md D1)")
	queueDepth := flag.Int("queue-depth", 256, "per-peer send queue depth in packets")
	devTickets := flag.Bool("dev-unsigned-tickets", false, "TESTING ONLY: accept unsigned roomID|virtualIP tickets")
	genKey := flag.Bool("genkey", false, "print a fresh static keypair and exit")
	flag.Parse()

	if *genKey {
		pub, priv, err := crypto.GenerateStaticKeypair()
		if err != nil {
			slog.Error("key generation failed", "err", err)
			os.Exit(1)
		}
		fmt.Printf("private %s\npublic  %s\n", hex.EncodeToString(priv), hex.EncodeToString(pub))
		return
	}

	priv, err := loadStaticKey(*keyHex)
	if err != nil {
		slog.Error("static key unusable", "err", err)
		os.Exit(1)
	}

	validate := newCoordinatorValidator(*coordinator)
	if *devTickets {
		slog.Warn("ACCEPTING UNSIGNED TICKETS - testing mode, never run this in production")
		validate = devValidator
	}

	srv, err := server.New(server.Config{
		Listen:         *listen,
		StaticPriv:     priv,
		AllowMulticast: *allowMulticast,
		QueueDepth:     *queueDepth,
		ValidateTicket: validate,
	})
	if err != nil {
		slog.Error("relay failed to start", "err", err)
		os.Exit(1)
	}

	pub, err := crypto.PublicFromPrivate(priv)
	if err != nil {
		slog.Error("could not derive public key", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.Info("relay listening",
		"addr", srv.LocalAddr().String(),
		"public_key", hex.EncodeToString(pub),
		"multicast", *allowMulticast,
	)
	if err := srv.Serve(ctx); err != nil {
		slog.Error("relay stopped", "err", err)
		os.Exit(1)
	}
	slog.Info("relay shut down cleanly")
}

// loadStaticKey accepts either hex on the command line or @path to read it
// from a file. The file form is what systemd uses, so the key never appears
// in the process list.
func loadStaticKey(spec string) ([]byte, error) {
	if spec == "" {
		return nil, fmt.Errorf("-static-key is required (use -genkey to create one)")
	}
	text := spec
	if strings.HasPrefix(spec, "@") {
		b, err := os.ReadFile(strings.TrimPrefix(spec, "@"))
		if err != nil {
			return nil, err
		}
		text = string(b)
	}
	priv, err := hex.DecodeString(strings.TrimSpace(text))
	if err != nil {
		return nil, fmt.Errorf("static key must be hex: %w", err)
	}
	if len(priv) != 32 {
		return nil, fmt.Errorf("static key must be 32 bytes (64 hex characters), got %d", len(priv))
	}
	return priv, nil
}
