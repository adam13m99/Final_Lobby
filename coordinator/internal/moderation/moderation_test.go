package moderation

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"lobbybaz/coordinator/internal/account"
	"lobbybaz/coordinator/internal/store"
)

func rig(t *testing.T, names ...string) (*Store, *sql.DB, map[string]string) {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	acc := account.New(db)
	ids := map[string]string{}
	for _, n := range names {
		a, err := acc.SignUp(n, n, "a long enough password", at(0))
		if err != nil {
			t.Fatalf("signing up %s: %v", n, err)
		}
		ids[n] = a.ID
	}
	return New(db), db, ids
}

func at(min int) time.Time {
	return time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC).Add(time.Duration(min) * time.Minute)
}

// --- roles --------------------------------------------------------------

func TestTheHeadAdminIsBootstrappedOnceAndOnlyOnce(t *testing.T) {
	s, _, id := rig(t, "owner", "someone")

	if head, _ := s.HeadAdmin(); head != "" {
		t.Fatal("a fresh database already has a head admin")
	}
	if err := s.BootstrapHeadAdmin(id["owner"], at(0)); err != nil {
		t.Fatal(err)
	}
	if head, _ := s.HeadAdmin(); head != id["owner"] {
		t.Fatalf("head admin = %q", head)
	}
	// Running it again for the same person is harmless, so a deployment
	// script that runs twice does not fail.
	if err := s.BootstrapHeadAdmin(id["owner"], at(1)); err != nil {
		t.Fatalf("bootstrapping the same person twice failed: %v", err)
	}
	// A second, different head admin is refused. There is exactly one (D47).
	if err := s.BootstrapHeadAdmin(id["someone"], at(2)); !errors.Is(err, ErrHeadAdminSet) {
		t.Fatalf("got %v, want ErrHeadAdminSet", err)
	}
}

// D47's central rule, and the one the plan asked for by name: an admin cannot
// appoint another admin, or the role spreads and cannot be pulled back.
func TestAnAdminCannotAppointAnotherAdmin(t *testing.T) {
	s, _, id := rig(t, "owner", "admin", "hopeful")
	if err := s.BootstrapHeadAdmin(id["owner"], at(0)); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantAdmin(id["owner"], id["admin"], at(1)); err != nil {
		t.Fatal(err)
	}
	if role, _ := s.RoleOf(id["admin"]); role != RoleAdmin {
		t.Fatalf("role = %q, want admin", role)
	}

	if err := s.GrantAdmin(id["admin"], id["hopeful"], at(2)); !errors.Is(err, ErrNotHeadAdmin) {
		t.Fatalf("an admin appointed another admin: %v", err)
	}
	if role, _ := s.RoleOf(id["hopeful"]); role != RoleNone {
		t.Fatalf("hopeful ended up as %q", role)
	}
	// Nor can an admin remove one.
	if err := s.RevokeAdmin(id["admin"], id["admin"], at(2)); !errors.Is(err, ErrNotHeadAdmin) {
		t.Fatalf("an admin revoked a role: %v", err)
	}
	// An ordinary player certainly cannot.
	if err := s.GrantAdmin(id["hopeful"], id["hopeful"], at(2)); !errors.Is(err, ErrNotHeadAdmin) {
		t.Fatalf("an ordinary player appointed themselves: %v", err)
	}
}

// "Who made this person an admin?" is exactly the question asked after
// something goes wrong, and it must have an answer (D47).
func TestAGrantRemembersWhoGaveItAndWhoTookItAway(t *testing.T) {
	s, _, id := rig(t, "owner", "admin")
	_ = s.BootstrapHeadAdmin(id["owner"], at(0))
	_ = s.GrantAdmin(id["owner"], id["admin"], at(1))

	if err := s.RevokeAdmin(id["owner"], id["admin"], at(5)); err != nil {
		t.Fatal(err)
	}
	if role, _ := s.RoleOf(id["admin"]); role != RoleNone {
		t.Fatalf("role after revoking = %q", role)
	}

	// The grant is stamped, not deleted: the history survives.
	history, err := s.GrantHistory(id["admin"])
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("history = %+v", history)
	}
	g := history[0]
	if g.GrantedBy != id["owner"] || g.RevokedBy != id["owner"] {
		t.Errorf("grant = %+v; the author of the appointment or its withdrawal is missing", g)
	}
	if g.GrantedAt.IsZero() || g.RevokedAt.IsZero() {
		t.Errorf("grant = %+v; a timestamp is missing", g)
	}
}

func TestTheHeadAdminCannotBeRemoved(t *testing.T) {
	s, _, id := rig(t, "owner", "admin")
	_ = s.BootstrapHeadAdmin(id["owner"], at(0))
	_ = s.GrantAdmin(id["owner"], id["admin"], at(1))

	if err := s.RevokeAdmin(id["owner"], id["owner"], at(2)); !errors.Is(err, ErrSelfDemotion) {
		t.Errorf("the head admin removed themselves: %v", err)
	}
	if err := s.RevokeAdmin(id["admin"], id["owner"], at(2)); !errors.Is(err, ErrNotHeadAdmin) {
		t.Errorf("an admin removed the head admin: %v", err)
	}
	if head, _ := s.HeadAdmin(); head != id["owner"] {
		t.Fatal("the head admin is gone")
	}
}

func TestStaffListsTheTeamHeadAdminFirst(t *testing.T) {
	s, _, id := rig(t, "owner", "admin")
	_ = s.BootstrapHeadAdmin(id["owner"], at(0))
	_ = s.GrantAdmin(id["owner"], id["admin"], at(1))

	staff, err := s.Staff()
	if err != nil {
		t.Fatal(err)
	}
	if len(staff) != 2 {
		t.Fatalf("staff = %+v", staff)
	}
	if staff[0].Role != RoleHeadAdmin {
		t.Errorf("first entry is %q, want the head admin", staff[0].Role)
	}
}

// --- sanctions ----------------------------------------------------------

func TestSanctionsNeedAReasonAndAnAdmin(t *testing.T) {
	s, _, id := rig(t, "owner", "player", "pest")
	_ = s.BootstrapHeadAdmin(id["owner"], at(0))

	if _, err := s.Sanction(id["player"], id["pest"], KindMute, "annoying", time.Hour, at(1)); !errors.Is(err, ErrNotStaff) {
		t.Errorf("an ordinary player applied a sanction: %v", err)
	}
	// An unexplained sanction cannot be reviewed, appealed, or defended by
	// the moderator who gave it.
	if _, err := s.Sanction(id["owner"], id["pest"], KindMute, "   ", time.Hour, at(1)); !errors.Is(err, ErrReasonMissing) {
		t.Errorf("a sanction with no reason was accepted: %v", err)
	}
	if _, err := s.Sanction(id["owner"], id["pest"], Kind("shadowban"), "why", time.Hour, at(1)); !errors.Is(err, ErrBadKind) {
		t.Errorf("an invented sanction kind was accepted: %v", err)
	}
}

func TestATimeoutExpiresAndABanDoesNot(t *testing.T) {
	s, _, id := rig(t, "owner", "pest")
	_ = s.BootstrapHeadAdmin(id["owner"], at(0))

	if _, err := s.Sanction(id["owner"], id["pest"], KindTimeout, "shouting", 30*time.Minute, at(0)); err != nil {
		t.Fatal(err)
	}
	if rest, _ := s.Restrictions(id["pest"], at(5)); !rest.Timeout {
		t.Fatal("the timeout is not in force")
	}
	if rest, _ := s.Restrictions(id["pest"], at(31)); rest.Timeout {
		t.Fatal("the timeout did not expire by itself")
	}

	// Zero duration is permanent, and has to be asked for on purpose.
	if _, err := s.Sanction(id["owner"], id["pest"], KindBan, "cheating", 0, at(40)); err != nil {
		t.Fatal(err)
	}
	rest, _ := s.Restrictions(id["pest"], at(60*24*365))
	if !rest.Banned {
		t.Fatal("a permanent ban expired")
	}
	if !rest.Until.IsZero() {
		t.Errorf("a permanent ban reported an end date: %v", rest.Until)
	}
}

func TestLiftingASanctionKeepsTheRecord(t *testing.T) {
	s, _, id := rig(t, "owner", "pest")
	_ = s.BootstrapHeadAdmin(id["owner"], at(0))

	one, err := s.Sanction(id["owner"], id["pest"], KindMute, "shouting", time.Hour, at(0))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Lift(id["owner"], one.ID, at(10)); err != nil {
		t.Fatal(err)
	}
	if rest, _ := s.Restrictions(id["pest"], at(11)); rest.Muted {
		t.Fatal("the mute is still in force after being lifted")
	}

	// The row is stamped, not deleted. A moderation table you can erase by
	// undoing things is not a record.
	all, err := s.Sanctions(id["pest"])
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("history = %+v", all)
	}
	if all[0].LiftedBy != id["owner"] || all[0].LiftedAt.IsZero() {
		t.Errorf("the lift is not attributed: %+v", all[0])
	}
	if err := s.Lift(id["owner"], one.ID, at(12)); !errors.Is(err, ErrNoSanction) {
		t.Errorf("lifting the same sanction twice: %v", err)
	}
}

// A single compromised or angry admin must not be able to remove the rest of
// the team.
func TestAdminsCannotSanctionEachOtherOrTheHeadAdmin(t *testing.T) {
	s, _, id := rig(t, "owner", "adminA", "adminB")
	_ = s.BootstrapHeadAdmin(id["owner"], at(0))
	_ = s.GrantAdmin(id["owner"], id["adminA"], at(1))
	_ = s.GrantAdmin(id["owner"], id["adminB"], at(1))

	if _, err := s.Sanction(id["adminA"], id["adminB"], KindBan, "disagreement", 0, at(2)); !errors.Is(err, ErrNotHeadAdmin) {
		t.Errorf("one admin banned another: %v", err)
	}
	if _, err := s.Sanction(id["adminA"], id["owner"], KindBan, "coup", 0, at(2)); !errors.Is(err, ErrNotHeadAdmin) {
		t.Errorf("an admin banned the head admin: %v", err)
	}
	// The head admin can, which is what makes the team removable at all.
	if _, err := s.Sanction(id["owner"], id["adminA"], KindMute, "warned twice", time.Hour, at(3)); err != nil {
		t.Errorf("the head admin could not sanction an admin: %v", err)
	}
}

// --- attribution --------------------------------------------------------

// The plan asks for this by name: every action attributed to the admin who
// took it.
func TestEveryActionIsAttributed(t *testing.T) {
	s, _, id := rig(t, "owner", "admin", "pest")
	_ = s.BootstrapHeadAdmin(id["owner"], at(0))
	_ = s.GrantAdmin(id["owner"], id["admin"], at(1))

	one, err := s.Sanction(id["admin"], id["pest"], KindMute, "shouting", time.Hour, at(2))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.LabelPlayer(id["admin"], id["pest"], LabelFakeMMR, at(3)); err != nil {
		t.Fatal(err)
	}
	if err := s.Lift(id["admin"], one.ID, at(4)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddBanner(id["admin"], Banner{Title: "Tournament on Friday"}, at(5)); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(id["admin"], "close_room", "r_abc", "griefing", at(6)); err != nil {
		t.Fatal(err)
	}

	byAdmin, err := s.ActionsBy(id["admin"], 100)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"mute": false, "label": false, "lift": false,
		"banner_add": false, "close_room": false,
	}
	for _, a := range byAdmin {
		if a.ActorID != id["admin"] {
			t.Errorf("action %q attributed to %q", a.Action, a.ActorID)
		}
		if a.At.IsZero() {
			t.Errorf("action %q has no timestamp", a.Action)
		}
		if _, ok := want[a.Action]; ok {
			want[a.Action] = true
		}
	}
	for action, seen := range want {
		if !seen {
			t.Errorf("%q was not written to the log", action)
		}
	}

	// And it is readable from the other side, which is the view somebody
	// appealing a sanction needs.
	aboutPest, err := s.ActionsAbout(id["pest"], 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(aboutPest) < 3 {
		t.Fatalf("the pest's record has %d entries: %+v", len(aboutPest), aboutPest)
	}
	// Appointing the admin is attributed to the head admin, not to the admin.
	aboutAdmin, _ := s.ActionsAbout(id["admin"], 100)
	found := false
	for _, a := range aboutAdmin {
		if a.Action == "grant_admin" && a.ActorID == id["owner"] {
			found = true
		}
	}
	if !found {
		t.Error("the appointment of an admin was not recorded against the head admin")
	}

	// An ordinary player cannot write to the log.
	if err := s.Record(id["pest"], "close_room", "r_abc", "for fun", at(7)); !errors.Is(err, ErrNotStaff) {
		t.Errorf("an ordinary player wrote to the audit log: %v", err)
	}
}

// --- labels -------------------------------------------------------------

func TestLabelsAreFixedAndAttributed(t *testing.T) {
	s, _, id := rig(t, "owner", "player")
	_ = s.BootstrapHeadAdmin(id["owner"], at(0))

	// A label a moderator can type freely is a licence to write anything next
	// to somebody's name with staff authority behind it.
	if err := s.LabelPlayer(id["owner"], id["player"], Label("smells"), at(1)); !errors.Is(err, ErrBadLabel) {
		t.Fatalf("an invented label was accepted: %v", err)
	}
	if err := s.LabelPlayer(id["owner"], id["player"], LabelVerified, at(1)); err != nil {
		t.Fatal(err)
	}
	labels, _ := s.LabelsOf(id["player"])
	if len(labels) != 1 || labels[0] != LabelVerified {
		t.Fatalf("labels = %v", labels)
	}
	// Labelling twice is not an error.
	if err := s.LabelPlayer(id["owner"], id["player"], LabelVerified, at(2)); err != nil {
		t.Fatal(err)
	}
	if err := s.UnlabelPlayer(id["owner"], id["player"], LabelVerified, at(3)); err != nil {
		t.Fatal(err)
	}
	if labels, _ := s.LabelsOf(id["player"]); len(labels) != 0 {
		t.Fatalf("labels after removal = %v", labels)
	}
}

func TestLabelsForManyPlayersAtOnce(t *testing.T) {
	s, _, id := rig(t, "owner", "amir", "bita")
	_ = s.BootstrapHeadAdmin(id["owner"], at(0))
	_ = s.LabelPlayer(id["owner"], id["amir"], LabelProPlayer, at(1))
	_ = s.LabelPlayer(id["owner"], id["bita"], LabelFakeMMR, at(1))

	got, err := s.LabelsOfMany([]string{id["amir"], id["bita"], "a_nobody"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got[id["amir"]]) != 1 || got[id["amir"]][0] != LabelProPlayer {
		t.Errorf("amir = %v", got[id["amir"]])
	}
	if len(got[id["bita"]]) != 1 {
		t.Errorf("bita = %v", got[id["bita"]])
	}
	if _, err := s.LabelsOfMany(nil); err != nil {
		t.Errorf("an empty lookup failed: %v", err)
	}
}

// --- banners ------------------------------------------------------------

func TestBannerLinksAreRestrictedToHTTP(t *testing.T) {
	s, _, id := rig(t, "owner")
	_ = s.BootstrapHeadAdmin(id["owner"], at(0))

	// A banner is the one place somebody who is not the player chooses what
	// the player clicks, and this is a desktop application.
	for _, bad := range []string{
		"javascript:alert(1)", "file:///c:/windows/system32", "data:text/html,<b>",
	} {
		if _, err := s.AddBanner(id["owner"], Banner{Title: "Click", LinkURL: bad}, at(1)); !errors.Is(err, ErrBannerLink) {
			t.Errorf("link %q was accepted: %v", bad, err)
		}
	}
	if _, err := s.AddBanner(id["owner"], Banner{Title: "Ok", LinkURL: "https://example.com"}, at(1)); err != nil {
		t.Errorf("an https link was refused: %v", err)
	}
}

func TestBannersAddEditRemoveAndHide(t *testing.T) {
	s, _, id := rig(t, "owner", "player")
	_ = s.BootstrapHeadAdmin(id["owner"], at(0))

	if _, err := s.AddBanner(id["player"], Banner{Title: "hello"}, at(1)); !errors.Is(err, ErrNotStaff) {
		t.Fatalf("an ordinary player added a banner: %v", err)
	}
	if _, err := s.AddBanner(id["owner"], Banner{Title: "   "}, at(1)); !errors.Is(err, ErrBannerEmpty) {
		t.Fatalf("a titleless banner was accepted: %v", err)
	}

	b, err := s.AddBanner(id["owner"], Banner{
		Title: "Tournament on Friday", Body: "Sign up in the lobby", Active: true, Sort: 1,
	}, at(1))
	if err != nil {
		t.Fatal(err)
	}
	if b.CreatedBy != id["owner"] || b.CreatedAt.IsZero() {
		t.Errorf("banner = %+v; not attributed", b)
	}

	// A banner can be prepared before it runs, so staff see hidden ones and
	// the lobby does not.
	hidden, err := s.AddBanner(id["owner"], Banner{Title: "Next month", Active: false}, at(2))
	if err != nil {
		t.Fatal(err)
	}
	if live, _ := s.Banners(true); len(live) != 1 {
		t.Fatalf("the lobby sees %d banners, want 1", len(live))
	}
	if all, _ := s.Banners(false); len(all) != 2 {
		t.Fatalf("staff see %d banners, want 2", len(all))
	}

	edited, err := s.EditBanner(id["owner"], hidden.ID, Banner{Title: "Next month", Active: true}, at(3))
	if err != nil {
		t.Fatal(err)
	}
	if !edited.Active || edited.UpdatedBy != id["owner"] {
		t.Errorf("edited = %+v", edited)
	}
	if live, _ := s.Banners(true); len(live) != 2 {
		t.Fatalf("the lobby sees %d banners after publishing, want 2", len(live))
	}

	if err := s.RemoveBanner(id["owner"], b.ID, at(4)); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveBanner(id["owner"], b.ID, at(5)); !errors.Is(err, ErrNoSuchBanner) {
		t.Errorf("removing a banner twice: %v", err)
	}
}

func TestOverlongBannerTextIsTrimmedNotRejected(t *testing.T) {
	s, _, id := rig(t, "owner")
	_ = s.BootstrapHeadAdmin(id["owner"], at(0))

	b, err := s.AddBanner(id["owner"], Banner{
		Title: strings.Repeat("ع", MaxBannerTitle+50),
		Body:  strings.Repeat("ع", MaxBannerBody+50),
	}, at(1))
	if err != nil {
		t.Fatal(err)
	}
	// Counted in characters, so a Persian banner is measured the way a person
	// would measure it.
	if n := len([]rune(b.Title)); n != MaxBannerTitle {
		t.Errorf("title trimmed to %d runes, want %d", n, MaxBannerTitle)
	}
	if n := len([]rune(b.Body)); n != MaxBannerBody {
		t.Errorf("body trimmed to %d runes, want %d", n, MaxBannerBody)
	}
}
