// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/review"
)

// docEnv is newEnv plus the "acme" brand and the "memo" template.
func docEnv(t *testing.T, rv review.Reviewer) (*env, *mcp.ClientSession) {
	t.Helper()
	e := newEnv(t)
	e.d.Reviewer = rv
	cs := connect(t, New(e.d), nil)
	callOK(t, cs, "create_brand", map[string]any{"brand": testBrand("acme", "")}, nil)
	callOK(t, cs, "create_template", map[string]any{"template": testTemplate("memo", "acme")}, nil)
	return e, cs
}

func renderArgs(extra map[string]any) map[string]any {
	a := map[string]any{"template_id": "memo", "markdown": "# Summary\n\nTODO later.",
		"metadata": map[string]any{"title": "Quarterly memo", "date": "2026-10-18", "language": "en", "client": "ACME"}}
	for k, v := range extra {
		a[k] = v
	}
	return a
}

func countContent(res *mcp.CallToolResult) (links, images int) {
	for _, c := range res.Content {
		switch c.(type) {
		case *mcp.ResourceLink:
			links++
		case *mcp.ImageContent:
			images++
		}
	}
	return links, images
}

func TestValidateDocument(t *testing.T) {
	_, cs := docEnv(t, nil)
	var v ValidateOut
	callOK(t, cs, "validate_document", renderArgs(nil), &v)
	if !v.Valid || len(v.Findings) != 1 || v.Findings[0].Code != model.CodePlaceholder || v.Template.Version != 1 {
		t.Fatalf("valid: %+v", v)
	}
	callOK(t, cs, "validate_document", renderArgs(map[string]any{"markdown": "FAIL"}), &v)
	if v.Valid || len(v.Findings) != 1 || v.Findings[0].Code != model.CodeMissingSection || v.Findings[0].Hint == "" {
		t.Fatalf("invalid: %+v", v)
	}
	if te := callErr(t, cs, "validate_document", renderArgs(map[string]any{"template_id": "nope"})); te.Code != model.ErrNotFound {
		t.Fatalf("unknown template: %+v", te)
	}
}

func TestDocumentLifecycleTools(t *testing.T) {
	e, cs := docEnv(t, nil)
	var r RenderOut
	res := callOK(t, cs, "render_document", renderArgs(map[string]any{"idempotency_key": "memo-1"}), &r)
	if r.Revision != 1 || r.Unchanged || r.Status != "draft" || len(r.Files) != 2 || len(r.Previews) != 1 || len(r.Warnings) != 1 {
		t.Fatalf("render: %+v", r)
	}
	if links, images := countContent(res); links != 2 || images != 1 {
		t.Fatalf("content: %d links, %d images", links, images)
	}
	var again RenderOut
	callOK(t, cs, "render_document", renderArgs(map[string]any{"idempotency_key": "memo-1", "markdown": "# Summary\n\nDifferent."}), &again)
	if again.DocumentID != r.DocumentID || !again.Unchanged || again.Revision != 1 {
		t.Fatalf("idempotent retry: %+v", again)
	}
	if te := callErr(t, cs, "render_document", renderArgs(map[string]any{"template_id": "", "markdown": "x"})); te.Code != model.ErrInvalidInput {
		t.Fatalf("no template: %+v", te)
	}
	if te := callErr(t, cs, "render_document", renderArgs(map[string]any{"formats": []string{"html"}})); te.Code != model.ErrInvalidInput {
		t.Fatalf("bad format: %+v", te)
	}
	rev := map[string]any{"document_id": r.DocumentID, "expected_revision": 1, "markdown": "# Summary\n\nRevised.", "include_previews": "none"}
	res = callOK(t, cs, "render_document", renderArgs(rev), &r)
	if r.Revision != 2 {
		t.Fatalf("revision: %+v", r)
	}
	if _, images := countContent(res); images != 0 {
		t.Fatal("include_previews=none returned images")
	}
	if te := callErr(t, cs, "render_document", renderArgs(rev)); te.Code != model.ErrConflict || !strings.Contains(te.Message, "expected_revision=2") {
		t.Fatalf("stale revision: %+v", te)
	}

	var doc DocumentOut
	res = callOK(t, cs, "get_document", map[string]any{"id": r.DocumentID, "include_source": true, "pages": []int{2}}, &doc)
	if doc.Revision.Revision != 2 || len(doc.Revisions) != 2 || doc.Revision.Pages != 2 || !strings.Contains(string(doc.Source), "Revised.") {
		t.Fatalf("get_document: %+v", doc)
	}
	if _, images := countContent(res); images != 1 || e.raster.calls.Load() != 1 {
		t.Fatalf("page 2 image: %d images, %d rasterisations", images, e.raster.calls.Load())
	}
	if te := callErr(t, cs, "get_document", map[string]any{"id": r.DocumentID, "pages": []int{3}}); te.Code != model.ErrInvalidInput {
		t.Fatalf("page out of range: %+v", te)
	}
	if te := callErr(t, cs, "get_document", map[string]any{"id": r.DocumentID, "pages": []int{1, 2, 1, 2, 1, 2, 1}}); te.Code != model.ErrInvalidInput {
		t.Fatalf("too many pages: %+v", te)
	}
	if te := callErr(t, cs, "get_document", map[string]any{"id": r.DocumentID, "revision": 1e20}); te.Code != model.ErrInvalidInput || strings.Contains(te.Message, "Go struct") || strings.Contains(te.Message, "GetDocumentIn") {
		t.Fatalf("decode error leaks Go types: %+v", te)
	}
	var doc1 DocumentOut
	callOK(t, cs, "get_document", map[string]any{"id": r.DocumentID, "revision": 1}, &doc1)
	if doc1.Revision.Revision != 1 || doc1.Revision.Parent != 0 || doc1.Source != nil {
		t.Fatalf("revision 1: %+v", doc1.Revision)
	}

	var st StatusOut
	callOK(t, cs, "publish_document", map[string]any{"id": r.DocumentID, "revision": 1}, &st)
	if st.Status != "published" || st.PublishedRevision != 1 {
		t.Fatalf("publish: %+v", st)
	}
	if te := callErr(t, cs, "delete_document", map[string]any{"id": r.DocumentID}); te.Code != model.ErrInvalidInput || !strings.Contains(te.Message, "confirm_published") {
		t.Fatalf("delete published: %+v", te)
	}
	var del DeleteDocumentOut
	callOK(t, cs, "delete_document", map[string]any{"id": r.DocumentID, "confirm_published": true}, &del)
	if !del.Trashed || del.Purged {
		t.Fatalf("trash: %+v", del)
	}
	var list ListDocumentsOut
	callOK(t, cs, "list_documents", map[string]any{}, &list)
	if len(list.Documents) != 0 {
		t.Fatalf("trashed document listed: %+v", list)
	}
	callOK(t, cs, "list_documents", map[string]any{"status": "trashed"}, &list)
	if len(list.Documents) != 1 || list.Documents[0].Client != "ACME" {
		t.Fatalf("trashed filter: %+v", list)
	}
	callOK(t, cs, "restore_document", map[string]any{"id": r.DocumentID}, &st)
	if st.Status != "published" {
		t.Fatalf("restore: %+v", st)
	}
	callOK(t, cs, "delete_document", map[string]any{"id": r.DocumentID, "purge": true, "confirm_published": true}, &del)
	if !del.Purged {
		t.Fatalf("purge: %+v", del)
	}
	if te := callErr(t, cs, "get_document", map[string]any{"id": r.DocumentID}); te.Code != model.ErrNotFound {
		t.Fatalf("purged document: %+v", te)
	}
	var n int
	_ = e.d.DB.R.QueryRow(`SELECT count(*) FROM audit WHERE action = 'render_document' AND outcome = 'conflict'`).Scan(&n)
	if n != 1 {
		t.Fatalf("failed render audit rows = %d", n)
	}
}

type stubReviewer struct {
	mu    sync.Mutex
	pages []int
	err   error
}

func (s *stubReviewer) Review(_ context.Context, pages []review.Page, info review.Info) ([]review.Finding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	s.pages = nil
	for _, p := range pages {
		if len(p.PNG) == 0 || info.Title == "" {
			return nil, errors.New("missing page data")
		}
		s.pages = append(s.pages, p.Number)
	}
	return []review.Finding{
		{Page: 1, Severity: "warning", Category: "overflow", Message: "table touches the right margin"},
		{Page: 9, Severity: "problem", Category: "blank", Message: "not a reviewed page"},
	}, nil
}

func TestReviewDocument(t *testing.T) {
	_, cs := docEnv(t, nil)
	var r RenderOut
	callOK(t, cs, "render_document", renderArgs(nil), &r)
	if te := callErr(t, cs, "review_document", map[string]any{"id": r.DocumentID}); te.Code != model.ErrUnavailable || te.Retryable || strings.Contains(te.Message, "retry later") {
		t.Fatalf("no reviewer: %+v", te)
	}

	stub := &stubReviewer{}
	_, cs = docEnv(t, stub)
	callOK(t, cs, "render_document", renderArgs(nil), &r)
	var out ReviewOut
	callOK(t, cs, "review_document", map[string]any{"id": r.DocumentID}, &out)
	if !out.Review.Advisory || !slices.Equal(out.Review.Pages, []int{1, 2}) || len(out.Review.Findings) != 1 || out.Review.ReviewedBy != LocalActor {
		t.Fatalf("review: %+v", out)
	}
	if !slices.Equal(stub.pages, []int{1, 2}) {
		t.Fatalf("reviewer saw pages %v", stub.pages)
	}
	callOK(t, cs, "review_document", map[string]any{"id": r.DocumentID, "pages": []int{2}}, &out)
	var doc DocumentOut
	callOK(t, cs, "get_document", map[string]any{"id": r.DocumentID, "include_previews": "none"}, &doc)
	var stored review.Report
	if err := json.Unmarshal(doc.Revision.Review, &stored); err != nil || !stored.Advisory || !slices.Equal(stored.Pages, []int{2}) || len(stored.Findings) != 0 {
		t.Fatalf("re-review must replace the stored findings: %s %v", doc.Revision.Review, err)
	}
	if !doc.Revisions[0].Reviewed {
		t.Fatal("revision summary must show reviewed")
	}
	stub.err = errors.New("upstream 502")
	if te := callErr(t, cs, "review_document", map[string]any{"id": r.DocumentID}); te.Code != model.ErrUnavailable || strings.Contains(te.Message, "502") {
		t.Fatalf("reviewer failure: %+v", te)
	}
	// A failing reviewer never affects rendering.
	var r2 RenderOut
	callOK(t, cs, "render_document", renderArgs(map[string]any{"markdown": "# Summary\n\nNew doc."}), &r2)
	if te := callErr(t, cs, "review_document", map[string]any{"id": r.DocumentID, "pages": []int{5}}); te.Code != model.ErrInvalidInput {
		t.Fatalf("missing page: %+v", te)
	}
}

func TestRenderSendsHeartbeats(t *testing.T) {
	e := newEnv(t)
	e.r.delay = 150 * time.Millisecond
	var mu sync.Mutex
	var msgs []string
	cs := connect(t, New(e.d), &mcp.ClientOptions{ProgressNotificationHandler: func(_ context.Context, r *mcp.ProgressNotificationClientRequest) {
		mu.Lock()
		msgs = append(msgs, r.Params.Message)
		mu.Unlock()
	}})
	callOK(t, cs, "create_brand", map[string]any{"brand": testBrand("acme", "")}, nil)
	params := &mcp.CallToolParams{Name: "create_template", Arguments: map[string]any{"template": testTemplate("memo", "acme")}}
	params.SetProgressToken(7)
	if res, err := cs.CallTool(context.Background(), params); err != nil || res.IsError {
		t.Fatalf("create_template: %v %+v", err, res)
	}
	ok := eventually(func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(msgs) >= 2 && strings.Contains(strings.Join(msgs, "|"), "(still working)")
	})
	if !ok {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("heartbeats: %q", msgs)
	}
}

func TestToolSurface(t *testing.T) {
	e := newEnv(t)
	cs := connect(t, New(e.d), nil)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	readOnlyTools := []string{"get_guide", "get_capabilities", "list_brands", "get_brand", "list_templates", "get_template",
		"list_assets", "validate_document", "list_documents", "get_document"}
	destructiveTools := []string{"delete_asset", "delete_document"}
	idempotentWrites := []string{"archive_brand", "archive_template", "preview_template", "review_document", "publish_document", "update_asset"}
	writes := []string{"create_brand", "update_brand", "create_template", "update_template", "upload_asset", "render_document", "restore_document"}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
		a := tl.Annotations
		if a == nil || a.DestructiveHint == nil || a.OpenWorldHint == nil || a.Title == "" {
			t.Errorf("%s: annotations must be explicit: %+v", tl.Name, a)
			continue
		}
		switch {
		case slices.Contains(readOnlyTools, tl.Name):
			if !a.ReadOnlyHint || *a.DestructiveHint {
				t.Errorf("%s must be read-only", tl.Name)
			}
		case slices.Contains(destructiveTools, tl.Name):
			if a.ReadOnlyHint || !*a.DestructiveHint {
				t.Errorf("%s must be destructive", tl.Name)
			}
		case slices.Contains(idempotentWrites, tl.Name):
			if a.ReadOnlyHint || *a.DestructiveHint || !a.IdempotentHint {
				t.Errorf("%s must be an idempotent write", tl.Name)
			}
		case slices.Contains(writes, tl.Name):
			if a.ReadOnlyHint || *a.DestructiveHint || a.IdempotentHint {
				t.Errorf("%s must be a non-idempotent write", tl.Name)
			}
		}
		if len(tl.Description) < 120 {
			t.Errorf("%s: the description must teach when and how to use the tool (%d chars)", tl.Name, len(tl.Description))
		}
		out, _ := json.Marshal(tl.OutputSchema)
		if !strings.Contains(string(out), `"error"`) {
			t.Errorf("%s: output schema lacks error", tl.Name)
		}
		if strings.Contains(string(out), `"computed"`) {
			t.Errorf("%s: output schema still has computed: Folio derives no values from the input", tl.Name)
		}
	}
	all := slices.Concat(readOnlyTools, destructiveTools, idempotentWrites, writes)
	slices.Sort(all)
	slices.Sort(names)
	if !slices.Equal(all, names) {
		t.Fatalf("tools = %v\nwant    %v", names, all)
	}
}

func TestGetDocumentPagesOnDocxOnlyRevision(t *testing.T) {
	_, cs := docEnv(t, nil)
	var r RenderOut
	callOK(t, cs, "render_document", renderArgs(map[string]any{"formats": []string{"docx"}}), &r)
	te := callErr(t, cs, "get_document", map[string]any{"id": r.DocumentID, "pages": []int{2}})
	if te.Code != model.ErrInvalidInput || !strings.Contains(te.Message, "formats=[docx]") {
		t.Fatalf("missing page on a DOCX-only revision: %+v", te)
	}
}
