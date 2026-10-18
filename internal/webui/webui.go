// SPDX-License-Identifier: AGPL-3.0-only

// Package webui serves Folio's password-protected web UI at /ui: the
// embedded single-page app, its JSON API under /ui/api (session cookie,
// CSRF header and Origin checks, login rate limits) and the live activity
// stream. It uses the same services as the MCP tools. Without
// FOLIO_UI_PASSWORD the package is not mounted at all.
package webui

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/vocdoni/folio/internal/activity"
	"github.com/vocdoni/folio/internal/assets"
	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/config"
	"github.com/vocdoni/folio/internal/documents"
	"github.com/vocdoni/folio/internal/render"
	"github.com/vocdoni/folio/internal/store"
)

// Actor is the actor recorded for every UI action (activity and audit).
const Actor = "ui"

// MaxStreamsPerSession bounds live activity streams per session.
const MaxStreamsPerSession = 3

// Rasterizer renders one page of a stored PDF (render.Engine implements it).
type Rasterizer interface {
	RasterizePDFPage(ctx context.Context, pdf []byte, page, ppi int) ([]byte, error)
}

// Options wires the UI to Folio's services. Config, DB, Logger and Now are
// required; the rest is required by the routes that use it.
type Options struct {
	Config      *config.Config
	DB          *store.DB
	Blobs       *store.Blobs
	Catalog     *catalog.Service
	Documents   *documents.Service
	Assets      *assets.Service
	Validator   catalog.Renderer // render.Pipeline (validate-only runs)
	Raster      Rasterizer       // on-demand document pages
	Queue       *render.Queue    // the MCP render queue (on-demand pages)
	Activity    *activity.Recorder
	Broadcaster *activity.Broadcaster
	Reviewer    bool // the layout reviewer is configured
	Version     string
	Sandbox     string
	Packages    []string
	Dist        fs.FS // built SPA (the embedded dist/ by default)
	Logger      *slog.Logger
	Now         func() time.Time
}

// Server is the web UI handler.
type Server struct {
	o        Options
	origin   string // scheme://host[:port] of FOLIO_PUBLIC_URL
	secure   bool   // cookies get Secure (FOLIO_PUBLIC_URL is https)
	sessions *sessions
	limiter  *loginLimiter
	static   *static
	pages    *pageCache
	handler  http.Handler

	ipLogOnce sync.Once
	heartbeat time.Duration // SSE keep-alive; 0 = 20 s (tests shorten it)
}

// New builds the UI handler. It fails when the UI is not enabled or a
// required option is missing.
func New(o Options) (*Server, error) {
	if o.Config == nil || o.DB == nil || o.Logger == nil {
		return nil, errors.New("webui: Config, DB and Logger are required")
	}
	if !o.Config.UIEnabled() {
		return nil, errors.New("webui: FOLIO_UI_PASSWORD is not set")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Dist == nil {
		sub, err := fs.Sub(distFS, "dist")
		if err != nil {
			return nil, err
		}
		o.Dist = sub
	}
	u, err := url.Parse(o.Config.PublicURL)
	if err != nil || u.Host == "" {
		return nil, errors.New("webui: FOLIO_PUBLIC_URL must be an absolute URL")
	}
	s := &Server{o: o, origin: strings.ToLower(u.Scheme + "://" + u.Host), secure: u.Scheme == "https"}
	s.sessions = newSessions(o.Config.UISessionTTL, maxSessions, o.Now, s.sessionEnded)
	s.limiter = newLoginLimiter(o.Now)
	s.static = newStatic(o.Dist)
	s.pages = newPageCache(pageCacheBytes)
	s.handler = s.securityHeaders(s.routes())
	return s, nil
}

// ServeHTTP serves /ui and everything below it.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// sessionEnded releases what a session owned (logout, expiry, eviction):
// live streams (later: preview renders and AI jobs).
func (s *Server) sessionEnded(id string) {
	if s.o.Broadcaster != nil {
		s.o.Broadcaster.CloseKey(id)
	}
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	// Public: the SPA shell, hashed assets, fonts and the session probe/login.
	mux.HandleFunc("/ui", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/", http.StatusMovedPermanently)
	})
	mux.Handle("/ui/", s.static)
	mux.Handle("/ui/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeError(w, r, apiErr(http.StatusNotFound, "not_found", "no such API route"))
	}))
	s.api(mux, "GET /ui/api/session", route{public: true}, s.handleSession)
	s.api(mux, "POST /ui/api/login", route{public: true, action: "ui.login"}, s.handleLogin)
	s.api(mux, "POST /ui/api/logout", route{action: "ui.logout"}, s.handleLogout)
	s.overviewRoutes(mux)
	s.documentRoutes(mux)
	s.catalogRoutes(mux)
	s.assetRoutes(mux)
	return mux
}
