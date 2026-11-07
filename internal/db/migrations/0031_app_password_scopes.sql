-- Application Password scopes (app_password_scopes.go): an empty list keeps the user's full
-- capabilities; a non-empty list narrows them to the named scopes.
ALTER TABLE app_passwords ADD COLUMN IF NOT EXISTS scopes text[] NOT NULL DEFAULT '{}';
