// --- FILE service.https ---

// Package crypto provides small hashing/token helpers used by the HTTP auth
// stack (session tokens + Argon2id password hashing).
package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"

	"golang.org/x/crypto/argon2"
)

// Hash returns the hex-encoded SHA256 hash of the input string.
// This is a fast, non-reversible hash suitable for indexing cryptographically
// random tokens. For password hashing, use HashPassword instead.
func Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// GenRandomString generates a cryptographically secure URL and filename random token of the given size.
func GenRandomString(size int) (string, error) {
	b := make([]byte, size)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Argon2id parameters: 3 passes, 32 MiB, 4 lanes, 32-byte tag.
const (
	argonTime    = 3
	argonMemory  = 32 * 1024
	argonThreads = 4
	argonKeyLen  = 32
	saltBytes    = 16
)

// HashPassword returns the base64 (URL) hashed password and the base64
// (raw URL) salt used to hash it. Both encode raw bytes; the salt fed to
// Argon2 is the decoded 16 bytes, not its text form, so the stored values
// interoperate with any standard Argon2id implementation.
//
// Uses Argon2id (RFC 9106's recommended variant): side-channel resistant on
// the first pass, memory-hard against GPU/TMTO attacks on the rest.
func HashPassword(password string) (string, string, error) {
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", "", err
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return base64.URLEncoding.EncodeToString(hash), base64.RawURLEncoding.EncodeToString(salt), nil
}

// ComparePasswords returns true if the plaintext password matches the given
// hash and salt when hashed. Malformed encodings never match.
func ComparePasswords(password, passHash, passSalt string) bool {
	expected, err := base64.URLEncoding.DecodeString(passHash)
	if err != nil {
		return false
	}
	salt, err := base64.RawURLEncoding.DecodeString(passSalt)
	if err != nil {
		return false
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return subtle.ConstantTimeCompare(hash, expected) == 1
}
