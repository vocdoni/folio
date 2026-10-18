// SPDX-License-Identifier: AGPL-3.0-only

// Package review defines the optional AI visual reviewer. A Reviewer looks at
// rendered page images and returns advisory findings; it never changes
// content, status or validation outcome. The service stores the findings on
// the revision as a Report.
package review

import (
	"context"
	"slices"
	"strings"
	"time"
)

// Page is one rendered page (1-based number, PNG bytes at preview resolution).
type Page struct {
	Number int
	PNG    []byte
}

// Info is the document context sent along with the pages.
type Info struct {
	DocumentID string
	Revision   int
	TemplateID string
	Title      string
	Language   string
}

// Severities and categories accepted in findings.
var (
	Severities = []string{"info", "warning", "problem"}
	Categories = []string{"blank", "overflow", "overlap", "table", "typography", "image", "branding", "other"}
)

// Finding is one advisory observation about a page.
type Finding struct {
	Page     int    `json:"page"`
	Severity string `json:"severity"` // info | warning | problem
	Category string `json:"category"` // one of Categories
	Message  string `json:"message"`
}

// Reviewer reviews rendered pages. Implementations must honour ctx and
// return an error (never partial findings) when the review cannot complete.
type Reviewer interface {
	Review(ctx context.Context, pages []Page, info Info) ([]Finding, error)
}

// Report is what is stored on a revision (revisions.review) and returned to
// agents. Advisory is always true.
type Report struct {
	Advisory   bool      `json:"advisory"`
	ReviewedAt time.Time `json:"reviewed_at"`
	ReviewedBy string    `json:"reviewed_by"`
	Pages      []int     `json:"pages"`
	Findings   []Finding `json:"findings"`
}

// MaxMessageRunes bounds a finding message.
const MaxMessageRunes = 500

// Normalize keeps findings that refer to a reviewed page, maps unknown
// severities to "info" and unknown categories to "other", trims messages to
// MaxMessageRunes and drops empty ones. It never returns nil.
func Normalize(fs []Finding, pages []int) []Finding {
	out := []Finding{}
	for _, f := range fs {
		if !slices.Contains(pages, f.Page) {
			continue
		}
		msg := strings.TrimSpace(f.Message)
		if msg == "" {
			continue
		}
		if r := []rune(msg); len(r) > MaxMessageRunes {
			msg = string(r[:MaxMessageRunes]) + "…"
		}
		sev, cat := strings.ToLower(f.Severity), strings.ToLower(f.Category)
		if !slices.Contains(Severities, sev) {
			sev = "info"
		}
		if !slices.Contains(Categories, cat) {
			cat = "other"
		}
		out = append(out, Finding{Page: f.Page, Severity: sev, Category: cat, Message: msg})
	}
	return out
}
