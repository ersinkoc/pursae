//go:build devhub

package server

import "wpgo/internal/config"

// devTrustHubKeys is the development opt-in (PURSAE_TRUST_HUB_KEYS=1): trust every key the Manager
// lists, endorsed or not, so a site can update from a local Pursae Manager.
func devTrustHubKeys() bool { return config.Env("TRUST_HUB_KEYS") == "1" }

// devTrustDevKey is the development opt-in (PURSAE_TRUST_DEV_KEY=1): trust the demo signing key
// (dev-key.pub) of the bundled example plugins.
func devTrustDevKey() bool { return config.Env("TRUST_DEV_KEY") == "1" }
