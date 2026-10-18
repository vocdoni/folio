// SPDX-License-Identifier: AGPL-3.0-only

package documents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/store"
)

// SetReview stores (replacing any previous one) the advisory AI review of a
// revision. review must be a JSON object. Trashed documents are refused.
func (s *Service) SetReview(ctx context.Context, actor, id string, revision int, review json.RawMessage) error {
	if !json.Valid(review) {
		return errorf(model.ErrInternal, "review is not valid JSON")
	}
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		d, err := loadDoc(ctx, tx, id)
		if err != nil {
			return err
		}
		if d.Trashed {
			return errorf(model.ErrConflict, "document %s is in the trash; restore it first", id)
		}
		res, err := tx.ExecContext(ctx, `UPDATE revisions SET review = ? WHERE document_id = ? AND revision = ?`, string(review), id, revision)
		if err != nil {
			return fmt.Errorf("documents: set review: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errorf(model.ErrNotFound, "document %s has no revision %d (latest is %d)", id, revision, d.Latest)
		}
		return store.Audit(ctx, tx, s.Now(), actor, "review_document", "document", id, "ok", []byte(fmt.Sprintf(`{"revision":%d}`, revision)))
	})
}
