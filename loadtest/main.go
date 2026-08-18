// Command loadtest drives a relay with synthetic peers.
//
// It answers two questions the two-PC physical test cannot:
//
//	1. Does the relay hold at the target concurrent player count?
//	2. What throughput does that require?
//
// Each synthetic peer completes a real Noise handshake and sends real
// encrypted datagrams, so what is measured is the relay's actual work, not a
// simplified stand-in.
//
// Usage:
//
//	loadtest -relay 127.0.0.1:9443 -relay-pub <hex> -peers 500 \
//	         -pps 60 -packet-size 200 -duration 120s
package main

import (
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"net/netip"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"finallobby/protocol/crypto"
	"finallobby/protocol/wire"
)

const (
	playersPerRoom = 10
	// ipv4HeaderLen is the inner header the relay parses to route.
	ipv4HeaderLen = 20
	// timestampLen is the nanosecond send time we embed after the header.
	timestampLen = 8
)

type stats struct {
	sent      atomic.Uint64
	received  atomic.Uint64
	handshake atomic.Uint64
	failed    atomic.Uint64

	mu        sync.Mutex
	latencies []time.Duration
}

func (s *stats) record(d time.Duration) {
	s.mu.Lock()
	// Cap the sample so a long soak does not exhaust memory; a few hundred
	// thousand samples is far more than percentiles need.
	if len(s.latencies) < 500_000 {
		s.latencies = append(s.latencies, d)
	}
	s.mu.Unlock()
}

func main() {
	relayAddr := flag.String("relay", "127.0.0.1:9443", "relay UDP address")
	relayPub := flag.String("relay-pub", "", "hex relay static public key")
	peers := flag.Int("peers", 500, "number of synthetic peers")
	pps := flag.Int("pps", 60, "packets per second per peer")
	size := flag.Int("packet-size", 200, "inner packet size in bytes")
	duration := flag.Duration("duration", 60*time.Second, "test duration")
	rampUp := flag.Duration("ramp-up", 10*time.Second, "spread handshakes over this window")
	flag.Parse()

	pub, err := hex.DecodeString(*relayPub)
	if err != nil || len(pub) != 32 {
		fmt.Fprintln(os.Stderr, "-relay-pub must be 64 hex characters")
		os.Exit(2)
	}
	if *size < ipv4HeaderLen+timestampLen {
		fmt.Fprintf(os.Stderr, "-packet-size must be at least %d\n", ipv4HeaderLen+timestampLen)
		os.Exit(2)
	}

	rooms := (*peers + playersPerRoom - 1) / playersPerRoom
	fmt.Printf("relay      %s\n", *relayAddr)
	fmt.Printf("peers      %d across %d rooms\n", *peers, rooms)
	fmt.Printf("rate       %d pps per peer, %d byte packets\n", *pps, *size)
	fmt.Printf("offered    %.1f Mbps aggregate, %d packets/sec total\n",
		float64(*peers**pps**size*8)/1e6, *peers**pps)
	fmt.Printf("duration   %s (after a %s ramp-up)\n\n", *duration, *rampUp)

	st := &stats{}
	stop := make(chan struct{})
	var wg sync.WaitGroup

	start := time.Now()
	for i := 0; i < *peers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Spread handshakes out: 1500 simultaneous handshakes would
			// measure a thundering herd, not steady-state capacity.
			if *rampUp > 0 {
				delay := time.Duration(float64(*rampUp) * float64(idx) / float64(*peers))
				select {
				case <-time.After(delay):
				case <-stop:
					return
				}
			}
			runPeer(idx, *relayAddr, pub, *pps, *size, st, stop)
		}(i)
	}

	// Let everyone connect, then measure only the steady state.
	time.Sleep(*rampUp + 2*time.Second)
	connected := st.handshake.Load()
	fmt.Printf("connected  %d/%d peers in %s\n", connected, *peers, time.Since(start).Round(time.Millisecond))
	if failed := st.failed.Load(); failed > 0 {
		fmt.Printf("failed     %d peers could not connect\n", failed)
	}
	fmt.Println("measuring...")

	measureStart := time.Now()
	sentAtStart := st.sent.Load()
	recvAtStart := st.received.Load()

	time.Sleep(*duration)
	close(stop)

	elapsed := time.Since(measureStart)
	sent := st.sent.Load() - sentAtStart
	recv := st.received.Load() - recvAtStart

	wg.Wait()
	report(st, sent, recv, elapsed, *size)
}

// runPeer handshakes, then sends to a partner in the same room and reads
// whatever the relay forwards back.
func runPeer(idx int, relayAddr string, relayPub []byte, pps, size int, st *stats, stop <-chan struct{}) {
	room := idx / playersPerRoom
	slot := idx % playersPerRoom

	self, err := slotIP(room, slot)
	if err != nil {
		st.failed.Add(1)
		return
	}
	// Send to the next slot around the room, so every peer both sends and
	// receives - which is what a real match looks like.
	partner, err := slotIP(room, (slot+1)%playersPerRoom)
	if err != nil {
		st.failed.Add(1)
		return
	}
	ticket := fmt.Sprintf("loadtest-room-%d|%s", room, self)

	conn, err := net.Dial("udp", relayAddr)
	if err != nil {
		st.failed.Add(1)
		return
	}
	defer conn.Close()
	udp := conn.(*net.UDPConn)
	// Without this the harness drops packets in its own kernel buffer and
	// reports them as relay loss.
	_ = udp.SetReadBuffer(1 << 20)
	_ = udp.SetWriteBuffer(1 << 20)

	sess, accept, err := handshake(udp, relayPub, []byte(ticket))
	if err != nil {
		st.failed.Add(1)
		return
	}
	st.handshake.Add(1)

	var wg sync.WaitGroup
	wg.Add(2)

	// Receiver.
	go func() {
		defer wg.Done()
		buf := make([]byte, 2048)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = udp.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			n, err := udp.Read(buf)
			if err != nil {
				continue
			}
			_, inner, err := sess.Open(buf[:n])
			if err != nil || len(inner) < ipv4HeaderLen+timestampLen {
				continue
			}
			sentAt := int64(binary.BigEndian.Uint64(inner[ipv4HeaderLen:]))
			st.received.Add(1)
			st.record(time.Duration(time.Now().UnixNano() - sentAt))
		}
	}()

	// Sender.
	go func() {
		defer wg.Done()
		interval := time.Second / time.Duration(pps)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		pkt := make([]byte, size)
		pkt[0] = 0x45
		s4, d4 := self.As4(), partner.As4()
		copy(pkt[12:16], s4[:])
		copy(pkt[16:20], d4[:])
		// Fill the rest with noise so compression cannot flatter the result.
		for i := ipv4HeaderLen + timestampLen; i < size; i++ {
			pkt[i] = byte(rand.Intn(256))
		}

		var seq uint64
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
			binary.BigEndian.PutUint64(pkt[ipv4HeaderLen:], uint64(time.Now().UnixNano()))
			seq++
			out, err := sess.Seal(nil, wire.Header{
				Version:   wire.ProtocolVersion,
				Type:      wire.TypeData,
				SessionID: accept.SessionID,
				Sequence:  seq,
			}, pkt)
			if err != nil {
				return
			}
			if _, err := udp.Write(out); err != nil {
				return
			}
			st.sent.Add(1)
		}
	}()

	wg.Wait()
}

func handshake(udp *net.UDPConn, relayPub, ticket []byte) (*crypto.Session, wire.Accept, error) {
	msg1, finish, err := crypto.ClientHandshake(relayPub, ticket)
	if err != nil {
		return nil, wire.Accept{}, err
	}
	out := make([]byte, wire.HeaderSize, wire.HeaderSize+len(msg1))
	wire.EncodeHeader(out, wire.Header{Version: wire.ProtocolVersion, Type: wire.TypeHandshakeInit})
	if _, err := udp.Write(append(out, msg1...)); err != nil {
		return nil, wire.Accept{}, err
	}
	buf := make([]byte, 2048)
	_ = udp.SetReadDeadline(time.Now().Add(10 * time.Second))
	n, err := udp.Read(buf)
	if err != nil {
		return nil, wire.Accept{}, err
	}
	h, err := wire.DecodeHeader(buf[:n])
	if err != nil || h.Type != wire.TypeHandshakeResp {
		return nil, wire.Accept{}, fmt.Errorf("unexpected handshake reply")
	}
	sess, payload, err := finish(buf[wire.HeaderSize:n])
	if err != nil {
		return nil, wire.Accept{}, err
	}
	accept, err := wire.DecodeAccept(payload)
	if err != nil {
		return nil, wire.Accept{}, err
	}
	return sess, accept, nil
}

// slotIP mirrors coordinator/internal/ipam: 10.87.0.0/16 carved into /28s,
// players at offsets 2..11. Duplicated rather than imported so the harness
// stays independent of the coordinator module.
func slotIP(room, slot int) (netip.Addr, error) {
	if room < 0 || room >= 4096 || slot < 0 || slot >= playersPerRoom {
		return netip.Addr{}, fmt.Errorf("room %d slot %d out of range", room, slot)
	}
	third := byte(room >> 4)
	fourth := byte((room&0x0F)<<4) + byte(2+slot)
	return netip.AddrFrom4([4]byte{10, 87, third, fourth}), nil
}

func report(st *stats, sent, recv uint64, elapsed time.Duration, size int) {
	st.mu.Lock()
	lat := append([]time.Duration(nil), st.latencies...)
	st.mu.Unlock()
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })

	pct := func(p float64) time.Duration {
		if len(lat) == 0 {
			return 0
		}
		i := int(float64(len(lat)-1) * p)
		return lat[i]
	}

	// recv can exceed sent by a few packets: the measurement window opens
	// on the send counter, so packets already in flight land inside it.
	// Subtracting unsigned counters directly would underflow into nonsense.
	var lossPct float64
	if sent > 0 && sent > recv {
		lossPct = 100 * float64(sent-recv) / float64(sent)
	}
	secs := elapsed.Seconds()

	fmt.Printf("\n=== results over %s ===\n", elapsed.Round(time.Millisecond))
	fmt.Printf("connected peers   %d\n", st.handshake.Load())
	fmt.Printf("handshake failed  %d\n", st.failed.Load())
	fmt.Printf("packets sent      %d (%.0f/sec)\n", sent, float64(sent)/secs)
	fmt.Printf("packets received  %d (%.0f/sec)\n", recv, float64(recv)/secs)
	fmt.Printf("loss              %.3f%%\n", lossPct)
	fmt.Printf("latency p50       %v\n", pct(0.50).Round(time.Microsecond))
	fmt.Printf("latency p95       %v\n", pct(0.95).Round(time.Microsecond))
	fmt.Printf("latency p99       %v\n", pct(0.99).Round(time.Microsecond))
	fmt.Printf("relay ingress     %.1f Mbps\n", float64(sent)*float64(size)*8/secs/1e6)
	fmt.Printf("relay egress      %.1f Mbps\n", float64(recv)*float64(size)*8/secs/1e6)
	fmt.Printf("\nPer player at this rate: %.2f Mbps in, %.2f Mbps out\n",
		float64(size)*8*float64(sent)/secs/float64(max64(st.handshake.Load(), 1))/1e6,
		float64(size)*8*float64(recv)/secs/float64(max64(st.handshake.Load(), 1))/1e6)
}

func max64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}
