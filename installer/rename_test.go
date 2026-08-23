package main

import "testing"

// The rename to LobbyBaz (D46) is only safe if the installer still knows the
// name it is replacing. Nothing about installing LobbyBaz disturbs a
// "Final Lobby" install, because they collide nowhere - not the service, not
// the directory, not the shortcut, not the uninstall key. That is precisely
// why the old one has to be removed on purpose: left alone the two coexist,
// and two network services race to create the same virtual adapter.
//
// These constants are the whole upgrade path for the two machines installed
// before the rename. A well-meaning search-and-replace across the repository
// would rewrite them to the new name and the upgrade would quietly stop
// working - the installer would report success while leaving the old service
// running. This test exists to fail loudly if that happens.
func TestLegacyNamesAreNotRenamed(t *testing.T) {
	cases := []struct{ got, want, what string }{
		{legacyServiceName, "FinalLobbyNet", "the previous Windows service"},
		{legacyAppName, "Final Lobby", "the previous install directory"},
		{
			legacyUninstall,
			`Software\Microsoft\Windows\CurrentVersion\Uninstall\FinalLobby`,
			"the previous Add or Remove Programs key",
		},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s is %q, want %q\n"+
				"These name the OLD product and must keep the OLD name. "+
				"If the rename was applied to them, machines running a "+
				"pre-rename build will end up with two installs.",
				c.what, c.got, c.want)
		}
	}
}

// And the converse: the names we install under must have actually moved.
func TestCurrentNamesAreRenamed(t *testing.T) {
	if appName != "LobbyBaz" {
		t.Errorf("appName = %q, want %q", appName, "LobbyBaz")
	}
	if serviceName != "LobbyBazNet" {
		t.Errorf("serviceName = %q, want %q", serviceName, "LobbyBazNet")
	}
	if appName == legacyAppName || serviceName == legacyServiceName {
		t.Fatal("the new install would collide with the old one instead of replacing it")
	}
}
