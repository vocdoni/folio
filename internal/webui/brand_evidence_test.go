// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/vocdoni/folio/internal/assets"
)

// TestBrandFromURLRefreshManifestInlineSVG: the page redirects with a meta
// refresh (followed once), links a manifest (fetched, bounded) and writes its
// logo as an inline SVG in the header; the inline logo is stored, sanitised,
// only on confirmation, and the model never sees the SVG bytes.
func TestBrandFromURLRefreshManifestInlineSVG(t *testing.T) {
	site := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<html><head><meta http-equiv="refresh" content="0;url=/en"></head></html>`))
		case "/en":
			_, _ = w.Write([]byte(`<html><head><title>Acme</title><link rel="manifest" href="/m.json"><link rel="stylesheet" href="/s.css"></head>` +
				`<body><header><a href="/en"><svg viewBox="0 0 10 10"><rect width="10" height="10" fill="#296948"/></svg></a></header></body></html>`))
		case "/s.css":
			_, _ = w.Write([]byte(`:root{--primary:oklch(47% .085 158)}`))
		case "/m.json":
			_, _ = w.Write([]byte(`{"icons":[{"src":"/i512.png","sizes":"512x512"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()
	pool := x509.NewCertPool()
	pool.AddCert(site.Certificate())
	stub := &aiStub{answers: map[string][]string{"brand_suggest": {strings.Replace(brandAnswer, "%d", "0", 1)}}}
	sv := newAIServices(t, stub, func(o *Options) {
		o.FetchOptions = []assets.FetchOption{assets.WithAddrPolicy(func(netip.Addr) bool { return true }), assets.WithRootCAs(pool)}
		o.Assets.Fetcher = assets.NewFetcher(1<<20, o.FetchOptions...)
	})
	started := decode[JobStartedOut](t, sv.do("POST", "/ui/api/ai/brand-suggest", BrandSuggestIn{URL: site.URL + "/"}, nil), http.StatusAccepted)
	j := sv.waitJob(started.JobID)
	raw, _ := json.Marshal(j.Result)
	var out BrandSuggestOut
	_ = json.Unmarshal(raw, &out)
	if j.State != jobDone || out.Evidence == nil || out.Evidence.Title != "Acme" || len(out.Evidence.Images) != 2 ||
		!strings.HasPrefix(out.Evidence.Images[0], "data:image/svg+xml;base64,") || !strings.HasSuffix(out.Evidence.Images[1], "/i512.png") {
		t.Fatalf("suggestion %s", raw)
	}
	if p := out.Evidence.Semantic["--primary"]; p != "#296948" {
		t.Fatalf("primary %q", p)
	}
	stub.mu.Lock()
	body, _ := json.Marshal(stub.reqs[0])
	stub.mu.Unlock()
	if strings.Contains(string(body), "base64,PHN2Zy") || !strings.Contains(string(body), "inline SVG logo") || !strings.Contains(string(body), "#296948") {
		t.Fatalf("model request %.600s", body)
	}
	a := decode[AssetOut](t, sv.do("POST", "/ui/api/ai/brand-logo", BrandLogoIn{JobID: started.JobID, Index: 0}, nil), 200)
	if a.MIME != "image/svg+xml" || a.Name != "logo.svg" {
		t.Fatalf("logo %+v", a)
	}
	ra, err := sv.s.o.Assets.Asset(t.Context(), a.ID, false)
	if err != nil || strings.Contains(string(ra.Data), "script") {
		t.Fatalf("stored SVG: %v %s", err, ra.Data)
	}
}
