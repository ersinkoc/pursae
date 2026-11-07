---
title: AI agents (MCP)
description: Let Claude, Cursor and other MCP clients manage a Pursae site with an Application Password.
weight: 85
section: operations
---

Pursae has a built-in [Model Context Protocol](https://modelcontextprotocol.io) server. An AI
client connected to it can write and publish content, upload media, build menus, edit theme files,
switch themes, turn plugins on and off, purge the cache and change Pursae SEO and the other
built-in plugins' settings — everything through the REST API, with the permissions of one
Application Password.

## How it works

- **Endpoint:** `https://<site>/wp-json/pursae/v1/mcp`, MCP's *streamable HTTP* transport (see
  [Sessions and streaming](#sessions-and-streaming)).
- **Sign-in:** [OAuth 2.1](../rest-api/authentication.md#oauth-21-mcp-clients) (clients that support it
  open a consent screen in your browser), or HTTP Basic with a user name and an
  [Application Password](../rest-api/authentication.md#application-passwords-scripts-apps-ci).
  Every tool runs as a REST request with the same credentials, so capabilities and
  [scopes](../rest-api/authentication.md#application-password-scopes) apply exactly as they do
  for direct REST calls.
- **stdio:** `wpgo mcp` is a small bridge for clients that start a local command. It forwards
  stdin/stdout to the endpoint above; the site can be local or remote.
- **Resources:** the developer docs (`pursae-docs://developer/…`), so the agent can read how themes,
  templates, `theme.json` and plugins work before it changes them.

## 1. Sign in

**OAuth (no password to copy).** Point the client at the endpoint. It finds the authorization server
from the `401` answer, registers itself and opens the Authorize Application screen of your site in your browser:
check the application's name, tick the scopes (none = all your permissions) and approve. Under Users ›
Profile › Connected applications you can revoke it at any time. In Claude Code:

```bash
claude mcp add --transport http pursae https://example.com/wp-json/pursae/v1/mcp
# then run /mcp inside Claude Code and choose "Authenticate"
```

Cursor and other clients that support remote MCP with OAuth need only the URL.

**Application Password.** Use it for clients without OAuth, scripts and `wpgo mcp`.

### Create an Application Password

Users › Profile › Application Passwords: give it a name (e.g. "Claude"), tick the scopes it needs —
or none for all of your permissions — and copy the password (it is shown once). Use a user whose
role fits the job: an Editor for content work, an Administrator for themes, plugins and settings.

Recommended scopes:

| Job | Scopes |
|-----|--------|
| Writing and publishing | `content`, `media`, `seo` |
| Design work | `themes`, `media`, `cache` |
| Running the whole site | none (full access) |

Revoke the password on the same screen to cut the agent off at once.

## 2. Connect a client

The examples use `https://example.com`, the user `admin` and the password
`abcd efgh ijkl mnop qrst uvwx`. Use HTTPS for remote sites: Basic auth sends the password with
every request.

### Claude Code

```bash
claude mcp add --transport http pursae https://example.com/wp-json/pursae/v1/mcp \
  --header "Authorization: Basic $(printf 'admin:abcd efgh ijkl mnop qrst uvwx' | base64)"
```

### Claude Desktop

Claude Desktop starts local commands, so use the bridge. In `claude_desktop_config.json`
(Settings › Developer › Edit Config):

```json
{
  "mcpServers": {
    "pursae": {
      "command": "/usr/local/bin/wpgo",
      "args": ["mcp"],
      "env": {
        "PURSAE_URL": "https://example.com",
        "PURSAE_USER": "admin",
        "PURSAE_APP_PASSWORD": "abcd efgh ijkl mnop qrst uvwx"
      }
    }
  }
}
```

With the Docker image, run the bridge from the container instead:
`"command": "docker", "args": ["run", "--rm", "-i", "-e", "PURSAE_URL", "-e", "PURSAE_USER", "-e", "PURSAE_APP_PASSWORD", "<image>", "mcp"]`.

### Cursor

In `~/.cursor/mcp.json` (or `.cursor/mcp.json` in a project):

```json
{
  "mcpServers": {
    "pursae": {
      "url": "https://example.com/wp-json/pursae/v1/mcp",
      "headers": { "Authorization": "Basic YWRtaW46YWJjZCBlZmdoIGlqa2wgbW5vcCBxcnN0IHV2d3g=" }
    }
  }
}
```

The header value is `Basic ` followed by base64 of `user:password`
(`printf 'admin:abcd efgh ijkl mnop qrst uvwx' | base64`). Any other MCP client works the same way:
streamable HTTP with that header, or `wpgo mcp` over stdio.

### `wpgo mcp` flags

| Flag | Environment | |
|------|-------------|---|
| `--url` | `PURSAE_URL` | site address |
| `--user` | `PURSAE_USER` | login or e-mail |
| `--password` | `PURSAE_APP_PASSWORD` | the Application Password |

The bridge prints nothing but protocol messages on stdout; errors go to stderr.

## Sessions and streaming

- `initialize` answers with an `Mcp-Session-Id` header. A client that sends it back on later requests
  keeps one session; a request with an unknown or ended id gets `404`, and the client should
  initialize again. Without the header the server answers statelessly, so simple clients keep working.
  Sessions belong to the user who created them, are capped at 10 per user and expire after an hour
  without use.
- `POST` answers with `application/json`, or as a one-event `text/event-stream` when the request's
  `Accept` header lists `text/event-stream` before `application/json` (or without it).
- `GET` with `Accept: text/event-stream` and a session opens the server→client stream: JSON-RPC
  notifications and a `: keep-alive` comment every 25 seconds. Every event has an `id`; the last 64 are
  kept, so a client that reconnects with `Last-Event-ID` gets what it missed.
- `DELETE` with the session id ends the session and closes its stream (`204`).

## Tools

| Area | Tools |
|------|-------|
| Posts and pages | `list_posts`, `get_post`, `create_post`, `update_post`, `delete_post` and the same for pages |
| Media | `list_media`, `upload_media` (base64 or a public URL), `update_media`, `delete_media` |
| Terms | `list_terms`, `create_term`, `update_term`, `delete_term` (categories, tags, …) |
| Menus | `list_menus`, `create_menu`, `list_menu_locations`, `list_menu_items`, `create_menu_item`, `update_menu_item`, `delete_menu_item` |
| Styles and settings | `get_global_styles`, `update_global_styles`, `get_settings`, `update_settings` |
| Users | `list_users`, `get_me` |
| Pursae SEO | `seo_get_meta`, `seo_update_meta`, `seo_analyze`, `seo_get_settings`, `seo_update_settings`, `seo_list_redirects`, `seo_create_redirect`, `seo_delete_redirect` |
| Plugins | `list_plugins`, `activate_plugin`, `deactivate_plugin`, `install_plugin`, `delete_plugin` |
| Themes | `list_themes`, `activate_theme`, `install_theme`, `list_theme_files`, `read_theme_file`, `write_theme_file`, `restore_theme_file` |
| Pursae Cache | `cache_status`, `cache_purge`, `cache_get_settings`, `cache_update_settings` |
| Built-in plugin settings | `list_option_groups`, `get_options`, `update_options` (`gocdn`, `headless`, `maintenance`, `security`, `seo`, `smtp`; secrets masked) |
| Anything else | `site_info`, `rest_request` (any REST route, same permissions) |

Each tool maps to a REST route listed in [Endpoints](../rest-api/endpoints.md); a refused call comes
back as a tool error with the REST error code (`rest_forbidden`, `rest_scope_forbidden`, …).

## Safety

- Give the agent the narrowest scopes and the least powerful user that does the job.
- `write_theme_file` checks templates before keeping them (the change is rolled back if the site
  stops rendering), and `restore_theme_file` undoes edits. Set `WPGO_DISALLOW_FILE_EDIT=1` or turn
  on "Disable file editing" in Pursae Security to forbid theme file changes entirely.
- `upload_media` fetches URLs only from public addresses.
- Pursae Security's lockouts and IP block list apply to the endpoint like to any REST request.
