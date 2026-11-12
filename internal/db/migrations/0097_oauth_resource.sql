-- The MCP resource (RFC 8707) a grant was issued for. Empty is a grant from before this column;
-- oauthBearer allows those. New codes and tokens store the canonical MCP resource URL.
ALTER TABLE oauth_codes ADD COLUMN IF NOT EXISTS resource text NOT NULL DEFAULT '';
ALTER TABLE oauth_tokens ADD COLUMN IF NOT EXISTS resource text NOT NULL DEFAULT '';

-- Application Passwords: an empty scope list used to mean the user's full capabilities. It now
-- means content and media, and full access is the explicit full_access marker, so passwords that
-- already exist keep the access they were created with.
UPDATE app_passwords SET scopes = '{full_access}' WHERE scopes = '{}';
