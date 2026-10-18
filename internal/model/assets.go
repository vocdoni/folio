// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"context"
	"errors"
)

// ResolvedAsset is a sanitised asset as the renderer needs it.
type ResolvedAsset struct {
	ID     string
	MIME   string // image/png | image/jpeg | image/svg+xml
	Data   []byte // sanitised original (used by Typst)
	PNG    []byte // PNG rendition for SVG (used by DOCX); nil for raster assets
	Width  int
	Height int
}

// AssetResolver loads assets. allowDeleted is true only for brand logos of
// existing brand versions; Markdown images may not reference deleted assets.
type AssetResolver interface {
	Asset(ctx context.Context, id string, allowDeleted bool) (ResolvedAsset, error)
}

// ErrAssetNotFound is returned by resolvers for unknown assets (and for
// deleted ones when allowDeleted is false).
var ErrAssetNotFound = errors.New("asset not found")
