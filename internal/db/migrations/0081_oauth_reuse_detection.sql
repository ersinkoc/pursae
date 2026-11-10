-- OAuth hardening (oauth.go): the refresh token a rotation replaced, so presenting it again ends the
-- grant (OAuth 2.1 refresh token reuse detection), and the authorization code a grant was issued for,
-- so replaying that code revokes the grant. Both hold SHA-256 hashes, like the other token columns.
ALTER TABLE oauth_tokens ADD COLUMN IF NOT EXISTS prev_refresh_hash text;
ALTER TABLE oauth_tokens ADD COLUMN IF NOT EXISTS code_hash text;
CREATE INDEX IF NOT EXISTS oauth_tokens_prev_refresh_idx ON oauth_tokens (prev_refresh_hash) WHERE prev_refresh_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS oauth_tokens_code_idx ON oauth_tokens (code_hash) WHERE code_hash IS NOT NULL;
