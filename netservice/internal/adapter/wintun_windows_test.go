//go:build windows

package adapter_test

import (
	"net/netip"
	"os"
	"testing"

	"finallobby/netservice/internal/adapter"
)

// These tests need Administrator rights and the Wintun driver.
// Run with: set LOBBY_INTEGRATION=1 && go test ./internal/adapter/ -v
func requireAdmin(t *testing.T) {
	t.Helper()
	if os.Getenv("LOBBY_INTEGRATION") == "" {
		t.Skip("set LOBBY_INTEGRATION=1 and run as Administrator")
	}
}

func TestDriverExtractsWithoutAdmin(t *testing.T) {
	// Extraction is pure file I/O and must work before any privileged call,
	// so this one runs everywhere.
	path, err := adapter.EnsureDriver()
	if err != nil {
		t.Fatalf("EnsureDriver: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("driver not on disk at %s: %v", path, err)
	}
	if st.Size() < 100_000 {
		t.Fatalf("driver is %d bytes, far too small to be wintun.dll", st.Size())
	}

	// Calling it twice must be a no-op, not a rewrite - the DLL may be
	// loaded and locked by a running service.
	again, err := adapter.EnsureDriver()
	if err != nil || again != path {
		t.Fatalf("second EnsureDriver: %v (%s)", err, again)
	}
}

func TestConfigureRejectsAddressOutsideSubnet(t *testing.T) {
	// A pure validation check, no adapter needed - this is the guard that
	// keeps a client from routing traffic for a room it is not in.
	err := adapter.ValidateAssignment(
		netip.MustParseAddr("10.87.0.20"),
		netip.MustParsePrefix("10.87.0.0/28"),
	)
	if err == nil {
		t.Fatal("expected an address outside its own /28 to be rejected")
	}
	if err := adapter.ValidateAssignment(
		netip.MustParseAddr("10.87.0.2"),
		netip.MustParsePrefix("10.87.0.0/28"),
	); err != nil {
		t.Fatalf("valid assignment rejected: %v", err)
	}
}

func TestMaskForKnownPrefixes(t *testing.T) {
	cases := map[string]string{
		"10.87.0.0/28": "255.255.255.240",
		"10.87.0.0/24": "255.255.255.0",
		"10.87.0.0/16": "255.255.0.0",
	}
	for prefix, want := range cases {
		if got := adapter.MaskFor(netip.MustParsePrefix(prefix)); got != want {
			t.Errorf("MaskFor(%s) = %s, want %s", prefix, got, want)
		}
	}
}

func TestAdapterLifecycle(t *testing.T) {
	requireAdmin(t)

	a, err := adapter.Open("FinalLobbyTest", adapter.MTU)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer a.Close()

	ip := netip.MustParseAddr("10.87.0.2")
	subnet := netip.MustParsePrefix("10.87.0.0/28")
	if err := a.Configure(ip, subnet); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if a.Name() == "" {
		t.Error("adapter has no interface name")
	}
}
