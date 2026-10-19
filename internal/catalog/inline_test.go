// SPDX-License-Identifier: AGPL-3.0-only

package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/vocdoni/folio/internal/components"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/store"
)

var inlineIDRE = regexp.MustCompile(`^inline-[0-9a-f]{16}$`)

// inlineService is newService with the "example" brand.
func inlineService(t *testing.T) *Service {
	t.Helper()
	s := newService(t)
	if _, err := s.CreateBrand(context.Background(), "alice", brand(), ""); err != nil {
		t.Fatal(err)
	}
	return s
}

func spec(t *testing.T, raw string) model.Template {
	t.Helper()
	var tp model.Template
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&tp); err != nil {
		t.Fatal(err)
	}
	return tp
}

var meta = json.RawMessage(`{"title":"T","date":"2026-10-19","language":"en"}`)

func countTemplates(t *testing.T, s *Service) int {
	t.Helper()
	var n int
	if err := s.DB.R.QueryRow(`SELECT count(*) FROM templates`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Acceptance 1 (catalog side): template: {} is the default brand, the report
// layout, every component, English plus the document language.
func TestInlineTemplateDefaults(t *testing.T) {
	s := inlineService(t)
	tp, canon, err := s.InlineTemplate(context.Background(), spec(t, `{}`), "", json.RawMessage(`{"language":"es"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !inlineIDRE.MatchString(tp.ID) || tp.Name != InlineName || tp.BrandID != "example" || tp.Layout.Base != "report" ||
		!slices.Equal(tp.Languages, []string{"en", "es"}) || !slices.Equal(tp.Components, components.Names) || tp.SpecVersion != model.SpecVersion {
		t.Fatalf("normalised: %+v", tp)
	}
	if string(tp.Layout.Options) != `{"cover":true,"toc":false,"page_size":"a4","heading_numbering":false}` {
		t.Fatalf("options: %s", tp.Layout.Options)
	}
	if !bytes.Contains(canon, []byte(`"id":"`+tp.ID+`"`)) || !bytes.Contains(canon, []byte(`"sample":{"markdown":"","metadata":{}}`)) {
		t.Fatalf("canonical spec: %s", canon)
	}
	if n := countTemplates(t, s); n != 0 {
		t.Fatalf("InlineTemplate wrote %d rows", n)
	}
	letter, _, err := s.InlineTemplate(context.Background(), spec(t, `{"layout":{"base":"letter","options":{"toc":true}}}`), "", meta)
	if err != nil {
		t.Fatal(err)
	}
	if string(letter.Layout.Options) != `{"cover":false,"toc":true,"page_size":"a4","heading_numbering":false}` || !slices.Equal(letter.Languages, []string{"en"}) {
		t.Fatalf("letter: %s %v", letter.Layout.Options, letter.Languages)
	}
}

// Acceptance 2 (catalog side): the id is a content hash of the normalised
// spec; ignored fields and empty forms do not change it.
func TestInlineTemplateID(t *testing.T) {
	s := inlineService(t)
	acme := brand()
	acme.ID = "acme"
	if _, err := s.CreateBrand(context.Background(), "alice", acme, ""); err != nil {
		t.Fatal(err)
	}
	id := func(raw, callBrand string, metadata json.RawMessage) string {
		t.Helper()
		tp, _, err := s.InlineTemplate(context.Background(), spec(t, raw), callBrand, metadata)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		return tp.ID
	}
	base := id(`{}`, "example", meta)
	same := []string{
		`{"id":"x","name":"Mine","description":"d","category":"c","body_skeleton":"# A","sample":{"markdown":"m","metadata":{}}}`,
		`{"components":null,"sections":[],"metadata_schema":null,"labels":{}}`,
		`{"layout":{"base":"report","typst":null,"options":{"cover":true}}}`,
		`{"brand_id":"example"}`,
	}
	for _, raw := range same {
		if got := id(raw, "example", meta); got != base {
			t.Errorf("%s: %s, want %s", raw, got, base)
		}
	}
	for name, got := range map[string]string{
		"call brand":    id(`{}`, "acme", meta),
		"language":      id(`{}`, "example", json.RawMessage(`{"language":"ca"}`)),
		"no components": id(`{"components":[]}`, "example", meta),
		"letter":        id(`{"layout":{"base":"letter"}}`, "example", meta),
	} {
		if got == base {
			t.Errorf("%s must change the id", name)
		}
	}
	// Explicit languages make the id independent of the document language.
	if id(`{"languages":["en","es"]}`, "example", json.RawMessage(`{"language":"es"}`)) != id(`{"languages":["en","es"]}`, "example", meta) {
		t.Error("explicit languages must not depend on metadata.language")
	}
}

// The stored spec round-trips through ResolveTemplate's decoding to the same
// canonical bytes, so the render transaction can re-insert it and its
// collision guard compares equal specs as equal.
func TestInlineSpecRoundTrip(t *testing.T) {
	s := inlineService(t)
	raw := `{"languages":["en","ca"],
	 "layout":{"base":"proposal","typst":"#import \"/base/proposal.typ\": folio-layout // <&> ünïcode","options":{"page_size":"letter"}},
	 "metadata_schema":{"type":"object","properties":{"rate":{"type":"number","maximum":1.50,"minimum":1e-2}}},
	 "data_schema":{"type":"object","properties":{"budget":{"$ref":"folio:component/budget"}}},
	 "sections":[{"title":"Résumé","required":true,"aliases":["Summary"]},{"title":"Budget","component":"budget"}],
	 "components":["budget"],"strict":true,"labels":{"en":{"page":"Pg."}}}`
	tp, canon, err := s.InlineTemplate(context.Background(), spec(t, raw), "", meta)
	if err != nil {
		t.Fatal(err)
	}
	var back model.Template
	if err := json.NewDecoder(bytes.NewReader(canon)).Decode(&back); err != nil {
		t.Fatal(err)
	}
	again, err := model.Canonical(back)
	if err != nil || !bytes.Equal(again, canon) {
		t.Fatalf("round trip changed the spec:\n%s\n%s", canon, again)
	}
	if id, _ := inlineID(back); id != tp.ID {
		t.Fatalf("round trip changed the id: %s vs %s", id, tp.ID)
	}
}

// Acceptance 3 (catalog side): invalid specs fail with findings under
// /template and write nothing.
func TestInlineTemplateValidation(t *testing.T) {
	s := inlineService(t)
	for raw, path := range map[string]string{
		`{"layout":{"base":"memo"}}`:                                        "/template/layout/base",
		`{"layout":{"typst":"#set page(width: 10cm)"}}`:                     "/template/layout/typst",
		`{"layout":{"options":{"cover":"yes"}}}`:                            "/template/layout/options",
		`{"languages":[]}`:                                                  "/template/languages",
		`{"components":[],"sections":[{"title":"B","component":"budget"}]}`: "/template/sections/0/component",
		`{"components":["gantt"]}`:                                          "/template/components/0",
		`{"metadata_schema":{"type":"nope"}}`:                               "/template/metadata_schema",
	} {
		_, _, err := s.InlineTemplate(context.Background(), spec(t, raw), "", meta)
		var me *model.Error
		if !errors.As(err, &me) || me.Code != model.ErrValidationFailed || len(me.Details) == 0 {
			t.Errorf("%s: %v", raw, err)
			continue
		}
		found := false
		for _, f := range me.Details {
			found = found || strings.HasPrefix(f.Path, path)
		}
		if !found {
			t.Errorf("%s: findings %+v lack %s", raw, me.Details, path)
		}
	}
	if n := countTemplates(t, s); n != 0 {
		t.Fatalf("invalid specs wrote %d rows", n)
	}
}

// The brand rules: the spec's, the call's, then the default; missing and
// archived brands fail as for stored templates.
func TestInlineTemplateBrand(t *testing.T) {
	s := inlineService(t)
	ctx := context.Background()
	if _, _, err := s.InlineTemplate(ctx, spec(t, `{"brand_id":"ghost"}`), "", meta); errCode(err) != model.ErrNotFound {
		t.Fatalf("missing brand: %v", err)
	}
	acme := brand()
	acme.ID = "acme"
	if _, err := s.CreateBrand(ctx, "alice", acme, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.InlineTemplate(ctx, spec(t, `{}`), "", meta); errCode(err) != model.ErrInvalidInput || !strings.Contains(err.Error(), "acme, example") {
		t.Fatalf("two brands, none named: %v", err)
	}
	if tp, _, err := s.InlineTemplate(ctx, spec(t, `{"brand_id":"acme"}`), "example", meta); err != nil || tp.BrandID != "acme" {
		t.Fatalf("the spec's brand wins over the call's: %+v %v", tp.BrandID, err)
	}
	if err := s.ArchiveBrand(ctx, "alice", "acme", true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.InlineTemplate(ctx, spec(t, `{}`), "acme", meta); errCode(err) != model.ErrArchived {
		t.Fatalf("archived brand: %v", err)
	}
}

// Acceptance 8 (catalog side): both size limits apply before anything else.
func TestInlineTemplateLimits(t *testing.T) {
	s := inlineService(t)
	s.MaxLayoutTypstBytes, s.MaxInlineBytes = 64, 2048
	big := `{"layout":{"typst":"` + strings.Repeat("x", 65) + `"}}`
	if _, _, err := s.InlineTemplate(context.Background(), spec(t, big), "", meta); errCode(err) != model.ErrTooLarge || !strings.Contains(err.Error(), "/template/layout/typst") {
		t.Fatalf("large layout.typst: %v", err)
	}
	huge := `{"sections":[{"title":"` + strings.Repeat("y", 2048) + `"}]}`
	if _, _, err := s.InlineTemplate(context.Background(), spec(t, huge), "", meta); errCode(err) != model.ErrTooLarge || !strings.Contains(err.Error(), "max_inline_template_bytes") {
		t.Fatalf("large spec: %v", err)
	}
	s.DefaultBrandID = "ghost" // the limit is checked before the brand
	if _, _, err := s.InlineTemplate(context.Background(), spec(t, huge), "", meta); errCode(err) != model.ErrTooLarge {
		t.Fatalf("limit before brand: %v", err)
	}
}

// storeInline inserts the inline row the way documents.Render does.
func storeInline(t *testing.T, s *Service, canon []byte, id string) {
	t.Helper()
	if _, err := s.DB.W.Exec(`INSERT INTO templates (id, version, spec, note, created_by, created_at) VALUES (?, 1, ?, 'inline', 'alice', ?)`,
		id, string(canon), store.Now(s.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.W.Exec(`INSERT INTO template_heads (id, latest_version) VALUES (?, 1)`, id); err != nil {
		t.Fatal(err)
	}
}

// save_inline_template's catalog half: a stored copy of the inline spec,
// created with every sample warning accepted.
func TestSaveInline(t *testing.T) {
	s := inlineService(t)
	s.Renderer = &warnRenderer{} // an edge_ink warning on every render
	ctx := context.Background()
	tp, canon, err := s.InlineTemplate(ctx, spec(t, `{"layout":{"base":"letter"}}`), "", meta)
	if err != nil {
		t.Fatal(err)
	}
	storeInline(t, s, canon, tp.ID)
	sample := model.Sample{Markdown: "Dear reader,", Metadata: meta}
	in := SaveInlineInput{ID: "letter", Name: "Letter", Description: "A plain letter.", Category: "letter", Sample: sample, Note: "from inline"}
	stored := tp
	stored.ID, stored.Name, stored.Sample = "letter", "Letter", sample
	if _, err := s.CreateTemplate(ctx, "alice", stored, ""); errCode(err) != model.ErrValidationFailed {
		t.Fatalf("create_template keeps its strict sample policy: %v", err)
	}
	r, err := s.SaveInline(ctx, "alice", tp.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != "letter" || r.Version != 1 || len(r.Warnings) != 1 || len(r.Files) == 0 {
		t.Fatalf("save: %+v", r)
	}
	saved, _, err := s.ResolveTemplate(ctx, "letter", 0, false)
	if err != nil || saved.Name != "Letter" || saved.Category != "letter" || saved.Layout.Base != "letter" || saved.BrandID != "example" ||
		!slices.Equal(saved.Components, tp.Components) || saved.Sample.Markdown != "Dear reader," {
		t.Fatalf("stored copy: %+v %v", saved, err)
	}
	var audit int
	_ = s.DB.R.QueryRow(`SELECT count(*) FROM audit WHERE action = 'create_template' AND target_id = 'letter'`).Scan(&audit)
	if audit != 1 {
		t.Fatalf("audit rows = %d", audit)
	}
	for name, c := range map[string]struct {
		inline string
		in     SaveInlineInput
		want   model.ErrorCode
	}{
		"exists":       {tp.ID, in, model.ErrConflict},
		"reserved id":  {tp.ID, SaveInlineInput{ID: "inline-copy", Name: "X", Sample: sample}, model.ErrInvalidInput},
		"not inline":   {"letter", SaveInlineInput{ID: "copy", Name: "X", Sample: sample}, model.ErrInvalidInput},
		"unknown":      {"inline-ffffffffffffffff", SaveInlineInput{ID: "copy", Name: "X", Sample: sample}, model.ErrNotFound},
		"invalid name": {tp.ID, SaveInlineInput{ID: "copy", Name: " ", Sample: sample}, model.ErrValidationFailed},
	} {
		if _, err := s.SaveInline(ctx, "alice", c.inline, c.in); errCode(err) != c.want {
			t.Errorf("%s: %v, want %s", name, err, c.want)
		}
	}
}
