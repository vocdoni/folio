// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	bundled "github.com/vocdoni/folio/assets"
	"github.com/vocdoni/folio/internal/activity"
	"github.com/vocdoni/folio/internal/brandkit"
	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/components"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/validate"
)

// heartbeat is the SSE keep-alive period.
const heartbeat = 20 * time.Second

func (s *Server) overviewRoutes(mux *http.ServeMux) {
	s.api(mux, "GET /ui/api/stats", route{}, s.handleStats)
	s.api(mux, "GET /ui/api/activity", route{}, s.handleActivity)
	s.api(mux, "GET /ui/api/activity/stream", route{}, s.handleStream)
	s.api(mux, "GET /ui/api/capabilities", route{}, s.handleCapabilities)
}

// StatsOut is GET /ui/api/stats.
type StatsOut struct {
	Documents       int     `json:"documents"` // not in the trash
	Trashed         int     `json:"trashed"`
	Templates       int     `json:"templates"`        // stored, not archived
	InlineTemplates int     `json:"inline_templates"` // unlisted, kept while a revision uses them
	Brands          int     `json:"brands"`           // not archived
	Assets          int     `json:"assets"`           // not deleted
	RendersToday    int     `json:"renders_today"`
	Calls24h        int     `json:"calls_24h"`
	Errors24h       int     `json:"errors_24h"`
	ErrorRate24h    float64 `json:"error_rate_24h"`
	StorageBytes    int64   `json:"storage_bytes"` // distinct output and asset blobs
	Reviewer        bool    `json:"reviewer"`
	AI              bool    `json:"ai"`
	SandboxLevel    string  `json:"sandbox_level"`
	Version         string  `json:"version"`
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := StatsOut{Reviewer: s.o.Reviewer, AI: s.aiEnabled(), SandboxLevel: s.o.Sandbox, Version: s.o.Version}
	err := s.o.DB.R.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM documents WHERE trashed_at IS NULL),
		(SELECT count(*) FROM documents WHERE trashed_at IS NOT NULL),
		(SELECT count(*) FROM template_heads WHERE archived_at IS NULL AND id NOT LIKE 'inline-%'),
		(SELECT count(*) FROM template_heads WHERE id LIKE 'inline-%'),
		(SELECT count(*) FROM brand_heads WHERE archived_at IS NULL),
		(SELECT count(*) FROM assets WHERE deleted_at IS NULL),
		(SELECT coalesce(sum(size), 0) FROM (SELECT sha256, max(size) AS size FROM files GROUP BY sha256
		                                     UNION SELECT sha256, size FROM assets))`).
		Scan(&out.Documents, &out.Trashed, &out.Templates, &out.InlineTemplates, &out.Brands, &out.Assets, &out.StorageBytes)
	if err != nil {
		writeError(w, r, fmt.Errorf("webui: stats: %w", err))
		return
	}
	a, err := activity.Summary(ctx, s.o.DB, s.o.Now())
	if err != nil {
		writeError(w, r, err)
		return
	}
	out.RendersToday, out.Calls24h, out.Errors24h, out.ErrorRate24h = a.RendersToday, a.Calls24h, a.Errors24h, a.ErrorRate24h
	writeJSON(w, http.StatusOK, out)
}

// ActivityOut is GET /ui/api/activity.
type ActivityOut struct {
	Items      []activity.Row `json:"items"`
	NextBefore int64          `json:"next_before,omitempty"` // pass as ?before= for the next page
	Actors     []string       `json:"actors"`                // filter values seen
	Actions    []string       `json:"actions"`
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := activity.Filter{Source: q.Get("source"), Actor: q.Get("actor"), Action: q.Get("action"), Outcome: q.Get("outcome")}
	var err error
	if v := q.Get("before"); v != "" {
		if f.Before, err = strconv.ParseInt(v, 10, 64); err != nil || f.Before < 0 {
			writeError(w, r, &model.Error{Code: model.ErrInvalidInput, Message: "before must be an activity id"})
			return
		}
	}
	if v := q.Get("limit"); v != "" {
		if f.Limit, err = strconv.Atoi(v); err != nil || f.Limit < 1 || f.Limit > 200 {
			writeError(w, r, &model.Error{Code: model.ErrInvalidInput, Message: "limit must be between 1 and 200"})
			return
		}
	}
	items, next, err := activity.List(r.Context(), s.o.DB, f)
	if err != nil {
		writeError(w, r, err)
		return
	}
	facets, err := activity.ListFacets(r.Context(), s.o.DB)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ActivityOut{Items: items, NextBefore: next, Actors: facets.Actors, Actions: facets.Actions})
}

// handleStream is the live activity feed (server-sent events): event
// "activity" with a Row per new entry, a comment line every 20 s, and event
// "bye" when the server shuts down or the session ends.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	if s.o.Broadcaster == nil {
		writeError(w, r, apiErr(http.StatusServiceUnavailable, "unavailable", "the live feed is not available"))
		return
	}
	sub, err := s.o.Broadcaster.Subscribe(sessionOf(r).key, MaxStreamsPerSession)
	switch {
	case errors.Is(err, activity.ErrTooManyStreams):
		writeError(w, r, apiErr(http.StatusTooManyRequests, "too_many_streams",
			fmt.Sprintf("at most %d live feeds per session; close another Folio tab", MaxStreamsPerSession)))
		return
	case err != nil:
		writeError(w, r, apiErr(http.StatusServiceUnavailable, "unavailable", "the server is shutting down"))
		return
	}
	defer s.o.Broadcaster.Unsubscribe(sub)
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	send := func(chunk string) bool {
		if _, err := w.Write([]byte(chunk)); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !send("retry: 5000\n: connected\n\n") {
		return
	}
	every := s.heartbeat
	if every <= 0 {
		every = heartbeat
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case row := <-sub.C:
			b, err := json.Marshal(row)
			if err != nil || !send(fmt.Sprintf("id: %d\nevent: activity\ndata: %s\n\n", row.ID, b)) {
				return
			}
		case <-sub.Done:
			send("event: bye\ndata: {}\n\n")
			return
		case <-tick.C:
			if !send(": ping\n\n") {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

// FontOut is a bundled font family with its specimen files.
type FontOut struct {
	Family  string `json:"family"`
	Regular string `json:"regular,omitempty"` // URL of the regular face (TTF)
	Bold    string `json:"bold,omitempty"`
}

// LayoutOut is a base layout with its options schema.
type LayoutOut struct {
	Name          string          `json:"name"`
	OptionsSchema json.RawMessage `json:"options_schema"`
}

// ComponentOut is a built-in component with its data schema.
type ComponentOut struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
}

// CapabilitiesOut is GET /ui/api/capabilities.
type CapabilitiesOut struct {
	Version            string          `json:"version"`
	Languages          []string        `json:"languages"`
	Layouts            []LayoutOut     `json:"layouts"`
	Components         []ComponentOut  `json:"components"`
	Fonts              []FontOut       `json:"fonts"`
	LabelKeys          []string        `json:"label_keys"`
	CoreMetadataSchema json.RawMessage `json:"core_metadata_schema"` // language enum: the template's languages
	Limits             LimitsOut       `json:"limits"`
	Reviewer           bool            `json:"reviewer"`
	SandboxLevel       string          `json:"sandbox_level"`
	PublicURL          string          `json:"public_url"`
	MCPURL             string          `json:"mcp_url"`
	DefaultBrand       string          `json:"default_brand"` // brand of inline templates that name none ("" = none)
}

// LimitsOut are the limits the UI enforces before sending.
type LimitsOut struct {
	MaxMarkdownBytes       int   `json:"max_markdown_bytes"`
	MaxAssetBytes          int64 `json:"max_asset_bytes"`
	MaxRequestBytes        int64 `json:"max_request_bytes"`
	MaxPages               int   `json:"max_pages"`
	PreviewPagesStored     int   `json:"preview_pages_stored"`
	TrashRetentionDays     int   `json:"trash_retention_days"`
	MaxInlineTemplateBytes int   `json:"max_inline_template_bytes"`
	MaxLayoutTypstBytes    int   `json:"max_layout_typst_bytes"`
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	bases, err := bundled.BaseLayouts()
	if err != nil {
		writeError(w, r, err)
		return
	}
	c := s.o.Config
	out := CapabilitiesOut{Version: s.o.Version, Languages: brandkit.BuiltinLanguages, LabelKeys: brandkit.LabelKeys,
		CoreMetadataSchema: validate.CoreMetadataSchema(brandkit.BuiltinLanguages), Reviewer: s.o.Reviewer, SandboxLevel: s.o.Sandbox,
		PublicURL: c.PublicURL, MCPURL: strings.TrimRight(c.PublicURL, "/") + "/mcp",
		Limits: LimitsOut{MaxMarkdownBytes: c.MaxMarkdownBytes, MaxAssetBytes: c.MaxAssetBytes, MaxRequestBytes: c.MaxRequestBytes,
			MaxPages: c.MaxPages, PreviewPagesStored: c.PreviewPagesStored, TrashRetentionDays: int(c.TrashRetention.Hours() / 24),
			MaxInlineTemplateBytes: c.MaxInlineTemplateBytes, MaxLayoutTypstBytes: c.MaxLayoutTypstBytes}}
	out.DefaultBrand, _ = s.o.Catalog.DefaultBrand(r.Context()) // "" when there is none
	for _, b := range bases {
		out.Layouts = append(out.Layouts, LayoutOut{Name: b, OptionsSchema: json.RawMessage(catalog.OptionsSchema)})
	}
	for _, n := range components.Names {
		sc, _ := components.Schema(n)
		out.Components = append(out.Components, ComponentOut{Name: n, Schema: json.RawMessage(sc)})
	}
	files, _ := fs.Glob(bundled.FS(), "fonts/*.ttf")
	for _, fam := range bundled.FontFamilies {
		f := FontOut{Family: fam}
		stem := strings.ReplaceAll(fam, " ", "")
		for _, p := range files {
			switch strings.TrimPrefix(p, "fonts/") {
			case stem + "-Regular.ttf":
				f.Regular = "/ui/" + p
			case stem + "-Bold.ttf":
				f.Bold = "/ui/" + p
			}
		}
		out.Fonts = append(out.Fonts, f)
	}
	writeJSON(w, http.StatusOK, out)
}
