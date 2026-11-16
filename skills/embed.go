// Package skills embeds the agent skills shipped with Pursae, so the admin (Tools › AI Access) can
// hand them out as a zip and the MCP server can offer them as resources.
package skills

import "embed"

// SiteBuilder is skills/pursae-site-builder: building whole sites through the Pursae MCP server.
//
//go:embed pursae-site-builder
var SiteBuilder embed.FS

// SiteBuilderName is the skill's folder name.
const SiteBuilderName = "pursae-site-builder"
