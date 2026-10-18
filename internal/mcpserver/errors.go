// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vocdoni/folio/internal/model"
)

// ToolError is structuredContent.error of a failed tool call.
type ToolError struct {
	Code      model.ErrorCode `json:"code"`
	Message   string          `json:"message"`
	Retryable bool            `json:"retryable"`
	Details   []model.Finding `json:"details"`
}

// retryHints is appended to messages that do not already say whether a retry
// helps, so agents never loop on errors that cannot succeed unchanged.
var retryHints = map[model.ErrorCode]string{
	model.ErrInvalidInput:     "Fix the arguments; retrying unchanged will fail the same way.",
	model.ErrValidationFailed: "Fix every finding in details (path and hint); retrying unchanged will fail the same way.",
	model.ErrNotFound:         "Check the id with the matching list_* tool; retrying unchanged will fail the same way.",
	model.ErrConflict:         "Re-read the current state and retry with it.",
	model.ErrArchived:         "Unarchive it or pick another one; retrying unchanged will fail the same way.",
	model.ErrTooLarge:         "Reduce the size; retrying unchanged will fail the same way.",
	model.ErrBusy:             "Retry in a few seconds.",
	model.ErrTimeout:          "Simplify the document (fewer nested blocks, smaller tables); retrying unchanged will likely time out again.",
	model.ErrRenderFailed:     "Fix the template layout or the content named in the message; retrying unchanged will fail the same way.",
	model.ErrUnavailable:      "This may be temporary; retry later.",
	model.ErrInternal:         "This is a server-side problem that was logged; retry once, then report it if it persists.",
}

// toolError maps any error to the agent-facing ToolError. Only *model.Error
// messages are shown; other errors (database, filesystem) become a generic
// internal error so server paths and driver messages never reach agents.
// internal reports whether the cause should be logged in full.
func toolError(err error) (te ToolError, internal bool) {
	var me *model.Error
	switch {
	case errors.As(err, &me):
		te = ToolError{Code: me.Code, Message: me.Message, Retryable: me.Retryable, Details: me.Details}
		internal = me.Code == model.ErrInternal
	case errors.Is(err, context.DeadlineExceeded):
		te = ToolError{Code: model.ErrTimeout, Message: "the operation exceeded its time limit"}
	case errors.Is(err, context.Canceled):
		te = ToolError{Code: model.ErrUnavailable, Message: "the request was cancelled"}
	default:
		te = ToolError{Code: model.ErrInternal, Message: "internal error"}
		internal = true
	}
	switch te.Code {
	case model.ErrBusy:
		te.Retryable = true
	case model.ErrUnavailable:
		te.Retryable = !errors.Is(err, errNoReviewer)
	case model.ErrInternal:
		// Retryable stays false: an automatic retry loop must not hammer a broken server.
	}
	if te.Message == "" {
		te.Message = string(te.Code)
	}
	if hint := retryHints[te.Code]; hint != "" && !strings.Contains(strings.ToLower(te.Message), "retry") {
		te.Message = strings.TrimRight(te.Message, ". ") + ". " + hint
	}
	if te.Details == nil {
		te.Details = []model.Finding{}
	}
	return te, internal
}

// maxDetailLines bounds the findings repeated in the text content.
const maxDetailLines = 10

// errorResult builds the isError result: structuredContent {"error": te},
// a readable text summary and the same JSON for clients without
// structuredContent support.
func errorResult(te ToolError) *mcp.CallToolResult {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Error %s: %s", te.Code, te.Message)
	for i, f := range te.Details {
		if i == maxDetailLines {
			fmt.Fprintf(&sb, "\n… %d more in structuredContent.error.details", len(te.Details)-i)
			break
		}
		fmt.Fprintf(&sb, "\n- [%s %s] %s", f.Severity, f.Code, f.Message)
		if f.Path != "" {
			fmt.Fprintf(&sb, " (at %s)", f.Path)
		}
		if f.Hint != "" {
			fmt.Fprintf(&sb, " — %s", f.Hint)
		}
	}
	body := map[string]ToolError{"error": te}
	raw, _ := json.Marshal(body) // ToolError always marshals
	return &mcp.CallToolResult{
		IsError:           true,
		StructuredContent: json.RawMessage(raw),
		Content:           []mcp.Content{&mcp.TextContent{Text: sb.String()}, &mcp.TextContent{Text: string(raw)}},
	}
}

// invalid is a shorthand for invalid_input errors raised by tool handlers.
func invalid(format string, args ...any) error {
	return &model.Error{Code: model.ErrInvalidInput, Message: fmt.Sprintf(format, args...)}
}
