// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/vocdoni/folio/internal/activity"
	"github.com/vocdoni/folio/internal/config"
	"github.com/vocdoni/folio/internal/store"
)

const testPassword = "correct horse battery staple"

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *testClock) Add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type fixture struct {
	t      *testing.T
	s      *Server
	opts   Options
	clk    *testClock
	cookie string
}

var testDist = fstest.MapFS{
	"index.html":            {Data: []byte("<!doctype html><title>Folio</title>")},
	"assets/app-3f2a9c.js":  {Data: []byte("console.log(1)")},
	"assets/app-3f2a9c.css": {Data: []byte("body{}")},
	"favicon.svg":           {Data: []byte("<svg/>")},
}

func testConfig(publicURL string) *config.Config {
	return &config.Config{PublicURL: publicURL, UIPassword: testPassword, UISessionTTL: time.Hour, MaxRequestBytes: 1 << 20,
		MaxAssetBytes: 1 << 20, TrustedProxies: config.DefaultTrustedProxies, UIPreviewConcurrency: 1, AIDailyCalls: 200, AISessionCalls: 30}
}

// newFixture builds a UI server over an empty database; tweak adjusts the
// options before New.
func newFixture(t *testing.T, tweak func(*Options)) *fixture {
	t.Helper()
	db, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	clk := &testClock{t: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}
	o := Options{Config: testConfig("https://folio.example.test"), DB: db, Dist: testDist, Version: "test", Sandbox: "rlimits-only",
		Logger: testLogger(), Now: clk.Now}
	if tweak != nil {
		tweak(&o)
	}
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, s: s, opts: o, clk: clk}
}

// do sends a request; non-GET requests carry the CSRF header unless the
// caller set headers itself.
func (f *fixture) do(method, path string, body any, hdr map[string]string) *httptest.ResponseRecorder {
	f.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req := httptest.NewRequest(method, path, rd)
	req.RemoteAddr = "203.0.113.7:4444"
	if method != http.MethodGet && hdr == nil {
		req.Header.Set(HeaderUI, "1")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if f.cookie != "" {
		req.AddCookie(&http.Cookie{Name: cookieName, Value: f.cookie})
	}
	w := httptest.NewRecorder()
	f.s.ServeHTTP(w, req)
	return w
}

func (f *fixture) login() {
	f.t.Helper()
	w := f.do(http.MethodPost, "/ui/api/login", LoginIn{Password: testPassword}, nil)
	if w.Code != http.StatusOK {
		f.t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName {
			f.cookie = c.Value
		}
	}
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error APIError `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("not an API error: %d %s", w.Code, w.Body)
	}
	return body.Error.Code
}

// TestDisabledWithoutPassword (B-AC1): no password → no UI handler (the
// HTTP layer then serves 404 for /ui; see httpserver.TestUIMount).
func TestDisabledWithoutPassword(t *testing.T) {
	cfg := testConfig("https://folio.example.test")
	cfg.UIPassword = ""
	if _, err := New(Options{Config: cfg, DB: &store.DB{}, Logger: slog.Default()}); err == nil {
		t.Fatal("New must refuse to build a UI without a password")
	}
}

func TestLoginSessionLogout(t *testing.T) {
	f := newFixture(t, nil)
	var s SessionOut
	w := f.do(http.MethodGet, "/ui/api/session", nil, nil)
	if _ = json.Unmarshal(w.Body.Bytes(), &s); w.Code != 200 || s.Authenticated {
		t.Fatalf("anonymous session: %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodGet, "/ui/api/stats", nil, nil); w.Code != http.StatusNotFound && w.Code != http.StatusUnauthorized {
		t.Fatalf("protected route without a session: %d", w.Code)
	}
	if w := f.do(http.MethodPost, "/ui/api/logout", nil, nil); w.Code != http.StatusUnauthorized || errorCode(t, w) != "unauthorized" {
		t.Fatalf("logout without session: %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodPost, "/ui/api/login", LoginIn{Password: "wrong password!!"}, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", w.Code)
	}
	w = f.do(http.MethodPost, "/ui/api/login", LoginIn{Password: testPassword}, nil)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	var c *http.Cookie
	for _, x := range w.Result().Cookies() {
		if x.Name == cookieName {
			c = x
		}
	}
	if c == nil || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/ui" || len(c.Value) != 43 {
		t.Fatalf("cookie attributes: %+v", c)
	}
	f.cookie = c.Value
	if _ = json.Unmarshal(f.do(http.MethodGet, "/ui/api/session", nil, nil).Body.Bytes(), &s); !s.Authenticated || s.Version != "test" {
		t.Fatalf("session after login: %+v", s)
	}
	f.clk.Add(50 * time.Minute) // sliding: each use extends the hour
	f.do(http.MethodGet, "/ui/api/session", nil, nil)
	f.clk.Add(50 * time.Minute)
	if _ = json.Unmarshal(f.do(http.MethodGet, "/ui/api/session", nil, nil).Body.Bytes(), &s); !s.Authenticated {
		t.Fatal("sliding expiry did not extend the session")
	}
	w = f.do(http.MethodPost, "/ui/api/logout", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("logout: %d %v", w.Code, w.Header())
	}
	if _ = json.Unmarshal(f.do(http.MethodGet, "/ui/api/session", nil, nil).Body.Bytes(), &s); s.Authenticated {
		t.Fatal("session survived logout")
	}
}

func TestSessionExpiryAndEviction(t *testing.T) {
	var ended []string
	clk := &testClock{t: time.Unix(0, 0)}
	ss := newSessions(time.Hour, 3, clk.Now, func(k string) { ended = append(ended, k) })
	var cookies []string
	for i := 0; i < 4; i++ {
		_, c, err := ss.create()
		if err != nil {
			t.Fatal(err)
		}
		cookies = append(cookies, c)
		clk.Add(time.Second)
	}
	if ss.len() != 3 || len(ended) != 1 || ended[0] != keyOf(cookies[0]) {
		t.Fatalf("eviction: len %d ended %v", ss.len(), ended)
	}
	if _, ok := ss.get(cookies[0]); ok {
		t.Fatal("evicted session still valid")
	}
	clk.Add(2 * time.Hour)
	if _, ok := ss.get(cookies[3]); ok {
		t.Fatal("expired session still valid")
	}
	if len(ended) != 2 {
		t.Fatalf("expiry must end the session: %v", ended)
	}
}

// TestLoginRateLimit (B-AC1): 429 with Retry-After after 5 attempts per
// minute from one address; other addresses are not affected.
func TestLoginRateLimit(t *testing.T) {
	f := newFixture(t, nil)
	for i := 0; i < 5; i++ {
		if w := f.do(http.MethodPost, "/ui/api/login", LoginIn{Password: "nope nope nope"}, nil); w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i+1, w.Code)
		}
	}
	w := f.do(http.MethodPost, "/ui/api/login", LoginIn{Password: testPassword}, nil)
	if w.Code != http.StatusTooManyRequests || errorCode(t, w) != "rate_limited" || w.Header().Get("Retry-After") != "60" {
		t.Fatalf("6th attempt: %d %v %s", w.Code, w.Header(), w.Body)
	}
	req := httptest.NewRequest(http.MethodPost, "/ui/api/login", strings.NewReader(`{"password":"`+testPassword+`"}`))
	req.RemoteAddr = "198.51.100.9:1"
	req.Header.Set(HeaderUI, "1")
	rec := httptest.NewRecorder()
	f.s.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("another address: %d", rec.Code)
	}
	f.clk.Add(61 * time.Second)
	if w := f.do(http.MethodPost, "/ui/api/login", LoginIn{Password: testPassword}, nil); w.Code != 200 {
		t.Fatalf("after the window: %d", w.Code)
	}
	l := newLoginLimiter(f.clk.Now)
	for i := 0; i < loginGlobal; i++ {
		if _, ok := l.allow(netip.AddrFrom4([4]byte{198, 18, byte(i), 1}).String()); !ok {
			t.Fatalf("global limit hit at %d", i)
		}
	}
	if _, ok := l.allow("192.0.2.1"); ok {
		t.Fatal("the global limit must apply across addresses")
	}
}

func TestClientIP(t *testing.T) {
	trusted := config.DefaultTrustedProxies
	cases := []struct {
		peer, xff, want, source string
	}{
		{"203.0.113.7:1", "", "203.0.113.7", "peer"},
		{"203.0.113.7:1", "1.2.3.4", "203.0.113.7", "peer"},                        // untrusted peer: header ignored
		{"10.0.0.2:1", "198.51.100.4", "198.51.100.4", "x-forwarded-for"},          // trusted proxy
		{"10.0.0.2:1", "6.6.6.6, 198.51.100.4", "198.51.100.4", "x-forwarded-for"}, // spoofed left entry ignored
		{"10.0.0.2:1", "198.51.100.4, 10.0.0.9", "198.51.100.4", "x-forwarded-for"},
		{"10.0.0.2:1", "garbage", "10.0.0.2", "peer"},
		{"[::1]:1", "2001:db8::7", "2001:db8::7", "x-forwarded-for"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = c.peer
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		got, src := clientIP(r, trusted)
		if got.String() != c.want || src != c.source {
			t.Errorf("%s %q: %s (%s), want %s (%s)", c.peer, c.xff, got, src, c.want, c.source)
		}
	}
}

// TestCSRFAndHeaders (B-AC2).
func TestCSRFAndHeaders(t *testing.T) {
	f := newFixture(t, nil)
	f.login()
	if w := f.do(http.MethodPost, "/ui/api/logout", nil, map[string]string{}); w.Code != http.StatusForbidden {
		t.Fatalf("missing X-Folio-UI: %d", w.Code)
	}
	if w := f.do(http.MethodPost, "/ui/api/logout", nil, map[string]string{HeaderUI: "1", "Origin": "https://evil.example"}); w.Code != http.StatusForbidden {
		t.Fatalf("foreign origin: %d", w.Code)
	}
	if w := f.do(http.MethodPost, "/ui/api/login", LoginIn{Password: testPassword}, map[string]string{HeaderUI: "1", "Origin": "http://folio.example.test"}); w.Code != http.StatusForbidden {
		t.Fatalf("scheme mismatch: %d", w.Code)
	}
	w := f.do(http.MethodGet, "/ui/api/session", nil, nil)
	for k, v := range map[string]string{"Content-Security-Policy": CSP, "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer", "Cache-Control": "no-store"} {
		if got := w.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if !strings.Contains(CSP, "frame-ancestors 'none'") || strings.Contains(CSP, "unsafe") {
		t.Fatal("CSP must forbid framing and unsafe sources")
	}
	if w := f.do(http.MethodPost, "/ui/api/logout", nil, map[string]string{HeaderUI: "1", "Origin": "https://FOLIO.example.test"}); w.Code != 200 {
		t.Fatalf("same origin: %d %s", w.Code, w.Body)
	}
	// Plain-http public URL: the cookie is not Secure (local development).
	g := newFixture(t, func(o *Options) { o.Config = testConfig("http://localhost:8080") })
	w = g.do(http.MethodPost, "/ui/api/login", LoginIn{Password: testPassword}, nil)
	if c := w.Result().Cookies(); len(c) != 1 || c[0].Secure {
		t.Fatalf("http cookie: %+v", c)
	}
}

func TestStaticAndSPAFallback(t *testing.T) {
	f := newFixture(t, nil)
	check := func(method, path string, code int, cache, body string) {
		t.Helper()
		w := f.do(method, path, nil, map[string]string{})
		if w.Code != code || (cache != "" && w.Header().Get("Cache-Control") != cache) || (body != "" && !strings.Contains(w.Body.String(), body)) {
			t.Errorf("%s %s: %d cache=%q body=%.60q", method, path, w.Code, w.Header().Get("Cache-Control"), w.Body.String())
		}
		if w.Header().Get("Content-Security-Policy") != CSP {
			t.Errorf("%s %s: no CSP", method, path)
		}
	}
	check("GET", "/ui", http.StatusMovedPermanently, "", "")
	check("GET", "/ui/", 200, "no-cache", "<title>Folio</title>")
	check("GET", "/ui/documents/0199a0", 200, "no-cache", "<title>Folio</title>")
	check("GET", "/ui/assets/app-3f2a9c.js", 200, "public, max-age=31536000, immutable", "console.log")
	check("GET", "/ui/assets/missing.js", 404, "", "")
	check("GET", "/ui/favicon.svg", 200, "public, max-age=3600", "<svg")
	check("GET", "/ui/index.html", 404, "", "")
	check("GET", "/ui/fonts/Inter-Regular.ttf", 200, "public, max-age=86400", "")
	check("GET", "/ui/fonts/../folio.db", http.StatusTemporaryRedirect, "", "") // cleaned by the mux, then 404
	check("GET", "/ui/folio.db", 404, "", "")
	check("GET", "/ui/api/nope", 404, "no-store", `"code":"not_found"`)
	check("POST", "/ui/documents", 405, "", "")
	g := newFixture(t, func(o *Options) { o.Dist = fstest.MapFS{} })
	w := g.do("GET", "/ui/", nil, nil)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "make ui") {
		t.Fatalf("unbuilt UI: %d %s", w.Code, w.Body)
	}
	if w := g.do("GET", "/ui/api/session", nil, nil); w.Code != 200 {
		t.Fatalf("the API works without the built UI: %d", w.Code)
	}
}

// TestLoginIsRecorded: UI actions appear in the activity feed (actor ui).
func TestLoginIsRecorded(t *testing.T) {
	var rec *activity.Recorder
	f := newFixture(t, func(o *Options) {
		rec = activity.NewRecorder(o.DB, nil, o.Logger, o.Now)
		o.Activity = rec
	})
	f.do(http.MethodPost, "/ui/api/login", LoginIn{Password: "not the password"}, nil)
	f.login()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec.Run(ctx)
	rows, _, err := activity.List(context.Background(), f.opts.DB, activity.Filter{})
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows %+v %v", rows, err)
	}
	if r := rows[1]; r.Source != "ui" || r.Actor != Actor || r.Action != "ui.login" || r.Outcome != "error" || r.ErrorCode != "unauthorized" {
		t.Fatalf("failed login row %+v", r)
	}
	if rows[0].Outcome != "ok" {
		t.Fatalf("login row %+v", rows[0])
	}
}

// testLogger discards logs unless FOLIO_TEST_LOG is set.
func testLogger() *slog.Logger {
	if os.Getenv("FOLIO_TEST_LOG") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
