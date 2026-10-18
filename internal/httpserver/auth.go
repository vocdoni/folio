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
// it; others get 401 from the SDK middleware.
func RequireToken(tokens []config.Token, h http.Handler) http.Handler {
	return auth.RequireBearerToken(Verifier(tokens), &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(h)
}
