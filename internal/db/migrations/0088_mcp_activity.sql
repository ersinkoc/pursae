-- Pursae MCP: one row per tool call that writes (or a REST write made through rest_request), so the site
-- owner can see which AI client changed what and when (mcp_audit.go, GET /pursae/v1/mcp/activity).
CREATE TABLE IF NOT EXISTS mcp_activity (
    id          bigserial PRIMARY KEY,
    created_at  timestamptz NOT NULL DEFAULT now(),
    user_id     bigint NOT NULL DEFAULT 0,
    client      text NOT NULL DEFAULT '',
    tool        text NOT NULL,
    method      text NOT NULL DEFAULT '',
    route       text NOT NULL DEFAULT '',
    status      integer NOT NULL DEFAULT 0,
    duration_ms integer NOT NULL DEFAULT 0,
    ip          text NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS mcp_activity_created_idx ON mcp_activity (created_at DESC);
