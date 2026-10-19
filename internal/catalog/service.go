// SPDX-License-Identifier: AGPL-3.0-only

package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	bundled "github.com/vocdoni/folio/assets"
	"github.com/vocdoni/folio/internal/brandkit"
	"github.com/vocdoni/folio/internal/components"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/render"
	"github.com/vocdoni/folio/internal/store"
	"github.com/vocdoni/folio/internal/validate"
)

// Renderer renders template samples (render.Pipeline implements it).
type Renderer interface {
	Render(ctx context.Context, req render.Request) (*render.Result, error)
}

// Service is the catalog API used by P2's MCP tools and by seeding.
type Service struct {
	DB              *store.DB
	Blobs           *store.Blobs
	Renderer        Renderer
	Assets          model.AssetResolver
	PackagesDir     string // vendored typst packages (allowlist source)
	Now             func() time.Time
	PreviewTTL      time.Duration // FOLIO_PREVIEW_RETENTION
	PreviewPagesMax int           // FOLIO_PREVIEW_PAGES_STORED
}

// File is a stored output row (also used by documents).
type File struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"` // pdf | docx | preview
	Page      int       `json:"page,omitempty"`
	Name      string    `json:"name"`
	MIME      string    `json:"mime"`
	SHA256    string    `json:"sha256"`
	Size      int       `json:"size"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
}

// TemplateResult is returned by CreateTemplate/UpdateTemplate/PreviewTemplate.
type TemplateResult struct {
	ID       string
	Version  int
	Warnings []model.Finding
	Files    []File // sample render: pdf, docx, first PreviewPagesMax preview pages
}

var (
	colorRE = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	langRE  = regexp.MustCompile(`^[a-z]{2,3}$`)
)

func bad(path, msg string) model.Finding {
	return model.Finding{Code: model.CodeSchemaViolation, Severity: model.SeverityError, Path: path, Message: msg}
}

// ValidateBrand checks id, colours, bundled fonts and the logo asset.
func (s *Service) ValidateBrand(ctx context.Context, b model.Brand) []model.Finding {
	var fs []model.Finding
	if !model.SlugRE.MatchString(b.ID) {
		fs = append(fs, bad("/id", "id must match ^[a-z0-9][a-z0-9-]{1,62}$"))
	}
	if strings.TrimSpace(b.Name) == "" {
		fs = append(fs, bad("/name", "name is required"))
	}
	for path, c := range map[string]string{"/colors/primary": b.Colors.Primary, "/colors/secondary": b.Colors.Secondary,
		"/colors/accent": b.Colors.Accent, "/colors/text": b.Colors.Text, "/colors/muted": b.Colors.Muted, "/colors/background": b.Colors.Background} {
		if !colorRE.MatchString(c) {
			fs = append(fs, bad(path, fmt.Sprintf("%q must be #RRGGBB", c)))
		}
	}
	for path, f := range map[string]string{"/fonts/heading": b.Fonts.Heading, "/fonts/body": b.Fonts.Body, "/fonts/mono": b.Fonts.Mono} {
		if !slices.Contains(bundled.FontFamilies, f) {
			fs = append(fs, bad(path, fmt.Sprintf("font %q is not bundled; use one of %s", f, strings.Join(bundled.FontFamilies, ", "))))
		}
	}
	if b.Logo != nil {
		if b.Logo.WidthMM < 5 || b.Logo.WidthMM > 120 {
			fs = append(fs, bad("/logo/width_mm", "width_mm must be between 5 and 120"))
		}
		if a, err := s.Assets.Asset(ctx, b.Logo.AssetID, false); err != nil {
			fs = append(fs, model.Finding{Code: model.CodeMissingAsset, Severity: model.SeverityError, Path: "/logo/asset_id",
				Message: fmt.Sprintf("asset %q does not exist or was deleted", b.Logo.AssetID)})
		} else if a.MIME == "image/jpeg" {
			fs = append(fs, bad("/logo/asset_id", "the logo must be a PNG or SVG asset (JPEG has no lossless DOCX rendition)"))
		}
	}
	return fs
}

// OptionsSchema is the layout.options schema (same for every base layout),
// published by get_capabilities.
const OptionsSchema = `{"type":"object","additionalProperties":false,"properties":{
 "cover":{"type":"boolean"},"toc":{"type":"boolean"},
 "page_size":{"enum":["a4","letter"]},"heading_numbering":{"type":"boolean"}}}`

// ValidateTemplate checks the template spec (without rendering the sample).
func (s *Service) ValidateTemplate(t model.Template) []model.Finding {
	var fs []model.Finding
	if !model.SlugRE.MatchString(t.ID) {
		fs = append(fs, bad("/id", "id must match ^[a-z0-9][a-z0-9-]{1,62}$"))
	}
	if !model.SlugRE.MatchString(t.BrandID) {
		fs = append(fs, bad("/brand_id", "brand_id must name an existing brand"))
	}
	if strings.TrimSpace(t.Name) == "" {
		fs = append(fs, bad("/name", "name is required"))
	}
	if len(t.Languages) == 0 {
		fs = append(fs, bad("/languages", "at least one language is required"))
	}
	for i, l := range t.Languages {
		if !langRE.MatchString(l) {
			fs = append(fs, bad(fmt.Sprintf("/languages/%d", i), "languages are BCP-47 primary tags such as en, es, ca"))
		}
	}
	bases, _ := bundled.BaseLayouts()
	if !slices.Contains(bases, t.Layout.Base) {
		fs = append(fs, bad("/layout/base", "base must be one of "+strings.Join(bases, ", ")))
	}
	if _, err := render.LayoutOptions(t.Layout.Options); err != nil {
		fs = append(fs, bad("/layout/options", err.Error()))
	}
	if t.Layout.Typst != nil {
		allow, _ := bundled.VendoredPackages(s.PackagesDir)
		fs = append(fs, render.ScanTypstImports(*t.Layout.Typst, allow)...)
		if !strings.Contains(*t.Layout.Typst, "folio-layout") {
			fs = append(fs, bad("/layout/typst", "a custom layout must define folio-layout(meta:, brand:, labels:, options:, body)"))
		}
	}
	for i, c := range t.Components {
		if !components.Known(c) {
			fs = append(fs, bad(fmt.Sprintf("/components/%d", i), "unknown component "+c))
		}
	}
	declared := map[string]string{} // component -> title of the section declaring it
	for i, sec := range t.Sections {
		if strings.TrimSpace(sec.Title) == "" {
			fs = append(fs, bad(fmt.Sprintf("/sections/%d/title", i), "section title is required"))
		}
		if sec.Component != "" && !slices.Contains(t.Components, sec.Component) {
			fs = append(fs, bad(fmt.Sprintf("/sections/%d/component", i), "component must also be listed in components"))
		}
		if sec.Component != "" {
			if first, dup := declared[sec.Component]; dup {
				fs = append(fs, bad(fmt.Sprintf("/sections/%d/component", i),
					fmt.Sprintf("component %s is already declared by section %q; a component sits in one section", sec.Component, first)))
			} else {
				declared[sec.Component] = sec.Title
			}
		}
	}
	for lang, table := range t.Labels {
		for k := range table {
			if !slices.Contains(brandkit.LabelKeys, k) && !slices.Contains(brandkit.RetiredLabelKeys, k) {
				fs = append(fs, bad("/labels/"+lang+"/"+k, "unknown label key"))
			}
		}
	}
	_, sf := validate.CompileSchemas(t)
	return append(fs, sf...)
}

// canonicalSpec stamps spec_version and returns canonical JSON.
func canonicalSpec(v any) ([]byte, error) { return model.Canonical(v) }

// CreateBrand validates and stores version 1.
func (s *Service) CreateBrand(ctx context.Context, actor string, b model.Brand, note string) (int, error) {
	return s.putBrand(ctx, actor, b, note, true, 0)
}

// UpdateBrand stores latest+1 when expectedVersion matches.
func (s *Service) UpdateBrand(ctx context.Context, actor, id string, expectedVersion int, b model.Brand, note string) (int, error) {
	if b.ID != id {
		return 0, &model.Error{Code: model.ErrInvalidInput, Message: "brand.id must equal id"}
	}
	return s.putBrand(ctx, actor, b, note, false, expectedVersion)
}

func (s *Service) putBrand(ctx context.Context, actor string, b model.Brand, note string, create bool, expected int) (int, error) {
	if fs := s.ValidateBrand(ctx, b); model.HasErrors(fs) {
		return 0, &model.Error{Code: model.ErrValidationFailed, Message: "the brand is invalid; see details", Details: fs}
	}
	b.SpecVersion = model.SpecVersion
	spec, err := canonicalSpec(b)
	if err != nil {
		return 0, err
	}
	return putVersion(ctx, s.DB, KindBrand, b.ID, create, expected, spec, note, actor, s.Now(), nil)
}

// CreateTemplate validates, renders the sample (any error, or any warning
// other than placeholder findings, fails) and stores version 1 with preview files.
func (s *Service) CreateTemplate(ctx context.Context, actor string, t model.Template, note string) (*TemplateResult, error) {
	return s.putTemplate(ctx, actor, t, note, true, 0)
}

// UpdateTemplate is CreateTemplate for latest+1 with expected_version.
func (s *Service) UpdateTemplate(ctx context.Context, actor, id string, expectedVersion int, t model.Template, note string) (*TemplateResult, error) {
	if t.ID != id {
		return nil, &model.Error{Code: model.ErrInvalidInput, Message: "template.id must equal id"}
	}
	return s.putTemplate(ctx, actor, t, note, false, expectedVersion)
}

// checkTemplate validates the spec structurally and resolves its brand.
func (s *Service) checkTemplate(ctx context.Context, t model.Template) (model.Brand, int, error) {
	if fs := s.ValidateTemplate(t); model.HasErrors(fs) {
		return model.Brand{}, 0, &model.Error{Code: model.ErrValidationFailed, Message: "the template is invalid; see details", Details: fs}
	}
	return s.ResolveBrand(ctx, t.BrandID, 0)
}

// sampleRender renders the sample with formats (nil = pdf and docx) and
// applies create_template's policy: any warning other than a placeholder
// fails the render.
func (s *Service) sampleRender(ctx context.Context, t model.Template, b model.Brand, bv int, formats []model.Format) (*render.Result, error) {
	res, err := s.Renderer.Render(ctx, render.Request{Template: t, Brand: b, BrandVersion: bv,
		Input: model.DocumentInput{Markdown: t.Sample.Markdown, Metadata: t.Sample.Metadata, Data: t.Sample.Data, Formats: formats}})
	if err != nil {
		return nil, err
	}
	for _, w := range res.Warnings {
		if w.Code != model.CodePlaceholder {
			return res, &model.Error{Code: model.ErrValidationFailed, Details: res.Warnings,
				Message: "rendering the sample produced warnings; fix the sample or the layout"}
		}
	}
	return res, nil
}

// DryRunTemplate runs everything create_template runs before saving —
// structural validation, brand resolution, the sample render with formats
// (nil = pdf and docx) and the same error/warning policy — and writes no
// blobs and no rows. It returns the sample's warnings and render result, or
// an error (*model.Error with findings in Details for invalid specs).
func (s *Service) DryRunTemplate(ctx context.Context, t model.Template, formats []model.Format) ([]model.Finding, *render.Result, error) {
	brand, brandVersion, err := s.checkTemplate(ctx, t)
	if err != nil {
		return nil, nil, err
	}
	res, err := s.sampleRender(ctx, t, brand, brandVersion, formats)
	if err != nil {
		return nil, nil, err
	}
	return res.Warnings, res, nil
}

func (s *Service) putTemplate(ctx context.Context, actor string, t model.Template, note string, create bool, expected int) (*TemplateResult, error) {
	brand, brandVersion, err := s.checkTemplate(ctx, t)
	if err != nil {
		return nil, err
	}
	// Fail fast on a stale expected_version before the (expensive) sample render;
	// putVersion re-checks inside the transaction.
	if !create {
		if cur, err := get(ctx, s.DB, KindTemplate, t.ID, 0); err != nil {
			return nil, err
		} else if cur.LatestVersion != expected {
			return nil, conflict(KindTemplate, t.ID, cur.LatestVersion)
		}
	}
	res, err := s.sampleRender(ctx, t, brand, brandVersion, nil)
	if err != nil {
		return nil, err
	}
	t.SpecVersion = model.SpecVersion
	spec, err := canonicalSpec(t)
	if err != nil {
		return nil, err
	}
	files, now, err := s.previewBlobs(res, t.ID) // Blobs.Put only; no DB
	if err != nil {
		return nil, err
	}
	v, err := putVersion(ctx, s.DB, KindTemplate, t.ID, create, expected, spec, note, actor, now,
		func(tx *sql.Tx, version int) error { return s.insertPreviewRows(ctx, tx, t.ID, version, files, now) })
	if err != nil {
		return nil, err
	}
	return &TemplateResult{ID: t.ID, Version: v, Warnings: res.Warnings, Files: files}, nil
}

func (s *Service) renderSample(ctx context.Context, t model.Template, version int, b model.Brand, bv int) (*render.Result, error) {
	return s.Renderer.Render(ctx, render.Request{Template: t, TemplateVersion: version, Brand: b, BrandVersion: bv,
		Input: model.DocumentInput{Markdown: t.Sample.Markdown, Metadata: t.Sample.Metadata, Data: t.Sample.Data}})
}

// PreviewTemplate renders the sample of id@version (0 = latest); files expire after PreviewTTL.
func (s *Service) PreviewTemplate(ctx context.Context, id string, version int) (*TemplateResult, error) {
	t, v, err := s.ResolveTemplate(ctx, id, version, true)
	if err != nil {
		return nil, err
	}
	b, bv, err := s.ResolveBrand(ctx, t.BrandID, 0)
	if err != nil {
		return nil, err
	}
	res, err := s.renderSample(ctx, t, v, b, bv)
	if err != nil {
		return nil, err
	}
	files, now, err := s.previewBlobs(res, id)
	if err != nil {
		return nil, err
	}
	if err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error { return s.insertPreviewRows(ctx, tx, id, v, files, now) }); err != nil {
		return nil, err
	}
	return &TemplateResult{ID: id, Version: v, Warnings: res.Warnings, Files: files}, nil
}

// previewBlobs writes the preview blobs (PDF, DOCX, the first PreviewPagesMax
// pages) and returns their file records, expiring PreviewTTL after the
// returned time. It never touches the database: blobs are written before any
// transaction so the single writer connection is never held during disk I/O.
func (s *Service) previewBlobs(res *render.Result, id string) ([]File, time.Time, error) {
	now := s.Now()
	exp := now.Add(s.PreviewTTL)
	type out struct {
		kind, name, mime string
		page             int
		data             []byte
	}
	var outs []out
	if res.PDF != nil {
		outs = append(outs, out{"pdf", id + ".pdf", "application/pdf", 0, res.PDF})
	}
	if res.DOCX != nil {
		outs = append(outs, out{"docx", id + ".docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", 0, res.DOCX})
	}
	for i, p := range res.Pages {
		if i >= s.PreviewPagesMax {
			break
		}
		outs = append(outs, out{"preview", fmt.Sprintf("%s-page-%d.png", id, i+1), "image/png", i + 1, p})
	}
	files := make([]File, 0, len(outs))
	for _, o := range outs {
		sum, err := s.Blobs.Put(o.data)
		if err != nil {
			return nil, now, err
		}
		files = append(files, File{ID: uuid.NewString(), Kind: o.kind, Page: o.page, Name: o.name, MIME: o.mime, SHA256: sum, Size: len(o.data), ExpiresAt: exp})
	}
	return files, now, nil
}

// insertPreviewRows inserts the template-owned, expiring file rows for
// id@version inside tx.
func (s *Service) insertPreviewRows(ctx context.Context, tx *sql.Tx, id string, version int, files []File, now time.Time) error {
	for _, f := range files {
		var page any
		if f.Page > 0 {
			page = f.Page
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO files (id, template_id, template_version, kind, page, name, mime, sha256, size, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, f.ID, id, version, f.Kind, page, f.Name, f.MIME, f.SHA256, f.Size, store.Now(now), store.Now(f.ExpiresAt)); err != nil {
			return fmt.Errorf("catalog: insert preview file: %w", err)
		}
	}
	return nil
}

// ResolveBrand returns brand id@version (0 = latest); archived brands are rejected.
func (s *Service) ResolveBrand(ctx context.Context, id string, version int) (model.Brand, int, error) {
	it, err := get(ctx, s.DB, KindBrand, id, version)
	if err != nil {
		return model.Brand{}, 0, err
	}
	if it.Archived {
		return model.Brand{}, 0, &model.Error{Code: model.ErrArchived, Message: fmt.Sprintf("brand %s is archived; unarchive it or pick another brand", id)}
	}
	var b model.Brand
	if err := json.Unmarshal(it.Spec, &b); err != nil {
		return model.Brand{}, 0, fmt.Errorf("catalog: decode brand: %w", err)
	}
	return b, it.Version, nil
}

// ResolveTemplate returns template id@version (0 = latest). New renders pass
// allowArchived=false and get `archived` for archived templates.
func (s *Service) ResolveTemplate(ctx context.Context, id string, version int, allowArchived bool) (model.Template, int, error) {
	it, err := get(ctx, s.DB, KindTemplate, id, version)
	if err != nil {
		return model.Template{}, 0, err
	}
	if it.Archived && !allowArchived {
		return model.Template{}, 0, &model.Error{Code: model.ErrArchived, Message: fmt.Sprintf("template %s is archived; unarchive it with archive_template(archived=false)", id)}
	}
	var t model.Template
	dec := json.NewDecoder(bytes.NewReader(it.Spec))
	if err := dec.Decode(&t); err != nil {
		return model.Template{}, 0, fmt.Errorf("catalog: decode template: %w", err)
	}
	return t, it.Version, nil
}

// GetBrand / GetTemplate return the stored item with history.
func (s *Service) GetBrand(ctx context.Context, id string, version int) (*Item, error) {
	return get(ctx, s.DB, KindBrand, id, version)
}

func (s *Service) GetTemplate(ctx context.Context, id string, version int) (*Item, error) {
	return get(ctx, s.DB, KindTemplate, id, version)
}

// ListBrands / ListTemplates page through latest versions by id.
func (s *Service) ListBrands(ctx context.Context, f ListFilter) ([]*Item, string, error) {
	return list(ctx, s.DB, KindBrand, f)
}

func (s *Service) ListTemplates(ctx context.Context, f ListFilter) ([]*Item, string, error) {
	return list(ctx, s.DB, KindTemplate, f)
}

// ArchiveBrand / ArchiveTemplate are reversible.
func (s *Service) ArchiveBrand(ctx context.Context, actor, id string, archived bool) error {
	return setArchived(ctx, s.DB, KindBrand, id, archived, actor, s.Now())
}

func (s *Service) ArchiveTemplate(ctx context.Context, actor, id string, archived bool) error {
	return setArchived(ctx, s.DB, KindTemplate, id, archived, actor, s.Now())
}

// IsConflict reports whether err is a catalog/documents conflict.
func IsConflict(err error) bool {
	var me *model.Error
	return errors.As(err, &me) && me.Code == model.ErrConflict
}
