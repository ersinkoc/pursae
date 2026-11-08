//go:build !devhub

package server

// devTrustHubKeys: a production build never trusts unendorsed Manager keys, whatever the
// environment says (WPGO_TRUST_HUB_KEYS is read only by devhub builds).
func devTrustHubKeys() bool { return false }
