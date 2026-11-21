package auth

import (
	"context"
	"crypto/hmac"
	"crypto/md5" //nolint:gosec // phpass and the legacy unsalted hash are MD5 by definition; verify-only, rehashed on login
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// WordPress password hashes. Users brought over from a WordPress database keep the user_pass value
// WordPress stored; CheckUserPassword verifies it the way wp_check_password() (WordPress 6.8+) does
// and reports that it should be replaced with an argon2id hash (see the server's login paths).
//
// Accepted formats:
//   - "$wp$2y$…": WordPress 6.8+ — bcrypt over base64(HMAC-SHA384(key "wp-sha384", password)).
//   - "$P$…" / "$H$…": phpass portable hashes (WordPress before 6.8; "$H$" is phpBB's spelling).
//   - "$2a$" / "$2b$" / "$2y$": plain bcrypt (plugins, other imports), checked like password_verify().
//   - 32 lowercase hex digits: the unsalted MD5 that wp_check_password() still accepts.
//   - "$argon2id$…": Pursae's own format (also what PHP's password_hash(PASSWORD_ARGON2ID) writes, and
//     what WordPress 6.8+ stores, without a "$wp" prefix, when wp_hash_password_algorithm picks it).
//   - "$argon2i$…": PHP's PASSWORD_ARGON2I through the same filter; verified, then rehashed to argon2id.
//
// Work factors are capped so a stored hash cannot make one login take seconds of CPU: see
// maxPhpassLog2 and maxBcryptCost. Hashes above the caps are treated as malformed.
const (
	// maxPhpassLog2 caps phpass iterations at 2^16 MD5 rounds. WordPress writes 2^13 ('B': its
	// PasswordHash(8) adds 5), phpBB 2^11 ('9'); phpass itself allows up to 2^30, which is minutes of
	// CPU per guess.
	maxPhpassLog2 = 16
	// minPhpassLog2 is phpass's own lower bound (crypt_private rejects fewer than 2^7 rounds).
	minPhpassLog2 = 7
	// maxBcryptCost caps bcrypt at cost 14 (~1 s); WordPress and PHP default to 10.
	maxBcryptCost = 14
	// maxLegacyPasswordLen is wp_check_password()'s limit: longer passwords never match.
	maxLegacyPasswordLen = 4096
	// Bounds on an imported argon2 hash (and on every "$argon2i$" one): at most 256 MiB (libsodium's
	// "moderate" limit; PHP's default is 64 MiB), 16 passes and 16 lanes, and PHC-sized salt and key.
	maxImportArgonMemory  = 256 * 1024 // KiB
	maxImportArgonTime    = 16
	maxImportArgonThreads = 16
)

const phpassItoa64 = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// HashKind names the format of a stored password hash ("" when it is not one CheckUserPassword knows).
func HashKind(encoded string) string {
	switch {
	case strings.HasPrefix(encoded, "$argon2id$"):
		return "argon2id"
	case strings.HasPrefix(encoded, "$argon2i$"):
		if h, err := parseArgon2(encoded); err == nil && argon2Bounded(h) {
			return "argon2i"
		}
	case strings.HasPrefix(encoded, "$wp$"):
		if bcryptWellFormed(encoded[3:]) {
			return "wp-bcrypt"
		}
	case strings.HasPrefix(encoded, "$P$"), strings.HasPrefix(encoded, "$H$"):
		if phpassWellFormed(encoded) {
			return "phpass"
		}
	case strings.HasPrefix(encoded, "$2"):
		if bcryptWellFormed(encoded) {
			return "bcrypt"
		}
	case isMD5Hex(encoded):
		return "md5"
	}
	return ""
}

// IsWordPressHash reports whether encoded is a well-formed hash in one of the WordPress (or plain
// bcrypt / legacy MD5) formats CheckUserPassword verifies. Importers use it to store such a value
// as-is instead of hashing it again.
func IsWordPressHash(encoded string) bool {
	switch HashKind(encoded) {
	case "wp-bcrypt", "phpass", "bcrypt", "md5":
		return true
	}
	return false
}

// IsImportableHash reports whether encoded may be stored as-is when a user is moved from WordPress:
// one of the IsWordPressHash formats, or a well-formed argon2id / argon2i PHC string within the
// import bounds (WordPress 6.8+ stores plain password_hash() output when the
// wp_hash_password_algorithm filter selects PASSWORD_ARGON2ID or PASSWORD_ARGON2I).
func IsImportableHash(encoded string) bool {
	if IsWordPressHash(encoded) {
		return true
	}
	switch HashKind(encoded) {
	case "argon2id":
		h, err := parseArgon2(encoded)
		return err == nil && argon2Bounded(h)
	case "argon2i":
		return true
	}
	return false
}

// argon2Bounded checks an argon2 hash against the import bounds.
func argon2Bounded(h argon2Hash) bool {
	return h.mem <= maxImportArgonMemory && h.t <= maxImportArgonTime && h.p <= maxImportArgonThreads &&
		len(h.salt) >= 8 && len(h.salt) <= 64 && len(h.key) >= 16 && len(h.key) <= 64
}

// CheckUserPassword verifies a user's password against any supported stored format. rehash is true
// when the password matched a non-argon2id hash: the caller should then store HashPassword(password)
// in its place (wp_password_needs_rehash()). A malformed or unknown hash reports an error and no match.
func CheckUserPassword(ctx context.Context, password, encoded string) (ok, rehash bool, err error) {
	kind := HashKind(encoded)
	if kind == "argon2id" {
		ok, err = CheckPasswordCtx(ctx, password, encoded)
		return ok, false, err
	}
	if kind == "" {
		return false, false, errBadHash
	}
	if len(password) > maxLegacyPasswordLen {
		return false, false, nil
	}
	switch kind {
	case "md5":
		sum := md5.Sum([]byte(password)) //nolint:gosec // legacy WordPress hash, verify-only
		want := hex.EncodeToString(sum[:])
		ok = subtle.ConstantTimeCompare([]byte(want), []byte(encoded)) == 1
	case "phpass":
		ok, err = checkPhpass(ctx, password, encoded)
	case "wp-bcrypt":
		mac := hmac.New(sha512.New384, []byte("wp-sha384"))
		mac.Write([]byte(password))
		pre := base64.StdEncoding.EncodeToString(mac.Sum(nil))
		ok, err = checkBcrypt(ctx, pre, encoded[3:])
	case "bcrypt":
		ok, err = checkBcrypt(ctx, password, encoded)
	case "argon2i":
		ok, err = checkArgon2i(ctx, password, encoded)
	}
	if err != nil {
		return false, false, err
	}
	return ok, ok, nil
}

// isMD5Hex is the 32-hex-digit md5() output wp_check_password() compares for short hashes.
// WordPress compares any hash of 32 bytes or fewer; only lowercase hex can ever match md5().
func isMD5Hex(s string) bool {
	if len(s) != 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// bcryptWellFormed accepts "$2a$", "$2b$" and "$2y$" hashes with a two-digit cost in
// [bcrypt.MinCost, maxBcryptCost] and the 53-character salt+hash in bcrypt's base64 alphabet.
// "$2x$" (the PHP < 5.3.7 bug-compatible variant) and other minors are refused.
func bcryptWellFormed(h string) bool {
	if len(h) != 60 || h[0] != '$' || h[1] != '2' || h[3] != '$' || h[6] != '$' {
		return false
	}
	switch h[2] {
	case 'a', 'b', 'y':
	default:
		return false
	}
	if h[4] < '0' || h[4] > '9' || h[5] < '0' || h[5] > '9' {
		return false
	}
	cost := int(h[4]-'0')*10 + int(h[5]-'0')
	if cost < bcrypt.MinCost || cost > maxBcryptCost {
		return false
	}
	for i := 7; i < len(h); i++ {
		if !strings.ContainsRune(phpassItoa64, rune(h[i])) { // same 64 characters as bcrypt's alphabet
			return false
		}
	}
	return true
}

// checkBcrypt is password_verify() for a bcrypt hash: PHP (like OpenBSD bcrypt) uses at most the
// first 72 bytes of the password. It runs under the same semaphore as argon2id.
func checkBcrypt(ctx context.Context, password, h string) (bool, error) {
	pw := []byte(password)
	if len(pw) > 72 {
		pw = pw[:72]
	}
	if err := acquireHashSlot(ctx); err != nil {
		return false, err
	}
	defer func() { <-argonSem }()
	// CompareHashAndPassword compares in constant time; any error (including a mismatch) is no match.
	return bcrypt.CompareHashAndPassword([]byte(h), pw) == nil, nil
}

// checkArgon2i is password_verify() for a PHP "$argon2i$" hash, under the argon2 semaphore.
func checkArgon2i(ctx context.Context, password, encoded string) (bool, error) {
	h, err := parseArgon2(encoded)
	if err != nil {
		return false, err
	}
	if err := acquireHashSlot(ctx); err != nil {
		return false, err
	}
	defer func() { <-argonSem }()
	got := argon2.Key([]byte(password), h.salt, h.t, h.mem, h.p, uint32(len(h.key)))
	return subtle.ConstantTimeCompare(got, h.key) == 1, nil
}

// acquireHashSlot takes a slot of argonSem (the bound on concurrent password hashing), giving up when
// ctx ends. The caller releases it with <-argonSem.
func acquireHashSlot(ctx context.Context) error {
	select {
	case argonSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// phpassWellFormed accepts the 34-character portable hash: "$P$" or "$H$", one itoa64 character
// giving log2 of the rounds (within [minPhpassLog2, maxPhpassLog2]), an 8-character salt and the
// 22-character encoded MD5.
func phpassWellFormed(h string) bool {
	if len(h) != 34 || (h[:3] != "$P$" && h[:3] != "$H$") {
		return false
	}
	log2 := strings.IndexByte(phpassItoa64, h[3])
	if log2 < minPhpassLog2 || log2 > maxPhpassLog2 {
		return false
	}
	for i := 4; i < len(h); i++ {
		if strings.IndexByte(phpassItoa64, h[i]) < 0 {
			return false
		}
	}
	return true
}

// checkPhpass is PasswordHash::CheckPassword() for portable hashes (crypt_private).
func checkPhpass(ctx context.Context, password, h string) (bool, error) {
	if err := acquireHashSlot(ctx); err != nil {
		return false, err
	}
	defer func() { <-argonSem }()
	got := phpassCrypt(password, h)
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(h)) == 1, nil
}

// phpassCrypt computes the portable hash of password with the setting (prefix, rounds, salt) of h.
func phpassCrypt(password, h string) string {
	if !phpassWellFormed(h) {
		return ""
	}
	count := 1 << strings.IndexByte(phpassItoa64, h[3])
	salt := h[4:12]
	sum := md5.Sum([]byte(salt + password)) //nolint:gosec // phpass is MD5 by definition
	buf := make([]byte, 0, md5.Size+len(password))
	for ; count > 0; count-- {
		buf = append(append(buf[:0], sum[:]...), password...)
		sum = md5.Sum(buf) //nolint:gosec // phpass is MD5 by definition
	}
	return h[:12] + phpassEncode64(sum[:])
}

// phpassEncode64 is PasswordHash::encode64(): each group of up to three bytes, read little-endian,
// becomes one itoa64 character per started 6 bits (2, 3 or 4 characters).
func phpassEncode64(in []byte) string {
	var out strings.Builder
	for len(in) > 0 {
		group := in[:min(3, len(in))]
		in = in[len(group):]
		v := 0
		for k, b := range group {
			v |= int(b) << (8 * k)
		}
		for j := 0; j <= len(group); j++ {
			out.WriteByte(phpassItoa64[(v>>(6*j))&0x3f])
		}
	}
	return out.String()
}
