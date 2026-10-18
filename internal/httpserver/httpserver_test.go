// SPDX-License-Identifier: AGPL-3.0-only

package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/config"
	"github.com/vocdoni/folio/internal/model"
)

const (
	tokA    = "alice-token-0123456789abcdefghijklmnop"
	tokB    = "bob-token-0123456789abcdefghijklmnopqrs"
	pdfID   = "0b1c2d3e-4f50-4a6b-8c7d-8e9fa0b1c2d3"
	docxID  = "1b1c2d3e-4f50-4a6b-9c7d-8e9fa0b1c2d3"
	trashID = "2b1c2d3e-4f50-4a6b-ac7d-8e9fa0b1c2d3"
)

var tokens = []config.Token{{Name: "alice", Value: tokA}, {Name: "bob", Value: tokB}}

type fakeFiles struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeFiles) FileForDownload(_ context.Context, id string) (catalog.File, []byte, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	switch id {
	case pdfID:
		return catalog.File{ID: id, Kind: "pdf", Name: "website-redesign.pdf", MIME: "application/pdf"}, []byte("%PDF-1.7 test"), nil
	case docxID:
		return catalog.File{ID: id, Kind: "docx", Name: "Propuesta ñ.docx", MIME: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"}, []byte("PK\x03\x04"), nil
	}
	return catalog.File{}, nil, &model.Error{Code: model.ErrNotFound, Message: "file not found"}
}

// whoami echoes the authenticated actor.
var whoami = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	ti := auth.TokenInfoFromContext(r.Context())
	if ti == nil {
		_, _ = io.WriteString(w, "anonymous")
		return
	}
	_, _ = io.WriteString(w, ti.UserID)
})

func newTestHandler(t *testing.T, o Options) (http.Handler, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	if o.MCP == nil {
		o.MCP = whoami
	}
	if o.Files == nil {
		o.Files = &fakeFiles{}
	}
	o.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	h, err := NewHandler(o)
	if err != nil {
		t.Fatal(err)
	}
	return h, &logs
}

func do(h http.Handler, method, path, token string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAuthRequiredOnMCP(t *testing.T) {
	h, logs := newTestHandler(t, Options{Tokens: tokens, Health: Health{Version: "test"}})
	for _, tc := range []struct{ name, header string }{
		{"missing", ""},
		{"wrong", "Bearer " + strings.Repeat("x", 40)},
		{"prefix of a valid token", "Bearer " + tokA[:32]},
		{"not bearer", "Basic " + tokA},
	} {
		if rec := do(h, http.MethodPost, "/mcp", tc.header, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", tc.name, rec.Code)
		}
	}
	if rec := do(h, http.MethodPost, "/mcp", "Bearer "+tokB, nil); rec.Code != 200 || rec.Body.String() != "bob" {
		t.Fatalf("valid token: %d %q", rec.Code, rec.Body.String())
	}
	if rec := do(h, http.MethodPost, "/mcp", "bearer "+tokA, nil); rec.Body.String() != "alice" {
		t.Fatalf("scheme is case-insensitive: %q", rec.Body.String())
	}
	// Public endpoints need no token.
	if rec := do(h, http.MethodGet, "/healthz", "", nil); rec.Code != 200 {
		t.Fatalf("healthz: %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/f/"+pdfID+"/x.pdf", "", nil); rec.Code != 200 {
		t.Fatalf("download: %d", rec.Code)
	}
	if strings.Contains(logs.String(), tokA) || strings.Contains(logs.String(), tokB) {
		t.Fatal("a token reached the logs")
	}
}

func TestNoTokenRequiresInsecureFlag(t *testing.T) {
	if _, err := NewHandler(Options{MCP: whoami, Files: &fakeFiles{}, Logger: slog.Default()}); err == nil {
		t.Fatal("handler without tokens and without FOLIO_INSECURE_NO_AUTH must be refused")
	}
	h, _ := newTestHandler(t, Options{InsecureNoAuth: true})
	if rec := do(h, http.MethodPost, "/mcp", "", nil); rec.Code != 200 || rec.Body.String() != "anonymous" {
		t.Fatalf("insecure mode: %d %q", rec.Code, rec.Body.String())
	}
}

func TestDownloadHeadersAnd404s(t *testing.T) {
	files := &fakeFiles{}
	h, logs := newTestHandler(t, Options{Tokens: tokens, Files: files})
	rec := do(h, http.MethodGet, "/f/"+pdfID+"/anything.pdf", "", nil)
	want := map[string]string{
		"Content-Type":           "application/pdf",
		"Content-Disposition":    `inline; filename=website-redesign.pdf`,
		"X-Robots-Tag":           "noindex, nofollow",
		"Referrer-Policy":        "no-referrer",
		"X-Content-Type-Options": "nosniff",
		"Cache-Control":          "private, max-age=300",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if rec.Body.String() != "%PDF-1.7 test" {
		t.Errorf("body %q", rec.Body.String())
	}
	rec = do(h, http.MethodGet, "/f/"+docxID+"/d.docx", "", nil)
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") || !strings.Contains(cd, "filename*=utf-8''Propuesta%20%C3%B1.docx") {
		t.Errorf("docx disposition %q", cd)
	}
	if rec := do(h, http.MethodHead, "/f/"+pdfID+"/x.pdf", "", nil); rec.Code != 200 || rec.Body.Len() != 0 {
		t.Errorf("HEAD: %d len %d", rec.Code, rec.Body.Len())
	}
	before := files.calls
	for _, p := range []string{"/f/" + trashID + "/x.pdf", "/f/not-a-uuid/x.pdf", "/f/" + strings.ToUpper(pdfID) + "/x.pdf", "/f/" + pdfID, "/f/"} {
		rec := do(h, http.MethodGet, p, "", nil)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "file not found") {
			t.Errorf("%s: %d %q", p, rec.Code, rec.Body.String())
		}
	}
	if files.calls != before+1 {
		t.Errorf("malformed ids must not reach the store: %d lookups", files.calls-before)
	}
	if rec := do(h, http.MethodPost, "/f/"+pdfID+"/x.pdf", "", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST download: %d", rec.Code)
	}
	if strings.Contains(logs.String(), pdfID) || strings.Contains(logs.String(), "website-redesign") {
		t.Fatal("a download capability reached the logs")
	}
}

func TestHealthzAndRobots(t *testing.T) {
	ping := errors.New("db down")
	var fail bool
	h, _ := newTestHandler(t, Options{Tokens: tokens, Health: Health{Version: "v1.2.3", Sandbox: "rlimits-only", SchemaVersion: 1},
		Ping: func(context.Context) error {
			if fail {
				return ping
			}
			return nil
		}})
	rec := do(h, http.MethodGet, "/healthz", "", nil)
	var got Health
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("healthz: %d %v", rec.Code, err)
	}
	if got != (Health{Status: "ok", Version: "v1.2.3", Sandbox: "rlimits-only", SchemaVersion: 1}) {
		t.Fatalf("healthz body %+v", got)
	}
	fail = true
	if rec := do(h, http.MethodGet, "/healthz", "", nil); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"degraded"`) {
		t.Fatalf("degraded healthz: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(h, http.MethodGet, "/robots.txt", "", nil)
	if rec.Body.String() != "User-agent: *\nDisallow: /\n" {
		t.Fatalf("robots %q", rec.Body.String())
	}
	if rec := do(h, http.MethodGet, "/", "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("/: %d", rec.Code)
	}
}

func TestRequestBodyLimit(t *testing.T) {
	read := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		_, _ = io.WriteString(w, "ok")
	})
	h, _ := newTestHandler(t, Options{Tokens: tokens, MCP: read, MaxRequestBytes: 1024})
	if rec := do(h, http.MethodPost, "/mcp", "Bearer "+tokA, strings.NewReader(strings.Repeat("a", 1025))); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("declared length: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", io.NopCloser(strings.NewReader(strings.Repeat("a", 2048))))
	req.ContentLength = -1 // chunked: only the reader can enforce the limit
	req.Header.Set("Authorization", "Bearer "+tokA)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("streamed body: %d", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/mcp", "Bearer "+tokA, strings.NewReader("small")); rec.Code != 200 {
		t.Fatalf("small body: %d", rec.Code)
	}
}

func TestServeDrainsInFlightRequests(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		if r.Context().Err() != nil {
			http.Error(w, "cancelled", 500)
			return
		}
		_, _ = io.WriteString(w, "rendered")
	})
	h, _ := newTestHandler(t, Options{Tokens: tokens, MCP: slow})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, h, 5*time.Second, slog.New(slog.DiscardHandler)) }()
	url := "http://" + ln.Addr().String()
	type result struct {
		body string
		err  error
	}
	resc := make(chan result, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodPost, url+"/mcp", nil)
		req.Header.Set("Authorization", "Bearer "+tokA)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			resc <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		resc <- result{body: string(b)}
	}()
	<-started
	cancel() // SIGTERM
	refused := false
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		c, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
		if err != nil {
			refused = true
			break
		}
		c.Close()
	}
	if !refused {
		t.Error("new connections are still accepted while draining")
	}
	close(release)
	if r := <-resc; r.err != nil || r.body != "rendered" {
		t.Fatalf("in-flight request: %q %v", r.body, r.err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}
