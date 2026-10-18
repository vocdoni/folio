// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vocdoni/folio/internal/documents"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/render"
	"github.com/vocdoni/folio/internal/review"
)

type ValidateIn struct {
	TemplateID      string          `json:"template_id"`
	TemplateVersion int             `json:"template_version,omitempty" jsonschema:"omit for the latest version"`
	Markdown        string          `json:"markdown" jsonschema:"document body in Folio Markdown (get_guide markdown); start from the template's body_skeleton"`
	Metadata        json.RawMessage `json:"metadata" jsonschema:"title, date (YYYY-MM-DD), language plus the template's metadata_schema fields"`
	Data            json.RawMessage `json:"data,omitempty" jsonschema:"component data, e.g. {\"budget\": {...}}; numbers may be strings for exact decimals"`
}

type ValidateOut struct {
	Valid    bool            `json:"valid" jsonschema:"false when render_document would fail with validation_failed"`
	Template model.Ref       `json:"template"`
	Brand    model.Ref       `json:"brand"`
	Computed json.RawMessage `json:"computed,omitempty" jsonschema:"totals, taxes and payments Folio computed (present when valid)"`
	Findings []model.Finding `json:"findings" jsonschema:"errors (must fix) and warnings"`
}

type RenderIn struct {
	TemplateID       string          `json:"template_id,omitempty" jsonschema:"required for a new document; defaults to the document's template for revisions"`
	TemplateVersion  int             `json:"template_version,omitempty" jsonschema:"omit for the latest version"`
	BrandID          string          `json:"brand_id,omitempty" jsonschema:"omit to use the template's brand"`
	Markdown         string          `json:"markdown"`
	Metadata         json.RawMessage `json:"metadata"`
	Data             json.RawMessage `json:"data,omitempty"`
	Formats          []string        `json:"formats,omitempty" jsonschema:"subset of [pdf, docx]; default both"`
	DocumentID       string          `json:"document_id,omitempty" jsonschema:"set to create a new revision of an existing document"`
	ExpectedRevision *int            `json:"expected_revision,omitempty" jsonschema:"required with document_id: the latest revision you read"`
	IdempotencyKey   string          `json:"idempotency_key,omitempty" jsonschema:"up to 128 characters; a retry with the same key within 24 h returns the first document instead of a duplicate"`
	IncludePreviews  string          `json:"include_previews,omitempty" jsonschema:"none, first (default) or all (up to 6 page images)"`
}

type RenderOut struct {
	DocumentID string          `json:"document_id"`
	Revision   int             `json:"revision"`
	Unchanged  bool            `json:"unchanged" jsonschema:"true when an identical input or idempotency_key returned an existing revision"`
	Status     string          `json:"status" jsonschema:"draft, published or trashed"`
	Template   model.Ref       `json:"template"`
	Brand      model.Ref       `json:"brand"`
	Files      []FileOut       `json:"files"`
	Previews   []string        `json:"previews"`
	Computed   json.RawMessage `json:"computed"`
	Warnings   []model.Finding `json:"warnings"`
}

type ListDocumentsIn struct {
	Query          string `json:"query,omitempty" jsonschema:"case-insensitive substring of title or client"`
	TemplateID     string `json:"template_id,omitempty"`
	Status         string `json:"status,omitempty" jsonschema:"draft, published or trashed"`
	Client         string `json:"client,omitempty" jsonschema:"exact client name"`
	CreatedBy      string `json:"created_by,omitempty" jsonschema:"token name of the author"`
	IncludeTrashed bool   `json:"include_trashed,omitempty"`
	Limit          int    `json:"limit,omitempty" jsonschema:"1-200, default 50"`
	Cursor         string `json:"cursor,omitempty"`
}

type ListDocumentsOut struct {
	Documents  []documents.Summary `json:"documents"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

type GetDocumentIn struct {
	ID              string `json:"id"`
	Revision        int    `json:"revision,omitempty" jsonschema:"omit for the latest revision"`
	IncludeSource   bool   `json:"include_source,omitempty" jsonschema:"return the stored input (markdown, metadata, data, formats) to revise it"`
	IncludePreviews string `json:"include_previews,omitempty" jsonschema:"none, first (default) or all; ignored when pages is set"`
	Pages           []int  `json:"pages,omitempty" jsonschema:"page numbers to return as images (at most 6)"`
}

type RevisionSummary struct {
	Revision  int       `json:"revision"`
	Parent    int       `json:"parent_revision,omitempty"`
	Template  model.Ref `json:"template"`
	Brand     model.Ref `json:"brand"`
	Reviewed  bool      `json:"reviewed"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

type RevisionOut struct {
	Revision  int             `json:"revision"`
	Parent    int             `json:"parent_revision,omitempty"`
	Template  model.Ref       `json:"template"`
	Brand     model.Ref       `json:"brand"`
	Pages     int             `json:"pages,omitempty"`
	Files     []FileOut       `json:"files"`
	Previews  []string        `json:"previews"`
	Computed  json.RawMessage `json:"computed"`
	Warnings  []model.Finding `json:"warnings"`
	Review    json.RawMessage `json:"review,omitempty" jsonschema:"advisory AI review (never affects content or status)"`
	CreatedBy string          `json:"created_by"`
	CreatedAt time.Time       `json:"created_at"`
}

type DocumentOut struct {
	ID                string            `json:"id"`
	Title             string            `json:"title"`
	TemplateID        string            `json:"template_id"`
	Client            string            `json:"client"`
	Status            string            `json:"status"`
	LatestRevision    int               `json:"latest_revision"`
	PublishedRevision int               `json:"published_revision,omitempty"`
	CreatedBy         string            `json:"created_by"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
	Revision          RevisionOut       `json:"revision" jsonschema:"the requested revision in full"`
	Revisions         []RevisionSummary `json:"revisions" jsonschema:"every revision, newest first"`
	Source            json.RawMessage   `json:"source,omitempty" jsonschema:"stored canonical input (include_source=true)"`
}

type ReviewIn struct {
	ID       string `json:"id"`
	Revision int    `json:"revision,omitempty" jsonschema:"omit for the latest revision"`
	Pages    []int  `json:"pages,omitempty" jsonschema:"pages to review; default all pages up to the server limit"`
}

type ReviewOut struct {
	DocumentID string        `json:"document_id"`
	Revision   int           `json:"revision"`
	Review     review.Report `json:"review"`
}

type PublishIn struct {
	ID       string `json:"id"`
	Revision int    `json:"revision" jsonschema:"the revision to mark final"`
}

type StatusOut struct {
	DocumentID        string `json:"document_id"`
	Status            string `json:"status"`
	PublishedRevision int    `json:"published_revision,omitempty"`
}

type DeleteDocumentIn struct {
	ID               string `json:"id"`
	Purge            bool   `json:"purge,omitempty" jsonschema:"true deletes permanently (revisions, files, links); default moves to the trash"`
	ConfirmPublished bool   `json:"confirm_published,omitempty" jsonschema:"required to delete a document that has a published revision"`
}

type DeleteDocumentOut struct {
	DocumentID string `json:"document_id"`
	Trashed    bool   `json:"trashed"`
	Purged     bool   `json:"purged"`
}

const (
	descValidate = `Dry-run a document against a template without rendering files: checks metadata and data against the template schemas, the Markdown dialect, required sections, components, images and placeholders, and computes money values. Returns valid, findings (errors and warnings with JSON pointer path and hint) and computed totals. Call it before render_document and fix every error; warnings (e.g. placeholders) do not block rendering unless the template is strict. Never compute totals yourself: read them from computed.`

	descRender = `Render a document to PDF and DOCX (formats) and store it as a revision. New document: template_id, markdown, metadata, data; pass an idempotency_key whenever you might retry, so a retry returns the first document (unchanged=true) instead of a duplicate. New revision of an existing document: document_id plus expected_revision = its latest revision (get_document); a stale value fails with conflict. Identical input returns the existing revision with unchanged=true. Returns download links (public capabilities; share only with intended readers), preview images (include_previews), computed totals and warnings. Errors: validation_failed (fix details and retry), archived, busy (retry in a few seconds), timeout (simplify the document). Long renders send progress notifications.`

	descListDocuments = `List documents, newest first, with status (draft, published, trashed), latest and published revision, template, client and author. Trashed documents are hidden unless include_trashed=true or status=trashed. Use get_document for files and details.`

	descGetDocument = `Get a document: metadata, every revision (newest first), and the requested revision in full (files with download links, preview links, computed totals, warnings, advisory review). include_source=true returns the stored input, which is what you edit to revise the document with render_document(document_id, expected_revision). Page images: include_previews (none, first, all) or explicit pages (at most 6).`

	descReview = `Ask the optional AI visual reviewer to look at rendered pages of a revision and store its findings on that revision (re-running replaces them). Findings are advisory only: they never change content, status or validation, and every response labels them advisory. Returns unavailable when the reviewer is not configured on this server or fails (retry later). Pages leave the server only when this tool runs.`

	descPublish = `Mark a revision as the final, published one. The document status becomes published; later revisions stay drafts until published. Deleting a published document needs confirm_published=true.`

	descDelete = `Move a document to the trash (default) or delete it permanently (purge=true). Trashed documents stop serving their download links immediately, can be restored with restore_document, and are purged automatically after the trash retention. Purging deletes every revision and file and cannot be undone. Documents with a published revision require confirm_published=true.`

	descRestore = `Restore a document from the trash: its download links work again and its status returns to published (if it had a published revision) or draft.`
)

func (c *call) renderOut(ctx context.Context, r *documents.RenderResult, mode string) (*RenderOut, error) {
	files, previews, pages := c.outputs(r.Files)
	if err := c.attachPreviews(ctx, r.Files, mode, pages); err != nil {
		return nil, err
	}
	out := &RenderOut{DocumentID: r.DocumentID, Revision: r.Revision, Unchanged: r.Unchanged, Status: r.Status, Template: r.Template,
		Brand: r.Brand, Files: files, Previews: previews, Computed: r.Computed, Warnings: r.Warnings}
	if out.Files == nil {
		out.Files = []FileOut{}
	}
	if out.Previews == nil {
		out.Previews = []string{}
	}
	if out.Warnings == nil {
		out.Warnings = []model.Finding{}
	}
	if len(out.Computed) == 0 {
		out.Computed = json.RawMessage(`{}`)
	}
	return out, nil
}

func (c *call) documentOut(ctx context.Context, d *documents.Document, in GetDocumentIn) (*DocumentOut, error) {
	r := d.Revision
	files, previews, pages := c.outputs(r.Files)
	out := &DocumentOut{ID: d.ID, Title: d.Title, TemplateID: d.TemplateID, Client: d.Client, Status: d.Status,
		LatestRevision: d.LatestRevision, PublishedRevision: d.PublishedRevision, CreatedBy: d.CreatedBy, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
		Revision: RevisionOut{Revision: r.Revision, Parent: r.Parent, Template: r.Template, Brand: r.Brand, Pages: pages,
			Files: files, Previews: previews, Computed: r.Computed, Warnings: r.Warnings, Review: r.Review, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt},
		Revisions: []RevisionSummary{}}
	if out.Revision.Files == nil {
		out.Revision.Files = []FileOut{}
	}
	if out.Revision.Previews == nil {
		out.Revision.Previews = []string{}
	}
	if out.Revision.Warnings == nil {
		out.Revision.Warnings = []model.Finding{}
	}
	for _, s := range d.Revisions {
		out.Revisions = append(out.Revisions, RevisionSummary{Revision: s.Revision, Parent: s.Parent, Template: s.Template, Brand: s.Brand,
			Reviewed: len(s.Review) > 0, CreatedBy: s.CreatedBy, CreatedAt: s.CreatedAt})
	}
	if in.IncludeSource {
		src, err := c.d.Documents.Source(ctx, d.ID, r.Revision)
		if err != nil {
			return nil, err
		}
		out.Source = src
	}
	if len(in.Pages) > 0 {
		if len(in.Pages) > MaxImages {
			return nil, invalid("pages: at most %d pages per call", MaxImages)
		}
		for _, p := range in.Pages {
			if p < 1 || (pages > 0 && p > pages) {
				return nil, invalid("pages: page %d does not exist (the PDF has %d pages)", p, pages)
			}
		}
		return out, c.attachPages(ctx, r.Files, in.Pages)
	}
	mode, err := previewMode(in.IncludePreviews)
	if err != nil {
		return nil, err
	}
	return out, c.attachPreviews(ctx, r.Files, mode, pages)
}

func formatsOf(fs []string) ([]model.Format, error) {
	var out []model.Format
	for _, f := range fs {
		if f != string(model.FormatPDF) && f != string(model.FormatDOCX) {
			return nil, invalid("formats must be a subset of [pdf, docx] (got %q)", f)
		}
		out = append(out, model.Format(f))
	}
	return out, nil
}

// reviewPages chooses the pages to review: the requested ones (each must
// exist) or every page up to FOLIO_REVIEW_MAX_PAGES.
func reviewPages(requested []int, pageCount, maxPages int) ([]int, error) {
	if len(requested) == 0 {
		var out []int
		for p := 1; p <= min(max(pageCount, 1), maxPages); p++ {
			out = append(out, p)
		}
		return out, nil
	}
	if len(requested) > maxPages {
		return nil, invalid("pages: at most %d pages per review", maxPages)
	}
	out := slices.Clone(requested)
	slices.Sort(out)
	out = slices.Compact(out)
	for _, p := range out {
		if p < 1 || (pageCount > 0 && p > pageCount) {
			return nil, invalid("pages: page %d does not exist (the PDF has %d pages)", p, pageCount)
		}
	}
	return out, nil
}

// errNoReviewer is returned when no AI reviewer is configured; unlike other
// unavailable errors it is not retryable.
var errNoReviewer = &model.Error{Code: model.ErrUnavailable,
	Message: "the AI reviewer is not configured on this server (the operator sets FOLIO_OPENROUTER_API_KEY); use get_document with include_previews to inspect pages yourself. Retrying will fail the same way until the operator enables it"}

func (d *Deps) review(ctx context.Context, c *call, in ReviewIn) (*ReviewOut, error) {
	if d.Reviewer == nil {
		return nil, errNoReviewer
	}
	doc, err := d.Documents.Get(ctx, in.ID, in.Revision)
	if err != nil {
		return nil, err
	}
	if doc.Status == "trashed" {
		return nil, &model.Error{Code: model.ErrConflict, Message: fmt.Sprintf("document %s is in the trash; restore it first", doc.ID)}
	}
	pageCount := 0
	for _, f := range doc.Revision.Files {
		if f.Kind == "pdf" {
			pageCount = c.pdfPages(f)
		}
	}
	pages, err := reviewPages(in.Pages, pageCount, d.Config.ReviewMaxPages)
	if err != nil {
		return nil, err
	}
	imgs, err := c.pagePNGs(ctx, doc.Revision.Files, pages)
	if err != nil {
		return nil, err
	}
	rp := make([]review.Page, 0, len(pages))
	for _, p := range pages {
		rp = append(rp, review.Page{Number: p, PNG: imgs[p]})
	}
	var meta struct {
		Language string `json:"language"`
	}
	if src, err := d.Documents.Source(ctx, doc.ID, doc.Revision.Revision); err == nil {
		var in struct {
			Metadata json.RawMessage `json:"metadata"`
		}
		if json.Unmarshal(src, &in) == nil {
			_ = json.Unmarshal(in.Metadata, &meta)
		}
	}
	findings, err := d.Reviewer.Review(ctx, rp, review.Info{DocumentID: doc.ID, Revision: doc.Revision.Revision,
		TemplateID: doc.TemplateID, Title: doc.Title, Language: meta.Language})
	if err != nil {
		d.Logger.WarnContext(ctx, "ai review failed", "document", doc.ID, "revision", doc.Revision.Revision, "err", err)
		return nil, &model.Error{Code: model.ErrUnavailable, Message: "the AI reviewer failed or timed out; the document is unaffected"}
	}
	rep := review.Report{Advisory: true, ReviewedAt: d.Now().UTC().Truncate(time.Second), ReviewedBy: c.Actor, Pages: pages,
		Findings: review.Normalize(findings, pages)}
	raw, err := model.Canonical(rep)
	if err != nil {
		return nil, err
	}
	if err := d.Documents.SetReview(ctx, c.Actor, doc.ID, doc.Revision.Revision, raw); err != nil {
		return nil, err
	}
	return &ReviewOut{DocumentID: doc.ID, Revision: doc.Revision.Revision, Review: rep}, nil
}

func registerDocumentTools(s *mcp.Server, d *Deps) {
	register(s, d, tool[ValidateIn, ValidateOut]{Name: "validate_document", Title: "Validate a document (dry run)", Description: descValidate, Access: readOnly,
		Handle: func(ctx context.Context, c *call, in ValidateIn) (*ValidateOut, error) {
			t, tv, err := d.Catalog.ResolveTemplate(ctx, in.TemplateID, in.TemplateVersion, false)
			if err != nil {
				return nil, err
			}
			b, bv, err := d.Catalog.ResolveBrand(ctx, t.BrandID, 0)
			if err != nil {
				return nil, err
			}
			out := &ValidateOut{Template: model.Ref{ID: t.ID, Version: tv}, Brand: model.Ref{ID: b.ID, Version: bv}}
			res, err := d.Validator.Render(ctx, render.Request{Template: t, TemplateVersion: tv, Brand: b, BrandVersion: bv, ValidateOnly: true,
				Input: model.DocumentInput{Markdown: in.Markdown, Metadata: in.Metadata, Data: in.Data}})
			var me *model.Error
			if errors.As(err, &me) && me.Code == model.ErrValidationFailed {
				out.Findings = me.Details
				c.Summary("invalid: %d finding(s); fix the errors before render_document", len(out.Findings))
				return out, nil
			}
			if err != nil {
				return nil, err
			}
			out.Valid, out.Computed, out.Findings = true, res.Computed, res.Warnings
			if out.Findings == nil {
				out.Findings = []model.Finding{}
			}
			c.Summary("valid: %d warning(s)", len(out.Findings))
			return out, nil
		}})

	register(s, d, tool[RenderIn, RenderOut]{Name: "render_document", Title: "Render a document", Description: descRender, Access: write,
		Target: func(in RenderIn) (string, string) { return "document", in.DocumentID },
		Handle: func(ctx context.Context, c *call, in RenderIn) (*RenderOut, error) {
			mode, err := previewMode(in.IncludePreviews)
			if err != nil {
				return nil, err
			}
			formats, err := formatsOf(in.Formats)
			if err != nil {
				return nil, err
			}
			if in.TemplateID == "" && in.DocumentID == "" {
				return nil, invalid("template_id is required for a new document (or pass document_id and expected_revision to revise one)")
			}
			p := c.startProgress(ctx)
			defer p.Stop()
			r, err := d.Documents.Render(p.Context(ctx), c.Actor, documents.RenderRequest{TemplateID: in.TemplateID, TemplateVersion: in.TemplateVersion,
				BrandID: in.BrandID, DocumentID: in.DocumentID, ExpectedRevision: in.ExpectedRevision, IdempotencyKey: in.IdempotencyKey,
				Input: model.DocumentInput{Markdown: in.Markdown, Metadata: in.Metadata, Data: in.Data, Formats: formats}})
			if err != nil {
				return nil, err
			}
			verb := "rendered"
			if r.Unchanged {
				verb = "unchanged, returned existing"
			}
			c.Summary("%s document %s revision %d (%s, %d warning(s))", verb, r.DocumentID, r.Revision, r.Status, len(r.Warnings))
			return c.renderOut(ctx, r, mode)
		}})

	register(s, d, tool[ListDocumentsIn, ListDocumentsOut]{Name: "list_documents", Title: "List documents", Description: descListDocuments, Access: readOnly,
		Handle: func(ctx context.Context, c *call, in ListDocumentsIn) (*ListDocumentsOut, error) {
			limit, err := listLimit(in.Limit)
			if err != nil {
				return nil, err
			}
			if in.Status != "" && in.Status != "draft" && in.Status != "published" && in.Status != "trashed" {
				return nil, invalid("status must be draft, published or trashed")
			}
			list, next, err := d.Documents.List(ctx, documents.ListFilter{Query: in.Query, TemplateID: in.TemplateID, Status: in.Status,
				Client: in.Client, CreatedBy: in.CreatedBy, IncludeTrashed: in.IncludeTrashed, Limit: limit, Cursor: in.Cursor})
			if err != nil {
				return nil, err
			}
			if list == nil {
				list = []documents.Summary{}
			}
			c.Summary("%d document(s)", len(list))
			return &ListDocumentsOut{Documents: list, NextCursor: next}, nil
		}})

	register(s, d, tool[GetDocumentIn, DocumentOut]{Name: "get_document", Title: "Get a document", Description: descGetDocument, Access: readOnly,
		Handle: func(ctx context.Context, c *call, in GetDocumentIn) (*DocumentOut, error) {
			doc, err := d.Documents.Get(ctx, in.ID, in.Revision)
			if err != nil {
				return nil, err
			}
			out, err := c.documentOut(ctx, doc, in)
			if err != nil {
				return nil, err
			}
			c.Summary("document %s %q: %s, revision %d of %d", doc.ID, doc.Title, doc.Status, doc.Revision.Revision, doc.LatestRevision)
			return out, nil
		}})

	register(s, d, tool[ReviewIn, ReviewOut]{Name: "review_document", Title: "AI visual review (advisory)", Description: descReview,
		Access: write, Idempotent: true, OpenWorld: d.Reviewer != nil,
		Target: func(in ReviewIn) (string, string) { return "document", in.ID },
		Handle: func(ctx context.Context, c *call, in ReviewIn) (*ReviewOut, error) {
			p := c.startProgress(ctx)
			defer p.Stop()
			p.Step(ctx, "reviewing pages")
			out, err := d.review(ctx, c, in)
			if err != nil {
				return nil, err
			}
			c.Summary("advisory review of %s revision %d: %d finding(s) on %d page(s)", out.DocumentID, out.Revision, len(out.Review.Findings), len(out.Review.Pages))
			return out, nil
		}})

	register(s, d, tool[PublishIn, StatusOut]{Name: "publish_document", Title: "Publish a revision", Description: descPublish, Access: write, Idempotent: true,
		Target: func(in PublishIn) (string, string) { return "document", in.ID },
		Handle: func(ctx context.Context, c *call, in PublishIn) (*StatusOut, error) {
			if err := d.Documents.Publish(ctx, c.Actor, in.ID, in.Revision); err != nil {
				return nil, err
			}
			c.Summary("document %s revision %d published", in.ID, in.Revision)
			return &StatusOut{DocumentID: in.ID, Status: "published", PublishedRevision: in.Revision}, nil
		}})

	register(s, d, tool[DeleteDocumentIn, DeleteDocumentOut]{Name: "delete_document", Title: "Trash or purge a document", Description: descDelete, Access: destructive,
		Target: func(in DeleteDocumentIn) (string, string) { return "document", in.ID },
		Handle: func(ctx context.Context, c *call, in DeleteDocumentIn) (*DeleteDocumentOut, error) {
			if in.Purge {
				if err := d.Documents.Purge(ctx, c.Actor, in.ID, in.ConfirmPublished); err != nil {
					return nil, err
				}
				c.Summary("document %s purged permanently", in.ID)
				return &DeleteDocumentOut{DocumentID: in.ID, Purged: true}, nil
			}
			if err := d.Documents.Trash(ctx, c.Actor, in.ID, in.ConfirmPublished); err != nil {
				return nil, err
			}
			c.Summary("document %s moved to the trash; restore_document undoes it", in.ID)
			return &DeleteDocumentOut{DocumentID: in.ID, Trashed: true}, nil
		}})

	register(s, d, tool[IDIn, StatusOut]{Name: "restore_document", Title: "Restore a document from the trash", Description: descRestore, Access: write,
		Target: func(in IDIn) (string, string) { return "document", in.ID },
		Handle: func(ctx context.Context, c *call, in IDIn) (*StatusOut, error) {
			if err := d.Documents.Restore(ctx, c.Actor, in.ID); err != nil {
				return nil, err
			}
			doc, err := d.Documents.Get(ctx, in.ID, 0)
			if err != nil {
				return nil, err
			}
			c.Summary("document %s restored (%s)", in.ID, doc.Status)
			return &StatusOut{DocumentID: in.ID, Status: doc.Status, PublishedRevision: doc.PublishedRevision}, nil
		}})
}
