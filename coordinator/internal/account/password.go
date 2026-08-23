package account

import "lobbybaz/coordinator/internal/secret"

// Password hashing lives in internal/secret, because room passwords need the
// same primitive and neither package should import the other. These names are
// kept here because an account's password is what most callers mean.
var (
	// ErrPasswordMismatch is deliberately the same error whatever went wrong,
	// so nothing about a failed login distinguishes "no such user" from
	// "wrong password" to somebody probing for valid usernames.
	ErrPasswordMismatch = secret.ErrPasswordMismatch
)

// HashPassword returns an encoded Argon2id hash, salt and parameters included.
func HashPassword(password string) (string, error) { return secret.HashPassword(password) }

// VerifyPassword reports whether password matches the encoded hash.
func VerifyPassword(encoded, password string) error {
	return secret.VerifyPassword(encoded, password)
}

// NeedsRehash reports whether a stored hash was made with weaker parameters
// than we now use, so it can be upgraded at the next successful login.
func NeedsRehash(encoded string) bool { return secret.NeedsRehash(encoded) }
