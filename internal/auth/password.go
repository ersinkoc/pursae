// Package auth handles password hashing, login sessions, nonces and login throttling.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters (RFC 9106 second recommended option, lowered memory for small servers).
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
	saltLen      = 16
)

// argonSem bounds how many argon2id computations run at once. Each one takes argonMemory (64 MiB), so
// without a bound a burst of logins or Basic-auth REST requests could exhaust memory and CPU.
var argonSem = make(chan struct{}, 2*runtime.NumCPU())

// argonIDKey is argon2.IDKey run under argonSem; it gives up when ctx ends while waiting for a slot.
func argonIDKey(ctx context.Context, password, salt []byte, t, mem uint32, p uint8, keyLen uint32) ([]byte, error) {
	select {
	case argonSem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-argonSem }()
	return argon2.IDKey(password, salt, t, mem, p, keyLen), nil
}

// HashPassword returns a PHC-formatted argon2id hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := argonIDKey(context.Background(), []byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	if err != nil {
		return "", err
	}
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

var errBadHash = errors.New("unrecognized password hash")

// CheckPassword compares a password with a hash produced by HashPassword.
func CheckPassword(password, encoded string) (bool, error) {
	return CheckPasswordCtx(context.Background(), password, encoded)
}

// CheckPasswordCtx is CheckPassword that stops waiting for an argon2 slot when ctx ends (the request
// was cancelled); it then reports false with ctx's error.
func CheckPasswordCtx(ctx context.Context, password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errBadHash
	}
	var mem, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &p); err != nil {
		return false, errBadHash
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, errBadHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, errBadHash
	}
	got, err := argonIDKey(ctx, []byte(password), salt, t, mem, p, uint32(len(want)))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// CheckDummyPassword runs the same argon2id work as CheckPassword against a throwaway hash and always
// reports false. Callers use it when the user does not exist, so the response time does not tell an
// unknown user from a wrong password.
func CheckDummyPassword(ctx context.Context, password string) {
	dummyOnce.Do(func() { dummyHash, _ = HashPassword(RandomToken(16)) })
	_, _ = CheckPasswordCtx(ctx, password, dummyHash)
}

// RandomToken returns n random bytes encoded as URL-safe base64.
func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
