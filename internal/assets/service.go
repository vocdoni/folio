// SPDX-License-Identifier: AGPL-3.0-only

package assets

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"image/png"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/store"
)

// Rasterizer renders sanitised SVG to PNG (render.Engine.SVGToPNG).
type Rasterizer interface {
	SVGToPNG(ctx context.Context, svg []byte) ([]byte, error)
}

// Asset is an asset row.
type Asset struct {
	ID              string    `json:"asset_id"`
	Name            string    `json:"name"`
	Description     string    `json:"description,omitempty"`
	MIME            string    `json:"mime"`
	SHA256          string    `json:"sha256"`
	Size            int       `json:"size"`
	Width           int       `json:"width"`
	Height          int       `json:"height"`
	RenditionSHA256 string    `json:"-"`
	CreatedBy       string    `json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
	Deleted         bool      `json:"-"`
}

// UploadRequest mirrors upload_asset: exactly one of ContentBase64 and URL.
type UploadRequest struct {
	Name          string
	Description   string
	ContentBase64 string
	URL           string
}

// Service stores assets.
type Service struct {
	DB       *store.DB
	Blobs    *store.Blobs
	Fetcher  *Fetcher
	Raster   Rasterizer
	MaxBytes int64
	Now      func() time.Time
}

func invalid(format string, args ...any) error {
	return &model.Error{Code: model.ErrInvalidInput, Message: fmt.Sprintf(format, args...)}
}

// Upload validates, sanitises and stores an asset.
func (s *Service) Upload(ctx context.Context, actor string, req UploadRequest) (*Asset, error) {
	name, description := strings.TrimSpace(req.Name), strings.TrimSpace(req.Description)
	if n := utf8.RuneCountInString(name); n == 0 || n > 200 {
		return nil, invalid("name is required (1 to 200 characters)")
	}
	if utf8.RuneCountInString(description) > 500 {
		return nil, invalid("description is at most 500 characters")
	}
	var raw []byte
	switch {
	case (req.ContentBase64 == "") == (req.URL == ""):
		return nil, invalid("send exactly one of content_base64 or url")
	case req.ContentBase64 != "":
		if int64(base64.StdEncoding.DecodedLen(len(req.ContentBase64))) > s.MaxBytes+3 {
			return nil, &model.Error{Code: model.ErrTooLarge, Message: fmt.Sprintf("asset exceeds %d bytes", s.MaxBytes)}
		}
		b, err := base64.StdEncoding.DecodeString(req.ContentBase64)
		if err != nil {
			return nil, invalid("content_base64 is not valid standard base64")
		}
		raw = b
	default:
		b, err := s.Fetcher.Fetch(ctx, req.URL)
		switch {
		case errors.Is(err, ErrTooLarge):
			return nil, &model.Error{Code: model.ErrTooLarge, Message: err.Error()}
		case errors.Is(err, ErrBadURL), errors.Is(err, ErrForbiddenAddress):
			return nil, invalid("%v", err)
		case err != nil:
			return nil, &model.Error{Code: model.ErrUnavailable, Message: "could not download the asset; check the URL or upload content_base64", Err: err}
		}
		raw = b
	}
	san, err := Sanitize(raw, s.MaxBytes)
	switch {
	case errors.Is(err, ErrTooLarge):
		return nil, &model.Error{Code: model.ErrTooLarge, Message: err.Error()}
	case err != nil:
		return nil, invalid("%v", err)
	}
	a := &Asset{ID: uuid.Must(uuid.NewV7()).String(), Name: name, Description: description, MIME: san.MIME,
		Size: len(san.Data), Width: san.Width, Height: san.Height, CreatedBy: actor, CreatedAt: s.Now().UTC().Truncate(time.Second)}
	if san.MIME == MIMESVG {
		pngData, err := s.Raster.SVGToPNG(ctx, san.Data)
		if err != nil {
			return nil, invalid("the SVG could not be rendered: %v", err)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(pngData))
		if err != nil {
			return nil, fmt.Errorf("assets: rendition: %w", err)
		}
		if int64(cfg.Width)*int64(cfg.Height) > MaxPixels || int64(len(pngData)) > s.MaxBytes {
			return nil, &model.Error{Code: model.ErrTooLarge,
				Message: fmt.Sprintf("the SVG rasterises to %dx%d px (%d bytes); reduce its width/height (limit %d pixels, %d bytes)", cfg.Width, cfg.Height, len(pngData), MaxPixels, s.MaxBytes)}
		}
		if a.Width == 0 || a.Height == 0 {
			a.Width, a.Height = cfg.Width, cfg.Height
		}
		if a.RenditionSHA256, err = s.Blobs.Put(pngData); err != nil {
			return nil, err
		}
	}
	if a.SHA256, err = s.Blobs.Put(san.Data); err != nil {
		return nil, err
	}
	err = s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		var rend any
		if a.RenditionSHA256 != "" {
			rend = a.RenditionSHA256
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO assets (id, name, description, mime, sha256, size, width, height, rendition_sha256, created_by, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, a.ID, a.Name, a.Description, a.MIME, a.SHA256, a.Size, a.Width, a.Height, rend, actor, store.Now(a.CreatedAt)); err != nil {
			return fmt.Errorf("assets: insert: %w", err)
		}
		return store.Audit(ctx, tx, a.CreatedAt, actor, "upload_asset", "asset", a.ID, "ok", nil)
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}

// Get returns an asset row (deleted ones included, Deleted set).
func (s *Service) Get(ctx context.Context, id string) (*Asset, error) {
	a := &Asset{ID: id}
	var rend, deleted sql.NullString
	var ts string
	err := s.DB.R.QueryRowContext(ctx, `SELECT name, description, mime, sha256, size, width, height, rendition_sha256, created_by, created_at, deleted_at
		FROM assets WHERE id = ?`, id).Scan(&a.Name, &a.Description, &a.MIME, &a.SHA256, &a.Size, &a.Width, &a.Height, &rend, &a.CreatedBy, &ts, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &model.Error{Code: model.ErrNotFound, Message: fmt.Sprintf("asset %s does not exist", id)}
	}
	if err != nil {
		return nil, fmt.Errorf("assets: get: %w", err)
	}
	a.RenditionSHA256, a.Deleted = rend.String, deleted.Valid
	a.CreatedAt, _ = time.Parse(time.RFC3339, ts)
	return a, nil
}

// Asset implements model.AssetResolver.
func (s *Service) Asset(ctx context.Context, id string, allowDeleted bool) (model.ResolvedAsset, error) {
	a, err := s.Get(ctx, id)
	if err != nil || (a.Deleted && !allowDeleted) {
		return model.ResolvedAsset{}, model.ErrAssetNotFound
	}
	data, err := s.Blobs.Get(a.SHA256)
	if err != nil {
		return model.ResolvedAsset{}, fmt.Errorf("assets: blob %s: %w", a.SHA256, err)
	}
	out := model.ResolvedAsset{ID: a.ID, MIME: a.MIME, Data: data, Width: a.Width, Height: a.Height}
	if a.RenditionSHA256 != "" {
		if out.PNG, err = s.Blobs.Get(a.RenditionSHA256); err != nil {
			return model.ResolvedAsset{}, fmt.Errorf("assets: rendition: %w", err)
		}
	}
	return out, nil
}

// Update renames a live asset and replaces its description (an empty
// description clears it). The stored blobs are untouched.
func (s *Service) Update(ctx context.Context, actor, id, name, description string) (*Asset, error) {
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if n := utf8.RuneCountInString(name); n == 0 || n > 200 {
		return nil, invalid("name is required (1 to 200 characters)")
	}
	if utf8.RuneCountInString(description) > 500 {
		return nil, invalid("description is at most 500 characters")
	}
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE assets SET name = ?, description = ? WHERE id = ? AND deleted_at IS NULL`, name, description, id)
		if err != nil {
			return fmt.Errorf("assets: update: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return &model.Error{Code: model.ErrNotFound, Message: fmt.Sprintf("asset %s does not exist or is deleted", id)}
		}
		return store.Audit(ctx, tx, s.Now(), actor, "update_asset", "asset", id, "ok", nil)
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Delete hides an asset from listings and new references (blobs stay).
func (s *Service) Delete(ctx context.Context, actor, id string) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE assets SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL`, store.Now(s.Now()), id)
		if err != nil {
			return fmt.Errorf("assets: delete: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return &model.Error{Code: model.ErrNotFound, Message: fmt.Sprintf("asset %s does not exist or is already deleted", id)}
		}
		return store.Audit(ctx, tx, s.Now(), actor, "delete_asset", "asset", id, "ok", nil)
	})
}

// List returns live assets newest first by (created_at, id); query matches
// name or description case-insensitively. The cursor is
// base64url("<created_at>|<id>") of the last item.
func (s *Service) List(ctx context.Context, query string, limit int, cursor string) ([]Asset, string, error) {
	switch {
	case limit <= 0:
		limit = 50
	case limit > 200:
		limit = 200
	}
	q := `SELECT id, name, description, mime, sha256, size, width, height, created_by, created_at
	      FROM assets WHERE deleted_at IS NULL`
	var args []any
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		ts, id, ok := strings.Cut(string(raw), "|")
		if err != nil || !ok {
			return nil, "", invalid("invalid cursor")
		}
		q += ` AND (created_at < ? OR (created_at = ? AND id < ?))`
		args = append(args, ts, ts, id)
	}
	if query != "" {
		like := "%" + strings.ToLower(query) + "%"
		q += ` AND (lower(name) LIKE ? OR lower(description) LIKE ?)`
		args = append(args, like, like)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.DB.R.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("assets: list: %w", err)
	}
	defer rows.Close()
	var out []Asset
	var stamps []string
	for rows.Next() {
		var a Asset
		var ts string
		if err := rows.Scan(&a.ID, &a.Name, &a.Description, &a.MIME, &a.SHA256, &a.Size, &a.Width, &a.Height, &a.CreatedBy, &ts); err != nil {
			return nil, "", fmt.Errorf("assets: scan: %w", err)
		}
		a.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		out = append(out, a)
		stamps = append(stamps, ts)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = base64.RawURLEncoding.EncodeToString([]byte(stamps[limit-1] + "|" + out[limit-1].ID))
	}
	return out, next, nil
}
