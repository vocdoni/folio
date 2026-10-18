// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"bytes"
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	bundled "github.com/vocdoni/folio/assets"
)

// distFS is the built single-page app (make ui writes it; only dist/.keep is
// committed, so a Go-only build serves the "not built" page).
//
//go:embed all:dist
var distFS embed.FS

const notBuilt = "UI assets not built — run `make ui` (or build the Docker image) to include the web UI.\n"

// static serves the SPA: hashed assets under /ui/assets/ (immutable), the
// bundled document fonts under /ui/fonts/ (brand specimens), other files
// with an extension from the dist root, and index.html for every other GET
// below /ui/ (client-side routes). Non-GET requests get 405.
type static struct {
	dist  fs.FS
	index []byte // nil when the UI was not built
}

func newStatic(dist fs.FS) *static {
	st := &static{dist: dist}
	if b, err := fs.ReadFile(dist, "index.html"); err == nil {
		st.index = b
	}
	return st
}

func (st *static) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel := strings.TrimPrefix(path.Clean(r.URL.Path), "/ui")
	rel = strings.TrimPrefix(rel, "/")
	switch {
	case strings.HasPrefix(rel, "fonts/"):
		st.font(w, r, strings.TrimPrefix(rel, "fonts/"))
	case strings.HasPrefix(rel, "assets/"):
		st.file(w, r, rel, "public, max-age=31536000, immutable")
	case path.Ext(rel) != "":
		st.file(w, r, rel, "public, max-age=3600")
	case st.index == nil:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(notBuilt))
	default:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(st.index))
	}
}

func (st *static) file(w http.ResponseWriter, r *http.Request, rel, cache string) {
	if rel == "index.html" || !fs.ValidPath(rel) {
		http.NotFound(w, r)
		return
	}
	b, err := fs.ReadFile(st.dist, rel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if ct := mime.TypeByExtension(path.Ext(rel)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", cache)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(b))
}

// font serves a bundled document font (OFL) so brand editors can show
// specimens; only the regular and bold faces of bundled families.
func (st *static) font(w http.ResponseWriter, r *http.Request, name string) {
	if !fs.ValidPath(name) || strings.Contains(name, "/") || path.Ext(name) != ".ttf" {
		http.NotFound(w, r)
		return
	}
	b, err := fs.ReadFile(bundled.FS(), "fonts/"+name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "font/ttf")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(b))
}
