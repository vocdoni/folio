// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vocdoni/folio/internal/assets"
	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/config"
	"github.com/vocdoni/folio/internal/documents"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/render"
	"github.com/vocdoni/folio/internal/store"
)

// testPNG returns a white w×h PNG with a dark band (so it is not blank).
func testPNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{255, 255, 255, 255}
			if y > h/3 && y < h/2 {
				c = color.RGBA{20, 40, 160, 255}
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// fakeRenderer stands in for render.Pipeline: it validates nothing, returns
// a real two-page PDF and two page PNGs, and fails on demand.
type fakeRenderer struct {
	pdf   []byte
	page  []byte
	calls atomic.Int32
	delay time.Duration
}

func newFakeRenderer(t *testing.T) *fakeRenderer {
	t.Helper()
	pdf, err := os.ReadFile(filepath.Join("..", "checks", "testdata", "two-pages.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	return &fakeRenderer{pdf: pdf, page: testPNG(1240, 1754)}
}

func (f *fakeRenderer) Render(ctx context.Context, req render.Request) (*render.Result, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if strings.Contains(req.Input.Markdown, "FAIL") {
		return nil, &model.Error{Code: model.ErrValidationFailed, Message: "1 problem(s) must be fixed before rendering; see details",
			Details: []model.Finding{{Code: model.CodeMissingSection, Severity: model.SeverityError, Path: "/markdown",
				Message: `required section "Scope" is missing`, Hint: "add a heading named Scope"}}}
	}
	canon, sum, err := render.CanonicalInput(model.Ref{ID: req.Template.ID, Version: req.TemplateVersion},
		model.Ref{ID: req.Brand.ID, Version: req.BrandVersion}, req.Input)
	if err != nil {
		return nil, err
	}
	res := &render.Result{Canonical: canon, InputSHA256: sum, Computed: json.RawMessage(`{"budget":{"total":"121.00"}}`), Warnings: []model.Finding{}}
	if strings.Contains(req.Input.Markdown, "TODO") {
		res.Warnings = append(res.Warnings, model.Finding{Code: model.CodePlaceholder, Severity: model.SeverityWarning, Path: "/markdown", Message: `placeholder "TODO"`})
	}
	if req.ValidateOnly {
		return res, nil
	}
	res.PDF, res.DOCX, res.PageCount = f.pdf, []byte("PK\x03\x04docx-"+sum), 2
	res.Pages = [][]byte{f.page, f.page}
	if len(req.Input.Formats) == 1 && req.Input.Formats[0] == model.FormatDOCX {
		// DOCX-only: no PDF; only the stored page-1 preview exists.
		res.PDF, res.PageCount, res.Pages = nil, 0, [][]byte{f.page}
	}
	return res, nil
}

type fakeRaster struct{ calls atomic.Int32 }

func (r *fakeRaster) RasterizePDFPage(_ context.Context, _ []byte, page, _ int) ([]byte, error) {
	r.calls.Add(1)
	return testPNG(1240+page, 1754), nil
}

func (r *fakeRaster) SVGToPNG(context.Context, []byte) ([]byte, error) { return testPNG(200, 60), nil }

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }

func (c *clock) Add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type env struct {
	d      *Deps
	r      *fakeRenderer
	raster *fakeRaster
	clk    *clock
	logs   *syncBuffer
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// newEnv wires the real catalog, documents and assets services over a temp
// SQLite database with the fake renderer and rasteriser.
func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	clk := &clock{t: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}
	blobs, err := store.NewBlobs(filepath.Join(dir, "blobs"), clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	r, raster := newFakeRenderer(t), &fakeRaster{}
	cfg := &config.Config{PublicURL: "https://folio.example.test", MaxAssetBytes: 1 << 20, MaxPages: 200, PreviewPagesStored: 1,
		RenderConcurrency: 2, RenderQueue: 4, RenderTimeout: time.Minute, PreviewRetention: 24 * time.Hour, TrashRetention: 720 * time.Hour,
		MaxMarkdownBytes: 1 << 20, MaxNesting: 32, MaxTableCells: 10000, MaxRequestBytes: 16 << 20, ReviewMaxPages: 20}
	as := &assets.Service{DB: db, Blobs: blobs, Fetcher: assets.NewFetcher(cfg.MaxAssetBytes), Raster: raster, MaxBytes: cfg.MaxAssetBytes, Now: clk.Now}
	cat := &catalog.Service{DB: db, Blobs: blobs, Renderer: r, Assets: as, PackagesDir: t.TempDir(), Now: clk.Now,
		PreviewTTL: cfg.PreviewRetention, PreviewPagesMax: cfg.PreviewPagesStored}
	docs := &documents.Service{DB: db, Blobs: blobs, Renderer: r, Resolver: cat, Now: clk.Now, PreviewPages: cfg.PreviewPagesStored, GCGrace: time.Hour}
	logs := &syncBuffer{}
	d := &Deps{Catalog: cat, Documents: docs, Assets: as, Validator: r, Raster: raster, Queue: render.NewQueue(2, 4),
		Blobs: blobs, DB: db, Config: cfg, Version: "test", Sandbox: "rlimits-only",
		Logger: slog.New(slog.NewJSONHandler(logs, nil)), Now: clk.Now, ProgressInterval: 50 * time.Millisecond}
	return &env{d: d, r: r, raster: raster, clk: clk, logs: logs}
}

// eventually polls cond for up to two seconds: client notification handlers
// run asynchronously, so the last notifications may arrive after the result.
func eventually(cond func() bool) bool {
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return cond()
}

// connect serves s over in-memory transports and returns a client session.
func connect(t *testing.T, s *mcp.Server, opts *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, opts).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// callResult calls a tool and decodes structuredContent into out (when non-nil).
func callResult(t *testing.T, cs *mcp.ClientSession, name string, args any, out any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	if out != nil && res.StructuredContent != nil {
		raw, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s: decode structuredContent: %v", name, err)
		}
	}
	return res
}

// callOK fails the test when the call returns an error result.
func callOK(t *testing.T, cs *mcp.ClientSession, name string, args any, out any) *mcp.CallToolResult {
	t.Helper()
	res := callResult(t, cs, name, args, out)
	if res.IsError {
		raw, _ := json.Marshal(res.StructuredContent)
		t.Fatalf("%s failed: %s", name, raw)
	}
	return res
}

// callErr expects an error result and returns structuredContent.error.
func callErr(t *testing.T, cs *mcp.ClientSession, name string, args any) ToolError {
	t.Helper()
	var body struct {
		Error ToolError `json:"error"`
	}
	res := callResult(t, cs, name, args, &body)
	if !res.IsError {
		t.Fatalf("%s: expected an error result", name)
	}
	return body.Error
}
