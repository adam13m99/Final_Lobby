package main

import (
	"path/filepath"
	"testing"
)

// within decides whether the installer is about to delete the folder it is
// running from. It got written after the updater downloaded the installer
// into exactly the folder the installer removes, so the removal was asking
// Windows to delete a running executable - it failed silently and left the
// folder half-emptied, which is a much worse outcome than not trying.
func TestWithin(t *testing.T) {
	cases := []struct {
		dir, path string
		want      bool
	}{
		{`C:\Users\a\AppData\Local\LobbyBaz`, `C:\Users\a\AppData\Local\LobbyBaz`, true},
		{`C:\Users\a\AppData\Local\LobbyBaz`, `C:\Users\a\AppData\Local\LobbyBaz\sub`, true},
		{`C:\Users\a\AppData\Local\LobbyBaz`, `C:\Users\a\AppData\Local`, false},
		{`C:\Users\a\AppData\Local\LobbyBaz`, `C:\Users\a\AppData\Local\Temp`, false},
		// The near-miss that matters: a sibling whose name starts with the
		// same characters must not be mistaken for a child.
		{`C:\Users\a\AppData\Local\LobbyBaz`, `C:\Users\a\AppData\Local\LobbyBazOther`, false},
	}
	for _, c := range cases {
		if got := within(filepath.FromSlash(c.dir), filepath.FromSlash(c.path)); got != c.want {
			t.Errorf("within(%q, %q) = %v, want %v", c.dir, c.path, got, c.want)
		}
	}
}
