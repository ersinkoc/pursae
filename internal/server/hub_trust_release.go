//go:build !devhub

package server

// devTrustHubKeys: a production build never trusts unendorsed Manager keys, whatever the
// environment says (PURSAE_TRUST_HUB_KEYS is read only by devhub builds).
func devTrustHubKeys() bool { return false }

// devTrustDevKey: a production build never trusts the public demo key of the example plugins
// (PURSAE_TRUST_DEV_KEY is read only by devhub builds).
func devTrustDevKey() bool { return false }
