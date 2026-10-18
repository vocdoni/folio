// SPDX-License-Identifier: AGPL-3.0-only

package ai

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// colorFuncRE matches the CSS colour functions Folio understands. The
// argument list holds no nested parentheses (calc() and var() are skipped).
var colorFuncRE = regexp.MustCompile(`(?i)\b(rgba?|hsla?|hwb|oklch|oklab|lab|lch)\(([^()]{1,100})\)`)

var colorHexRE = regexp.MustCompile(`(?i)#[0-9a-f]{3,8}\b`)

// ParseColor converts one CSS colour (hex, rgb, hsl, hwb, oklch, oklab, lab,
// lch or a named colour) to #RRGGBB. Alpha is ignored. It returns false for
// anything else (var(), currentcolor, transparent, relative colours, ...).
func ParseColor(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSpace(strings.TrimSuffix(s, "!important"))
	if s == "" {
		return "", false
	}
	if s[0] == '#' {
		if colorHexRE.FindString(s) != s {
			return "", false
		}
		return hexToRGB(s[1:])
	}
	if m := colorFuncRE.FindStringSubmatch(s); m != nil && m[0] == s {
		return parseColorFunc(m[1], m[2])
	}
	if h, ok := namedColors[s]; ok {
		return "#" + strings.ToUpper(h), true
	}
	return "", false
}

// FindColors returns the colours inside a CSS declaration value in order of
// appearance. Named colours count only when named is set (colour-valued
// properties); var() references and values with url() are ignored.
func FindColors(value string, named bool) []string {
	low := strings.ToLower(value)
	if strings.Contains(low, "url(") {
		return nil
	}
	type hit struct {
		pos int
		hex string
	}
	var hits []hit
	for _, re := range []*regexp.Regexp{colorHexRE, colorFuncRE} {
		for _, loc := range re.FindAllStringIndex(low, 20) {
			if h, ok := ParseColor(low[loc[0]:loc[1]]); ok {
				hits = append(hits, hit{loc[0], h})
			}
		}
	}
	if named {
		rest := colorFuncRE.ReplaceAllStringFunc(low, func(m string) string { return strings.Repeat(" ", len(m)) })
		pos := 0
		for _, w := range strings.FieldsFunc(rest, func(r rune) bool { return r < 'a' || r > 'z' }) {
			i := strings.Index(rest[pos:], w) + pos
			pos = i + len(w)
			if h, ok := namedColors[w]; ok && (i == 0 || rest[i-1] != '-') && (pos >= len(rest) || rest[pos] != '-') {
				hits = append(hits, hit{i, "#" + strings.ToUpper(h)})
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].pos < hits[j].pos })
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.hex)
	}
	return out
}

func hexToRGB(h string) (string, bool) {
	if (len(h) == 4 && h[3] == '0') || (len(h) == 8 && h[6:] == "00") {
		return "", false // fully transparent: not a colour of the brand
	}
	switch len(h) {
	case 3, 4:
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	case 6, 8:
		h = h[:6]
	default:
		return "", false
	}
	return "#" + strings.ToUpper(h), true
}

func parseColorFunc(fn, args string) (string, bool) {
	if i := strings.IndexByte(args, '/'); i >= 0 { // drop alpha
		args = args[:i]
	}
	fields := strings.FieldsFunc(args, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
	if len(fields) == 4 && (fn == "rgba" || fn == "hsla") {
		fields = fields[:3]
	}
	if len(fields) != 3 {
		return "", false
	}
	num := func(i int, pctScale float64, angle bool) (float64, bool) {
		return cssNumber(fields[i], pctScale, angle)
	}
	var r, g, b float64
	switch fn {
	case "rgb", "rgba":
		var c [3]float64
		for i := range c {
			x, ok := num(i, 255, false)
			if !ok {
				return "", false
			}
			c[i] = x / 255
		}
		r, g, b = c[0], c[1], c[2]
	case "hsl", "hsla", "hwb":
		h, ok1 := num(0, 0, true)
		x, ok2 := cssPercent(fields[1])
		y, ok3 := cssPercent(fields[2])
		if !ok1 || !ok2 || !ok3 {
			return "", false
		}
		switch {
		case fn != "hwb":
			r, g, b = hslToRGB(h, x, y)
		case x+y >= 1:
			gray := x / (x + y)
			r, g, b = gray, gray, gray
		default:
			r, g, b = hslToRGB(h, 1, 0.5)
			r, g, b = r*(1-x-y)+x, g*(1-x-y)+x, b*(1-x-y)+x
		}
	case "oklch", "oklab", "lab", "lch":
		lScale, abScale, cScale := 1.0, 0.4, 0.4
		if fn == "lab" || fn == "lch" {
			lScale, abScale, cScale = 100, 125, 150
		}
		L, ok1 := num(0, lScale, false)
		var a, bb float64
		var ok2, ok3 bool
		if fn == "oklch" || fn == "lch" {
			var c, h float64
			c, ok2 = num(1, cScale, false)
			h, ok3 = num(2, 0, true)
			a, bb = c*math.Cos(h*math.Pi/180), c*math.Sin(h*math.Pi/180)
		} else {
			a, ok2 = num(1, abScale, false)
			bb, ok3 = num(2, abScale, false)
		}
		if !ok1 || !ok2 || !ok3 {
			return "", false
		}
		if fn == "oklch" || fn == "oklab" {
			r, g, b = oklabToLinear(L, a, bb)
		} else {
			r, g, b = labToLinear(L, a, bb)
		}
		r, g, b = gammaEncode(r), gammaEncode(g), gammaEncode(b)
	default:
		return "", false
	}
	return fmt.Sprintf("#%02X%02X%02X", to8(r), to8(g), to8(b)), true
}

func to8(x float64) int {
	return int(math.Round(255 * math.Max(0, math.Min(1, x))))
}

func finite(f float64, err error) (float64, bool) {
	return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
}

// cssNumber parses a number, a percentage (scaled so that 100% = pctScale)
// or, with angle set, an angle in degrees.
func cssNumber(s string, pctScale float64, angle bool) (float64, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "none" {
		return 0, true
	}
	if angle {
		for _, u := range []struct {
			suffix string
			mul    float64
		}{{"deg", 1}, {"grad", 0.9}, {"rad", 180 / math.Pi}, {"turn", 360}} {
			if t, ok := strings.CutSuffix(s, u.suffix); ok {
				f, ok := finite(strconv.ParseFloat(t, 64))
				return f * u.mul, ok
			}
		}
	}
	if t, ok := strings.CutSuffix(s, "%"); ok {
		f, ok := finite(strconv.ParseFloat(t, 64))
		return f / 100 * pctScale, ok
	}
	return finite(strconv.ParseFloat(s, 64))
}

// cssPercent parses saturation/lightness/whiteness style components given as
// "40%" or the legacy bare "40"; it returns a 0..1 fraction.
func cssPercent(s string) (float64, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "none" {
		return 0, true
	}
	f, ok := finite(strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64))
	return math.Max(0, math.Min(1, f/100)), ok
}

func hslToRGB(h, s, l float64) (float64, float64, float64) {
	h = math.Mod(math.Mod(h, 360)+360, 360)
	f := func(n float64) float64 {
		k := math.Mod(n+h/30, 12)
		return l - s*math.Min(l, 1-l)*math.Max(-1, math.Min(math.Min(k-3, 9-k), 1))
	}
	return f(0), f(8), f(4)
}

func gammaEncode(x float64) float64 {
	x = math.Max(0, math.Min(1, x))
	if x <= 0.0031308 {
		return 12.92 * x
	}
	return 1.055*math.Pow(x, 1/2.4) - 0.055
}

func cube(x float64) float64 { return x * x * x }

func oklabToLinear(L, a, b float64) (float64, float64, float64) {
	l := cube(L + 0.3963377774*a + 0.2158037573*b)
	m := cube(L - 0.1055613458*a - 0.0638541728*b)
	s := cube(L - 0.0894841775*a - 1.2914855480*b)
	return 4.0767416621*l - 3.3077115913*m + 0.2309699292*s,
		-1.2684380046*l + 2.6097574011*m - 0.3413193965*s,
		-0.0041960863*l - 0.7034186147*m + 1.7076147010*s
}

// labToLinear converts CIE Lab (D50) to linear sRGB (Bradford-adapted).
func labToLinear(L, a, b float64) (float64, float64, float64) {
	const eps, kappa = 216.0 / 24389, 24389.0 / 27
	fy := (L + 16) / 116
	fx, fz := fy+a/500, fy-b/200
	inv := func(f float64) float64 {
		if f*f*f > eps {
			return f * f * f
		}
		return (116*f - 16) / kappa
	}
	y := L / kappa
	if L > kappa*eps {
		y = fy * fy * fy
	}
	x, z := inv(fx)*0.3457/0.3585, inv(fz)*(1-0.3457-0.3585)/0.3585
	// Bradford adaptation D50 -> D65, then XYZ -> linear sRGB.
	X := 0.9554734527042182*x - 0.023098536874261423*y + 0.0632593086610217*z
	Y := -0.028369706963208136*x + 1.0099954580058226*y + 0.021041398966943008*z
	Z := 0.012314001688319899*x - 0.020507696433477912*y + 1.3303659366080753*z
	return 3.2409699419045226*X - 1.537383177570094*Y - 0.4986107602930034*Z,
		-0.9692436362808796*X + 1.8759675015077202*Y + 0.04155505740717559*Z,
		0.05563007969699366*X - 0.20397695888897652*Y + 1.0569715142428786*Z
}

// namedColors are the CSS named colours (hex without #).
var namedColors = func() map[string]string {
	m := map[string]string{}
	const list = `aliceblue f0f8ff antiquewhite faebd7 aqua 00ffff aquamarine 7fffd4 azure f0ffff beige f5f5dc bisque ffe4c4 black 000000 blanchedalmond ffebcd blue 0000ff blueviolet 8a2be2 brown a52a2a burlywood deb887 cadetblue 5f9ea0 chartreuse 7fff00 chocolate d2691e coral ff7f50 cornflowerblue 6495ed cornsilk fff8dc crimson dc143c cyan 00ffff darkblue 00008b darkcyan 008b8b darkgoldenrod b8860b darkgray a9a9a9 darkgreen 006400 darkgrey a9a9a9 darkkhaki bdb76b darkmagenta 8b008b darkolivegreen 556b2f darkorange ff8c00 darkorchid 9932cc darkred 8b0000 darksalmon e9967a darkseagreen 8fbc8f darkslateblue 483d8b darkslategray 2f4f4f darkslategrey 2f4f4f darkturquoise 00ced1 darkviolet 9400d3 deeppink ff1493 deepskyblue 00bfff dimgray 696969 dimgrey 696969 dodgerblue 1e90ff firebrick b22222 floralwhite fffaf0 forestgreen 228b22 fuchsia ff00ff gainsboro dcdcdc ghostwhite f8f8ff gold ffd700 goldenrod daa520 gray 808080 green 008000 greenyellow adff2f grey 808080 honeydew f0fff0 hotpink ff69b4 indianred cd5c5c indigo 4b0082 ivory fffff0 khaki f0e68c lavender e6e6fa lavenderblush fff0f5 lawngreen 7cfc00 lemonchiffon fffacd lightblue add8e6 lightcoral f08080 lightcyan e0ffff lightgoldenrodyellow fafad2 lightgray d3d3d3 lightgreen 90ee90 lightgrey d3d3d3 lightpink ffb6c1 lightsalmon ffa07a lightseagreen 20b2aa lightskyblue 87cefa lightslategray 778899 lightslategrey 778899 lightsteelblue b0c4de lightyellow ffffe0 lime 00ff00 limegreen 32cd32 linen faf0e6 magenta ff00ff maroon 800000 mediumaquamarine 66cdaa mediumblue 0000cd mediumorchid ba55d3 mediumpurple 9370db mediumseagreen 3cb371 mediumslateblue 7b68ee mediumspringgreen 00fa9a mediumturquoise 48d1cc mediumvioletred c71585 midnightblue 191970 mintcream f5fffa mistyrose ffe4e1 moccasin ffe4b5 navajowhite ffdead navy 000080 oldlace fdf5e6 olive 808000 olivedrab 6b8e23 orange ffa500 orangered ff4500 orchid da70d6 palegoldenrod eee8aa palegreen 98fb98 paleturquoise afeeee palevioletred db7093 papayawhip ffefd5 peachpuff ffdab9 peru cd853f pink ffc0cb plum dda0dd powderblue b0e0e6 purple 800080 rebeccapurple 663399 red ff0000 rosybrown bc8f8f royalblue 4169e1 saddlebrown 8b4513 salmon fa8072 sandybrown f4a460 seagreen 2e8b57 seashell fff5ee sienna a0522d silver c0c0c0 skyblue 87ceeb slateblue 6a5acd slategray 708090 slategrey 708090 snow fffafa springgreen 00ff7f steelblue 4682b4 tan d2b48c teal 008080 thistle d8bfd8 tomato ff6347 turquoise 40e0d0 violet ee82ee wheat f5deb3 white ffffff whitesmoke f5f5f5 yellow ffff00 yellowgreen 9acd32`
	f := strings.Fields(list)
	for i := 0; i+1 < len(f); i += 2 {
		m[f[i]] = f[i+1]
	}
	return m
}()
