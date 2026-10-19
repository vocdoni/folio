// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vocdoni/folio/internal/activity"
	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/model"
)

type SaveInlineIn struct {
	DocumentID  string        `json:"document_id,omitempty" jsonschema:"a document whose current template is inline; its latest input is the sample unless sample is set"`
	InlineID    string        `json:"inline_id,omitempty" jsonschema:"an inline template id (inline-…); sample is then required"`
	ID          string        `json:"id" jsonschema:"id of the new template (slug ^[a-z0-9][a-z0-9-]{1,62}$, not starting with inline-)"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Category    string        `json:"category,omitempty"`
	Sample      *model.Sample `json:"sample,omitempty" jsonschema:"markdown, metadata and data of the template's sample"`
	Note        string        `json:"note,omitempty"`
}

type SaveInlineOut struct {
	TemplateResultOut
	SourceInlineID string `json:"source_inline_id" jsonschema:"the inline template the new template copies"`
}

const descSaveInline = `Keep an inline template as a regular, listed template (version 1 of a new id). Pass document_id (a document rendered with an inline template; its latest input becomes the sample unless you pass sample) or inline_id (an inline-… id; sample required), plus id, name and optionally description, category, note. Folio validates the spec, renders the sample like create_template but accepts every warning (the document already rendered with them) and returns them, with links to the sample PDF/DOCX and source_inline_id. Then render with template_id = id; the documents keep their inline template. Errors: conflict (id exists), not_found (unknown document or inline id), invalid_input (reserved id, document without an inline template), validation_failed (the sample does not render).`

func registerInlineTools(s *mcp.Server, d *Deps) {
	register(s, d, tool[SaveInlineIn, SaveInlineOut]{Name: "save_inline_template", Title: "Save an inline template as a template",
		Description: descSaveInline, Access: write,
		Target: func(in SaveInlineIn) (string, string) { return "template", in.ID },
		Record: func(in SaveInlineIn, out *SaveInlineOut) (string, activity.Detail) {
			return "", activity.Detail{Version: out.Version, SourceDocument: in.DocumentID}
		},
		Handle: func(ctx context.Context, c *call, in SaveInlineIn) (*SaveInlineOut, error) {
			source, sample := in.InlineID, in.Sample
			switch {
			case (in.DocumentID == "") == (in.InlineID == ""):
				return nil, invalid("pass exactly one of document_id or inline_id")
			case in.DocumentID != "":
				id, latest, err := d.Documents.InlineSource(ctx, in.DocumentID)
				if err != nil {
					return nil, err
				}
				source = id
				if sample == nil {
					sample = &latest
				}
			case sample == nil:
				return nil, invalid("sample is required with inline_id (markdown, metadata and data the template renders as its sample)")
			}
			p := c.startProgress(ctx)
			defer p.Stop()
			r, err := d.Catalog.SaveInline(p.Context(ctx), c.Actor, source, catalog.SaveInlineInput{ID: in.ID, Name: in.Name,
				Description: in.Description, Category: in.Category, Sample: *sample, Note: in.Note})
			if err != nil {
				return nil, err
			}
			res, err := c.templateResult(ctx, r, previewsNone)
			if err != nil {
				return nil, err
			}
			if d.Reviewer != nil {
				p.Step(ctx, "reviewing the sample")
			}
			res.Review = c.reviewTemplate(ctx, r, model.Template{Sample: *sample})
			c.Summary("saved inline template %s as template %s v%d (%d warning(s)%s)", source, r.ID, r.Version, len(r.Warnings), reviewNote(res.Review))
			return &SaveInlineOut{TemplateResultOut: *res, SourceInlineID: source}, nil
		}})
}
