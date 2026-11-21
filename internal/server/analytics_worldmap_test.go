package server

import "testing"

func TestM49Of(t *testing.T) {
	for cc, want := range map[string]string{"TR": "792", "US": "840", "DE": "276", "AF": "004", "": "", "XX": ""} {
		if got := m49Of(cc); got != want {
			t.Errorf("m49Of(%q) = %q, want %q", cc, got, want)
		}
	}
}
