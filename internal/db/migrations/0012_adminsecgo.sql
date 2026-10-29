-- AdminSecGo (native security plugin): second factors, login challenges, remembered devices,
-- lockouts, known login IPs and the activity log. The tables exist whether or not the plugin is
-- active; while it is off nothing reads or writes them, and deactivating keeps users' factors.

CREATE TABLE IF NOT EXISTS adminsec_factors (
    user_id             bigint PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    email_enabled       boolean NOT NULL DEFAULT false,      -- MFA: e-mailed one-time codes
    email_address       text NOT NULL DEFAULT '',
    email_verified_at   timestamptz,
    totp_enabled        boolean NOT NULL DEFAULT false,      -- 2FA: authenticator app (RFC 6238)
    totp_secret         text NOT NULL DEFAULT '',            -- sealed (AES-GCM with the server secret)
    totp_pending        text NOT NULL DEFAULT '',            -- sealed secret of an unfinished setup
    totp_last_step      bigint NOT NULL DEFAULT 0,           -- replay protection: last accepted time step
    totp_enabled_at     timestamptz,
    backup_codes        jsonb NOT NULL DEFAULT '[]',         -- [{"h": hash, "u": used unix time or 0}]
    backup_generated_at timestamptz,
    updated_at          timestamptz NOT NULL DEFAULT now()
);

-- A login (or factor setup) waiting for its second step. The token travels in the challenge form.
CREATE TABLE IF NOT EXISTS adminsec_challenges (
    id           bigserial PRIMARY KEY,
    token_hash   bytea NOT NULL UNIQUE,
    user_id      bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose      text NOT NULL DEFAULT 'login',              -- login, setup_email
    remember     boolean NOT NULL DEFAULT false,
    redirect_to  text NOT NULL DEFAULT '',
    ip           text NOT NULL DEFAULT '',
    email        text NOT NULL DEFAULT '',                   -- setup_email: the address being verified
    code_hash    text NOT NULL DEFAULT '',
    code_expires timestamptz,
    code_sent_at timestamptz,
    sends        int NOT NULL DEFAULT 0,
    attempts     int NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS adminsec_challenges_user ON adminsec_challenges (user_id);

-- "Remember this device": the cookie names the row and carries a token hashed here.
CREATE TABLE IF NOT EXISTS adminsec_devices (
    id           bigserial PRIMARY KEY,
    user_id      bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   bytea NOT NULL UNIQUE,
    user_agent   text NOT NULL DEFAULT '',
    ip           text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS adminsec_devices_user ON adminsec_devices (user_id);

CREATE TABLE IF NOT EXISTS adminsec_lockouts (
    id         bigserial PRIMARY KEY,
    kind       text NOT NULL,                                -- ip, user
    value      text NOT NULL,
    reason     text NOT NULL DEFAULT '',
    failures   int NOT NULL DEFAULT 0,
    long_lock  boolean NOT NULL DEFAULT false,
    until      timestamptz NOT NULL,
    released   boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS adminsec_lockouts_value ON adminsec_lockouts (kind, value, until);

CREATE TABLE IF NOT EXISTS adminsec_known_ips (
    user_id    bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ip         text NOT NULL,
    first_seen timestamptz NOT NULL DEFAULT now(),
    last_seen  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, ip)
);

CREATE TABLE IF NOT EXISTS adminsec_log (
    id         bigserial PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    event      text NOT NULL,
    severity   text NOT NULL DEFAULT 'info',                 -- info, notice, warning, critical
    user_id    bigint,
    username   text NOT NULL DEFAULT '',
    ip         text NOT NULL DEFAULT '',
    user_agent text NOT NULL DEFAULT '',
    object     text NOT NULL DEFAULT '',
    message    text NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS adminsec_log_created ON adminsec_log (created_at DESC);
CREATE INDEX IF NOT EXISTS adminsec_log_event ON adminsec_log (event, created_at DESC);

CREATE INDEX IF NOT EXISTS login_attempts_login_time ON login_attempts (lower(login), attempted_at);
