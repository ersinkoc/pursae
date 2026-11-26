package auth

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestClientNet(t *testing.T) {
	for in, want := range map[string]string{
		"198.51.100.7":                  "198.51.100.7",
		" 198.51.100.7 ":                "198.51.100.7",
		"::ffff:198.51.100.7":           "198.51.100.7", // IPv4-mapped: the IPv4 address
		"2001:db8:1:2::1":               "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff:ffff:ffff:1": "2001:db8:1:2::/64",
		"2001:db8:1:3::1":               "2001:db8:1:3::/64",
		"fe80::1%eth0":                  "fe80::/64",
		"":                              "",
		"not-an-ip":                     "not-an-ip",
	} {
		if got := ClientNet(in); got != want {
			t.Errorf("ClientNet(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAccountDelay(t *testing.T) {
	if d := AccountDelay(MaxFailedLoginsPerLogin, MaxFailedLoginsPerLogin); d != 500*time.Millisecond {
		t.Errorf("at the threshold: %v", d)
	}
	if d := AccountDelay(MaxFailedLoginsPerLogin+5, MaxFailedLoginsPerLogin); d != 1500*time.Millisecond {
		t.Errorf("5 past the threshold: %v", d)
	}
	if d := AccountDelay(100000, MaxFailedLoginsPerLogin); d != MaxAccountDelay {
		t.Errorf("capped: %v", d)
	}
}

// fail stores a completed failure at login from ip, as a wrong password leaves it.
func fail(t *testing.T, m *Manager, ip, login string) Attempt {
	t.Helper()
	a, err := m.BeginLogin(context.Background(), ip, login)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Throttled {
		m.AttemptFailed(context.Background(), a.ID)
	}
	return a
}

// O3: failures at an account spread over many networks (here 200 IPv6 /64s, far past every limit) must not
// lock the owner out: a correct password from a network that has not failed is still checked. The
// account is only "under attack": a network that never signed in waits a little, a known one does not.
func TestDistributedFailuresDoNotLockOwner(t *testing.T) {
	m := attemptsManager(t)
	ctx := context.Background()
	m.RecordAttempt(ctx, "2001:db8:aaaa:1::10", "admin", true) // the owner signed in from home yesterday
	for i := 0; i < 200; i++ {
		if a := fail(t, m, fmt.Sprintf("2001:db8:%x::1", i+1), "admin"); a.Throttled {
			t.Fatalf("attempt %d from its own /64 throttled", i)
		}
	}
	// A fresh network: not refused, slowed down (capped).
	a, err := m.BeginLogin(ctx, "198.51.100.50", "admin")
	if err != nil || a.Throttled {
		t.Fatalf("fresh network refused at an account under attack: %+v %v", a, err)
	}
	if a.Delay <= 0 || a.Delay > MaxAccountDelay {
		t.Errorf("fresh network delay %v, want (0, %v]", a.Delay, MaxAccountDelay)
	}
	m.AttemptSucceeded(ctx, a.ID, "198.51.100.50", "admin")
	// The owner's known network, from another address of the same /64: no delay at all.
	a, err = m.BeginLogin(ctx, "2001:db8:aaaa:1::99", "admin")
	if err != nil || a.Throttled || a.Delay != 0 {
		t.Fatalf("known network at an account under attack: %+v %v, want no delay", a, err)
	}
	// The network that just signed in is known now too.
	if a, _ = m.BeginLogin(ctx, "198.51.100.50", "admin"); a.Throttled || a.Delay != 0 {
		t.Errorf("network after its sign-in: %+v", a)
	}
}

// While an account is under attack a fresh network gets MaxFailedPerLoginNetUnderAttack failures there,
// not the full per-network budget, so spreading guesses over many networks gains less per network.
func TestUnderAttackNetBudget(t *testing.T) {
	m := attemptsManager(t)
	for i := 0; i < MaxFailedLoginsPerLogin; i++ {
		fail(t, m, fmt.Sprintf("203.0.113.%d", i+1), "admin")
	}
	for i := 0; i < MaxFailedPerLoginNetUnderAttack; i++ {
		if a := fail(t, m, "198.51.100.60", "admin"); a.Throttled {
			t.Fatalf("guess %d of a fresh network refused", i+1)
		}
	}
	if a := fail(t, m, "198.51.100.60", "admin"); !a.Throttled {
		t.Error("a fresh network got more than its budget at an account under attack")
	}
	// Another account from the same network is not under attack: the normal per-network budget applies.
	if a := fail(t, m, "198.51.100.60", "bob"); a.Throttled || a.Delay != 0 {
		t.Errorf("other account from the network: %+v", a)
	}
}

// Brute force from one network is still refused, whichever addresses of its /64 it uses.
func TestSameNetworkBruteForceThrottled(t *testing.T) {
	m := attemptsManager(t)
	ctx := context.Background()
	for i := 0; i < MaxFailedLogins; i++ {
		if a := fail(t, m, fmt.Sprintf("2001:db8:5:6::%x", i+1), "admin"); a.Throttled {
			t.Fatalf("guess %d refused too early", i+1)
		}
	}
	// One more from yet another address in the same /64: refused, also with the right password.
	if a, _ := m.BeginLogin(ctx, "2001:db8:5:6:dead:beef:0:1", "admin"); !a.Throttled {
		t.Fatal("a sixth guess from the same /64 was not throttled")
	}
	if throttled, _ := m.Throttled(ctx, "2001:db8:5:6::abcd"); !throttled {
		t.Error("Throttled does not share the /64 bucket")
	}
	// The neighbouring /64 is another subscriber: its own budget.
	if a, _ := m.BeginLogin(ctx, "2001:db8:5:7::1", "admin"); a.Throttled {
		t.Error("the next /64 shares the bucket")
	}
	// An IPv4 address is its own bucket.
	for i := 0; i < MaxFailedLogins; i++ {
		fail(t, m, "192.0.2.80", "carol")
	}
	if a, _ := m.BeginLogin(ctx, "192.0.2.80", "carol"); !a.Throttled {
		t.Error("IPv4 brute force not throttled")
	}
	if a, _ := m.BeginLogin(ctx, "192.0.2.81", "carol"); a.Throttled {
		t.Error("the neighbouring IPv4 address shares the bucket")
	}
}

// Application Password checks have their own bucket per network: wrong account passwords do not refuse
// them, and their failures neither refuse account sign-ins nor put the account under attack.
func TestAppPasswordBucket(t *testing.T) {
	m := attemptsManager(t)
	ctx := context.Background()
	for i := 0; i < MaxFailedLogins; i++ {
		fail(t, m, "2001:db8:9:9::1", "admin") // account passwords: the network is now throttled for sign-ins
	}
	if a, _ := m.BeginLogin(ctx, "2001:db8:9:9::2", "admin"); !a.Throttled {
		t.Fatal("setup: sign-ins from the network should be throttled")
	}
	id, throttled, err := m.BeginAppPasswordAttempt(ctx, "2001:db8:9:9::3", "admin", MaxFailedLogins)
	if err != nil || throttled {
		t.Fatalf("an Application Password was refused because of wrong account passwords: %v %v", throttled, err)
	}
	m.DropAttempt(ctx, id)

	for i := 0; i < MaxFailedLogins; i++ {
		id, throttled, _ := m.BeginAppPasswordAttempt(ctx, "203.0.113.200", "bob", MaxFailedLogins)
		if throttled {
			t.Fatalf("app password failure %d refused too early", i+1)
		}
		m.AttemptFailed(ctx, id)
	}
	if _, throttled, _ := m.BeginAppPasswordAttempt(ctx, "203.0.113.200", "bob", MaxFailedLogins); !throttled {
		t.Error("app password failures from one network are not limited")
	}
	if a, _ := m.BeginLogin(ctx, "203.0.113.200", "bob"); a.Throttled || a.Delay != 0 {
		t.Errorf("app password failures refused an account sign-in: %+v", a)
	}
	if p, _ := m.LoginPressure(ctx, "198.51.100.1", "bob", FailedWindow, 0); p.Failures != 1 { // the sign-in above, in flight
		t.Errorf("app password failures counted as account failures: %+v", p)
	}
}
