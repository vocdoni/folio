// SPDX-License-Identifier: AGPL-3.0-only

package ai

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// ColorCount is a colour seen on a web page and how often (code-highlighting
// rules count a fifth).
type ColorCount struct {
	Hex   string `json:"hex"`
	Count int    `json:"count"`
}

// FontEvidence is a font family a page declares (in @font-face or a --font-*
// variable), as written, and the bundled font closest to it.
type FontEvidence struct {
	Family  string `json:"family"`
	Source  string `json:"source"`
	Closest string `json:"closest,omitempty"`
}

// Evidence is what Folio extracted from a web page; the model sees only
// this, never the raw HTML. Images are logo and icon candidates, best first:
// absolute https URLs or, for an SVG written inline in the page header, a
// data: URL (see InlineSVG). The user may pick one; Folio downloads or
// sanitises and stores it only then.
type Evidence struct {
	URL        string            `json:"url"`
	Title      string            `json:"title,omitempty"`
	SiteName   string            `json:"site_name,omitempty"`
	ThemeColor string            `json:"theme_color,omitempty"`
	Colors     []ColorCount      `json:"colors"`               // top 12 by frequency
	Semantic   map[string]string `json:"semantic,omitempty"`   // --primary, --background, ... colours
	Properties map[string]string `json:"properties,omitempty"` // other CSS custom properties holding colours
	Fonts      []FontEvidence    `json:"fonts,omitempty"`
	Images     []string          `json:"images"` // ≤ 8 candidates

	weights  map[string]float64
	header   []headerCand
	og       []string
	apple    []string
	manifest []string
	icons    []string
	logoImgs []string
	manifURL string
}

type headerCand struct {
	ref  string
	hint bool // "logo" appears in its src, alt or class: tie-breaker only
}

const (
	maxImages    = 8
	maxInlineSVG = 48 << 10
	// InlineSVGPrefix starts the data: URL Folio uses for an inline SVG logo.
	InlineSVGPrefix = "data:image/svg+xml;base64,"
	// MaxManifest bounds the web app manifest Folio reads.
	MaxManifest = 64 << 10
)

// InlineSVG returns the SVG source of an inline logo candidate.
func InlineSVG(ref string) ([]byte, bool) {
	b64, ok := strings.CutPrefix(ref, InlineSVGPrefix)
	if !ok {
		return nil, false
	}
	b, err := base64.StdEncoding.DecodeString(b64)
	return b, err == nil && len(b) > 0 && len(b) <= maxInlineSVG
}

// ForModel returns the evidence as the model sees it: an inline SVG logo is
// replaced by a short label (the model only picks an index).
func (e *Evidence) ForModel() *Evidence {
	c := *e
	c.Images = make([]string, len(e.Images))
	for i, im := range e.Images {
		if strings.HasPrefix(im, "data:") {
			im = "(inline SVG logo from the page header)"
		}
		c.Images[i] = im
	}
	return &c
}

var (
	propRE = regexp.MustCompile(`^(?:color-)?(?:primary|secondary|accent|background|foreground|muted|text|brand|bg|fg|surface|link)(?:[-_a-z0-9]*)$`)
	// scaleRE matches Tailwind palette scales: --color-red-500, --blue-50.
	scaleRE   = regexp.MustCompile(`^(?:color-)?(?:slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)-(?:50|[1-9]00|950)$`)
	localeRE  = regexp.MustCompile(`^[a-z]{2}(?:[-_][a-zA-Z]{2,4})?$`)
	bareHSLRE = regexp.MustCompile(`^(\d+(?:\.\d+)?)(?:deg)?[ ,]+(\d+(?:\.\d+)?)%[ ,]+(\d+(?:\.\d+)?)%$`)
	codeSelRE = regexp.MustCompile(`(?i)(^|[\s>+~])(pre|code|samp|kbd)($|[\s.:#\[>+~,])|\.(hljs|shiki|token|prism|chroma|highlight|cm-|pl-|sh-|astro-code)`)
)

func isCodeSelector(sel string) bool {
	for p := range strings.SplitSeq(sel, ",") {
		if !codeSelRE.MatchString(strings.TrimSpace(p)) {
			return false
		}
	}
	return strings.TrimSpace(sel) != ""
}

// normHex returns #RRGGBB for any CSS colour, "" otherwise.
func normHex(s string) string {
	h, _ := ParseColor(s)
	return h
}

// Limits for AddCSS.
const (
	maxDecls     = 100000
	maxProps     = 40
	maxFonts     = 8
	codeWeight   = 0.2
	maxSelectorN = 200
)

// AddCSS counts the colours and reads the colour custom properties and font
// families of a stylesheet.
func (e *Evidence) AddCSS(css string) {
	if e.weights == nil {
		e.weights = map[string]float64{}
	}
	type frame struct {
		sel  string
		skip bool // @property, @keyframes, @font-face: no colour counting
		dark bool
		code bool
		face bool
	}
	var stack []frame
	var cur strings.Builder
	depth, decls := 0, 0
	top := func() (f frame) {
		for _, s := range stack {
			f.skip = f.skip || s.skip
			f.dark = f.dark || s.dark
			f.code = f.code || s.code
			f.face = f.face || s.face
		}
		return f
	}
	decl := func(text string) {
		text = strings.TrimSpace(text)
		i := strings.IndexByte(text, ':')
		if i <= 0 || text[0] == '@' || decls >= maxDecls {
			return
		}
		decls++
		prop, val := strings.ToLower(strings.TrimSpace(text[:i])), strings.TrimSpace(text[i+1:])
		if len(val) > 300 {
			return
		}
		f := top()
		switch {
		case f.face:
			if prop == "font-family" {
				e.addFont(firstFamily(val, true), "font-face")
			}
			return
		case f.skip:
			return
		case strings.HasPrefix(prop, "--"):
			e.addProperty(prop[2:], val, f.dark, f.code)
		case strings.Contains(prop, "color") || prop == "background" || prop == "fill" || prop == "stroke" || prop == "box-shadow" ||
			strings.HasPrefix(prop, "border") || strings.HasPrefix(prop, "outline"):
			e.count(FindColors(val, prop != "box-shadow"), f.code)
		}
	}
	for i := 0; i < len(css); i++ {
		c := css[i]
		switch {
		case c == '/' && i+1 < len(css) && css[i+1] == '*':
			j := strings.Index(css[i+2:], "*/")
			if j < 0 {
				i = len(css)
			} else {
				i += j + 3
			}
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(css) && css[j] != c && css[j] != '\n' {
				if css[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j, len(css)-1)
			cur.WriteString(css[i : j+1])
			i = j
		case c == '(':
			depth++
			cur.WriteByte(c)
		case c == ')':
			depth = max(0, depth-1)
			cur.WriteByte(c)
		case depth == 0 && c == '{':
			sel := strings.TrimSpace(cur.String())
			low := strings.ToLower(sel)
			fr := frame{sel: sel[:min(len(sel), maxSelectorN)], dark: strings.Contains(low, "dark")}
			switch {
			case strings.HasPrefix(low, "@font-face"):
				fr.face = true
			case strings.HasPrefix(low, "@property"), strings.HasPrefix(low, "@keyframes"), strings.HasPrefix(low, "@-webkit-keyframes"):
				fr.skip = true
			case !strings.HasPrefix(low, "@"):
				fr.code = isCodeSelector(sel)
			}
			stack = append(stack, fr)
			cur.Reset()
		case depth == 0 && c == ';':
			decl(cur.String())
			cur.Reset()
		case depth == 0 && c == '}':
			decl(cur.String())
			cur.Reset()
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		default:
			cur.WriteByte(c)
		}
		if len(stack) > 64 {
			break
		}
	}
	e.rankColors()
}

func (e *Evidence) count(hexes []string, code bool) {
	w := 1.0
	if code {
		w = codeWeight
	}
	for _, h := range hexes {
		e.weights[h] += w
	}
}

func (e *Evidence) rankColors() {
	e.Colors = e.Colors[:0]
	for h, w := range e.weights {
		e.Colors = append(e.Colors, ColorCount{Hex: h, Count: int(math.Ceil(w - 1e-9))})
	}
	sort.Slice(e.Colors, func(i, j int) bool {
		wi, wj := e.weights[e.Colors[i].Hex], e.weights[e.Colors[j].Hex]
		if wi != wj {
			return wi > wj
		}
		return e.Colors[i].Hex < e.Colors[j].Hex
	})
	if len(e.Colors) > 12 {
		e.Colors = e.Colors[:12]
	}
}

// addProperty records a custom property: fonts for --font-*, colours for the
// rest. Tailwind internals (--tw-*) and palette scales are dropped.
func (e *Evidence) addProperty(name, val string, dark, code bool) {
	switch {
	case strings.HasPrefix(name, "tw-"), scaleRE.MatchString(name):
		return
	case strings.HasPrefix(name, "font-") || strings.HasPrefix(name, "font_") || name == "font":
		e.addFont(firstFamily(val, false), "--"+name)
		return
	case len(name) > 64:
		return
	}
	val = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(val), "!important"))
	semantic := propRE.MatchString(name)
	hex, ok := ParseColor(val)
	if !ok && semantic {
		if m := bareHSLRE.FindStringSubmatch(val); m != nil { // shadcn-style "222 47% 11%"
			hex, ok = ParseColor("hsl(" + m[1] + " " + m[2] + "% " + m[3] + "%)")
		}
	}
	if !ok {
		e.count(FindColors(val, false), code)
		return
	}
	e.count([]string{hex}, code)
	if dark || code {
		return // light-mode values describe the brand; code themes are not the brand
	}
	dst := &e.Properties
	if semantic {
		dst = &e.Semantic
	}
	if *dst == nil {
		*dst = map[string]string{}
	}
	if _, seen := (*dst)["--"+name]; !seen && len(*dst) < maxProps {
		(*dst)["--"+name] = hex
	}
}

var genericFamilies = map[string]bool{
	"serif": true, "sans-serif": true, "monospace": true, "cursive": true, "fantasy": true, "system-ui": true,
	"ui-serif": true, "ui-sans-serif": true, "ui-monospace": true, "ui-rounded": true, "emoji": true, "math": true,
	"fangsong": true, "inherit": true, "initial": true, "unset": true, "-apple-system": true, "blinkmacsystemfont": true,
	"segoe ui": true, "helvetica": true, "helvetica neue": true, "arial": true, "apple color emoji": true,
	"segoe ui emoji": true, "segoe ui symbol": true, "noto color emoji": true,
}

// firstFamily returns the first named (non-generic) family of a
// font-family value; single is true when the value is one family.
func firstFamily(val string, single bool) string {
	for p := range strings.SplitSeq(val, ",") {
		p = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(p), "!important"))
		p = strings.Trim(p, `"'`)
		if p == "" || strings.HasPrefix(strings.ToLower(p), "var(") || genericFamilies[strings.ToLower(p)] || len(p) > 60 {
			if single {
				return ""
			}
			continue
		}
		return p
	}
	return ""
}

func (e *Evidence) addFont(family, source string) {
	family = strings.TrimSpace(family)
	if family == "" || len(e.Fonts) >= maxFonts {
		return
	}
	for _, f := range e.Fonts {
		if strings.EqualFold(f.Family, family) {
			return
		}
	}
	e.Fonts = append(e.Fonts, FontEvidence{Family: family, Source: source, Closest: ClosestFont(family)})
}

// Bundled font family names ClosestFont can return.
const (
	fontSans  = "Inter"
	fontSerif = "Source Serif 4"
	fontMono  = "JetBrains Mono"
	fontNoto  = "Noto Sans"
)

var (
	monoHints  = []string{"mono", "code", "courier", "consolas", "menlo", "monaco", "cascadia", "hack", "inconsolata", "typewriter"}
	serifNames = []string{"georgia", "times", "garamond", "merriweather", "playfair", "lora", "baskerville", "cambria", "palatino", "crimson",
		"charter", "literata", "spectral", "cormorant", "bitter", "domine", "fraunces", "newsreader", "serif"}
	notoNames = []string{"noto", "open sans", "droid sans"}
	sansNames = []string{"sans", "inter", "hanken", "roboto", "helvetica", "arial", "segoe", "poppins", "montserrat", "lato", "nunito",
		"manrope", "figtree", "geist", "outfit", "jakarta", "avenir", "gotham", "proxima", "rubik", "karla", "mulish", "urbanist",
		"lexend", "sora", "grotesk", "grotesque", "sf pro", "work sans", "dm sans", "raleway", "ubuntu", "barlow", "heebo"}
)

// ClosestFont maps a font family to the closest bundled font by name and
// style, or "" when nothing about the name says what it looks like.
func ClosestFont(family string) string {
	f := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(family), " Variable"))
	f = strings.TrimSuffix(f, " variable")
	has := func(list []string) bool {
		return slices.ContainsFunc(list, func(s string) bool { return strings.Contains(f, s) })
	}
	switch {
	case has(monoHints):
		return fontMono
	case strings.Contains(f, "sans") && !strings.Contains(f, "noto") && !has([]string{"open sans", "droid sans"}):
		return fontSans
	case has(notoNames) && !strings.Contains(f, "serif"):
		return fontNoto
	case has(serifNames):
		return fontSerif
	case has(sansNames):
		return fontSans
	}
	return ""
}

// MaxStylesheets bounds the same-origin stylesheets Folio fetches.
const MaxStylesheets = 3

func attr(n *html.Node, k string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, k) {
			return a.Val
		}
	}
	return ""
}

// validImage resolves ref against base and returns it when it is a usable
// https candidate.
func validImage(base *url.URL, ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", false
	}
	u, err := base.Parse(ref)
	if err != nil || u.Scheme != "https" || u.User != nil || len(u.String()) > 2048 {
		return "", false
	}
	return u.String(), true
}

// isHomeLink reports whether href points at the site's home page (the root,
// a short locale path such as /en, or the page itself).
func isHomeLink(base *url.URL, href string) bool {
	href = strings.TrimSpace(href)
	if href == "" {
		return false
	}
	u, err := base.Parse(href)
	if err != nil || u.Host != base.Host || u.RawQuery != "" {
		return false
	}
	p := strings.Trim(u.Path, "/")
	if p == "" || localeRE.MatchString(p) {
		return true
	}
	return p == strings.Trim(base.Path, "/")
}

func hasToken(n *html.Node, k, tok string) bool {
	return slices.Contains(strings.Fields(strings.ToLower(attr(n, k))), tok)
}

// ExtractEvidence parses a page: title, theme colour, site name, inline
// styles, icon and logo candidates; it returns at most MaxStylesheets
// same-origin stylesheet URLs to fetch. ManifestURL then names the web app
// manifest, if any.
func ExtractEvidence(page []byte, base *url.URL) (*Evidence, []string) {
	e := &Evidence{URL: base.String(), Colors: []ColorCount{}, Images: []string{}}
	doc, err := html.Parse(bytes.NewReader(page))
	if err != nil {
		return e, nil
	}
	var sheets []string
	var css strings.Builder
	add := func(list *[]string, ref string) {
		if u, ok := validImage(base, ref); ok && len(*list) < maxImages && !slices.Contains(*list, u) {
			*list = append(*list, u)
		}
	}
	addHeader := func(ref string, hint bool) {
		if len(e.header) < 4 && !slices.ContainsFunc(e.header, func(c headerCand) bool { return c.ref == ref }) {
			e.header = append(e.header, headerCand{ref, hint})
		}
	}
	// homeLogos adds the images and inline SVGs inside a home link.
	var homeLogos func(n *html.Node)
	homeLogos = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			switch c.Data {
			case "img":
				src := attr(c, "src")
				hint := strings.Contains(strings.ToLower(src+" "+attr(c, "alt")+" "+attr(c, "class")), "logo")
				if u, ok := validImage(base, src); ok {
					addHeader(u, hint)
				}
			case "svg":
				if ref, ok := inlineSVG(c); ok {
					addHeader(ref, strings.Contains(strings.ToLower(attr(c, "class")+attr(n, "aria-label")), "logo"))
				}
			default:
				homeLogos(c)
			}
		}
	}
	var walk func(n *html.Node, inHeader bool)
	walk = func(n *html.Node, inHeader bool) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "header", "nav":
				inHeader = true
			case "title":
				if n.FirstChild != nil && e.Title == "" {
					e.Title = Short(strings.TrimSpace(n.FirstChild.Data))
				}
			case "meta":
				name, prop, content := strings.ToLower(attr(n, "name")), strings.ToLower(attr(n, "property")), attr(n, "content")
				switch {
				case name == "theme-color" && e.ThemeColor == "":
					e.ThemeColor = normHex(content)
				case prop == "og:site_name":
					e.SiteName = Short(content)
				case prop == "og:image" || prop == "og:image:url" || prop == "og:image:secure_url":
					add(&e.og, content)
				}
			case "link":
				href := attr(n, "href")
				switch {
				case hasToken(n, "rel", "apple-touch-icon") || hasToken(n, "rel", "apple-touch-icon-precomposed"):
					add(&e.apple, href)
				case hasToken(n, "rel", "icon") || hasToken(n, "rel", "shortcut"):
					add(&e.icons, href)
				case hasToken(n, "rel", "manifest") && e.manifURL == "":
					if u, err := base.Parse(href); err == nil && u.Scheme == "https" && u.User == nil {
						e.manifURL = u.String()
					}
				case hasToken(n, "rel", "stylesheet") && len(sheets) < MaxStylesheets:
					if u, err := base.Parse(href); err == nil && u.Scheme == base.Scheme && u.Host == base.Host {
						sheets = append(sheets, u.String())
					}
				}
			case "a":
				if inHeader && isHomeLink(base, attr(n, "href")) {
					homeLogos(n)
				}
			case "img":
				if strings.Contains(strings.ToLower(attr(n, "src")+" "+attr(n, "alt")+" "+attr(n, "class")), "logo") {
					add(&e.logoImgs, attr(n, "src"))
				}
			case "style":
				if n.FirstChild != nil && css.Len() < 512<<10 {
					css.WriteString(n.FirstChild.Data)
				}
			case "svg": // never look inside inline SVGs
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inHeader)
		}
	}
	walk(doc, false)
	e.AddCSS(css.String())
	e.rebuildImages()
	return e, sheets
}

// inlineSVG serialises an inline <svg> element as a data: URL candidate.
func inlineSVG(n *html.Node) (string, bool) {
	if n.FirstChild == nil {
		return "", false
	}
	if !slices.ContainsFunc(n.Attr, func(a html.Attribute) bool { return a.Key == "xmlns" }) {
		n.Attr = append(n.Attr, html.Attribute{Key: "xmlns", Val: "http://www.w3.org/2000/svg"})
	}
	var buf bytes.Buffer
	if err := html.Render(&buf, n); err != nil || buf.Len() > maxInlineSVG {
		return "", false
	}
	return InlineSVGPrefix + base64.StdEncoding.EncodeToString(buf.Bytes()), true
}

// rebuildImages orders the candidates: header home-link logos, og:image,
// apple-touch-icon, manifest icons, favicons, then any other image whose
// name says "logo". A "logo" hint only orders candidates of the same kind.
func (e *Evidence) rebuildImages() {
	hc := slices.Clone(e.header)
	sort.SliceStable(hc, func(i, j int) bool { return hc[i].hint && !hc[j].hint })
	var all []string
	for _, c := range hc {
		all = append(all, c.ref)
	}
	for _, l := range [][]string{e.og, e.apple, e.manifest, e.icons, e.logoImgs} {
		all = append(all, l...)
	}
	e.Images = []string{}
	for _, u := range all {
		if len(e.Images) < maxImages && !slices.Contains(e.Images, u) {
			e.Images = append(e.Images, u)
		}
	}
}

// ManifestURL is the web app manifest the page links, or "".
func (e *Evidence) ManifestURL() string { return e.manifURL }

// AddManifest reads a web app manifest (fetched from at): its icons become
// logo candidates (largest first) and its theme colour fills a missing one.
func (e *Evidence) AddManifest(data []byte, at *url.URL) {
	if len(data) > MaxManifest {
		return
	}
	var m struct {
		Name       string `json:"name"`
		ShortName  string `json:"short_name"`
		ThemeColor string `json:"theme_color"`
		Icons      []struct {
			Src     string `json:"src"`
			Sizes   string `json:"sizes"`
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}
	if json.Unmarshal(data, &m) != nil {
		return
	}
	if e.SiteName == "" {
		e.SiteName = Short(cmpOr(m.Name, m.ShortName))
	}
	if e.ThemeColor == "" {
		e.ThemeColor = normHex(m.ThemeColor)
	}
	size := func(s string) int {
		best := 0
		for f := range strings.FieldsSeq(s) {
			w, _, _ := strings.Cut(strings.ToLower(f), "x")
			n, _ := strconv.Atoi(w)
			best = max(best, n)
		}
		return best
	}
	icons := m.Icons
	sort.SliceStable(icons, func(i, j int) bool { return size(icons[i].Sizes) > size(icons[j].Sizes) })
	for _, ic := range icons {
		if strings.Contains(ic.Purpose, "monochrome") || len(e.manifest) >= 3 {
			continue
		}
		if u, ok := validImage(at, ic.Src); ok && !slices.Contains(e.manifest, u) {
			e.manifest = append(e.manifest, u)
		}
	}
	e.rebuildImages()
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

var refreshRE = regexp.MustCompile(`(?i)^\s*(\d+(?:\.\d+)?)\s*[;,]?\s*(?:url\s*=\s*)?['"]?([^'"]*?)['"]?\s*$`)

// MetaRefresh returns the https target of a <meta http-equiv="refresh"> in
// the page (outside <noscript>), resolved against base, or "". Delays over
// ten seconds are not redirects worth following.
func MetaRefresh(page []byte, base *url.URL) string {
	doc, err := html.Parse(bytes.NewReader(page))
	if err != nil {
		return ""
	}
	var target string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if target != "" || (n.Type == html.ElementNode && (n.Data == "noscript" || n.Data == "body")) {
			return
		}
		if n.Type == html.ElementNode && n.Data == "meta" && strings.EqualFold(strings.TrimSpace(attr(n, "http-equiv")), "refresh") {
			if m := refreshRE.FindStringSubmatch(attr(n, "content")); m != nil && m[2] != "" {
				if secs, _ := strconv.ParseFloat(m[1], 64); secs <= 10 {
					if u, err := base.Parse(strings.TrimSpace(m[2])); err == nil && u.Scheme == "https" && u.User == nil && u.String() != base.String() {
						target = u.String()
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return target
}
