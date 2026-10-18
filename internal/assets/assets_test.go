// SPDX-License-Identifier: AGPL-3.0-only

package assets

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestSanitizeRasterReencodes(t *testing.T) {
	in := pngBytes(t, 4, 3)
	in = append(in[:len(in):len(in)], []byte("trailing-metadata")...)
	s, err := Sanitize(in, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if s.MIME != MIMEPNG || s.Width != 4 || s.Height != 3 || bytes.Contains(s.Data, []byte("trailing-metadata")) {
		t.Fatalf("got %+v", s)
	}
}

func TestSanitizeRejectsUnknownAndOversize(t *testing.T) {
	if _, err := Sanitize([]byte("GIF89a...."), 1<<20); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("gif: %v", err)
	}
	if _, err := Sanitize(pngBytes(t, 2, 2), 10); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("size: %v", err)
	}
}

func TestSecuritySVGSanitiser(t *testing.T) {
	good := `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="200" height="60" viewBox="0 0 200 60">` +
		`<defs><linearGradient id="g"/></defs><rect width="200" height="60" fill="url(#g)"/><use xlink:href="#g"/></svg>`
	s, err := Sanitize([]byte(good), 1<<20)
	if err != nil {
		t.Fatalf("good SVG rejected: %v", err)
	}
	if s.MIME != MIMESVG || s.Width != 200 || s.Height != 60 {
		t.Fatalf("got %+v", s)
	}
	bad := map[string]string{
		"script":        `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"onload":        `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`,
		"foreignObject": `<svg xmlns="http://www.w3.org/2000/svg"><foreignObject><p>x</p></foreignObject></svg>`,
		"external href": `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><image xlink:href="https://evil.example/x.png"/></svg>`,
		"file href":     `<svg xmlns="http://www.w3.org/2000/svg"><image href="/etc/passwd"/></svg>`,
		"nested svg":    `<svg xmlns="http://www.w3.org/2000/svg"><image href="data:image/svg+xml;base64,PHN2Zy8+"/></svg>`,
		"css url":       `<svg xmlns="http://www.w3.org/2000/svg"><rect style="fill:url(https://evil.example/a)"/></svg>`,
		"style import":  `<svg xmlns="http://www.w3.org/2000/svg"><style>@import url(x.css);</style></svg>`,
		"doctype":       `<?xml version="1.0"?><!DOCTYPE svg [<!ENTITY x SYSTEM "file:///etc/passwd">]><svg xmlns="http://www.w3.org/2000/svg">&x;</svg>`,
		"pi":            `<?xml-stylesheet href="x.css"?><svg xmlns="http://www.w3.org/2000/svg"/>`,
		"deep":          `<svg xmlns="http://www.w3.org/2000/svg">` + strings.Repeat("<g>", 200) + strings.Repeat("</g>", 200) + `</svg>`,
		"smil":          `<svg xmlns="http://www.w3.org/2000/svg"><image href="#x"><set attributeName="href" to="https://evil.example/x.png"/></image></svg>`,
	}
	for name, svg := range bad {
		if _, err := Sanitize([]byte(svg), 1<<20); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSecuritySSRFRefusesInternalAddresses(t *testing.T) {
	f := NewFetcher(1 << 20)
	for _, u := range []string{
		"https://127.0.0.1/x.png",
		"https://localhost/x.png",
		"https://169.254.169.254/latest/meta-data/",
		"https://10.0.0.1/x.png",
		"https://192.168.1.1/x.png",
		"https://[::1]/x.png",
		"https://100.100.100.200/x.png",
	} {
		_, err := f.Fetch(context.Background(), u)
		if !errors.Is(err, ErrForbiddenAddress) {
			t.Errorf("%s: err = %v, want ErrForbiddenAddress", u, err)
		}
	}
	for _, u := range []string{"http://example.com/x.png", "https://user:pw@example.com/x", "file:///etc/passwd", "ftp://x/y"} {
		if _, err := f.Fetch(context.Background(), u); !errors.Is(err, ErrBadURL) {
			t.Errorf("%s: err = %v, want ErrBadURL", u, err)
		}
	}
}

func TestSecuritySSRFRedirectsAreRechecked(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte("PNGDATA"))
		case "/to-metadata":
			http.Redirect(w, r, "https://169.254.169.254/latest/meta-data/", http.StatusFound)
		case "/to-http":
			http.Redirect(w, r, "http://"+r.Host+"/ok", http.StatusFound)
		case "/big":
			_, _ = w.Write(bytes.Repeat([]byte("x"), 2048))
		default: // /loop/N redirects forever
			http.Redirect(w, r, srv.URL+"/loop"+r.URL.Path, http.StatusFound)
		}
	}))
	defer srv.Close()
	pool := srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	loopbackOnly := func(a netip.Addr) bool { return a.Unmap() == netip.MustParseAddr("127.0.0.1") }
	f := NewFetcher(1024, WithAddrPolicy(loopbackOnly), WithRootCAs(pool))
	ctx := context.Background()
	if b, err := f.Fetch(ctx, srv.URL+"/ok"); err != nil || string(b) != "PNGDATA" {
		t.Fatalf("ok: %q %v", b, err)
	}
	if _, err := f.Fetch(ctx, srv.URL+"/to-metadata"); !errors.Is(err, ErrForbiddenAddress) {
		t.Errorf("redirect to metadata: %v", err)
	}
	if _, err := f.Fetch(ctx, srv.URL+"/to-http"); !errors.Is(err, ErrBadURL) {
		t.Errorf("redirect to http: %v", err)
	}
	if _, err := f.Fetch(ctx, srv.URL+"/loop"); err == nil || !strings.Contains(err.Error(), "too many redirects") {
		t.Errorf("redirect loop: %v", err)
	}
	if _, err := f.Fetch(ctx, srv.URL+"/big"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("size cap: %v", err)
	}
}
