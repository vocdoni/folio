// SPDX-License-Identifier: AGPL-3.0-only

// Package store owns the SQLite database (schema, migrations, transactions)
// and the content-addressed blob store with its grace-period garbage collector.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver (pure Go)
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrSchemaTooNew means the database was written by a newer Folio.
var ErrSchemaTooNew = errors.New("store: database schema is newer than this binary")

// DB is the SQLite database: one writer connection (all writes, IMMEDIATE
// transactions) and a pool of query-only reader connections.
type DB struct {
	W    *sql.DB
	R    *sql.DB
	Path string
}

func dsn(path string, readOnly bool) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	if readOnly {
		q.Add("_pragma", "query_only(1)")
	} else {
		q.Set("_txlock", "immediate")
	}
	return "file:" + path + "?" + q.Encode()
}

// Open creates dataDir if needed, opens dataDir/folio.db and applies pending
// migrations inside one transaction.
func Open(ctx context.Context, dataDir string) (*DB, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("store: create data dir: %w", err)
	}
	path := filepath.Join(dataDir, "folio.db")
	w, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return nil, fmt.Errorf("store: open writer: %w", err)
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)
	if err := migrate(ctx, w); err != nil {
		_ = w.Close()
		return nil, err
	}
	r, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("store: open readers: %w", err)
	}
	r.SetMaxOpenConns(8)
	return &DB{W: w, R: r, Path: path}, nil
}

// Close closes both pools.
func (db *DB) Close() error {
	return errors.Join(db.R.Close(), db.W.Close())
}

// WriteTx runs fn in an IMMEDIATE transaction on the writer connection and
// commits when fn returns nil.
func (db *DB) WriteTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := db.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("store: read migrations: %w", err)
	}
	var ms []migration
	for _, e := range entries {
		num, _, ok := strings.Cut(e.Name(), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil {
			return nil, fmt.Errorf("store: bad migration name %q", e.Name())
		}
		b, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("store: read %s: %w", e.Name(), err)
		}
		ms = append(ms, migration{version: v, name: e.Name(), sql: string(b)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	for i, m := range ms {
		if m.version != i+1 {
			return nil, fmt.Errorf("store: migrations must be numbered 1..n, found %s", m.name)
		}
	}
	return ms, nil
}

// SchemaVersion is the number of embedded migrations.
func SchemaVersion() int {
	ms, err := loadMigrations()
	if err != nil {
		return 0
	}
	return len(ms)
}

func migrate(ctx context.Context, w *sql.DB) error {
	ms, err := loadMigrations()
	if err != nil {
		return err
	}
	var cur int
	if err := w.QueryRowContext(ctx, "PRAGMA user_version").Scan(&cur); err != nil {
		return fmt.Errorf("store: read user_version: %w", err)
	}
	if cur > len(ms) {
		return fmt.Errorf("%w: database at v%d, binary knows v%d", ErrSchemaTooNew, cur, len(ms))
	}
	if cur == len(ms) {
		return nil
	}
	tx, err := w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, m := range ms[cur:] {
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			return fmt.Errorf("store: apply %s: %w", m.name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", len(ms))); err != nil {
		return fmt.Errorf("store: set user_version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit migration: %w", err)
	}
	return nil
}

// Now returns the current time as stored: RFC 3339 UTC with seconds precision.
func Now(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// ReferencedBlobs returns every blob hash referenced by a database row.
// Asset blobs stay referenced while the asset row exists (deleted assets
// remain referenced, because brand/template versions and revisions may use them).
func (db *DB) ReferencedBlobs(ctx context.Context) (map[string]struct{}, error) {
	rows, err := db.W.QueryContext(ctx, `
		SELECT sha256 FROM assets
		UNION SELECT rendition_sha256 FROM assets WHERE rendition_sha256 IS NOT NULL
		UNION SELECT sha256 FROM files
		UNION SELECT input_blob FROM revisions`)
	if err != nil {
		return nil, fmt.Errorf("store: referenced blobs: %w", err)
	}
	defer rows.Close()
	refs := map[string]struct{}{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("store: scan blob ref: %w", err)
		}
		refs[s] = struct{}{}
	}
	return refs, rows.Err()
}

// Audit appends an audit entry inside tx.
func Audit(ctx context.Context, tx *sql.Tx, ts time.Time, actor, action, targetKind, targetID, outcome string, detail []byte) error {
	if len(detail) == 0 {
		detail = []byte("{}")
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO audit (ts, actor, action, target_kind, target_id, outcome, detail) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		Now(ts), actor, action, targetKind, targetID, outcome, string(detail))
	if err != nil {
		return fmt.Errorf("store: audit: %w", err)
	}
	return nil
}
