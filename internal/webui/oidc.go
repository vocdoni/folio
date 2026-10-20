// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/vocdoni/folio/internal/activity"
	"github.com/vocdoni/folio/internal/config"
)

// SSO sign-in: the OpenID Connect authorization code flow with PKCE (S256).
const (
	oidcCookie       = "folio_oidc"
	oidcCallbackPath = "/ui/api/oidc/callback"
	oidcFlowTTL      = 10 * time.Minute // folio_oidc lifetime, sign-in start to callback
	oidcClockSkew    = 60 * time.Second // leeway on the ID token's expiry
	oidcHTTPTimeout  = 10 * time.Second // each request to the provider
	oidcBackoffMin   = 2 * time.Second  // discovery retry delay after a failure, doubling
	oidcBackoffMax   = time.Minute
)

// Sign-in error codes, sent to the SPA as /ui/?error=<code>.
const (
	oidcErrState       = "state"           // flow cookie missing, expired or not matching
	oidcErrProvider    = "provider"        // the provider answered with an error
	oidcErrExchange    = "exchange"        // the code could not be exchanged for tokens
	oidcErrToken       = "token"           // ID token missing or invalid (signature, issuer, audience, expiry, nonce)
	oidcErrDomain      = "domain"          // e-mail missing, unverified or not allowed
	oidcErrUnavailable = "sso_unavailable" // the provider's metadata could not be fetched
	oidcErrRateLimited = "rate_limited"
)

// returnToRE accepts relative paths only: not "//host" or "/\host".
var returnToRE = regexp.MustCompile(`^/[^/\\]`)

// errOIDCBackoff: discovery failed recently and is not retried yet.
var errOIDCBackoff = errors.New("webui: SSO provider discovery failed recently; waiting before the next attempt")

// safeReturnTo returns v when it is a relative path below /ui that is not
// an API route and not unreasonably long (the flow cookie must stay small),
// else "/". Backslashes, control characters and "." or ".." segments are
// refused: http.Redirect cleans the path, so "/../\host" would leave /ui
// and become "/\host", which browsers resolve as //host.
func safeReturnTo(v string) string {
	p, _, _ := strings.Cut(v, "?")
	if len(v) > 2048 || !returnToRE.MatchString(v) || strings.HasPrefix(v, "/api/") ||
		strings.ContainsFunc(v, func(r rune) bool { return r == '\\' || r < 0x20 || r == 0x7f }) ||
		slices.ContainsFunc(strings.Split(p, "/"), func(seg string) bool { return seg == "." || seg == ".." }) {
		return "/"
	}
	return v
}

// ssoHome is where a sign-in lands when return_to is refused.
const ssoHome = "/ui/"

// ssoLocation is the redirect target after a successful SSO sign-in:
// "/ui" + safeReturnTo(v), checked again as http.Redirect and a browser
// will see it. The path is checked raw and percent-decoded (browsers treat
// "%2e%2e" as ".."): no backslash, no control character, no "." or ".."
// segment, and path.Clean must keep it below /ui and outside /ui/api
// (the router decodes "%61pi" to "api"). So the Location can never start
// with "//" or "/\" (a network-path reference to another host), whatever
// cleaning happens on the way.
func ssoLocation(v string) string {
	loc := "/ui" + safeReturnTo(v)
	p, _, _ := strings.Cut(loc, "?")
	dec, err := url.PathUnescape(p)
	if err != nil {
		return ssoHome
	}
	for _, s := range []string{p, dec} {
		c := path.Clean(s)
		if strings.ContainsFunc(s, func(r rune) bool { return r == '\\' || r < 0x20 || r == 0x7f }) ||
			slices.ContainsFunc(strings.Split(s, "/"), func(seg string) bool { return seg == "." || seg == ".." }) ||
			(c != "/ui" && !strings.HasPrefix(c, "/ui/")) || strings.HasPrefix(c+"/", "/ui/api/") {
			return ssoHome
		}
	}
	return loc
}

// oidcFlow is the folio_oidc payload: one sign-in in progress.
type oidcFlow struct {
	State        string `json:"state"`
	Nonce        string `json:"nonce"`
	CodeVerifier string `json:"code_verifier"`
	ReturnTo     string `json:"return_to"`
	IAT          int64  `json:"iat"`
}

// oidcClient talks to the provider. Discovery is lazy: the metadata is
// fetched on the first sign-in, never at startup (Folio starts while the
// provider is down), cached once it succeeds and retried with backoff.
type oidcClient struct {
	issuer string
	conf   oauth2.Config // Endpoint is set by discovery
	http   *http.Client
	aead   cipher.AEAD // folio_oidc encryption with a per-process random key
	now    func() time.Time

	mu       sync.Mutex
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	retryAt  time.Time
	backoff  time.Duration
}

func newOIDCClient(c *config.Config, now func() time.Time) (*oidcClient, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &oidcClient{issuer: c.OIDCIssuer, http: &http.Client{Timeout: oidcHTTPTimeout}, aead: aead, now: now,
		conf: oauth2.Config{ClientID: c.OIDCClientID, ClientSecret: string(c.OIDCClientSecret),
			RedirectURL: strings.TrimRight(c.PublicURL, "/") + oidcCallbackPath, Scopes: []string{oidc.ScopeOpenID, "email", "profile"}}}, nil
}

// ctx carries the provider HTTP client (oauth2 and go-oidc read the same key).
func (o *oidcClient) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(oidc.ClientContext(context.WithoutCancel(parent), o.http), oidcHTTPTimeout)
}

// ready returns the OAuth2 configuration and the ID token verifier,
// discovering the provider on first use.
func (o *oidcClient) ready(parent context.Context) (*oauth2.Config, *oidc.IDTokenVerifier, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.provider == nil {
		now := o.now()
		if now.Before(o.retryAt) {
			return nil, nil, errOIDCBackoff
		}
		ctx, cancel := o.ctx(parent)
		defer cancel()
		p, err := oidc.NewProvider(ctx, o.issuer)
		if err != nil {
			o.backoff = min(max(2*o.backoff, oidcBackoffMin), oidcBackoffMax)
			o.retryAt = now.Add(o.backoff)
			return nil, nil, err
		}
		o.provider, o.backoff = p, 0
		o.conf.Endpoint = p.Endpoint()
		// The keys are fetched on the first verification and refetched for an
		// unknown key id.
		o.verifier = p.Verifier(&oidc.Config{ClientID: o.conf.ClientID, Now: func() time.Time { return o.now().Add(-oidcClockSkew) }})
	}
	conf := o.conf
	return &conf, o.verifier, nil
}

// seal encrypts a flow for the folio_oidc cookie: base64url(nonce || AES-GCM).
func (o *oidcClient) seal(f oidcFlow) (string, error) {
	plain, err := json.Marshal(f)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, o.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(o.aead.Seal(nonce, nonce, plain, []byte(oidcCookie))), nil
}

// open decrypts a folio_oidc value; it fails for anything tampered with,
// sealed by another process or older than oidcFlowTTL.
func (o *oidcClient) open(v string) (oidcFlow, bool) {
	var f oidcFlow
	b, err := base64.RawURLEncoding.DecodeString(v)
	ns := o.aead.NonceSize()
	if err != nil || len(b) < ns {
		return f, false
	}
	plain, err := o.aead.Open(nil, b[:ns], b[ns:], []byte(oidcCookie))
	if err != nil || json.Unmarshal(plain, &f) != nil {
		return f, false
	}
	age := o.now().Sub(time.Unix(f.IAT, 0))
	return f, f.State != "" && f.Nonce != "" && f.CodeVerifier != "" && age > -oidcClockSkew && age <= oidcFlowTTL
}

// randomToken is 32 random bytes, base64url (state and nonce).
func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func (s *Server) oidcRoutes(mux *http.ServeMux) {
	if s.oidc == nil {
		return
	}
	s.api(mux, "GET /ui/api/oidc/login", route{public: true}, s.handleOIDCLogin)
	s.api(mux, "GET "+oidcCallbackPath, route{public: true}, s.handleOIDCCallback)
}

func (s *Server) setOIDCCookie(w http.ResponseWriter, value string, maxAge int) {
	//nolint:gosec // G124: Secure follows FOLIO_PUBLIC_URL, as for the session cookie
	http.SetCookie(w, &http.Cookie{Name: oidcCookie, Value: value, Path: cookiePath, HttpOnly: true,
		Secure: s.secure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}

// oidcFail clears the flow cookie and sends the browser back to the sign-in
// screen with an error code (never the provider's own message).
func (s *Server) oidcFail(w http.ResponseWriter, r *http.Request, code string) {
	s.setOIDCCookie(w, "", -1)
	http.Redirect(w, r, "/ui/?error="+code, http.StatusSeeOther)
}

// allowSSO applies the sign-in rate limit (shared with the password) to an
// SSO route.
func (s *Server) allowSSO(w http.ResponseWriter, r *http.Request) bool {
	ip, source := clientIP(r, s.o.Config.TrustedProxies)
	s.logIPSourceOnce(source)
	if wait, ok := s.limiter.allow(ip.String()); !ok {
		w.Header().Set("Retry-After", retryAfter(wait))
		s.oidcFail(w, r, oidcErrRateLimited)
		return false
	}
	return true
}

// handleOIDCLogin starts an SSO sign-in: it stores state, nonce, PKCE
// verifier and return path in folio_oidc and redirects to the provider. The
// SPA links here (a GET navigation: the CSP's form-action 'self' would block
// a form whose answer redirects to the provider).
func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if !s.allowSSO(w, r) {
		return
	}
	conf, _, err := s.oidc.ready(r.Context())
	if err != nil {
		s.o.Logger.WarnContext(r.Context(), "web UI SSO: provider unavailable", "issuer", s.o.Config.OIDCIssuer, "err", err)
		s.oidcFail(w, r, oidcErrUnavailable)
		return
	}
	f := oidcFlow{CodeVerifier: oauth2.GenerateVerifier(), ReturnTo: safeReturnTo(r.URL.Query().Get("return_to")), IAT: s.o.Now().Unix()}
	if f.State, err = randomToken(); err == nil {
		f.Nonce, err = randomToken()
	}
	var sealed string
	if err == nil {
		sealed, err = s.oidc.seal(f)
	}
	if err != nil {
		s.o.Logger.ErrorContext(r.Context(), "web UI SSO: sign-in start failed", "err", err)
		s.oidcFail(w, r, oidcErrUnavailable)
		return
	}
	s.setOIDCCookie(w, sealed, int(oidcFlowTTL.Seconds()))
	http.Redirect(w, r, conf.AuthCodeURL(f.State, oidc.Nonce(f.Nonce), oauth2.S256ChallengeOption(f.CodeVerifier)), http.StatusFound)
}

// ssoClaims are the ID token claims Folio reads.
type ssoClaims struct {
	Email         string `json:"email"`
	EmailVerified any    `json:"email_verified"` // only the JSON value true admits
}

// handleOIDCCallback finishes an SSO sign-in: state, code exchange with the
// PKCE verifier, ID token (signature, issuer, audience, expiry, nonce) and
// admission (verified e-mail in the allowed lists). It always clears
// folio_oidc; refusals record no activity.
func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	start := s.o.Now()
	if !s.allowSSO(w, r) {
		return
	}
	fail := func(code, reason string, args ...any) {
		s.o.Logger.InfoContext(r.Context(), "web UI SSO: sign-in refused", append([]any{"code", code, "reason", reason}, args...)...)
		s.oidcFail(w, r, code)
	}
	q := r.URL.Query()
	var f oidcFlow
	ok := false
	if c, err := r.Cookie(oidcCookie); err == nil {
		f, ok = s.oidc.open(c.Value)
	}
	if !ok || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(f.State)) != 1 {
		fail(oidcErrState, "missing, expired or mismatched state")
		return
	}
	if e := q.Get("error"); e != "" {
		fail(oidcErrProvider, "the provider answered an error", "error", truncate(e, 100), "error_description", truncate(q.Get("error_description"), 300))
		return
	}
	conf, verifier, err := s.oidc.ready(r.Context())
	if err != nil {
		fail(oidcErrUnavailable, err.Error())
		return
	}
	ctx, cancel := s.oidc.ctx(r.Context())
	defer cancel()
	tok, err := conf.Exchange(ctx, q.Get("code"), oauth2.VerifierOption(f.CodeVerifier))
	if err != nil {
		fail(oidcErrExchange, err.Error())
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		fail(oidcErrToken, "no id_token in the token response")
		return
	}
	idt, err := verifier.Verify(ctx, raw)
	if err != nil {
		fail(oidcErrToken, err.Error())
		return
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(f.Nonce)) != 1 {
		fail(oidcErrToken, "nonce mismatch")
		return
	}
	var cl ssoClaims
	if err := idt.Claims(&cl); err != nil {
		fail(oidcErrToken, err.Error())
		return
	}
	email := strings.ToLower(strings.TrimSpace(cl.Email))
	if verified, _ := cl.EmailVerified.(bool); !verified || !s.admits(email) {
		fail(oidcErrDomain, "e-mail missing, unverified or not allowed", "domain", domainOf(email), "email_verified", truncate(fmt.Sprint(cl.EmailVerified), 32))
		return
	}
	if old, ok := s.currentSession(r); ok {
		s.sessions.remove(old.key) // a sign-in replaces the browser's previous session
	}
	ss, cookie, err := s.sessions.create(methodOIDC, email)
	if err != nil {
		s.o.Logger.ErrorContext(r.Context(), "web UI SSO: session creation failed", "err", err)
		s.oidcFail(w, r, oidcErrUnavailable)
		return
	}
	s.setOIDCCookie(w, "", -1)
	s.setCookie(w, cookie, 0)
	if s.o.Activity != nil {
		s.o.Activity.Record(activity.Row{TS: s.o.Now(), Source: activity.SourceUI, Actor: ss.actor(), Action: "ui.login",
			Outcome: activity.OutcomeOK, DurationMS: s.o.Now().Sub(start).Milliseconds(), Detail: activity.Detail{Method: methodOIDC}})
	}
	http.Redirect(w, r, ssoLocation(f.ReturnTo), http.StatusSeeOther)
}

// admits reports whether a lower-cased e-mail is in FOLIO_OIDC_ALLOWED_EMAILS
// or its domain (after the last @, exact match) in FOLIO_OIDC_ALLOWED_DOMAINS.
func (s *Server) admits(email string) bool {
	at := strings.LastIndexByte(email, '@')
	if at <= 0 || at == len(email)-1 {
		return false
	}
	return slices.Contains(s.o.Config.OIDCAllowedEmails, email) || slices.Contains(s.o.Config.OIDCAllowedDomains, email[at+1:])
}

// domainOf is the part of an e-mail after the last @ (for logs).
func domainOf(email string) string {
	return email[strings.LastIndexByte(email, '@')+1:]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}
