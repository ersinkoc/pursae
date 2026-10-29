-- Gooike (native cookie consent plugin): the consent log. Every banner decision is one row, the
-- proof of consent asked for by GDPR art. 7(1) / KVKK. No raw IP address is stored: ip_anon is the
-- truncated address (IPv4 /24, IPv6 /48), a keyed hash, or empty, as the plugin is configured, and
-- the user agent is only kept as a keyed hash. The table exists whether or not the plugin is
-- active; it stays empty until it is used and is kept when the plugin is deactivated.
CREATE TABLE IF NOT EXISTS gooike_consent_log (
    id             bigserial PRIMARY KEY,
    consent_id     text NOT NULL,
    action         text NOT NULL DEFAULT 'custom', -- accept_all, reject_all, custom, withdraw, gpc
    categories     text[] NOT NULL DEFAULT '{}',   -- granted categories
    policy_version int NOT NULL DEFAULT 1,
    regulation     text NOT NULL DEFAULT 'gdpr',   -- gdpr, ccpa, kvkk
    ip_anon        text NOT NULL DEFAULT '',
    ua_hash        text NOT NULL DEFAULT '',
    url            text NOT NULL DEFAULT '',
    gpc            boolean NOT NULL DEFAULT false,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS gooike_consent_log_created ON gooike_consent_log (created_at DESC);
CREATE INDEX IF NOT EXISTS gooike_consent_log_consent ON gooike_consent_log (consent_id);
