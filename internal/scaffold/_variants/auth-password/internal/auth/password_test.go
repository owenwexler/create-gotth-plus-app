package auth

import (
	"strings"
	"testing"
)

func TestPasswordHash(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("hash %q", hash)
	}
	if ok, err := VerifyPassword("correct horse battery staple", hash); !ok || err != nil {
		t.Errorf("right password: %v %v", ok, err)
	}
	if ok, _ := VerifyPassword("correct horse battery stapler", hash); ok {
		t.Error("wrong password accepted")
	}
	if NeedsRehash(hash) {
		t.Error("a new hash needs rehashing")
	}
	again, _ := HashPassword("correct horse battery staple")
	if again == hash {
		t.Error("two hashes of one password are equal: no random salt")
	}
}

func TestNeedsRehash(t *testing.T) {
	weak := "$argon2id$v=19$m=4096,t=1,p=1$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g"
	if !NeedsRehash(weak) {
		t.Error("weaker parameters not flagged")
	}
	if _, err := VerifyPassword("x", "$2a$10$bcrypt"); err == nil {
		t.Error("a non-argon2id hash was accepted")
	}
}

func TestCheckPasswordLength(t *testing.T) {
	for pw, ok := range map[string]bool{
		strings.Repeat("a", 14):  false,
		strings.Repeat("a", 15):  true,
		strings.Repeat("é", 15):  true, // characters, not bytes
		strings.Repeat("a", 256): true,
		strings.Repeat("a", 257): false,
	} {
		if err := CheckPasswordLength(pw); (err == nil) != ok {
			t.Errorf("%d characters: %v", len([]rune(pw)), err)
		}
	}
}
