-- OAuth 2.1 for the MCP endpoint (oauth.go): dynamically registered public clients, one-time
-- authorization codes (PKCE) and rotating access/refresh token pairs. Only SHA-256 hashes of codes
-- and tokens are stored; a row of oauth_tokens is one grant and can be revoked from the profile.
CREATE TABLE IF NOT EXISTS oauth_clients (
    client_id     text PRIMARY KEY,
    name          text NOT NULL,
    redirect_uris text[] NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS oauth_codes (
    code_hash    text PRIMARY KEY,
    client_id    text NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    user_id      bigint NOT NULL,
    redirect_uri text NOT NULL,
    scopes       text[] NOT NULL DEFAULT '{}',
    challenge    text NOT NULL,
    expires_at   timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS oauth_tokens (
    id                 bigserial PRIMARY KEY,
    client_id          text NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    user_id            bigint NOT NULL,
    access_hash        text NOT NULL UNIQUE,
    refresh_hash       text NOT NULL UNIQUE,
    scopes             text[] NOT NULL DEFAULT '{}',
    created_at         timestamptz NOT NULL DEFAULT now(),
    access_expires_at  timestamptz NOT NULL,
    refresh_expires_at timestamptz NOT NULL,
    last_used_at       timestamptz,
    last_ip            text NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS oauth_tokens_user_idx ON oauth_tokens (user_id);
