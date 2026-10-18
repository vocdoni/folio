// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"encoding/json"
	"math"
	"net/http"

	"github.com/vocdoni/folio/internal/activity"
	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/validate"
)

func (s *Server) catalogRoutes(mux *http.ServeMux) {
	s.api(mux, "GET /ui/api/templates", route{}, s.handleListTemplates)
	s.api(mux, "POST /ui/api/templates", route{action: "ui.template.create", kind: "template"}, s.handleCreateTemplate)
	s.api(mux, "GET /ui/api/templates/{id}", route{}, s.handleGetTemplate)
	s.api(mux, "PUT /ui/api/templates/{id}", route{action: "ui.template.update", kind: "template"}, s.handleUpdateTemplate)
	s.api(mux, "POST /ui/api/templates/{id}/archive", route{action: "ui.template.archive", kind: "template"}, s.handleArchive(catalog.KindTemplate))
	s.api(mux, "GET /ui/api/brands", route{}, s.handleListBrands)
	s.api(mux, "POST /ui/api/brands", route{action: "ui.brand.create", kind: "brand"}, s.handleCreateBrand)
	s.api(mux, "GET /ui/api/brands/{id}", route{}, s.handleGetBrand)
	s.api(mux, "PUT /ui/api/brands/{id}", route{action: "ui.brand.update", kind: "brand"}, s.handleUpdateBrand)
	s.api(mux, "POST /ui/api/brands/{id}/archive", route{action: "ui.brand.archive", kind: "brand"}, s.handleArchive(catalog.KindBrand))
}

// SectionOut is a template section in list entries.
type SectionOut struct {
	Title     string `json:"title"`
	Required  bool   `json:"required,omitempty"`
	Component string `json:"component,omitempty"`
}

// TemplateSummaryOut is one templates list entry.
type TemplateSummaryOut struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Category    string       `json:"category"`
	BrandID     string       `json:"brand_id"`
	Languages   []string     `json:"languages"`
	Layout      string       `json:"layout"` // base layout, or "custom"
	Sections    []SectionOut `json:"sections"`
	Version     int          `json:"version"`
	Archived    bool         `json:"archived"`
}

// TemplateListOut is GET /ui/api/templates.
type TemplateListOut struct {
	Items      []TemplateSummaryOut `json:"items"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

func listFilter(r *http.Request) (catalog.ListFilter, error) {
	limit, err := intParam(r, "limit", 200, 1, 200)
	q := r.URL.Query()
	return catalog.ListFilter{Query: q.Get("query"), Category: q.Get("category"), IncludeArchived: boolParam(r, "include_archived"),
		Limit: limit, Cursor: q.Get("cursor")}, err
}

func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	f, err := listFilter(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	items, next, err := s.o.Catalog.ListTemplates(r.Context(), f)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := TemplateListOut{Items: []TemplateSummaryOut{}, NextCursor: next}
	for _, it := range items {
		var t model.Template
		if err := json.Unmarshal(it.Spec, &t); err != nil {
			writeError(w, r, err)
			return
		}
		o := TemplateSummaryOut{ID: it.ID, Name: t.Name, Description: t.Description, Category: t.Category, BrandID: t.BrandID,
			Languages: t.Languages, Layout: t.Layout.Base, Sections: []SectionOut{}, Version: it.Version, Archived: it.Archived}
		if t.Layout.Typst != nil {
			o.Layout = "custom"
		}
		for _, sec := range t.Sections {
			o.Sections = append(o.Sections, SectionOut{Title: sec.Title, Required: sec.Required, Component: sec.Component})
		}
		out.Items = append(out.Items, o)
	}
	writeJSON(w, http.StatusOK, out)
}

// ItemOut is a stored brand or template version with its history.
type ItemOut struct {
	ID            string            `json:"id"`
	Version       int               `json:"version"`
	LatestVersion int               `json:"latest_version"`
	Archived      bool              `json:"archived"`
	Spec          json.RawMessage   `json:"spec"`
	Versions      []catalog.Version `json:"versions"`
	// CoreMetadataSchema is the metadata every document of this template
	// has (title, date, language…), combined with the template's own schema.
	CoreMetadataSchema json.RawMessage `json:"core_metadata_schema,omitempty"`
}

func (s *Server) getItem(w http.ResponseWriter, r *http.Request, kind catalog.Kind) {
	v, err := intParam(r, "version", 0, 0, math.MaxInt32)
	if err != nil {
		writeError(w, r, err)
		return
	}
	get := s.o.Catalog.GetBrand
	if kind == catalog.KindTemplate {
		get = s.o.Catalog.GetTemplate
	}
	it, err := get(r.Context(), r.PathValue("id"), v)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := ItemOut{ID: it.ID, Version: it.Version, LatestVersion: it.LatestVersion, Archived: it.Archived, Spec: it.Spec, Versions: it.Versions}
	if kind == catalog.KindTemplate {
		var t model.Template
		if err := json.Unmarshal(it.Spec, &t); err == nil {
			out.CoreMetadataSchema = validate.CoreMetadataSchema(t.Languages)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetTemplate(w http.ResponseWriter, r *http.Request) {
	s.getItem(w, r, catalog.KindTemplate)
}
func (s *Server) handleGetBrand(w http.ResponseWriter, r *http.Request) {
	s.getItem(w, r, catalog.KindBrand)
}

// TemplateIn is POST /ui/api/templates and PUT /ui/api/templates/{id}.
type TemplateIn struct {
	Template        model.Template `json:"template"`
	ExpectedVersion int            `json:"expected_version,omitempty"` // PUT only
	Note            string         `json:"note,omitempty"`
}

// TemplateSavedOut is the result of saving a template version.
type TemplateSavedOut struct {
	ID       string          `json:"id"`
	Version  int             `json:"version"`
	Warnings []model.Finding `json:"warnings"`
	Files    []FileOut       `json:"files"` // sample render (expiring links)
}

func (s *Server) templateSaved(w http.ResponseWriter, r *http.Request, res *catalog.TemplateResult) {
	out := TemplateSavedOut{ID: res.ID, Version: res.Version, Warnings: res.Warnings, Files: []FileOut{}}
	if out.Warnings == nil {
		out.Warnings = []model.Finding{}
	}
	for _, f := range res.Files {
		out.Files = append(out.Files, FileOut{Kind: f.Kind, Name: f.Name, MIME: f.MIME, Size: f.Size, Page: f.Page, URL: s.fileURL(f)})
	}
	noteActivity(r, res.ID, activity.Detail{Version: res.Version})
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateTemplate(w http.ResponseWriter, r *http.Request) {
	var in TemplateIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	noteActivity(r, in.Template.ID, activity.Detail{})
	res, err := s.o.Catalog.CreateTemplate(r.Context(), Actor, in.Template, in.Note)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.templateSaved(w, r, res)
}

func (s *Server) handleUpdateTemplate(w http.ResponseWriter, r *http.Request) {
	var in TemplateIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	res, err := s.o.Catalog.UpdateTemplate(r.Context(), Actor, r.PathValue("id"), in.ExpectedVersion, in.Template, in.Note)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.templateSaved(w, r, res)
}

// ArchiveIn archives (true) or restores (false) a brand or template.
type ArchiveIn struct {
	Archived bool `json:"archived"`
}

func (s *Server) handleArchive(kind catalog.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in ArchiveIn
		if err := decodeJSON(r, &in); err != nil {
			writeError(w, r, err)
			return
		}
		archive := s.o.Catalog.ArchiveBrand
		if kind == catalog.KindTemplate {
			archive = s.o.Catalog.ArchiveTemplate
		}
		noteActivity(r, "", activity.Detail{Archived: &in.Archived})
		if err := archive(r.Context(), Actor, r.PathValue("id"), in.Archived); err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("id"), "archived": in.Archived})
	}
}

// BrandSummaryOut is one brands list entry (the full spec: brands are small).
type BrandSummaryOut struct {
	ID       string          `json:"id"`
	Version  int             `json:"version"`
	Archived bool            `json:"archived"`
	Spec     json.RawMessage `json:"spec"`
}

func (s *Server) handleListBrands(w http.ResponseWriter, r *http.Request) {
	f, err := listFilter(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	items, next, err := s.o.Catalog.ListBrands(r.Context(), f)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := struct {
		Items      []BrandSummaryOut `json:"items"`
		NextCursor string            `json:"next_cursor,omitempty"`
	}{Items: []BrandSummaryOut{}, NextCursor: next}
	for _, it := range items {
		out.Items = append(out.Items, BrandSummaryOut{ID: it.ID, Version: it.Version, Archived: it.Archived, Spec: it.Spec})
	}
	writeJSON(w, http.StatusOK, out)
}

// BrandIn is POST /ui/api/brands and PUT /ui/api/brands/{id}.
type BrandIn struct {
	Brand           model.Brand `json:"brand"`
	ExpectedVersion int         `json:"expected_version,omitempty"`
	Note            string      `json:"note,omitempty"`
}

func (s *Server) handleCreateBrand(w http.ResponseWriter, r *http.Request) {
	var in BrandIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	noteActivity(r, in.Brand.ID, activity.Detail{})
	v, err := s.o.Catalog.CreateBrand(r.Context(), Actor, in.Brand, in.Note)
	if err != nil {
		writeError(w, r, err)
		return
	}
	noteActivity(r, in.Brand.ID, activity.Detail{Version: v})
	writeJSON(w, http.StatusOK, map[string]any{"id": in.Brand.ID, "version": v})
}

func (s *Server) handleUpdateBrand(w http.ResponseWriter, r *http.Request) {
	var in BrandIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.o.Catalog.UpdateBrand(r.Context(), Actor, r.PathValue("id"), in.ExpectedVersion, in.Brand, in.Note)
	if err != nil {
		writeError(w, r, err)
		return
	}
	noteActivity(r, "", activity.Detail{Version: v})
	writeJSON(w, http.StatusOK, map[string]any{"id": in.Brand.ID, "version": v})
}
