// SPDX-License-Identifier: AGPL-3.0-only

// Package documents renders and stores documents as append-only revisions
// with optimistic concurrency (expected_revision), per-actor idempotency
// keys, input-hash dedup, publish, trash/restore and purge with blob GC.
package documents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/render"
	"github.com/vocdoni/folio/internal/store"
)

// IdempotencyWindow is how long an idempotency key maps to its document.
const IdempotencyWindow = 24 * time.Hour

// Renderer runs the pipeline (render.Pipeline implements it).
type Renderer interface {
	Render(ctx context.Context, req render.Request) (*render.Result, error)
}

// Resolver resolves templates and brands (catalog.Service implements it).
type Resolver interface {
	ResolveTemplate(ctx context.Context, id string, version int, allowArchived bool) (model.Template, int, error)
	ResolveBrand(ctx context.Context, id string, version int) (model.Brand, int, error)
}

// Service is the documents API used by P2's MCP tools.
type Service struct {
	DB           *store.DB
	Blobs        *store.Blobs
	Renderer     Renderer
	Resolver     Resolver
	Now          func() time.Time
	PreviewPages int           // FOLIO_PREVIEW_PAGES_STORED
	GCGrace      time.Duration // FOLIO_BLOB_GC_GRACE
}

// RenderRequest mirrors the render_document tool input.
type RenderRequest struct {
	TemplateID       string
	TemplateVersion  int    // 0 = latest
	BrandID          string // "" = the template's brand
	Input            model.DocumentInput
	DocumentID       string // "" = new document
	ExpectedRevision *int   // required with DocumentID
	IdempotencyKey   string // ≤ 128 chars, scoped per actor, new documents only
}

// RenderResult mirrors the render_document tool output (URLs are built by P2).
type RenderResult struct {
	DocumentID string          `json:"document_id"`
	Revision   int             `json:"revision"`
	Unchanged  bool            `json:"unchanged"`
	Status     string          `json:"status"`
	Template   model.Ref       `json:"template"`
	Brand      model.Ref       `json:"brand"`
	Files      []catalog.File  `json:"files"`
	Warnings   []model.Finding `json:"warnings"`
}

func errorf(code model.ErrorCode, format string, args ...any) error {
	return &model.Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

type docRow struct {
	ID, Title, TemplateID, Status string
	Latest                        int
	Published                     sql.NullInt64
	Trashed                       bool
}

func loadDoc(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (*docRow, error) {
	d := &docRow{ID: id}
	var trashed sql.NullString
	err := q.QueryRowContext(ctx, `SELECT title, template_id, status, latest_revision, published_revision, trashed_at FROM documents WHERE id = ?`, id).
		Scan(&d.Title, &d.TemplateID, &d.Status, &d.Latest, &d.Published, &trashed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errorf(model.ErrNotFound, "document %s does not exist", id)
	}
	if err != nil {
		return nil, fmt.Errorf("documents: load: %w", err)
	}
	d.Trashed = trashed.Valid
	return d, nil
}

// Render creates a document (DocumentID empty) or a new revision.
func (s *Service) Render(ctx context.Context, actor string, req RenderRequest) (*RenderResult, error) {
	now := s.Now()
	if len(req.IdempotencyKey) > 128 {
		return nil, errorf(model.ErrInvalidInput, "idempotency_key must be at most 128 characters")
	}
	var doc *docRow
	if req.DocumentID != "" {
		if req.ExpectedRevision == nil {
			return nil, errorf(model.ErrInvalidInput, "expected_revision is required with document_id (use the document's latest revision)")
		}
		if req.IdempotencyKey != "" {
			return nil, errorf(model.ErrInvalidInput, "idempotency_key applies to new documents only; revisions are idempotent through expected_revision")
		}
		var err error
		if doc, err = loadDoc(ctx, s.DB.R, req.DocumentID); err != nil {
			return nil, err
		}
		if doc.Trashed {
			return nil, errorf(model.ErrConflict, "document %s is in the trash; restore it first", doc.ID)
		}
		if *req.ExpectedRevision != doc.Latest {
			return nil, errorf(model.ErrConflict, "document %s is at revision %d; re-read it and pass expected_revision=%d", doc.ID, doc.Latest, doc.Latest)
		}
		if req.TemplateID == "" {
			req.TemplateID = doc.TemplateID
		}
	} else if req.IdempotencyKey != "" {
		if id, err := s.lookupKey(ctx, s.DB.R, actor, req.IdempotencyKey, now); err != nil {
			return nil, err
		} else if id != "" {
			return s.unchanged(ctx, id, 0)
		}
	}
	t, tv, err := s.Resolver.ResolveTemplate(ctx, req.TemplateID, req.TemplateVersion, false)
	if err != nil {
		return nil, err
	}
	brandID := req.BrandID
	if brandID == "" {
		brandID = t.BrandID
	}
	b, bv, err := s.Resolver.ResolveBrand(ctx, brandID, 0)
	if err != nil {
		return nil, err
	}
	// Dedup: identical canonical input + versions as the latest revision.
	if doc != nil {
		_, sum, err := render.CanonicalInput(model.Ref{ID: t.ID, Version: tv}, model.Ref{ID: b.ID, Version: bv}, req.Input)
		if err != nil {
			return nil, errorf(model.ErrInvalidInput, "metadata and data must be valid JSON: %v", err)
		}
		var latestSum string
		if err := s.DB.R.QueryRowContext(ctx, `SELECT input_sha256 FROM revisions WHERE document_id = ? AND revision = ?`, doc.ID, doc.Latest).Scan(&latestSum); err != nil {
			return nil, fmt.Errorf("documents: latest hash: %w", err)
		}
		if latestSum == sum {
			return s.unchanged(ctx, doc.ID, doc.Latest)
		}
	}

	res, err := s.Renderer.Render(ctx, render.Request{Template: t, TemplateVersion: tv, Brand: b, BrandVersion: bv, Input: req.Input})
	if err != nil {
		return nil, err
	}
	// Blobs first: a crash before the commit leaves only unreferenced blobs (GC'd).
	inputBlob, err := s.Blobs.Put(res.Canonical)
	if err != nil {
		return nil, err
	}
	files, err := s.putFiles(res, fileBase(req.Input))
	if err != nil {
		return nil, err
	}
	warnings, _ := model.Canonical(nonNil(res.Warnings))
	var meta struct {
		Title  string `json:"title"`
		Client string `json:"client"`
	}
	_ = json.Unmarshal(req.Input.Metadata, &meta) // validated by the pipeline

	out := &RenderResult{Template: model.Ref{ID: t.ID, Version: tv}, Brand: model.Ref{ID: b.ID, Version: bv},
		Files: files, Warnings: res.Warnings}
	err = s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		ts := store.Now(now)
		var revision, parent int
		if doc == nil {
			if req.IdempotencyKey != "" {
				// A key older than the window no longer maps to its document.
				if _, err := tx.ExecContext(ctx, `UPDATE documents SET idempotency_key = NULL
					WHERE created_by = ? AND idempotency_key = ? AND created_at <= ?`,
					actor, req.IdempotencyKey, store.Now(now.Add(-IdempotencyWindow))); err != nil {
					return fmt.Errorf("documents: expire key: %w", err)
				}
				if id, err := s.lookupKey(ctx, tx, actor, req.IdempotencyKey, now); err != nil {
					return err
				} else if id != "" {
					out.DocumentID = id // lost a race with an identical retry
					return errUnchanged
				}
			}
			out.DocumentID = uuid.Must(uuid.NewV7()).String()
			revision = 1
			var key any
			if req.IdempotencyKey != "" {
				key = req.IdempotencyKey
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO documents (id, title, template_id, client, status, latest_revision,
				idempotency_key, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, 'draft', 1, ?, ?, ?, ?)`,
				out.DocumentID, meta.Title, t.ID, meta.Client, key, actor, ts, ts); err != nil {
				return fmt.Errorf("documents: insert document: %w", err)
			}
			out.Status = "draft"
		} else {
			// Re-check inside the IMMEDIATE transaction: of two renders with the
			// same expected_revision exactly one commits.
			cur, err := loadDoc(ctx, tx, doc.ID)
			if err != nil {
				return err
			}
			if cur.Trashed {
				return errorf(model.ErrConflict, "document %s was moved to the trash", doc.ID)
			}
			if cur.Latest != *req.ExpectedRevision {
				return errorf(model.ErrConflict, "document %s is at revision %d; re-read it and pass expected_revision=%d", doc.ID, cur.Latest, cur.Latest)
			}
			out.DocumentID, parent, revision, out.Status = doc.ID, cur.Latest, cur.Latest+1, cur.Status
			if _, err := tx.ExecContext(ctx, `UPDATE documents SET latest_revision = ?, title = ?, client = ?, template_id = ?, updated_at = ?
				WHERE id = ? AND latest_revision = ?`, revision, meta.Title, meta.Client, t.ID, ts, doc.ID, parent); err != nil {
				return fmt.Errorf("documents: bump revision: %w", err)
			}
		}
		var parentArg any
		if parent > 0 {
			parentArg = parent
		}
		// revisions.computed keeps its default '{}': Folio derives no values
		// from the input any more, and nothing reads the column.
		if _, err := tx.ExecContext(ctx, `INSERT INTO revisions (document_id, revision, parent_revision, template_id, template_version,
			brand_id, brand_version, input_sha256, input_blob, warnings, render_ms, created_by, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			out.DocumentID, revision, parentArg, t.ID, tv, b.ID, bv, res.InputSHA256, inputBlob,
			string(warnings), res.Duration.Milliseconds(), actor, ts); err != nil {
			return fmt.Errorf("documents: insert revision: %w", err)
		}
		for _, f := range files {
			var page any
			if f.Page > 0 {
				page = f.Page
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO files (id, document_id, revision, kind, page, name, mime, sha256, size, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, f.ID, out.DocumentID, revision, f.Kind, page, f.Name, f.MIME, f.SHA256, f.Size, ts); err != nil {
				return fmt.Errorf("documents: insert file: %w", err)
			}
		}
		out.Revision = revision
		return store.Audit(ctx, tx, now, actor, "render_document", "document", out.DocumentID, "ok", []byte(fmt.Sprintf(`{"revision":%d}`, revision)))
	})
	if errors.Is(err, errUnchanged) {
		return s.unchanged(ctx, out.DocumentID, 0)
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

var errUnchanged = errors.New("documents: unchanged")

// lookupKey returns the document created by actor with key inside the window.
func (s *Service) lookupKey(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, actor, key string, now time.Time) (string, error) {
	var id string
	err := q.QueryRowContext(ctx, `SELECT id FROM documents WHERE created_by = ? AND idempotency_key = ? AND created_at > ?`,
		actor, key, store.Now(now.Add(-IdempotencyWindow))).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("documents: idempotency lookup: %w", err)
	}
	return id, nil
}

// unchanged returns revision (0 = latest) of id with unchanged=true.
func (s *Service) unchanged(ctx context.Context, id string, revision int) (*RenderResult, error) {
	d, err := s.Get(ctx, id, revision)
	if err != nil {
		return nil, err
	}
	return &RenderResult{DocumentID: d.ID, Revision: d.Revision.Revision, Unchanged: true, Status: d.Status,
		Template: d.Revision.Template, Brand: d.Revision.Brand, Files: d.Revision.Files,
		Warnings: d.Revision.Warnings}, nil
}

func nonNil(fs []model.Finding) []model.Finding {
	if fs == nil {
		return []model.Finding{}
	}
	return fs
}

// fileBase derives a download file name from the metadata title.
func fileBase(in model.DocumentInput) string {
	var m struct {
		Title string `json:"title"`
	}
	_ = json.Unmarshal(in.Metadata, &m)
	var b strings.Builder
	for _, r := range strings.ToLower(m.Title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
		if b.Len() >= 60 {
			break
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		name = "document"
	}
	return name
}

func (s *Service) putFiles(res *render.Result, base string) ([]catalog.File, error) {
	type out struct {
		kind, name, mime string
		page             int
		data             []byte
	}
	var outs []out
	if res.PDF != nil {
		outs = append(outs, out{"pdf", base + ".pdf", "application/pdf", 0, res.PDF})
	}
	if res.DOCX != nil {
		outs = append(outs, out{"docx", base + ".docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", 0, res.DOCX})
	}
	for i, p := range res.Pages {
		if i >= s.PreviewPages {
			break
		}
		outs = append(outs, out{"preview", fmt.Sprintf("%s-page-%d.png", base, i+1), "image/png", i + 1, p})
	}
	files := make([]catalog.File, 0, len(outs))
	for _, o := range outs {
		sum, err := s.Blobs.Put(o.data)
		if err != nil {
			return nil, err
		}
		files = append(files, catalog.File{ID: uuid.NewString(), Kind: o.kind, Page: o.page, Name: o.name, MIME: o.mime, SHA256: sum, Size: len(o.data)})
	}
	return files, nil
}
