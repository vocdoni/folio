// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/vocdoni/folio/internal/activity"
	"github.com/vocdoni/folio/internal/assets"
	"github.com/vocdoni/folio/internal/model"
)

func (s *Server) assetRoutes(mux *http.ServeMux) {
	s.api(mux, "GET /ui/api/assets", route{}, s.handleListAssets)
	s.api(mux, "POST /ui/api/assets", route{action: "ui.asset.upload", kind: "asset"}, s.handleUploadAsset)
	s.api(mux, "GET /ui/api/assets/{id}/content", route{}, s.handleAssetContent)
	s.api(mux, "PATCH /ui/api/assets/{id}", route{action: "ui.asset.update", kind: "asset"}, s.handleUpdateAsset)
	s.api(mux, "DELETE /ui/api/assets/{id}", route{action: "ui.asset.delete", kind: "asset"}, s.handleDeleteAsset)
}

// AssetOut is an asset with its session URL.
type AssetOut struct {
	assets.Asset
	URL string `json:"url"`
}

func assetOut(a assets.Asset) AssetOut {
	return AssetOut{Asset: a, URL: "/ui/api/assets/" + a.ID + "/content"}
}

func (s *Server) handleListAssets(w http.ResponseWriter, r *http.Request) {
	limit, err := intParam(r, "limit", 60, 1, 200)
	if err != nil {
		writeError(w, r, err)
		return
	}
	items, next, err := s.o.Assets.List(r.Context(), r.URL.Query().Get("query"), limit, r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := struct {
		Items      []AssetOut `json:"items"`
		NextCursor string     `json:"next_cursor,omitempty"`
	}{Items: []AssetOut{}, NextCursor: next}
	for _, a := range items {
		out.Items = append(out.Items, assetOut(a))
	}
	writeJSON(w, http.StatusOK, out)
}

// readUpload reads the single "file" part of a multipart form (≤ maxBytes)
// and the optional "name" and "description" fields.
func readUpload(r *http.Request, maxBytes int64) (name, desc string, data []byte, err error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return "", "", nil, &model.Error{Code: model.ErrInvalidInput, Message: "send multipart/form-data with a file field"}
	}
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if mbe, ok := errors.AsType[*http.MaxBytesError](err); ok {
				return "", "", nil, mbe
			}
			return "", "", nil, &model.Error{Code: model.ErrInvalidInput, Message: "malformed multipart body"}
		}
		switch p.FormName() {
		case "file":
			if name == "" {
				name = p.FileName()
			}
			b, err := io.ReadAll(io.LimitReader(p, maxBytes+1))
			if err != nil {
				return "", "", nil, err
			}
			if int64(len(b)) > maxBytes {
				return "", "", nil, &model.Error{Code: model.ErrTooLarge, Message: fmt.Sprintf("the file exceeds %d bytes", maxBytes)}
			}
			data = b
		case "name", "description":
			b, err := io.ReadAll(io.LimitReader(p, 2001))
			if err != nil {
				return "", "", nil, err
			}
			if p.FormName() == "name" {
				name = strings.TrimSpace(string(b))
			} else {
				desc = strings.TrimSpace(string(b))
			}
		}
		_ = p.Close()
	}
	if data == nil {
		return "", "", nil, &model.Error{Code: model.ErrInvalidInput, Message: "the form has no file"}
	}
	return name, desc, data, nil
}

func (s *Server) handleUploadAsset(w http.ResponseWriter, r *http.Request) {
	name, desc, data, err := readUpload(r, s.o.Config.MaxAssetBytes)
	if err != nil {
		writeError(w, r, err)
		return
	}
	a, err := s.o.Assets.Upload(r.Context(), Actor, assets.UploadRequest{Name: name, Description: desc,
		ContentBase64: base64.StdEncoding.EncodeToString(data)})
	if err != nil {
		writeError(w, r, err)
		return
	}
	noteActivity(r, a.ID, activity.Detail{Bytes: int64(a.Size)})
	writeJSON(w, http.StatusOK, assetOut(*a))
}

// handleAssetContent serves the sanitised asset. SVGs get a sandboxing CSP
// so opening one directly can never run script.
func (s *Server) handleAssetContent(w http.ResponseWriter, r *http.Request) {
	a, err := s.o.Assets.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	data, err := s.o.Blobs.Get(a.SHA256)
	if err != nil {
		writeError(w, r, fmt.Errorf("webui: asset blob: %w", err))
		return
	}
	h := w.Header()
	h.Set("Content-Type", a.MIME)
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	h.Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": "asset" + extOf(a.MIME)}))
	h.Set("Cache-Control", "private, max-age=86400, immutable")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func extOf(m string) string {
	switch m {
	case "image/svg+xml":
		return ".svg"
	case "image/jpeg":
		return ".jpg"
	}
	return ".png"
}

// AssetUpdateIn is the PATCH body: the new name and description (an empty
// description clears it).
type AssetUpdateIn struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s *Server) handleUpdateAsset(w http.ResponseWriter, r *http.Request) {
	var in AssetUpdateIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	id := r.PathValue("id")
	noteActivity(r, id, activity.Detail{})
	a, err := s.o.Assets.Update(r.Context(), Actor, id, in.Name, in.Description)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, assetOut(*a))
}

func (s *Server) handleDeleteAsset(w http.ResponseWriter, r *http.Request) {
	if err := s.o.Assets.Delete(r.Context(), Actor, r.PathValue("id")); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"asset_id": r.PathValue("id"), "deleted": true})
}
