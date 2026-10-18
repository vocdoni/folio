// SPDX-License-Identifier: AGPL-3.0-only

package assets

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/store"
)

type fakeRaster struct{ png []byte }

func (f fakeRaster) SVGToPNG(context.Context, []byte) ([]byte, error) { return f.png, nil }

func newService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	blobs, _ := store.NewBlobs(dir+"/blobs", time.Now)
	return &Service{DB: db, Blobs: blobs, Fetcher: NewFetcher(1 << 20), Raster: fakeRaster{png: pngBytes(t, 30, 10)}, MaxBytes: 1 << 20, Now: time.Now}
}

func TestUploadResolveDelete(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	a, err := s.Upload(ctx, "alice", UploadRequest{Name: "logo.png", ContentBase64: base64.StdEncoding.EncodeToString(pngBytes(t, 8, 4))})
	if err != nil || a.MIME != MIMEPNG || a.Width != 8 {
		t.Fatalf("png upload: %+v %v", a, err)
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 30 10"><rect width="30" height="10"/></svg>`
	sv, err := s.Upload(ctx, "alice", UploadRequest{Name: "logo.svg", ContentBase64: base64.StdEncoding.EncodeToString([]byte(svg))})
	if err != nil || sv.MIME != MIMESVG || sv.RenditionSHA256 == "" {
		t.Fatalf("svg upload: %+v %v", sv, err)
	}
	r, err := s.Asset(ctx, sv.ID, false)
	if err != nil || r.PNG == nil || string(r.Data) != svg {
		t.Fatalf("resolve: %v", err)
	}
	if err := s.Delete(ctx, "alice", sv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Asset(ctx, sv.ID, false); !errors.Is(err, model.ErrAssetNotFound) {
		t.Fatalf("deleted asset resolvable for new references: %v", err)
	}
	if _, err := s.Asset(ctx, sv.ID, true); err != nil {
		t.Fatalf("deleted asset not resolvable for existing brand versions: %v", err)
	}
}

func TestUploadRejects(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	for name, req := range map[string]UploadRequest{
		"neither": {Name: "x"},
		"both":    {Name: "x", ContentBase64: "AA==", URL: "https://example.com/x.png"},
		"badb64":  {Name: "x", ContentBase64: "!!"},
		"script":  {Name: "x", ContentBase64: base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script/></svg>`))},
		"ssrf":    {Name: "x", URL: "https://169.254.169.254/latest"},
	} {
		var me *model.Error
		if _, err := s.Upload(ctx, "alice", req); !errors.As(err, &me) || me.Code != model.ErrInvalidInput {
			t.Errorf("%s: err = %v, want invalid_input", name, err)
		}
	}
}

func TestListNewestFirstSkipsDeleted(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	var ids []string
	for _, name := range []string{"alpha.png", "beta.png", "gamma.png"} {
		a, err := s.Upload(ctx, "alice", UploadRequest{Name: name, ContentBase64: base64.StdEncoding.EncodeToString(pngBytes(t, 2, 2))})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.ID)
	}
	_ = s.Delete(ctx, "alice", ids[1])
	page, next, err := s.List(ctx, "", 1, "")
	if err != nil || len(page) != 1 || page[0].ID != ids[2] || next == "" {
		t.Fatalf("page1 = %+v next=%q err=%v", page, next, err)
	}
	page, next, _ = s.List(ctx, "", 1, next)
	if len(page) != 1 || page[0].ID != ids[0] || next != "" {
		t.Fatalf("page2 = %+v next=%q (deleted asset must be skipped)", page, next)
	}
	if got, _, _ := s.List(ctx, "GAMMA", 10, ""); len(got) != 1 {
		t.Fatalf("query: %d", len(got))
	}
}
