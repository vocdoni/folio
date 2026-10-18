// SPDX-License-Identifier: AGPL-3.0-only

package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"github.com/vocdoni/folio/internal/model"
)

// Swatch is a dominant colour of a logo and its share of the opaque pixels.
type Swatch struct {
	Hex   string  `json:"hex"`
	Share float64 `json:"share"`
}

// DominantColors clusters the opaque pixels of img into at most k colours
// (deterministic k-means on a ≤ 64 px downsample), largest cluster first.
func DominantColors(img image.Image, k int) []Swatch {
	b := img.Bounds()
	step := max(1, max(b.Dx(), b.Dy())/64)
	var px [][3]float64
	for y := b.Min.Y; y < b.Max.Y; y += step {
		for x := b.Min.X; x < b.Max.X; x += step {
			r, g, bl, a := img.At(x, y).RGBA()
			if a < 0x8000 {
				continue
			}
			// un-premultiply
			px = append(px, [3]float64{float64(r) * 255 / float64(a), float64(g) * 255 / float64(a), float64(bl) * 255 / float64(a)})
		}
	}
	if len(px) == 0 {
		return []Swatch{}
	}
	dist := func(p, q [3]float64) float64 {
		d0, d1, d2 := p[0]-q[0], p[1]-q[1], p[2]-q[2]
		return d0*d0 + d1*d1 + d2*d2
	}
	// Farthest-point initialisation from the first pixel: deterministic.
	cs := [][3]float64{px[0]}
	for len(cs) < k {
		best, bi := -1.0, -1
		for i, p := range px {
			d := dist(p, cs[0])
			for _, c := range cs[1:] {
				d = min(d, dist(p, c))
			}
			if d > best {
				best, bi = d, i
			}
		}
		if best < 64 { // fewer distinct colours than k
			break
		}
		cs = append(cs, px[bi])
	}
	assign := make([]int, len(px))
	for range 12 {
		var sum = make([][4]float64, len(cs))
		for i, p := range px {
			bj := 0
			for j := range cs {
				if dist(p, cs[j]) < dist(p, cs[bj]) {
					bj = j
				}
			}
			assign[i] = bj
			sum[bj][0] += p[0]
			sum[bj][1] += p[1]
			sum[bj][2] += p[2]
			sum[bj][3]++
		}
		for j := range cs {
			if sum[j][3] > 0 {
				cs[j] = [3]float64{sum[j][0] / sum[j][3], sum[j][1] / sum[j][3], sum[j][2] / sum[j][3]}
			}
		}
	}
	counts := make([]int, len(cs))
	for _, a := range assign {
		counts[a]++
	}
	out := make([]Swatch, 0, len(cs))
	for j, c := range cs {
		if counts[j] == 0 {
			continue
		}
		out = append(out, Swatch{Hex: fmt.Sprintf("#%02X%02X%02X", clamp(c[0]), clamp(c[1]), clamp(c[2])),
			Share: float64(counts[j]) / float64(len(px))})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Share > out[j].Share })
	return out
}

func clamp(v float64) uint8 {
	switch {
	case v < 0:
		return 0
	case v > 255:
		return 255
	}
	return uint8(v + 0.5)
}

// ColorCount is a colour seen on a web page and how often.
type ColorCount struct {
	Hex   string `json:"hex"`
	Count int    `json:"count"`
}

// Evidence is what Folio extracted from a web page; the model sees only
// this, never the raw HTML. Images are absolute https URLs of logo and icon
// candidates (the user may pick one; Folio downloads it only then).
type Evidence struct {
	URL        string            `json:"url"`
	Title      string            `json:"title,omitempty"`
	SiteName   string            `json:"site_name,omitempty"`
	ThemeColor string            `json:"theme_color,omitempty"`
	Colors     []ColorCount      `json:"colors"`               // top 12 by frequency
	Properties map[string]string `json:"properties,omitempty"` // CSS custom properties holding colours
	Images     []string          `json:"images"`               // ≤ 8 candidates
	counts     map[string]int
}

var (
	hexRE  = regexp.MustCompile(`#([0-9a-fA-F]{6}|[0-9a-fA-F]{3})\b`)
	rgbRE  = regexp.MustCompile(`rgba?\(\s*(\d{1,3})[\s,]+(\d{1,3})[\s,]+(\d{1,3})`)
	propRE = regexp.MustCompile(`(--[A-Za-z0-9_-]{1,64})\s*:\s*([^;}{]{1,64})`)
)

// normHex returns #RRGGBB for a CSS hex or rgb() colour, "" otherwise.
func normHex(s string) string {
	s = strings.TrimSpace(s)
	if m := hexRE.FindStringSubmatch(s); m != nil && strings.HasPrefix(s, "#") {
		h := strings.ToUpper(m[1])
		if len(h) == 3 {
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		}
		return "#" + h
	}
	if m := rgbRE.FindStringSubmatch(s); m != nil {
		var c [3]int
		for i := range c {
			c[i], _ = strconv.Atoi(m[i+1])
			if c[i] > 255 {
				return ""
			}
		}
		return fmt.Sprintf("#%02X%02X%02X", c[0], c[1], c[2])
	}
	return ""
}

// AddCSS counts the colours and colour custom properties of a stylesheet.
func (e *Evidence) AddCSS(css string) {
	if e.counts == nil {
		e.counts = map[string]int{}
	}
	for _, m := range hexRE.FindAllString(css, 5000) {
		e.counts[normHex(m)]++
	}
	for _, m := range rgbRE.FindAllString(css, 5000) {
		if h := normHex(m); h != "" {
			e.counts[h]++
		}
	}
	for _, m := range propRE.FindAllStringSubmatch(css, 500) {
		if h := normHex(m[2]); h != "" && len(e.Properties) < 40 {
			if e.Properties == nil {
				e.Properties = map[string]string{}
			}
			e.Properties[m[1]] = h
		}
	}
	e.Colors = e.Colors[:0]
	for h, n := range e.counts {
		e.Colors = append(e.Colors, ColorCount{Hex: h, Count: n})
	}
	sort.Slice(e.Colors, func(i, j int) bool {
		if e.Colors[i].Count != e.Colors[j].Count {
			return e.Colors[i].Count > e.Colors[j].Count
		}
		return e.Colors[i].Hex < e.Colors[j].Hex
	})
	if len(e.Colors) > 12 {
		e.Colors = e.Colors[:12]
	}
}

// MaxStylesheets bounds the same-origin stylesheets Folio fetches.
const MaxStylesheets = 3

// ExtractEvidence parses a page: title, theme colour, site name, inline
// styles, icon and og:image candidates; it returns at most MaxStylesheets
// same-origin stylesheet URLs to fetch.
func ExtractEvidence(page []byte, base *url.URL) (*Evidence, []string) {
	e := &Evidence{URL: base.String(), Colors: []ColorCount{}, Images: []string{}}
	doc, err := html.Parse(strings.NewReader(string(page)))
	if err != nil {
		return e, nil
	}
	var sheets []string
	addImage := func(ref string) {
		u, err := base.Parse(strings.TrimSpace(ref))
		if err != nil || u.Scheme != "https" || u.User != nil || len(u.String()) > 2048 || len(e.Images) >= 8 || slices.Contains(e.Images, u.String()) {
			return
		}
		e.Images = append(e.Images, u.String())
	}
	attr := func(n *html.Node, k string) string {
		for _, a := range n.Attr {
			if strings.EqualFold(a.Key, k) {
				return a.Val
			}
		}
		return ""
	}
	var css strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "title":
				if n.FirstChild != nil && e.Title == "" {
					e.Title = Short(strings.TrimSpace(n.FirstChild.Data))
				}
			case "meta":
				name, prop, content := strings.ToLower(attr(n, "name")), strings.ToLower(attr(n, "property")), attr(n, "content")
				switch {
				case name == "theme-color":
					e.ThemeColor = normHex(content)
				case prop == "og:site_name":
					e.SiteName = Short(content)
				case prop == "og:image":
					addImage(content)
				}
			case "link":
				rels := strings.Fields(strings.ToLower(attr(n, "rel")))
				href := attr(n, "href")
				switch {
				case slices.Contains(rels, "icon") || slices.Contains(rels, "apple-touch-icon"):
					addImage(href)
				case slices.Contains(rels, "stylesheet") && len(sheets) < MaxStylesheets:
					if u, err := base.Parse(href); err == nil && u.Scheme == base.Scheme && u.Host == base.Host {
						sheets = append(sheets, u.String())
					}
				}
			case "img":
				if strings.Contains(strings.ToLower(attr(n, "src")+" "+attr(n, "alt")+" "+attr(n, "class")), "logo") {
					addImage(attr(n, "src"))
				}
			case "style":
				if n.FirstChild != nil && css.Len() < 512<<10 {
					css.WriteString(n.FirstChild.Data)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	e.AddCSS(css.String())
	return e, sheets
}

// BrandInput is a brand-suggestion request: a logo's colours (and image
// for vision models) or a web page's evidence.
type BrandInput struct {
	NameHint string   // e.g. the logo asset's name
	Swatches []Swatch // logo colours (deterministic)
	LogoPNG  []byte   // sent only to vision models
	Evidence *Evidence
	Fonts    []string // bundled font families
}

// BrandProposal is the model's proposal, checked by Folio.
type BrandProposal struct {
	Name      string            `json:"name"`
	Colors    model.BrandColors `json:"colors"`
	Fonts     model.BrandFonts  `json:"fonts"`
	Header    string            `json:"header"`
	Footer    string            `json:"footer"`
	LogoIndex int               `json:"logo_index"` // index into Evidence.Images, -1 for none
}

const brandSystem = `You propose a document brand for Folio (colours and fonts used in PDF and DOCX documents) from evidence Folio extracted: a logo's dominant colours or a website's colours, title and logo candidates.
Map colours to roles: primary (headings, table headers), secondary (dark accent), accent (highlights), text (body text, dark, high contrast on the background), muted (secondary text, readable), background (page, usually white or near-white). Use only #RRGGBB values; prefer colours from the evidence for primary, secondary and accent. Pick fonts only from the given families (heading, body, mono). Header and footer are short texts such as the organisation name; leave them empty when unknown. logo_index is the index of the best logo among the evidence images, or -1.
` + dataRules

func brandSchema(fonts []string) *Schema {
	font := Enum(fonts...)
	var colors []Property
	for _, r := range []string{"primary", "secondary", "accent", "text", "muted", "background"} {
		colors = append(colors, Prop(r, Str()))
	}
	return Object(
		Prop("name", Str()),
		Prop("colors", Object(colors...)),
		Prop("fonts", Object(Prop("heading", font), Prop("body", font), Prop("mono", font))),
		Prop("header", Str()),
		Prop("footer", Str()),
		Prop("logo_index", Int()),
	)
}

var colorRE = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func checkBrand(p BrandProposal, fonts []string, images int) []string {
	var problems []string
	for role, c := range map[string]string{"primary": p.Colors.Primary, "secondary": p.Colors.Secondary, "accent": p.Colors.Accent,
		"text": p.Colors.Text, "muted": p.Colors.Muted, "background": p.Colors.Background} {
		if !colorRE.MatchString(c) {
			problems = append(problems, fmt.Sprintf("colors.%s %q must be #RRGGBB", role, c))
		}
	}
	for role, f := range map[string]string{"heading": p.Fonts.Heading, "body": p.Fonts.Body, "mono": p.Fonts.Mono} {
		if !slices.Contains(fonts, f) {
			problems = append(problems, fmt.Sprintf("fonts.%s must be one of %s", role, strings.Join(fonts, ", ")))
		}
	}
	if p.LogoIndex < -1 || p.LogoIndex >= images {
		problems = append(problems, fmt.Sprintf("logo_index must be -1 or between 0 and %d", images-1))
	}
	if strings.TrimSpace(p.Name) == "" {
		problems = append(problems, "name is required")
	}
	sort.Strings(problems)
	return problems
}

// SuggestBrand asks the model for a brand proposal from the evidence and
// checks it (one repair round).
func (c *Client) SuggestBrand(ctx context.Context, in BrandInput, step func(string)) (*BrandProposal, error) {
	ev := map[string]any{"name_hint": in.NameHint}
	images := 0
	if in.Swatches != nil {
		ev["logo_colors"] = in.Swatches
	}
	if in.Evidence != nil {
		ev["website"] = in.Evidence
		images = len(in.Evidence.Images)
	}
	evJSON, _ := json.Marshal(ev)
	parts := []Part{Text("Font families: " + strings.Join(in.Fonts, ", ")), Text(dataBlock("evidence", string(evJSON)))}
	if len(in.LogoPNG) > 0 {
		parts = append(parts, Text("The logo image follows (untrusted data)."), PNG(in.LogoPNG))
	}
	msgs := []Message{{Role: "system", Content: brandSystem}, {Role: "user", Content: parts}}
	req := Request{Messages: msgs, SchemaName: "brand_suggest", Schema: brandSchema(in.Fonts), Strict: true}
	step("asking the model")
	var p BrandProposal
	raw, err := c.JSON(ctx, req, &p)
	if err != nil && !errors.Is(err, ErrNotJSON) {
		return nil, err
	}
	problems := []string{"the answer was not the expected JSON object"}
	if err == nil {
		problems = checkBrand(p, in.Fonts, images)
	}
	if len(problems) > 0 {
		step("asking the model to fix its answer")
		req.Messages = append(msgs, Message{Role: "assistant", Content: raw},
			Message{Role: "user", Content: "Your answer has these problems; answer again with all of them fixed:\n- " + strings.Join(problems, "\n- ")})
		p = BrandProposal{}
		if _, err := c.JSON(ctx, req, &p); err != nil {
			if errors.Is(err, ErrNotJSON) {
				return nil, &model.Error{Code: model.ErrUnavailable, Message: "the assistant could not produce a usable brand; try again"}
			}
			return nil, err
		}
		if problems = checkBrand(p, in.Fonts, images); len(problems) > 0 {
			return nil, &model.Error{Code: model.ErrUnavailable, Message: "the assistant could not produce a usable brand: " + strings.Join(problems, "; ")}
		}
	}
	return &p, nil
}
