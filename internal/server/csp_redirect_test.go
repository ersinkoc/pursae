package server

import (
	"strings"
	"testing"
)

func TestCSPRedirectSource(t *testing.T) {
	for in, want := range map[string]string{
		"https://Client.example/cb?x=1":  "https://client.example",
		"http://localhost:3000/callback": "http://localhost:3000",
		"myapp://oauth/cb":               "myapp:",
		"com.example.app:/cb":            "com.example.app:",
		"":                               "",
		"/relative":                      "",
	} {
		if got := cspRedirectSource(in); got != want {
			t.Errorf("cspRedirectSource(%q) = %q, want %q", in, got, want)
		}
	}
	policy := cspMerge("default-src 'self'; form-action 'self'", map[string][]string{"form-action": {cspRedirectSource("http://localhost:3000/cb")}})
	if !strings.Contains(policy, "form-action 'self' http://localhost:3000") {
		t.Errorf("merged policy: %q", policy)
	}
}
