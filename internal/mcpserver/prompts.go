// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func userMessage(text string) []*mcp.PromptMessage {
	return []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}}
}

// newDocumentPrompt walks an agent through creating a document; with a
// template id it inlines the template's required sections and components.
func (d *Deps) newDocumentPrompt(ctx context.Context, templateID string) (string, error) {
	var sb strings.Builder
	sb.WriteString("Create a branded document with Folio.\n\n")
	if templateID == "" {
		sb.WriteString("1. Call list_templates and pick the template that fits the request (ask the user if several fit).\n")
		sb.WriteString("2. Call get_template(id) and read sections, metadata_schema, data_schema, components, body_skeleton and sample.\n")
	} else {
		t, v, err := d.Catalog.ResolveTemplate(ctx, templateID, 0, false)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&sb, "Template: %s (%q, version %d, languages %s).\n", t.ID, t.Name, v, strings.Join(t.Languages, ", "))
		var req []string
		for _, s := range t.Sections {
			if s.Required {
				req = append(req, s.Title)
			}
		}
		if len(req) > 0 {
			fmt.Fprintf(&sb, "Required sections (headings): %s.\n", strings.Join(req, ", "))
		}
		if len(t.Components) > 0 {
			fmt.Fprintf(&sb, "Components available: %s (empty fenced divs, data in data.<name>).\n", strings.Join(t.Components, ", "))
		}
		fmt.Fprintf(&sb, "\n1. Call get_template(%q) and read metadata_schema, data_schema, body_skeleton and sample.\n", t.ID)
		sb.WriteString("2. Start from body_skeleton; keep its headings and component blocks.\n")
	}
	sb.WriteString(`3. Gather the facts from the user; never invent prices, dates or names. If the document has images, upload them once with upload_asset and reference them as ![caption](asset:<asset_id>).
4. Put line items, milestones, deliverables and signatures in data (amounts as strings for exact decimals). Folio computes subtotals, taxes, totals and payment amounts: never compute or type them yourself.
5. Call validate_document and fix every error (each finding has a path and a hint). Replace placeholders such as TODO.
6. Call render_document with an idempotency_key. Check the warnings and the first preview image.
7. Share the PDF and DOCX links (they are public capabilities: anyone with the link can download). To change the document later, call get_document(include_source=true) and render_document(document_id, expected_revision).
Read get_guide('markdown') and get_guide('components') for syntax details.`)
	return sb.String(), nil
}

func newTemplatePrompt(category string) string {
	var sb strings.Builder
	sb.WriteString("Create a reusable Folio template.\n\n")
	sb.WriteString("1. Read get_guide('templates'), get_guide('brands') and, if you need a custom layout, get_guide('typst-layouts'). Call get_capabilities for fonts, components, layout options and limits.\n")
	if category != "" {
		fmt.Fprintf(&sb, "2. Call list_templates(category=%q) and get_template on the closest one: its spec is valid create_template input to start from.\n", category)
	} else {
		sb.WriteString("2. Call list_templates and get_template on the closest one: its spec is valid create_template input to start from.\n")
	}
	sb.WriteString(`3. Pick a brand with list_brands, or create one: upload the logo with upload_asset, then create_brand.
4. Write the template: a new slug id, sections (titles, aliases per language, required flags, hints), metadata_schema and data_schema (reference component schemas as {"$ref": "folio:component/budget"}), components, body_skeleton and a complete, realistic sample. create_template renders the sample and fails on any error or any non-placeholder warning.
5. Call create_template, then preview_template(include_previews='all') and look at every page.
6. Fix problems with update_template(expected_version = the latest version). Archive templates you no longer want with archive_template.`)
	return sb.String()
}

func registerPrompts(s *mcp.Server, d *Deps) {
	s.AddPrompt(&mcp.Prompt{Name: "new_document", Title: "New document",
		Description: "Step-by-step guidance to write, validate and render a document from a template.",
		Arguments:   []*mcp.PromptArgument{{Name: "template_id", Description: "template to use; omit to choose one with list_templates"}}},
		func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			text, err := d.newDocumentPrompt(ctx, req.Params.Arguments["template_id"])
			if err != nil {
				te, _ := toolError(err)
				return nil, fmt.Errorf("new_document: %s", te.Message)
			}
			return &mcp.GetPromptResult{Description: "Create a document with Folio", Messages: userMessage(text)}, nil
		})
	s.AddPrompt(&mcp.Prompt{Name: "new_template", Title: "New template",
		Description: "Step-by-step guidance to design, validate and preview a new template (and brand if needed).",
		Arguments:   []*mcp.PromptArgument{{Name: "category", Description: "e.g. proposal, quote, report; used to find a template to start from"}}},
		func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{Description: "Create a template with Folio", Messages: userMessage(newTemplatePrompt(req.Params.Arguments["category"]))}, nil
		})
}
