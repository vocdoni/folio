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
)

// Session cookie and limits.
const (
	cookieName  = "folio_ui"
	cookiePath  = "/ui"
	maxSessions = 256
)

type session struct {
	key     string // SHA-256 of the cookie value: the map key and the owner id of streams, previews and jobs
	created time.Time
	expires time.Time
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

// create returns a new session and its cookie value.
func (s *sessions) create() (*session, string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, "", err
	}
	cookie := base64.RawURLEncoding.EncodeToString(b[:])
	now := s.now()
	ss := &session{key: keyOf(cookie), created: now, expires: now.Add(s.ttl)}
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

func (s *Server) setCookie(w http.ResponseWriter, value string, maxAge int) {
	//nolint:gosec // G124: Secure follows FOLIO_PUBLIC_URL (https in production; plain http only for local development)
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: value, Path: cookiePath, HttpOnly: true,
		Secure: s.secure, SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
}

// passwordOK compares SHA-256 digests in constant time.
func (s *Server) passwordOK(pw string) bool {
	got := sha256.Sum256([]byte(pw))
	want := sha256.Sum256([]byte(s.o.Config.UIPassword))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

// SessionOut is GET /ui/api/session.
type SessionOut struct {
	Authenticated bool      `json:"authenticated"`
	ExpiresAt     time.Time `json:"expires_at,omitzero"`
	AI            bool      `json:"ai,omitempty"`
	Version       string    `json:"version,omitempty"`
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	ss, ok := s.currentSession(r)
	if !ok {
		writeJSON(w, http.StatusOK, SessionOut{})
		return
	}
	writeJSON(w, http.StatusOK, SessionOut{Authenticated: true, ExpiresAt: ss.expires.UTC(), AI: s.aiEnabled(), Version: s.o.Version})
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
	ss, cookie, err := s.sessions.create()
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.setCookie(w, cookie, 0)
	writeJSON(w, http.StatusOK, SessionOut{Authenticated: true, ExpiresAt: ss.expires.UTC(), AI: s.aiEnabled(), Version: s.o.Version})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.sessions.remove(sessionOf(r).key)
	s.setCookie(w, "", -1)
	writeJSON(w, http.StatusOK, SessionOut{})
}

// aiEnabled reports whether the AI assistants are available.
func (s *Server) aiEnabled() bool { return s.o.AI != nil }
