// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"log/slog"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// New builds the MCP server: name "folio", the build version, Instructions
// and every tool, resource and prompt.
func New(d *Deps) *mcp.Server {
	if d.Logger == nil || d.Now == nil || d.Config == nil {
		panic("mcpserver: Deps.Logger, Deps.Now and Deps.Config are required")
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "folio", Title: "Folio", Version: d.Version}, &mcp.ServerOptions{
		Instructions: Instructions,
		Logger:       d.Logger,
		Capabilities: &mcp.ServerCapabilities{}, // tools, resources and prompts are inferred; no logging capability
	})
	registerCatalogTools(s, d)
	registerAssetTools(s, d)
	registerDocumentTools(s, d)
	registerResources(s, d)
	registerPrompts(s, d)
	return s
}

// Handler serves s over stateless Streamable HTTP. maxBody is
// FOLIO_MAX_REQUEST_BYTES (the HTTP layer enforces it too).
func Handler(s *mcp.Server, maxBody int64, log *slog.Logger) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{
		Stateless:           true,
		MaxRequestBodyBytes: maxBody,
		Logger:              log,
	})
}
