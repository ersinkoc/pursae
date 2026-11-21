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
//
//   - PURSAE_<name> non-empty: it wins. When WPGO_<name> is also set to a different value, a warning
//     says the old one is ignored.
//   - PURSAE_<name> empty or unset and WPGO_<name> non-empty: the old value is used, with a
//     deprecation warning naming the new variable. An empty new name does not hide a set old one:
//     the compose files pass every PURSAE_* key, empty when .env has neither name, while an override
//     file, env_file or `docker run -e` may still set the WPGO_* name.
//   - otherwise PURSAE_<name> (empty) when it is set, else WPGO_<name> (empty) when that is set.
//
// Each variable is warned about once per process. Values are never logged.
func LookupEnv(name string) (string, bool) {
	name = strings.TrimPrefix(name, EnvPrefix)
	v, ok := os.LookupEnv(EnvPrefix + name)
	old, oldOK := os.LookupEnv(LegacyEnvPrefix + name)
	switch {
	case v != "":
		if oldOK && old != v {
			warnEnvOnce(name, envConflict)
		}
		return v, true
	case old != "":
		if ok {
			warnEnvOnce(name, envEmptyNew)
		} else {
			warnEnvOnce(name, envLegacy)
		}
		return old, true
	case ok:
		return "", true
	case oldOK:
		warnEnvOnce(name, envLegacy)
		return "", true
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

type envWarning int

const (
	envLegacy   envWarning = iota // only WPGO_<name> is set
	envConflict                   // both are set, to different non-empty values: PURSAE_ wins
	envEmptyNew                   // PURSAE_<name> is empty, WPGO_<name> is not: the old value is used
)

// inContainer reports whether this process runs in the Docker image (which sets PURSAE_BIN_DIR and
// WPGO_BIN_DIR). There the compose files decide which names reach the process: the ones from before
// 0.5.0 read only WPGO_* from .env, so renaming .env first would drop the settings.
func inContainer() bool {
	return os.Getenv(EnvPrefix+"BIN_DIR") != "" || os.Getenv(LegacyEnvPrefix+"BIN_DIR") != ""
}

const containerRenameHint = "update the compose files (git pull) before renaming the variables in .env: compose files from before 0.5.0 read only the WPGO_* names"

func warnEnvOnce(name string, kind envWarning) {
	if _, done := envWarned.LoadOrStore(name, true); done {
		return
	}
	attrs := []any{"old", LegacyEnvPrefix + name, "new", EnvPrefix + name}
	switch kind {
	case envConflict:
		slog.Warn("deprecated environment variable ignored: both names are set with different values; remove the old one",
			append(attrs, "using", EnvPrefix+name)...)
		return
	case envEmptyNew:
		attrs = append(attrs, "using", LegacyEnvPrefix+name)
		if inContainer() {
			attrs = append(attrs, "docker", containerRenameHint)
		}
		slog.Warn("deprecated environment variable used: the new name is set but empty; set the value under the new name (the old name stops working no earlier than Pursae 1.0)",
			attrs...)
		return
	}
	if inContainer() {
		attrs = append(attrs, "docker", containerRenameHint)
	}
	slog.Warn("deprecated environment variable: rename it (the old name stops working no earlier than Pursae 1.0)", attrs...)
}

// WithLegacyNames returns env (KEY=value entries, as from os.Environ) with WPGO_<name>=value added
// for every PURSAE_<name> that has no WPGO_<name> entry. A core update rollback (or a Docker
// launcher running an older installed binary) execs a release before 0.5.0, which reads only the
// WPGO_* names; with the same value under both names a 0.5+ binary does not warn either. Existing
// WPGO_* entries are kept as they are.
func WithLegacyNames(env []string) []string {
	have := make(map[string]bool, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		have[k] = true
	}
	out := env[:len(env):len(env)]
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if name, ok := strings.CutPrefix(k, EnvPrefix); ok && name != "" && !have[LegacyEnvPrefix+name] {
			out = append(out, LegacyEnvPrefix+name+"="+v)
			have[LegacyEnvPrefix+name] = true
		}
	}
	return out
}
