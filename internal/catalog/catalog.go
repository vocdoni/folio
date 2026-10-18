// SPDX-License-Identifier: AGPL-3.0-only

// Package catalog stores versioned brands and templates: create (v1),
// update with optimistic concurrency (expected_version), archive/unarchive,
// get (latest or a version) and list with cursors. Template create/update
// validate the spec and render its sample before committing.
package catalog

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/store"
)

// Kind selects brands or templates (same table layout).
type Kind string

const (
	KindBrand    Kind = "brand"
	KindTemplate Kind = "template"
)

func (k Kind) tables() (versions, heads string) {
	if k == KindBrand {
		return "brands", "brand_heads"
	}
	return "templates", "template_heads"
}

// Version is one entry of an item's history.
type Version struct {
	Version   int       `json:"version"`
	Note      string    `json:"note"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

// Item is a stored version plus head state.
type Item struct {
	ID            string
	Version       int
	LatestVersion int
	Spec          json.RawMessage // canonical spec JSON (spec_version included)
	Archived      bool
	Versions      []Version // filled by Get
}

// conflict builds the documented conflict error carrying the current latest version.
func conflict(kind Kind, id string, latest int) error {
	return &model.Error{Code: model.ErrConflict,
		Message: fmt.Sprintf("%s %s is at version %d; fetch it, re-apply your change and pass expected_version=%d", kind, id, latest, latest)}
}

func notFound(kind Kind, id string) error {
	return &model.Error{Code: model.ErrNotFound, Message: fmt.Sprintf("%s %q does not exist", kind, id)}
}

// putVersion inserts version 1 (create) or latest+1 (update) inside one
// IMMEDIATE transaction. For updates expectedVersion must equal the current
// latest version, otherwise the transaction is rolled back with conflict.
// The single writer connection serialises concurrent updates, so of two
// updates with the same expected_version exactly one succeeds.
func putVersion(ctx context.Context, db *store.DB, kind Kind, id string, create bool, expectedVersion int,
	spec []byte, note, actor string, now time.Time) (int, error) {
	versions, heads := kind.tables()
	var newVersion int
	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		var latest int
		var archived sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT latest_version, archived_at FROM `+heads+` WHERE id = ?`, id).Scan(&latest, &archived)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if !create {
				return notFound(kind, id)
			}
			newVersion = 1
		case err != nil:
			return fmt.Errorf("catalog: read head: %w", err)
		case create:
			return &model.Error{Code: model.ErrConflict, Message: fmt.Sprintf("%s %s already exists (version %d); use update with expected_version=%d", kind, id, latest, latest)}
		case expectedVersion != latest:
			return conflict(kind, id, latest)
		default:
			newVersion = latest + 1
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO `+versions+` (id, version, spec, note, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			id, newVersion, string(spec), note, actor, store.Now(now)); err != nil {
			return fmt.Errorf("catalog: insert version: %w", err)
		}
		if newVersion == 1 {
			_, err = tx.ExecContext(ctx, `INSERT INTO `+heads+` (id, latest_version) VALUES (?, 1)`, id)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE `+heads+` SET latest_version = ? WHERE id = ? AND latest_version = ?`, newVersion, id, newVersion-1)
		}
		if err != nil {
			return fmt.Errorf("catalog: update head: %w", err)
		}
		action := "update_" + string(kind)
		if create {
			action = "create_" + string(kind)
		}
		return store.Audit(ctx, tx, now, actor, action, string(kind), id, "ok", []byte(fmt.Sprintf(`{"version":%d}`, newVersion)))
	})
	return newVersion, err
}

// setArchived archives (archived=true) or unarchives an item.
func setArchived(ctx context.Context, db *store.DB, kind Kind, id string, archived bool, actor string, now time.Time) error {
	_, heads := kind.tables()
	return db.WriteTx(ctx, func(tx *sql.Tx) error {
		var val any
		if archived {
			val = store.Now(now)
		}
		res, err := tx.ExecContext(ctx, `UPDATE `+heads+` SET archived_at = ? WHERE id = ?`, val, id)
		if err != nil {
			return fmt.Errorf("catalog: archive: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return notFound(kind, id)
		}
		return store.Audit(ctx, tx, now, actor, "archive_"+string(kind), string(kind), id, "ok", []byte(fmt.Sprintf(`{"archived":%t}`, archived)))
	})
}

// get returns version (0 = latest) with the history.
func get(ctx context.Context, db *store.DB, kind Kind, id string, version int) (*Item, error) {
	versions, heads := kind.tables()
	it := &Item{ID: id}
	var archived sql.NullString
	err := db.R.QueryRowContext(ctx, `SELECT latest_version, archived_at FROM `+heads+` WHERE id = ?`, id).Scan(&it.LatestVersion, &archived)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound(kind, id)
	}
	if err != nil {
		return nil, fmt.Errorf("catalog: get head: %w", err)
	}
	it.Archived = archived.Valid
	it.Version = version
	if version == 0 {
		it.Version = it.LatestVersion
	}
	var spec string
	err = db.R.QueryRowContext(ctx, `SELECT spec FROM `+versions+` WHERE id = ? AND version = ?`, id, it.Version).Scan(&spec)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &model.Error{Code: model.ErrNotFound, Message: fmt.Sprintf("%s %s has no version %d (latest is %d)", kind, id, version, it.LatestVersion)}
	}
	if err != nil {
		return nil, fmt.Errorf("catalog: get version: %w", err)
	}
	it.Spec = json.RawMessage(spec)
	rows, err := db.R.QueryContext(ctx, `SELECT version, note, created_by, created_at FROM `+versions+` WHERE id = ? ORDER BY version`, id)
	if err != nil {
		return nil, fmt.Errorf("catalog: versions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v Version
		var ts string
		if err := rows.Scan(&v.Version, &v.Note, &v.CreatedBy, &ts); err != nil {
			return nil, fmt.Errorf("catalog: scan version: %w", err)
		}
		v.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		it.Versions = append(it.Versions, v)
	}
	return it, rows.Err()
}

// ListFilter selects items; brands and templates sort by id ascending.
type ListFilter struct {
	Query           string // case-insensitive substring of id, name or description
	Category        string // templates only
	IncludeArchived bool
	Limit           int    // default 50, max 200
	Cursor          string // opaque, from a previous page
}

// EncodeCursor / DecodeCursor implement the opaque cursor (base64url of the sort key).
func EncodeCursor(key string) string { return base64.RawURLEncoding.EncodeToString([]byte(key)) }

func DecodeCursor(c string) (string, error) {
	if c == "" {
		return "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", &model.Error{Code: model.ErrInvalidInput, Message: "invalid cursor"}
	}
	return string(b), nil
}

func clampLimit(n int) int {
	switch {
	case n <= 0:
		return 50
	case n > 200:
		return 200
	}
	return n
}

func list(ctx context.Context, db *store.DB, kind Kind, f ListFilter) ([]*Item, string, error) {
	versions, heads := kind.tables()
	after, err := DecodeCursor(f.Cursor)
	if err != nil {
		return nil, "", err
	}
	limit := clampLimit(f.Limit)
	q := `SELECT h.id, h.latest_version, h.archived_at, v.spec FROM ` + heads + ` h
	      JOIN ` + versions + ` v ON v.id = h.id AND v.version = h.latest_version WHERE h.id > ?`
	args := []any{after}
	if !f.IncludeArchived {
		q += ` AND h.archived_at IS NULL`
	}
	if f.Query != "" {
		like := "%" + strings.ToLower(f.Query) + "%"
		q += ` AND (lower(h.id) LIKE ? OR lower(json_extract(v.spec, '$.name')) LIKE ? OR lower(coalesce(json_extract(v.spec, '$.description'), '')) LIKE ?)`
		args = append(args, like, like, like)
	}
	if f.Category != "" && kind == KindTemplate {
		q += ` AND json_extract(v.spec, '$.category') = ?`
		args = append(args, f.Category)
	}
	q += ` ORDER BY h.id LIMIT ?`
	args = append(args, limit+1)
	rows, err := db.R.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("catalog: list: %w", err)
	}
	defer rows.Close()
	var out []*Item
	for rows.Next() {
		it := &Item{}
		var archived sql.NullString
		var spec string
		if err := rows.Scan(&it.ID, &it.LatestVersion, &archived, &spec); err != nil {
			return nil, "", fmt.Errorf("catalog: scan: %w", err)
		}
		it.Version, it.Archived, it.Spec = it.LatestVersion, archived.Valid, json.RawMessage(spec)
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = EncodeCursor(out[limit-1].ID)
	}
	return out, next, nil
}
