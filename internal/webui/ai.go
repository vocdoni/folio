// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/vocdoni/folio/internal/ai"
	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/model"
)

func (s *Server) aiRoutes(mux *http.ServeMux) {
	s.api(mux, "GET /ui/api/ai/status", route{}, s.handleAIStatus)
	s.api(mux, "GET /ui/api/ai/jobs/{id}", route{}, s.handleAIJob)
	s.api(mux, "POST /ui/api/ai/document-plan", route{action: "ui.ai.document-plan"}, s.handleDocumentPlan)
	s.api(mux, "POST /ui/api/ai/template-draft", route{action: "ui.ai.template-draft"}, s.handleTemplateDraft)
	s.api(mux, "POST /ui/api/ai/brand-suggest", route{action: "ui.ai.brand-suggest"}, s.handleBrandSuggest)
	s.api(mux, "POST /ui/api/ai/brand-logo", route{action: "ui.ai.brand-logo", kind: "asset"}, s.handleBrandLogo)
}

// AIStatusOut is GET /ui/api/ai/status.
type AIStatusOut struct {
	Enabled bool   `json:"enabled"`
	Model   string `json:"model,omitempty"`
	Vision  bool   `json:"vision"` // PDF examples and logos need a vision-capable model
	QuotaOut
}

func (s *Server) handleAIStatus(w http.ResponseWriter, r *http.Request) {
	out := AIStatusOut{Enabled: s.aiEnabled()}
	if out.Enabled {
		out.Model, out.Vision = s.o.AI.Model(), s.vision.Load()
		out.QuotaOut = s.quotas.status(sessionOf(r).key)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAIJob(w http.ResponseWriter, r *http.Request) {
	out, ok := s.jobs.get(r.PathValue("id"), sessionOf(r).key)
	if !ok {
		writeError(w, r, &model.Error{Code: model.ErrNotFound, Message: "this assistant request expired or belongs to another session (requests are kept 15 minutes and lost on restart)"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// JobStartedOut is the 202 answer of the AI endpoints.
type JobStartedOut struct {
	JobID string `json:"job_id"`
}

var errAIOff = apiErr(http.StatusServiceUnavailable, "unavailable", "the assistants are off: set FOLIO_OPENROUTER_API_KEY to enable them")

// startJob checks availability and quotas and starts fn as a job.
func (s *Server) startJob(w http.ResponseWriter, r *http.Request, kind string, fn func(ctx context.Context, step func(string)) (any, error)) {
	if !s.aiEnabled() {
		writeError(w, r, errAIOff)
		return
	}
	session := sessionOf(r).key
	id, err := s.jobs.start(session, kind, func() error { return s.quotas.take(session) }, fn)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, JobStartedOut{JobID: id})
}

// DocumentPlanIn is POST /ui/api/ai/document-plan.
type DocumentPlanIn struct {
	Markdown   string `json:"markdown"`
	TemplateID string `json:"template_id,omitempty"`
}

func (s *Server) handleDocumentPlan(w http.ResponseWriter, r *http.Request) {
	var in DocumentPlanIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	if strings.TrimSpace(in.Markdown) == "" {
		writeError(w, r, &model.Error{Code: model.ErrInvalidInput, Message: "paste or upload the Markdown first"})
		return
	}
	s.startJob(w, r, "document-plan", func(ctx context.Context, step func(string)) (any, error) {
		step("reading your templates")
		tpls, err := s.templateInfos(ctx)
		if err != nil {
			return nil, err
		}
		return s.o.AI.PlanDocument(ctx, ai.PlanInput{Markdown: in.Markdown, TemplateID: in.TemplateID, Templates: tpls, Today: s.o.Now()}, step)
	})
}

// templateInfos lists the live templates for the planner.
func (s *Server) templateInfos(ctx context.Context) ([]ai.TemplateInfo, error) {
	items, _, err := s.o.Catalog.ListTemplates(ctx, catalog.ListFilter{Limit: 50})
	if err != nil {
		return nil, err
	}
	out := make([]ai.TemplateInfo, 0, len(items))
	for _, it := range items {
		var t model.Template
		if err := json.Unmarshal(it.Spec, &t); err != nil {
			return nil, err
		}
		ti := ai.TemplateInfo{ID: t.ID, Name: t.Name, Description: t.Description, Languages: t.Languages, Components: t.Components,
			MetadataSchema: t.MetadataSchema, DataSchema: t.DataSchema, Sections: []string{}}
		for _, sec := range t.Sections {
			ti.Sections = append(ti.Sections, sec.Title)
		}
		out = append(out, ti)
	}
	return out, nil
}
