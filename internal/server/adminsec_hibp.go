package server

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

// Opt-in breached-password check (Pursae Security › Passwords): the Have I Been Pwned "range" API
// with k-anonymity. Only the first five hex characters of the password's SHA-1 leave the server;
// the API answers every suffix with that prefix and the match is done here. A lookup that fails
// (network, timeout) never blocks the password: the other policy rules still apply.

// asHIBPEndpoint is the range API (a variable so tests can point it at an httptest server).
var asHIBPEndpoint = "https://api.pwnedpasswords.com/range/"

var asHIBPClient = &http.Client{Timeout: 4 * time.Second}

// asPasswordBreached returns how often pw appears in known breaches (0: not found or unknown).
func asPasswordBreached(ctx context.Context, pw string) int {
	sum := sha1.Sum([]byte(pw))
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	prefix, suffix := h[:5], h[5:]
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asHIBPEndpoint+prefix, nil)
	if err != nil {
		return 0
	}
	req.Header.Set("Add-Padding", "true") // padded responses hide the real size of the answer
	req.Header.Set("User-Agent", "Pursae-Security")
	resp, err := asHIBPClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		suf, cnt, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(suf, suffix) {
			continue
		}
		n := 0
		for _, ch := range cnt {
			if ch < '0' || ch > '9' {
				break
			}
			n = n*10 + int(ch-'0')
		}
		return n // padding rows carry a count of 0
	}
	return 0
}
