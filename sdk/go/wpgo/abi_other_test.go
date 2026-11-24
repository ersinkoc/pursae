//go:build !(wasip1 && wasm)

package wpgo

import (
	"errors"
	"testing"
)

func TestRestoreOldHostKeepsCurrentHost(t *testing.T) {
	host := func(name string) func(string, []byte) ([]byte, error) {
		return func(string, []byte) ([]byte, error) {
			return []byte(`{"ok":true,"result":{"found":true,"value":"` + name + `"}}`), nil
		}
	}
	older := SetHostForTest(host("older"))
	t.Cleanup(older)
	gate, done := make(chan struct{}), make(chan struct{})
	go func() {
		<-gate
		older()
		close(done)
	}()
	newer := SetHostForTest(host("newer"))
	t.Cleanup(newer)
	close(gate)
	<-done
	if got, found, err := GetOption("name"); err != nil || !found || got != "newer" {
		t.Fatalf("active host gave %q, found=%v, err=%v", got, found, err)
	}
	newer()
	if _, _, err := GetOption("name"); !errors.Is(err, ErrNotInHost) {
		t.Fatalf("removed host was restored, err=%v", err)
	}
}
