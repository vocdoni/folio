// SPDX-License-Identifier: AGPL-3.0-only

// Package httpserver serves Folio's HTTP surface: /mcp (bearer auth), public
// downloads at /f/{file_id}/{name}, /healthz and /robots.txt, plus request
// logging, panic recovery, the request body limit and graceful shutdown.
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/vocdoni/folio/internal/config"
)

// Health is the /healthz body.
type Health struct {
	Status        string `json:"status"` // ok | degraded
	Version       string `json:"version"`
	Sandbox       string `json:"sandbox"` // landlock+rlimits | rlimits-only
	SchemaVersion int    `json:"schema_version"`
	Reviewer      bool   `json:"reviewer"`
}

// Options configures the handler.
type Options struct {
	MCP             http.Handler // the MCP Streamable HTTP handler (unauthenticated; wrapped here)
	Tokens          []config.Token
	InsecureNoAuth  bool // serve /mcp without auth (only valid with no tokens)
	Files           FileSource
	Health          Health
	Ping            func(context.Context) error // database liveness for /healthz; nil = always ok
	MaxRequestBytes int64                       // FOLIO_MAX_REQUEST_BYTES for /mcp
	Logger          *slog.Logger
}

const robots = "User-agent: *\nDisallow: /\n"

// NewHandler builds the mux. It refuses to build an unauthenticated /mcp
// unless InsecureNoAuth is set and no token is configured.
func NewHandler(o Options) (http.Handler, error) {
	if o.MCP == nil || o.Files == nil || o.Logger == nil {
		return nil, errors.New("httpserver: MCP, Files and Logger are required")
	}
	if len(o.Tokens) == 0 && !o.InsecureNoAuth {
		return nil, errors.New("httpserver: no token configured and FOLIO_INSECURE_NO_AUTH is not set")
	}
	mcpH := limitBody(o.MaxRequestBytes, o.MCP)
	if len(o.Tokens) > 0 {
		mcpH = RequireToken(o.Tokens, mcpH)
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpH)
	mux.Handle("GET /f/{file_id}/{name}", downloads(o.Files, o.Logger))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		h, code := o.Health, http.StatusOK
		h.Status = "ok"
		if o.Ping != nil {
			if err := o.Ping(r.Context()); err != nil {
				o.Logger.ErrorContext(r.Context(), "health: database ping failed", "err", err)
				h.Status, code = "degraded", http.StatusServiceUnavailable
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(h)
	})
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(robots))
	})
	return recoverer(o.Logger, logRequests(o.Logger, mux)), nil
}

// limitBody rejects bodies larger than max with 413 (declared length up
// front, actual length while reading). max ≤ 0 disables the limit.
func limitBody(maxBytes int64, h http.Handler) http.Handler {
	if maxBytes <= 0 {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxBytes {
			http.Error(w, fmt.Sprintf("request body exceeds %d bytes", maxBytes), http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		h.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// Flush keeps streaming (SSE) responses working through the wrapper.
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// logRequests logs method, route pattern (never the raw path: download
// paths carry capabilities), status and duration.
func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		log.InfoContext(r.Context(), "http request", "method", r.Method, "route", route, "status", sw.status,
			"duration_ms", time.Since(start).Milliseconds())
	})
}

func recoverer(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler { //nolint:errorlint // sentinel compared as documented by net/http
					panic(v)
				}
				log.ErrorContext(r.Context(), "panic in handler", "panic", fmt.Sprint(v))
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// Serve serves h on ln until ctx is cancelled, then runs beforeShutdown (to
// end long-lived streams so they do not hold up the drain), stops accepting
// connections and waits up to drain for in-flight requests (renders) to
// finish. It returns nil after a clean shutdown.
func Serve(ctx context.Context, ln net.Listener, h http.Handler, drain time.Duration, log *slog.Logger, beforeShutdown ...func()) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		BaseContext:       func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return fmt.Errorf("httpserver: %w", err)
	case <-ctx.Done():
	}
	for _, f := range beforeShutdown {
		f()
	}
	log.Info("shutting down: draining in-flight requests", "drain", drain.String())
	sctx, cancel := context.WithTimeout(context.Background(), drain)
	defer cancel()
	err := srv.Shutdown(sctx)
	if serr := <-errc; serr != nil && !errors.Is(serr, http.ErrServerClosed) {
		return fmt.Errorf("httpserver: %w", serr)
	}
	if err != nil {
		_ = srv.Close()
		return fmt.Errorf("httpserver: drain incomplete: %w", err)
	}
	return nil
}
