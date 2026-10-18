// SPDX-License-Identifier: AGPL-3.0-only

package httpserver

import (
	"fmt"
	"io/fs"
	"net/http"

	"github.com/vocdoni/folio/skills"
)

// skillRoutes registers the public skill download: one fixed path serving the
// embedded SKILL.md. Nothing is derived from the request path.
func skillRoutes(mux *http.ServeMux) error {
	md, err := fs.ReadFile(skills.FS, "folio/SKILL.md")
	if err != nil {
		return fmt.Errorf("httpserver: skill: %w", err)
	}
	mux.HandleFunc("GET /skills/folio/SKILL.md", func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "text/markdown; charset=utf-8")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(md)
	})
	return nil
}
