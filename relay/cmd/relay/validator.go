package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"lobbybaz/relay/internal/server"
)

// ticketTTL is how long a positive validation is trusted. Short enough that
// a revoked ticket stops working promptly, long enough that a coordinator
// restart does not stall every handshake in flight.
const ticketTTL = 30 * time.Second

type cachedClaims struct {
	claims  server.TicketClaims
	expires time.Time
}

// coordinatorValidator asks the coordinator whether a ticket is valid, and
// caches positive answers briefly. Negative answers are never cached: a
// player who has just been granted a ticket must not be locked out by a
// stale "no".
type coordinatorValidator struct {
	baseURL string
	client  *http.Client

	mu    sync.Mutex
	cache map[[32]byte]cachedClaims
}

func newCoordinatorValidator(baseURL string) func([]byte) (server.TicketClaims, error) {
	v := &coordinatorValidator{
		baseURL: strings.TrimRight(baseURL, "/"),
		client: &http.Client{
			Timeout: 3 * time.Second,
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 8,
				DialContext:         (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
			},
		},
		cache: make(map[[32]byte]cachedClaims),
	}
	return v.validate
}

func (v *coordinatorValidator) validate(ticket []byte) (server.TicketClaims, error) {
	key := sha256.Sum256(ticket)

	v.mu.Lock()
	if hit, ok := v.cache[key]; ok && time.Now().Before(hit.expires) {
		v.mu.Unlock()
		return hit.claims, nil
	}
	v.mu.Unlock()

	body, err := json.Marshal(map[string]string{"ticket": string(ticket)})
	if err != nil {
		return server.TicketClaims{}, err
	}
	resp, err := v.client.Post(v.baseURL+"/internal/validate-ticket", "application/json", bytes.NewReader(body))
	if err != nil {
		return server.TicketClaims{}, fmt.Errorf("coordinator unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return server.TicketClaims{}, fmt.Errorf("coordinator rejected ticket: %s", resp.Status)
	}

	var out struct {
		RoomID    string `json:"room_id"`
		VirtualIP string `json:"virtual_ip"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return server.TicketClaims{}, err
	}
	addr, err := netip.ParseAddr(out.VirtualIP)
	if err != nil {
		return server.TicketClaims{}, fmt.Errorf("coordinator returned a bad virtual IP %q: %w", out.VirtualIP, err)
	}
	claims := server.TicketClaims{RoomID: out.RoomID, VirtualIP: addr}

	v.mu.Lock()
	v.cache[key] = cachedClaims{claims: claims, expires: time.Now().Add(ticketTTL)}
	// The cache is bounded by eviction on read plus this sweep, so a flood
	// of distinct tickets cannot grow it without limit.
	if len(v.cache) > 8192 {
		now := time.Now()
		for k, c := range v.cache {
			if now.After(c.expires) {
				delete(v.cache, k)
			}
		}
	}
	v.mu.Unlock()

	return claims, nil
}

// devValidator accepts unsigned "roomID|virtualIP" tickets.
//
// It exists so the relay can be deployed and smoke-tested on the server
// before the coordinator issues real signed tickets. It is gated behind an
// explicit flag and logs a warning at startup. Delete it once Task 10 lands.
func devValidator(ticket []byte) (server.TicketClaims, error) {
	room, ip, found := strings.Cut(string(ticket), "|")
	if !found || room == "" {
		return server.TicketClaims{}, errors.New("dev ticket must be roomID|virtualIP")
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return server.TicketClaims{}, err
	}
	return server.TicketClaims{RoomID: room, VirtualIP: addr}, nil
}
