// SPDX-License-Identifier: AGPL-3.0-only

package catalog

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/store"
)

func errCode(err error) model.ErrorCode {
	var me *model.Error
	if errors.As(err, &me) {
		return me.Code
	}
	return ""
}

// insertInlineRow stores an inline template row the way documents.Render
// does (version 1, note "inline").
func insertInlineRow(t *testing.T, s *Service, id string) {
	t.Helper()
	tp := tmpl()
	tp.ID, tp.Name = id, "Inline template"
	spec, err := model.Canonical(tp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.W.Exec(`INSERT INTO templates (id, version, spec, note, created_by, created_at) VALUES (?, 1, ?, 'inline', 'alice', ?)`,
		id, string(spec), store.Now(s.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.W.Exec(`INSERT INTO template_heads (id, latest_version) VALUES (?, 1)`, id); err != nil {
		t.Fatal(err)
	}
}

// Acceptance 5: inline rows are invisible to every catalog writer and list,
// readable by id, and their ids are reserved.
func TestInlineTemplatesAreHiddenAndReserved(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	if _, err := s.CreateBrand(ctx, "alice", brand(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTemplate(ctx, "alice", tmpl(), ""); err != nil {
		t.Fatal(err)
	}
	const id = "inline-0123456789abcdef"
	insertInlineRow(t, s, id)

	items, _, err := s.ListTemplates(ctx, ListFilter{IncludeArchived: true})
	if err != nil || len(items) != 1 || items[0].ID != "proposal" {
		t.Fatalf("list must hide inline rows: %d items %v", len(items), err)
	}
	if items, _, _ := s.ListTemplates(ctx, ListFilter{Query: "inline"}); len(items) != 0 {
		t.Fatalf("a query must not find inline rows: %d", len(items))
	}
	if it, err := s.GetTemplate(ctx, id, 0); err != nil || it.LatestVersion != 1 || it.Archived {
		t.Fatalf("get inline: %+v %v", it, err)
	}
	if tp, v, err := s.ResolveTemplate(ctx, id, 1, false); err != nil || v != 1 || tp.ID != id {
		t.Fatalf("resolve inline: %v", err)
	}
	if _, _, err := s.ResolveTemplate(ctx, id, 2, false); errCode(err) != model.ErrNotFound {
		t.Fatalf("inline version 2: %v", err)
	}
	up := tmpl()
	up.ID = id
	if _, err := s.UpdateTemplate(ctx, "alice", id, 1, up, ""); errCode(err) != model.ErrNotFound {
		t.Fatalf("update inline: %v", err)
	}
	if err := s.ArchiveTemplate(ctx, "alice", id, true); errCode(err) != model.ErrNotFound {
		t.Fatalf("archive inline: %v", err)
	}
	if _, err := s.PreviewTemplate(ctx, id, 0); errCode(err) != model.ErrNotFound {
		t.Fatalf("preview inline: %v", err)
	}
	for _, rid := range []string{"inline", "inline-mine", id} {
		tp := tmpl()
		tp.ID = rid
		_, err := s.CreateTemplate(ctx, "alice", tp, "")
		if errCode(err) != model.ErrInvalidInput || !strings.Contains(err.Error(), "reserved for inline templates") {
			t.Fatalf("create %s: %v", rid, err)
		}
	}
	reservedUpdate := tmpl()
	reservedUpdate.ID = "inline"
	if _, err := s.UpdateTemplate(ctx, "alice", "inline", 1, reservedUpdate, ""); errCode(err) != model.ErrInvalidInput {
		t.Fatalf("update reserved id: %v", err)
	}
	var n int
	_ = s.DB.R.QueryRow(`SELECT count(*) FROM files WHERE template_id = ?`, id).Scan(&n)
	if n != 0 {
		t.Fatalf("inline template got %d preview files", n)
	}
}

// Acceptance 8 (stored side): layout.typst is bounded for every template.
func TestLayoutTypstLimit(t *testing.T) {
	s := newService(t)
	s.MaxLayoutTypstBytes = 64
	ctx := context.Background()
	if _, err := s.CreateBrand(ctx, "alice", brand(), ""); err != nil {
		t.Fatal(err)
	}
	tp := tmpl()
	big := "#let folio-layout(meta: (:), brand: (:), labels: (:), options: (:), body) = body\n"
	tp.Layout.Typst = &big
	if _, err := s.CreateTemplate(ctx, "alice", tp, ""); errCode(err) != model.ErrTooLarge || !strings.Contains(err.Error(), "max_layout_typst_bytes") {
		t.Fatalf("create with a large layout.typst: %v", err)
	}
	if _, _, err := s.DryRunTemplate(ctx, tp, nil); errCode(err) != model.ErrTooLarge {
		t.Fatalf("dry run with a large layout.typst: %v", err)
	}
	var n int
	_ = s.DB.R.QueryRow(`SELECT count(*) FROM templates`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d template rows written", n)
	}
}

// Acceptance 7 (catalog side): the default brand resolution order.
func TestDefaultBrand(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	if _, err := s.DefaultBrand(ctx); errCode(err) != model.ErrInvalidInput || !strings.Contains(err.Error(), "create_brand") {
		t.Fatalf("no brand: %v", err)
	}
	acme := brand()
	acme.ID = "acme"
	if _, err := s.CreateBrand(ctx, "alice", acme, ""); err != nil {
		t.Fatal(err)
	}
	if id, err := s.DefaultBrand(ctx); err != nil || id != "acme" { // (c) the only live brand
		t.Fatalf("one brand: %q %v", id, err)
	}
	if _, err := s.CreateBrand(ctx, "alice", brand(), ""); err != nil { // "example"
		t.Fatal(err)
	}
	if _, err := s.DefaultBrand(ctx); errCode(err) != model.ErrInvalidInput || !strings.Contains(err.Error(), "available brands: acme, example") {
		t.Fatalf("two brands (d): %v", err)
	}
	if err := s.ArchiveBrand(ctx, "alice", "example", true); err != nil {
		t.Fatal(err)
	}
	if id, err := s.DefaultBrand(ctx); err != nil || id != "acme" { // (e) archived brands do not count
		t.Fatalf("one live, one archived: %q %v", id, err)
	}
	s.DefaultBrandID = "acme"
	if id, err := s.DefaultBrand(ctx); err != nil || id != "acme" { // (a)
		t.Fatalf("configured: %q %v", id, err)
	}
	for _, ghost := range []string{"ghost", "example"} { // (b) missing or archived
		s.DefaultBrandID = ghost
		if _, err := s.DefaultBrand(ctx); errCode(err) != model.ErrInvalidInput || !strings.Contains(err.Error(), `"`+ghost+`"`) {
			t.Fatalf("configured %s: %v", ghost, err)
		}
	}
}
