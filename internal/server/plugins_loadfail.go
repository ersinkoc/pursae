package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"time"

	"wpgo/internal/mail"
)

// An active plugin can fail to load when the process starts or the sync loop picks it up: its package
// is missing or unreadable, its signer is no longer trusted, or a core update left it on a plugin ABI
// or a "requires" version this core does not run. Such a failure is made visible without changing the
// plugin's status:
//
//   - the row stays active. Several instances share one database; during a rolling update a node on an
//     older (or newer) binary, or one whose disk lost the package for a moment, must not switch the
//     plugin off for every node. A transient error heals by itself on the next retry.
//   - the reason goes into disabled_reason (the Plugins screen, the REST API and the CLI show it while
//     the plugin is not loaded here) and the administrator is emailed when it first appears.
//   - this process does not retry on every sync tick with the same package and trusted keys, only every
//     pluginLoadRetry; a new version of the package or a change of the trusted keys retries at once.
//   - a successful load anywhere clears the reason, as does activating the plugin again.

// pluginLoadRetry is how long a process waits before loading again an active plugin that failed with
// the same package version and trusted keys.
var pluginLoadRetry = 10 * time.Minute

type pluginLoadFail struct {
	version, trust, err string
	at                  time.Time
}

// pluginTrustTag identifies the trusted keys and the unsigned-plugin setting: a change can make a
// package that failed its signature check load.
func (s *Server) pluginTrustTag() string {
	h := sha256.New()
	for _, k := range s.plugins.trusted() {
		h.Write(k)
	}
	if s.allowUnsignedPlugins() {
		h.Write([]byte("unsigned"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (ps *pluginSystem) loadFailedRecently(slug, version, trust string) bool {
	ps.failMu.Lock()
	defer ps.failMu.Unlock()
	f, ok := ps.fails[slug]
	return ok && f.version == version && f.trust == trust && time.Since(f.at) < pluginLoadRetry
}

func (ps *pluginSystem) forgetLoadFail(slug string) {
	ps.failMu.Lock()
	delete(ps.fails, slug)
	ps.failMu.Unlock()
}

// loadActivePlugin loads a plugin whose row is active and records the outcome (see above).
func (s *Server) loadActivePlugin(ctx context.Context, slug, version string, perms []byte) error {
	trust := s.pluginTrustTag()
	err := s.loadPlugin(ctx, slug, perms)
	ps := s.plugins
	if err == nil {
		ps.forgetLoadFail(slug)
		// Clear only a load failure: a row that is active has no other reason to carry one.
		_, _ = s.store.Pool.Exec(ctx, `UPDATE plugins SET disabled_reason = '', disabled_at = NULL WHERE slug = $1 AND status = 'active' AND disabled_reason <> ''`, slug)
		return nil
	}
	if ps.rt == nil {
		return err // the runtime itself did not start: not this plugin's fault
	}
	ps.failMu.Lock()
	prev, seen := ps.fails[slug]
	if ps.fails == nil {
		ps.fails = map[string]pluginLoadFail{}
	}
	ps.fails[slug] = pluginLoadFail{version: version, trust: trust, err: err.Error(), at: time.Now()}
	ps.failMu.Unlock()
	if !seen || prev.err != err.Error() {
		slog.Error("plugin load failed; it stays active but does not run until the problem is fixed", "plugin", slug, "version", version, "err", err)
	}
	s.recordPluginLoadFailed(ctx, slug, pluginLoadFailedReason(err.Error()))
	return err
}

func pluginLoadFailedReason(err string) string {
	return "Could not be loaded: " + err
}

// recordPluginLoadFailed keeps why an active plugin could not be loaded and emails the administrator
// when the plugin had no such reason yet (once per failure, not once per retry or per instance).
func (s *Server) recordPluginLoadFailed(ctx context.Context, slug, reason string) {
	var old string
	err := s.store.Pool.QueryRow(ctx, `WITH old AS (SELECT slug, disabled_reason FROM plugins WHERE slug = $1 AND status = 'active' FOR UPDATE)
		UPDATE plugins p SET disabled_reason = $2, disabled_at = now() FROM old WHERE p.slug = old.slug AND p.disabled_reason <> $2
		RETURNING old.disabled_reason`, slug, reason).Scan(&old)
	if err != nil || old != "" {
		return // unchanged, no longer active, or already reported
	}
	to := strings.TrimSpace(s.settings.String("admin_email"))
	if to == "" {
		return
	}
	site := s.settings.String("blogname")
	s.sendMail(ctx, mail.Message{To: []string{to}, Subject: "[" + site + "] The plugin " + slug + " could not be loaded",
		Body: "Howdy,\n\nThe plugin \"" + slug + "\" is active but could not be loaded, so it is not running:\n\n" + reason +
			"\n\nThis can happen after a Pursae update when the plugin needs a newer release, or when the key that signed it is no longer trusted. " +
			"The rest of the site keeps working. Install a compatible release of the plugin (or trust its signing key again under Plugins › Trusted Keys); " +
			"it is loaded again automatically once the problem is fixed.\n\nRegards,\nAll at " + site + "\n"})
}
