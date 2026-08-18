package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"os"
	"time"

	"finallobby/protocol/wire"
)

// runEchoPeer joins a room as an ordinary peer and answers ICMP echo
// requests, so a real machine on the other side of the relay can ping it.
//
// This exists to prove the data path without a second PC. A ping from
// Windows travels: the OS, into the Wintun adapter, through the tunnel, to
// the relay, out to this peer - and the reply travels all the way back. If
// that round trip works, everything between the two ends is sound, and the
// only thing left needing a second machine is Dota itself.
func runEchoPeer(relayAddr, relayPubHex, ticket string, quiet bool) error {
	pub, err := hex.DecodeString(relayPubHex)
	if err != nil || len(pub) != 32 {
		return fmt.Errorf("relay key must be 64 hex characters")
	}

	conn, err := net.Dial("udp", relayAddr)
	if err != nil {
		return err
	}
	defer conn.Close()
	udp := conn.(*net.UDPConn)

	sess, accept, err := handshake(udp, pub, []byte(ticket))
	if err != nil {
		return fmt.Errorf("handshake: %w", err)
	}
	fmt.Printf("echo peer is %s in room %q (session %d)\n",
		accept.VirtualIP, accept.RoomID, accept.SessionID)
	fmt.Println("answering pings; press Ctrl-C to stop")

	var seq uint64
	buf := make([]byte, 2048)
	for {
		_ = udp.SetReadDeadline(time.Now().Add(60 * time.Second))
		n, err := udp.Read(buf)
		if err != nil {
			if os.IsTimeout(err) {
				continue
			}
			return err
		}
		h, err := wire.DecodeHeader(buf[:n])
		if err != nil || h.Type != wire.TypeData {
			continue
		}
		_, inner, err := sess.Open(buf[:n])
		if err != nil {
			continue
		}

		reply, ok := icmpEchoReply(inner, accept.VirtualIP)
		if !ok {
			continue
		}
		seq++
		out, err := sess.Seal(nil, wire.Header{
			Version:   wire.ProtocolVersion,
			Type:      wire.TypeData,
			SessionID: accept.SessionID,
			Sequence:  seq,
		}, reply)
		if err != nil {
			continue
		}
		if _, err := udp.Write(out); err != nil {
			return err
		}
		if !quiet {
			src := netip.AddrFrom4([4]byte(inner[12:16]))
			fmt.Printf("  replied to a ping from %s (%d bytes)\n", src, len(inner))
		}
	}
}

// icmpEchoReply turns an ICMP echo request into its reply, in place on a
// copy. It returns false for anything that is not an echo request addressed
// to us.
func icmpEchoReply(pkt []byte, self netip.Addr) ([]byte, bool) {
	if len(pkt) < 28 || pkt[0]>>4 != 4 {
		return nil, false
	}
	ihl := int(pkt[0]&0x0F) * 4
	if ihl < 20 || len(pkt) < ihl+8 {
		return nil, false
	}
	if pkt[9] != 1 { // protocol: ICMP
		return nil, false
	}
	dst := netip.AddrFrom4([4]byte(pkt[16:20]))
	if dst != self {
		return nil, false
	}
	if pkt[ihl] != 8 { // ICMP type: echo request
		return nil, false
	}

	out := make([]byte, len(pkt))
	copy(out, pkt)

	// Swap source and destination.
	copy(out[12:16], pkt[16:20])
	copy(out[16:20], pkt[12:16])

	// Echo reply is type 0. Both checksums must be recomputed: the IP header
	// changed, and so did the ICMP type byte.
	out[ihl] = 0
	out[10], out[11] = 0, 0
	binary.BigEndian.PutUint16(out[10:12], checksum(out[:ihl]))
	out[ihl+2], out[ihl+3] = 0, 0
	binary.BigEndian.PutUint16(out[ihl+2:ihl+4], checksum(out[ihl:]))

	return out, true
}

// checksum is the standard one's-complement sum used by IP and ICMP.
func checksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i : i+2]))
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xFFFF) + (sum >> 16)
	}
	return ^uint16(sum)
}
