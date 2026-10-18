// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"net/http"
	"strings"

	"github.com/vocdoni/folio/internal/activity"
)

func (s *Server) connectionRoutes(mux *http.ServeMux) {
	s.api(mux, "GET /ui/api/connection", route{}, s.handleConnection)
}

// ConnectionToken is one MCP bearer token shown to a signed-in UI user.
type ConnectionToken struct {
	Name  string `json:"name"`
	Token string `json:"token"`
}

// ConnectionOut is GET /ui/api/connection: what an agent needs to connect.
// Tokens is empty when the instance runs without authentication.
type ConnectionOut struct {
	MCPURL   string            `json:"mcp_url"`
	Tokens   []ConnectionToken `json:"tokens"`
	SkillURL string            `json:"skill_url"`
}

// handleConnection reveals the MCP tokens to a signed-in UI user (the UI
// password holder is trusted with them). Every call leaves an activity row
// without the token.
func (s *Server) handleConnection(w http.ResponseWriter, _ *http.Request) {
	base := strings.TrimRight(s.o.Config.PublicURL, "/")
	out := ConnectionOut{MCPURL: base + "/mcp", SkillURL: base + "/skills/folio/SKILL.md", Tokens: []ConnectionToken{}}
	if !s.o.Config.InsecureNoAuth {
		for _, t := range s.o.Config.Tokens {
			out.Tokens = append(out.Tokens, ConnectionToken{Name: t.Name, Token: t.Value})
		}
	}
	if s.o.Activity != nil {
		s.o.Activity.Record(activity.Row{TS: s.o.Now(), Source: activity.SourceUI, Actor: Actor, Action: "ui.connection.reveal",
			Outcome: activity.OutcomeOK})
	}
	writeJSON(w, http.StatusOK, out)
}
