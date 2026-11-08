//go:build !devhub

package config

// DevHub reports whether this is a development build (-tags devhub) whose Manager URL and key
// trust can be changed from the environment. Production builds: false.
const DevHub = false

// hubURL is fixed in a production build: no environment variable or option redirects it.
func hubURL() string { return DefaultHubURL }
