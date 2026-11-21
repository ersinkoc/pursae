package config

import (
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
)

// EnvPrefix is the prefix of every Pursae environment variable (PURSAE_SITE_URL, PURSAE_SECRET …).
const EnvPrefix = "PURSAE_"

// LegacyEnvPrefix is the prefix Pursae used before 0.5.0 (WPGO_SITE_URL …). The old names are still
// read, with a deprecation warning, and will be removed no earlier than 1.0
// (docs/developer/getting-started/upgrading.md).
const LegacyEnvPrefix = "WPGO_"

// envWarned holds the names already warned about, so each variable is reported once per process.
var envWarned sync.Map

// Env returns the environment variable PURSAE_<name>, falling back to the deprecated WPGO_<name>.
// name may be given with or without the PURSAE_ prefix ("SITE_URL" or "PURSAE_SITE_URL"). An unset
// variable is "".
func Env(name string) string {
	v, _ := LookupEnv(name)
	return v
}

// EnvName is the full name of a Pursae environment variable: EnvName("SITE_URL") is "PURSAE_SITE_URL".
func EnvName(name string) string { return EnvPrefix + strings.TrimPrefix(name, EnvPrefix) }

// LookupEnv is Env that also reports whether the variable is set (to any value, even an empty one).
// PURSAE_<name> wins when it is set; WPGO_<name> is used only when PURSAE_<name> is not set, and then
// a deprecation warning naming the new variable is logged once. When both are set to different values
// PURSAE_<name> is used and a warning says the old one is ignored. Values are never logged.
func LookupEnv(name string) (string, bool) {
	name = strings.TrimPrefix(name, EnvPrefix)
	v, ok := os.LookupEnv(EnvPrefix + name)
	old, oldOK := os.LookupEnv(LegacyEnvPrefix + name)
	switch {
	case ok:
		if oldOK && old != v {
			warnEnvOnce(name, true)
		}
		return v, true
	case oldOK:
		warnEnvOnce(name, false)
		return old, true
	}
	return "", false
}

// WarnDeprecatedEnv logs the deprecation warnings for every WPGO_* variable in the environment, so they
// appear at start-up even for settings that are read later (or only by some code paths). Each variable
// is still reported only once.
func WarnDeprecatedEnv() {
	var names []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if name, ok := strings.CutPrefix(k, LegacyEnvPrefix); ok && name != "" && !internalEnv[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		LookupEnv(name)
	}
}

// internalEnv are the variables one Pursae process passes to the next (a core update hands its
// listener to the new binary, the CLI re-executes the installed binary). Older releases only know the
// WPGO_ names, so both names are set and read and neither is a setting to warn about.
var internalEnv = map[string]bool{"LISTEN_FD": true, "CLI_REEXEC": true}

func warnEnvOnce(name string, conflict bool) {
	if _, done := envWarned.LoadOrStore(name, true); done {
		return
	}
	if conflict {
		slog.Warn("deprecated environment variable ignored: both names are set with different values; remove the old one",
			"old", LegacyEnvPrefix+name, "new", EnvPrefix+name, "using", EnvPrefix+name)
		return
	}
	slog.Warn("deprecated environment variable: rename it (the old name stops working no earlier than Pursae 1.0)",
		"old", LegacyEnvPrefix+name, "new", EnvPrefix+name)
}
