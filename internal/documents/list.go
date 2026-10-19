// SPDX-License-Identifier: AGPL-3.0-only

package documents

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/vocdoni/folio/internal/model"
)

// ListFilter mirrors list_documents.
type ListFilter struct {
	Query          string // case-insensitive substring of title or client
	TemplateID     string
	Inline         bool   // only documents whose current template is inline
	Status         string // draft | published | trashed
	Client         string
	CreatedBy      string
	IncludeTrashed bool
	Limit          int    // default 50, max 200
	Cursor         string // opaque
}

// Summary is one list entry.
type Summary struct {
	ID                string    `json:"id"`
	Title             string    `json:"title"`
	TemplateID        string    `json:"template_id"`
	TemplateInline    bool      `json:"template_inline"` // the current template is an inline template
	Client            string    `json:"client"`
	Status            string    `json:"status"`
	LatestRevision    int       `json:"latest_revision"`
	PublishedRevision int       `json:"published_revision,omitempty"`
	CreatedBy         string    `json:"created_by"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// List returns documents newest first by (created_at, id); the cursor is
// base64url("<created_at>|<id>") of the last item.
func (s *Service) List(ctx context.Context, f ListFilter) ([]Summary, string, error) {
	limit := f.Limit
	switch {
	case limit <= 0:
		limit = 50
	case limit > 200:
		limit = 200
	}
	if f.Inline && f.TemplateID != "" {
		return nil, "", &model.Error{Code: model.ErrInvalidInput, Message: "pass template_id or inline=true, not both"}
	}
	q := `SELECT id, title, template_id, client, status, latest_revision, coalesce(published_revision, 0), created_by, created_at, updated_at
	      FROM documents WHERE 1=1`
	var args []any
	if f.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(f.Cursor)
		ts, id, ok := strings.Cut(string(raw), "|")
		if err != nil || !ok {
			return nil, "", &model.Error{Code: model.ErrInvalidInput, Message: "invalid cursor"}
		}
		q += ` AND (created_at < ? OR (created_at = ? AND id < ?))`
		args = append(args, ts, ts, id)
	}
	if !f.IncludeTrashed && f.Status != "trashed" {
		q += ` AND trashed_at IS NULL`
	}
	for col, v := range map[string]string{"template_id": f.TemplateID, "status": f.Status, "client": f.Client, "created_by": f.CreatedBy} {
		if v != "" {
			q += ` AND ` + col + ` = ?`
			args = append(args, v)
		}
	}
	if f.Inline {
		q += ` AND template_id LIKE 'inline-%'`
	}
	if f.Query != "" {
		like := "%" + strings.ToLower(f.Query) + "%"
		q += ` AND (lower(title) LIKE ? OR lower(client) LIKE ?)`
		args = append(args, like, like)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.DB.R.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("documents: list: %w", err)
	}
	defer rows.Close()
	var out []Summary
	var lastTS []string
	for rows.Next() {
		var d Summary
		var created, updated string
		if err := rows.Scan(&d.ID, &d.Title, &d.TemplateID, &d.Client, &d.Status, &d.LatestRevision, &d.PublishedRevision, &d.CreatedBy, &created, &updated); err != nil {
			return nil, "", fmt.Errorf("documents: scan: %w", err)
		}
		d.CreatedAt, d.UpdatedAt, d.TemplateInline = parseTS(created), parseTS(updated), model.IsInlineID(d.TemplateID)
		out = append(out, d)
		lastTS = append(lastTS, created)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = base64.RawURLEncoding.EncodeToString([]byte(lastTS[limit-1] + "|" + out[limit-1].ID))
	}
	return out, next, nil
}
