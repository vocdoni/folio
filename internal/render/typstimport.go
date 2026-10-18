// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/vocdoni/folio/internal/model"
)

// importRE finds `import` / `include` keywords (with or without '#') and
// captures the start of their argument. Comments are not stripped, so
// commented-out imports are also checked; this scan is advisory and errs on
// the side of rejecting.
var importRE = regexp.MustCompile(`(?m)(?:^|[^\w.-])#?(import|include)\b[ \t]*`)

var baseImportRE = regexp.MustCompile(`^/base/[a-z0-9-]+\.typ$`)

// ScanTypstImports checks every import/include in a custom layout source.
// Each must be a string literal that is a relative path without "..", a
// "/base/<name>.typ" path, or an allowlisted "@preview/<name>:<version>".
// The enforced controls are the sandbox (package path, dead proxy, Landlock);
// this gives agents a precise error before compiling.
func ScanTypstImports(src string, allowlist []string) []model.Finding {
	allowed := map[string]bool{}
	for _, a := range allowlist {
		allowed[a] = true
	}
	var out []model.Finding
	for _, loc := range importRE.FindAllStringSubmatchIndex(src, -1) {
		kw := src[loc[2]:loc[3]]
		rest := src[loc[1]:]
		line := strings.Count(src[:loc[2]], "\n") + 1
		bad := func(msg string) {
			out = append(out, model.Finding{
				Code: model.CodeInvalidTypstImport, Severity: model.SeverityError,
				Message: fmt.Sprintf("layout.typst line %d: %s %s", line, kw, msg),
				Path:    "/layout/typst",
				Hint:    `use a string literal: a relative path, "/base/<name>.typ" or an allowlisted @preview package from get_capabilities`,
			})
		}
		if !strings.HasPrefix(rest, `"`) {
			// Bare "import"/"include" followed by neither a string nor an
			// expression is ordinary prose in markup mode; only the #-form
			// and expression arguments are import statements. The match may
			// start with the preceding non-word character, so the '#' is the
			// last byte before the keyword.
			if !strings.HasSuffix(src[loc[0]:loc[2]], "#") && !strings.HasPrefix(rest, "(") {
				continue
			}
			bad("must be followed by a string literal")
			continue
		}
		end := strings.IndexAny(rest[1:], "\"\n")
		if end < 0 || rest[1+end] != '"' {
			bad("has an unterminated string")
			continue
		}
		target := rest[1 : 1+end]
		switch {
		case strings.Contains(target, `\`):
			bad("path must not contain escapes")
		case strings.HasPrefix(target, "@"):
			if !allowed[target] {
				bad(fmt.Sprintf("%q is not an allowlisted package", target))
			}
		case strings.HasPrefix(target, "/"):
			if !baseImportRE.MatchString(target) {
				bad(fmt.Sprintf("%q: absolute paths must be /base/<name>.typ", target))
			}
		default:
			clean := path.Clean(target)
			if target == "" || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(target, "://") {
				bad(fmt.Sprintf("%q must be a relative path inside the layout", target))
			}
		}
	}
	return out
}
