// SPDX-License-Identifier: AGPL-3.0-only

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"github.com/vocdoni/folio/internal/activity"
	"github.com/vocdoni/folio/internal/webui"
)

const uiPassword = "e2e web ui password"

// uiClient is a signed-in browser stand-in (cookie jar, CSRF header).
type uiClient struct {
	t    *testing.T
	base string
	c    *http.Client
}

func (u *uiClient) call(method, path string, body any, out any) int {
	u.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, u.base+path, rd)
	if method != http.MethodGet {
		req.Header.Set(webui.HeaderUI, "1")
		req.Header.Set("Origin", u.base)
	}
	resp, err := u.c.Do(req)
	if err != nil {
		u.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			u.t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, raw)
		}
	}
	return resp.StatusCode
}

// TestWebUIWiring drives the web UI API of a complete Folio: disabled
// without a password; with one, login, MCP calls in the activity feed (one
// row each), a live template preview through the preview queue (real
// pandoc and typst, nothing stored), a render from Markdown and its page.
func TestWebUIWiring(t *testing.T) {
	off := start(t, map[string]string{"FOLIO_SEED": "false"}, nil)
	if code, _, _ := get(t, off.url+"/ui/"); code != http.StatusNotFound {
		t.Fatalf("/ui without a password: %d", code)
	}
	if code, _, _ := get(t, off.url+"/ui/api/session"); code != http.StatusNotFound {
		t.Fatalf("/ui/api without a password: %d", code)
	}

	h := start(t, map[string]string{"FOLIO_UI_PASSWORD": uiPassword}, nil)
	// The SPA when `make ui` ran (or in the image), else the "not built" page.
	code, hdr, body := get(t, h.url+"/ui/")
	built := code == http.StatusOK && strings.Contains(string(body), `<div id="app">`)
	notBuilt := code == http.StatusServiceUnavailable && strings.Contains(string(body), "make ui")
	if (!built && !notBuilt) || !strings.Contains(hdr.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatalf("/ui/: %d %.200s", code, body)
	}
	jar, _ := cookiejar.New(nil)
	u := &uiClient{t: t, base: h.url, c: &http.Client{Jar: jar}}
	if code := u.call("GET", "/ui/api/stats", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("stats without a session: %d", code)
	}
	var sess webui.SessionOut
	if code := u.call("POST", "/ui/api/login", webui.LoginIn{Password: uiPassword}, &sess); code != 200 || !sess.Authenticated {
		t.Fatalf("login %d %+v", code, sess)
	}

	// MCP calls (read and write, ok and error) show up as activity rows.
	before := h.app.Activity.Written()
	h.ok("get_guide", nil, nil)
	h.ok("list_templates", nil, nil)
	h.fail("get_document", map[string]any{"id": "0199a000-0000-7000-8000-000000000001"})
	for deadline := time.Now().Add(5 * time.Second); h.app.Activity.Written() < before+3; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("activity rows written: %d", h.app.Activity.Written()-before)
		}
	}
	var act webui.ActivityOut
	u.call("GET", "/ui/api/activity?source=mcp", nil, &act)
	var actions []string
	for _, r := range act.Items {
		actions = append(actions, r.Actor+":"+r.Action+":"+r.Outcome)
	}
	if got := strings.Join(actions, ","); !strings.HasPrefix(got, "alice:get_document:error,alice:list_templates:ok,alice:get_guide:ok") {
		t.Fatalf("activity %s", got)
	}

	// Live template preview: real render, PNGs in memory only.
	var tpl webui.ItemOut
	if code := u.call("GET", "/ui/api/templates/technical-report", nil, &tpl); code != 200 {
		t.Fatalf("get template %d", code)
	}
	var spec map[string]any
	_ = json.Unmarshal(tpl.Spec, &spec)
	spec["id"], spec["name"] = "report-draft", "Report draft"
	var files int
	_ = h.app.DB.R.QueryRow(`SELECT count(*) FROM files`).Scan(&files)
	var pv webui.PreviewOut
	if code := u.call("POST", "/ui/api/templates/preview", map[string]any{"spec": spec}, &pv); code != 200 || !pv.OK || len(pv.PageURLs) == 0 {
		t.Fatalf("preview %d %+v", code, pv)
	}
	if code, _, body := uget(u, pv.PageURLs[0]); code != 200 || !bytes.HasPrefix(body, []byte("\x89PNG")) {
		t.Fatalf("preview page %d", code)
	}
	var after int
	_ = h.app.DB.R.QueryRow(`SELECT count(*) FROM files`).Scan(&after)
	if after != files {
		t.Fatal("a preview stored files")
	}

	// Create from Markdown through the UI.
	var r webui.RenderOut
	in := map[string]any{"template_id": "technical-report", "markdown": "# Summary\n\nWritten in the web UI.\n\n# Findings\n\nAll good.\n\n# Recommendations\n\nKeep going.",
		"metadata": map[string]any{"title": "UI report", "date": "2026-10-18", "language": "en", "project": "Folio"}, "formats": []string{"pdf"},
		"idempotency_key": "e2e-ui-render-0001"}
	if code := u.call("POST", "/ui/api/render", in, &r); code != 200 || r.Revision != 1 {
		var e map[string]any
		u.call("POST", "/ui/api/render", in, &e)
		t.Fatalf("render %d %+v %v", code, r, e)
	}
	var doc webui.DocumentOut
	u.call("GET", "/ui/api/documents/"+r.DocumentID, nil, &doc)
	if doc.PageCount < 1 || len(doc.PageURLs) != doc.PageCount {
		t.Fatalf("document %+v", doc)
	}
	rows, _, _ := activity.List(t.Context(), h.app.DB, activity.Filter{Source: "ui", Action: "ui.render"})
	for deadline := time.Now().Add(5 * time.Second); len(rows) == 0 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		rows, _, _ = activity.List(t.Context(), h.app.DB, activity.Filter{Source: "ui", Action: "ui.render"})
	}
	if len(rows) != 1 || rows[0].Actor != "ui" || rows[0].TargetID != r.DocumentID {
		t.Fatalf("ui.render rows %+v", rows)
	}
}

func uget(u *uiClient, path string) (int, http.Header, []byte) {
	resp, err := u.c.Get(u.base + path)
	if err != nil {
		u.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b
}
