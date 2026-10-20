// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/vocdoni/folio/internal/activity"
)

const testCallback = "https://folio.example.test" + oidcCallbackPath

// ssoFixture builds a UI server whose SSO provider is a fakeIdP (allowed
// domain example.com); password keeps FOLIO_UI_PASSWORD set.
func ssoFixture(t *testing.T, password bool, tweak func(*Options)) (*fixture, *fakeIdP, *activity.Recorder) {
	t.Helper()
	idp := newFakeIdP(t, testCallback)
	var rec *activity.Recorder
	f := newFixture(t, func(o *Options) {
		o.Config = withOIDC(testConfig("https://folio.example.test"), idp.srv.URL)
		if !password {
			o.Config.UIPassword = ""
		}
		rec = activity.NewRecorder(o.DB, nil, o.Logger, o.Now)
		o.Activity = rec
		if tweak != nil {
			tweak(o)
		}
	})
	idp.set(f.clk.Now, nil, nil)
	return f, idp, rec
}

func cookieNamed(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// ssoFlow is a sign-in started at Folio and approved by the provider.
type ssoFlow struct {
	login    *httptest.ResponseRecorder // GET /ui/api/oidc/login
	flow     *http.Cookie               // folio_oidc
	callback *url.URL                   // where the provider sent the browser
}

// ssoStart opens the sign-in and follows the redirect to the provider,
// which approves at once.
func (f *fixture) ssoStart(returnTo string) ssoFlow {
	f.t.Helper()
	st := ssoFlow{login: f.do(http.MethodGet, "/ui/api/oidc/login?return_to="+url.QueryEscape(returnTo), nil, nil)}
	if st.login.Code != http.StatusFound {
		f.t.Fatalf("SSO login: %d %v", st.login.Code, st.login.Header())
	}
	st.flow = cookieNamed(st.login, oidcCookie)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(st.login.Header().Get("Location"))
	if err != nil {
		f.t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		f.t.Fatalf("provider: %d", resp.StatusCode)
	}
	if st.callback, err = url.Parse(resp.Header.Get("Location")); err != nil {
		f.t.Fatal(err)
	}
	return st
}

// ssoFinish sends the browser's callback request with query and the flow
// cookie (nil: none).
func (f *fixture) ssoFinish(query string, flow *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, oidcCallbackPath+"?"+query, nil)
	req.RemoteAddr = "203.0.113.7:4444"
	if flow != nil {
		req.AddCookie(&http.Cookie{Name: flow.Name, Value: flow.Value})
	}
	w := httptest.NewRecorder()
	f.s.ServeHTTP(w, req)
	return w
}

// wantSSOError checks a refused sign-in: back to the sign-in screen with
// the code, the flow cookie cleared, no session cookie.
func wantSSOError(t *testing.T, w *httptest.ResponseRecorder, code string) {
	t.Helper()
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/ui/?error="+code {
		t.Fatalf("want error %s: %d %v", code, w.Code, w.Header())
	}
	if c := cookieNamed(w, oidcCookie); c == nil || c.MaxAge >= 0 {
		t.Fatalf("folio_oidc not cleared: %+v", c)
	}
	if c := cookieNamed(w, cookieName); c != nil {
		t.Fatalf("a refused sign-in set a session cookie: %+v", c)
	}
}

func activityRows(t *testing.T, f *fixture, rec *activity.Recorder) []activity.Row {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec.Run(ctx)
	rows, _, err := activity.List(context.Background(), f.opts.DB, activity.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// TestSSOSignIn: the full flow with SSO alone and next to the password.
func TestSSOSignIn(t *testing.T) {
	for _, password := range []bool{false, true} {
		f, idp, rec := ssoFixture(t, password, nil)
		if idp.discovered.Load() != 0 {
			t.Fatal("discovery must wait for the first sign-in")
		}
		st := f.ssoStart("/documents?status=draft")
		if c := st.flow; c == nil || c.Path != "/ui" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.MaxAge != 600 {
			t.Fatalf("folio_oidc attributes: %+v", c)
		}
		auth, _ := url.Parse(st.login.Header().Get("Location"))
		q := auth.Query()
		if !strings.HasPrefix(auth.String(), idp.srv.URL+"/auth?") || q.Get("client_id") != "folio" || q.Get("redirect_uri") != testCallback ||
			q.Get("scope") != "openid email profile" || q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43 ||
			len(q.Get("state")) != 43 || len(q.Get("nonce")) != 43 || q.Has("prompt") || q.Has("code_verifier") {
			t.Fatalf("authorization request: %s", auth)
		}
		w := f.ssoFinish(st.callback.RawQuery, st.flow)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/ui/documents?status=draft" {
			t.Fatalf("callback: %d %v", w.Code, w.Header())
		}
		if c := cookieNamed(w, oidcCookie); c == nil || c.MaxAge >= 0 {
			t.Fatalf("folio_oidc not cleared: %+v", c)
		}
		c := cookieNamed(w, cookieName)
		if c == nil || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/ui" || len(c.Value) != 43 {
			t.Fatalf("session cookie: %+v", c)
		}
		f.cookie = c.Value
		if s := probeSession(t, f); !s.Authenticated || s.Method != "oidc" || s.Email != "jane.doe@example.com" || s.Methods.Password != password {
			t.Fatalf("session: %+v", s)
		}
		if w := f.do(http.MethodPost, "/ui/api/logout", nil, nil); w.Code != http.StatusOK {
			t.Fatalf("logout: %d", w.Code)
		}
		f.cookie = ""
		if password {
			f.login()
		}
		f.ssoStart("/") // the provider metadata is cached
		if n := idp.discovered.Load(); n != 1 {
			t.Fatalf("%d discovery requests, want 1", n)
		}
		rows := activityRows(t, f, rec)
		want := []string{"ui.logout jane.doe@example.com", "ui.login jane.doe@example.com oidc"}
		if password {
			want = append([]string{"ui.login ui"}, want...)
		}
		var got []string
		for _, r := range rows {
			got = append(got, strings.TrimSpace(r.Action+" "+r.Actor+" "+r.Detail.Method))
		}
		if strings.Join(got, ", ") != strings.Join(want, ", ") {
			t.Fatalf("activity (password %v): %q, want %q", password, got, want)
		}
	}
}

// TestSSORefusals: every check refuses with its code, clears the flow
// cookie, creates no session and records nothing.
func TestSSORefusals(t *testing.T) {
	other := testRSAKeys(t)[1]
	set := func(k string, v any) func(map[string]any) { return func(c map[string]any) { c[k] = v } }
	del := func(k string) func(map[string]any) { return func(c map[string]any) { delete(c, k) } }
	cases := []struct {
		name    string
		claims  func(map[string]any)
		signer  *rsa.PrivateKey
		advance time.Duration
		tamper  func(q url.Values, flow *http.Cookie) (url.Values, *http.Cookie)
		want    string
	}{
		{name: "email not verified", claims: set("email_verified", false), want: "domain"},
		{name: "email_verified missing", claims: del("email_verified"), want: "domain"},
		{name: "email_verified as a string", claims: set("email_verified", "true"), want: "domain"},
		{name: "missing email", claims: del("email"), want: "domain"},
		{name: "domain not allowed", claims: set("email", "jane@evil.example"), want: "domain"},
		{name: "domain suffix", claims: set("email", "jane@notexample.com"), want: "domain"},
		{name: "subdomain", claims: set("email", "jane@mail.example.com"), want: "domain"},
		{name: "allowed domain before the last @", claims: set("email", "jane@example.com@evil.example"), want: "domain"},
		{name: "wrong nonce", claims: set("nonce", "not-the-nonce"), want: "token"},
		{name: "wrong audience", claims: set("aud", "another-client"), want: "token"},
		{name: "wrong issuer", claims: set("iss", "https://evil.example"), want: "token"},
		{name: "expired beyond the skew", claims: func(c map[string]any) { c["exp"] = c["iat"].(int64) - 61 }, want: "token"},
		{name: "signed by another key", signer: other, want: "token"},
		{name: "wrong state", want: "state", tamper: func(q url.Values, fl *http.Cookie) (url.Values, *http.Cookie) {
			q.Set("state", "AAAA"+q.Get("state")[4:])
			return q, fl
		}},
		{name: "missing state", want: "state", tamper: func(q url.Values, fl *http.Cookie) (url.Values, *http.Cookie) {
			q.Del("state")
			return q, fl
		}},
		{name: "no flow cookie", want: "state", tamper: func(q url.Values, _ *http.Cookie) (url.Values, *http.Cookie) { return q, nil }},
		{name: "tampered flow cookie", want: "state", tamper: func(q url.Values, fl *http.Cookie) (url.Values, *http.Cookie) {
			b := []byte(fl.Value)
			b[len(b)/2] ^= 1
			return q, &http.Cookie{Name: fl.Name, Value: string(b)}
		}},
		{name: "flow older than ten minutes", advance: 11 * time.Minute, want: "state"},
		{name: "provider error", want: "provider", tamper: func(q url.Values, fl *http.Cookie) (url.Values, *http.Cookie) {
			return url.Values{"state": {q.Get("state")}, "error": {"access_denied"}, "error_description": {"<b>no</b>"}}, fl
		}},
		{name: "bad code", want: "exchange", tamper: func(q url.Values, fl *http.Cookie) (url.Values, *http.Cookie) {
			q.Set("code", "not-a-code")
			return q, fl
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, idp, rec := ssoFixture(t, true, nil)
			idp.set(nil, c.claims, c.signer)
			st := f.ssoStart("/")
			f.clk.Add(c.advance)
			q, flow := st.callback.Query(), st.flow
			if c.tamper != nil {
				q, flow = c.tamper(q, flow)
			}
			w := f.ssoFinish(q.Encode(), flow)
			wantSSOError(t, w, c.want)
			if rows := activityRows(t, f, rec); len(rows) != 0 {
				t.Fatalf("a refused sign-in recorded %+v", rows)
			}
		})
	}
}

// TestSSOAllowedEmailAndSkew: an e-mail list admits addresses outside the
// domains; a token expired less than 60 s ago is still accepted.
func TestSSOAllowedEmailAndSkew(t *testing.T) {
	f, idp, _ := ssoFixture(t, false, func(o *Options) {
		o.Config.OIDCAllowedDomains, o.Config.OIDCAllowedEmails = nil, []string{"guest@partner.example"}
	})
	idp.set(nil, func(c map[string]any) { c["email"], c["exp"] = "Guest@Partner.example", c["iat"].(int64)-30 }, nil)
	st := f.ssoStart("/")
	w := f.ssoFinish(st.callback.RawQuery, st.flow)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/ui/" {
		t.Fatalf("allowed e-mail: %d %v", w.Code, w.Header())
	}
	f.cookie = cookieNamed(w, cookieName).Value
	if s := probeSession(t, f); s.Email != "guest@partner.example" {
		t.Fatalf("session %+v", s)
	}
	idp.set(nil, nil, nil) // jane.doe@example.com: the domain list is empty now
	st = f.ssoStart("/")
	wantSSOError(t, f.ssoFinish(st.callback.RawQuery, st.flow), "domain")
}

func TestSafeReturnTo(t *testing.T) {
	for in, want := range map[string]string{
		"/": "/", "/documents": "/documents", "/documents?status=draft&q=a%20b": "/documents?status=draft&q=a%20b",
		"": "/", "documents": "/", "//evil.example": "/", "//evil.example/ui": "/", `/\evil.example`: "/",
		"https://evil.example/": "/", "javascript:alert(1)": "/", " /documents": "/",
		"/api/oidc/login": "/", strings.Repeat("/a", 1025): "/",
	} {
		if got := safeReturnTo(in); got != want {
			t.Errorf("safeReturnTo(%q) = %q, want %q", in, got, want)
		}
	}
	f, _, _ := ssoFixture(t, false, nil)
	st := f.ssoStart("//evil.example")
	if w := f.ssoFinish(st.callback.RawQuery, st.flow); w.Header().Get("Location") != "/ui/" {
		t.Fatalf("open redirect: %v", w.Header())
	}
}

// TestSSOIssuerDown: Folio starts and the password works while the provider
// is down; SSO answers sso_unavailable and retries discovery after a backoff.
func TestSSOIssuerDown(t *testing.T) {
	f, idp, _ := ssoFixture(t, true, nil)
	idp.down.Store(true)
	if w := f.do(http.MethodGet, "/ui/", nil, nil); w.Code != http.StatusOK {
		t.Fatalf("SPA: %d", w.Code)
	}
	f.login()
	f.cookie = ""
	for range 2 {
		w := f.do(http.MethodGet, "/ui/api/oidc/login", nil, nil)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/ui/?error=sso_unavailable" {
			t.Fatalf("provider down: %d %v", w.Code, w.Header())
		}
	}
	if n := idp.discovered.Load(); n != 1 {
		t.Fatalf("%d discovery requests during the backoff, want 1", n)
	}
	idp.down.Store(false)
	f.clk.Add(3 * time.Second)
	f.ssoStart("/")
	if n := idp.discovered.Load(); n != 2 {
		t.Fatalf("%d discovery requests, want 2", n)
	}
}

// TestSSORateLimit: both SSO routes share the sign-in limit with the
// password and record nothing when refused.
func TestSSORateLimit(t *testing.T) {
	f, _, rec := ssoFixture(t, true, nil)
	for i := range 5 {
		if w := f.do(http.MethodGet, "/ui/api/oidc/login", nil, nil); w.Code != http.StatusFound {
			t.Fatalf("attempt %d: %d", i+1, w.Code)
		}
	}
	w := f.do(http.MethodGet, "/ui/api/oidc/login", nil, nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/ui/?error=rate_limited" || w.Header().Get("Retry-After") != "60" {
		t.Fatalf("6th login: %d %v", w.Code, w.Header())
	}
	wantSSOError(t, f.ssoFinish("code=x&state=y", nil), "rate_limited")
	if w := f.do(http.MethodPost, "/ui/api/login", LoginIn{Password: testPassword}, nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("password after SSO attempts: %d", w.Code)
	}
	f.clk.Add(61 * time.Second)
	if w := f.do(http.MethodGet, "/ui/api/oidc/login", nil, nil); w.Code != http.StatusFound {
		t.Fatalf("after the window: %d", w.Code)
	}
	if rows := activityRows(t, f, rec); len(rows) != 0 {
		t.Fatalf("rate-limited attempts recorded %+v", rows)
	}
}

// TestSSORoutesNeedIssuer: without FOLIO_OIDC_ISSUER the SSO routes do not exist.
func TestSSORoutesNeedIssuer(t *testing.T) {
	f := newFixture(t, nil)
	for _, p := range []string{"/ui/api/oidc/login", oidcCallbackPath + "?code=x&state=y"} {
		if w := f.do(http.MethodGet, p, nil, nil); w.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
}

// TestTruncateUTF8: a shortened log string never ends in a split rune.
func TestTruncateUTF8(t *testing.T) {
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{
		{"access_denied", 100, "access_denied"},
		{"abcdef", 3, "abc…"},
		{"ééé", 3, "é…"}, // the cut falls inside the second é
		{"ééé", 4, "éé…"},
		{"日本語", 4, "日…"},
	} {
		if got := truncate(c.in, c.n); got != c.want || !utf8.ValidString(got) {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

// TestSSODomainRefusalLog: a domain refusal logs the domain and a short
// email_verified, never the address or the raw claim.
func TestSSODomainRefusalLog(t *testing.T) {
	var logs bytes.Buffer
	f, idp, _ := ssoFixture(t, false, func(o *Options) { o.Logger = slog.New(slog.NewJSONHandler(&logs, nil)) })
	for _, v := range []any{strings.Repeat("x", 500), map[string]any{"nested": strings.Repeat("y", 500)}} {
		start := logs.Len()
		idp.set(nil, func(c map[string]any) { c["email_verified"] = v }, nil)
		st := f.ssoStart("/")
		wantSSOError(t, f.ssoFinish(st.callback.RawQuery, st.flow), "domain")
		out := logs.String()[start:]
		var line map[string]any
		for l := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
			var m map[string]any
			if json.Unmarshal([]byte(l), &m) == nil && m["msg"] == "web UI SSO: sign-in refused" {
				line = m
			}
		}
		ev, _ := line["email_verified"].(string)
		if line == nil || line["code"] != "domain" || line["domain"] != "example.com" || !strings.HasSuffix(ev, "…") || len(ev) > 32+len("…") {
			t.Fatalf("refusal log: %s", out)
		}
		if strings.Contains(out, "jane.doe") {
			t.Fatalf("the log carries the address: %s", out)
		}
	}
}

// TestSSOReplacesSession: an SSO sign-in in a browser that already holds a
// session ends that session.
func TestSSOReplacesSession(t *testing.T) {
	f, _, _ := ssoFixture(t, true, nil)
	f.login()
	old := f.cookie
	st := f.ssoStart("/")
	req := httptest.NewRequest(http.MethodGet, oidcCallbackPath+"?"+st.callback.RawQuery, nil)
	req.RemoteAddr = "203.0.113.7:4444"
	req.AddCookie(&http.Cookie{Name: oidcCookie, Value: st.flow.Value})
	req.AddCookie(&http.Cookie{Name: cookieName, Value: old})
	w := httptest.NewRecorder()
	f.s.ServeHTTP(w, req)
	c := cookieNamed(w, cookieName)
	if w.Code != http.StatusSeeOther || c == nil || c.Value == old {
		t.Fatalf("callback: %d %v", w.Code, w.Header())
	}
	if s := probeSession(t, f); s.Authenticated {
		t.Fatalf("the previous session survived: %+v", s)
	}
	f.cookie = c.Value
	if s := probeSession(t, f); !s.Authenticated || s.Method != "oidc" {
		t.Fatalf("new session: %+v", s)
	}
	if n := f.s.sessions.len(); n != 1 {
		t.Fatalf("%d sessions, want 1", n)
	}
}

// TestSSOHandlersNeverAnswerJSON: the browser routes report every failure,
// including the ones that need a broken random source and cannot be
// provoked in a test, through oidcFail (303 and folio_oidc cleared).
func TestSSOHandlersNeverAnswerJSON(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "oidc.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, d := range file.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || (fn.Name.Name != "handleOIDCLogin" && fn.Name.Name != "handleOIDCCallback") {
			continue
		}
		seen++
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if id, ok := call.Fun.(*ast.Ident); ok && (id.Name == "writeError" || id.Name == "writeJSON") {
					t.Errorf("%s calls %s", fn.Name.Name, id.Name)
				}
			}
			return true
		})
	}
	if seen != 2 {
		t.Fatalf("found %d SSO handlers, want 2", seen)
	}
}
