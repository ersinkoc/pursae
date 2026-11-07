-- Wasm plugin options move into a namespace of their own (internal/server/plugins.go,
-- pluginOptionKey): a plugin's "<slug>_*" and "plugin_<slug>_*" options are stored as
-- "wasm:<slug>:<name>", so a plugin can never read or overwrite a core or built-in plugin option
-- that happens to start with its slug. Existing values are COPIED, not moved: a built-in option
-- matching an installed plugin's prefix (a plugin slugged "wp" and wp_mail_smtp) must stay where
-- the built-in reads it; the old plugin rows are left behind, harmless and unread.
INSERT INTO settings (key, value, grp, autoload)
SELECT 'wasm:' || p.slug || ':' || s.key, s.value, s.grp, s.autoload
FROM settings s
JOIN plugins p ON s.key LIKE replace(replace(p.slug, '-', '_'), '_', '\_') || '\_%'
	OR s.key LIKE 'plugin\_' || replace(replace(p.slug, '-', '_'), '_', '\_') || '\_%'
WHERE s.key NOT LIKE 'wasm:%'
	AND s.key NOT IN ('plugin_trusted_keys', 'plugin_allow_unsigned')
ON CONFLICT (key) DO NOTHING;

-- The key that signed each installed plugin (hex Ed25519 public key; empty for an unsigned
-- package or one installed before this column existed). An update signed by another key, or an
-- older version, is refused from the Manager and needs a confirmation on upload.
ALTER TABLE plugins ADD COLUMN IF NOT EXISTS signer text NOT NULL DEFAULT '';
