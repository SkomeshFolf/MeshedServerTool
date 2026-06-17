package auth

import (
	"regexp"
	"testing"
)

// hex64 matches exactly 64 lowercase hex characters, which is the
// encoding of 32 raw bytes via hex.EncodeToString.
var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestHashPasswordAndCheckPassword_RoundTrip(t *testing.T) {
	const pw = "correct horse battery staple"
	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "" {
		t.Fatal("HashPassword returned empty hash")
	}
	if hash == pw {
		t.Fatal("hash should not equal plaintext")
	}
	if !CheckPassword(hash, pw) {
		t.Fatal("CheckPassword should accept the right password")
	}
}

func TestCheckPassword_RejectsWrongPassword(t *testing.T) {
	hash, err := HashPassword("right")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if CheckPassword(hash, "wrong") {
		t.Fatal("CheckPassword should reject a wrong password")
	}
	// Empty password should also be rejected.
	if CheckPassword(hash, "") {
		t.Fatal("CheckPassword should reject empty password against a real hash")
	}
	// Garbage hash should not match.
	if CheckPassword("not-a-bcrypt-hash", "right") {
		t.Fatal("CheckPassword should reject a malformed hash")
	}
}

func TestNewSessionToken_ShapeAndUniqueness(t *testing.T) {
	tok, err := NewSessionToken()
	if err != nil {
		t.Fatalf("NewSessionToken: %v", err)
	}
	if len(tok) != 64 {
		t.Fatalf("token length = %d, want 64 (32 bytes hex-encoded)", len(tok))
	}
	if !hex64.MatchString(tok) {
		t.Fatalf("token %q is not 64 lowercase hex chars", tok)
	}

	// Uniqueness across many calls. With 32 random bytes the chance of
	// collision in 1000 draws is astronomically small, so this catches
	// regressions where the source became non-random or constant.
	const n = 1000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		x, err := NewSessionToken()
		if err != nil {
			t.Fatalf("NewSessionToken[%d]: %v", i, err)
		}
		if _, dup := seen[x]; dup {
			t.Fatalf("token %q repeated at iteration %d", x, i)
		}
		seen[x] = struct{}{}
	}
	if len(seen) != n {
		t.Fatalf("got %d unique tokens, want %d", len(seen), n)
	}
}
