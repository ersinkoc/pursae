// Package docs embeds the developer documentation so the MCP server (internal/server/mcp.go) can
// offer it to AI agents as resources.
package docs

import "embed"

// Developer is docs/developer: guides for themes, plugins, the REST API and operations.
//
//go:embed developer
var Developer embed.FS
