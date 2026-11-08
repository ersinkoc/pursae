//go:build devhub

package server

import "os"

// devTrustHubKeys is the development opt-in (WPGO_TRUST_HUB_KEYS=1): trust every key the Manager
// lists, endorsed or not, so a site can update from a local Pursae Manager.
func devTrustHubKeys() bool { return os.Getenv("WPGO_TRUST_HUB_KEYS") == "1" }
