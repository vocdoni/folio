// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vocdoni/folio/internal/assets"
)

type UploadAssetIn struct {
	Name          string `json:"name" jsonschema:"file name shown in listings, e.g. logo.svg"`
	ContentBase64 string `json:"content_base64,omitempty" jsonschema:"standard base64 of a PNG, JPEG, WebP or SVG file; exactly one of content_base64 and url"`
	URL           string `json:"url,omitempty" jsonschema:"public https URL to fetch (private and internal addresses are refused)"`
	Description   string `json:"description,omitempty"`
}

type AssetOut struct {
	AssetID     string    `json:"asset_id" jsonschema:"use as ![caption](asset:<asset_id>) in markdown or as brand logo.asset_id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	MIME        string    `json:"mime" jsonschema:"image/png, image/jpeg or image/svg+xml (WebP is converted to PNG)"`
	Width       int       `json:"width"`
	Height      int       `json:"height"`
	Size        int       `json:"size"`
	SHA256      string    `json:"sha256"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
}

func assetOut(a *assets.Asset) AssetOut {
	return AssetOut{AssetID: a.ID, Name: a.Name, Description: a.Description, MIME: a.MIME, Width: a.Width, Height: a.Height,
		Size: a.Size, SHA256: a.SHA256, CreatedBy: a.CreatedBy, CreatedAt: a.CreatedAt}
}

type ListAssetsIn struct {
	Query  string `json:"query,omitempty" jsonschema:"case-insensitive substring of name or description"`
	Limit  int    `json:"limit,omitempty" jsonschema:"1-200, default 50"`
	Cursor string `json:"cursor,omitempty"`
}

type ListAssetsOut struct {
	Assets     []AssetOut `json:"assets"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

type IDIn struct {
	ID string `json:"id"`
}

type DeleteAssetOut struct {
	AssetID string `json:"asset_id"`
	Deleted bool   `json:"deleted"`
}

const (
	descUploadAsset = `Upload an image for use in documents (![caption](asset:<asset_id>)) or as a brand logo. Send exactly one of content_base64 (standard base64) or url (public https only; private, loopback and cloud-metadata addresses are refused; at most 3 redirects, 20 s). Accepted: PNG, JPEG, WebP (stored as PNG) and SVG without scripts, event handlers, foreignObject or external references; raster images are re-encoded without metadata, max 50 megapixels and get_capabilities.limits.max_asset_bytes. Upload once and reuse the asset_id; list_assets finds existing ones.`

	descListAssets = `List uploaded assets, newest first, with ids, types and pixel sizes. Deleted assets are not listed. Paginate with limit and next_cursor.`

	descDeleteAsset = `Delete an asset: it disappears from list_assets and can no longer be referenced by new documents or brands. Documents, templates and brand versions that already use it keep rendering. Not reversible through MCP.`
)

func registerAssetTools(s *mcp.Server, d *Deps) {
	register(s, d, tool[UploadAssetIn, AssetOut]{Name: "upload_asset", Title: "Upload an image asset", Description: descUploadAsset,
		Access: write, OpenWorld: true,
		Target: func(in UploadAssetIn) (string, string) {
			n := in.Name
			if len(n) > 200 {
				n = n[:200]
			}
			return "asset", n
		},
		Handle: func(ctx context.Context, c *call, in UploadAssetIn) (*AssetOut, error) {
			a, err := d.Assets.Upload(ctx, c.Actor, assets.UploadRequest{Name: in.Name, Description: in.Description, ContentBase64: in.ContentBase64, URL: in.URL})
			if err != nil {
				return nil, err
			}
			out := assetOut(a)
			c.Summary("uploaded %s as asset %s (%s, %dx%d)", a.Name, a.ID, a.MIME, a.Width, a.Height)
			return &out, nil
		}})

	register(s, d, tool[ListAssetsIn, ListAssetsOut]{Name: "list_assets", Title: "List assets", Description: descListAssets, Access: readOnly,
		Handle: func(ctx context.Context, c *call, in ListAssetsIn) (*ListAssetsOut, error) {
			limit, err := listLimit(in.Limit)
			if err != nil {
				return nil, err
			}
			list, next, err := d.Assets.List(ctx, in.Query, limit, in.Cursor)
			if err != nil {
				return nil, err
			}
			out := &ListAssetsOut{Assets: []AssetOut{}, NextCursor: next}
			for i := range list {
				out.Assets = append(out.Assets, assetOut(&list[i]))
			}
			c.Summary("%d asset(s)", len(out.Assets))
			return out, nil
		}})

	register(s, d, tool[IDIn, DeleteAssetOut]{Name: "delete_asset", Title: "Delete an asset", Description: descDeleteAsset, Access: destructive,
		Target: func(in IDIn) (string, string) { return "asset", in.ID },
		Handle: func(ctx context.Context, c *call, in IDIn) (*DeleteAssetOut, error) {
			if err := d.Assets.Delete(ctx, c.Actor, in.ID); err != nil {
				return nil, err
			}
			c.Summary("deleted asset %s", in.ID)
			return &DeleteAssetOut{AssetID: in.ID, Deleted: true}, nil
		}})
}
