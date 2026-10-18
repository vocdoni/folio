// SPDX-License-Identifier: AGPL-3.0-only

package ai

import (
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	bundled "github.com/vocdoni/folio/assets"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMetaRefresh(t *testing.T) {
	base, _ := url.Parse("https://example.org/")
	if got := MetaRefresh(fixture(t, "refresh.html"), base); got != "https://example.org/en" {
		t.Fatalf("refresh target %q", got)
	}
	for name, page := range map[string]string{
		"none":     `<html><head><title>x</title></head></html>`,
		"http":     `<html><head><meta http-equiv="refresh" content="0;url=http://example.org/en"></head></html>`,
		"slow":     `<html><head><meta http-equiv="refresh" content="60;url=/en"></head></html>`,
		"self":     `<html><head><meta http-equiv="refresh" content="0;url=https://example.org/"></head></html>`,
		"noscript": `<html><head><noscript><meta http-equiv="refresh" content="0;url=/nojs"></noscript></head></html>`,
		"data":     `<html><head><meta http-equiv="refresh" content="0;url=data:text/html,x"></head></html>`,
	} {
		if got := MetaRefresh([]byte(page), base); got != "" {
			t.Errorf("%s: followed %q", name, got)
		}
	}
	quoted := `<meta HTTP-EQUIV="Refresh" content="1; URL='/q'">`
	if got := MetaRefresh([]byte(quoted), base); got != "https://example.org/q" {
		t.Errorf("quoted: %q", got)
	}
}

func site(t *testing.T) (*Evidence, []string) {
	base, _ := url.Parse("https://example.org/en")
	ev, sheets := ExtractEvidence(fixture(t, "site.html"), base)
	for _, f := range []string{"fonts.css", "site.css"} {
		ev.AddCSS(string(fixture(t, f)))
	}
	return ev, sheets
}

func TestEvidenceColoursAndProperties(t *testing.T) {
	ev, sheets := site(t)
	if !slices.Equal(sheets, []string{"https://example.org/assets/fonts.css", "https://example.org/assets/site.css"}) {
		t.Fatalf("sheets %v", sheets)
	}
	want := map[string]string{ // reference conversions from oklch.py
		"--background": "#FDFBF3", "--foreground": "#202019", "--primary": "#296948", "--secondary": "#F6F3E2",
		"--muted": "#F0EAD2", "--accent": "#C8246B",
	}
	for k, w := range want {
		within(t, k, ev.Semantic[k], w, 1)
	}
	if ev.Semantic["--brand-accent"] == "" || ev.Semantic["--surface-tint"] != "#FFFFFF" {
		t.Fatalf("semantic %v properties %v", ev.Semantic, ev.Properties)
	}
	// Dark-mode overrides never replace the light values.
	within(t, "--primary dark", ev.Semantic["--primary"], "#296948", 1)
	all := mustJSON(ev)
	for _, bad := range []string{"--tw-", "--color-red-500", "--color-blue-600", "63.7%"} {
		if strings.Contains(string(all), bad) {
			t.Fatalf("%q must not reach the evidence: %s", bad, all)
		}
	}
	for _, c := range ev.Colors {
		if c.Hex == "#000000" {
			t.Fatalf("transparent @property initial value counted: %+v", ev.Colors)
		}
	}
	// The brand green (one custom property + one .btn background) outranks every
	// colour that only code-highlighting rules use.
	if len(ev.Colors) == 0 {
		t.Fatal("no colours")
	}
	within(t, "top colour", ev.Colors[0].Hex, "#296948", 1)
	code := map[string]bool{"#C084FC": true, "#1E1E2E": true, "#CDD6F4": true, "#A6E3A1": true, "#FAB387": true, "#6C7086": true}
	pos := map[string]int{}
	for i, c := range ev.Colors {
		pos[c.Hex] = i
		if code[c.Hex] && c.Count > 1 {
			t.Errorf("code colour %s counted %d", c.Hex, c.Count)
		}
	}
	if _, ok := pos["#C8246B"]; !ok {
		t.Errorf("accent colour missing: %+v", ev.Colors)
	}
}

func TestEvidenceFonts(t *testing.T) {
	ev, _ := site(t)
	got := map[string]FontEvidence{}
	for _, f := range ev.Fonts {
		got[f.Family] = f
	}
	for family, closest := range map[string]string{
		"Example Sans Variable": "Inter", "Plex Serif": "Source Serif 4", "Georgia": "Source Serif 4", "Fira Code": "JetBrains Mono",
	} {
		if f, ok := got[family]; !ok || f.Closest != closest {
			t.Errorf("%s: %+v (all %+v)", family, f, ev.Fonts)
		}
	}
	if got["Example Sans Variable"].Source == "" || len(ev.Fonts) != 4 {
		t.Errorf("fonts %+v (face and --font-* duplicates collapse)", ev.Fonts)
	}
	for _, c := range []string{"Inter", "Source Serif 4", "JetBrains Mono", "Noto Sans"} {
		if !slices.Contains(bundled.FontFamilies, c) {
			t.Errorf("ClosestFont may return %q, which is not bundled", c)
		}
	}
	for in, want := range map[string]string{
		"Noto Sans JP": "Noto Sans", "Open Sans": "Noto Sans", "IBM Plex Sans": "Inter", "Noto Serif": "Source Serif 4",
		"Hanken Grotesk Variable": "Inter", "JetBrains Mono": "JetBrains Mono", "Zork": "",
	} {
		if g := ClosestFont(in); g != want {
			t.Errorf("ClosestFont(%q) = %q, want %q", in, g, want)
		}
	}
}

func TestEvidenceLogoOrdering(t *testing.T) {
	ev, _ := site(t)
	mf, _ := url.Parse("https://example.org/site.webmanifest")
	if ev.ManifestURL() != mf.String() {
		t.Fatalf("manifest url %q", ev.ManifestURL())
	}
	ev.AddManifest(fixture(t, "site.webmanifest"), mf)
	if len(ev.Images) < 6 {
		t.Fatalf("images %v", ev.Images)
	}
	svg, ok := InlineSVG(ev.Images[0])
	if !ok || !strings.Contains(string(svg), `viewBox="0 0 20 20"`) || !strings.Contains(string(svg), `xmlns=`) || !strings.Contains(string(svg), "<path") {
		t.Fatalf("first candidate must be the header home-link SVG, got %.80s (%s)", ev.Images[0], svg)
	}
	want := []string{
		"https://example.org/assets/og-card.png",
		"https://example.org/assets/apple-icon.png",
		"https://example.org/assets/icon-512.png",
		"https://example.org/assets/icon-192.png",
		"https://example.org/favicon.ico",
		"https://example.org/assets/promo-logo-banner.png",
	}
	if !slices.Equal(ev.Images[1:], want) {
		t.Fatalf("order %v", ev.Images[1:])
	}
	if ev.ThemeColor != "#296948" || ev.SiteName != "Example Co" {
		t.Fatalf("theme %q site %q", ev.ThemeColor, ev.SiteName)
	}
	// Not a home link: the pricing image in the nav is not a logo candidate.
	if slices.Contains(ev.Images, "https://example.org/assets/icon-pricing.svg") {
		t.Fatal("non-home link image offered as logo")
	}
	// The model gets a label, never the SVG bytes.
	if m := string(mustJSON(ev.ForModel())); strings.Contains(m, "base64") || !strings.Contains(m, "inline SVG logo") {
		t.Fatalf("model view %s", m)
	}
	if !strings.HasPrefix(ev.Images[0], InlineSVGPrefix) {
		t.Fatal("UI view keeps the data URL")
	}
}

func TestEvidenceHomeLinkImageBeatsOgImage(t *testing.T) {
	base, _ := url.Parse("https://example.org/")
	page := `<html><head><meta property="og:image" content="/og.png"></head><body>
<header><a href="https://example.org/"><img src="/brand/mark.svg" alt="Example"></a><a href="/x"><img src="/other-logo.png"></a></header></body></html>`
	ev, _ := ExtractEvidence([]byte(page), base)
	if !slices.Equal(ev.Images, []string{"https://example.org/brand/mark.svg", "https://example.org/og.png", "https://example.org/other-logo.png"}) {
		t.Fatalf("images %v", ev.Images)
	}
	// "logo" in the name only breaks ties between candidates of the same kind.
	page = `<html><body><header><a href="/"><img src="/a.png"><img src="/site-logo.png"></a></header></body></html>`
	ev, _ = ExtractEvidence([]byte(page), base)
	if ev.Images[0] != "https://example.org/site-logo.png" {
		t.Fatalf("tie-breaker: %v", ev.Images)
	}
}

func TestAddManifestIgnoresJunk(t *testing.T) {
	base, _ := url.Parse("https://example.org/")
	ev, _ := ExtractEvidence([]byte(`<html></html>`), base)
	ev.AddManifest([]byte(`not json`), base)
	ev.AddManifest([]byte(`{"icons":[{"src":"http://x.example/a.png"},{"src":"javascript:alert(1)"}]}`), base)
	if len(ev.Images) != 0 {
		t.Fatalf("images %v", ev.Images)
	}
}

func TestAddCSSIsRobust(t *testing.T) {
	ev := &Evidence{}
	ev.AddCSS(`a{color:red;background:url(data:image/png;base64,AAA;BBB)} /* unterminated {{{ ` + strings.Repeat("{", 500) + `}}}} "str {` + `;}`)
	ev.AddCSS(`b{border:1px solid red}.x{box-shadow:0 0 1px rgba(1,2,3,.4)}`)
	if len(ev.Colors) == 0 || ev.Colors[0].Hex != "#FF0000" || ev.Colors[0].Count != 2 {
		t.Fatalf("colours %+v", ev.Colors)
	}
}
