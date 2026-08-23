package account

import (
	"errors"
	"strings"
	"testing"
	"time"

	"lobbybaz/coordinator/internal/store"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db)
}

func at(min int) time.Time {
	return time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC).Add(time.Duration(min) * time.Minute)
}

func TestSignUpThenSignIn(t *testing.T) {
	s := newStore(t)

	a, err := s.SignUp("Reza_79", "رضا", "a long enough password", at(0))
	if err != nil {
		t.Fatalf("signing up: %v", err)
	}
	if a.Username != "reza_79" {
		t.Errorf("username = %q, want it folded to lower case", a.Username)
	}
	if a.DisplayName != "رضا" {
		t.Errorf("display name = %q, want it kept as typed", a.DisplayName)
	}

	// Signing in is case-insensitive on the username: somebody who typed
	// "Reza_79" at signup must not be locked out by typing "reza_79" later.
	got, err := s.Authenticate("REZA_79", "a long enough password", at(1))
	if err != nil {
		t.Fatalf("signing in: %v", err)
	}
	if got.ID != a.ID {
		t.Errorf("signed in as %s, want %s", got.ID, a.ID)
	}
}

func TestWrongPasswordAndUnknownUserAreIndistinguishable(t *testing.T) {
	s := newStore(t)
	if _, err := s.SignUp("reza", "Reza", "correct horse battery", at(0)); err != nil {
		t.Fatal(err)
	}

	wrong := errFrom(s.Authenticate("reza", "not the password", at(1)))
	missing := errFrom(s.Authenticate("nobody-at-all", "not the password", at(1)))

	if !errors.Is(wrong, ErrPasswordMismatch) {
		t.Errorf("wrong password gave %v, want ErrPasswordMismatch", wrong)
	}
	// The same error text, deliberately. Anything that distinguishes the two
	// hands somebody a way to find out which usernames exist.
	if wrong.Error() != missing.Error() {
		t.Errorf("a wrong password says %q but an unknown user says %q; these must match",
			wrong, missing)
	}
}

func TestPasswordIsNeverStoredInTheClear(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)

	const secret = "the password itself"
	a, err := s.SignUp("reza", "Reza", secret, at(0))
	if err != nil {
		t.Fatal(err)
	}

	var stored string
	if err := db.QueryRow(`SELECT password_hash FROM accounts WHERE id = ?`, a.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, secret) {
		t.Fatal("the password appears in the database")
	}
	if !strings.HasPrefix(stored, "$argon2id$") {
		t.Errorf("stored hash = %q, want an argon2id hash", stored)
	}
}

func TestUsernamesAreClaimedOnceRegardlessOfCase(t *testing.T) {
	s := newStore(t)
	if _, err := s.SignUp("reza", "Reza", "a long enough password", at(0)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SignUp("REZA", "Someone Else", "a long enough password", at(1)); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("got %v, want ErrUsernameTaken", err)
	}
}

func TestSignUpRejectsWhatCannotBeTypedOrSeen(t *testing.T) {
	s := newStore(t)
	cases := []struct {
		name             string
		user, disp, pass string
		want             error
	}{
		{"username too short", "ab", "Ab", "a long enough password", ErrBadUsername},
		{"username with a space", "re za", "Reza", "a long enough password", ErrBadUsername},
		{"username with Persian letters", "رضا", "Reza", "a long enough password", ErrBadUsername},
		{"display name empty", "reza", " ", "a long enough password", ErrBadDisplayName},
		{"display name with a control character", "reza", "Re\tza", "a long enough password", ErrBadDisplayName},
		{"password too short", "reza", "Reza", "short", ErrBadPassword},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := s.SignUp(c.user, c.disp, c.pass, at(0)); !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
}

// A Persian display name is counted in characters, not bytes. "علی" is three
// letters and six bytes; a byte-counted limit of twenty would let somebody
// have a name three times longer than an English speaker's.
func TestDisplayNameIsCountedInCharacters(t *testing.T) {
	twenty := strings.Repeat("ع", 20)
	if _, err := CleanDisplayName(twenty); err != nil {
		t.Errorf("twenty Persian letters were rejected: %v", err)
	}
	if _, err := CleanDisplayName(twenty + "ع"); !errors.Is(err, ErrBadDisplayName) {
		t.Errorf("twenty-one letters were accepted")
	}
}

func TestSessionsResolveRotateAndExpire(t *testing.T) {
	s := newStore(t)
	a, err := s.SignUp("reza", "Reza", "a long enough password", at(0))
	if err != nil {
		t.Fatal(err)
	}

	token, err := s.StartSession(a.ID, "PC-1", at(0))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Resolve(token, at(1)); err != nil || got.ID != a.ID {
		t.Fatalf("Resolve = %v, %v; want the account back", got.ID, err)
	}

	// Sliding expiry: being seen at minute 1 pushes the deadline out, so a
	// player who uses the app daily is never signed out mid-week.
	almost := at(1).Add(SessionLifetime - time.Minute)
	if _, err := s.Resolve(token, almost); err != nil {
		t.Fatalf("a session in use expired early: %v", err)
	}
	// ...and one nobody has touched for the full lifetime does expire.
	stale := almost.Add(SessionLifetime + time.Minute)
	if _, err := s.Resolve(token, stale); !errors.Is(err, ErrSessionUnknown) {
		t.Fatalf("got %v, want ErrSessionUnknown", err)
	}
	// An expired session is removed, not merely refused.
	if _, err := s.Resolve(token, stale); !errors.Is(err, ErrSessionUnknown) {
		t.Fatalf("got %v, want ErrSessionUnknown", err)
	}
}

func TestOnlyTheHashOfASessionTokenIsStored(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)

	a, _ := s.SignUp("reza", "Reza", "a long enough password", at(0))
	token, err := s.StartSession(a.ID, "PC-1", at(0))
	if err != nil {
		t.Fatal(err)
	}

	var stored string
	if err := db.QueryRow(`SELECT token_hash FROM sessions WHERE account_id = ?`, a.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token {
		t.Fatal("the session token itself is in the database; a copy of the file would be a set of working logins")
	}
}

func TestChangingAPasswordSignsEveryDeviceOut(t *testing.T) {
	s := newStore(t)
	a, _ := s.SignUp("reza", "Reza", "a long enough password", at(0))

	phone, _ := s.StartSession(a.ID, "phone", at(0))
	desktop, _ := s.StartSession(a.ID, "desktop", at(0))

	if err := s.ChangePassword(a.ID, "a long enough password", "a different long password"); err != nil {
		t.Fatalf("changing password: %v", err)
	}
	for name, token := range map[string]string{"phone": phone, "desktop": desktop} {
		if _, err := s.Resolve(token, at(1)); !errors.Is(err, ErrSessionUnknown) {
			t.Errorf("the %s session survived a password change", name)
		}
	}
	if _, err := s.Authenticate("reza", "a different long password", at(1)); err != nil {
		t.Errorf("the new password does not work: %v", err)
	}
	if _, err := s.Authenticate("reza", "a long enough password", at(1)); !errors.Is(err, ErrPasswordMismatch) {
		t.Error("the old password still works")
	}
}

func TestChangingAPasswordRequiresTheCurrentOne(t *testing.T) {
	s := newStore(t)
	a, _ := s.SignUp("reza", "Reza", "a long enough password", at(0))
	if err := s.ChangePassword(a.ID, "a guess", "a different long password"); !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("got %v, want ErrPasswordMismatch", err)
	}
}

// D37, the consequence the signup screen has to state before the fact rather
// than after it: with no email and no SMS there is no way to prove an account
// is yours, so a forgotten password is a lost account.
func TestAnAccountWithNoVerifiedContactCannotRecoverItsPassword(t *testing.T) {
	s := newStore(t)
	a, _ := s.SignUp("reza", "Reza", "a long enough password", at(0))

	can, err := s.CanRecoverPassword(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if can {
		t.Fatal("recovery was offered to an account with nothing to recover through")
	}
	contacts, err := s.Contacts(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts) != 0 {
		t.Fatalf("contacts = %d, want the table to ship empty", len(contacts))
	}
}

// The other half of D37: the seam is real, so the day email is switched on it
// is a feature rather than a migration.
func TestAVerifiedContactTurnsRecoveryOn(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	a, _ := s.SignUp("reza", "Reza", "a long enough password", at(0))

	insert := func(kind, value string, verified interface{}) {
		t.Helper()
		_, err := db.Exec(
			`INSERT INTO contact_methods (id, account_id, kind, value, verified_at, created_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			"c_"+value, a.ID, kind, value, verified, stamp(at(0)))
		if err != nil {
			t.Fatal(err)
		}
	}

	// Unverified is not enough: anybody can type somebody else's address.
	insert("email", "typed@example.com", nil)
	if can, _ := s.CanRecoverPassword(a.ID); can {
		t.Fatal("an unverified address was treated as a recovery route")
	}

	insert("email", "proven@example.com", stamp(at(1)))
	if can, _ := s.CanRecoverPassword(a.ID); !can {
		t.Fatal("a verified address did not enable recovery")
	}

	contacts, err := s.Contacts(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts) != 2 {
		t.Fatalf("contacts = %d, want 2", len(contacts))
	}
}

func TestTermsAcceptanceIsRecordedPerVersion(t *testing.T) {
	s := newStore(t)
	a, _ := s.SignUp("reza", "Reza", "a long enough password", at(0))

	if ok, _ := s.HasAcceptedTerms(a.ID, "2026-08-24"); ok {
		t.Fatal("terms were accepted before anybody was asked")
	}
	if err := s.AcceptTerms(a.ID, "2026-08-24", at(0)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.HasAcceptedTerms(a.ID, "2026-08-24"); !ok {
		t.Fatal("acceptance was not recorded")
	}
	// A new version is a new question. This is what makes people re-prompt
	// when the text changes, rather than silently inheriting an old consent.
	if ok, _ := s.HasAcceptedTerms(a.ID, "2027-01-01"); ok {
		t.Fatal("accepting one version accepted a later one")
	}
	// Accepting twice is not an error - a client that retries must not fail.
	if err := s.AcceptTerms(a.ID, "2026-08-24", at(5)); err != nil {
		t.Fatalf("re-accepting failed: %v", err)
	}
}

func TestDisabledAccountsCannotSignInOrKeepASession(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)

	a, _ := s.SignUp("pest", "Pest", "a long enough password", at(0))
	token, _ := s.StartSession(a.ID, "PC-1", at(0))

	if _, err := db.Exec(`UPDATE accounts SET disabled_at = ? WHERE id = ?`, stamp(at(1)), a.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Authenticate("pest", "a long enough password", at(2)); !errors.Is(err, ErrDisabled) {
		t.Errorf("a banned account signed in: %v", err)
	}
	// A ban that only stops new sign-ins leaves the banned person online
	// until they happen to restart the app, which is no ban at all.
	if _, err := s.Resolve(token, at(2)); !errors.Is(err, ErrDisabled) {
		t.Errorf("a banned account kept its session: %v", err)
	}
}

func TestDeletingAnAccountTakesItsSessionsWithIt(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)

	a, _ := s.SignUp("reza", "Reza", "a long enough password", at(0))
	if _, err := s.StartSession(a.ID, "PC-1", at(0)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM accounts WHERE id = ?`, a.ID); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE account_id = ?`, a.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d sessions outlived the account they belonged to; foreign keys are not on", n)
	}
}

func TestGetReportsAMissingAccountPlainly(t *testing.T) {
	s := newStore(t)
	if _, err := s.Get("a_nothing"); !errors.Is(err, ErrNoSuchAccount) {
		t.Fatalf("got %v, want ErrNoSuchAccount", err)
	}
}

func errFrom(_ Account, err error) error { return err }
