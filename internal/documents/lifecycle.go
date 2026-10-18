// SPDX-License-Identifier: AGPL-3.0-only

package documents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/store"
)

// Revision is one stored revision.
type Revision struct {
	Revision  int             `json:"revision"`
	Parent    int             `json:"parent_revision,omitempty"`
	Template  model.Ref       `json:"template"`
	Brand     model.Ref       `json:"brand"`
	InputBlob string          `json:"-"`
	Warnings  []model.Finding `json:"warnings"`
	Review    json.RawMessage `json:"review,omitempty"`
	Files     []catalog.File  `json:"files"`
	CreatedBy string          `json:"created_by"`
	CreatedAt time.Time       `json:"created_at"`
}

// Document is get_document's view.
type Document struct {
	ID                string     `json:"id"`
	Title             string     `json:"title"`
	TemplateID        string     `json:"template_id"`
	Client            string     `json:"client"`
	Status            string     `json:"status"`
	LatestRevision    int        `json:"latest_revision"`
	PublishedRevision int        `json:"published_revision,omitempty"`
	CreatedBy         string     `json:"created_by"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	Revisions         []Revision `json:"revisions"` // summaries, newest first (no files)
	Revision          Revision   `json:"revision"`  // the requested revision in full
}

func parseTS(s string) time.Time { t, _ := time.Parse(time.RFC3339, s); return t }

// Get returns a document with revision (0 = latest) in full.
func (s *Service) Get(ctx context.Context, id string, revision int) (*Document, error) {
	d := &Document{ID: id}
	var pub sql.NullInt64
	var created, updated string
	err := s.DB.R.QueryRowContext(ctx, `SELECT title, template_id, client, status, latest_revision, published_revision, created_by, created_at, updated_at
		FROM documents WHERE id = ?`, id).Scan(&d.Title, &d.TemplateID, &d.Client, &d.Status, &d.LatestRevision, &pub, &d.CreatedBy, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errorf(model.ErrNotFound, "document %s does not exist", id)
	}
	if err != nil {
		return nil, fmt.Errorf("documents: get: %w", err)
	}
	d.PublishedRevision, d.CreatedAt, d.UpdatedAt = int(pub.Int64), parseTS(created), parseTS(updated)
	if revision == 0 {
		revision = d.LatestRevision
	}
	rows, err := s.DB.R.QueryContext(ctx, `SELECT revision, coalesce(parent_revision, 0), template_id, template_version, brand_id, brand_version,
		input_blob, warnings, review, created_by, created_at FROM revisions WHERE document_id = ? ORDER BY revision DESC`, id)
	if err != nil {
		return nil, fmt.Errorf("documents: revisions: %w", err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var r Revision
		var warnings, ts string
		var review sql.NullString
		if err := rows.Scan(&r.Revision, &r.Parent, &r.Template.ID, &r.Template.Version, &r.Brand.ID, &r.Brand.Version,
			&r.InputBlob, &warnings, &review, &r.CreatedBy, &ts); err != nil {
			return nil, fmt.Errorf("documents: scan revision: %w", err)
		}
		r.CreatedAt = parseTS(ts)
		_ = json.Unmarshal([]byte(warnings), &r.Warnings)
		if review.Valid {
			r.Review = json.RawMessage(review.String)
		}
		if r.Revision == revision {
			d.Revision, found = r, true
		}
		r.Warnings = nil
		d.Revisions = append(d.Revisions, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !found {
		return nil, errorf(model.ErrNotFound, "document %s has no revision %d (latest is %d)", id, revision, d.LatestRevision)
	}
	files, err := s.revisionFiles(ctx, id, revision)
	if err != nil {
		return nil, err
	}
	d.Revision.Files = files
	return d, nil
}

func (s *Service) revisionFiles(ctx context.Context, id string, revision int) ([]catalog.File, error) {
	rows, err := s.DB.R.QueryContext(ctx, `SELECT id, kind, coalesce(page, 0), name, mime, sha256, size FROM files
		WHERE document_id = ? AND revision = ? ORDER BY kind, page`, id, revision)
	if err != nil {
		return nil, fmt.Errorf("documents: files: %w", err)
	}
	defer rows.Close()
	var out []catalog.File
	for rows.Next() {
		var f catalog.File
		if err := rows.Scan(&f.ID, &f.Kind, &f.Page, &f.Name, &f.MIME, &f.SHA256, &f.Size); err != nil {
			return nil, fmt.Errorf("documents: scan file: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Source returns the canonical input of a revision (include_source).
func (s *Service) Source(ctx context.Context, id string, revision int) (json.RawMessage, error) {
	d, err := s.Get(ctx, id, revision)
	if err != nil {
		return nil, err
	}
	b, err := s.Blobs.Get(d.Revision.InputBlob)
	if err != nil {
		return nil, fmt.Errorf("documents: source blob: %w", err)
	}
	return b, nil
}

// Publish marks revision as final.
func (s *Service) Publish(ctx context.Context, actor, id string, revision int) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		d, err := loadDoc(ctx, tx, id)
		if err != nil {
			return err
		}
		if d.Trashed {
			return errorf(model.ErrConflict, "document %s is in the trash; restore it first", id)
		}
		if revision < 1 || revision > d.Latest {
			return errorf(model.ErrNotFound, "document %s has no revision %d (latest is %d)", id, revision, d.Latest)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE documents SET status = 'published', published_revision = ?, updated_at = ? WHERE id = ?`,
			revision, store.Now(s.Now()), id); err != nil {
			return fmt.Errorf("documents: publish: %w", err)
		}
		return store.Audit(ctx, tx, s.Now(), actor, "publish_document", "document", id, "ok", []byte(fmt.Sprintf(`{"revision":%d}`, revision)))
	})
}

// Trash moves a document to the trash; its files stop being served.
func (s *Service) Trash(ctx context.Context, actor, id string, confirmPublished bool) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		d, err := loadDoc(ctx, tx, id)
		if err != nil {
			return err
		}
		if d.Published.Valid && !confirmPublished {
			return errorf(model.ErrInvalidInput, "document %s is published; pass confirm_published=true to delete it", id)
		}
		if d.Trashed {
			return nil
		}
		now := store.Now(s.Now())
		if _, err := tx.ExecContext(ctx, `UPDATE documents SET status = 'trashed', trashed_at = ?, updated_at = ? WHERE id = ?`, now, now, id); err != nil {
			return fmt.Errorf("documents: trash: %w", err)
		}
		return store.Audit(ctx, tx, s.Now(), actor, "delete_document", "document", id, "ok", []byte(`{"purge":false}`))
	})
}

// Restore takes a document out of the trash (status published if it has a
// published revision, else draft).
func (s *Service) Restore(ctx context.Context, actor, id string) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		d, err := loadDoc(ctx, tx, id)
		if err != nil {
			return err
		}
		if !d.Trashed {
			return errorf(model.ErrConflict, "document %s is not in the trash", id)
		}
		status := "draft"
		if d.Published.Valid {
			status = "published"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE documents SET status = ?, trashed_at = NULL, updated_at = ? WHERE id = ?`, status, store.Now(s.Now()), id); err != nil {
			return fmt.Errorf("documents: restore: %w", err)
		}
		return store.Audit(ctx, tx, s.Now(), actor, "restore_document", "document", id, "ok", nil)
	})
}

// Purge permanently deletes a document, its revisions and file rows, then
// garbage-collects unreferenced blobs (subject to the grace period).
func (s *Service) Purge(ctx context.Context, actor, id string, confirmPublished bool) error {
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		d, err := loadDoc(ctx, tx, id)
		if err != nil {
			return err
		}
		if d.Published.Valid && !confirmPublished {
			return errorf(model.ErrInvalidInput, "document %s is published; pass confirm_published=true to purge it", id)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM documents WHERE id = ?`, id); err != nil { // cascades to revisions and files
			return fmt.Errorf("documents: purge: %w", err)
		}
		return store.Audit(ctx, tx, s.Now(), actor, "delete_document", "document", id, "ok", []byte(`{"purge":true}`))
	})
	if err != nil {
		return err
	}
	_, err = s.GC(ctx)
	return err
}

// PurgeExpiredTrash purges documents trashed longer than retention (P2 schedules it).
func (s *Service) PurgeExpiredTrash(ctx context.Context, retention time.Duration) (int, error) {
	rows, err := s.DB.R.QueryContext(ctx, `SELECT id FROM documents WHERE trashed_at IS NOT NULL AND trashed_at <= ?`, store.Now(s.Now().Add(-retention)))
	if err != nil {
		return 0, fmt.Errorf("documents: expired trash: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if err := s.Purge(ctx, "folio", id, true); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// DeleteExpiredFiles removes expired (template preview) file rows and runs GC.
func (s *Service) DeleteExpiredFiles(ctx context.Context) (int64, error) {
	var n int64
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM files WHERE expires_at IS NOT NULL AND expires_at <= ?`, store.Now(s.Now()))
		if err != nil {
			return fmt.Errorf("documents: expire files: %w", err)
		}
		n, _ = res.RowsAffected()
		return nil
	})
	if err != nil {
		return 0, err
	}
	_, err = s.GC(ctx)
	return n, err
}

// GC removes blobs no row references, older than the grace period.
func (s *Service) GC(ctx context.Context) (int, error) {
	return s.Blobs.GC(ctx, s.GCGrace, s.DB.ReferencedBlobs)
}

// FileForDownload resolves a public file id. Unknown, expired, trashed or
// purged files are not_found (P2 maps this to a bare 404).
func (s *Service) FileForDownload(ctx context.Context, fileID string) (catalog.File, []byte, error) {
	var f catalog.File
	var expires, trashed sql.NullString
	err := s.DB.R.QueryRowContext(ctx, `SELECT f.id, f.kind, coalesce(f.page, 0), f.name, f.mime, f.sha256, f.size, f.expires_at, d.trashed_at
		FROM files f LEFT JOIN documents d ON d.id = f.document_id WHERE f.id = ?`, fileID).
		Scan(&f.ID, &f.Kind, &f.Page, &f.Name, &f.MIME, &f.SHA256, &f.Size, &expires, &trashed)
	notFound := errorf(model.ErrNotFound, "file not found")
	if errors.Is(err, sql.ErrNoRows) {
		return f, nil, notFound
	}
	if err != nil {
		return f, nil, fmt.Errorf("documents: file lookup: %w", err)
	}
	if trashed.Valid || (expires.Valid && !parseTS(expires.String).After(s.Now())) {
		return f, nil, notFound
	}
	data, err := s.Blobs.Get(f.SHA256)
	if err != nil {
		return f, nil, notFound
	}
	return f, data, nil
}
