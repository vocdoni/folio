// SPDX-License-Identifier: AGPL-3.0-only

package documents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/vocdoni/folio/internal/model"
)

// resolveTemplate returns the template of a render: the normalised inline
// spec (version 1) when req.Template is set, else template id@version,
// stored or inline. inline is the canonical spec of an inline template (nil
// for stored ones); the render transaction inserts it.
func (s *Service) resolveTemplate(ctx context.Context, req RenderRequest) (t model.Template, version int, inline []byte, err error) {
	if req.Template != nil {
		t, inline, err = s.Resolver.InlineTemplate(ctx, *req.Template, req.BrandID, req.Input.Metadata)
		return t, 1, inline, err
	}
	t, version, err = s.Resolver.ResolveTemplate(ctx, req.TemplateID, req.TemplateVersion, false)
	if err == nil && model.IsInlineID(req.TemplateID) {
		// Re-inserted (or ignored) in the transaction: the retention pass may
		// collect an unreferenced row between this read and the commit.
		inline, err = model.Canonical(t)
	}
	return t, version, inline, err
}

// insertInline stores inline template id (version 1, note "inline") inside
// tx unless it exists. An existing row must hold the same spec: a different
// one is a hash-prefix collision, and the render fails rather than use
// another caller's template.
func insertInline(ctx context.Context, tx *sql.Tx, id string, spec []byte, actor, ts string) error {
	res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO templates (id, version, spec, note, created_by, created_at)
		VALUES (?, 1, ?, 'inline', ?, ?)`, id, string(spec), actor, ts)
	if err != nil {
		return fmt.Errorf("documents: insert inline template: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var cur string
		if err := tx.QueryRowContext(ctx, `SELECT spec FROM templates WHERE id = ? AND version = 1`, id).Scan(&cur); err != nil {
			return fmt.Errorf("documents: read inline template: %w", err)
		}
		if cur != string(spec) {
			return &model.Error{Code: model.ErrInternal, Message: fmt.Sprintf("inline template id %s collides with a different stored spec", id)}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO template_heads (id, latest_version) VALUES (?, 1)`, id); err != nil {
		return fmt.Errorf("documents: insert inline template head: %w", err)
	}
	return nil
}

// InlineSource returns the inline template of document id and its latest
// revision's input as a template sample (save_inline_template). A document
// whose current template is stored fails with invalid_input.
func (s *Service) InlineSource(ctx context.Context, id string) (string, model.Sample, error) {
	d, err := s.Get(ctx, id, 0)
	if err != nil {
		return "", model.Sample{}, err
	}
	if !model.IsInlineID(d.TemplateID) {
		return "", model.Sample{}, errorf(model.ErrInvalidInput, "document %s uses the stored template %s, not an inline template", id, d.TemplateID)
	}
	raw, err := s.Blobs.Get(d.Revision.InputBlob)
	if err != nil {
		return "", model.Sample{}, fmt.Errorf("documents: source blob: %w", err)
	}
	var in model.Sample // the canonical input's markdown, metadata and data
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", model.Sample{}, fmt.Errorf("documents: stored input: %w", err)
	}
	return d.TemplateID, in, nil
}
