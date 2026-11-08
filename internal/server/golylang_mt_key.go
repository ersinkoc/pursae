package server

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"strings"
)

// Pursae Multi Language: the machine translation API key is stored sealed (AES-GCM with a key
// derived from the server secret, the same scheme as Pursae SMTP's msSeal) in pllOptions.MTKey.
// It is never rendered back into a form or a REST response: screens show pllMTKeyPlaceholder
// and keep the stored key when that placeholder (or nothing) comes back.

const (
	pllSealPrefix = "pllenc:v1:"
	// pllMTKeyPlaceholder is what the settings form shows for a saved key.
	pllMTKeyPlaceholder = "••••••••"
)

func (s *Server) pllMTCipher() cipher.AEAD {
	secret := s.cfg.Secret
	if secret == "" {
		secret = s.settings.String("auth_secret")
	}
	k := sha256.Sum256([]byte("wpgo-golylang-mt|" + secret))
	block, _ := aes.NewCipher(k[:])
	gcm, _ := cipher.NewGCM(block)
	return gcm
}

// pllSealMTKey encrypts an API key for storage; empty stays empty and a sealed value is kept.
func (s *Server) pllSealMTKey(plain string) string {
	plain = strings.TrimSpace(plain)
	if plain == "" || strings.HasPrefix(plain, pllSealPrefix) {
		return plain
	}
	gcm := s.pllMTCipher()
	nonce := make([]byte, gcm.NonceSize())
	_, _ = rand.Read(nonce)
	return pllSealPrefix + base64.RawStdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil))
}

// pllRevealMTKey decrypts a stored key ("" when it cannot be decrypted). A value without the
// prefix is returned as it is (a key put in the option by hand).
func (s *Server) pllRevealMTKey(v string) string {
	raw, ok := strings.CutPrefix(v, pllSealPrefix)
	if !ok {
		return v
	}
	data, err := base64.RawStdEncoding.DecodeString(raw)
	gcm := s.pllMTCipher()
	if err != nil || len(data) < gcm.NonceSize() {
		return ""
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	if err != nil {
		slog.Warn("golylang: the machine translation API key cannot be decrypted (was the server secret changed?)")
		return ""
	}
	return string(plain)
}

// pllMTKey is the plain API key of the Machine translation module ("" when none is saved).
func (s *Server) pllMTKey() string {
	return s.pllRevealMTKey(s.pllOpts().MTKey)
}

// pllMTKeyInput merges a submitted key field into the stored (sealed) value: empty or the
// placeholder keeps the saved key, anything else replaces it. clear removes the key.
func (s *Server) pllMTKeyInput(stored, submitted string, clear bool) string {
	if clear {
		return ""
	}
	submitted = strings.TrimSpace(submitted)
	if submitted == "" || submitted == pllMTKeyPlaceholder {
		return stored
	}
	return s.pllSealMTKey(submitted)
}
