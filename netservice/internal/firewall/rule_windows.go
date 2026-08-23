//go:build windows

// Package firewall makes sure the host's Windows Firewall lets other players
// in.
//
// This exists because of how the failure looks without it. The host's game
// starts, the tunnel is up, both players see the room - and the joining
// player simply times out, because Windows silently dropped the packets. It
// looks like a fault in our network, and it is not.
//
// A fresh Windows install blocks inbound connections by default, and the
// Wintun adapter lands in the Public profile where the rules are strictest.
// Dota usually has a rule from the first time it ran, but "usually" is not
// something to build a product on.
package firewall

import (
	"fmt"
	"os/exec"
	"strings"
)

// RuleName is what appears in Windows Defender Firewall. It is distinctive so
// a person can find and remove it.
const RuleName = "LobbyBaz (Dota 2 match hosting)"

// ourSubnet scopes the rule to our own address space. Without this we would
// be opening the player's game to their whole local network, which is more
// than we need and more than we should ask for.
const ourSubnet = "10.87.0.0/16"

// Ensure creates the inbound rule if it is missing. It is safe to call
// repeatedly.
func Ensure(dotaExe string) error {
	if Exists() {
		return nil
	}
	return Add(dotaExe)
}

// Exists reports whether our rule is already present.
func Exists() bool {
	out, err := exec.Command("netsh", "advfirewall", "firewall", "show", "rule",
		"name="+RuleName).CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), RuleName)
}

// Add creates the inbound allow rule for Dota, scoped to our subnet.
func Add(dotaExe string) error {
	args := []string{
		"advfirewall", "firewall", "add", "rule",
		"name=" + RuleName,
		"dir=in",
		"action=allow",
		"protocol=UDP",
		"localport=27015-27020",
		"remoteip=" + ourSubnet,
		"profile=any",
	}
	if dotaExe != "" {
		args = append(args, "program="+dotaExe)
	}
	if out, err := exec.Command("netsh", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("firewall: could not add rule: %w (%s)",
			err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Remove deletes the rule. Uninstalling should leave nothing behind.
func Remove() error {
	out, err := exec.Command("netsh", "advfirewall", "firewall", "delete", "rule",
		"name="+RuleName).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "No rules match") {
		return fmt.Errorf("firewall: could not remove rule: %w (%s)",
			err, strings.TrimSpace(string(out)))
	}
	return nil
}
