// --- FILE service.https ---

package crypto

import (
	"encoding/base64"
	"testing"

	"golang.org/x/crypto/argon2"
)

func TestHashPasswordRoundTrip(t *testing.T) {
	hash, salt, err := HashPassword("correct horse")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !ComparePasswords("correct horse", hash, salt) {
		t.Fatal("matching password rejected")
	}
	if ComparePasswords("wrong horse", hash, salt) {
		t.Fatal("wrong password accepted")
	}
	if ComparePasswords("", hash, salt) {
		t.Fatal("empty password accepted")
	}
}

func TestHashPasswordSaltIsRawBytes(t *testing.T) {
	hash, salt, err := HashPassword("pw")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	rawSalt, err := base64.RawURLEncoding.DecodeString(salt)
	if err != nil {
		t.Fatalf("salt is not raw URL base64: %v", err)
	}
	if len(rawSalt) != saltBytes {
		t.Fatalf("salt length = %d bytes, want %d", len(rawSalt), saltBytes)
	}
	// An independent Argon2id computation over the decoded salt must reproduce
	// the stored hash; this is what makes the stored pair interoperable.
	want := base64.URLEncoding.EncodeToString(
		argon2.IDKey([]byte("pw"), rawSalt, argonTime, argonMemory, argonThreads, argonKeyLen))
	if hash != want {
		t.Fatal("stored hash was not derived from the decoded salt")
	}
}

func TestHashPasswordUsesFreshSalts(t *testing.T) {
	_, first, err := HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two hashes reused the same salt")
	}
}

func TestComparePasswordsRejectsMalformedEncodings(t *testing.T) {
	hash, salt, err := HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	if ComparePasswords("pw", "not base64!", salt) {
		t.Fatal("malformed hash accepted")
	}
	if ComparePasswords("pw", hash, "not base64!") {
		t.Fatal("malformed salt accepted")
	}
	if ComparePasswords("pw", hash, "") {
		t.Fatal("empty salt accepted")
	}
}

func TestGenRandomString(t *testing.T) {
	token, err := GenRandomString(32)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("token is not raw URL base64: %v", err)
	}
	if len(raw) != 32 {
		t.Fatalf("decoded token length = %d, want 32", len(raw))
	}
}

func TestHashIsStableHex(t *testing.T) {
	const want = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got := Hash(""); got != want {
		t.Fatalf("Hash(\"\") = %s, want SHA-256 of empty input", got)
	}
}
