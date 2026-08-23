//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"finallobby/client/build"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc/mgr"
)

const serviceName = "FinalLobbyNet"

func installDir() string {
	base := os.Getenv("ProgramFiles")
	if base == "" {
		base = `C:\Program Files`
	}
	return filepath.Join(base, appName)
}

// --- elevation ----------------------------------------------------------

// elevated reports whether this process can do privileged work.
func elevated() bool {
	var sid *windows.SID
	// The well-known Administrators group.
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY, 2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0, &sid)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)

	member, err := windows.Token(0).IsMember(sid)
	return err == nil && member
}

// reexecElevated restarts this installer through the UAC prompt, passing our
// own arguments along so /silent and /uninstall survive the transition.
func reexecElevated() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := strings.Join(os.Args[1:], " ")

	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	cwd, _ := syscall.UTF16PtrFromString(filepath.Dir(exe))
	var argPtr *uint16
	if args != "" {
		argPtr, _ = syscall.UTF16PtrFromString(args)
	}

	// SW_NORMAL: the elevated copy gets its own console window, which is
	// where the player watches the install happen.
	return windows.ShellExecute(0, verb, file, argPtr, cwd, 1)
}

// --- service ------------------------------------------------------------

// stopExisting clears out a previous install. Both halves are handled
// separately because they can go missing independently: the service can be
// registered with its binary already deleted, and the app can be running
// from a folder whose service was never installed.
func stopExisting(dir string) {
	svcExe := filepath.Join(dir, "netservice.exe")
	if _, err := os.Stat(svcExe); err == nil {
		say("Removing the previous version")
		_ = exec.Command(svcExe, "uninstall").Run()
	} else if serviceExists() {
		// Its binary is somewhere else, or gone: unregister it directly.
		// This is the path an upgrade from the first test build takes, and
		// it used to happen in silence, which made a working install look
		// like it had skipped a step.
		say("Removing the previous version")
		_ = exec.Command("sc.exe", "stop", serviceName).Run()
		_ = exec.Command("sc.exe", "delete", serviceName).Run()
	}

	// The app may be running from the folder we are about to overwrite.
	_ = exec.Command("taskkill.exe", "/F", "/IM", "lobbyapp.exe").Run()

	// Windows keeps a service registered until every handle to it closes,
	// and creating one with the same name fails until it does.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !serviceExists() {
			return
		}
		time.Sleep(400 * time.Millisecond)
	}
}

func serviceExists() bool {
	m, err := mgr.Connect()
	if err != nil {
		return false
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return false
	}
	s.Close()
	return true
}

func waitForService(within time.Duration) error {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		m, err := mgr.Connect()
		if err == nil {
			s, err := m.OpenService(serviceName)
			if err == nil {
				status, err := s.Query()
				s.Close()
				m.Disconnect()
				if err == nil && status.State == windows.SERVICE_RUNNING {
					return nil
				}
				time.Sleep(500 * time.Millisecond)
				continue
			}
			m.Disconnect()
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("the service was installed but did not start; try: Start-Service %s", serviceName)
}

// --- shortcut -----------------------------------------------------------

func desktopShortcutPath() (string, error) {
	// The elevated process may be running as a different account, so the
	// per-user desktop is the wrong place to look. The all-users desktop is
	// visible to whoever is logged in, which is what we want.
	dir, err := windows.KnownFolderPath(windows.FOLDERID_PublicDesktop, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, appName+".lnk"), nil
}

// writeShortcut asks PowerShell to create the .lnk. Building one by hand
// means writing a binary shell-link structure; a two-line script is clearer
// and this runs once.
func writeShortcut(dir string) error {
	lnk, err := desktopShortcutPath()
	if err != nil {
		return err
	}
	target := filepath.Join(dir, "lobbyapp.exe")
	script := fmt.Sprintf(
		`$s=(New-Object -ComObject WScript.Shell).CreateShortcut(%q);`+
			`$s.TargetPath=%q;$s.WorkingDirectory=%q;`+
			`$s.Description='Final Lobby - play Dota 2 with friends';$s.Save()`,
		lnk, target, dir)

	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive",
		"-ExecutionPolicy", "Bypass", "-Command", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// --- add or remove programs ---------------------------------------------

const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\FinalLobby`

func registerUninstall(dir string) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, uninstallKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	self := filepath.Join(dir, "FinalLobby-Setup.exe")
	// Keep a copy of ourselves so the entry still works after the download
	// is deleted, which is the first thing most people do.
	if exe, err := os.Executable(); err == nil {
		_ = copyFile(exe, self)
	}

	for name, value := range map[string]string{
		"DisplayName":     appName,
		"DisplayVersion":  build.Version,
		"Publisher":       "Final Lobby",
		"InstallLocation": dir,
		"UninstallString": `"` + self + `" /uninstall`,
		"DisplayIcon":     filepath.Join(dir, "lobbyapp.exe"),
	} {
		if err := k.SetStringValue(name, value); err != nil {
			return err
		}
	}
	return k.SetDWordValue("NoModify", 1)
}

func unregisterUninstall() {
	_ = registry.DeleteKey(registry.LOCAL_MACHINE, uninstallKey)
}

func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o755)
}

// --- launching back into the user's session -----------------------------

// launchAsUser starts the app without our elevated token.
//
// Starting it directly would hand the player an app running as
// Administrator, which is exactly the arrangement that gave the predecessor
// platform a remote-code-execution hole. Going through the shell makes
// Windows start it as the logged-in user instead.
func launchAsUser(path string) {
	cmd := exec.Command(filepath.Join(os.Getenv("WINDIR"), "explorer.exe"), path)
	if err := cmd.Start(); err != nil {
		return
	}
	_ = cmd.Process.Release()
}
