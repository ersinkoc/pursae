package auth

import (
	"strings"
	"testing"
)

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$") {
		t.Fatalf("unexpected hash format %q", h)
	}
	if ok, _ := CheckPassword("s3cret", h); !ok {
		t.Error("correct password rejected")
	}
	if ok, _ := CheckPassword("wrong", h); ok {
		t.Error("wrong password accepted")
	}
}

func TestNonce(t *testing.T) {
	m := NewManager(nil, "secret")
	n := m.Nonce("delete-post_5", 1, "tok")
	if m.VerifyNonce(n, "delete-post_5", 1, "tok") != 1 {
		t.Error("nonce did not verify")
	}
	for _, c := range []struct {
		action string
		user   int64
		token  string
	}{{"delete-post_6", 1, "tok"}, {"delete-post_5", 2, "tok"}, {"delete-post_5", 1, "other"}} {
		if m.VerifyNonce(n, c.action, c.user, c.token) != 0 {
			t.Errorf("nonce verified for %+v", c)
		}
	}
}
