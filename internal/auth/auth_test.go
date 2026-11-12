package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
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

// Sign is stable (no time tick) and depends on the message and the secret.
func TestSign(t *testing.T) {
	m := NewManager(nil, "secret")
	a := m.Sign("postpass|one")
	if a != m.Sign("postpass|one") || len(a) != 64 {
		t.Fatalf("Sign not stable: %q", a)
	}
	if a == m.Sign("postpass|two") || a == NewManager(nil, "other").Sign("postpass|one") {
		t.Error("Sign ignores its message or the secret")
	}
}

// The argon2 semaphore: a check waiting for a slot gives up when its context ends.
func TestCheckPasswordCtxWaitsForSlot(t *testing.T) {
	h, err := HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < cap(argonSem); i++ { // occupy every slot
		argonSem <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ok, err := CheckPasswordCtx(ctx, "pw", h)
	for i := 0; i < cap(argonSem); i++ {
		<-argonSem
	}
	if ok || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CheckPasswordCtx with every slot busy = %v, %v; want false, deadline exceeded", ok, err)
	}
	if ok, err := CheckPasswordCtx(context.Background(), "pw", h); !ok || err != nil {
		t.Fatalf("after slots are free: %v, %v", ok, err)
	}
	CheckDummyPassword(context.Background(), "anything") // must not panic or block
}

func TestCheckPasswordRejectsInvalidArgonParameters(t *testing.T) {
	h, err := HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(h, "$")
	parts[5] = ""
	noSalt := strings.Split(h, "$")
	noSalt[4] = ""
	for _, bad := range []string{
		strings.Replace(h, "$v=19$", "$v=19junk$", 1),
		strings.Replace(h, "$m=65536,", "$m=0,", 1),
		strings.Replace(h, "$m=65536,", "$m=1,", 1),
		strings.Replace(h, ",t=3,", ",t=0,", 1),
		strings.Replace(h, ",p=2$", ",p=0$", 1),
		strings.Replace(h, ",p=2$", ",p=2junk$", 1),
		strings.Replace(h, ",p=2$", ",p=2,x=1$", 1),
		strings.Join(noSalt, "$"),
		strings.Join(parts, "$"),
	} {
		if ok, err := CheckPassword("pw", bad); ok || err == nil {
			t.Errorf("invalid hash: got %v, %v; want false and error", ok, err)
		}
	}
}
