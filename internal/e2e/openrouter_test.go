// SPDX-License-Identifier: AGPL-3.0-only

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vocdoni/folio/internal/mcpserver"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/review"
)

const e2eOpenRouterKey = "sk-or-e2e-xxxxxxxxxxxxxxxx"

// openRouterStub is a fake OpenRouter chat completions endpoint.
type openRouterStub struct {
	mu        sync.Mutex
	mode      string // ok | fail | hang
	calls     int
	images    int
	denyEvery bool // every request asked providers to deny data collection
}

func (s *openRouterStub) set(mode string) { s.mu.Lock(); s.mode = mode; s.mu.Unlock() }

func (s *openRouterStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider struct {
			DataCollection string `json:"data_collection"`
		} `json:"provider"`
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	b, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(b, &req)
	s.mu.Lock()
	s.calls++
	if s.calls == 1 {
		s.denyEvery = true
	}
	s.denyEvery = s.denyEvery && req.Provider.DataCollection == "deny" && r.Header.Get("Authorization") == "Bearer "+e2eOpenRouterKey
	if len(req.Messages) == 2 {
		s.images = strings.Count(string(req.Messages[1].Content), `"type":"image_url"`)
	}
	mode := s.mode
	s.mu.Unlock()
	switch mode {
	case "fail":
		http.Error(w, `{"error":{"message":"upstream exploded"}}`, http.StatusInternalServerError)
		return
	case "hang":
		select {
		case <-time.After(10 * time.Second):
		case <-r.Context().Done():
		}
		return
	}
	content := `{"findings":[` +
		`{"page":1,"severity":"warning","category":"typography","message":"STUB-FINDING: the cover title wraps awkwardly."},` +
		`{"page":1,"severity":"severe","category":"weird","message":"Unknown severity and category are normalised."},` +
		`{"page":99,"severity":"problem","category":"blank","message":"A page that was not reviewed is dropped."}]}`
	out, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": content}}}})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

// TestOpenRouterReviewer covers AC14 with the real OpenRouter client against
// an HTTP stub: review_document stores advisory findings, template creation
// returns advisory findings, reviewer failures and timeouts return
// unavailable and never fail a render or a template, and neither the API key
// nor page images reach the logs.
func TestOpenRouterReviewer(t *testing.T) {
	stub := &openRouterStub{mode: "ok"}
	srv := httptest.NewServer(stub)
	defer srv.Close()
	rv, err := review.NewOpenRouter(review.OpenRouterConfig{APIKey: e2eOpenRouterKey, BaseURL: srv.URL + "/api/v1", MaxPages: 20, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	h := start(t, map[string]string{"FOLIO_SEED": "false"}, rv)

	var caps mcpserver.CapabilitiesOut
	h.ok("get_capabilities", nil, &caps)
	if !caps.ReviewerEnabled {
		t.Fatal("reviewer_enabled must be true")
	}
	if _, _, body := get(t, h.url+"/healthz"); !strings.Contains(string(body), `"reviewer":true`) {
		t.Fatalf("/healthz: %s", body)
	}

	// Template creation reviews the sample and returns advisory findings.
	h.ok("create_brand", map[string]any{"brand": brandSpec("e2e", "")}, nil)
	var tr mcpserver.TemplateResultOut
	h.ok("create_template", map[string]any{"template": memoTemplate("memo", "e2e", nil)}, &tr)
	if tr.Review == nil || !tr.Review.Advisory || tr.Review.Status != "ok" || len(tr.Review.Findings) != 2 ||
		tr.Review.Findings[1].Severity != "info" || tr.Review.Findings[1].Category != "other" {
		t.Fatalf("template review: %+v", tr.Review)
	}

	// review_document stores advisory findings on the revision.
	var r mcpserver.RenderOut
	h.ok("render_document", renderArgs("memo", "Reviewed quote", nil), &r)
	var out mcpserver.ReviewOut
	h.ok("review_document", map[string]any{"id": r.DocumentID}, &out)
	stub.mu.Lock()
	images := stub.images
	stub.mu.Unlock()
	if !out.Review.Advisory || len(out.Review.Findings) != 2 || out.Review.Findings[0].Page != 1 || images != len(out.Review.Pages) {
		t.Fatalf("review: %+v (stub saw %d images)", out.Review, images)
	}
	var doc mcpserver.DocumentOut
	h.ok("get_document", map[string]any{"id": r.DocumentID, "include_previews": "none"}, &doc)
	if !strings.Contains(string(doc.Revision.Review), `"advisory":true`) || !strings.Contains(string(doc.Revision.Review), "STUB-FINDING") ||
		doc.Status != "draft" {
		t.Fatalf("stored review: %s (status %s)", doc.Revision.Review, doc.Status)
	}

	// Failures and timeouts: unavailable, renders and templates unaffected,
	// previous findings kept.
	for _, mode := range []string{"fail", "hang"} {
		stub.set(mode)
		began := time.Now()
		if te := h.fail("review_document", map[string]any{"id": r.DocumentID}); te.Code != model.ErrUnavailable || !te.Retryable {
			t.Fatalf("%s: review_document: %+v", mode, te)
		}
		if time.Since(began) > 8*time.Second {
			t.Fatalf("%s: the reviewer timeout was not applied", mode)
		}
		var r2 mcpserver.RenderOut
		h.ok("render_document", renderArgs("memo", "Still renders "+mode, nil), &r2)
	}
	var tr2 mcpserver.TemplateResultOut
	h.ok("update_template", map[string]any{"id": "memo", "expected_version": 1, "template": memoTemplate("memo", "e2e", nil)}, &tr2)
	if tr2.Version != 2 || tr2.Review == nil || tr2.Review.Status != "unavailable" || len(tr2.Review.Findings) != 0 {
		t.Fatalf("a failing reviewer must not block update_template: %+v", tr2)
	}
	h.ok("get_document", map[string]any{"id": r.DocumentID, "include_previews": "none"}, &doc)
	if !strings.Contains(string(doc.Revision.Review), "STUB-FINDING") {
		t.Fatal("a failed review must keep the previous findings")
	}

	stub.mu.Lock()
	deny := stub.denyEvery
	stub.mu.Unlock()
	if !deny {
		t.Fatal("every OpenRouter request must carry the key and provider.data_collection=deny")
	}
	h.mu.Lock()
	logs := h.logs.String()
	h.mu.Unlock()
	for _, leak := range []string{e2eOpenRouterKey, "data:image", "STUB-FINDING"} {
		if strings.Contains(logs, leak) {
			t.Fatalf("logs contain %q", leak)
		}
	}
	if !strings.Contains(logs, "ai review failed") {
		t.Fatal("reviewer failures must be logged")
	}
}

// TestReviewerFromConfiguration: FOLIO_OPENROUTER_API_KEY alone enables the
// reviewer (no request is sent to OpenRouter here).
func TestReviewerFromConfiguration(t *testing.T) {
	h := start(t, map[string]string{"FOLIO_SEED": "false", "FOLIO_OPENROUTER_API_KEY": e2eOpenRouterKey}, nil)
	var caps mcpserver.CapabilitiesOut
	h.ok("get_capabilities", nil, &caps)
	if _, _, body := get(t, h.url+"/healthz"); !caps.ReviewerEnabled || !strings.Contains(string(body), `"reviewer":true`) {
		t.Fatalf("reviewer_enabled=%v healthz=%s", caps.ReviewerEnabled, body)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if strings.Contains(h.logs.String(), e2eOpenRouterKey) {
		t.Fatal("the API key reached the logs")
	}
}

// TestReviewerHonoursBaseURL: FOLIO_OPENROUTER_BASE_URL points the
// configured reviewer at a stub (the Playwright job relies on it).
func TestReviewerHonoursBaseURL(t *testing.T) {
	stub := &openRouterStub{mode: "ok"}
	srv := httptest.NewServer(stub)
	defer srv.Close()
	h := start(t, map[string]string{"FOLIO_SEED": "false", "FOLIO_OPENROUTER_API_KEY": e2eOpenRouterKey,
		"FOLIO_OPENROUTER_BASE_URL": srv.URL + "/api/v1"}, nil)
	h.ok("create_brand", map[string]any{"brand": brandSpec("e2e", "")}, nil)
	var tr mcpserver.TemplateResultOut
	h.ok("create_template", map[string]any{"template": memoTemplate("memo", "e2e", nil)}, &tr)
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if stub.calls != 1 || !stub.denyEvery || tr.Review == nil || tr.Review.Status != "ok" {
		t.Fatalf("stub calls %d deny=%v review %+v", stub.calls, stub.denyEvery, tr.Review)
	}
}
