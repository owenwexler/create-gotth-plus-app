package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Password length limits, following NIST SP 800-63B-4 for passwords that are the only factor: a
// long minimum, a generous maximum, and no rules about which kinds of characters to use.
const (
	MinPasswordLength = 15
	MaxPasswordLength = 256
)

// argon2id parameters: OWASP's recommendation of 19 MiB, 2 passes, 1 thread. They are stored in
// every hash, so raising them later only affects new hashes; NeedsRehash upgrades old ones at login.
const (
	argonMemory  = 19 * 1024 // KiB
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

// ErrPasswordLength means the password is too short or too long.
var ErrPasswordLength = fmt.Errorf("password must be %d to %d characters", MinPasswordLength, MaxPasswordLength)

// CheckPasswordLength enforces the length limits, counting characters rather than bytes.
func CheckPasswordLength(password string) error {
	if n := utf8.RuneCountInString(password); n < MinPasswordLength || n > MaxPasswordLength {
		return ErrPasswordLength
	}
	return nil
}

// HashPassword returns an argon2id hash in the usual encoded form:
// $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

var errBadHash = errors.New("not an argon2id hash")

func decodeHash(encoded string) (argonParams, []byte, []byte, error) {
	var p argonParams
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return p, nil, nil, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return p, nil, nil, errBadHash
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return p, nil, nil, errBadHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return p, nil, nil, errBadHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return p, nil, nil, errBadHash
	}
	return p, salt, key, nil
}

// VerifyPassword reports whether password matches encoded. The comparison is constant-time.
func VerifyPassword(password, encoded string) (bool, error) {
	p, salt, key, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, uint32(len(key)))
	return subtle.ConstantTimeCompare(got, key) == 1, nil
}

// NeedsRehash reports whether encoded was made with weaker parameters than the current ones.
func NeedsRehash(encoded string) bool {
	p, _, key, err := decodeHash(encoded)
	return err != nil || p.memory < argonMemory || p.time < argonTime || len(key) < argonKeyLen
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// VerifyDummy does the work of a real check against a hash that matches nothing. Call it when there
// is no such user (or no password), so the response takes as long as a wrong password and doesn't
// reveal which emails have accounts.
func VerifyDummy(password string) {
	dummyOnce.Do(func() {
		h, err := HashPassword("not a real password: only used to spend the same time as a real check")
		if err != nil {
			panic(err)
		}
		dummyHash = h
	})
	_, _ = VerifyPassword(password, dummyHash)
}
