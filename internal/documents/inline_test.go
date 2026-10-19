// SPDX-License-Identifier: AGPL-3.0-only

package documents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/store"
)

var inlineIDRE = regexp.MustCompile(`^inline-[0-9a-f]{16}$`)

func inlineSpec(t *testing.T, raw string) *model.Template {
	t.Helper()
	var tp model.Template
	if err := json.Unmarshal([]byte(raw), &tp); err != nil {
		t.Fatal(err)
	}
	return &tp
}

func inlineRows(t *testing.T, s *Service) int {
	t.Helper()
	var n int
	if err := s.DB.R.QueryRow(`SELECT count(*) FROM templates WHERE id LIKE 'inline-%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	var heads int
	_ = s.DB.R.QueryRow(`SELECT count(*) FROM template_heads WHERE id LIKE 'inline-%'`).Scan(&heads)
	if heads != n {
		t.Fatalf("%d inline template rows but %d heads", n, heads)
	}
	return n
}

// Acceptance 1 and 2: template: {} renders with the default brand and the
// report layout; the same spec twice shares one row; a re-send is unchanged.
func TestInlineRender(t *testing.T) {
	s, r, _ := setup(t)
	ctx := context.Background()
	a, err := s.Render(ctx, "alice", RenderRequest{Template: inlineSpec(t, `{}`), Input: in("v1")})
	if err != nil {
		t.Fatal(err)
	}
	if !inlineIDRE.MatchString(a.Template.ID) || !a.Template.Inline || a.Template.Version != 1 || a.Brand.ID != "example" || len(a.Files) != 3 {
		t.Fatalf("render: %+v", a)
	}
	var note, by, spec string
	if err := s.DB.R.QueryRow(`SELECT note, created_by, spec FROM templates WHERE id = ?`, a.Template.ID).Scan(&note, &by, &spec); err != nil ||
		note != "inline" || by != "alice" || !strings.Contains(spec, `"base":"report"`) {
		t.Fatalf("row: %q %q %s %v", note, by, spec, err)
	}
	var detail string
	_ = s.DB.R.QueryRow(`SELECT detail FROM audit WHERE action = 'render_document' AND target_id = ?`, a.DocumentID).Scan(&detail)
	if detail != `{"revision":1,"template":"`+a.Template.ID+`"}` {
		t.Fatalf("audit detail %s", detail)
	}
	b, err := s.Render(ctx, "bob", RenderRequest{Template: inlineSpec(t, `{"name":"ignored"}`), Input: in("other")})
	if err != nil || b.Template.ID != a.Template.ID || b.DocumentID == a.DocumentID {
		t.Fatalf("second document: %+v %v", b, err)
	}
	if n := inlineRows(t, s); n != 1 {
		t.Fatalf("inline rows = %d, want 1", n)
	}
	one, calls := 1, r.calls.Load()
	again, err := s.Render(ctx, "alice", RenderRequest{DocumentID: a.DocumentID, ExpectedRevision: &one, Template: inlineSpec(t, `{}`), Input: in("v1")})
	if err != nil || !again.Unchanged || again.Revision != 1 || !again.Template.Inline || r.calls.Load() != calls {
		t.Fatalf("re-send: %+v %v calls %d→%d", again, err, calls, r.calls.Load())
	}
}

// Acceptance 3: an invalid spec fails before rendering with findings under
// /template; a valid spec whose render fails writes no row either.
func TestInlineFailuresWriteNoRow(t *testing.T) {
	s, r, _ := setup(t)
	ctx := context.Background()
	_, err := s.Render(ctx, "alice", RenderRequest{Template: inlineSpec(t, `{"layout":{"base":"memo"}}`), Input: in("v1")})
	var me *model.Error
	if !errors.As(err, &me) || me.Code != model.ErrValidationFailed || len(me.Details) == 0 || me.Details[0].Path != "/template/layout/base" || r.calls.Load() != 0 {
		t.Fatalf("invalid spec: %v calls=%d", err, r.calls.Load())
	}
	if _, err := s.Render(ctx, "alice", RenderRequest{Template: inlineSpec(t, `{}`), Input: in("invalid")}); code(err) != model.ErrValidationFailed {
		t.Fatalf("render failure: %v", err)
	}
	var docs int
	_ = s.DB.R.QueryRow(`SELECT count(*) FROM documents`).Scan(&docs)
	if n := inlineRows(t, s); n != 0 || docs != 0 {
		t.Fatalf("failures wrote %d inline rows and %d documents", n, docs)
	}
	for name, req := range map[string]RenderRequest{
		"template and template_id":      {Template: inlineSpec(t, `{}`), TemplateID: "proposal", Input: in("x")},
		"template and template_version": {Template: inlineSpec(t, `{}`), TemplateVersion: 1, Input: in("x")},
	} {
		if _, err := s.Render(ctx, "alice", req); code(err) != model.ErrInvalidInput {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Acceptance 4: revisions reuse, switch to another inline template, switch
// to a stored one; template_id: inline-<hash> reuses an inline template.
func TestInlineRevisions(t *testing.T) {
	s, _, _ := setup(t)
	ctx := context.Background()
	cat := s.Resolver.(*catalog.Service)
	acme := model.Brand{ID: "acme", Name: "Acme",
		Colors: model.BrandColors{Primary: "#111111", Secondary: "#111111", Accent: "#111111", Text: "#111111", Muted: "#111111", Background: "#FFFFFF"},
		Fonts:  model.BrandFonts{Heading: "Inter", Body: "Inter", Mono: "JetBrains Mono"}}
	if _, err := cat.CreateBrand(ctx, "seed", acme, ""); err != nil {
		t.Fatal(err)
	}
	first, err := s.Render(ctx, "alice", RenderRequest{Template: inlineSpec(t, `{}`), BrandID: "acme", Input: in("v1")})
	if err != nil || first.Brand.ID != "acme" {
		t.Fatalf("new: %+v %v", first, err)
	}
	inlineID := first.Template.ID
	rev := func(n int, req RenderRequest) *RenderResult {
		t.Helper()
		req.DocumentID, req.ExpectedRevision = first.DocumentID, &n
		res, err := s.Render(ctx, "alice", req)
		if err != nil {
			t.Fatalf("revision after %d: %v", n, err)
		}
		return res
	}
	r2 := rev(1, RenderRequest{BrandID: "example", Input: in("v2")}) // reuse; the call brand overrides at render time
	if r2.Template.ID != inlineID || r2.Brand.ID != "example" {
		t.Fatalf("reuse: %+v", r2)
	}
	// Two live brands and no FOLIO_DEFAULT_BRAND: a new inline template names its brand.
	r3 := rev(2, RenderRequest{Template: inlineSpec(t, `{"layout":{"base":"letter"}}`), BrandID: "acme", Input: in("v3")})
	if !r3.Template.Inline || r3.Template.ID == inlineID {
		t.Fatalf("switch to another inline template: %+v", r3)
	}
	r4 := rev(3, RenderRequest{TemplateID: "proposal", Input: in("v4")})
	if r4.Template.ID != "proposal" || r4.Template.Inline {
		t.Fatalf("switch to stored: %+v", r4)
	}
	d, err := s.Get(ctx, first.DocumentID, 0)
	if err != nil || d.TemplateID != "proposal" || d.TemplateInline || !d.Revisions[1].Template.Inline || d.Revisions[3].Template.ID != inlineID {
		t.Fatalf("history: %+v %v", d, err)
	}
	other, err := s.Render(ctx, "bob", RenderRequest{TemplateID: inlineID, Input: in("reuse by id")})
	if err != nil || other.Template.ID != inlineID || !other.Template.Inline || other.Brand.ID != "acme" {
		t.Fatalf("template_id: inline id: %+v %v", other, err)
	}
	if _, err := s.Render(ctx, "bob", RenderRequest{TemplateID: inlineID, TemplateVersion: 2, Input: in("v2")}); code(err) != model.ErrNotFound {
		t.Fatalf("inline version 2: %v", err)
	}
	if n := inlineRows(t, s); n != 2 {
		t.Fatalf("inline rows = %d, want 2", n)
	}
}

// Concurrent renders of the same new inline spec all succeed on one row.
func TestConcurrentInlineRenders(t *testing.T) {
	s, r, _ := setup(t)
	r.delay = 20 * time.Millisecond // every render finishes before the first commit
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make([]error, 4)
	ids := make([]string, 4)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := s.Render(ctx, "alice", RenderRequest{Template: inlineSpec(t, `{"layout":{"base":"quote"}}`), Input: in("doc " + string(rune('a'+i)))})
			if errs[i] = err; err == nil {
				ids[i] = res.Template.ID
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil || ids[i] != ids[0] {
			t.Fatalf("render %d: %v (%s vs %s)", i, err, ids[i], ids[0])
		}
	}
	if n := inlineRows(t, s); n != 1 {
		t.Fatalf("inline rows = %d, want 1", n)
	}
}

// The transaction insert is idempotent for the same spec and refuses a
// different spec under the same id (hash-prefix collision guard).
func TestInsertInlineCollisionGuard(t *testing.T) {
	s, _, _ := setup(t)
	ctx := context.Background()
	const id = "inline-00000000000000aa"
	put := func(spec string) error {
		return s.DB.WriteTx(ctx, func(tx *sql.Tx) error { return insertInline(ctx, tx, id, []byte(spec), "alice", store.Now(s.Now())) })
	}
	if err := put(`{"id":"inline-00000000000000aa","name":"A"}`); err != nil {
		t.Fatal(err)
	}
	if err := put(`{"id":"inline-00000000000000aa","name":"A"}`); err != nil {
		t.Fatalf("same spec again: %v", err)
	}
	if err := put(`{"id":"inline-00000000000000aa","name":"B"}`); code(err) != model.ErrInternal {
		t.Fatalf("different spec: %v", err)
	}
	if n := inlineRows(t, s); n != 1 {
		t.Fatalf("inline rows = %d", n)
	}
}

// list_documents' inline filter and the template_inline flags.
func TestListInlineFilter(t *testing.T) {
	s, _, _ := setup(t)
	ctx := context.Background()
	if _, err := s.Render(ctx, "alice", RenderRequest{TemplateID: "proposal", Input: in("stored")}); err != nil {
		t.Fatal(err)
	}
	inl, err := s.Render(ctx, "alice", RenderRequest{Template: inlineSpec(t, `{}`), Input: in("inline")})
	if err != nil {
		t.Fatal(err)
	}
	all, _, err := s.List(ctx, ListFilter{})
	if err != nil || len(all) != 2 || !all[0].TemplateInline || all[1].TemplateInline {
		t.Fatalf("list: %+v %v", all, err)
	}
	only, _, err := s.List(ctx, ListFilter{Inline: true})
	if err != nil || len(only) != 1 || only[0].ID != inl.DocumentID {
		t.Fatalf("inline filter: %+v %v", only, err)
	}
	if _, _, err := s.List(ctx, ListFilter{Inline: true, TemplateID: "proposal"}); code(err) != model.ErrInvalidInput {
		t.Fatalf("template_id with inline: %v", err)
	}
	d, err := s.Get(ctx, inl.DocumentID, 0)
	if err != nil || !d.TemplateInline || !d.Revision.Template.Inline {
		t.Fatalf("get: %+v %v", d, err)
	}
}

// save_inline_template's documents half: the inline template and the
// latest input of a document.
func TestInlineSource(t *testing.T) {
	s, _, _ := setup(t)
	ctx := context.Background()
	inl, err := s.Render(ctx, "alice", RenderRequest{Template: inlineSpec(t, `{}`), Input: in("# Body")})
	if err != nil {
		t.Fatal(err)
	}
	id, sample, err := s.InlineSource(ctx, inl.DocumentID)
	if err != nil || id != inl.Template.ID || sample.Markdown != "# Body" || !strings.Contains(string(sample.Metadata), "Website redesign") {
		t.Fatalf("source: %s %+v %v", id, sample, err)
	}
	stored, _ := s.Render(ctx, "alice", RenderRequest{TemplateID: "proposal", Input: in("x")})
	if _, _, err := s.InlineSource(ctx, stored.DocumentID); code(err) != model.ErrInvalidInput {
		t.Fatalf("stored document: %v", err)
	}
	if _, _, err := s.InlineSource(ctx, "nope"); code(err) != model.ErrNotFound {
		t.Fatalf("unknown document: %v", err)
	}
}
