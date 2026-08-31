package gamemode

import "testing"

// The IDs are Valve's, and they reach a real Dota 2 command line. Renumbering
// one to tidy the list up would start the wrong game and nothing in the
// harness above this point would notice, so they are pinned here against the
// published DOTA_GameMode enum.
func TestTheIDsAreValvesAndNotOurs(t *testing.T) {
	want := map[int]string{
		1: "All Pick", 2: "Captains Mode", 3: "Random Draft",
		4: "Single Draft", 5: "All Random", 8: "Reverse Captains Mode",
		16: "Captains Draft", 18: "Ability Draft",
		20: "All Random Deathmatch", 21: "1v1 Solo Mid",
		22: "Ranked All Pick", 23: "Turbo",
	}
	for _, m := range Modes {
		name, ok := want[m.ID]
		if !ok {
			t.Errorf("mode %d (%s) is not in the DOTA_GameMode enum we pinned", m.ID, m.Name)
			continue
		}
		if name != m.Name {
			t.Errorf("mode %d is %q here and %q in Dota", m.ID, m.Name, name)
		}
		delete(want, m.ID)
	}
	for id, name := range want {
		t.Errorf("%s (%d) was dropped from the menu", name, id)
	}
}

// Keys become interface strings and IDs become command lines. Either one
// repeated is a menu with two entries that do the same thing, or worse, one
// entry whose label belongs to the other.
func TestNoModeIsListedTwice(t *testing.T) {
	ids, keys := map[int]bool{}, map[string]bool{}
	for _, m := range Modes {
		if ids[m.ID] {
			t.Errorf("mode ID %d appears twice", m.ID)
		}
		if keys[m.Key] {
			t.Errorf("mode key %q appears twice", m.Key)
		}
		if m.Key == "" || m.Name == "" {
			t.Errorf("mode %d has an empty key or name", m.ID)
		}
		ids[m.ID], keys[m.Key] = true, true
	}
}

// A room stored before rooms had a mode reads back as zero. That must be the
// mode every such room was already being launched with, not a refusal and not
// a different game.
func TestAnUnsetModeIsAllPick(t *testing.T) {
	if got := OrDefault(0); got != 1 {
		t.Fatalf("an unset mode became %d, want 1 (All Pick)", got)
	}
	if got := OrDefault(9999); got != Default {
		t.Fatalf("an unknown mode became %d, want the default %d", got, Default)
	}
	if got := OrDefault(23); got != 23 {
		t.Fatalf("a real mode was changed to %d", got)
	}
	if !Valid(Default) {
		t.Fatal("the default mode is not in the menu")
	}
}

func TestNameAndGet(t *testing.T) {
	if name, ok := Name(23); !ok || name != "Turbo" {
		t.Fatalf("Name(23) = %q, %v; want Turbo, true", name, ok)
	}
	if _, ok := Name(7); ok {
		t.Fatal("mode 7 (The Greeviling) is offered; it is not playable here")
	}
	if m, ok := Get(2); !ok || m.Key != "cm" {
		t.Fatalf("Get(2) = %+v, %v", m, ok)
	}
}
