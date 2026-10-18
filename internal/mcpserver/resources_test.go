// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestResources(t *testing.T) {
	_, cs := docEnv(t, nil)
	ctx := context.Background()
	var r RenderOut
	callOK(t, cs, "render_document", renderArgs(nil), &r)

	list, err := cs.ListResources(ctx, nil)
	if err != nil || len(list.Resources) != len(Topics) {
		t.Fatalf("resources: %+v %v", list, err)
	}
	tpls, err := cs.ListResourceTemplates(ctx, nil)
	if err != nil || len(tpls.ResourceTemplates) != 3 {
		t.Fatalf("resource templates: %+v %v", tpls, err)
	}
	read := func(uri string) string {
		t.Helper()
		res, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			t.Fatalf("%s: %v", uri, err)
		}
		return res.Contents[0].Text
	}
	md, _ := Guide("markdown")
	if got := read("folio://guide/markdown"); got != md {
		t.Fatal("guide resource differs from get_guide")
	}
	if got := read("folio://templates/memo"); !strings.Contains(got, `"latest_version": 1`) || !strings.Contains(got, `"body_skeleton"`) {
		t.Fatalf("template resource: %s", got)
	}
	if got := read("folio://documents/" + r.DocumentID); !strings.Contains(got, r.DocumentID) || !strings.Contains(got, "https://folio.example.test/f/") {
		t.Fatalf("document resource: %s", got)
	}
	for _, uri := range []string{"folio://guide/nope", "folio://templates/nope", "folio://templates/..%2Fx", "folio://documents/0192aaaa-0000-7000-8000-000000000000"} {
		if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri}); err == nil {
			t.Errorf("%s: expected not found", uri)
		}
	}
}

func TestPrompts(t *testing.T) {
	_, cs := docEnv(t, nil)
	ctx := context.Background()
	list, err := cs.ListPrompts(ctx, nil)
	if err != nil || len(list.Prompts) != 2 {
		t.Fatalf("prompts: %+v %v", list, err)
	}
	get := func(name string, args map[string]string) string {
		t.Helper()
		res, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return res.Messages[0].Content.(*mcp.TextContent).Text
	}
	txt := get("new_document", map[string]string{"template_id": "memo"})
	for _, want := range []string{`Template: memo`, "Required sections (headings): Summary", "validate_document", "idempotency_key", "exactly as it should appear"} {
		if !strings.Contains(txt, want) {
			t.Errorf("new_document(memo) lacks %q", want)
		}
	}
	if txt := get("new_document", nil); !strings.Contains(txt, "list_templates") {
		t.Error("new_document without template must start with list_templates")
	}
	if _, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "new_document", Arguments: map[string]string{"template_id": "nope"}}); err == nil {
		t.Error("unknown template must fail")
	} else if je := new(jsonrpc.Error); !errors.As(err, &je) || je.Code != jsonrpc.CodeInvalidParams {
		t.Errorf("unknown template error = %#v, want JSON-RPC invalid params", err)
	}
	txt = get("new_template", map[string]string{"category": "quote"})
	for _, want := range []string{"get_guide('templates')", `list_templates(category="quote")`, "preview_template", "expected_version"} {
		if !strings.Contains(txt, want) {
			t.Errorf("new_template lacks %q", want)
		}
	}
}
