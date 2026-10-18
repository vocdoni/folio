// SPDX-License-Identifier: AGPL-3.0-only

package seed

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/vocdoni/folio/internal/assets"
	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/store"
)

// MetaKey records in the meta table that seeding has run.
const MetaKey = "seeded_at"

// ApplyOnce seeds the starter content once per database and records it under
// MetaKey. It returns true when it created content. A database holding
// content created by anyone other than Actor is marked without seeding. A
// previous seeding interrupted half-way is resumed: items that already exist
// are kept and the missing ones are created.
func ApplyOnce(ctx context.Context, db *store.DB, cat *catalog.Service, as *assets.Service, now time.Time) (bool, error) {
	var marker string
	err := db.R.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, MetaKey).Scan(&marker)
	switch {
	case err == nil:
		return false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return false, fmt.Errorf("seed: read marker: %w", err)
	}
	var foreign int
	if err := db.R.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM brands WHERE created_by <> ?) + (SELECT count(*) FROM templates WHERE created_by <> ?) +
		(SELECT count(*) FROM assets WHERE created_by <> ?) + (SELECT count(*) FROM documents)`, Actor, Actor, Actor).Scan(&foreign); err != nil {
		return false, fmt.Errorf("seed: inspect database: %w", err)
	}
	created := false
	if foreign == 0 {
		if created, err = resume(ctx, cat, as); err != nil {
			return false, err
		}
	}
	err = db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)`, MetaKey, store.Now(now))
		return err
	})
	if err != nil {
		return false, fmt.Errorf("seed: write marker: %w", err)
	}
	return created, nil
}

func exists(err error) (bool, error) {
	var me *model.Error
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &me) && me.Code == model.ErrNotFound:
		return false, nil
	}
	return false, err
}

// resume creates whatever starter content is missing.
func resume(ctx context.Context, cat *catalog.Service, as *assets.Service) (bool, error) {
	created := false
	_, err := cat.GetBrand(ctx, "example", 0)
	ok, err := exists(err)
	if err != nil {
		return false, fmt.Errorf("seed: brand: %w", err)
	}
	if !ok {
		logo, err := as.Upload(ctx, Actor, assets.UploadRequest{Name: "example-logo.svg", Description: "Example Co text logo",
			ContentBase64: base64.StdEncoding.EncodeToString(Logo())})
		if err != nil {
			return false, fmt.Errorf("seed: logo: %w", err)
		}
		b, err := Brand()
		if err != nil {
			return false, err
		}
		b.Logo.AssetID = logo.ID
		if _, err := cat.CreateBrand(ctx, Actor, b, "starter brand"); err != nil {
			return false, fmt.Errorf("seed: brand: %w", err)
		}
		created = true
	}
	ts, err := Templates()
	if err != nil {
		return false, err
	}
	for _, t := range ts {
		_, err := cat.GetTemplate(ctx, t.ID, 0)
		ok, err := exists(err)
		if err != nil {
			return false, fmt.Errorf("seed: template %s: %w", t.ID, err)
		}
		if ok {
			continue
		}
		if _, err := cat.CreateTemplate(ctx, Actor, t, "starter template"); err != nil {
			return false, fmt.Errorf("seed: template %s: %w", t.ID, err)
		}
		created = true
	}
	return created, nil
}
