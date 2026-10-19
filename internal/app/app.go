// SPDX-License-Identifier: AGPL-3.0-only

// Package app wires a complete Folio service from a validated configuration:
// store, bundled runtime, sandboxed engine, services, startup maintenance,
// seeding, the MCP server and the HTTP handler. `folio serve` and the e2e
// tests both use it.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	bundled "github.com/vocdoni/folio/assets"
	"github.com/vocdoni/folio/internal/activity"
	"github.com/vocdoni/folio/internal/ai"
	"github.com/vocdoni/folio/internal/assets"
	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/checks"
	"github.com/vocdoni/folio/internal/config"
	"github.com/vocdoni/folio/internal/documents"
	"github.com/vocdoni/folio/internal/httpserver"
	"github.com/vocdoni/folio/internal/maintenance"
	"github.com/vocdoni/folio/internal/mcpserver"
	"github.com/vocdoni/folio/internal/pandocast"
	"github.com/vocdoni/folio/internal/render"
	"github.com/vocdoni/folio/internal/review"
	"github.com/vocdoni/folio/internal/sandbox"
	"github.com/vocdoni/folio/internal/seed"
	"github.com/vocdoni/folio/internal/store"
	"github.com/vocdoni/folio/internal/validate"
	"github.com/vocdoni/folio/internal/webui"
)

// Options are the parts of the wiring that are not configuration.
type Options struct {
	Version  string
	Logger   *slog.Logger
	Reviewer review.Reviewer // nil: review_document returns unavailable
	Self     string          // binary providing `sandbox-exec`; "" = os.Executable()
	JobRoot  string          // parent of render job dirs; "" = os.TempDir()
	Now      func() time.Time
	// ProgressInterval overrides the progress heartbeat (tests); 0 = default.
	ProgressInterval time.Duration
	// ReadFile reads cgroup files; nil = os.ReadFile (tests inject).
	ReadFile func(string) ([]byte, error)
}

// App is a wired service. Close releases it.
type App struct {
	Config      *config.Config
	Handler     http.Handler
	DB          *store.DB
	Catalog     *catalog.Service
	Documents   *documents.Service
	Assets      *assets.Service
	Maintenance maintenance.Config
	Activity    *activity.Recorder
	Broadcaster *activity.Broadcaster
	stopRec     context.CancelFunc
	recDone     chan struct{}
	closeUI     func() // cancels web UI jobs; nil without the UI
	Seeded      bool
	log         *slog.Logger
}

// NewLogger builds the slog logger from FOLIO_LOG_LEVEL and FOLIO_LOG_FORMAT.
func NewLogger(cfg *config.Config, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}

// MemoryLimit reads the cgroup memory limit (v2 memory.max, then v1
// memory.limit_in_bytes). ok is false when there is no readable limit.
func MemoryLimit(read func(string) ([]byte, error)) (limit uint64, ok bool) {
	for _, p := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} {
		b, err := read(p)
		if err != nil {
			continue
		}
		s := strings.TrimSpace(string(b))
		if s == "max" {
			return 0, false
		}
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil || n == 0 || n >= 1<<62 { // cgroup v1 reports "unlimited" as a huge number
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// logMemoryBudget logs concurrency × per-process memory against the cgroup
// limit and warns when the renders could exceed it.
func logMemoryBudget(log *slog.Logger, cfg *config.Config, read func(string) ([]byte, error)) {
	slots := cfg.RenderConcurrency
	if cfg.UIEnabled() {
		slots += cfg.UIPreviewConcurrency // the web UI preview queue
	}
	total := uint64(slots) * cfg.RenderMemory
	limit, ok := MemoryLimit(read)
	attrs := []any{"render_concurrency", cfg.RenderConcurrency, "render_memory_bytes", cfg.RenderMemory, "render_total_bytes", total}
	if !ok {
		log.Info("render memory budget (no cgroup memory limit found)", attrs...)
		return
	}
	attrs = append(attrs, "cgroup_memory_max_bytes", limit)
	if total > limit {
		log.Warn("FOLIO_RENDER_CONCURRENCY × FOLIO_RENDER_MEMORY exceeds the container memory limit; lower the concurrency (about (memory − 1 GiB) / FOLIO_RENDER_MEMORY)", attrs...)
		return
	}
	log.Info("render memory budget", attrs...)
}

// logDefaultBrand warns at startup when FOLIO_DEFAULT_BRAND is set but does
// not name a live brand: inline templates that name no brand fail until it
// does (renders report the same problem as invalid_input).
func logDefaultBrand(ctx context.Context, log *slog.Logger, cat *catalog.Service) {
	if cat.DefaultBrandID == "" {
		return
	}
	if _, err := cat.DefaultBrand(ctx); err != nil {
		log.Warn("FOLIO_DEFAULT_BRAND does not name a live brand; inline templates without brand_id fail until it does",
			"default_brand", cat.DefaultBrandID, "err", err)
	}
}

// selfTest proves the tools work under the sandbox before serving: pandoc
// --sandbox must write a DOCX (only the official static pandoc build can) and
// typst must rasterise an SVG.
func selfTest(ctx context.Context, e *render.Engine) error {
	job, err := e.NewJob()
	if err != nil {
		return err
	}
	defer job.Remove() //nolint:errcheck // best effort
	ref, err := bundled.ReferenceDOCX()
	if err != nil {
		return err
	}
	doc := &pandocast.Document{Meta: map[string]json.RawMessage{}, Blocks: []pandocast.Block{&pandocast.Para{Inlines: pandocast.Text("Folio self-test")}}}
	docx, err := e.WriteDOCX(ctx, job, doc, render.DOCXOptions{ReferenceDOCX: ref})
	if err != nil {
		return fmt.Errorf("pandoc --sandbox could not write a DOCX (use the official static pandoc 3.12.1 binary): %w", err)
	}
	if fs := checks.DOCX(docx); len(fs) > 0 {
		return fmt.Errorf("pandoc wrote an invalid DOCX: %s", fs[0].Message)
	}
	if _, err := e.SVGToPNG(ctx, []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10"/></svg>`)); err != nil {
		return fmt.Errorf("typst could not rasterise an SVG: %w", err)
	}
	return nil
}

// newReviewer returns the AI reviewer: the injected one (tests), else the
// OpenRouter client when FOLIO_OPENROUTER_API_KEY is set, else nil (the
// reviewer is disabled and review_document returns unavailable).
func newReviewer(cfg *config.Config, injected review.Reviewer, log *slog.Logger) (review.Reviewer, error) {
	if injected != nil {
		return injected, nil
	}
	if cfg.OpenRouterAPIKey == "" {
		log.Info("ai reviewer disabled (set FOLIO_OPENROUTER_API_KEY to enable it)")
		return nil, nil
	}
	or, err := review.NewOpenRouter(review.OpenRouterConfig{APIKey: cfg.OpenRouterAPIKey, Model: cfg.ReviewModel, MaxPages: cfg.ReviewMaxPages,
		BaseURL: cfg.OpenRouterBaseURL, Logger: log})
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}
	log.Info("ai reviewer enabled: page images are sent to OpenRouter when review_document runs and when templates are created or updated",
		"model", or.Model(), "max_pages", cfg.ReviewMaxPages)
	return or, nil
}

// New wires the service. It runs the startup maintenance and seeding before
// returning, so the handler is ready to serve.
func New(ctx context.Context, cfg *config.Config, o Options) (a *App, err error) {
	if o.Logger == nil {
		return nil, errors.New("app: Options.Logger is required")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.ReadFile == nil {
		o.ReadFile = os.ReadFile
	}
	if o.JobRoot == "" {
		o.JobRoot = os.TempDir()
	}
	log := o.Logger
	db, err := store.Open(ctx, cfg.DataDir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = db.Close()
		}
	}()
	blobs, err := store.NewBlobs(filepath.Join(cfg.DataDir, "blobs"), o.Now)
	if err != nil {
		return nil, err
	}
	rt, err := bundled.Materialize(filepath.Join(cfg.DataDir, "runtime"))
	if err != nil {
		return nil, err
	}
	pandoc, err := sandbox.LookupTool("pandoc")
	if err != nil {
		return nil, err
	}
	typst, err := sandbox.LookupTool("typst")
	if err != nil {
		return nil, err
	}
	level := sandbox.HostLevel()
	log.Info("sandbox", "sandbox_level", string(level), "landlock_abi", sandbox.ProbeABI())
	logMemoryBudget(log, cfg, o.ReadFile)
	limits := sandbox.DefaultLimits()
	limits.Timeout, limits.MemoryBytes = cfg.RenderTimeout, cfg.RenderMemory
	engine, err := render.NewEngine(render.EngineConfig{Runtime: rt, Pandoc: pandoc, Typst: typst, Self: o.Self, JobRoot: o.JobRoot, Limits: limits})
	if err != nil {
		return nil, err
	}
	if err := selfTest(ctx, engine); err != nil {
		return nil, fmt.Errorf("app: tool self-test: %w", err)
	}
	rv, err := newReviewer(cfg, o.Reviewer, log)
	if err != nil {
		return nil, err
	}
	queue := render.NewQueue(cfg.RenderConcurrency, cfg.RenderQueue)
	// Web UI previews and example conversions get their own small queue so
	// they can never take a render slot from agents.
	previewQueue := render.NewQueue(cfg.UIPreviewConcurrency, 4)
	as := &assets.Service{DB: db, Blobs: blobs, Fetcher: assets.NewFetcher(cfg.MaxAssetBytes), Raster: engine, MaxBytes: cfg.MaxAssetBytes, Now: o.Now}
	pipeline := render.NewPipeline(render.PipelineConfig{Engine: engine, Queue: queue, Assets: as,
		Limits:   validate.Limits{MaxMarkdownBytes: cfg.MaxMarkdownBytes, MaxNesting: cfg.MaxNesting, MaxTableCells: cfg.MaxTableCells},
		MaxPages: cfg.MaxPages})
	cat := &catalog.Service{DB: db, Blobs: blobs, Renderer: pipeline, Assets: as, PackagesDir: rt.PackagesDir, Now: o.Now,
		PreviewTTL: cfg.PreviewRetention, PreviewPagesMax: cfg.PreviewPagesStored, DefaultBrandID: cfg.DefaultBrand,
		MaxLayoutTypstBytes: cfg.MaxLayoutTypstBytes, MaxInlineBytes: cfg.MaxInlineTemplateBytes}
	previewCat := *cat
	previewCat.Renderer = render.NewPipeline(render.PipelineConfig{Engine: engine, Queue: previewQueue, Assets: as,
		Limits:   validate.Limits{MaxMarkdownBytes: cfg.MaxMarkdownBytes, MaxNesting: cfg.MaxNesting, MaxTableCells: cfg.MaxTableCells},
		MaxPages: cfg.MaxPages})
	docs := &documents.Service{DB: db, Blobs: blobs, Renderer: pipeline, Resolver: cat, Now: o.Now,
		PreviewPages: cfg.PreviewPagesStored, GCGrace: cfg.BlobGCGrace}
	mc := maintenance.Config{Documents: docs, DB: db, BackupDir: filepath.Join(cfg.DataDir, "backups"), JobRoot: o.JobRoot,
		JobMaxAge: 3 * cfg.RenderTimeout, TrashRetention: cfg.TrashRetention, BackupInterval: cfg.BackupInterval,
		BackupKeep: cfg.BackupKeep, ActivityRetention: cfg.ActivityRetention, Now: o.Now, Logger: log}
	if err := maintenance.Startup(ctx, mc); err != nil {
		return nil, err
	}
	seeded := false
	if cfg.Seed {
		res, err := seed.Apply(ctx, db, cat, as, o.Now(), log)
		if err != nil {
			return nil, err
		}
		if seeded = res.Seeded; seeded {
			log.Info("seeded starter content", "brand", "example", "templates", strings.Join(seed.TemplateIDs, ","), "seed_version", seed.Version)
		}
	}
	logDefaultBrand(ctx, log, cat)
	packages, err := bundled.VendoredPackages(rt.PackagesDir)
	if err != nil {
		return nil, err
	}
	bc := activity.NewBroadcaster()
	rec := activity.NewRecorder(db, bc, log, o.Now)
	srv := mcpserver.New(&mcpserver.Deps{Catalog: cat, Documents: docs, Assets: as, Validator: pipeline, Raster: engine, Queue: queue,
		Blobs: blobs, DB: db, Reviewer: rv, Activity: rec, Config: cfg, Version: o.Version, Sandbox: string(level), Packages: packages,
		Logger: log, Now: o.Now, ProgressInterval: o.ProgressInterval})
	var ui http.Handler
	var uiClose func()
	if cfg.UIEnabled() {
		var assistant *ai.Client
		if cfg.OpenRouterAPIKey != "" {
			if assistant, err = ai.New(ai.Config{APIKey: cfg.OpenRouterAPIKey, Model: cfg.AIModel, BaseURL: cfg.OpenRouterBaseURL, Logger: log}); err != nil {
				return nil, fmt.Errorf("app: %w", err)
			}
			log.Info("web UI assistants enabled: content you send to an assistant goes to OpenRouter", "model", cfg.AIModel)
		}
		ws, err := webui.New(webui.Options{Config: cfg, DB: db, Blobs: blobs, Catalog: cat, Documents: docs, Assets: as,
			Validator: pipeline, Raster: engine, Queue: queue, Preview: &previewCat, PreviewQueue: previewQueue, Convert: engine, Activity: rec, Broadcaster: bc, AI: assistant,
			Reviewer: rv != nil, Version: o.Version, Sandbox: string(level), Packages: packages, Logger: log, Now: o.Now})
		if err != nil {
			return nil, err
		}
		ui, uiClose = ws, ws.Close
		log.Info("web UI enabled", "path", "/ui/")
	}
	h, err := httpserver.NewHandler(httpserver.Options{
		MCP: mcpserver.Handler(srv, cfg.MaxRequestBytes, log), Tokens: cfg.Tokens, InsecureNoAuth: cfg.InsecureNoAuth, UI: ui,
		Files: docs, MaxRequestBytes: cfg.MaxRequestBytes, Logger: log, Ping: db.R.PingContext,
		Health: httpserver.Health{Version: o.Version, Sandbox: string(level), SchemaVersion: store.SchemaVersion(), Reviewer: rv != nil},
	})
	if err != nil {
		return nil, err
	}
	// The activity writer runs until Close, so it outlives the HTTP drain
	// and records the last calls.
	rctx, stopRec := context.WithCancel(context.WithoutCancel(ctx))
	recDone := make(chan struct{})
	go func() {
		defer close(recDone)
		rec.Run(rctx)
	}()
	return &App{Config: cfg, Handler: h, DB: db, Catalog: cat, Documents: docs, Assets: as, Maintenance: mc,
		Activity: rec, Broadcaster: bc, stopRec: stopRec, recDone: recDone, closeUI: uiClose, Seeded: seeded, log: log}, nil
}

// Run serves on ln until ctx is cancelled, with the periodic maintenance and,
// in insecure mode, a warning every minute. It drains in-flight requests for
// up to FOLIO_RENDER_TIMEOUT + 20 s before returning.
func (a *App) Run(ctx context.Context, ln net.Listener) error {
	mctx, stop := context.WithCancel(ctx)
	defer stop()
	maintDone := make(chan struct{})
	go func() {
		defer close(maintDone)
		maintenance.Run(mctx, a.Maintenance)
	}()
	if a.Config.InsecureNoAuth && len(a.Config.Tokens) == 0 {
		go func() {
			t := time.NewTicker(time.Minute)
			defer t.Stop()
			for {
				a.log.Warn("FOLIO_INSECURE_NO_AUTH is set: /mcp accepts requests without a token; never use this outside local development")
				select {
				case <-mctx.Done():
					return
				case <-t.C:
				}
			}
		}()
	}
	a.log.Info("listening", "addr", ln.Addr().String(), "public_url", a.Config.PublicURL)
	// Live activity streams are closed before the drain starts.
	beforeShutdown := []func(){a.Broadcaster.Close}
	if a.closeUI != nil {
		beforeShutdown = append(beforeShutdown, a.closeUI)
	}
	err := httpserver.Serve(ctx, ln, a.Handler, a.Config.RenderTimeout+20*time.Second, a.log, beforeShutdown...)
	stop()
	<-maintDone // the caller closes the database next
	return err
}

// Close stops web UI jobs, flushes and stops the activity writer, then
// closes the database. Call it after Run returns (the HTTP drain is over),
// so the rows of the last calls are written.
func (a *App) Close() error {
	if a.closeUI != nil {
		a.closeUI()
	}
	a.stopRec()
	<-a.recDone
	return a.DB.Close()
}
