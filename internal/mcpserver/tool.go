// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vocdoni/folio/internal/activity"
	"github.com/vocdoni/folio/internal/model"
)

// access classifies a tool for its annotations and audit behaviour.
type access int

const (
	readOnly    access = iota // no side effects
	write                     // creates or changes state, not destructive
	destructive               // may delete or irreversibly hide data
)

// tool describes one MCP tool with typed input and output.
type tool[In, Out any] struct {
	Name, Title, Description string
	Access                   access
	Idempotent               bool
	OpenWorld                bool // talks to systems outside Folio (URL fetch, AI reviewer)
	// Target names the target of a call: the audit target of a failed
	// mutating call and the activity target of every call.
	Target func(In) (kind, id string)
	// Record (optional) returns the activity target id when only the output
	// knows it (new documents, uploaded assets; "" keeps Target's) and the
	// allowlisted activity detail of a successful call.
	Record func(in In, out *Out) (targetID string, d activity.Detail)
	Handle func(ctx context.Context, c *call, in In) (*Out, error)
}

// call is the per-invocation context handed to handlers.
type call struct {
	d       *Deps
	req     *mcp.CallToolRequest
	Actor   string
	summary string
	text    string // replaces the JSON text block when set (get_guide)
	content []mcp.Content
}

// Summary sets the one-line text summary of a successful result.
func (c *call) Summary(format string, args ...any) { c.summary = fmt.Sprintf(format, args...) }

// Text replaces the JSON text block with t (for Markdown payloads).
func (c *call) Text(t string) { c.text = t }

// Add appends content blocks (resource links, images) after the text.
func (c *call) Add(content ...mcp.Content) { c.content = append(c.content, content...) }

func actorOf(req *mcp.CallToolRequest) string {
	if req != nil && req.Extra != nil && req.Extra.TokenInfo != nil && req.Extra.TokenInfo.UserID != "" {
		return req.Extra.TokenInfo.UserID
	}
	return LocalActor
}

// schemaOptions maps json.RawMessage (metadata, data, schemas, specs) to a
// JSON object schema instead of the byte-array schema inferred by default.
var schemaOptions = &jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{
	reflect.TypeFor[json.RawMessage](): {Type: "object"},
}}

// errorSchema describes structuredContent.error.
var errorSchema = &jsonschema.Schema{
	Type:        "object",
	Description: "Present only on failed calls (isError=true).",
	Required:    []string{"code", "message", "retryable", "details"},
	Properties: map[string]*jsonschema.Schema{
		"code": {Type: "string", Enum: []any{"invalid_input", "validation_failed", "not_found", "conflict", "archived",
			"too_large", "busy", "timeout", "render_failed", "unavailable", "internal"}},
		"message":   {Type: "string", Description: "What went wrong and how to fix it."},
		"retryable": {Type: "boolean", Description: "true when retrying the same call later can succeed."},
		"details": {Type: "array", Description: "Findings: {code, severity, message, path?, page?, hint?}.",
			Items: &jsonschema.Schema{Type: "object"}},
	},
}

// relaxRequired clears "required" below the root: nested objects (brand and
// template specs) are validated by the services, which return precise
// findings with JSON pointers; the schema still rejects unknown properties.
func relaxRequired(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	s.Required = nil
	for _, p := range s.Properties {
		relaxRequired(p)
	}
	relaxRequired(s.Items)
	relaxRequired(s.AdditionalProperties)
}

func inputSchema[In any]() (*jsonschema.Schema, *jsonschema.Resolved) {
	s, err := jsonschema.For[In](schemaOptions)
	if err != nil {
		panic(fmt.Sprintf("mcpserver: input schema: %v", err))
	}
	for _, p := range s.Properties {
		relaxRequired(p)
	}
	r, err := s.Resolve(nil)
	if err != nil {
		panic(fmt.Sprintf("mcpserver: input schema: %v", err))
	}
	return s, r
}

// outputSchema is the schema of Out plus the optional "error" property; the
// root has no required list so error results conform too.
func outputSchema[Out any]() (*jsonschema.Schema, *jsonschema.Resolved) {
	s, err := jsonschema.For[Out](schemaOptions)
	if err != nil {
		panic(fmt.Sprintf("mcpserver: output schema: %v", err))
	}
	s.Required = nil
	s.Properties["error"] = errorSchema
	r, err := s.Resolve(nil)
	if err != nil {
		panic(fmt.Sprintf("mcpserver: output schema: %v", err))
	}
	return s, r
}

// decodeArgs validates the raw arguments against the input schema, then
// decodes them strictly (unknown fields rejected, numbers inside
// json.RawMessage fields kept as their decimal text).
func decodeArgs[In any](raw json.RawMessage, resolved *jsonschema.Resolved) (In, error) {
	var in In
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		raw = json.RawMessage(`{}`)
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return in, invalid("arguments are not valid JSON: %v", err)
	}
	if err := resolved.Validate(generic); err != nil {
		return in, &model.Error{Code: model.ErrInvalidInput, Message: "the arguments do not match the tool's input schema",
			Details: []model.Finding{{Code: model.CodeSchemaViolation, Severity: model.SeverityError, Path: "/", Message: err.Error()}}}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		var ute *json.UnmarshalTypeError
		if errors.As(err, &ute) {
			return in, invalid("argument %s must be a %s (got %s)", ute.Field, ute.Type.Kind(), ute.Value)
		}
		return in, invalid("arguments: %v", err)
	}
	return in, nil
}

func boolPtr(b bool) *bool { return &b }

// register adds t to s. Handlers return (*Out, nil) or an error; register
// turns errors into isError results with structuredContent.error, writes
// an audit row for failed mutating calls, validates successful output
// against the declared schema and logs tool, actor, outcome and duration
// (never arguments or content).
func register[In, Out any](s *mcp.Server, d *Deps, t tool[In, Out]) {
	inS, inR := inputSchema[In]()
	outS, outR := outputSchema[Out]()
	ann := &mcp.ToolAnnotations{
		Title:           t.Title,
		ReadOnlyHint:    t.Access == readOnly,
		DestructiveHint: boolPtr(t.Access == destructive),
		IdempotentHint:  t.Idempotent || t.Access == readOnly,
		OpenWorldHint:   boolPtr(t.OpenWorld),
	}
	s.AddTool(&mcp.Tool{Name: t.Name, Title: t.Title, Description: t.Description,
		InputSchema: inS, OutputSchema: outS, Annotations: ann},
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			start := time.Now()
			c := &call{d: d, req: req, Actor: actorOf(req)}
			in, err := decodeArgs[In](req.Params.Arguments, inR)
			var out *Out
			if err == nil {
				out, err = t.Handle(ctx, c, in)
			}
			kind, id := "", ""
			if t.Target != nil {
				kind, id = t.Target(in)
			}
			if err == nil {
				var res *mcp.CallToolResult
				if res, err = c.success(out, outR); err == nil {
					dur := time.Since(start)
					d.Logger.InfoContext(ctx, "tool call", "tool", t.Name, "actor", c.Actor, "outcome", "ok",
						"duration_ms", dur.Milliseconds())
					var detail activity.Detail
					if t.Record != nil {
						var outID string
						if outID, detail = t.Record(in, out); outID != "" {
							id = outID
						}
					}
					d.record(t.Name, c.Actor, kind, id, "", dur, detail)
					return res, nil
				}
			}
			te, internal := toolError(err)
			attrs := []any{"tool", t.Name, "actor", c.Actor, "outcome", string(te.Code), "duration_ms", time.Since(start).Milliseconds()}
			if internal {
				d.Logger.ErrorContext(ctx, "tool call failed", append(attrs, "err", err)...)
			} else {
				d.Logger.InfoContext(ctx, "tool call", attrs...)
			}
			if t.Access != readOnly {
				d.audit(ctx, c.Actor, t.Name, kind, id, string(te.Code))
			}
			d.record(t.Name, c.Actor, kind, id, string(te.Code), time.Since(start), activity.Detail{})
			return errorResult(te), nil
		})
}

// success marshals out, validates it against the output schema and builds
// the result: summary text, JSON text (or the handler's Text), extra content.
func (c *call) success(out any, outR *jsonschema.Resolved) (*mcp.CallToolResult, error) {
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("mcpserver: marshal output: %w", err)
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("mcpserver: output: %w", err)
	}
	if err := outR.Validate(generic); err != nil {
		return nil, fmt.Errorf("mcpserver: output does not match its schema: %w", err)
	}
	text := c.text
	if text == "" {
		text = string(raw)
	}
	summary := c.summary
	if summary == "" {
		summary = "ok"
	}
	content := []mcp.Content{&mcp.TextContent{Text: summary}, &mcp.TextContent{Text: text}}
	return &mcp.CallToolResult{StructuredContent: json.RawMessage(raw), Content: append(content, c.content...)}, nil
}
