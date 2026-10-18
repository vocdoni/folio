// SPDX-License-Identifier: AGPL-3.0-only

package seed

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/vocdoni/folio/internal/assets"
	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/store"
)

// Meta keys: MetaKey records that seeding has run, VersionKey the starter
// content version the database holds (missing means 1).
const (
	MetaKey    = "seeded_at"
	VersionKey = "seed_version"
)

// Result reports what Apply did. Items are "brand:<id>" or "template:<id>".
type Result struct {
	Seeded   bool     // an empty database received the starter content
	Created  []string // starter items that were missing and were created
	Upgraded []string // starter items that got a new version
	Skipped  []string // starter items left alone: a user created their latest version
}

// Apply seeds an empty database once and keeps the starter content current.
//
// Without the MetaKey marker it seeds as before: a database holding content
// created by anyone other than Actor is marked without seeding; otherwise
// every missing starter item is created (resuming an interrupted run). With
// the marker and a stored VersionKey below Version it upgrades: missing
// items are left missing (a database marked without seeding is never seeded
// later); an item whose latest version Actor created and whose
// spec differs from the embedded one gets a new version through the normal
// catalog update (validation and sample render); an item whose latest
// version a user created is left alone and logged as a warning. Either way
// it records MetaKey (first run) and VersionKey = Version. Any error leaves
// VersionKey unchanged, so the caller fails startup and the next start
// retries.
func Apply(ctx context.Context, db *store.DB, cat *catalog.Service, as *assets.Service, now time.Time, log *slog.Logger) (Result, error) {
	var res Result
	var marker string
	err := db.R.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, MetaKey).Scan(&marker)
	firstRun := errors.Is(err, sql.ErrNoRows)
	if err != nil && !firstRun {
		return res, fmt.Errorf("seed: read marker: %w", err)
	}
	if firstRun {
		var foreign int
		if err := db.R.QueryRowContext(ctx, `SELECT
			(SELECT count(*) FROM brands WHERE created_by <> ?) + (SELECT count(*) FROM templates WHERE created_by <> ?) +
			(SELECT count(*) FROM assets WHERE created_by <> ?) + (SELECT count(*) FROM documents)`, Actor, Actor, Actor).Scan(&foreign); err != nil {
			return res, fmt.Errorf("seed: inspect database: %w", err)
		}
		if foreign == 0 {
			p := &pass{cat: cat, as: as, log: log}
			if err := p.run(ctx); err != nil {
				return res, err
			}
			res = p.res
			res.Seeded = len(res.Created) > 0
		}
	} else {
		stored, err := storedVersion(ctx, db)
		if err != nil {
			return res, err
		}
		if stored >= Version {
			return res, nil
		}
		p := &pass{cat: cat, as: as, log: log, upgrade: true}
		if err := p.run(ctx); err != nil {
			return res, err
		}
		res = p.res
		log.Info("starter upgrade done", "from_seed_version", stored, "seed_version", Version,
			"created", len(res.Created), "upgraded", len(res.Upgraded), "skipped", len(res.Skipped))
	}
	err = db.WriteTx(ctx, func(tx *sql.Tx) error {
		if firstRun {
			if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)`, MetaKey, store.Now(now)); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
			VersionKey, strconv.Itoa(Version))
		return err
	})
	if err != nil {
		return res, fmt.Errorf("seed: write marker: %w", err)
	}
	return res, nil
}

// storedVersion reads VersionKey; a database seeded before it existed is 1.
func storedVersion(ctx context.Context, db *store.DB) (int, error) {
	var v string
	err := db.R.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, VersionKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 1, nil
	}
	if err != nil {
		return 0, fmt.Errorf("seed: read %s: %w", VersionKey, err)
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("seed: %s %q is not a number", VersionKey, v)
	}
	return n, nil
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

func isArchived(err error) bool {
	var me *model.Error
	return errors.As(err, &me) && me.Code == model.ErrArchived
}

// latestBy returns who created the latest version of it.
func latestBy(it *catalog.Item) string {
	for _, v := range it.Versions {
		if v.Version == it.LatestVersion {
			return v.CreatedBy
		}
	}
	return ""
}

// sameSpec compares two specs in canonical form (spec_version stamped).
func sameSpec(a, b any) (bool, error) {
	ca, err := model.Canonical(a)
	if err != nil {
		return false, err
	}
	cb, err := model.Canonical(b)
	if err != nil {
		return false, err
	}
	return string(ca) == string(cb), nil
}

// pass creates missing starter items and upgrades the ones Actor owns.
type pass struct {
	cat     *catalog.Service
	as      *assets.Service
	log     *slog.Logger
	upgrade bool // an upgrade never creates: a database marked without seeding stays without starters
	res     Result
}

func (p *pass) note() string { return fmt.Sprintf("starter upgrade (seed v%d)", Version) }

func (p *pass) uploadLogo(ctx context.Context) (string, error) {
	logo, err := p.as.Upload(ctx, Actor, assets.UploadRequest{Name: "example-logo.svg", Description: "Example Co text logo",
		ContentBase64: base64.StdEncoding.EncodeToString(Logo())})
	if err != nil {
		return "", fmt.Errorf("seed: logo: %w", err)
	}
	return logo.ID, nil
}

func (p *pass) skip(kind, id string, it *catalog.Item, reason string) {
	p.res.Skipped = append(p.res.Skipped, kind+":"+id)
	p.log.Warn("starter upgrade skipped: "+reason+"; compare it with the embedded starter by hand",
		"kind", kind, "id", id, "latest_version", it.LatestVersion, "created_by", latestBy(it), "seed_version", Version)
}

func (p *pass) upgraded(kind, id string, version int) {
	p.res.Upgraded = append(p.res.Upgraded, kind+":"+id)
	p.log.Info("starter upgrade", "kind", kind, "id", id, "version", version, "seed_version", Version)
}

func (p *pass) run(ctx context.Context) error {
	if err := p.brand(ctx); err != nil {
		return err
	}
	for _, id := range TemplateIDs {
		if err := p.template(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (p *pass) brand(ctx context.Context) error {
	want, err := Brand()
	if err != nil {
		return err
	}
	it, err := p.cat.GetBrand(ctx, want.ID, 0)
	ok, err := exists(err)
	if err != nil {
		return fmt.Errorf("seed: brand: %w", err)
	}
	if !ok {
		if p.upgrade {
			p.log.Debug("starter upgrade skipped: the starter does not exist", "kind", "brand", "id", want.ID, "seed_version", Version)
			return nil
		}
		if want.Logo.AssetID, err = p.uploadLogo(ctx); err != nil {
			return err
		}
		if _, err := p.cat.CreateBrand(ctx, Actor, want, "starter brand"); err != nil {
			return fmt.Errorf("seed: brand: %w", err)
		}
		p.res.Created = append(p.res.Created, "brand:"+want.ID)
		return nil
	}
	var cur model.Brand
	if err := json.Unmarshal(it.Spec, &cur); err != nil {
		return fmt.Errorf("seed: brand %s: %w", want.ID, err)
	}
	// The logo asset is reused: compare and update with the current asset id.
	want.Logo.AssetID = ""
	if cur.Logo != nil {
		want.Logo.AssetID = cur.Logo.AssetID
	}
	want.SpecVersion = model.SpecVersion
	if same, err := sameSpec(cur, want); err != nil || same {
		return err
	}
	if latestBy(it) != Actor {
		p.skip("brand", want.ID, it, "a user created its latest version")
		return nil
	}
	if want.Logo.AssetID == "" {
		if want.Logo.AssetID, err = p.uploadLogo(ctx); err != nil {
			return err
		}
	}
	v, err := p.cat.UpdateBrand(ctx, Actor, want.ID, it.LatestVersion, want, p.note())
	if err != nil {
		return fmt.Errorf("seed: upgrade brand %s: %w", want.ID, err)
	}
	p.upgraded("brand", want.ID, v)
	return nil
}

func (p *pass) template(ctx context.Context, id string) error {
	want, err := Template(id)
	if err != nil {
		return err
	}
	it, err := p.cat.GetTemplate(ctx, id, 0)
	ok, err := exists(err)
	if err != nil {
		return fmt.Errorf("seed: template %s: %w", id, err)
	}
	if !ok {
		if p.upgrade {
			p.log.Debug("starter upgrade skipped: the starter does not exist", "kind", "template", "id", id, "seed_version", Version)
			return nil
		}
		if _, err := p.cat.CreateTemplate(ctx, Actor, want, "starter template"); err != nil {
			return fmt.Errorf("seed: template %s: %w", id, err)
		}
		p.res.Created = append(p.res.Created, "template:"+id)
		return nil
	}
	var cur model.Template
	if err := json.Unmarshal(it.Spec, &cur); err != nil {
		return fmt.Errorf("seed: template %s: %w", id, err)
	}
	want.SpecVersion = model.SpecVersion
	if same, err := sameSpec(cur, want); err != nil || same {
		return err
	}
	if latestBy(it) != Actor {
		p.skip("template", id, it, "a user created its latest version")
		return nil
	}
	r, err := p.cat.UpdateTemplate(ctx, Actor, id, it.LatestVersion, want, p.note())
	if isArchived(err) {
		p.skip("template", id, it, "its brand is archived (or the update was refused as archived)")
		return nil
	}
	if err != nil {
		return fmt.Errorf("seed: upgrade template %s: %w", id, err)
	}
	p.upgraded("template", id, r.Version)
	return nil
}
