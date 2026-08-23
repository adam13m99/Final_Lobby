package secret

import (
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyPassword(h, "correct horse battery staple"); err != nil {
		t.Fatalf("the right password did not verify: %v", err)
	}
	if err := VerifyPassword(h, "correct horse battery stapl"); err == nil {
		t.Fatal("a wrong password verified")
	}
}

// The same password must not produce the same hash twice, or a glance at the
// table would show which people share a password.
func TestEveryHashHasItsOwnSalt(t *testing.T) {
	a, _ := HashPassword("the same password")
	b, _ := HashPassword("the same password")
	if a == b {
		t.Fatal("two hashes of the same password are identical")
	}
}

// The parameters travel with the hash. That is what makes it possible to
// raise the cost later without invalidating anybody's password.
func TestParametersAreEncodedInTheHash(t *testing.T) {
	h, _ := HashPassword("a password")
	for _, want := range []string{"$argon2id$", "m=65536", "t=1", "p=4"} {
		if !strings.Contains(h, want) {
			t.Errorf("hash %q does not carry %q", h, want)
		}
	}
	if NeedsRehash(h) {
		t.Error("a hash made with the current parameters was said to need rehashing")
	}
	// A hash made when the cost was lower is upgraded at the next successful
	// login, which is the only moment the plaintext is available.
	weaker := strings.Replace(h, "m=65536", "m=16384", 1)
	if !NeedsRehash(weaker) {
		t.Error("a hash made with weaker parameters was not flagged")
	}
}

func TestAnUnreadableHashNeverVerifies(t *testing.T) {
	for _, bad := range []string{
		"", "not a hash", "$argon2id$v=19$m=65536,t=1,p=4$only-three-parts",
		"$bcrypt$v=19$m=65536,t=1,p=4$c2FsdA$a2V5",
	} {
		if err := VerifyPassword(bad, "anything"); err == nil {
			t.Errorf("%q verified", bad)
		}
		// And it is flagged for replacement, since it cannot be verified
		// against either.
		if !NeedsRehash(bad) {
			t.Errorf("%q was not flagged for rehashing", bad)
		}
	}
}
