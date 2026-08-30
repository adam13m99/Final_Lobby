package main

import "testing"

// Every component this installer promises has to actually be inside it.
//
// This is here because of D67. The desktop shell existed, built and worked
// for days while the installer shipped only the three Go binaries and pointed
// the desktop shortcut at the console one - so the owner opened LobbyBaz and
// got a command window and a browser tab. Nothing failed. The installer was
// correct about everything it knew about.
//
// writeComponent does report a missing payload, but only on the player's
// machine, halfway through an install, after the service has been registered.
// This catches it here, where a forgotten line in scripts/build.sh costs a
// red test instead of a bad install.
func TestEveryComponentIsInThePayload(t *testing.T) {
	for _, name := range components {
		f, err := payload.Open("payload/" + name + ".gz")
		if err != nil {
			t.Errorf("%s is listed as a component but is not in the payload: %v", name, err)
			continue
		}
		info, err := f.Stat()
		if err == nil && info.Size() < 1024 {
			t.Errorf("payload/%s.gz is %d bytes - that is not an executable", name, info.Size())
		}
		f.Close()
	}
}

// The shortcut, the uninstall icon and the post-update relaunch must all name
// the window rather than the server behind it. They are three separate lines
// in two files, and getting any one of them wrong reinstates the console
// window the owner reported (D67).
func TestTheShellIsWhatGetsInstalled(t *testing.T) {
	found := false
	for _, name := range components {
		if name == shellExe {
			found = true
		}
	}
	if !found {
		t.Fatalf("%s is not among the installed components: %v", shellExe, components)
	}
	if shellExe == serverExe {
		t.Fatalf("the window and the server must be different executables")
	}
}
