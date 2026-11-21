package auth

import (
	"context"
	"crypto/md5" //nolint:gosec // phpass reference
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Vectors produced by WordPress 7.1's own code (wp-includes/class-phpass.php, wp_hash_password() with
// cost 5 to keep the test fast, PHP's password_hash() and md5()), except the first phpass one, which is
// the reference "test12345" vector shipped with phpass 0.3's test.php.
var wpVectors = []struct {
	name, password, hash, kind string
}{
	{"phpass reference", "test12345", "$P$9IQRaTwmfeRo7ud9Fh4E2PdI0S3r.L0", "phpass"},
	{"phpass wordpress", "password", "$P$B2quFTY/dB/kUQqA.Jj1pGh9WakUwP.", "phpass"},
	{"phpass utf-8", "Pursae çöğ ünicode", "$P$B7/q87WSHEk.NFxhHlPskHYVto2ewd1", "phpass"},
	{"phpass long", strings.Repeat("a", 100), "$P$BEk5jdyKO3r4x52/JxMBVnir7aqCJR1", "phpass"},
	{"phpass $H$", "password", "$H$BxcasPH4/a0J1jjVQUCW.1Rl4UG5YV/", "phpass"},
	{"wp 6.8 bcrypt", "password", "$wp$2y$05$u//MIaiTMd889nQrv4tRLe6SFvcZuLdJ3PrPDBY/vdbwr4JX/Ye9u", "wp-bcrypt"},
	{"wp 6.8 bcrypt long", strings.Repeat("b", 100), "$wp$2y$05$6feXh/EXlSq1nwxxTyVJdunpISfipQ3gNuOD0gmi4LxLGHjZQdOIi", "wp-bcrypt"},
	{"plain bcrypt", "password", "$2y$05$AAp6WbBcI7IPPADPfJFc0ew7e0BHKQN50PoHcW1YTnG5IGadAye.q", "bcrypt"},
	{"plain bcrypt over 72 bytes", strings.Repeat("c", 100), "$2y$05$M.h6XBlnTMCgpMnbw/nfROW4jI7xSHRe.8OKTbQWEDKRDs8E3W7d2", "bcrypt"},
	{"md5", "password", "5f4dcc3b5aa765d61d8327deb882cf99", "md5"},
}

func TestCheckUserPasswordWordPressVectors(t *testing.T) {
	ctx := context.Background()
	for _, v := range wpVectors {
		t.Run(v.name, func(t *testing.T) {
			if got := HashKind(v.hash); got != v.kind {
				t.Fatalf("HashKind = %q, want %q", got, v.kind)
			}
			if !IsWordPressHash(v.hash) {
				t.Error("IsWordPressHash = false")
			}
			ok, rehash, err := CheckUserPassword(ctx, v.password, v.hash)
			if err != nil || !ok || !rehash {
				t.Fatalf("correct password: ok=%v rehash=%v err=%v", ok, rehash, err)
			}
			for _, wrong := range []string{"x" + v.password, strings.ToUpper(v.password), "", " " + v.password} {
				if ok, rehash, err := CheckUserPassword(ctx, wrong, v.hash); ok || rehash || err != nil {
					t.Errorf("wrong password %q: ok=%v rehash=%v err=%v", wrong, ok, rehash, err)
				}
			}
		})
	}
	// password_verify() and the wp-sha384 pre-hash: the 6.8 format is not plain bcrypt of the password,
	// and plain bcrypt is not read as the 6.8 format.
	if ok, _, _ := CheckUserPassword(ctx, "password", "$wp$2y$05$AAp6WbBcI7IPPADPfJFc0ew7e0BHKQN50PoHcW1YTnG5IGadAye.q"); ok {
		t.Error("$wp$ prefix verified the password without the HMAC pre-hash")
	}
	// bcrypt only reads 72 bytes, like PHP.
	if ok, _, _ := CheckUserPassword(ctx, strings.Repeat("c", 72)+"different tail", wpVectors[8].hash); !ok {
		t.Error("bcrypt did not ignore bytes after the 72nd like password_verify()")
	}
}

// An independent phpass: the textbook definition written out step by step, cross-checked with
// phpassCrypt on hashes it produces itself (any salt, any round count within the cap).
func TestPhpassMatchesReference(t *testing.T) {
	ref := func(password, salt string, log2 int) string {
		const itoa = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
		h := md5sum([]byte(salt + password))
		for i := 0; i < 1<<log2; i++ {
			h = md5sum(append(h, []byte(password)...))
		}
		// 16 bytes as 5 little-endian 24-bit groups plus one byte → 22 characters.
		out := ""
		for i := 0; i < 16; i += 3 {
			v := int(h[i])
			n := 2
			if i+1 < 16 {
				v |= int(h[i+1]) << 8
				n = 3
			}
			if i+2 < 16 {
				v |= int(h[i+2]) << 16
				n = 4
			}
			for j := 0; j < n; j++ {
				out += string(itoa[(v>>(6*j))&0x3f])
			}
		}
		return "$P$" + string(itoa[log2]) + salt + out
	}
	for _, c := range []struct {
		pw, salt string
		log2     int
	}{{"password", "2quFTY/d", 13}, {"test12345", "IQRaTwmf", 11}, {"", "abcdefgh", 7}, {"x", "./././..", 16}} {
		want := ref(c.pw, c.salt, c.log2)
		if got := phpassCrypt(c.pw, want); got != want {
			t.Errorf("phpassCrypt(%q) = %q, reference %q", c.pw, got, want)
		}
	}
	if ref("password", "2quFTY/d", 13) != "$P$B2quFTY/dB/kUQqA.Jj1pGh9WakUwP." {
		t.Error("reference phpass disagrees with the WordPress vector")
	}
}

func md5sum(b []byte) []byte {
	s := md5.Sum(b) //nolint:gosec // phpass reference
	return s[:]
}

func TestCheckUserPasswordRejectsMalformed(t *testing.T) {
	ctx := context.Background()
	slow, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	tooCostly := "$2y$15" + string(slow[6:]) // cost above maxBcryptCost
	for _, h := range []string{
		"", "*", "password", "$P$", "$P$B2quFTY/dB/kUQqA.Jj1pGh9WakUwP", // truncated
		"$P$B2quFTY/dB/kUQqA.Jj1pGh9WakUwP.x", // too long
		"$P$32quFTY/dB/kUQqA.Jj1pGh9WakUwP.",  // 2^5 rounds: below phpass's minimum
		"$P$J2quFTY/dB/kUQqA.Jj1pGh9WakUwP.",  // 2^21 rounds: above the cap
		"$P$B2quFTY/dB/kUQqA.Jj1pGh9WakUw!.",  // outside itoa64
		"$X$B2quFTY/dB/kUQqA.Jj1pGh9WakUwP.",  // unknown prefix
		"5F4DCC3B5AA765D61D8327DEB882CF99",    // uppercase hex never equals md5()
		"5f4dcc3b5aa765d61d8327deb882cf9",     // 31 digits
		"5f4dcc3b5aa765d61d8327deb882cf9g",    // not hex
		tooCostly, "$wp" + tooCostly,
		"$2x" + string(slow[3:]), "$2$" + string(slow[3:]), // unsupported variants
		"$2y$5$" + string(slow[7:]), // one-digit cost
		string(slow[:59]), "$wp$" + string(slow[:59]),
		"$wp$P$B2quFTY/dB/kUQqA.Jj1pGh9WakUwP.",
		"$argon2i$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA",                                                        // 4-byte salt and key
		"$argon2i$v=19$m=1048576,t=2,p=2$YzJBSzV4TUhkMzc3d3laeg$zqU/1IN0/AogfP4cmSJI1vc8lpXRW9/S0sYY2i2jHT0", // 1 GiB: over the cap
		"$argon2i$v=16$m=1024,t=2,p=2$YzJBSzV4TUhkMzc3d3laeg$zqU/1IN0/AogfP4cmSJI1vc8lpXRW9/S0sYY2i2jHT0",    // argon2 1.0
		"$argon2d$v=19$m=1024,t=2,p=2$YzJBSzV4TUhkMzc3d3laeg$zqU/1IN0/AogfP4cmSJI1vc8lpXRW9/S0sYY2i2jHT0",    // argon2d: not password_hash()
	} {
		ok, rehash, err := CheckUserPassword(ctx, "pw", h)
		if ok || rehash || !errors.Is(err, errBadHash) {
			t.Errorf("%q: ok=%v rehash=%v err=%v, want errBadHash", h, ok, rehash, err)
		}
		if IsWordPressHash(h) {
			t.Errorf("%q: IsWordPressHash = true", h)
		}
	}
	// A real bcrypt hash at the cap still verifies.
	if ok, _, err := CheckUserPassword(ctx, "pw", string(slow)); !ok || err != nil {
		t.Errorf("minimum-cost bcrypt: ok=%v err=%v", ok, err)
	}
}

// Passwords over 4096 bytes never match a legacy hash (wp_check_password()), so a huge password
// cannot multiply phpass's rounds.
func TestCheckUserPasswordLengthCap(t *testing.T) {
	long := strings.Repeat("p", maxLegacyPasswordLen+1)
	h := phpassCrypt(long, "$P$Babcdefgh"+strings.Repeat("0", 22))
	if h == "" {
		t.Fatal("phpassCrypt refused a well-formed setting")
	}
	if ok, _, err := CheckUserPassword(context.Background(), long, h); ok || err != nil {
		t.Errorf("4097-byte password: ok=%v err=%v", ok, err)
	}
}

// argon2id hashes go through CheckPasswordCtx and never ask for a rehash; PHP's own argon2id hashes
// (password_hash(PASSWORD_ARGON2ID)) use the same PHC string and verify too.
func TestCheckUserPasswordArgon(t *testing.T) {
	ctx := context.Background()
	h, err := HashPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if ok, rehash, err := CheckUserPassword(ctx, "s3cret", h); !ok || rehash || err != nil {
		t.Fatalf("argon2id: ok=%v rehash=%v err=%v", ok, rehash, err)
	}
	if IsWordPressHash(h) {
		t.Error("argon2id reported as a WordPress hash")
	}
	php := "$argon2id$v=19$m=65536,t=4,p=1$eHlUOXBqYXpaVUI0bkRTag$T0+xyqKdm6w6XcEjgzZvGbLwMYa5jUDZMlKW/AekIng"
	if ok, rehash, err := CheckUserPassword(ctx, "password", php); !ok || rehash || err != nil {
		t.Fatalf("PHP argon2id: ok=%v rehash=%v err=%v", ok, rehash, err)
	}
}

// phpass and bcrypt share the argon2 semaphore: with every slot busy a check gives up when its
// context ends.
func TestCheckUserPasswordWaitsForSlot(t *testing.T) {
	for i := 0; i < cap(argonSem); i++ { // occupy every slot
		argonSem <- struct{}{}
	}
	release := func() {
		for i := 0; i < cap(argonSem); i++ {
			<-argonSem
		}
	}
	for _, v := range []int{0, 5, 7} { // phpass, $wp$ bcrypt, plain bcrypt
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		ok, _, err := CheckUserPassword(ctx, wpVectors[v].password, wpVectors[v].hash)
		cancel()
		if ok || !errors.Is(err, context.DeadlineExceeded) {
			release()
			t.Fatalf("%s with every slot busy: ok=%v err=%v", wpVectors[v].name, ok, err)
		}
	}
	release()
}

// PHP's PASSWORD_ARGON2I hashes (WordPress 6.8+ with the wp_hash_password_algorithm filter) verify
// and ask for a rehash; argon2id and argon2i PHC strings within the import bounds are importable.
func TestCheckUserPasswordArgon2i(t *testing.T) {
	ctx := context.Background()
	// password_hash('rasmuslerdorf', PASSWORD_ARGON2I, ['memory_cost' => 1024, 'time_cost' => 2, 'threads' => 2]) (PHP manual).
	php := "$argon2i$v=19$m=1024,t=2,p=2$YzJBSzV4TUhkMzc3d3laeg$zqU/1IN0/AogfP4cmSJI1vc8lpXRW9/S0sYY2i2jHT0"
	if ok, rehash, err := CheckUserPassword(ctx, "rasmuslerdorf", php); !ok || !rehash || err != nil {
		t.Fatalf("PHP argon2i: ok=%v rehash=%v err=%v", ok, rehash, err)
	}
	if ok, rehash, err := CheckUserPassword(ctx, "wrong", php); ok || rehash || err != nil {
		t.Fatalf("PHP argon2i, wrong password: ok=%v rehash=%v err=%v", ok, rehash, err)
	}
	if IsWordPressHash(php) || !IsImportableHash(php) {
		t.Errorf("argon2i: IsWordPressHash=%v IsImportableHash=%v", IsWordPressHash(php), IsImportableHash(php))
	}
	own, err := HashPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{own, "$argon2id$v=19$m=65536,t=4,p=1$eHlUOXBqYXpaVUI0bkRTag$T0+xyqKdm6w6XcEjgzZvGbLwMYa5jUDZMlKW/AekIng"} {
		if !IsImportableHash(h) {
			t.Errorf("%q: not importable", h)
		}
	}
	for _, h := range []string{
		"$argon2id$v=19$m=4194304,t=4,p=1$eHlUOXBqYXpaVUI0bkRTag$T0+xyqKdm6w6XcEjgzZvGbLwMYa5jUDZMlKW/AekIng", // 4 GiB
		"$argon2id$v=19$m=65536,t=99,p=1$eHlUOXBqYXpaVUI0bkRTag$T0+xyqKdm6w6XcEjgzZvGbLwMYa5jUDZMlKW/AekIng",  // 99 passes
		"$argon2id$v=19$m=65536,t=4,p=1$eHlUOXBqYXpaVUI0bkRTag",                                               // no key
		"$argon2id$v=19$m=65536,t=4,p=1$c2FsdA$aGFzaA",                                                        // 4-byte salt and key
		"$argon2id$",
	} {
		if IsImportableHash(h) {
			t.Errorf("%q: importable", h)
		}
	}
	for _, v := range wpVectors {
		if !IsImportableHash(v.hash) {
			t.Errorf("%s: WordPress hash not importable", v.name)
		}
	}
}
