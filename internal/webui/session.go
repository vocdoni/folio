// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/vocdoni/folio/internal/activity"
)

// Session cookie and limits.
const (
	cookieName  = "folio_ui"
	cookiePath  = "/ui"
	maxSessions = 256
)

// Sign-in methods (session.method, SessionOut.method).
const (
	methodPassword = "password"
	methodOIDC     = "oidc"
)

type session struct {
	key     string // SHA-256 of the cookie value: the map key and the owner id of streams, previews and jobs
	method  string // methodPassword | methodOIDC
	email   string // lower-cased e-mail of an SSO sign-in; "" for the password
	created time.Time
	expires time.Time
}

// actor is the activity actor of the session's actions: its e-mail, or
// Actor for password sessions.
func (ss *session) actor() string {
	if ss.email != "" && activity.ValidActor(ss.email) {
		return ss.email
	}
	return Actor
}

// sessions is the in-memory session store: random 256-bit ids, sliding
// expiry, at most max live sessions (the oldest is evicted). Sessions are
// lost on restart.
type sessions struct {
	mu    sync.Mutex
	m     map[string]*session
	ttl   time.Duration
	max   int
	now   func() time.Time
	onEnd func(key string) // called without the lock held
}

func newSessions(ttl time.Duration, maxN int, now func() time.Time, onEnd func(string)) *sessions {
	return &sessions{m: map[string]*session{}, ttl: ttl, max: maxN, now: now, onEnd: onEnd}
}

func keyOf(cookie string) string {
	sum := sha256.Sum256([]byte(cookie))
	return hex.EncodeToString(sum[:])
}

// create returns a new session signed in with method (and email for SSO)
// and its cookie value.
func (s *sessions) create(method, email string) (*session, string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, "", err
	}
	cookie := base64.RawURLEncoding.EncodeToString(b[:])
	now := s.now()
	ss := &session{key: keyOf(cookie), method: method, email: email, created: now, expires: now.Add(s.ttl)}
	var ended []string
	s.mu.Lock()
	for k, v := range s.m {
		if !now.Before(v.expires) {
			delete(s.m, k)
			ended = append(ended, k)
		}
	}
	for len(s.m) >= s.max {
		var oldest *session
		for _, v := range s.m {
			if oldest == nil || v.created.Before(oldest.created) {
				oldest = v
			}
		}
		delete(s.m, oldest.key)
		ended = append(ended, oldest.key)
	}
	s.m[ss.key] = ss
	s.mu.Unlock()
	for _, k := range ended {
		s.onEnd(k)
	}
	return ss, cookie, nil
}

// get returns the live session for a cookie value and slides its expiry.
func (s *sessions) get(cookie string) (*session, bool) {
	if cookie == "" {
		return nil, false
	}
	key := keyOf(cookie)
	now := s.now()
	s.mu.Lock()
	ss, ok := s.m[key]
	if ok && !now.Before(ss.expires) {
		delete(s.m, key)
		s.mu.Unlock()
		s.onEnd(key)
		return nil, false
	}
	if ok {
		ss.expires = now.Add(s.ttl)
	}
	s.mu.Unlock()
	return ss, ok
}

// remove ends a session (logout).
func (s *sessions) remove(key string) {
	s.mu.Lock()
	_, ok := s.m[key]
	delete(s.m, key)
	s.mu.Unlock()
	if ok {
		s.onEnd(key)
	}
}

func (s *sessions) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}

type sessionKey struct{}

func withSession(ctx context.Context, ss *session) context.Context {
	return context.WithValue(ctx, sessionKey{}, ss)
}

// sessionOf returns the request's session (set by the auth wrapper).
func sessionOf(r *http.Request) *session {
	ss, _ := r.Context().Value(sessionKey{}).(*session)
	return ss
}

func (s *Server) currentSession(r *http.Request) (*session, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return nil, false
	}
	return s.sessions.get(c.Value)
}

// setCookie sets the session cookie. It is SameSite=Lax, not Strict: the
// browser drops Strict cookies on the request that ends a cross-site
// redirect chain (the SSO callback's 303 to /ui/). Mutating requests stay
// guarded by the X-Folio-UI header and the Origin check.
func (s *Server) setCookie(w http.ResponseWriter, value string, maxAge int) {
	//nolint:gosec // G124: Secure follows FOLIO_PUBLIC_URL (https in production; plain http only for local development)
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: value, Path: cookiePath, HttpOnly: true,
		Secure: s.secure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}

// passwordOK compares SHA-256 digests in constant time. Without a
// configured password nothing matches (not even the empty string).
func (s *Server) passwordOK(pw string) bool {
	if s.o.Config.UIPassword == "" {
		return false
	}
	got := sha256.Sum256([]byte(pw))
	want := sha256.Sum256([]byte(s.o.Config.UIPassword))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

// SessionOut is GET /ui/api/session (and the answer of login and logout).
type SessionOut struct {
	Authenticated bool      `json:"authenticated"`
	ExpiresAt     time.Time `json:"expires_at,omitzero"`
	Method        string    `json:"method,omitempty"` // password | oidc
	Email         string    `json:"email,omitempty"`  // SSO sessions only
	AI            bool      `json:"ai,omitempty"`
	Version       string    `json:"version,omitempty"`
	Methods       Methods   `json:"methods"` // what the sign-in screen offers
}

// Methods are the configured sign-in methods.
type Methods struct {
	Password bool        `json:"password"`
	OIDC     *OIDCMethod `json:"oidc"` // null when SSO is off
}

// OIDCMethod is the SSO sign-in button.
type OIDCMethod struct {
	Name string `json:"name"`
}

// sessionOut describes ss (nil: signed out) and the sign-in methods.
func (s *Server) sessionOut(ss *session) SessionOut {
	out := SessionOut{Methods: Methods{Password: s.o.Config.UIPassword != ""}}
	if s.o.Config.OIDCIssuer != "" {
		out.Methods.OIDC = &OIDCMethod{Name: s.o.Config.OIDCName}
	}
	if ss != nil {
		out.Authenticated, out.ExpiresAt, out.Method, out.Email = true, ss.expires.UTC(), ss.method, ss.email
		out.AI, out.Version = s.aiEnabled(), s.o.Version
	}
	return out
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	ss, _ := s.currentSession(r)
	writeJSON(w, http.StatusOK, s.sessionOut(ss))
}

// LoginIn is POST /ui/api/login.
type LoginIn struct {
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip, source := clientIP(r, s.o.Config.TrustedProxies)
	s.logIPSourceOnce(source)
	if wait, ok := s.limiter.allow(ip.String()); !ok {
		w.Header().Set("Retry-After", retryAfter(wait))
		writeError(w, r, apiErr(http.StatusTooManyRequests, "rate_limited",
			"too many sign-in attempts; wait "+retryAfter(wait)+" seconds and try again"))
		return
	}
	var in LoginIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	if !s.passwordOK(in.Password) {
		writeError(w, r, apiErr(http.StatusUnauthorized, "unauthorized", "the password is not correct"))
		return
	}
	ss, cookie, err := s.sessions.create(methodPassword, "")
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.setCookie(w, cookie, 0)
	writeJSON(w, http.StatusOK, s.sessionOut(ss))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.sessions.remove(sessionOf(r).key)
	s.setCookie(w, "", -1)
	writeJSON(w, http.StatusOK, s.sessionOut(nil))
}

// aiEnabled reports whether the AI assistants are available.
func (s *Server) aiEnabled() bool { return s.o.AI != nil }
