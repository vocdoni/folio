// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"fmt"
	"strings"
)

// Severity of a finding.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Finding is one validation or check result.
type Finding struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	Path     string   `json:"path,omitempty"` // JSON pointer into the input, or "markdown:<line>"
	Page     int      `json:"page,omitempty"`
	Hint     string   `json:"hint,omitempty"`
}

// Finding codes (stable, documented in the agent guide).
const (
	CodeSchemaViolation     = "schema_violation"
	CodeInvalidFormat       = "invalid_format"
	CodeMarkdownTooLarge    = "markdown_too_large"
	CodeNestingTooDeep      = "nesting_too_deep"
	CodeTableTooLarge       = "table_too_large"
	CodeMalformedTable      = "malformed_table"
	CodeMissingSection      = "missing_section"
	CodeUnknownComponent    = "unknown_component"
	CodeComponentNotAllowed = "component_not_allowed"
	CodeComponentNotEmpty   = "component_not_empty"
	CodeMisplacedComponent  = "misplaced_component"
	CodeComponentMissing    = "component_missing"
	CodeInvalidImage        = "invalid_image_target"
	CodeMissingAsset        = "missing_asset"
	CodeInvalidLink         = "invalid_link"
	CodeRawContent          = "raw_content"
	CodePlaceholder         = "placeholder"
	CodeMoneyInvalid        = "money_invalid"
	CodeExpectedTotal       = "expected_total_mismatch"
	CodePaymentSum          = "payment_pct_sum"
	CodePandocWarning       = "pandoc_warning"
	CodeTypstWarning        = "typst_warning"
	CodePDFInvalid          = "pdf_invalid"
	CodeTooManyPages        = "too_many_pages"
	CodeBlankPage           = "blank_page"
	CodeEdgeInk             = "edge_ink"
	CodeDOCXInvalid         = "docx_invalid"
	CodeInvalidTypstImport  = "invalid_typst_import"
)

// HasErrors reports whether any finding is an error.
func HasErrors(fs []Finding) bool {
	for _, f := range fs {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}

// ErrorCode is the top-level error code returned to agents.
type ErrorCode string

const (
	ErrInvalidInput     ErrorCode = "invalid_input"
	ErrValidationFailed ErrorCode = "validation_failed"
	ErrNotFound         ErrorCode = "not_found"
	ErrConflict         ErrorCode = "conflict"
	ErrArchived         ErrorCode = "archived"
	ErrTooLarge         ErrorCode = "too_large"
	ErrBusy             ErrorCode = "busy"
	ErrTimeout          ErrorCode = "timeout"
	ErrRenderFailed     ErrorCode = "render_failed"
	ErrUnavailable      ErrorCode = "unavailable"
	ErrInternal         ErrorCode = "internal"
)

// Error is the structured error every engine API returns for expected
// failures; P2 maps it 1:1 to the MCP error result.
type Error struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	Retryable bool      `json:"retryable"`
	Details   []Finding `json:"details,omitempty"`
	Err       error     `json:"-"` // wrapped cause, never shown to agents
}

func (e *Error) Error() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s: %s", e.Code, e.Message)
	if e.Err != nil {
		fmt.Fprintf(&sb, ": %v", e.Err)
	}
	return sb.String()
}

func (e *Error) Unwrap() error { return e.Err }

// Errorf builds an *Error; only busy is retryable (a timeout fails the same way on an unchanged retry).
func Errorf(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Retryable: code == ErrBusy}
}
