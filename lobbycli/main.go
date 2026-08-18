// Command lobbycli is the throwaway test client for the network core.
//
// It is not the product. It exists so a human can drive the relay by hand -
// prove a handshake completes, measure round-trip time, check that a room
// is isolated - before the real desktop client exists.
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"finallobby/protocol/crypto"
	"finallobby/protocol/wire"
)

func main() {
	relayAddr := flag.String("relay", "", "relay host:port, e.g. 87.107.110.199:443")
	pubHex := flag.String("relay-key", "", "hex-encoded relay static public key")
	ticket := flag.String("ticket", "", "session ticket (dev mode accepts roomID|virtualIP)")
	count := flag.Int("count", 3, "how many handshake attempts to make")
	timeout := flag.Duration("timeout", 5*time.Second, "per-attempt reply timeout")
	flag.Parse()

	if *relayAddr == "" || *pubHex == "" || *ticket == "" {
		flag.Usage()
		os.Exit(2)
	}
	pub, err := hex.DecodeString(*pubHex)
	if err != nil || len(pub) != 32 {
		fmt.Fprintln(os.Stderr, "relay-key must be 64 hex characters")
		os.Exit(2)
	}

	var ok int
	for i := 1; i <= *count; i++ {
		rtt, accept, err := handshake(*relayAddr, pub, []byte(*ticket), *timeout)
		if err != nil {
			fmt.Printf("attempt %d: FAILED after %v: %v\n", i, rtt.Round(time.Millisecond), err)
			continue
		}
		ok++
		fmt.Printf("attempt %d: ok in %v - session %d, virtual IP %s, room %q\n",
			i, rtt.Round(time.Millisecond), accept.SessionID, accept.VirtualIP, accept.RoomID)
	}

	fmt.Printf("\n%d/%d handshakes succeeded against %s\n", ok, *count, *relayAddr)
	if ok == 0 {
		os.Exit(1)
	}
}

func handshake(addr string, relayPub, ticket []byte, timeout time.Duration) (time.Duration, wire.Accept, error) {
	start := time.Now()

	conn, err := net.Dial("udp", addr)
	if err != nil {
		return time.Since(start), wire.Accept{}, err
	}
	defer conn.Close()

	msg1, finish, err := crypto.ClientHandshake(relayPub, ticket)
	if err != nil {
		return time.Since(start), wire.Accept{}, err
	}
	hdr := make([]byte, wire.HeaderSize)
	wire.EncodeHeader(hdr, wire.Header{Version: wire.ProtocolVersion, Type: wire.TypeHandshakeInit})
	if _, err := conn.Write(append(hdr, msg1...)); err != nil {
		return time.Since(start), wire.Accept{}, err
	}

	buf := make([]byte, 2048)
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	n, err := conn.Read(buf)
	if err != nil {
		return time.Since(start), wire.Accept{}, fmt.Errorf("no reply: %w", err)
	}
	rtt := time.Since(start)

	h, err := wire.DecodeHeader(buf[:n])
	if err != nil {
		return rtt, wire.Accept{}, err
	}
	if h.Type != wire.TypeHandshakeResp {
		return rtt, wire.Accept{}, fmt.Errorf("unexpected reply type %d", h.Type)
	}
	_, payload, err := finish(buf[wire.HeaderSize:n])
	if err != nil {
		return rtt, wire.Accept{}, err
	}
	accept, err := wire.DecodeAccept(payload)
	if err != nil {
		return rtt, wire.Accept{}, err
	}
	return rtt, accept, nil
}
