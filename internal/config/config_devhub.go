//go:build devhub

package config

import (
	"strings"
)

// DevHub reports whether this is a development build (-tags devhub) whose Manager URL and key
// trust can be changed from the environment.
const DevHub = true

// hubURL in a development build: PURSAE_DEV_HUB_URL (e.g. a local Pursae Manager on
// http://localhost:8090, or "off"), else DefaultHubURL.
func hubURL() string {
	if v := strings.TrimSpace(Env("DEV_HUB_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return DefaultHubURL
}
