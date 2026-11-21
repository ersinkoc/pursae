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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

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
	h, err := parseArgon2(encoded)
	if err != nil || h.variant != "argon2id" {
		return false, errBadHash
	}
	got, err := argonIDKey(ctx, []byte(password), h.salt, h.t, h.mem, h.p, uint32(len(h.key)))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, h.key) == 1, nil
}

// argon2Hash is a parsed PHC string "$argon2id$v=19$m=…,t=…,p=…$salt$key" (or "$argon2i$…").
type argon2Hash struct {
	variant   string // "argon2id" or "argon2i"
	mem, t    uint32
	p         uint8
	salt, key []byte
}

// parseArgon2 parses the PHC string written by HashPassword and by PHP's password_hash() with
// PASSWORD_ARGON2ID / PASSWORD_ARGON2I. Only version 19 (0x13) is accepted.
func parseArgon2(encoded string) (argon2Hash, error) {
	var h argon2Hash
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || (parts[1] != "argon2id" && parts[1] != "argon2i") {
		return h, errBadHash
	}
	h.variant = parts[1]
	if !strings.HasPrefix(parts[2], "v=") || len(parts[2]) == 2 {
		return h, errBadHash
	}
	version, err := strconv.ParseUint(parts[2][2:], 10, 32)
	if err != nil || int(version) != argon2.Version {
		return h, errBadHash
	}
	params := strings.Split(parts[3], ",")
	parse := func(value, prefix string, bits int) (uint64, error) {
		if !strings.HasPrefix(value, prefix) || len(value) == len(prefix) {
			return 0, errBadHash
		}
		return strconv.ParseUint(value[len(prefix):], 10, bits)
	}
	if len(params) != 3 {
		return h, errBadHash
	}
	mem64, err1 := parse(params[0], "m=", 32)
	t64, err2 := parse(params[1], "t=", 32)
	p64, err3 := parse(params[2], "p=", 8)
	h.mem, h.t, h.p = uint32(mem64), uint32(t64), uint8(p64)
	if err1 != nil || err2 != nil || err3 != nil || h.mem == 0 || h.t == 0 || h.p == 0 || uint64(h.mem) < 8*uint64(h.p) {
		return h, errBadHash
	}
	b64 := base64.RawStdEncoding
	if h.salt, err = b64.DecodeString(parts[4]); err != nil || len(h.salt) == 0 {
		return h, errBadHash
	}
	if h.key, err = b64.DecodeString(parts[5]); err != nil || len(h.key) == 0 {
		return h, errBadHash
	}
	return h, nil
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// CheckDummyPassword runs the same argon2id work as CheckPassword against a throwaway hash and always
// reports false. Callers use it when the user does not exist, so the response time does not tell an
// unknown user from a wrong password.
func CheckDummyPassword(ctx context.Context, password string) {
	dummyChecks.Add(1)
	dummyOnce.Do(func() { dummyHash, _ = HashPassword(RandomToken(16)) })
	_, _ = CheckPasswordCtx(ctx, password, dummyHash)
}

// dummyChecks counts CheckDummyPassword calls, so tests can check that a path pays the argon2id
// pass without timing it (wall-clock comparisons are flaky on shared CI runners).
var dummyChecks atomic.Int64

// DummyPasswordChecks reports how many times CheckDummyPassword has run.
func DummyPasswordChecks() int64 { return dummyChecks.Load() }

// RandomToken returns n random bytes encoded as URL-safe base64.
func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
