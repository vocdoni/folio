// SPDX-License-Identifier: AGPL-3.0-only

package validate

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/vocdoni/folio/internal/components"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/pandocast"
)

// AssetTargetRE is the only accepted image target: asset:<uuid>.
var AssetTargetRE = regexp.MustCompile(`^asset:([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)

var placeholderREs = []*regexp.Regexp{
	regexp.MustCompile(`\b(TODO|TBD|FIXME|XXX)\b`),
	regexp.MustCompile(`\[(\.\.\.|…)\]`),
	regexp.MustCompile(`\{\{[^}]*\}\}`),
	regexp.MustCompile(`(?i)\blorem ipsum\b`),
}

// ASTResult is what the walk found besides findings.
type ASTResult struct {
	Findings []model.Finding
	AssetIDs []string         // distinct asset ids referenced by images, in order
	Uses     []components.Use // component divs (top-level), in order
}

func errf(code, path, hint, format string, args ...any) model.Finding {
	return model.Finding{Code: code, Severity: model.SeverityError, Path: path, Hint: hint, Message: fmt.Sprintf(format, args...)}
}

// sectionKey normalises heading text for section matching.
func sectionKey(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

// CheckAST walks a parsed document. assetOK reports whether an asset id
// exists and may be newly referenced (not deleted).
func CheckAST(doc *pandocast.Document, t model.Template, l Limits, assetOK func(id string) bool) ASTResult {
	var r ASTResult
	add := func(f model.Finding) { r.Findings = append(r.Findings, f) }
	if len(doc.Meta) != 0 {
		add(errf(model.CodeRawContent, "/markdown", "remove the leading % title/author/date lines (pandoc title block); send metadata separately", "the document must not carry pandoc metadata"))
	}

	allowed := map[string]bool{}
	for _, c := range t.Components {
		allowed[c] = true
	}
	// section title/alias -> index into t.Sections
	sectionIdx := map[string]int{}
	for i, s := range t.Sections {
		sectionIdx[sectionKey(s.Title)] = i
		for _, a := range s.Aliases {
			sectionIdx[sectionKey(a)] = i
		}
	}
	present := map[int]bool{}       // template sections found
	componentOf := map[string]int{} // component -> declaring section
	for i, s := range t.Sections {
		if s.Component != "" {
			componentOf[s.Component] = i
		}
	}
	seenInSection := map[int]bool{} // sections whose declared component div was found inside them

	// Top-level pass: sections and component placement.
	current := -1
	for idx, b := range doc.Blocks {
		switch x := b.(type) {
		case *pandocast.Header:
			if i, ok := sectionIdx[sectionKey(pandocast.Stringify(x.Inlines))]; ok {
				current = i
				present[i] = true
			} else if x.Level <= 1 {
				current = -1
			}
		case *pandocast.Div:
			name, ok := componentName(x.Attr)
			if !ok {
				continue
			}
			use := components.Use{Name: name, Attrs: map[string]string{}, Index: idx}
			for _, kv := range x.Attr.KeyVals {
				use.Attrs[kv[0]] = kv[1]
			}
			path := "/markdown"
			switch {
			case !components.Known(name):
				add(errf(model.CodeUnknownComponent, path, "known components: "+strings.Join(components.Names, ", "), "unknown component folio-%s", name))
				continue
			case !allowed[name]:
				add(errf(model.CodeComponentNotAllowed, path, "this template allows: "+strings.Join(t.Components, ", "), "component %s is not enabled in template %s", name, t.ID))
				continue
			case len(x.Blocks) != 0:
				add(errf(model.CodeComponentNotEmpty, path, "write ::: folio-"+name+" and ::: on consecutive lines; the content comes from data."+name, "component div folio-%s must be empty", name))
				continue
			}
			if want, ok := componentOf[name]; ok {
				if current != want {
					add(errf(model.CodeMisplacedComponent, path, "move it under the heading "+t.Sections[want].Title,
						"component %s belongs in section %q", name, t.Sections[want].Title))
					continue
				}
				seenInSection[want] = true
			}
			r.Uses = append(r.Uses, use)
		}
	}
	for i, s := range t.Sections {
		if s.Required && !present[i] {
			hint := s.Hint
			if hint == "" {
				hint = "add a heading named " + s.Title
			}
			add(errf(model.CodeMissingSection, "/markdown", hint, "required section %q is missing", s.Title))
		}
		if s.Component != "" && present[i] && !seenInSection[i] && allowed[s.Component] {
			add(model.Finding{Code: model.CodeComponentMissing, Severity: model.SeverityWarning, Path: "/markdown",
				Message: fmt.Sprintf("section %q has no ::: folio-%s block", s.Title, s.Component)})
		}
	}

	// Full walk: nesting, raw content, nested components, images, links, tables, placeholders.
	nestingReported := false
	seenAsset := map[string]bool{}
	placeholderSeverity := model.SeverityWarning
	if t.Strict {
		placeholderSeverity = model.SeverityError
	}
	checkText := func(ins []pandocast.Inline) {
		text := plainText(ins)
		for _, re := range placeholderREs {
			if m := re.FindString(text); m != "" {
				r.Findings = append(r.Findings, model.Finding{Code: model.CodePlaceholder, Severity: placeholderSeverity, Path: "/markdown",
					Message: fmt.Sprintf("placeholder %q in: %s", m, snippet(text)), Hint: "replace placeholders with real content"})
				return
			}
		}
	}
	pandocast.Walk(doc.Blocks, pandocast.Visitor{
		Block: func(b pandocast.Block, depth int) bool {
			if depth > l.MaxNesting && !nestingReported {
				add(errf(model.CodeNestingTooDeep, "/markdown", "flatten nested lists, quotes and divs", "content is nested more than %d levels deep", l.MaxNesting))
				nestingReported = true
			}
			switch x := b.(type) {
			case *pandocast.RawBlock:
				add(errf(model.CodeRawContent, "/markdown", "raw HTML, TeX and Typst are not supported", "raw %s block is not allowed", x.Format))
			case *pandocast.Div:
				if name, ok := componentName(x.Attr); ok && depth > 0 {
					add(errf(model.CodeMisplacedComponent, "/markdown", "components must be top-level blocks", "component folio-%s is nested inside another block", name))
				}
			case *pandocast.Table:
				checkTable(x, l, add)
			case *pandocast.Para:
				checkText(x.Inlines)
			case *pandocast.Plain:
				checkText(x.Inlines)
			case *pandocast.Header:
				checkText(x.Inlines)
			case *pandocast.LineBlock:
				for _, ln := range x.Lines {
					checkText(ln)
				}
			}
			return true
		},
		Inline: func(in pandocast.Inline, _ int) bool {
			switch x := in.(type) {
			case *pandocast.RawInline:
				add(errf(model.CodeRawContent, "/markdown", "raw HTML, TeX and Typst are not supported", "raw %s inline is not allowed", x.Format))
			case *pandocast.Image:
				m := AssetTargetRE.FindStringSubmatch(x.Target.URL)
				if m == nil {
					add(errf(model.CodeInvalidImage, "/markdown", "upload the image with upload_asset and use ![caption](asset:<asset_id>)",
						"image target %q is not an uploaded asset", snippet(x.Target.URL)))
					return true
				}
				if !seenAsset[m[1]] {
					seenAsset[m[1]] = true
					if assetOK != nil && !assetOK(m[1]) {
						add(errf(model.CodeMissingAsset, "/markdown", "check the id with list_assets or upload it again", "asset %s does not exist or was deleted", m[1]))
					} else {
						r.AssetIDs = append(r.AssetIDs, m[1])
					}
				}
			case *pandocast.Link:
				if !linkAllowed(x.Target.URL) {
					add(errf(model.CodeInvalidLink, "/markdown", "links may use http, https, mailto or #anchors", "link target %q is not allowed", snippet(x.Target.URL)))
				}
			}
			return true
		},
	})
	return r
}

func componentName(a pandocast.Attr) (string, bool) {
	for _, c := range a.Classes {
		if n, ok := strings.CutPrefix(c, "folio-"); ok {
			return n, true
		}
	}
	return "", false
}

// linkAllowed accepts http(s) and mailto URLs and same-document #anchors.
func linkAllowed(raw string) bool {
	if strings.HasPrefix(raw, "#") {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.Host != ""
	case "mailto":
		return u.Opaque != ""
	}
	return false
}

func checkTable(t *pandocast.Table, l Limits, add func(model.Finding)) {
	cells := 0
	cols := len(t.ColSpecs)
	var rows []pandocast.Row
	rows = append(rows, t.Head.Rows...)
	for _, b := range t.Bodies {
		rows = append(rows, b.Head...)
		rows = append(rows, b.Body...)
	}
	rows = append(rows, t.Foot.Rows...)
	badRow := false
	for _, r := range rows {
		span := 0
		for _, c := range r.Cells {
			span += c.ColSpan
			cells++
		}
		if span != cols && len(r.Cells) > 0 {
			badRow = true
		}
	}
	if l.MaxTableCells > 0 && cells > l.MaxTableCells {
		add(errf(model.CodeTableTooLarge, "/markdown", "split the table or summarise it", "a table has %d cells; the limit is %d", cells, l.MaxTableCells))
	}
	if badRow {
		add(errf(model.CodeMalformedTable, "/markdown", "give every row the same number of cells as the header", "a table has rows whose cell count differs from its %d columns", cols))
	}
}

// plainText is Stringify without code and math (placeholders in code are allowed).
func plainText(ins []pandocast.Inline) string {
	var sb strings.Builder
	var rec func([]pandocast.Inline)
	rec = func(ins []pandocast.Inline) {
		for _, in := range ins {
			switch x := in.(type) {
			case *pandocast.Str:
				sb.WriteString(x.Text)
			case *pandocast.Space, *pandocast.SoftBreak, *pandocast.LineBreak:
				sb.WriteByte(' ')
			case *pandocast.Emph:
				rec(x.Inlines)
			case *pandocast.Strong:
				rec(x.Inlines)
			case *pandocast.Underline:
				rec(x.Inlines)
			case *pandocast.Strikeout:
				rec(x.Inlines)
			case *pandocast.Superscript:
				rec(x.Inlines)
			case *pandocast.Subscript:
				rec(x.Inlines)
			case *pandocast.SmallCaps:
				rec(x.Inlines)
			case *pandocast.Quoted:
				rec(x.Inlines)
			case *pandocast.Span:
				rec(x.Inlines)
			case *pandocast.Link:
				rec(x.Inlines)
			}
		}
	}
	rec(ins)
	return sb.String()
}

func snippet(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > 80 {
		return string([]rune(s)[:80]) + "…"
	}
	return s
}
