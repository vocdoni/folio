// SPDX-License-Identifier: AGPL-3.0-only

// Package assets accepts uploaded images (base64 or SSRF-guarded https
// fetch), sniffs and sanitises them (raster re-encode, strict SVG allowlist)
// and stores them as content-addressed blobs.
package assets

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"regexp"
	"strconv"
	"strings"

	_ "golang.org/x/image/webp" // registers the WebP decoder
)

// MIME types Folio stores.
const (
	MIMEPNG  = "image/png"
	MIMEJPEG = "image/jpeg"
	MIMESVG  = "image/svg+xml"
)

// MaxPixels is the decoded raster size limit (50 MP).
const MaxPixels = 50_000_000

var (
	ErrUnsupported = errors.New("unsupported image type (accepted: PNG, JPEG, WebP, SVG)")
	ErrTooLarge    = errors.New("image too large")
	ErrUnsafeSVG   = errors.New("unsafe SVG")
)

// Sanitized is a cleaned image ready to store.
type Sanitized struct {
	MIME   string
	Ext    string // png | jpg | svg
	Data   []byte
	Width  int // pixels; for SVG the intrinsic size (0 when unknown)
	Height int
}

// Sniff returns the detected type ("png", "jpeg", "webp", "svg") from content.
func Sniff(data []byte) (string, error) {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "png", nil
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return "jpeg", nil
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "webp", nil
	case looksLikeSVG(data):
		return "svg", nil
	}
	return "", ErrUnsupported
}

func looksLikeSVG(data []byte) bool {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	for i := 0; i < 64; i++ {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se.Name.Local == "svg"
		}
	}
	return false
}

// Sanitize validates and cleans an image. Raster images are decoded (after a
// pixel-count check on the header) and re-encoded, which drops all metadata;
// WebP becomes PNG. SVGs must pass CheckSVG and are stored unchanged.
func Sanitize(data []byte, maxBytes int64) (Sanitized, error) {
	if int64(len(data)) > maxBytes {
		return Sanitized{}, fmt.Errorf("%w: %d bytes exceeds %d", ErrTooLarge, len(data), maxBytes)
	}
	kind, err := Sniff(data)
	if err != nil {
		return Sanitized{}, err
	}
	if kind == "svg" {
		if err := CheckSVG(data); err != nil {
			return Sanitized{}, err
		}
		w, h := SVGSize(data)
		return Sanitized{MIME: MIMESVG, Ext: "svg", Data: data, Width: w, Height: h}, nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Sanitized{}, fmt.Errorf("%w: cannot decode %s header: %w", ErrUnsupported, kind, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return Sanitized{}, fmt.Errorf("%w: %dx%d exceeds %d pixels", ErrTooLarge, cfg.Width, cfg.Height, MaxPixels)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Sanitized{}, fmt.Errorf("%w: cannot decode %s: %w", ErrUnsupported, kind, err)
	}
	var out bytes.Buffer
	s := Sanitized{Width: cfg.Width, Height: cfg.Height}
	if kind == "jpeg" {
		if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 90}); err != nil {
			return Sanitized{}, fmt.Errorf("re-encode jpeg: %w", err)
		}
		s.MIME, s.Ext = MIMEJPEG, "jpg"
	} else {
		if err := (&png.Encoder{CompressionLevel: png.DefaultCompression}).Encode(&out, img); err != nil {
			return Sanitized{}, fmt.Errorf("re-encode png: %w", err)
		}
		s.MIME, s.Ext = MIMEPNG, "png"
	}
	s.Data = out.Bytes()
	return s, nil
}

const (
	maxSVGElements = 50_000
	maxSVGDepth    = 128
)

var forbiddenSVGElements = map[string]bool{
	"script": true, "foreignobject": true, "iframe": true, "embed": true,
	"object": true, "handler": true, "listener": true,
	"set": true, "animate": true, "animatetransform": true, "animatemotion": true, "discard": true,
}

// urlRefRE finds CSS url(...) references; group 1 is the target.
var urlRefRE = regexp.MustCompile(`(?i)url\(\s*['"]?\s*([^'")\s]*)`)

// CheckSVG rejects SVGs that could execute code or reference anything outside
// the file: DOCTYPE/entities, processing instructions other than the XML
// declaration, script/foreignObject/embedding elements, on* event handlers,
// href/xlink:href/src not starting with "#" or a data:image/png|jpeg|gif|webp
// URI (data:image/svg+xml is refused: nested SVG would bypass this check),
// CSS url() to anything but "#..." and @import. It also bounds element count
// and depth.
func CheckSVG(data []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	dec.Entity = map[string]string{} // only the five predefined entities
	depth, elements := 0, 0
	sawRoot := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("%w: malformed XML: %w", ErrUnsafeSVG, err)
		}
		switch t := tok.(type) {
		case xml.Directive:
			return fmt.Errorf("%w: DOCTYPE and entity declarations are not allowed", ErrUnsafeSVG)
		case xml.ProcInst:
			if t.Target != "xml" {
				return fmt.Errorf("%w: processing instruction <?%s?> is not allowed", ErrUnsafeSVG, t.Target)
			}
		case xml.StartElement:
			depth++
			elements++
			if depth > maxSVGDepth || elements > maxSVGElements {
				return fmt.Errorf("%w: too many or too deeply nested elements", ErrUnsafeSVG)
			}
			name := strings.ToLower(t.Name.Local)
			if !sawRoot {
				if name != "svg" {
					return fmt.Errorf("%w: root element must be <svg>", ErrUnsafeSVG)
				}
				sawRoot = true
			}
			if forbiddenSVGElements[name] {
				return fmt.Errorf("%w: <%s> is not allowed", ErrUnsafeSVG, t.Name.Local)
			}
			for _, a := range t.Attr {
				if err := checkSVGAttr(a); err != nil {
					return err
				}
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			if err := checkCSS(string(t)); err != nil {
				return err
			}
		}
	}
	if !sawRoot {
		return fmt.Errorf("%w: no <svg> root", ErrUnsafeSVG)
	}
	return nil
}

func checkSVGAttr(a xml.Attr) error {
	local := strings.ToLower(a.Name.Local)
	if strings.HasPrefix(local, "on") {
		return fmt.Errorf("%w: event handler attribute %q is not allowed", ErrUnsafeSVG, a.Name.Local)
	}
	if local == "href" || local == "src" {
		v := strings.TrimSpace(a.Value)
		lv := strings.ToLower(v)
		ok := strings.HasPrefix(v, "#")
		for _, p := range []string{"data:image/png", "data:image/jpeg", "data:image/gif", "data:image/webp"} {
			ok = ok || strings.HasPrefix(lv, p+";") || strings.HasPrefix(lv, p+",")
		}
		if !ok {
			return fmt.Errorf("%w: external reference %s=%q is not allowed", ErrUnsafeSVG, a.Name.Local, truncate(v))
		}
	}
	return checkCSS(a.Value)
}

func checkCSS(s string) error {
	ls := strings.ToLower(s)
	if strings.Contains(ls, "@import") || strings.Contains(ls, "expression(") {
		return fmt.Errorf("%w: CSS @import/expression() is not allowed", ErrUnsafeSVG)
	}
	for _, m := range urlRefRE.FindAllStringSubmatch(s, -1) {
		if !strings.HasPrefix(m[1], "#") {
			return fmt.Errorf("%w: url(%s) must reference a local fragment", ErrUnsafeSVG, truncate(m[1]))
		}
	}
	return nil
}

func truncate(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}

// SVGSize returns the intrinsic size in whole CSS pixels from the root
// width/height (unitless or px) or the viewBox, or 0,0 when unknown.
func SVGSize(data []byte) (int, int) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err != nil {
			return 0, 0
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		var w, h int
		var vb string
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "width":
				w = parseLen(a.Value)
			case "height":
				h = parseLen(a.Value)
			case "viewBox":
				vb = a.Value
			}
		}
		if w > 0 && h > 0 {
			return w, h
		}
		if f := strings.Fields(strings.ReplaceAll(vb, ",", " ")); len(f) == 4 {
			return parseLen(f[2]), parseLen(f[3])
		}
		return 0, 0
	}
}

// parseLen parses "200", "200px" or "200.5" into whole pixels; other units → 0.
func parseLen(s string) int {
	s = strings.TrimSuffix(strings.TrimSpace(s), "px")
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i]
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
