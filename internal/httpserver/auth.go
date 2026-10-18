// SPDX-License-Identifier: AGPL-3.0-only

package httpserver

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/vocdoni/folio/internal/config"
)

// Verifier returns a bearer-token verifier for the configured tokens. Tokens
// are compared as SHA-256 digests with subtle.ConstantTimeCompare, and every
// configured token is compared on every call, so neither the token length
// nor the position of the matching entry leaks through timing. The returned
// TokenInfo carries the token name as UserID (the audit actor) and no
// expiration (static tokens; the middleware runs with AllowMissingExpiration).
func Verifier(tokens []config.Token) auth.TokenVerifier {
	type entry struct {
		name string
		sum  [sha256.Size]byte
	}
	entries := make([]entry, len(tokens))
	for i, t := range tokens {
		entries[i] = entry{name: t.Name, sum: sha256.Sum256([]byte(t.Value))}
	}
	return func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		sum := sha256.Sum256([]byte(token))
		name := ""
		for _, e := range entries {
			if subtle.ConstantTimeCompare(sum[:], e.sum[:]) == 1 {
				name = e.name
			}
		}
		if name == "" {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{UserID: name}, nil
	}
}

// RequireToken wraps h so that only requests with a valid bearer token reach
// it; others get 401 (with a Bearer challenge) from the SDK middleware.
func RequireToken(tokens []config.Token, h http.Handler) http.Handler {
	inner := auth.RequireBearerToken(Verifier(tokens), &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(h)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner.ServeHTTP(&challengeWriter{ResponseWriter: w}, r)
	})
}

// challengeWriter adds the Bearer challenge to 401 responses.
type challengeWriter struct {
	http.ResponseWriter
	wrote bool
}

func (c *challengeWriter) WriteHeader(code int) {
	if !c.wrote {
		c.wrote = true
		if code == http.StatusUnauthorized {
			c.Header().Set("WWW-Authenticate", `Bearer realm="folio"`)
		}
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *challengeWriter) Write(b []byte) (int, error) {
	if !c.wrote {
		c.WriteHeader(http.StatusOK)
	}
	return c.ResponseWriter.Write(b)
}

func (c *challengeWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (c *challengeWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }
