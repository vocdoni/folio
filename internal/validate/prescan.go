// SPDX-License-Identifier: AGPL-3.0-only

// Package validate checks document input before rendering: size and nesting
// pre-scan of the raw Markdown (before pandoc runs), JSON Schema validation
// of metadata and data, and a walk of the pandoc AST for nesting, raw
// content, image targets, links, tables, components, sections and placeholders.
package validate

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/vocdoni/folio/internal/model"
)

// Limits are the configured input limits.
type Limits struct {
	MaxMarkdownBytes int // FOLIO_MAX_MARKDOWN_BYTES
	MaxNesting       int // FOLIO_MAX_NESTING
	MaxTableCells    int // FOLIO_MAX_TABLE_CELLS
}

// preScanFactor: the raw-text estimate over-counts (indentation, fences), so
// it rejects only at twice the configured nesting; the AST walk enforces the
// exact limit afterwards.
const preScanFactor = 2

// preScanHardFactor bounds the estimate on every line, code fences included.
// The fence tracking below is approximate (pandoc does not treat an unclosed
// fence, a one-line ```code```, a tab-indented fence or a ```lang line inside
// an open block the way it does), so content it skips may still be Markdown
// to pandoc. This fence-independent limit caps what such a desync can hide.
const preScanHardFactor = 16

var (
	fenceRE    = regexp.MustCompile("^\\s{0,3}(```+|~~~+)")
	divOpenRE  = regexp.MustCompile(`^\s{0,3}:{3,}\s*\S`)
	divCloseRE = regexp.MustCompile(`^\s{0,3}:{3,}\s*$`)
	tableSepRE = regexp.MustCompile(`^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)+\|?\s*$`)
	codeSpanRE = regexp.MustCompile("`[^`]*`")
)

// PreScan runs on the raw Markdown before pandoc: size limit, a cheap
// nesting estimate (block-quote markers + indentation/2 + open fenced divs,
// outside code fences) so nesting bombs never reach pandoc, and pipe-table
// rows whose cell count differs from the header (pandoc silently pads or
// truncates them, so the AST cannot show it).
func PreScan(md string, l Limits) []model.Finding {
	if l.MaxMarkdownBytes > 0 && len(md) > l.MaxMarkdownBytes {
		return []model.Finding{{Code: model.CodeMarkdownTooLarge, Severity: model.SeverityError, Path: "/markdown",
			Message: fmt.Sprintf("markdown is %d bytes; the limit is %d", len(md), l.MaxMarkdownBytes),
			Hint:    "split the document or move large tables into data"}}
	}
	var out []model.Finding
	lines := strings.Split(md, "\n")
	inFence, fence := false, ""
	divDepth := 0
	limit := l.MaxNesting * preScanFactor
	hardLimit := l.MaxNesting * preScanHardFactor
	anyDivDepth := 0 // fenced divs counted on every line, fences ignored
	nestingReported := false
	tableCols, tableStart := 0, 0
	for i, line := range lines {
		ln := i + 1
		switch {
		case divCloseRE.MatchString(line):
			if anyDivDepth > 0 {
				anyDivDepth--
			}
		case divOpenRE.MatchString(line):
			anyDivDepth++
		}
		if l.MaxNesting > 0 && !nestingReported && anyDivDepth+quoteDepth(line)+indentCols(line)/2 > hardLimit {
			out = append(out, model.Finding{Code: model.CodeNestingTooDeep, Severity: model.SeverityError,
				Path: fmt.Sprintf("markdown:%d", ln), Message: fmt.Sprintf("line %d is nested too deeply (limit %d levels)", ln, l.MaxNesting),
				Hint: "flatten nested lists, quotes and divs"})
			nestingReported = true
		}
		if m := fenceRE.FindStringSubmatch(line); m != nil {
			if !inFence {
				inFence, fence = true, m[1][:3]
			} else if strings.HasPrefix(strings.TrimSpace(line), fence) {
				inFence = false
			}
			continue
		}
		if inFence {
			continue
		}
		switch {
		case divCloseRE.MatchString(line):
			if divDepth > 0 {
				divDepth--
			}
		case divOpenRE.MatchString(line):
			divDepth++
		}
		depth := divDepth + quoteDepth(line) + indentCols(line)/2
		if l.MaxNesting > 0 && depth > limit && !nestingReported {
			out = append(out, model.Finding{Code: model.CodeNestingTooDeep, Severity: model.SeverityError,
				Path: fmt.Sprintf("markdown:%d", ln), Message: fmt.Sprintf("line %d is nested too deeply (limit %d levels)", ln, l.MaxNesting),
				Hint: "flatten nested lists, quotes and divs"})
			nestingReported = true
		}
		// Pipe tables: a separator row after a header row starts a table.
		if tableCols == 0 && i > 0 && tableSepRE.MatchString(line) && strings.Contains(lines[i-1], "|") {
			tableCols, tableStart = countCells(lines[i-1]), ln-1
			if sep := countCells(line); sep != tableCols {
				out = append(out, malformed(ln, tableStart, sep, tableCols))
			}
			continue
		}
		if tableCols > 0 {
			if strings.TrimSpace(line) == "" || !strings.Contains(line, "|") {
				tableCols = 0
				continue
			}
			if n := countCells(line); n != tableCols {
				out = append(out, malformed(ln, tableStart, n, tableCols))
			}
		}
	}
	return out
}

func malformed(line, start, got, want int) model.Finding {
	return model.Finding{Code: model.CodeMalformedTable, Severity: model.SeverityError, Path: fmt.Sprintf("markdown:%d", line),
		Message: fmt.Sprintf("table starting at line %d: row on line %d has %d cells, the header has %d", start, line, got, want),
		Hint:    "give every row the same number of | separated cells as the header"}
}

func quoteDepth(line string) int {
	n := 0
	for _, r := range line {
		switch r {
		case '>':
			n++
		case ' ', '\t':
		default:
			return n
		}
	}
	return n
}

func indentCols(line string) int {
	n := 0
	for _, r := range line {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}
	return n
}

// countCells counts pipe-table cells: unescaped '|' outside code spans,
// ignoring one leading and one trailing pipe.
func countCells(line string) int {
	s := strings.TrimSpace(codeSpanRE.ReplaceAllString(line, "``"))
	s = strings.ReplaceAll(s, `\|`, "")
	s = strings.TrimPrefix(s, "|")
	s = strings.TrimSuffix(s, "|")
	return strings.Count(s, "|") + 1
}
