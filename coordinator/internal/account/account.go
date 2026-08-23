// Package account is identity: who somebody is, how they prove it, and what
// they agreed to.
//
// It replaces the arrangement in D31, where a player was a name they typed
// and an ID generated at install. That was explicitly a test-only measure and
// its consequence was written down: a kicked player who reinstalled came back
// as somebody new, so kicks, room ownership and declared MMR all rested on
// nothing an unwilling person had to respect.
//
// A username and a password, and nothing else (D37). No email, no SMS, no
// third party - all three are unreliable domestically and any of them would
// gate signing up on something that can be blocked. The shape email and SMS
// would need is in the schema from the first migration so that switching them
// on later is a feature rather than a migration.
package account

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

const (
	// SessionLifetime is how long a session lasts without being seen.
	SessionLifetime = 30 * 24 * time.Hour

	minUsername = 3
	maxUsername = 20
	minPassword = 8
	maxPassword = 200

	// MaxDisplayName matches the old nickname limit, counted in runes so
	// Persian and Cyrillic names are not silently shortened.
	MaxDisplayName = 20
)

var (
	ErrUsernameTaken   = errors.New("account: that username is taken")
	ErrNoSuchAccount   = errors.New("account: no such account")
	ErrDisabled        = errors.New("account: this account is disabled")
	ErrBadUsername     = errors.New("account: a username is 3 to 20 characters, letters, digits, dot, dash or underscore")
	ErrBadPassword     = errors.New("account: a password must be at least 8 characters")
	ErrBadDisplayName  = errors.New("account: choose a display name of 2 to 20 characters")
	ErrSessionUnknown  = errors.New("account: session is unknown or has expired")
	ErrNoRecoveryRoute = errors.New("account: this account has no verified email or phone, so its password cannot be reset")
)

// Account is a person.
type Account struct {
	ID          string
	Username    string // folded to lower case; this is the login identifier
	DisplayName string // as typed; this is what other players see
	MMR         int
	MMRSetAt    time.Time
	CreatedAt   time.Time
	Disabled    bool
}

// Contact is an email address or phone number. None exist yet - the table
// ships empty (D37) - but the type is here so the code that will use it is
// written against something real rather than invented later.
type Contact struct {
	ID         string
	Kind       string // "email" or "sms"
	Value      string
	Verified   bool
	VerifiedAt time.Time
}

// Store is accounts, sessions and terms acceptance.
type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

// --- creating and identifying ------------------------------------------

// SignUp creates an account. The username is matched case-insensitively; the
// display name keeps whatever the player typed.
func (s *Store) SignUp(username, displayName, password string, now time.Time) (Account, error) {
	folded, err := CleanUsername(username)
	if err != nil {
		return Account{}, err
	}
	name, err := CleanDisplayName(displayName)
	if err != nil {
		return Account{}, err
	}
	if err := CheckPassword(password); err != nil {
		return Account{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return Account{}, err
	}

	id, err := newID("a_")
	if err != nil {
		return Account{}, err
	}
	a := Account{
		ID:          id,
		Username:    folded,
		DisplayName: name,
		CreatedAt:   now.UTC(),
	}
	_, err = s.db.Exec(
		`INSERT INTO accounts (id, username, display_name, password_hash, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		a.ID, a.Username, a.DisplayName, hash, stamp(a.CreatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return Account{}, ErrUsernameTaken
		}
		return Account{}, fmt.Errorf("account: creating: %w", err)
	}
	return a, nil
}

// Authenticate checks a password and returns the account behind it.
//
// Every failure returns the same error on purpose. Telling somebody that a
// username exists but the password is wrong hands them half of a credential.
func (s *Store) Authenticate(username, password string, now time.Time) (Account, error) {
	folded := strings.ToLower(strings.TrimSpace(username))

	var (
		a    Account
		hash string
	)
	row := s.db.QueryRow(
		`SELECT id, username, display_name, password_hash, mmr, mmr_set_at, created_at, disabled_at
		 FROM accounts WHERE username = ?`, folded)
	disabled, mmrSet, err := scanAccount(row, &a, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		// Spend roughly the time a real verification costs, so the failure
		// for an unknown username is not measurably faster than for a wrong
		// password. Otherwise timing alone enumerates who has an account.
		_, _ = HashPassword(password)
		return Account{}, ErrPasswordMismatch
	}
	if err != nil {
		return Account{}, fmt.Errorf("account: reading: %w", err)
	}
	a.MMRSetAt, a.Disabled = mmrSet, disabled

	if err := VerifyPassword(hash, password); err != nil {
		return Account{}, ErrPasswordMismatch
	}
	if a.Disabled {
		return Account{}, ErrDisabled
	}

	// An old hash is upgraded quietly the moment we know the password is
	// right, which is the only time it is possible.
	if NeedsRehash(hash) {
		if fresh, err := HashPassword(password); err == nil {
			_, _ = s.db.Exec(`UPDATE accounts SET password_hash = ? WHERE id = ?`, fresh, a.ID)
		}
	}
	return a, nil
}

// Get returns an account by ID.
func (s *Store) Get(id string) (Account, error) {
	var (
		a    Account
		hash string
	)
	row := s.db.QueryRow(
		`SELECT id, username, display_name, password_hash, mmr, mmr_set_at, created_at, disabled_at
		 FROM accounts WHERE id = ?`, id)
	disabled, mmrSet, err := scanAccount(row, &a, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNoSuchAccount
	}
	if err != nil {
		return Account{}, fmt.Errorf("account: reading: %w", err)
	}
	a.MMRSetAt, a.Disabled = mmrSet, disabled
	return a, nil
}

// --- sessions -----------------------------------------------------------

// StartSession issues a token for an account. Only its hash is stored, so a
// copy of the database does not hand somebody a working session.
func (s *Store) StartSession(accountID, device string, now time.Time) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("account: generating session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	_, err := s.db.Exec(
		`INSERT INTO sessions (token_hash, account_id, device, created_at, last_seen_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		hashToken(token), accountID, device,
		stamp(now.UTC()), stamp(now.UTC()), stamp(now.UTC().Add(SessionLifetime)))
	if err != nil {
		return "", fmt.Errorf("account: starting session: %w", err)
	}
	return token, nil
}

// Resolve turns a session token back into an account and marks it seen.
func (s *Store) Resolve(token string, now time.Time) (Account, error) {
	var (
		accountID string
		expires   string
	)
	err := s.db.QueryRow(
		`SELECT account_id, expires_at FROM sessions WHERE token_hash = ?`,
		hashToken(token)).Scan(&accountID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrSessionUnknown
	}
	if err != nil {
		return Account{}, fmt.Errorf("account: reading session: %w", err)
	}
	if at, perr := time.Parse(time.RFC3339Nano, expires); perr == nil && now.UTC().After(at) {
		_, _ = s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
		return Account{}, ErrSessionUnknown
	}

	a, err := s.Get(accountID)
	if err != nil {
		return Account{}, err
	}
	if a.Disabled {
		return Account{}, ErrDisabled
	}

	// Sliding expiry: somebody who uses the app keeps their session, and
	// somebody who stops using it loses it.
	_, _ = s.db.Exec(
		`UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE token_hash = ?`,
		stamp(now.UTC()), stamp(now.UTC().Add(SessionLifetime)), hashToken(token))
	return a, nil
}

// EndSession signs one device out.
func (s *Store) EndSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
	return err
}

// EndAllSessions signs every device out. This is what a password change does,
// and what somebody who thinks they were compromised needs.
func (s *Store) EndAllSessions(accountID string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE account_id = ?`, accountID)
	return err
}

// --- passwords ----------------------------------------------------------

// ChangePassword replaces a password, given the current one, and signs every
// other device out.
func (s *Store) ChangePassword(accountID, current, next string) error {
	var hash string
	err := s.db.QueryRow(`SELECT password_hash FROM accounts WHERE id = ?`, accountID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoSuchAccount
	}
	if err != nil {
		return fmt.Errorf("account: reading: %w", err)
	}
	if err := VerifyPassword(hash, current); err != nil {
		return ErrPasswordMismatch
	}
	if err := CheckPassword(next); err != nil {
		return err
	}
	fresh, err := HashPassword(next)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE accounts SET password_hash = ? WHERE id = ?`, fresh, accountID); err != nil {
		return fmt.Errorf("account: changing password: %w", err)
	}
	return s.EndAllSessions(accountID)
}

// CanRecoverPassword reports whether this account has any route back in if
// its password is forgotten.
//
// D37: recovery exists only where a verified contact method exists. With no
// email and no SMS there is nothing to send a reset to and no way to tell the
// real owner from somebody claiming to be them - so the honest answer is that
// the account is gone, and the signup screen has to say so before the fact
// rather than after it.
func (s *Store) CanRecoverPassword(accountID string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM contact_methods WHERE account_id = ? AND verified_at IS NOT NULL`,
		accountID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("account: checking recovery: %w", err)
	}
	return n > 0, nil
}

// Contacts lists an account's email addresses and phone numbers. Always empty
// today; the seam is here so nothing has to be reshaped when it is not.
func (s *Store) Contacts(accountID string) ([]Contact, error) {
	rows, err := s.db.Query(
		`SELECT id, kind, value, verified_at FROM contact_methods WHERE account_id = ? ORDER BY created_at`,
		accountID)
	if err != nil {
		return nil, fmt.Errorf("account: reading contacts: %w", err)
	}
	defer rows.Close()

	var out []Contact
	for rows.Next() {
		var (
			c        Contact
			verified sql.NullString
		)
		if err := rows.Scan(&c.ID, &c.Kind, &c.Value, &verified); err != nil {
			return nil, err
		}
		if verified.Valid {
			if at, err := time.Parse(time.RFC3339Nano, verified.String); err == nil {
				c.Verified, c.VerifiedAt = true, at
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// --- terms --------------------------------------------------------------

// AcceptTerms records that an account agreed to a version of the terms.
func (s *Store) AcceptTerms(accountID, version string, now time.Time) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO terms_acceptance (account_id, version, accepted_at)
		 VALUES (?, ?, ?)`,
		accountID, version, stamp(now.UTC()))
	if err != nil {
		return fmt.Errorf("account: recording terms acceptance: %w", err)
	}
	return nil
}

// HasAcceptedTerms reports whether an account has agreed to a given version.
// A new version is a new row, which is what makes people re-prompt.
func (s *Store) HasAcceptedTerms(accountID, version string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM terms_acceptance WHERE account_id = ? AND version = ?`,
		accountID, version).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("account: reading terms acceptance: %w", err)
	}
	return n > 0, nil
}

// --- validation ---------------------------------------------------------

// CleanUsername folds a login name to lower case and checks its shape.
//
// Deliberately narrow: a username is an identifier people type to sign in, so
// it stays to characters that survive every keyboard and cannot be confused
// with each other. Expressiveness belongs in the display name, which is
// unrestricted apart from its length.
func CleanUsername(username string) (string, error) {
	folded := strings.ToLower(strings.TrimSpace(username))
	n := 0
	for _, r := range folded {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '.' || r == '-' || r == '_':
		default:
			return "", ErrBadUsername
		}
		n++
	}
	if n < minUsername || n > maxUsername {
		return "", ErrBadUsername
	}
	return folded, nil
}

// CleanDisplayName trims a display name and counts it in runes, so a Persian
// or Cyrillic name is measured the way a person would measure it.
func CleanDisplayName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	// Control characters would let somebody paint a name that is not what it
	// looks like in a player list.
	for _, r := range trimmed {
		if unicode.IsControl(r) {
			return "", ErrBadDisplayName
		}
	}
	n := len([]rune(trimmed))
	if n < 2 || n > MaxDisplayName {
		return "", ErrBadDisplayName
	}
	return trimmed, nil
}

// CheckPassword enforces a length floor and nothing else.
//
// No character-class rules on purpose. They push people towards "Password1!"
// while forbidding a long passphrase that is genuinely stronger, and the
// research is clear that length is what matters.
func CheckPassword(password string) error {
	if len(password) < minPassword || len(password) > maxPassword {
		return ErrBadPassword
	}
	return nil
}

// --- helpers ------------------------------------------------------------

func scanAccount(row *sql.Row, a *Account, hash *string) (disabled bool, mmrSet time.Time, err error) {
	var (
		mmrSetAt   sql.NullString
		disabledAt sql.NullString
		createdAt  string
	)
	err = row.Scan(&a.ID, &a.Username, &a.DisplayName, hash, &a.MMR, &mmrSetAt, &createdAt, &disabledAt)
	if err != nil {
		return false, time.Time{}, err
	}
	if at, perr := time.Parse(time.RFC3339Nano, createdAt); perr == nil {
		a.CreatedAt = at
	}
	if mmrSetAt.Valid {
		if at, perr := time.Parse(time.RFC3339Nano, mmrSetAt.String); perr == nil {
			mmrSet = at
		}
	}
	return disabledAt.Valid, mmrSet, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newID(prefix string) (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("account: generating id: %w", err)
	}
	return prefix + hex.EncodeToString(b), nil
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func isUniqueViolation(err error) bool {
	// The pure-Go driver reports this as text rather than a typed error.
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

// --- declared rating ----------------------------------------------------

// ErrMMROutOfRange rejects a rating nobody could hold.
var ErrMMROutOfRange = errors.New("account: MMR must be between 0 and 15000")

// ErrMMRTooSoon enforces the product rule: MMR is self-declared and may be
// changed once a week. Self-declared because there is no way to read a rating
// out of Valve's service from here; once a week because a number somebody can
// retype at will is not a number anybody can match on.
var ErrMMRTooSoon = errors.New("account: MMR can only be changed once a week")

// MMRChangeInterval is how long a declared rating stands.
const MMRChangeInterval = 7 * 24 * time.Hour

// MaxMMR sits above the highest real Dota rating with room to spare.
const MaxMMR = 15000

// SetMMR records a self-declared rating.
//
// The first declaration is free. Re-declaring the same number is not a change
// and is always allowed, so a client that echoes its current value back never
// gets an error for it.
func (s *Store) SetMMR(accountID string, mmr int, now time.Time) (Account, error) {
	if mmr < 0 || mmr > MaxMMR {
		return Account{}, ErrMMROutOfRange
	}
	a, err := s.Get(accountID)
	if err != nil {
		return Account{}, err
	}
	if mmr == a.MMR && !a.MMRSetAt.IsZero() {
		return a, nil
	}
	if !a.MMRSetAt.IsZero() && now.UTC().Before(a.MMRSetAt.Add(MMRChangeInterval)) {
		return Account{}, ErrMMRTooSoon
	}
	if _, err := s.db.Exec(
		`UPDATE accounts SET mmr = ?, mmr_set_at = ? WHERE id = ?`,
		mmr, stamp(now.UTC()), accountID); err != nil {
		return Account{}, fmt.Errorf("account: setting MMR: %w", err)
	}
	a.MMR, a.MMRSetAt = mmr, now.UTC()
	return a, nil
}

// SetDisplayName changes the name other players see. The username - what you
// sign in with - never changes; letting it change would mean a kick record or
// a friendship could be shaken off by renaming.
func (s *Store) SetDisplayName(accountID, name string, now time.Time) (Account, error) {
	clean, err := CleanDisplayName(name)
	if err != nil {
		return Account{}, err
	}
	res, err := s.db.Exec(`UPDATE accounts SET display_name = ? WHERE id = ?`, clean, accountID)
	if err != nil {
		return Account{}, fmt.Errorf("account: setting display name: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Account{}, ErrNoSuchAccount
	}
	return s.Get(accountID)
}

// SetDisabled bans or unbans an account. A ban takes effect immediately: every
// session is dropped, so the banned person is offline now rather than the next
// time they happen to restart the app.
func (s *Store) SetDisabled(accountID string, disabled bool, now time.Time) error {
	var at interface{}
	if disabled {
		at = stamp(now.UTC())
	}
	res, err := s.db.Exec(`UPDATE accounts SET disabled_at = ? WHERE id = ?`, at, accountID)
	if err != nil {
		return fmt.Errorf("account: setting disabled: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSuchAccount
	}
	if disabled {
		return s.EndAllSessions(accountID)
	}
	return nil
}

// ByUsername finds an account by its login name, which must already be
// folded. It is how a friend request finds somebody to be addressed to.
func (s *Store) ByUsername(folded string) (Account, error) {
	var (
		a    Account
		hash string
	)
	row := s.db.QueryRow(
		`SELECT id, username, display_name, password_hash, mmr, mmr_set_at, created_at, disabled_at
		 FROM accounts WHERE username = ?`, folded)
	disabled, mmrSet, err := scanAccount(row, &a, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNoSuchAccount
	}
	if err != nil {
		return Account{}, fmt.Errorf("account: reading: %w", err)
	}
	a.MMRSetAt, a.Disabled = mmrSet, disabled
	return a, nil
}
