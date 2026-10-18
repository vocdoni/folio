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

const brandAnswer = `{"name":"Acme Widgets","colors":{"primary":"#C8246B","secondary":"#0B7FAB","accent":"#E8B500","text":"#1C2421","muted":"#5B6662","background":"#FFFFFF"},"fonts":{"heading":"Inter","body":"Source Serif 4","mono":"JetBrains Mono"},"header":"Acme Widgets","footer":"acme.example","logo_index":%d}`

// TestBrandFromLogo (B-AC5): extracted colours plus a proposal that
// validates, with the logo attached.
func TestBrandFromLogo(t *testing.T) {
	stub := &aiStub{answers: map[string][]string{"brand_suggest": {strings.Replace(brandAnswer, "%d", "-1", 1)}}}
	sv := newAIServices(t, stub, nil)
	logo := decode[AssetOut](t, sv.upload("acme-logo.png", testPNG(120, 60)), 200)
	started := decode[JobStartedOut](t, sv.do("POST", "/ui/api/ai/brand-suggest", BrandSuggestIn{AssetID: logo.ID}, nil), http.StatusAccepted)
	j := sv.waitJob(started.JobID)
	raw, _ := json.Marshal(j.Result)
	var out BrandSuggestOut
	_ = json.Unmarshal(raw, &out)
	if j.State != jobDone || len(out.Swatches) < 2 || out.Swatches[0].Hex != "#FFFFFF" || out.Brand.ID != "acme-widgets" ||
		out.Brand.Logo == nil || out.Brand.Logo.AssetID != logo.ID || len(out.Findings) != 0 {
		t.Fatalf("suggestion %s", raw)
	}
	stub.mu.Lock()
	body, _ := json.Marshal(stub.reqs[0])
	stub.mu.Unlock()
	if !strings.Contains(string(body), "#C8246B") || !strings.Contains(string(body), "data:image/png;base64,") {
		t.Fatal("the model must see the logo colours and, with vision, the logo")
	}
	var brands int
	_ = sv.opts.DB.R.QueryRow(`SELECT count(*) FROM brands`).Scan(&brands)
	if brands != 0 {
		t.Fatal("a suggestion must not be saved")
	}
}

// TestBrandFromURL (B-AC5): evidence from an https page and its same-origin
// stylesheet; the chosen logo is uploaded only on confirmation and only from
// a URL Folio extracted; a private address is refused by the SSRF guard.
func TestBrandFromURL(t *testing.T) {
	site := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><head><title>Acme Widgets</title><link rel="stylesheet" href="/site.css">` +
				`<link rel="icon" href="/logo.svg"></head><body>Buy widgets</body></html>`))
		case "/site.css":
			_, _ = w.Write([]byte(`:root{--brand:#C8246B} a{color:#0b7fab}`))
		case "/logo.svg":
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(testSVG))
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()
	pool := x509.NewCertPool()
	pool.AddCert(site.Certificate())
	allow := func(netip.Addr) bool { return true }
	stub := &aiStub{answers: map[string][]string{"brand_suggest": {strings.Replace(brandAnswer, "%d", "0", 1)}}}
	sv := newAIServices(t, stub, func(o *Options) {
		o.FetchOptions = []assets.FetchOption{assets.WithAddrPolicy(allow), assets.WithRootCAs(pool)}
		o.Assets.Fetcher = assets.NewFetcher(1<<20, o.FetchOptions...)
	})
	started := decode[JobStartedOut](t, sv.do("POST", "/ui/api/ai/brand-suggest", BrandSuggestIn{URL: site.URL + "/"}, nil), http.StatusAccepted)
	j := sv.waitJob(started.JobID)
	raw, _ := json.Marshal(j.Result)
	var out BrandSuggestOut
	_ = json.Unmarshal(raw, &out)
	if j.State != jobDone || out.Evidence == nil || len(out.Evidence.Images) != 1 || out.LogoIndex != 0 ||
		out.Evidence.Properties["--brand"] != "#C8246B" || out.Brand.Logo != nil {
		t.Fatalf("suggestion %s", raw)
	}
	var assetsBefore int
	_ = sv.opts.DB.R.QueryRow(`SELECT count(*) FROM assets`).Scan(&assetsBefore)
	if assetsBefore != 0 {
		t.Fatal("nothing may be downloaded before the user confirms")
	}
	if w := sv.do("POST", "/ui/api/ai/brand-logo", BrandLogoIn{JobID: started.JobID, Index: 3}, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("index out of range: %d", w.Code)
	}
	a := decode[AssetOut](t, sv.do("POST", "/ui/api/ai/brand-logo", BrandLogoIn{JobID: started.JobID, Index: 0}, nil), 200)
	if a.MIME != "image/svg+xml" || a.Name != "logo.svg" {
		t.Fatalf("logo %+v", a)
	}

	// Default address policy: loopback is refused.
	strict := newAIServices(t, &aiStub{answers: map[string][]string{"brand_suggest": {strings.Replace(brandAnswer, "%d", "-1", 1)}}}, nil)
	started = decode[JobStartedOut](t, strict.do("POST", "/ui/api/ai/brand-suggest", BrandSuggestIn{URL: site.URL + "/"}, nil), http.StatusAccepted)
	if j := strict.waitJob(started.JobID); j.State != jobError || !strings.Contains(j.Error.Message, "non-public") {
		t.Fatalf("private address: %+v", j)
	}
	if w := strict.do("POST", "/ui/api/ai/brand-suggest", BrandSuggestIn{URL: "http://example.org/"}, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("http URL: %d", w.Code)
	}
}

// TestBrandPageEvidenceRefusesInternalAddresses: with the default address
// policy the page fetch refuses loopback, private and cloud metadata
// addresses before connecting.
func TestBrandPageEvidenceRefusesInternalAddresses(t *testing.T) {
	sv := newAIServices(t, &aiStub{}, nil)
	for _, u := range []string{
		"https://127.0.0.1/",
		"https://10.0.0.1/",
		"https://169.254.169.254/latest/meta-data/",
		"https://[::1]/",
		"https://[fd00:ec2::254]/",
	} {
		if _, err := sv.s.pageEvidence(t.Context(), u); err == nil || !strings.Contains(err.Error(), "non-public") {
			t.Errorf("%s: %v", u, err)
		}
	}
}
