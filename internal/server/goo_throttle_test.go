package server

import (
	"testing"
	"time"
)

func TestGooThrottle(t *testing.T) {
	th := newGooThrottle(3, 50*time.Millisecond)
	for i := 0; i < 3; i++ {
		if th.Blocked("ip") {
			t.Fatalf("blocked too early at %d", i)
		}
		th.Fail("ip")
	}
	if !th.Blocked("ip") {
		t.Error("should be blocked after max failures")
	}
	if th.Blocked("other") {
		t.Error("other keys must not be blocked")
	}
	time.Sleep(70 * time.Millisecond)
	if th.Blocked("ip") {
		t.Error("block should expire with the window")
	}
}
