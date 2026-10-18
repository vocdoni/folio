// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vocdoni/folio/internal/activity"
	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/checks"
	"github.com/vocdoni/folio/internal/documents"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/render"
)

func (s *Server) documentRoutes(mux *http.ServeMux) {
	s.api(mux, "GET /ui/api/documents", route{}, s.handleListDocuments)
	s.api(mux, "GET /ui/api/documents/{id}", route{}, s.handleGetDocument)
	s.api(mux, "GET /ui/api/documents/{id}/source", route{}, s.handleDocumentSource)
	s.api(mux, "GET /ui/api/documents/{id}/pages/{page}", route{}, s.handleDocumentPage)
	s.api(mux, "DELETE /ui/api/documents/{id}", route{action: "ui.document.delete", kind: "document"}, s.handleDeleteDocument)
	s.api(mux, "POST /ui/api/documents/{id}/restore", route{action: "ui.document.restore", kind: "document"}, s.handleRestoreDocument)
	s.api(mux, "POST /ui/api/documents/{id}/publish", route{action: "ui.document.publish", kind: "document"}, s.handlePublishDocument)
	s.api(mux, "POST /ui/api/render", route{action: "ui.render", kind: "document"}, s.handleRender)
}

// FileOut is a stored output: the public /f/ link (a capability anyone with
// the URL can use) and, for previews, the session-only UI URL.
type FileOut struct {
	Kind string `json:"kind"` // pdf | docx | preview
	Name string `json:"name"`
	MIME string `json:"mime"`
	Size int    `json:"size"`
	Page int    `json:"page,omitempty"`
	URL  string `json:"url"` // public download link
}

// DocumentSummaryOut is one documents list entry.
type DocumentSummaryOut struct {
	documents.Summary
	PreviewURL string `json:"preview_url,omitempty"` // stored page 1 of the latest revision (session URL)
}

// DocumentListOut is GET /ui/api/documents.
type DocumentListOut struct {
	Items      []DocumentSummaryOut `json:"items"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

func (s *Server) pageURL(id string, revision, page int) string {
	return fmt.Sprintf("/ui/api/documents/%s/pages/%d.png?revision=%d", id, page, revision)
}

func (s *Server) fileURL(f catalog.File) string {
	return strings.TrimRight(s.o.Config.PublicURL, "/") + "/f/" + f.ID + "/" + f.Name
}

func intParam(r *http.Request, name string, def, minV, maxV int) (int, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < minV || n > maxV {
		return 0, &model.Error{Code: model.ErrInvalidInput, Message: fmt.Sprintf("%s must be an integer between %d and %d", name, minV, maxV)}
	}
	return n, nil
}

func boolParam(r *http.Request, name string) bool {
	v, _ := strconv.ParseBool(r.URL.Query().Get(name))
	return v
}

func (s *Server) handleListDocuments(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := intParam(r, "limit", 48, 1, 200)
	if err != nil {
		writeError(w, r, err)
		return
	}
	status := q.Get("status")
	if status != "" && status != "draft" && status != "published" && status != "trashed" {
		writeError(w, r, &model.Error{Code: model.ErrInvalidInput, Message: "status must be draft, published or trashed"})
		return
	}
	items, next, err := s.o.Documents.List(r.Context(), documents.ListFilter{Query: q.Get("query"), TemplateID: q.Get("template"),
		Status: status, Client: q.Get("client"), IncludeTrashed: boolParam(r, "include_trashed"), Limit: limit, Cursor: q.Get("cursor")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	stored, err := s.storedFirstPages(r.Context(), items)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := DocumentListOut{Items: make([]DocumentSummaryOut, 0, len(items)), NextCursor: next}
	for _, d := range items {
		o := DocumentSummaryOut{Summary: d}
		if stored[d.ID] {
			o.PreviewURL = s.pageURL(d.ID, d.LatestRevision, 1) + "&stored=true"
		}
		out.Items = append(out.Items, o)
	}
	writeJSON(w, http.StatusOK, out)
}

// storedFirstPages reports which documents have a stored page-1 preview for
// their latest revision (the grid never triggers rasterisation).
func (s *Server) storedFirstPages(ctx context.Context, items []documents.Summary) (map[string]bool, error) {
	out := map[string]bool{}
	if len(items) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(items))
	for _, d := range items {
		args = append(args, d.ID)
	}
	rows, err := s.o.DB.R.QueryContext(ctx, `SELECT f.document_id FROM files f JOIN documents d ON d.id = f.document_id AND d.latest_revision = f.revision
		WHERE f.kind = 'preview' AND f.page = 1 AND f.document_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("webui: stored previews: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// RevisionOut is a revision in the document view.
type RevisionOut struct {
	Revision  int             `json:"revision"`
	Parent    int             `json:"parent_revision,omitempty"`
	Template  model.Ref       `json:"template"`
	Brand     model.Ref       `json:"brand"`
	Warnings  []model.Finding `json:"warnings,omitempty"`
	Review    json.RawMessage `json:"review,omitempty"`
	CreatedBy string          `json:"created_by"`
	CreatedAt time.Time       `json:"created_at"`
}

// DocumentOut is GET /ui/api/documents/{id}.
type DocumentOut struct {
	ID                string        `json:"id"`
	Title             string        `json:"title"`
	TemplateID        string        `json:"template_id"`
	Client            string        `json:"client"`
	Status            string        `json:"status"`
	LatestRevision    int           `json:"latest_revision"`
	PublishedRevision int           `json:"published_revision,omitempty"`
	CreatedBy         string        `json:"created_by"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
	Revision          RevisionOut   `json:"revision"`
	Revisions         []RevisionOut `json:"revisions"` // newest first, without warnings
	Files             []FileOut     `json:"files"`     // of the requested revision
	PageCount         int           `json:"page_count"`
	PageURLs          []string      `json:"page_urls"` // one session URL per page (stored previews or rendered on demand)
}

func revisionOut(r documents.Revision) RevisionOut {
	return RevisionOut{Revision: r.Revision, Parent: r.Parent, Template: r.Template, Brand: r.Brand, Warnings: r.Warnings,
		Review: r.Review, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt}
}

func (s *Server) handleGetDocument(w http.ResponseWriter, r *http.Request) {
	rev, err := intParam(r, "revision", 0, 0, math.MaxInt32)
	if err != nil {
		writeError(w, r, err)
		return
	}
	d, err := s.o.Documents.Get(r.Context(), r.PathValue("id"), rev)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := DocumentOut{ID: d.ID, Title: d.Title, TemplateID: d.TemplateID, Client: d.Client, Status: d.Status, LatestRevision: d.LatestRevision,
		PublishedRevision: d.PublishedRevision, CreatedBy: d.CreatedBy, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
		Revision: revisionOut(d.Revision), Files: []FileOut{}, PageURLs: []string{}}
	for _, rv := range d.Revisions {
		out.Revisions = append(out.Revisions, revisionOut(rv))
	}
	stored := 0
	for _, f := range d.Revision.Files {
		out.Files = append(out.Files, FileOut{Kind: f.Kind, Name: f.Name, MIME: f.MIME, Size: f.Size, Page: f.Page, URL: s.fileURL(f)})
		switch f.Kind {
		case "pdf":
			if data, err := s.o.Blobs.Get(f.SHA256); err == nil {
				out.PageCount, _ = checks.PDF(r.Context(), data, math.MaxInt32)
			}
		case "preview":
			stored = max(stored, f.Page)
		}
	}
	for p := 1; p <= max(out.PageCount, stored); p++ {
		out.PageURLs = append(out.PageURLs, s.pageURL(d.ID, d.Revision.Revision, p))
	}
	writeJSON(w, http.StatusOK, out)
}

// SourceOut is the stored input of a revision (to revise it in the editor).
type SourceOut struct {
	TemplateID string          `json:"template_id"`
	Markdown   string          `json:"markdown"`
	Metadata   json.RawMessage `json:"metadata"`
	Data       json.RawMessage `json:"data"`
	Formats    []string        `json:"formats"`
	Revision   int             `json:"revision"`
}

func (s *Server) handleDocumentSource(w http.ResponseWriter, r *http.Request) {
	rev, err := intParam(r, "revision", 0, 0, math.MaxInt32)
	if err != nil {
		writeError(w, r, err)
		return
	}
	id := r.PathValue("id")
	d, err := s.o.Documents.Get(r.Context(), id, rev)
	if err != nil {
		writeError(w, r, err)
		return
	}
	raw, err := s.o.Documents.Source(r.Context(), id, d.Revision.Revision)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in SourceOut
	if err := json.Unmarshal(raw, &in); err != nil {
		writeError(w, r, fmt.Errorf("webui: stored input: %w", err))
		return
	}
	in.TemplateID, in.Revision = d.Revision.Template.ID, d.Revision.Revision
	writeJSON(w, http.StatusOK, in)
}

var pageRE = regexp.MustCompile(`^([1-9][0-9]{0,4})\.png$`)

// handleDocumentPage serves one page PNG of a revision: the stored preview
// when there is one (also for trashed documents, which /f/ refuses), else a
// rasterisation of the stored PDF through the MCP render queue, cached in
// an in-memory LRU. With stored=true only stored previews are served (the
// documents grid never triggers rasterisation).
func (s *Server) handleDocumentPage(w http.ResponseWriter, r *http.Request) {
	m := pageRE.FindStringSubmatch(r.PathValue("page"))
	rev, err := intParam(r, "revision", 0, 0, math.MaxInt32)
	if m == nil || err != nil {
		writeError(w, r, &model.Error{Code: model.ErrInvalidInput, Message: "use /pages/<n>.png?revision=<r>"})
		return
	}
	page, _ := strconv.Atoi(m[1])
	d, err := s.o.Documents.Get(r.Context(), r.PathValue("id"), rev)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var pdf *catalog.File
	for i, f := range d.Revision.Files {
		switch {
		case f.Kind == "preview" && f.Page == page:
			data, err := s.o.Blobs.Get(f.SHA256)
			if err != nil {
				writeError(w, r, fmt.Errorf("webui: preview blob: %w", err))
				return
			}
			servePNG(w, data, "private, max-age=86400, immutable")
			return
		case f.Kind == "pdf":
			pdf = &d.Revision.Files[i]
		}
	}
	if pdf == nil || boolParam(r, "stored") {
		writeError(w, r, &model.Error{Code: model.ErrNotFound, Message: fmt.Sprintf("page %d has no stored preview", page)})
		return
	}
	key := pdf.SHA256 + "/" + strconv.Itoa(page)
	if img, ok := s.pages.get(key); ok {
		servePNG(w, img, "private, max-age=86400, immutable")
		return
	}
	data, err := s.o.Blobs.Get(pdf.SHA256)
	if err != nil {
		writeError(w, r, fmt.Errorf("webui: pdf blob: %w", err))
		return
	}
	if n, _ := checks.PDF(r.Context(), data, math.MaxInt32); page > n {
		writeError(w, r, &model.Error{Code: model.ErrNotFound, Message: fmt.Sprintf("the document has %d pages", n)})
		return
	}
	release, err := s.o.Queue.Acquire(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	img, err := s.o.Raster.RasterizePDFPage(r.Context(), data, page, render.PreviewPPI)
	release()
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.pages.put(key, img)
	servePNG(w, img, "private, max-age=86400, immutable")
}

func servePNG(w http.ResponseWriter, data []byte, cache string) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", cache)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// StatusOut answers lifecycle actions.
type StatusOut struct {
	ID     string `json:"id"`
	Status string `json:"status"` // draft | published | trashed | purged
}

func (s *Server) statusOf(r *http.Request, id string) (StatusOut, error) {
	d, err := s.o.Documents.Get(r.Context(), id, 0)
	if err != nil {
		return StatusOut{}, err
	}
	return StatusOut{ID: id, Status: d.Status}, nil
}

func (s *Server) handleDeleteDocument(w http.ResponseWriter, r *http.Request) {
	id, purge, confirm := r.PathValue("id"), boolParam(r, "purge"), boolParam(r, "confirm_published")
	noteActivity(r, "", activity.Detail{Purge: purge})
	var err error
	if purge {
		err = s.o.Documents.Purge(r.Context(), Actor, id, confirm)
	} else {
		err = s.o.Documents.Trash(r.Context(), Actor, id, confirm)
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	if purge {
		writeJSON(w, http.StatusOK, StatusOut{ID: id, Status: "purged"})
		return
	}
	out, err := s.statusOf(r, id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRestoreDocument(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.o.Documents.Restore(r.Context(), Actor, id); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.statusOf(r, id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// PublishIn is POST /ui/api/documents/{id}/publish.
type PublishIn struct {
	Revision int `json:"revision"`
}

func (s *Server) handlePublishDocument(w http.ResponseWriter, r *http.Request) {
	var in PublishIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	id := r.PathValue("id")
	noteActivity(r, "", activity.Detail{Revision: in.Revision})
	if err := s.o.Documents.Publish(r.Context(), Actor, id, in.Revision); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.statusOf(r, id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// RenderIn is POST /ui/api/render: a new document (idempotency_key
// required, one per form instance) or a new revision (document_id +
// expected_revision).
type RenderIn struct {
	TemplateID       string          `json:"template_id"`
	TemplateVersion  int             `json:"template_version,omitempty"`
	BrandID          string          `json:"brand_id,omitempty"`
	Markdown         string          `json:"markdown"`
	Metadata         json.RawMessage `json:"metadata"`
	Data             json.RawMessage `json:"data,omitempty"`
	Formats          []model.Format  `json:"formats,omitempty"`
	IdempotencyKey   string          `json:"idempotency_key,omitempty"`
	DocumentID       string          `json:"document_id,omitempty"`
	ExpectedRevision *int            `json:"expected_revision,omitempty"`
}

// RenderOut is the render result.
type RenderOut struct {
	DocumentID string          `json:"document_id"`
	Revision   int             `json:"revision"`
	Unchanged  bool            `json:"unchanged"`
	Status     string          `json:"status"`
	Template   model.Ref       `json:"template"`
	Brand      model.Ref       `json:"brand"`
	Files      []FileOut       `json:"files"`
	Warnings   []model.Finding `json:"warnings"`
}

var idempotencyRE = regexp.MustCompile(`^[A-Za-z0-9-]{8,128}$`)

func (s *Server) handleRender(w http.ResponseWriter, r *http.Request) {
	var in RenderIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	if in.DocumentID == "" && !idempotencyRE.MatchString(in.IdempotencyKey) {
		writeError(w, r, &model.Error{Code: model.ErrInvalidInput, Message: "idempotency_key is required for a new document (one UUID per form)"})
		return
	}
	res, err := s.o.Documents.Render(r.Context(), Actor, documents.RenderRequest{TemplateID: in.TemplateID, TemplateVersion: in.TemplateVersion,
		BrandID: in.BrandID, Input: model.DocumentInput{Markdown: in.Markdown, Metadata: in.Metadata, Data: in.Data, Formats: in.Formats},
		DocumentID: in.DocumentID, ExpectedRevision: in.ExpectedRevision, IdempotencyKey: in.IdempotencyKey})
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := RenderOut{DocumentID: res.DocumentID, Revision: res.Revision, Unchanged: res.Unchanged, Status: res.Status, Template: res.Template,
		Brand: res.Brand, Files: []FileOut{}, Warnings: res.Warnings}
	detail := activity.Detail{Revision: res.Revision, Unchanged: res.Unchanged}
	for _, f := range res.Files {
		out.Files = append(out.Files, FileOut{Kind: f.Kind, Name: f.Name, MIME: f.MIME, Size: f.Size, Page: f.Page, URL: s.fileURL(f)})
		if f.Kind != "preview" {
			detail.Formats = append(detail.Formats, f.Kind)
		}
	}
	if out.Warnings == nil {
		out.Warnings = []model.Finding{}
	}
	noteActivity(r, res.DocumentID, detail)
	writeJSON(w, http.StatusOK, out)
}

// pageCache is the LRU of on-demand page PNGs keyed by (PDF sha256, page),
// bounded by total bytes.
type pageCache struct {
	mu    sync.Mutex
	max   int
	size  int
	ll    *list.List
	items map[string]*list.Element
}

type pageEntry struct {
	key  string
	data []byte
}

// pageCacheBytes bounds the on-demand page cache.
const pageCacheBytes = 64 << 20

func newPageCache(maxBytes int) *pageCache {
	return &pageCache{max: maxBytes, ll: list.New(), items: map[string]*list.Element{}}
}

func (c *pageCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		c.ll.MoveToFront(e)
		return e.Value.(*pageEntry).data, true
	}
	return nil, false
}

func (c *pageCache) put(key string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(data) > c.max {
		return
	}
	if e, ok := c.items[key]; ok {
		c.ll.MoveToFront(e)
		return
	}
	c.items[key] = c.ll.PushFront(&pageEntry{key: key, data: data})
	c.size += len(data)
	for c.size > c.max {
		e := c.ll.Back()
		pe := e.Value.(*pageEntry)
		c.ll.Remove(e)
		delete(c.items, pe.key)
		c.size -= len(pe.data)
	}
}
