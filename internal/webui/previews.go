// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/vocdoni/folio/internal/model"
)

// Preview cache bounds.
const (
	previewsPerSession = 8
	previewBytes       = 64 << 20
	previewTTL         = 10 * time.Minute
	previewPagesMax    = 6
	previewPagesDef    = 2
)

// preview is an unsaved template's sample render held in memory.
type preview struct {
	id, session, sha string
	pages            [][]byte
	pageCount        int
	warnings         []model.Finding
	created          time.Time
	size             int
}

// previewCache holds template preview PNGs in memory (never in the blob
// store or the files table): per session at most 8, 64 MiB in total, 10
// minutes each, oldest evicted first; one preview render in flight per
// session.
type previewCache struct {
	mu       sync.Mutex
	now      func() time.Time
	items    []*preview // oldest first
	size     int
	inflight map[string]bool
}

func newPreviewCache(now func() time.Time) *previewCache {
	return &previewCache{now: now, inflight: map[string]bool{}}
}

// expire drops previews older than the TTL; the caller holds mu.
func (c *previewCache) expire() {
	now := c.now()
	keep := c.items[:0]
	for _, p := range c.items {
		if now.Sub(p.created) < previewTTL {
			keep = append(keep, p)
		} else {
			c.size -= p.size
		}
	}
	c.items = keep
}

func (c *previewCache) bySpec(session, sha string) *preview {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expire()
	for _, p := range c.items {
		if p.session == session && p.sha == sha {
			return p
		}
	}
	return nil
}

// begin marks a render in flight for session; false when one already is.
func (c *previewCache) begin(session string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inflight[session] {
		return false
	}
	c.inflight[session] = true
	return true
}

func (c *previewCache) end(session string) {
	c.mu.Lock()
	delete(c.inflight, session)
	c.mu.Unlock()
}

func (c *previewCache) put(p *preview) {
	if p.size > previewBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expire()
	mine := 0
	for _, q := range c.items {
		if q.session == p.session {
			mine++
		}
	}
	// Oldest first: this session's own previews beyond its cap, then anyone's
	// while the total would exceed the byte budget.
	for i := 0; i < len(c.items); {
		q := c.items[i]
		if (q.session == p.session && mine >= previewsPerSession) || c.size+p.size > previewBytes {
			if q.session == p.session {
				mine--
			}
			c.size -= q.size
			c.items = append(c.items[:i], c.items[i+1:]...)
			continue
		}
		i++
	}
	c.items = append(c.items, p)
	c.size += p.size
}

func (c *previewCache) page(id, session string, n int) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expire()
	for _, p := range c.items {
		if p.id == id && p.session == session && n >= 1 && n <= len(p.pages) {
			return p.pages[n-1], true
		}
	}
	return nil, false
}

func (c *previewCache) dropSession(session string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	keep := c.items[:0]
	for _, p := range c.items {
		if p.session != session {
			keep = append(keep, p)
		} else {
			c.size -= p.size
		}
	}
	c.items = keep
}

// previewRoutes registers the live template preview. POST templates/preview is
// the one non-GET /ui/api request without an activity row: a live-typing
// preview is not an action and writes nothing.
func (s *Server) previewRoutes(mux *http.ServeMux) {
	s.api(mux, "POST /ui/api/templates/preview", route{}, s.handleTemplatePreview)
	s.api(mux, "GET /ui/api/previews/{id}/pages/{page}", route{}, s.handlePreviewPage)
}

// PreviewIn is POST /ui/api/templates/preview: an unsaved template spec.
type PreviewIn struct {
	Spec  model.Template `json:"spec"`
	Pages int            `json:"pages,omitempty"` // page images to keep, 1–6 (default 2)
}

// PreviewOut is the dry run of an unsaved spec. OK is false when the spec
// or its sample is invalid: Findings then says what to fix.
type PreviewOut struct {
	OK        bool            `json:"ok"`
	Message   string          `json:"message,omitempty"`
	Findings  []model.Finding `json:"findings"` // errors when !OK, sample warnings otherwise
	PreviewID string          `json:"preview_id,omitempty"`
	Pages     int             `json:"pages"`     // pages of the sample
	PageURLs  []string        `json:"page_urls"` // the first Pages≤n images (session-only)
	Cached    bool            `json:"cached"`
}

func previewOut(p *preview, cached bool) PreviewOut {
	out := PreviewOut{OK: true, Findings: p.warnings, PreviewID: p.id, Pages: p.pageCount, PageURLs: []string{}, Cached: cached}
	if out.Findings == nil {
		out.Findings = []model.Finding{}
	}
	for i := range p.pages {
		out.PageURLs = append(out.PageURLs, fmt.Sprintf("/ui/api/previews/%s/pages/%d.png", p.id, i+1))
	}
	return out
}

// errPreviewInProgress is the 409 for a second concurrent preview.
var errPreviewInProgress = apiErr(http.StatusConflict, "preview_in_progress", "a preview is already rendering for this session; send the latest spec when it finishes")

// runPreview dry-runs spec through the preview queue (pdf only) and caches
// its first pages for the session. Invalid specs are an OK=false result, not
// an error; errors are busy, in progress, timeouts and internal failures.
func (s *Server) runPreview(ctx context.Context, session string, spec model.Template, pages int) (PreviewOut, error) {
	if pages <= 0 {
		pages = previewPagesDef
	}
	pages = min(pages, previewPagesMax)
	sha, err := specSHA(map[string]any{"spec": spec, "pages": pages})
	if err != nil {
		return PreviewOut{}, err
	}
	if p := s.previews.bySpec(session, sha); p != nil {
		return previewOut(p, true), nil
	}
	if !s.previews.begin(session) {
		return PreviewOut{}, errPreviewInProgress
	}
	defer s.previews.end(session)
	warnings, res, err := s.o.Preview.DryRunTemplate(ctx, spec, []model.Format{model.FormatPDF})
	if me, ok := errors.AsType[*model.Error](err); ok {
		switch me.Code {
		case model.ErrValidationFailed, model.ErrRenderFailed, model.ErrInvalidInput, model.ErrNotFound, model.ErrArchived:
			fs := me.Details
			if len(fs) == 0 {
				fs = []model.Finding{{Code: string(me.Code), Severity: model.SeverityError, Message: me.Message}}
			}
			return PreviewOut{OK: false, Message: me.Message, Findings: fs, PageURLs: []string{}}, nil
		}
	}
	if err != nil {
		return PreviewOut{}, err
	}
	id, err := randomID()
	if err != nil {
		return PreviewOut{}, err
	}
	p := &preview{id: id, session: session, sha: sha, warnings: warnings, created: s.o.Now(), pageCount: res.PageCount}
	for i := 0; i < len(res.Pages) && i < pages; i++ {
		p.pages = append(p.pages, res.Pages[i])
		p.size += len(res.Pages[i])
	}
	if p.pageCount == 0 {
		p.pageCount = len(res.Pages)
	}
	s.previews.put(p)
	return previewOut(p, false), nil
}

func (s *Server) handleTemplatePreview(w http.ResponseWriter, r *http.Request) {
	var in PreviewIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.runPreview(r.Context(), sessionOf(r).key, in.Spec, in.Pages)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePreviewPage(w http.ResponseWriter, r *http.Request) {
	m := pageRE.FindStringSubmatch(r.PathValue("page"))
	if m == nil {
		writeError(w, r, &model.Error{Code: model.ErrInvalidInput, Message: "use /pages/<n>.png"})
		return
	}
	n, _ := strconv.Atoi(m[1])
	img, ok := s.previews.page(r.PathValue("id"), sessionOf(r).key, n)
	if !ok {
		writeError(w, r, &model.Error{Code: model.ErrNotFound, Message: "this preview expired or belongs to another session; render it again"})
		return
	}
	servePNG(w, img, "private, no-store")
}

// randomID is a random 128-bit hex id (preview and job ids).
func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// specSHA returns the hex SHA-256 of v's canonical JSON.
func specSHA(v any) (string, error) {
	b, err := model.Canonical(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
