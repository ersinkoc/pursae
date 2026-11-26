//go:build !devhub

package server

import "sync/atomic"

// devTrustHubKeys: a production build never trusts unendorsed Manager keys, whatever the
// environment says (PURSAE_TRUST_HUB_KEYS is read only by devhub builds).
func devTrustHubKeys() bool { return false }

// devTrustDevKey: a production build never trusts the public demo key of the example plugins
// (PURSAE_TRUST_DEV_KEY is read only by devhub builds).
func devTrustDevKey() bool { return false }

// devLocalHubs: a production build reaches Pursae Manager only over https on a public address, so
// neither a redirect nor a URL the Manager lists can point its client at localhost, a *.test name or
// a loopback address (localDevHost). localHubsForTests is set by this package's tests alone, which run
// their Managers on 127.0.0.1 (hub_trust_prod_test.go); nothing in a binary sets it.
func devLocalHubs() bool { return localHubsForTests.Load() }

var localHubsForTests atomic.Bool
