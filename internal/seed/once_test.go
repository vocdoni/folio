// SPDX-License-Identifier: AGPL-3.0-only

package seed

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"path/filepath"
	"testing"
	"time"

	"github.com/vocdoni/folio/internal/assets"
	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/render"
	"github.com/vocdoni/folio/internal/store"
)

type okRenderer struct{}

func (okRenderer) Render(_ context.Context, req render.Request) (*render.Result, error) {
	canon, sum, err := render.CanonicalInput(model.Ref{ID: req.Template.ID, Version: req.TemplateVersion}, model.Ref{ID: req.Brand.ID, Version: req.BrandVersion}, req.Input)
	return &render.Result{Canonical: canon, InputSHA256: sum, PDF: []byte("%PDF"), DOCX: []byte("PK")}, err
}

type pngRaster struct{}

func (pngRaster) SVGToPNG(context.Context, []byte) ([]byte, error) {
	var buf bytes.Buffer
	err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 10, 3)))
	return buf.Bytes(), err
}

func TestApplyOnce(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	blobs, _ := store.NewBlobs(filepath.Join(dir, "blobs"), time.Now)
	as := &assets.Service{DB: db, Blobs: blobs, Fetcher: assets.NewFetcher(1 << 20), Raster: pngRaster{}, MaxBytes: 1 << 20, Now: time.Now}
	cat := &catalog.Service{DB: db, Blobs: blobs, Renderer: okRenderer{}, Assets: as, PackagesDir: t.TempDir(), Now: time.Now, PreviewTTL: time.Hour, PreviewPagesMax: 3}

	// An interrupted earlier run left only the brand: the templates are resumed.
	logo, err := as.Upload(ctx, Actor, assets.UploadRequest{Name: "l.svg", ContentBase64: "PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIxMCIgaGVpZ2h0PSIzIi8+"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Brand()
	b.Logo.AssetID = logo.ID
	if _, err := cat.CreateBrand(ctx, Actor, b, "starter brand"); err != nil {
		t.Fatal(err)
	}
	created, err := ApplyOnce(ctx, db, cat, as, time.Now())
	if err != nil || !created {
		t.Fatalf("first run: %v %v", created, err)
	}
	for _, id := range TemplateIDs {
		if _, err := cat.GetTemplate(ctx, id, 0); err != nil {
			t.Errorf("template %s: %v", id, err)
		}
	}
	if created, err := ApplyOnce(ctx, db, cat, as, time.Now()); err != nil || created {
		t.Fatalf("second run must be a no-op: %v %v", created, err)
	}

	// A database with someone else's content is marked, never seeded.
	dir2 := t.TempDir()
	db2, _ := store.Open(ctx, dir2)
	defer db2.Close()
	blobs2, _ := store.NewBlobs(filepath.Join(dir2, "blobs"), time.Now)
	as2 := &assets.Service{DB: db2, Blobs: blobs2, Fetcher: assets.NewFetcher(1 << 20), Raster: pngRaster{}, MaxBytes: 1 << 20, Now: time.Now}
	cat2 := &catalog.Service{DB: db2, Blobs: blobs2, Renderer: okRenderer{}, Assets: as2, PackagesDir: t.TempDir(), Now: time.Now, PreviewTTL: time.Hour, PreviewPagesMax: 3}
	b.ID, b.Logo = "mine", nil
	if _, err := cat2.CreateBrand(ctx, "alice", b, ""); err != nil {
		t.Fatal(err)
	}
	if created, err := ApplyOnce(ctx, db2, cat2, as2, time.Now()); err != nil || created {
		t.Fatalf("foreign content: %v %v", created, err)
	}
	if _, err := cat2.GetBrand(ctx, "example", 0); err == nil {
		t.Fatal("seeded into a database with user content")
	}
	var marker string
	if err := db2.R.QueryRow(`SELECT value FROM meta WHERE key = ?`, MetaKey).Scan(&marker); err != nil || marker == "" {
		t.Fatalf("marker: %q %v", marker, err)
	}
}
