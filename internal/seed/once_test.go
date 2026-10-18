// SPDX-License-Identifier: AGPL-3.0-only

package seed

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vocdoni/folio/internal/assets"
	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/render"
	"github.com/vocdoni/folio/internal/store"
)

type okRenderer struct{ fail bool }

func (r okRenderer) Render(_ context.Context, req render.Request) (*render.Result, error) {
	if r.fail {
		return nil, &model.Error{Code: model.ErrRenderFailed, Message: "renderer down"}
	}
	canon, sum, err := render.CanonicalInput(model.Ref{ID: req.Template.ID, Version: req.TemplateVersion}, model.Ref{ID: req.Brand.ID, Version: req.BrandVersion}, req.Input)
	return &render.Result{Canonical: canon, InputSHA256: sum, PDF: []byte("%PDF"), DOCX: []byte("PK")}, err
}

type pngRaster struct{}

func (pngRaster) SVGToPNG(context.Context, []byte) ([]byte, error) {
	var buf bytes.Buffer
	err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 10, 3)))
	return buf.Bytes(), err
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeEnv is a database with a catalog whose renderer never runs pandoc or typst.
func fakeEnv(t *testing.T, r catalog.Renderer) (*store.DB, *catalog.Service, *assets.Service) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	blobs, _ := store.NewBlobs(filepath.Join(dir, "blobs"), time.Now)
	as := &assets.Service{DB: db, Blobs: blobs, Fetcher: assets.NewFetcher(1 << 20), Raster: pngRaster{}, MaxBytes: 1 << 20, Now: time.Now}
	cat := &catalog.Service{DB: db, Blobs: blobs, Renderer: r, Assets: as, PackagesDir: t.TempDir(), Now: time.Now, PreviewTTL: time.Hour, PreviewPagesMax: 3}
	return db, cat, as
}

func meta(t *testing.T, db *store.DB, key string) string {
	t.Helper()
	var v string
	if err := db.R.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v); err != nil {
		return ""
	}
	return v
}

func TestApplyFirstRun(t *testing.T) {
	ctx := context.Background()
	db, cat, as := fakeEnv(t, okRenderer{})

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
	res, err := Apply(ctx, db, cat, as, time.Now(), quiet())
	if err != nil || !res.Seeded || len(res.Created) != len(TemplateIDs) || len(res.Upgraded) != 0 {
		t.Fatalf("first run: %+v %v", res, err)
	}
	for _, id := range TemplateIDs {
		if _, err := cat.GetTemplate(ctx, id, 0); err != nil {
			t.Errorf("template %s: %v", id, err)
		}
	}
	if meta(t, db, MetaKey) == "" || meta(t, db, VersionKey) != "2" {
		t.Fatalf("meta: %q %q", meta(t, db, MetaKey), meta(t, db, VersionKey))
	}
	if res, err := Apply(ctx, db, cat, as, time.Now(), quiet()); err != nil || res.Seeded || len(res.Created)+len(res.Upgraded) != 0 {
		t.Fatalf("second run must be a no-op: %+v %v", res, err)
	}

	// A database with someone else's content is marked, never seeded.
	db2, cat2, as2 := fakeEnv(t, okRenderer{})
	b.ID, b.Logo = "mine", nil
	if _, err := cat2.CreateBrand(ctx, "alice", b, ""); err != nil {
		t.Fatal(err)
	}
	if res, err := Apply(ctx, db2, cat2, as2, time.Now(), quiet()); err != nil || res.Seeded {
		t.Fatalf("foreign content: %+v %v", res, err)
	}
	if _, err := cat2.GetBrand(ctx, "example", 0); err == nil {
		t.Fatal("seeded into a database with user content")
	}
	if meta(t, db2, MetaKey) == "" || meta(t, db2, VersionKey) != "2" {
		t.Fatal("foreign content: markers not written")
	}
}

// A failed upgrade is an error (startup fails) and leaves seed_version as it
// was, so the next start retries.
func TestApplyUpgradeFailureFailsAndRetries(t *testing.T) {
	ctx := context.Background()
	db, cat, as := fakeEnv(t, okRenderer{})
	seedV1(t, db, cat, as, "")
	cat.Renderer = okRenderer{fail: true}
	_, err := Apply(ctx, db, cat, as, time.Now(), quiet())
	var me *model.Error
	if !errors.As(err, &me) || me.Code != model.ErrRenderFailed {
		t.Fatalf("a failed upgrade must fail: %v", err)
	}
	if v := meta(t, db, VersionKey); v != "" {
		t.Fatalf("seed_version = %q after a failed upgrade", v)
	}
	cat.Renderer = okRenderer{}
	res, err := Apply(ctx, db, cat, as, time.Now(), quiet())
	if err != nil || len(res.Upgraded) != 3 || meta(t, db, VersionKey) != "2" {
		t.Fatalf("retry: %+v %v", res, err)
	}
}

// seedV1 recreates a database seeded by the first starter version (seed v1,
// no seed_version): the brand and logo, requirements and technical-report
// (unchanged since v1) through the catalog, and proposal, quote and sow from
// testdata/v1 as version 1 by Actor. Those v1 specs no longer validate, so
// they are inserted directly, as an older Folio stored them. With alice set,
// quote also gets a version 2 created by that user.
func seedV1(t *testing.T, db *store.DB, cat *catalog.Service, as *assets.Service, alice string) {
	t.Helper()
	ctx := context.Background()
	p := &pass{cat: cat, as: as, log: quiet()}
	if err := p.brand(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"requirements", "technical-report"} {
		if err := p.template(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(tx *sql.Tx, id string, version int, spec model.Template, by string) error {
		spec.SpecVersion = model.SpecVersion
		raw, err := model.Canonical(spec)
		if err != nil {
			return err
		}
		ts := store.Now(time.Now())
		if _, err := tx.ExecContext(ctx, `INSERT INTO templates (id, version, spec, note, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			id, version, string(raw), "starter template", by, ts); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO template_heads (id, latest_version) VALUES (?, ?)
			ON CONFLICT (id) DO UPDATE SET latest_version = excluded.latest_version`, id, version)
		return err
	}
	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, id := range []string{"proposal", "quote", "sow"} {
			raw, err := os.ReadFile(filepath.Join("testdata", "v1", id+".json"))
			if err != nil {
				return err
			}
			var v1 model.Template
			if err := json.Unmarshal(raw, &v1); err != nil {
				return err
			}
			if err := insert(tx, id, 1, v1, Actor); err != nil {
				return err
			}
			if id == "quote" && alice != "" {
				v1.Description = "Alice's quote"
				if err := insert(tx, id, 2, v1, alice); err != nil {
					return err
				}
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)`, MetaKey, store.Now(time.Now()))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A database marked without seeding (it held someone else's content) is not
// seeded by an upgrade either: only seed_version is recorded.
func TestApplyUpgradeDoesNotSeedAMarkedDatabase(t *testing.T) {
	ctx := context.Background()
	db, cat, as := fakeEnv(t, okRenderer{})
	b, _ := Brand()
	b.ID, b.Logo = "mine", nil
	if _, err := cat.CreateBrand(ctx, "alice", b, ""); err != nil {
		t.Fatal(err)
	}
	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)`, MetaKey, store.Now(time.Now()))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Apply(ctx, db, cat, as, time.Now(), quiet())
	if err != nil || res.Seeded || len(res.Created)+len(res.Upgraded)+len(res.Skipped) != 0 {
		t.Fatalf("marked database: %+v %v", res, err)
	}
	if _, err := cat.GetBrand(ctx, "example", 0); err == nil {
		t.Fatal("an upgrade seeded a database that was never seeded")
	}
	if v := meta(t, db, VersionKey); v != "2" {
		t.Fatalf("seed_version = %q", v)
	}
}
