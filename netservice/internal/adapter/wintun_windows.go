//go:build windows

// Package adapter owns the Wintun virtual network adapter: creating it,
// giving it the player's virtual address, and moving packets across it.
package adapter

import (
	_ "embed"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.zx2c4.com/wireguard/tun"
)

// MTU is deliberately below the usual 1500 so that an encrypted datagram
// plus its outer UDP and IP headers still fits inside a normal Ethernet
// frame. A fragmented game packet is a lost game packet.
const MTU = 1300

// wintunDLL is the official Wintun driver, version 0.14.1, Authenticode
// signed by WireGuard LLC. It is embedded rather than downloaded because
// wintun.net is unreachable from Iranian networks, and because a player
// should never have to install a driver by hand.
//
//go:embed bin/wintun.dll
var wintunDLL []byte

// Adapter wraps a Wintun device.
type Adapter struct {
	dev  tun.Device
	name string
}

// EnsureDriver writes wintun.dll next to the running executable if it is not
// already there, and returns its path. Wintun's loader only searches the
// application directory and System32, so this is where it has to go.
//
// An existing file is left alone: it may be loaded and locked by a running
// service, and rewriting it would fail for no benefit.
func EnsureDriver() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("adapter: locate executable: %w", err)
	}
	dst := filepath.Join(filepath.Dir(exe), "wintun.dll")

	if st, err := os.Stat(dst); err == nil {
		if st.Size() == int64(len(wintunDLL)) {
			return dst, nil
		}
		// A different build is present. Replace it via a temporary file and
		// a rename, so a crash never leaves a half-written driver behind.
	}

	tmp, err := os.CreateTemp(filepath.Dir(exe), "wintun-*.tmp")
	if err != nil {
		return "", fmt.Errorf("adapter: stage driver: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(wintunDLL); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", fmt.Errorf("adapter: write driver: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("adapter: install driver: %w", err)
	}
	return dst, nil
}

// Open creates (or reuses) the named Wintun adapter.
func Open(name string, mtu int) (*Adapter, error) {
	if _, err := EnsureDriver(); err != nil {
		return nil, err
	}
	dev, err := tun.CreateTUN(name, mtu)
	if err != nil {
		return nil, fmt.Errorf("adapter: create %q: %w", name, err)
	}
	actual, err := dev.Name()
	if err != nil {
		_ = dev.Close()
		return nil, fmt.Errorf("adapter: name: %w", err)
	}
	return &Adapter{dev: dev, name: actual}, nil
}

// ValidateAssignment rejects an address that does not belong to its own
// subnet. Getting this wrong would give the machine a route covering
// addresses in other people's rooms.
func ValidateAssignment(ip netip.Addr, subnet netip.Prefix) error {
	if !ip.Is4() {
		return fmt.Errorf("adapter: %s is not an IPv4 address", ip)
	}
	if !subnet.Contains(ip) {
		return fmt.Errorf("adapter: %s is not inside %s", ip, subnet)
	}
	return nil
}

// Configure assigns ip to the adapter and leaves it with a route for subnet
// only.
//
// Restricting the on-link route to the room's own /28 is the client-side
// half of room isolation: this machine has no route to any other room's
// addresses, so even a bug in the relay cannot make it talk to strangers.
func (a *Adapter) Configure(ip netip.Addr, subnet netip.Prefix) error {
	if err := ValidateAssignment(ip, subnet); err != nil {
		return err
	}
	// netsh is used rather than a routing library because it is present on
	// every supported Windows build and needs no privileges beyond the ones
	// the service already holds.
	cmds := [][]string{
		{"netsh", "interface", "ip", "set", "address",
			"name=" + a.name, "static", ip.String(), MaskFor(subnet)},
		{"netsh", "interface", "ipv4", "set", "subinterface",
			a.name, fmt.Sprintf("mtu=%d", MTU), "store=active"},
	}
	for _, c := range cmds {
		out, err := exec.Command(c[0], c[1:]...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("adapter: %s: %w (%s)",
				strings.Join(c, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// MaskFor renders a prefix length as a dotted-decimal subnet mask, which is
// the only form netsh accepts.
func MaskFor(p netip.Prefix) string {
	var mask [4]byte
	for i := 0; i < p.Bits(); i++ {
		mask[i/8] |= 1 << uint(7-i%8)
	}
	return netip.AddrFrom4(mask).String()
}

// Read returns one outbound IP packet from the operating system.
func (a *Adapter) Read(buf []byte) (int, error) {
	sizes := make([]int, 1)
	bufs := [][]byte{buf}
	n, err := a.dev.Read(bufs, sizes, 0)
	if err != nil || n == 0 {
		return 0, err
	}
	return sizes[0], nil
}

// Write injects one inbound IP packet into the operating system.
func (a *Adapter) Write(pkt []byte) error {
	_, err := a.dev.Write([][]byte{pkt}, 0)
	return err
}

// Close tears the adapter down. Wintun removes the interface with it, so a
// crashed client does not leave a dead adapter in the player's network
// settings.
func (a *Adapter) Close() error { return a.dev.Close() }

// Name returns the adapter's interface name.
func (a *Adapter) Name() string { return a.name }
