// SPDX-License-Identifier: AGPL-3.0-only

package maintenance

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/documents"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/render"
	"github.com/vocdoni/folio/internal/store"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type fakeRenderer struct{}

func (fakeRenderer) Render(_ context.Context, req render.Request) (*render.Result, error) {
	canon, sum, err := render.CanonicalInput(model.Ref{ID: req.Template.ID, Version: req.TemplateVersion}, model.Ref{ID: req.Brand.ID, Version: req.BrandVersion}, req.Input)
	if err != nil {
		return nil, err
	}
	return &render.Result{Canonical: canon, InputSHA256: sum,
		PDF: []byte("%PDF-" + sum), DOCX: []byte("PK-" + sum), Pages: [][]byte{[]byte("png-" + sum)}}, nil
}

type noAssets struct{}

func (noAssets) Asset(context.Context, string, bool) (model.ResolvedAsset, error) {
	return model.ResolvedAsset{}, model.ErrAssetNotFound
}

type fixture struct {
	cfg  Config
	cat  *catalog.Service
	docs *documents.Service
	clk  *clock
	dir  string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	clk := &clock{t: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}
	blobs, _ := store.NewBlobs(filepath.Join(dir, "blobs"), clk.Now)
	cat := &catalog.Service{DB: db, Blobs: blobs, Renderer: fakeRenderer{}, Assets: noAssets{}, Now: clk.Now, PreviewTTL: 24 * time.Hour, PreviewPagesMax: 3}
	b := model.Brand{ID: "example", Name: "Example",
		Colors: model.BrandColors{Primary: "#000000", Secondary: "#000000", Accent: "#000000", Text: "#000000", Muted: "#000000", Background: "#FFFFFF"},
		Fonts:  model.BrandFonts{Heading: "Inter", Body: "Inter", Mono: "JetBrains Mono"}}
	if _, err := cat.CreateBrand(ctx, "seed", b, ""); err != nil {
		t.Fatal(err)
	}
	tp := model.Template{ID: "memo", Name: "Memo", BrandID: "example", Languages: []string{"en"}, Layout: model.Layout{Base: "report"},
		Sample: model.Sample{Markdown: "x", Metadata: json.RawMessage(`{"title":"T","date":"2026-10-18","language":"en"}`)}}
	if _, err := cat.CreateTemplate(ctx, "seed", tp, ""); err != nil {
		t.Fatal(err)
	}
	docs := &documents.Service{DB: db, Blobs: blobs, Renderer: fakeRenderer{}, Resolver: cat, Now: clk.Now, PreviewPages: 3, GCGrace: time.Hour}
	cfg := Config{Documents: docs, DB: db, BackupDir: filepath.Join(dir, "backups"), JobRoot: filepath.Join(dir, "jobs"), JobMaxAge: 3 * time.Minute,
		TrashRetention: 720 * time.Hour, BackupInterval: 24 * time.Hour, BackupKeep: 2, Interval: 10 * time.Millisecond, Now: clk.Now,
		Logger: slog.New(slog.DiscardHandler)}
	return &fixture{cfg: cfg, cat: cat, docs: docs, clk: clk, dir: dir}
}

func (f *fixture) render(t *testing.T) *documents.RenderResult {
	t.Helper()
	r, err := f.docs.Render(context.Background(), "alice", documents.RenderRequest{TemplateID: "memo", Input: model.DocumentInput{
		Markdown: "body " + f.clk.Now().String(), Metadata: json.RawMessage(`{"title":"T","date":"2026-10-18","language":"en"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func blobCount(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(filepath.Join(dir, "blobs", "sha256"), func(_ string, d os.DirEntry, _ error) error {
		if d != nil && d.Type().IsRegular() {
			n++
		}
		return nil
	})
	return n
}

func TestRetentionPassPurgesTrashAndExpiredPreviews(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	keep, gone := f.render(t), f.render(t)
	if err := f.docs.Trash(ctx, "alice", gone.DocumentID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.cat.PreviewTemplate(ctx, "memo", 0); err != nil {
		t.Fatal(err)
	}
	before := blobCount(t, f.dir)
	f.clk.Add(721 * time.Hour) // past trash retention, preview expiry and blob grace
	purged, expired, err := RetentionPass(ctx, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if purged != 1 || expired != 6 { // two template previews (create + preview_template) × pdf, docx, png
		t.Fatalf("purged %d, expired %d", purged, expired)
	}
	if _, err := f.docs.Get(ctx, gone.DocumentID, 0); err == nil {
		t.Fatal("trashed document survived retention")
	}
	if _, err := f.docs.Get(ctx, keep.DocumentID, 0); err != nil {
		t.Fatalf("live document purged: %v", err)
	}
	for _, file := range keep.Files {
		if _, _, err := f.docs.FileForDownload(ctx, file.ID); err != nil {
			t.Fatalf("live file not served: %v", err)
		}
	}
	if after := blobCount(t, f.dir); after >= before {
		t.Fatalf("blobs not collected: %d → %d", before, after)
	}
}

func TestStartupSweepsJobDirsAndCollectsBlobs(t *testing.T) {
	f := setup(t)
	old, fresh := filepath.Join(f.cfg.JobRoot, "folio-job-old"), filepath.Join(f.cfg.JobRoot, "folio-job-fresh")
	other := filepath.Join(f.cfg.JobRoot, "not-a-job")
	for _, d := range []string{old, fresh, other} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-time.Hour)
	_ = os.Chtimes(old, past, past)
	_ = os.Chtimes(other, past, past)
	orphan, err := f.docs.Blobs.Put([]byte("orphan blob from a crashed render"))
	if err != nil {
		t.Fatal(err)
	}
	f.clk.Add(2 * time.Hour) // orphan is older than the GC grace
	if err := Startup(context.Background(), f.cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("stale job dir survived")
	}
	for _, d := range []string{fresh, other} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("%s removed: %v", d, err)
		}
	}
	if _, err := f.docs.Blobs.Get(orphan); err == nil {
		t.Error("orphan blob survived startup GC")
	}
	f.cfg.JobRoot = filepath.Join(f.dir, "missing")
	if err := Startup(context.Background(), f.cfg); err != nil {
		t.Fatalf("a missing job root is not an error: %v", err)
	}
}

func TestBackupSnapshotsAndPrunes(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	dir := filepath.Join(f.dir, "it's backups") // quoting must not matter
	var paths []string
	for i := 0; i < 3; i++ {
		p, err := Backup(ctx, f.cfg.DB, dir, 2, f.clk.Now())
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
		f.clk.Add(time.Hour)
	}
	names, _ := listBackups(dir)
	if want := []string{filepath.Base(paths[1]), filepath.Base(paths[2])}; !slices.Equal(names, want) {
		t.Fatalf("kept %v, want %v", names, want)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("leftover files: %v", entries)
	}
	snap, err := store.Open(ctx, filepath.Join(f.dir, "restore"))
	if err != nil {
		t.Fatal(err)
	}
	snap.Close()
	raw, err := os.ReadFile(paths[2])
	if err != nil || len(raw) < 100 || string(raw[:15]) != "SQLite format 3" {
		t.Fatalf("snapshot is not a SQLite database: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, "restore", "folio.db"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(ctx, filepath.Join(f.dir, "restore"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var n int
	if err := restored.R.QueryRow(`SELECT count(*) FROM template_heads WHERE id = 'memo'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("restored snapshot: %d %v", n, err)
	}
}

func TestBackupPrunesStaleTempSnapshots(t *testing.T) {
	f := setup(t)
	dir := filepath.Join(f.dir, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, ".tmp-folio-20200101T000000Z.db")
	if err := os.WriteFile(stale, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Backup(context.Background(), f.cfg.DB, dir, 2, f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale temp snapshot survived: %v", err)
	}
}

func TestNextBackup(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	if _, ok, _ := NextBackup(dir, 0, now); ok {
		t.Fatal("interval 0 disables backups")
	}
	if due, ok, _ := NextBackup(filepath.Join(dir, "none"), 24*time.Hour, now); !ok || !due.Equal(now) {
		t.Fatalf("no snapshot yet: due %v", due)
	}
	_ = os.WriteFile(filepath.Join(dir, backupName(now.Add(-time.Hour))), nil, 0o600)
	if due, _, _ := NextBackup(dir, 24*time.Hour, now); !due.Equal(now.Add(23 * time.Hour)) {
		t.Fatalf("due %v", due)
	}
}

func TestRunAppliesRetentionAndBacksUp(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	r := f.render(t)
	if err := f.docs.Trash(context.Background(), "alice", r.DocumentID, false); err != nil {
		t.Fatal(err)
	}
	f.clk.Add(721 * time.Hour)
	done := make(chan struct{})
	go func() { Run(ctx, f.cfg); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := f.docs.Get(context.Background(), r.DocumentID, 0)
		names, _ := listBackups(f.cfg.BackupDir)
		if err != nil && len(names) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Run did not purge and back up: doc err %v, backups %v", err, names)
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // several more ticks: no second backup within the interval
	if names, _ := listBackups(f.cfg.BackupDir); len(names) != 1 {
		t.Fatalf("backups %v", names)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
}
