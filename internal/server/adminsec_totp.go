package server

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"
)

// TOTP (RFC 6238 over RFC 4226 HOTP): HMAC-SHA1, 6 digits, 30-second steps, ±1 step of clock
// drift. Replay protection is the caller's: a step at or below the last accepted one is refused.

const (
	totpPeriod = 30
	totpDigits = 6
	totpDrift  = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// newTOTPSecret is 160 random bits, base32 (the key length RFC 4226 recommends).
func newTOTPSecret() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return b32.EncodeToString(b)
}

func decodeTOTPSecret(secret string) ([]byte, error) {
	return b32.DecodeString(strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", "")))
}

// hotp is RFC 4226's HOTP value of counter.
func hotp(key []byte, counter uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[off])&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])
	return fmt.Sprintf("%0*d", totpDigits, bin%1_000_000)
}

func totpStep(t time.Time) int64 { return t.Unix() / totpPeriod }

// totpCode is the code of the step at t (the tests and the setup preview use it).
func totpCode(secret string, t time.Time) string {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return ""
	}
	return hotp(key, uint64(totpStep(t)))
}

// totpMatch finds the step within the drift window whose code is code; ok is false when none is,
// or when that step is not newer than lastStep (a replayed code).
func totpMatch(secret, code string, now time.Time, lastStep int64) (step int64, ok bool) {
	code = normalizeCode(code)
	key, err := decodeTOTPSecret(secret)
	if err != nil || len(code) != totpDigits {
		return 0, false
	}
	cur := totpStep(now)
	for d := -totpDrift; d <= totpDrift; d++ {
		s := cur + int64(d)
		if s <= lastStep {
			continue
		}
		if hmac.Equal([]byte(hotp(key, uint64(s))), []byte(code)) {
			return s, true
		}
	}
	return 0, false
}

// normalizeCode drops spaces and dashes people type into codes.
func normalizeCode(v string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '\t' {
			return -1
		}
		return r
	}, strings.TrimSpace(v))
}

// otpauthURI is the Key URI Format authenticator apps read from the QR code.
func otpauthURI(issuer, account, secret string) string {
	// Spaces must be %20 (not "+"): several apps show a "+" literally.
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	return "otpauth://totp/" + label + "?secret=" + secret + "&issuer=" + url.PathEscape(issuer) + "&algorithm=SHA1&digits=6&period=30"
}

// groupKey shows a base32 key in blocks of four for typing it by hand.
func groupKey(k string) string {
	var parts []string
	for len(k) > 4 {
		parts = append(parts, k[:4])
		k = k[4:]
	}
	return strings.Join(append(parts, k), " ")
}

// randomDigits is a uniformly random n-digit string (e-mailed codes).
func randomDigits(n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		d, _ := rand.Int(rand.Reader, big.NewInt(10))
		sb.WriteByte(byte('0' + d.Int64()))
	}
	return sb.String()
}

// backupAlphabet leaves out look-alike characters (0/o, 1/l/i).
const backupAlphabet = "23456789abcdefghjkmnpqrstuvwxyz"

func newBackupCode() string {
	var sb strings.Builder
	for i := 0; i < 10; i++ {
		if i == 5 {
			sb.WriteByte('-')
		}
		d, _ := rand.Int(rand.Reader, big.NewInt(int64(len(backupAlphabet))))
		sb.WriteByte(backupAlphabet[d.Int64()])
	}
	return sb.String()
}

// --- keyed hashes and sealing ------------------------------------------------------------------

func (s *Server) asSecret() string {
	if s.cfg.Secret != "" {
		return s.cfg.Secret
	}
	return s.settings.String("auth_secret")
}

// asHash is an HMAC of v under the server secret, for codes kept only as hashes.
func (s *Server) asHash(purpose, v string) string {
	mac := hmac.New(sha256.New, []byte("adminsecgo|"+purpose+"|"+s.asSecret()))
	mac.Write([]byte(v))
	return hex.EncodeToString(mac.Sum(nil))
}

const asSealPrefix = "asg:v1:"

func (s *Server) asKey() []byte {
	k := sha256.Sum256([]byte("adminsecgo-seal|" + s.asSecret()))
	return k[:]
}

// asSeal encrypts a TOTP secret for storage (AES-256-GCM).
func (s *Server) asSeal(plain string) string {
	if plain == "" {
		return ""
	}
	block, _ := aes.NewCipher(s.asKey())
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	_, _ = rand.Read(nonce)
	return asSealPrefix + base64.RawStdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil))
}

func (s *Server) asReveal(v string) string {
	raw, ok := strings.CutPrefix(v, asSealPrefix)
	if !ok {
		return ""
	}
	b, err := base64.RawStdEncoding.DecodeString(raw)
	if err != nil {
		return ""
	}
	block, _ := aes.NewCipher(s.asKey())
	gcm, _ := cipher.NewGCM(block)
	if len(b) < gcm.NonceSize() {
		return ""
	}
	out, err := gcm.Open(nil, b[:gcm.NonceSize()], b[gcm.NonceSize():], nil)
	if err != nil {
		return ""
	}
	return string(out)
}
