-- The setup token that guards /wp-admin/install until the site is installed (internal/server/setup_token.go).
-- Only its SHA-256 is kept: the token itself is shown once, in the log of the process that generated it,
-- or by `wpgo setup-token`, which issues a new one. One row at most; the installer deletes it.
CREATE TABLE IF NOT EXISTS setup_token (
  id         SMALLINT    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  token_hash BYTEA       NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
