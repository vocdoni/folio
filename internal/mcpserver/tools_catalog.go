// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	bundled "github.com/vocdoni/folio/assets"
	"github.com/vocdoni/folio/internal/activity"
	"github.com/vocdoni/folio/internal/brandkit"
	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/components"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/review"
)

// ---- get_guide / get_capabilities ----

type GuideIn struct {
	Topic string `json:"topic,omitempty" jsonschema:"workflow, recipes, markdown, components, templates, typst-layouts, brands or troubleshooting; omit for the index"`
}

type GuideOut struct {
	Topic    string   `json:"topic"`
	Markdown string   `json:"markdown"`
	Topics   []string `json:"topics"`
}

type ComponentCap struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema" jsonschema:"JSON Schema of data.<name>; also referable as folio:component/<name> in data_schema"`
}

type LayoutCap struct {
	Name          string          `json:"name"`
	OptionsSchema json.RawMessage `json:"options_schema" jsonschema:"JSON Schema of layout.options"`
}

type LimitsOut struct {
	MaxMarkdownBytes       int   `json:"max_markdown_bytes"`
	MaxNesting             int   `json:"max_nesting"`
	MaxTableCells          int   `json:"max_table_cells"`
	MaxAssetBytes          int64 `json:"max_asset_bytes"`
	MaxRequestBytes        int64 `json:"max_request_bytes"`
	MaxPages               int   `json:"max_pages"`
	RenderTimeoutSeconds   int   `json:"render_timeout_seconds"`
	PreviewPagesStored     int   `json:"preview_pages_stored"`
	PreviewRetentionHours  int   `json:"preview_retention_hours"`
	TrashRetentionHours    int   `json:"trash_retention_hours"`
	MaxImagesPerCall       int   `json:"max_images_per_call"`
	MaxListLimit           int   `json:"max_list_limit"`
	IdempotencyWindowHours int   `json:"idempotency_window_hours"`
}

type CapabilitiesOut struct {
	Version         string         `json:"version"`
	Formats         []string       `json:"formats"`
	Languages       []string       `json:"languages" jsonschema:"languages with built-in labels and number formatting"`
	BaseLayouts     []LayoutCap    `json:"base_layouts"`
	Components      []ComponentCap `json:"components"`
	Fonts           []string       `json:"fonts" jsonschema:"font families usable in brands"`
	MathFont        string         `json:"math_font"`
	TypstPackages   []string       `json:"typst_packages" jsonschema:"vendored @preview packages a custom layout may import"`
	LabelKeys       []string       `json:"label_keys" jsonschema:"keys a template may override in labels.<lang>"`
	Limits          LimitsOut      `json:"limits"`
	ReviewerEnabled bool           `json:"reviewer_enabled"`
	SandboxLevel    string         `json:"sandbox_level"`
}

func (d *Deps) capabilities() (*CapabilitiesOut, error) {
	bases, err := bundled.BaseLayouts()
	if err != nil {
		return nil, err
	}
	out := &CapabilitiesOut{Version: d.Version, Formats: []string{"pdf", "docx"}, Languages: brandkit.BuiltinLanguages,
		Fonts: bundled.FontFamilies, MathFont: bundled.MathFont, TypstPackages: d.Packages, LabelKeys: brandkit.LabelKeys,
		ReviewerEnabled: d.Reviewer != nil, SandboxLevel: d.Sandbox}
	if out.TypstPackages == nil {
		out.TypstPackages = []string{}
	}
	for _, b := range bases {
		out.BaseLayouts = append(out.BaseLayouts, LayoutCap{Name: b, OptionsSchema: json.RawMessage(compactJSON(catalog.OptionsSchema))})
	}
	for _, n := range components.Names {
		s, _ := components.Schema(n)
		out.Components = append(out.Components, ComponentCap{Name: n, Schema: json.RawMessage(compactJSON(string(s)))})
	}
	c := d.Config
	out.Limits = LimitsOut{MaxMarkdownBytes: c.MaxMarkdownBytes, MaxNesting: c.MaxNesting, MaxTableCells: c.MaxTableCells,
		MaxAssetBytes: c.MaxAssetBytes, MaxRequestBytes: c.MaxRequestBytes, MaxPages: c.MaxPages,
		RenderTimeoutSeconds: int(c.RenderTimeout.Seconds()), PreviewPagesStored: c.PreviewPagesStored,
		PreviewRetentionHours: int(c.PreviewRetention.Hours()), TrashRetentionHours: int(c.TrashRetention.Hours()),
		MaxImagesPerCall: MaxImages, MaxListLimit: 200, IdempotencyWindowHours: 24}
	return out, nil
}

func compactJSON(s string) string {
	b, err := model.CanonicalBytes([]byte(s))
	if err != nil {
		return s
	}
	return string(b)
}

// ---- brands ----

type ListBrandsIn struct {
	Query           string `json:"query,omitempty" jsonschema:"case-insensitive substring of id or name"`
	IncludeArchived bool   `json:"include_archived,omitempty"`
	Limit           int    `json:"limit,omitempty" jsonschema:"1-200, default 50"`
	Cursor          string `json:"cursor,omitempty" jsonschema:"next_cursor from the previous page"`
}

type BrandSummary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Version  int    `json:"version"`
	Archived bool   `json:"archived"`
}

type ListBrandsOut struct {
	Brands     []BrandSummary `json:"brands"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

type GetIn struct {
	ID      string `json:"id"`
	Version int    `json:"version,omitempty" jsonschema:"omit for the latest version"`
}

type GetBrandOut struct {
	Brand         json.RawMessage   `json:"brand" jsonschema:"the brand spec; valid create_brand/update_brand input"`
	Version       int               `json:"version"`
	LatestVersion int               `json:"latest_version"`
	Archived      bool              `json:"archived"`
	Versions      []catalog.Version `json:"versions"`
}

type CreateBrandIn struct {
	Brand model.Brand `json:"brand" jsonschema:"brand spec: id, name, colors, fonts, logo?, header, footer, docx (see get_guide brands)"`
	Note  string      `json:"note,omitempty" jsonschema:"why this version exists"`
}

type UpdateBrandIn struct {
	ID              string      `json:"id"`
	ExpectedVersion int         `json:"expected_version" jsonschema:"the latest version you read; a mismatch fails with conflict"`
	Brand           model.Brand `json:"brand"`
	Note            string      `json:"note,omitempty"`
}

type VersionOut struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

type ArchiveIn struct {
	ID       string `json:"id"`
	Archived bool   `json:"archived" jsonschema:"true archives, false restores"`
}

type ArchiveOut struct {
	ID       string `json:"id"`
	Archived bool   `json:"archived"`
}

// ---- templates ----

type ListTemplatesIn struct {
	Query           string `json:"query,omitempty" jsonschema:"case-insensitive substring of id, name or description"`
	Category        string `json:"category,omitempty"`
	IncludeArchived bool   `json:"include_archived,omitempty"`
	Limit           int    `json:"limit,omitempty" jsonschema:"1-200, default 50"`
	Cursor          string `json:"cursor,omitempty"`
}

type TemplateSummary struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	BrandID     string   `json:"brand_id"`
	Languages   []string `json:"languages"`
	Version     int      `json:"version"`
	Archived    bool     `json:"archived"`
}

type ListTemplatesOut struct {
	Templates  []TemplateSummary `json:"templates"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

type GetTemplateOut struct {
	Template      json.RawMessage   `json:"template" jsonschema:"the full template spec; valid create_template/update_template input"`
	Version       int               `json:"version"`
	LatestVersion int               `json:"latest_version"`
	Archived      bool              `json:"archived"`
	Versions      []catalog.Version `json:"versions"`
}

type CreateTemplateIn struct {
	Template model.Template `json:"template" jsonschema:"template spec (see get_guide templates)"`
	Note     string         `json:"note,omitempty"`
}

type UpdateTemplateIn struct {
	ID              string         `json:"id"`
	ExpectedVersion int            `json:"expected_version" jsonschema:"the latest version you read; a mismatch fails with conflict"`
	Template        model.Template `json:"template"`
	Note            string         `json:"note,omitempty"`
}

type PreviewTemplateIn struct {
	ID              string `json:"id"`
	Version         int    `json:"version,omitempty"`
	IncludePreviews string `json:"include_previews,omitempty" jsonschema:"none, first (default) or all (up to 6 page images)"`
}

type TemplateResultOut struct {
	ID       string             `json:"id"`
	Version  int                `json:"version"`
	Files    []FileOut          `json:"files" jsonschema:"sample render: pdf and docx; links expire after the preview retention"`
	Previews []string           `json:"previews" jsonschema:"URLs of stored preview PNGs"`
	Warnings []model.Finding    `json:"warnings"`
	Review   *TemplateReviewOut `json:"review,omitempty" jsonschema:"advisory AI review of the sample render (create_template and update_template, only when the server has a reviewer); never blocks the template"`
}

// TemplateReviewOut is the advisory AI review of a template's sample render.
// It is returned, not stored, and never changes the outcome of the call.
type TemplateReviewOut struct {
	Advisory bool             `json:"advisory" jsonschema:"always true: findings are advice, the template was saved"`
	Status   string           `json:"status" jsonschema:"ok, or unavailable when the reviewer failed or timed out (the template was saved anyway)"`
	Pages    []int            `json:"pages" jsonschema:"pages that were reviewed"`
	Findings []review.Finding `json:"findings"`
}

func (c *call) templateResult(ctx context.Context, r *catalog.TemplateResult, mode string) (*TemplateResultOut, error) {
	files, previews, pages := c.outputs(r.Files)
	if err := c.attachPreviews(ctx, r.Files, mode, pages); err != nil {
		return nil, err
	}
	w := r.Warnings
	if w == nil {
		w = []model.Finding{}
	}
	if previews == nil {
		previews = []string{}
	}
	return &TemplateResultOut{ID: r.ID, Version: r.Version, Files: files, Previews: previews, Warnings: w}, nil
}

// reviewTemplate asks the reviewer, when one is configured, to look at the
// sample render of a template that was just saved. Failures are logged and
// reported as status "unavailable"; they never fail the call.
func (c *call) reviewTemplate(ctx context.Context, r *catalog.TemplateResult, t model.Template) *TemplateReviewOut {
	d := c.d
	if d.Reviewer == nil {
		return nil
	}
	out := &TemplateReviewOut{Advisory: true, Status: "unavailable", Pages: []int{}, Findings: []review.Finding{}}
	pageCount := 0
	for _, f := range r.Files {
		if f.Kind == "pdf" {
			pageCount = c.pdfPages(f)
		}
	}
	pages, err := reviewPages(nil, pageCount, d.Config.ReviewMaxPages)
	if err != nil {
		d.Logger.WarnContext(ctx, "ai review of template sample skipped", "template", r.ID, "version", r.Version, "err", err)
		return out
	}
	imgs, err := c.pagePNGs(ctx, r.Files, pages)
	if err != nil {
		d.Logger.WarnContext(ctx, "ai review of template sample skipped", "template", r.ID, "version", r.Version, "err", err)
		return out
	}
	rp := make([]review.Page, 0, len(pages))
	for _, p := range pages {
		rp = append(rp, review.Page{Number: p, PNG: imgs[p]})
	}
	var meta struct {
		Language string `json:"language"`
	}
	_ = json.Unmarshal(t.Sample.Metadata, &meta)
	findings, err := d.Reviewer.Review(ctx, rp, review.Info{TemplateID: r.ID, Language: meta.Language})
	if err != nil {
		d.Logger.WarnContext(ctx, "ai review of template sample failed", "template", r.ID, "version", r.Version, "err", err)
		return out
	}
	out.Status, out.Pages, out.Findings = "ok", pages, review.Normalize(findings, pages)
	return out
}

// reviewNote summarises a template review for the one-line summary.
func reviewNote(r *TemplateReviewOut) string {
	switch {
	case r == nil:
		return ""
	case r.Status != "ok":
		return ", AI review unavailable"
	default:
		return fmt.Sprintf(", %d advisory AI finding(s)", len(r.Findings))
	}
}

func templateSummary(it *catalog.Item) TemplateSummary {
	var t struct {
		Name, Description, Category string
		BrandID                     string   `json:"brand_id"`
		Languages                   []string `json:"languages"`
	}
	_ = json.Unmarshal(it.Spec, &t)
	return TemplateSummary{ID: it.ID, Name: t.Name, Description: t.Description, Category: t.Category, BrandID: t.BrandID,
		Languages: t.Languages, Version: it.Version, Archived: it.Archived}
}

const (
	descGetGuide = `Read Folio's authoring guides (Markdown). Call with no topic for the index. Topics: workflow (tool sequence, revising, publishing), recipes (proposal, quote, statement of work, revising a colleague's document, new brand and template), markdown (supported syntax, images, links), components (budget, milestones, deliverables, signatures, info and their data), templates (spec fields, schemas, sections, samples), typst-layouts (custom layout contract), brands (colors, fonts, logos), troubleshooting (every error and finding code with the fix). Read templates, typst-layouts and brands before create_template or create_brand.`

	descCapabilities = `Report what this Folio instance supports: version, output formats, built-in languages, base layouts with the layout.options schema, components with their data schemas, brand fonts, vendored typst packages, template label keys, limits (sizes, pages, timeouts, retention), whether the AI reviewer is enabled and the sandbox level. Call it once before authoring templates or brands.`

	descListBrands = `List brands (latest versions, sorted by id). Archived brands are hidden unless include_archived=true. Paginate with limit and next_cursor. Use get_brand for the full spec.`

	descGetBrand = `Get a brand spec (latest or a given version) with its version history. The returned brand is valid create_brand / update_brand input. Before update_brand, read the latest version and pass it as expected_version.`

	descCreateBrand = `Create a brand (version 1). Required: id (slug ^[a-z0-9][a-z0-9-]{1,62}$, unique), name, colors (primary, secondary, accent, text, muted, background as #RRGGBB), fonts (heading, body, mono: one of get_capabilities.fonts), header {text, show_logo}, footer {text, legal, page_numbers}, docx {font_fallbacks?}. Optional logo {asset_id from upload_asset, width_mm 5-120}. Fails with validation_failed listing every problem (path per field) or conflict when the id exists. Read get_guide('brands') first.`

	descUpdateBrand = `Create a new version of a brand. expected_version must equal the latest version (get_brand); otherwise it fails with conflict and reports the latest version: re-read, re-apply your change, retry. brand.id must equal id. Existing documents keep the brand version they were rendered with; new renders use the newest version.`

	descArchiveBrand = `Archive (archived=true) or restore (archived=false) a brand. Archived brands are hidden from list_brands and cannot be used for new renders or templates; every version stays available to existing documents. Reversible.`

	descListTemplates = `List document templates (latest versions, sorted by id), the first step of every new document. Filter by query or category; archived templates are hidden unless include_archived=true. Then call get_template for the one that fits.`

	descGetTemplate = `Get a template (latest or a given version): sections (required ones and their aliases), metadata_schema and data_schema (what metadata and data render_document needs), components, body_skeleton (start your Markdown from it), a complete sample, labels, layout, and versions [{version, note, created_by, created_at}]. The template object is valid create_template / update_template input (round-trippable). Read it before writing a document.`

	descCreateTemplate = `Create a template (version 1). Folio validates the spec (schemas compile, sections, components, layout.options, custom typst imports) and renders the template's sample; the call fails with validation_failed if the sample render produces any error or any warning other than placeholder findings, so ship a complete sample. Returns the version, warnings, links to the sample PDF/DOCX and preview PNGs (they expire), and, when the server has an AI reviewer, an advisory review of the sample (never blocking). Read get_guide('templates') and, for custom layouts, get_guide('typst-layouts') first. brand_id must name an existing, unarchived brand.`

	descUpdateTemplate = `Create a new version of a template with the same checks as create_template (spec validation plus a sample render, and the advisory AI review of the sample when enabled). expected_version must equal the latest version (get_template), otherwise conflict with the latest version reported: re-read, re-apply, retry. template.id must equal id. The newest version is used for new renders immediately; existing documents keep their version.`

	descArchiveTemplate = `Archive (archived=true) or restore (archived=false) a template. Archived templates are hidden from list_templates and render_document fails with archived; versions stay available for existing documents. Reversible.`

	descPreviewTemplate = `Render a template's sample (latest or a given version) and return links to the PDF, DOCX and preview PNGs plus warnings. Preview links expire after the preview retention (get_capabilities.limits.preview_retention_hours). include_previews: none, first (default) or all (up to 6 page images as content). Use it to check a template visually.`
)

func registerCatalogTools(s *mcp.Server, d *Deps) {
	register(s, d, tool[GuideIn, GuideOut]{Name: "get_guide", Title: "Read a Folio guide", Description: descGetGuide, Access: readOnly,
		Handle: func(_ context.Context, c *call, in GuideIn) (*GuideOut, error) {
			if in.Topic == "" {
				md := GuideIndex()
				c.Summary("guide index (%d topics)", len(Topics))
				c.Text(md)
				return &GuideOut{Topic: "index", Markdown: md, Topics: Topics}, nil
			}
			md, ok := Guide(in.Topic)
			if !ok {
				return nil, invalid("unknown topic %q; topics: %s", in.Topic, strings.Join(Topics, ", "))
			}
			c.Summary("guide %s", in.Topic)
			c.Text(md)
			return &GuideOut{Topic: in.Topic, Markdown: md, Topics: Topics}, nil
		}})

	register(s, d, tool[struct{}, CapabilitiesOut]{Name: "get_capabilities", Title: "Folio capabilities", Description: descCapabilities, Access: readOnly,
		Handle: func(_ context.Context, c *call, _ struct{}) (*CapabilitiesOut, error) {
			out, err := d.capabilities()
			if err != nil {
				return nil, err
			}
			c.Summary("folio %s: %d base layouts, %d components, sandbox %s", out.Version, len(out.BaseLayouts), len(out.Components), out.SandboxLevel)
			return out, nil
		}})

	register(s, d, tool[ListBrandsIn, ListBrandsOut]{Name: "list_brands", Title: "List brands", Description: descListBrands, Access: readOnly,
		Handle: func(ctx context.Context, c *call, in ListBrandsIn) (*ListBrandsOut, error) {
			limit, err := listLimit(in.Limit)
			if err != nil {
				return nil, err
			}
			items, next, err := d.Catalog.ListBrands(ctx, catalog.ListFilter{Query: in.Query, IncludeArchived: in.IncludeArchived, Limit: limit, Cursor: in.Cursor})
			if err != nil {
				return nil, err
			}
			out := &ListBrandsOut{Brands: []BrandSummary{}, NextCursor: next}
			for _, it := range items {
				var b struct{ Name string }
				_ = json.Unmarshal(it.Spec, &b)
				out.Brands = append(out.Brands, BrandSummary{ID: it.ID, Name: b.Name, Version: it.Version, Archived: it.Archived})
			}
			c.Summary("%d brand(s)", len(out.Brands))
			return out, nil
		}})

	register(s, d, tool[GetIn, GetBrandOut]{Name: "get_brand", Title: "Get a brand", Description: descGetBrand, Access: readOnly,
		Target: func(in GetIn) (string, string) { return "brand", in.ID },
		Handle: func(ctx context.Context, c *call, in GetIn) (*GetBrandOut, error) {
			it, err := d.Catalog.GetBrand(ctx, in.ID, in.Version)
			if err != nil {
				return nil, err
			}
			c.Summary("brand %s v%d (latest v%d)", it.ID, it.Version, it.LatestVersion)
			return &GetBrandOut{Brand: it.Spec, Version: it.Version, LatestVersion: it.LatestVersion, Archived: it.Archived, Versions: it.Versions}, nil
		}})

	register(s, d, tool[CreateBrandIn, VersionOut]{Name: "create_brand", Title: "Create a brand", Description: descCreateBrand, Access: write,
		Target: func(in CreateBrandIn) (string, string) { return "brand", in.Brand.ID },
		Record: func(_ CreateBrandIn, out *VersionOut) (string, activity.Detail) {
			return "", activity.Detail{Version: out.Version}
		},
		Handle: func(ctx context.Context, c *call, in CreateBrandIn) (*VersionOut, error) {
			v, err := d.Catalog.CreateBrand(ctx, c.Actor, in.Brand, in.Note)
			if err != nil {
				return nil, err
			}
			c.Summary("created brand %s v%d", in.Brand.ID, v)
			return &VersionOut{ID: in.Brand.ID, Version: v}, nil
		}})

	register(s, d, tool[UpdateBrandIn, VersionOut]{Name: "update_brand", Title: "Update a brand", Description: descUpdateBrand, Access: write,
		Target: func(in UpdateBrandIn) (string, string) { return "brand", in.ID },
		Record: func(_ UpdateBrandIn, out *VersionOut) (string, activity.Detail) {
			return "", activity.Detail{Version: out.Version}
		},
		Handle: func(ctx context.Context, c *call, in UpdateBrandIn) (*VersionOut, error) {
			v, err := d.Catalog.UpdateBrand(ctx, c.Actor, in.ID, in.ExpectedVersion, in.Brand, in.Note)
			if err != nil {
				return nil, err
			}
			c.Summary("brand %s is now v%d", in.ID, v)
			return &VersionOut{ID: in.ID, Version: v}, nil
		}})

	register(s, d, tool[ArchiveIn, ArchiveOut]{Name: "archive_brand", Title: "Archive or restore a brand", Description: descArchiveBrand, Access: write, Idempotent: true,
		Target: func(in ArchiveIn) (string, string) { return "brand", in.ID },
		Record: func(in ArchiveIn, _ *ArchiveOut) (string, activity.Detail) {
			return "", activity.Detail{Archived: &in.Archived}
		},
		Handle: func(ctx context.Context, c *call, in ArchiveIn) (*ArchiveOut, error) {
			if err := d.Catalog.ArchiveBrand(ctx, c.Actor, in.ID, in.Archived); err != nil {
				return nil, err
			}
			c.Summary("brand %s archived=%t", in.ID, in.Archived)
			return &ArchiveOut{ID: in.ID, Archived: in.Archived}, nil
		}})

	register(s, d, tool[ListTemplatesIn, ListTemplatesOut]{Name: "list_templates", Title: "List templates", Description: descListTemplates, Access: readOnly,
		Handle: func(ctx context.Context, c *call, in ListTemplatesIn) (*ListTemplatesOut, error) {
			limit, err := listLimit(in.Limit)
			if err != nil {
				return nil, err
			}
			items, next, err := d.Catalog.ListTemplates(ctx, catalog.ListFilter{Query: in.Query, Category: in.Category,
				IncludeArchived: in.IncludeArchived, Limit: limit, Cursor: in.Cursor})
			if err != nil {
				return nil, err
			}
			out := &ListTemplatesOut{Templates: []TemplateSummary{}, NextCursor: next}
			for _, it := range items {
				out.Templates = append(out.Templates, templateSummary(it))
			}
			c.Summary("%d template(s)", len(out.Templates))
			return out, nil
		}})

	register(s, d, tool[GetIn, GetTemplateOut]{Name: "get_template", Title: "Get a template", Description: descGetTemplate, Access: readOnly,
		Target: func(in GetIn) (string, string) { return "template", in.ID },
		Handle: func(ctx context.Context, c *call, in GetIn) (*GetTemplateOut, error) {
			it, err := d.Catalog.GetTemplate(ctx, in.ID, in.Version)
			if err != nil {
				return nil, err
			}
			c.Summary("template %s v%d (latest v%d, %d versions)", it.ID, it.Version, it.LatestVersion, len(it.Versions))
			return &GetTemplateOut{Template: it.Spec, Version: it.Version, LatestVersion: it.LatestVersion, Archived: it.Archived, Versions: it.Versions}, nil
		}})

	register(s, d, tool[CreateTemplateIn, TemplateResultOut]{Name: "create_template", Title: "Create a template", Description: descCreateTemplate, Access: write,
		Target: func(in CreateTemplateIn) (string, string) { return "template", in.Template.ID },
		Record: func(_ CreateTemplateIn, out *TemplateResultOut) (string, activity.Detail) {
			return "", activity.Detail{Version: out.Version}
		},
		Handle: func(ctx context.Context, c *call, in CreateTemplateIn) (*TemplateResultOut, error) {
			p := c.startProgress(ctx)
			defer p.Stop()
			r, err := d.Catalog.CreateTemplate(p.Context(ctx), c.Actor, in.Template, in.Note)
			if err != nil {
				return nil, err
			}
			out, err := c.templateResult(ctx, r, previewsNone)
			if err != nil {
				return nil, err
			}
			if d.Reviewer != nil {
				p.Step(ctx, "reviewing the sample")
			}
			out.Review = c.reviewTemplate(ctx, r, in.Template)
			c.Summary("created template %s v%d (%d warning(s)%s)", r.ID, r.Version, len(r.Warnings), reviewNote(out.Review))
			return out, nil
		}})

	register(s, d, tool[UpdateTemplateIn, TemplateResultOut]{Name: "update_template", Title: "Update a template", Description: descUpdateTemplate, Access: write,
		Target: func(in UpdateTemplateIn) (string, string) { return "template", in.ID },
		Record: func(_ UpdateTemplateIn, out *TemplateResultOut) (string, activity.Detail) {
			return "", activity.Detail{Version: out.Version}
		},
		Handle: func(ctx context.Context, c *call, in UpdateTemplateIn) (*TemplateResultOut, error) {
			p := c.startProgress(ctx)
			defer p.Stop()
			r, err := d.Catalog.UpdateTemplate(p.Context(ctx), c.Actor, in.ID, in.ExpectedVersion, in.Template, in.Note)
			if err != nil {
				return nil, err
			}
			out, err := c.templateResult(ctx, r, previewsNone)
			if err != nil {
				return nil, err
			}
			if d.Reviewer != nil {
				p.Step(ctx, "reviewing the sample")
			}
			out.Review = c.reviewTemplate(ctx, r, in.Template)
			c.Summary("template %s is now v%d (%d warning(s)%s)", r.ID, r.Version, len(r.Warnings), reviewNote(out.Review))
			return out, nil
		}})

	register(s, d, tool[ArchiveIn, ArchiveOut]{Name: "archive_template", Title: "Archive or restore a template", Description: descArchiveTemplate, Access: write, Idempotent: true,
		Target: func(in ArchiveIn) (string, string) { return "template", in.ID },
		Record: func(in ArchiveIn, _ *ArchiveOut) (string, activity.Detail) {
			return "", activity.Detail{Archived: &in.Archived}
		},
		Handle: func(ctx context.Context, c *call, in ArchiveIn) (*ArchiveOut, error) {
			if err := d.Catalog.ArchiveTemplate(ctx, c.Actor, in.ID, in.Archived); err != nil {
				return nil, err
			}
			c.Summary("template %s archived=%t", in.ID, in.Archived)
			return &ArchiveOut{ID: in.ID, Archived: in.Archived}, nil
		}})

	register(s, d, tool[PreviewTemplateIn, TemplateResultOut]{Name: "preview_template", Title: "Preview a template", Description: descPreviewTemplate,
		Access: write, Idempotent: true,
		Target: func(in PreviewTemplateIn) (string, string) { return "template", in.ID },
		Record: func(_ PreviewTemplateIn, out *TemplateResultOut) (string, activity.Detail) {
			return "", activity.Detail{Version: out.Version}
		},
		Handle: func(ctx context.Context, c *call, in PreviewTemplateIn) (*TemplateResultOut, error) {
			mode, err := previewMode(in.IncludePreviews)
			if err != nil {
				return nil, err
			}
			p := c.startProgress(ctx)
			defer p.Stop()
			r, err := d.Catalog.PreviewTemplate(p.Context(ctx), in.ID, in.Version)
			if err != nil {
				return nil, err
			}
			d.audit(ctx, c.Actor, "preview_template", "template", in.ID, "ok")
			c.Summary("template %s v%d sample rendered (%d warning(s))", r.ID, r.Version, len(r.Warnings))
			return c.templateResult(ctx, r, mode)
		}})
}
