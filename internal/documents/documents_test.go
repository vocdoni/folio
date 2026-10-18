// SPDX-License-Identifier: AGPL-3.0-only

package documents

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/render"
	"github.com/vocdoni/folio/internal/store"
)

// fakeRenderer returns outputs derived from the canonical input; it counts calls.
type fakeRenderer struct {
	calls atomic.Int32
	delay time.Duration
}

func (f *fakeRenderer) Render(_ context.Context, req render.Request) (*render.Result, error) {
	f.calls.Add(1)
	time.Sleep(f.delay)
	if req.Input.Markdown == "invalid" {
		return nil, &model.Error{Code: model.ErrValidationFailed, Message: "1 problem(s) must be fixed before rendering"}
	}
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

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func setup(t *testing.T) (*Service, *fakeRenderer, *clock) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	clk := &clock{t: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}
	blobs, _ := store.NewBlobs(dir+"/blobs", clk.Now)
	r := &fakeRenderer{}
	cat := &catalog.Service{DB: db, Blobs: blobs, Renderer: r, Assets: noAssets{}, Now: clk.Now, PreviewTTL: time.Hour, PreviewPagesMax: 3}
	b := model.Brand{ID: "example", Name: "Example",
		Colors: model.BrandColors{Primary: "#000000", Secondary: "#000000", Accent: "#000000", Text: "#000000", Muted: "#000000", Background: "#FFFFFF"},
		Fonts:  model.BrandFonts{Heading: "Inter", Body: "Inter", Mono: "JetBrains Mono"}}
	if _, err := cat.CreateBrand(ctx, "seed", b, ""); err != nil {
		t.Fatal(err)
	}
	tp := model.Template{ID: "proposal", Name: "P", BrandID: "example", Languages: []string{"en"}, Layout: model.Layout{Base: "proposal"},
		Sample: model.Sample{Markdown: "x", Metadata: json.RawMessage(`{"title":"T","date":"2026-10-18","language":"en"}`)}}
	if _, err := cat.CreateTemplate(ctx, "seed", tp, ""); err != nil {
		t.Fatal(err)
	}
	r.calls.Store(0)
	return &Service{DB: db, Blobs: blobs, Renderer: r, Resolver: cat, Now: clk.Now, PreviewPages: 3, GCGrace: time.Hour}, r, clk
}

func in(md string) model.DocumentInput {
	return model.DocumentInput{Markdown: md, Metadata: json.RawMessage(`{"title":"Website redesign","date":"2026-10-18","language":"en"}`)}
}

func code(err error) model.ErrorCode {
	var me *model.Error
	if errors.As(err, &me) {
		return me.Code
	}
	return ""
}

func TestConcurrentRevisionsExactlyOneWins(t *testing.T) {
	s, r, _ := setup(t)
	ctx := context.Background()
	first, err := s.Render(ctx, "alice", RenderRequest{TemplateID: "proposal", Input: in("v1")})
	if err != nil {
		t.Fatal(err)
	}
	r.delay = 20 * time.Millisecond // both renders finish before either commits
	one := 1
	var wg sync.WaitGroup
	errs := make([]error, 6)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = s.Render(ctx, "bob", RenderRequest{DocumentID: first.DocumentID, ExpectedRevision: &one, Input: in("v2-" + string(rune('a'+i)))})
		}(i)
	}
	wg.Wait()
	ok, conflicts := 0, 0
	for _, err := range errs {
		switch code(err) {
		case "":
			ok++
		case model.ErrConflict:
			conflicts++
		default:
			t.Fatalf("unexpected: %v", err)
		}
	}
	if ok != 1 || conflicts != 5 {
		t.Fatalf("ok=%d conflicts=%d", ok, conflicts)
	}
	d, _ := s.Get(ctx, first.DocumentID, 0)
	if d.LatestRevision != 2 || len(d.Revisions) != 2 {
		t.Fatalf("latest=%d revisions=%d", d.LatestRevision, len(d.Revisions))
	}
}

func TestIdempotencyKeyReturnsFirstDocument(t *testing.T) {
	s, r, clk := setup(t)
	ctx := context.Background()
	req := RenderRequest{TemplateID: "proposal", Input: in("hello"), IdempotencyKey: "retry-1"}
	a, err := s.Render(ctx, "alice", req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Render(ctx, "alice", req)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Unchanged || b.DocumentID != a.DocumentID || r.calls.Load() != 1 {
		t.Fatalf("retry: %+v calls=%d", b, r.calls.Load())
	}
	c, _ := s.Render(ctx, "carol", req) // keys are scoped per actor
	if c.DocumentID == a.DocumentID {
		t.Fatal("key leaked across actors")
	}
	clk.Add(IdempotencyWindow + time.Minute)
	d, err := s.Render(ctx, "alice", req)
	if err != nil || d.Unchanged || d.DocumentID == a.DocumentID {
		t.Fatalf("expired key reused: %+v %v", d, err)
	}
}

func TestIdempotencyConcurrentRetries(t *testing.T) {
	s, r, _ := setup(t)
	r.delay = 20 * time.Millisecond
	ctx := context.Background()
	var wg sync.WaitGroup
	ids := make([]string, 4)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := s.Render(ctx, "alice", RenderRequest{TemplateID: "proposal", Input: in("x"), IdempotencyKey: "k"})
			if err != nil {
				t.Error(err)
				return
			}
			ids[i] = res.DocumentID
		}(i)
	}
	wg.Wait()
	for _, id := range ids[1:] {
		if id != ids[0] {
			t.Fatalf("concurrent retries created different documents: %v", ids)
		}
	}
}

func TestDedupUnchanged(t *testing.T) {
	s, r, _ := setup(t)
	ctx := context.Background()
	a, _ := s.Render(ctx, "alice", RenderRequest{TemplateID: "proposal", Input: in("same")})
	one := 1
	b, err := s.Render(ctx, "alice", RenderRequest{DocumentID: a.DocumentID, ExpectedRevision: &one, Input: in("same")})
	if err != nil || !b.Unchanged || b.Revision != 1 || r.calls.Load() != 1 {
		t.Fatalf("dedup: %+v %v calls=%d", b, err, r.calls.Load())
	}
}

func TestTrashPurgeAndDownloads(t *testing.T) {
	s, _, clk := setup(t)
	ctx := context.Background()
	a, _ := s.Render(ctx, "alice", RenderRequest{TemplateID: "proposal", Input: in("doc")})
	fileID := a.Files[0].ID
	if _, _, err := s.FileForDownload(ctx, fileID); err != nil {
		t.Fatalf("live file: %v", err)
	}
	if err := s.Publish(ctx, "alice", a.DocumentID, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Trash(ctx, "alice", a.DocumentID, false); code(err) != model.ErrInvalidInput {
		t.Fatalf("published trash without confirm: %v", err)
	}
	if err := s.Trash(ctx, "alice", a.DocumentID, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.FileForDownload(ctx, fileID); code(err) != model.ErrNotFound {
		t.Fatalf("trashed file served: %v", err)
	}
	if err := s.Restore(ctx, "alice", a.DocumentID); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.Get(ctx, a.DocumentID, 0); d.Status != "published" {
		t.Fatalf("restored status = %s", d.Status)
	}
	sha := a.Files[0].SHA256
	clk.Add(2 * time.Hour) // blobs are now older than the grace period
	if err := s.Purge(ctx, "alice", a.DocumentID, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.FileForDownload(ctx, fileID); code(err) != model.ErrNotFound {
		t.Fatalf("purged file served: %v", err)
	}
	if _, err := s.Blobs.Get(sha); !errors.Is(err, store.ErrBlobNotFound) {
		t.Fatalf("purge left the blob: %v", err)
	}
}

func TestExpiredPreviewFileNotServed(t *testing.T) {
	s, _, clk := setup(t)
	ctx := context.Background()
	var id string
	if err := s.DB.R.QueryRowContext(ctx, `SELECT id FROM files WHERE template_id = 'proposal' LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.FileForDownload(ctx, id); err != nil {
		t.Fatalf("fresh preview: %v", err)
	}
	clk.Add(2 * time.Hour)
	if _, _, err := s.FileForDownload(ctx, id); code(err) != model.ErrNotFound {
		t.Fatalf("expired preview served: %v", err)
	}
}

func TestListNewestFirstWithCursor(t *testing.T) {
	s, _, clk := setup(t)
	ctx := context.Background()
	var ids []string
	for i := 0; i < 5; i++ {
		res, err := s.Render(ctx, "alice", RenderRequest{TemplateID: "proposal", Input: in("doc " + string(rune('a'+i)))})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, res.DocumentID)
		clk.Add(time.Second)
	}
	page1, next, err := s.List(ctx, ListFilter{Limit: 2})
	if err != nil || len(page1) != 2 || next == "" || page1[0].ID != ids[4] {
		t.Fatalf("page1 = %+v next=%q err=%v", page1, next, err)
	}
	var all []Summary
	cursor := ""
	for {
		page, n, err := s.List(ctx, ListFilter{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, page...)
		if n == "" {
			break
		}
		cursor = n
	}
	if len(all) != 5 || all[4].ID != ids[0] {
		t.Fatalf("paged %d docs", len(all))
	}
	_ = s.Trash(ctx, "alice", ids[0], false)
	if got, _, _ := s.List(ctx, ListFilter{}); len(got) != 4 {
		t.Fatalf("trashed listed by default: %d", len(got))
	}
}

// Folio derives no values from the input: a new revision stores the column
// default '{}' in revisions.computed and Get exposes no computed field.
func TestRevisionsCarryNoComputedValues(t *testing.T) {
	s, _, _ := setup(t)
	ctx := context.Background()
	r, err := s.Render(ctx, "alice", RenderRequest{TemplateID: "proposal", Input: in("v1")})
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := s.DB.R.QueryRowContext(ctx, `SELECT computed FROM revisions WHERE document_id = ? AND revision = 1`, r.DocumentID).Scan(&stored); err != nil || stored != "{}" {
		t.Fatalf("revisions.computed = %q %v", stored, err)
	}
	d, err := s.Get(ctx, r.DocumentID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []any{r, d} {
		raw, _ := json.Marshal(v)
		if strings.Contains(string(raw), `"computed"`) {
			t.Fatalf("output carries computed: %s", raw)
		}
	}
}

// A render pinned to an older template version that fails validation names
// the current version (starter templates are upgraded in place).
func TestPinnedOldVersionNamesTheLatest(t *testing.T) {
	s, _, _ := setup(t)
	ctx := context.Background()
	cat := s.Resolver.(*catalog.Service)
	tp, _, err := cat.ResolveTemplate(ctx, "proposal", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	tp.Name = "P v2"
	if _, err := cat.UpdateTemplate(ctx, "folio", "proposal", 1, tp, "starter upgrade"); err != nil {
		t.Fatal(err)
	}
	_, err = s.Render(ctx, "alice", RenderRequest{TemplateID: "proposal", TemplateVersion: 1, Input: in("invalid")})
	var me *model.Error
	if !errors.As(err, &me) || me.Code != model.ErrValidationFailed ||
		!strings.Contains(me.Message, "template proposal version 1 is not the latest: version 2 is current") {
		t.Fatalf("pinned v1: %v", err)
	}
	_, err = s.Render(ctx, "alice", RenderRequest{TemplateID: "proposal", Input: in("invalid")})
	if !errors.As(err, &me) || strings.Contains(me.Message, "not the latest") {
		t.Fatalf("latest: %v", err)
	}
}
