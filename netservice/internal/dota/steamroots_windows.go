//go:build windows

package dota

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// steamRoots returns candidate Steam installation directories, most
// authoritative first: what Steam itself recorded in the registry, then the
// conventional locations for people who moved it.
func steamRoots() []string {
	var roots []string

	for _, k := range []struct {
		key  registry.Key
		path string
	}{
		{registry.CURRENT_USER, `Software\Valve\Steam`},
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Valve\Steam`},
		{registry.LOCAL_MACHINE, `SOFTWARE\Valve\Steam`},
	} {
		key, err := registry.OpenKey(k.key, k.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		for _, name := range []string{"SteamPath", "InstallPath"} {
			if v, _, err := key.GetStringValue(name); err == nil && v != "" {
				roots = append(roots, filepath.Clean(v))
			}
		}
		key.Close()
	}

	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles"} {
		if base := os.Getenv(env); base != "" {
			roots = append(roots, filepath.Join(base, "Steam"))
		}
	}
	return roots
}
