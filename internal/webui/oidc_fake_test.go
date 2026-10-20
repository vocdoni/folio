// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeIdP is an OpenID Connect provider for tests: discovery, a JWKS with
// one RSA key, an /auth endpoint that approves at once (the user signed in)
// and a /token endpoint that checks the client secret and PKCE and issues an
// RS256 ID token. claims adjusts the token per test.
type fakeIdP struct {
	t        *testing.T
	srv      *httptest.Server
	key      *rsa.PrivateKey
	clientID string
	secret   string
	redirect string // the only redirect_uri accepted

	mu     sync.Mutex
	now    func() time.Time
	grants map[string]fakeGrant
	claims func(map[string]any) // edits the default claims; nil = defaults
	signer *rsa.PrivateKey      // nil = key (tests sign with another key to break signatures)

	down       atomic.Bool  // discovery answers 503
	discovered atomic.Int32 // discovery requests served or refused
}

type fakeGrant struct {
	nonce, challenge string
}

const fakeKID = "test-key-1"

var (
	fakeKeyOnce sync.Once
	fakeKeys    [2]*rsa.PrivateKey
)

// testRSAKeys generates two RSA keys once per test binary.
func testRSAKeys(t *testing.T) [2]*rsa.PrivateKey {
	fakeKeyOnce.Do(func() {
		for i := range fakeKeys {
			k, err := rsa.GenerateKey(rand.Reader, 2048)
			if err != nil {
				panic(err)
			}
			fakeKeys[i] = k
		}
	})
	return fakeKeys
}

func newFakeIdP(t *testing.T, redirect string) *fakeIdP {
	t.Helper()
	p := &fakeIdP{t: t, key: testRSAKeys(t)[0], clientID: "folio", secret: "fake-client-secret", redirect: redirect,
		now: time.Now, grants: map[string]fakeGrant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("GET /keys", p.keys)
	mux.HandleFunc("GET /auth", p.auth)
	mux.HandleFunc("POST /token", p.token)
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *fakeIdP) discovery(w http.ResponseWriter, _ *http.Request) {
	p.discovered.Add(1)
	if p.down.Load() {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	u := p.srv.URL
	writeJSON(w, http.StatusOK, map[string]any{"issuer": u, "authorization_endpoint": u + "/auth", "token_endpoint": u + "/token",
		"jwks_uri": u + "/keys", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"}, "code_challenge_methods_supported": []string{"S256"}})
}

func (p *fakeIdP) keys(w http.ResponseWriter, _ *http.Request) {
	b64 := base64.RawURLEncoding.EncodeToString
	writeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": fakeKID, "alg": "RS256", "use": "sig",
		"n": b64(p.key.N.Bytes()), "e": b64(big.NewInt(int64(p.key.E)).Bytes())}}})
}

// auth approves the request at once and redirects back with a code.
func (p *fakeIdP) auth(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("client_id") != p.clientID || q.Get("response_type") != "code" || q.Get("redirect_uri") != p.redirect ||
		q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") == "" ||
		!strings.Contains(" "+q.Get("scope")+" ", " openid ") {
		http.Error(w, "fake: bad authorization request "+r.URL.RawQuery, http.StatusBadRequest)
		return
	}
	code, _ := randomToken()
	p.mu.Lock()
	p.grants[code] = fakeGrant{nonce: q.Get("nonce"), challenge: q.Get("code_challenge")}
	p.mu.Unlock()
	http.Redirect(w, r, p.redirect+"?code="+code+"&state="+q.Get("state"), http.StatusFound)
}

func (p *fakeIdP) token(w http.ResponseWriter, r *http.Request) {
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostFormValue("client_id"), r.PostFormValue("client_secret")
	}
	p.mu.Lock()
	g, found := p.grants[r.PostFormValue("code")]
	delete(p.grants, r.PostFormValue("code")) // codes are single use
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostFormValue("code_verifier")))
	if id != p.clientID || secret != p.secret || r.PostFormValue("grant_type") != "authorization_code" || !found ||
		r.PostFormValue("redirect_uri") != p.redirect || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	now := p.clock()
	claims := map[string]any{"iss": p.srv.URL, "sub": "user-1", "aud": p.clientID, "iat": now.Unix(), "exp": now.Add(10 * time.Minute).Unix(),
		"nonce": g.nonce, "email": "Jane.Doe@Example.com", "email_verified": true, "name": "Jane Doe"}
	p.mu.Lock()
	edit, signer := p.claims, p.signer
	p.mu.Unlock()
	if edit != nil {
		edit(claims)
	}
	if signer == nil {
		signer = p.key
	}
	writeJSON(w, http.StatusOK, map[string]any{"access_token": "fake-access-token", "token_type": "Bearer", "expires_in": 600,
		"id_token": signJWT(p.t, signer, claims)})
}

func (p *fakeIdP) clock() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.now()
}

// set changes the clock, the claims editor and the signing key.
func (p *fakeIdP) set(now func() time.Time, claims func(map[string]any), signer *rsa.PrivateKey) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if now != nil {
		p.now = now
	}
	p.claims, p.signer = claims, signer
}

// signJWT returns a compact RS256 JWS of claims.
func signJWT(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	input := enc(map[string]string{"alg": "RS256", "typ": "JWT", "kid": fakeKID}) + "." + enc(claims)
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}
