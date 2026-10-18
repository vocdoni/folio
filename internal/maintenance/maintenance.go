// SPDX-License-Identifier: AGPL-3.0-only

// Package maintenance runs Folio's background housekeeping: the startup pass
// (stale job directories, blob GC) and the periodic tasks (trash purge,
// expired template previews, SQLite snapshots with retention).
package maintenance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vocdoni/folio/internal/activity"
	"github.com/vocdoni/folio/internal/documents"
	"github.com/vocdoni/folio/internal/render"
	"github.com/vocdoni/folio/internal/store"
)

// Config wires the housekeeping tasks.
type Config struct {
	Documents      *documents.Service
	DB             *store.DB
	BackupDir      string        // $FOLIO_DATA_DIR/backups
	JobRoot        string        // parent of render job dirs (os.TempDir() in production)
	JobMaxAge      time.Duration // job dirs older than this are removed at startup
	TrashRetention time.Duration // FOLIO_TRASH_RETENTION
	BackupInterval time.Duration // FOLIO_BACKUP_INTERVAL; 0 disables backups
	BackupKeep     int           // FOLIO_BACKUP_KEEP
	Interval       time.Duration // period of the retention pass; 0 means 10 minutes
	// ActivityRetention is FOLIO_ACTIVITY_RETENTION; 0 keeps activity rows forever.
	ActivityRetention time.Duration
	Now               func() time.Time
	Logger            *slog.Logger
}

// Startup runs before the server accepts requests: it removes job dirs left
// by a crash, applies retention once and collects unreferenced blobs.
func Startup(ctx context.Context, c Config) error {
	// Job dir ages come from file mtimes, so they are compared with the wall clock.
	n, err := render.SweepJobs(c.JobRoot, c.JobMaxAge, time.Now())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("maintenance: sweep job dirs: %w", err)
	}
	if _, _, err := RetentionPass(ctx, c); err != nil {
		return err
	}
	blobs, err := c.Documents.GC(ctx)
	if err != nil {
		return fmt.Errorf("maintenance: blob gc: %w", err)
	}
	c.Logger.Info("startup maintenance done", "job_dirs_removed", n, "blobs_removed", blobs)
	return nil
}

// RetentionPass purges documents trashed longer than TrashRetention and
// deletes expired files (template previews); both run blob GC afterwards.
// It also deletes activity rows older than ActivityRetention.
func RetentionPass(ctx context.Context, c Config) (purged int, expired int64, err error) {
	if c.ActivityRetention > 0 {
		n, err := activity.Prune(ctx, c.DB, c.Now().Add(-c.ActivityRetention))
		if err != nil {
			return 0, 0, fmt.Errorf("maintenance: prune activity: %w", err)
		}
		if n > 0 {
			c.Logger.Info("activity retention", "rows_deleted", n)
		}
	}
	if purged, err = c.Documents.PurgeExpiredTrash(ctx, c.TrashRetention); err != nil {
		return 0, 0, fmt.Errorf("maintenance: purge trash: %w", err)
	}
	if expired, err = c.Documents.DeleteExpiredFiles(ctx); err != nil {
		return purged, 0, fmt.Errorf("maintenance: expire files: %w", err)
	}
	if purged > 0 || expired > 0 {
		c.Logger.Info("retention pass", "documents_purged", purged, "files_expired", expired)
	}
	return purged, expired, nil
}

const backupPrefix, backupSuffix = "folio-", ".db"

// backupName is sortable: lexical order is chronological order.
func backupName(t time.Time) string {
	return backupPrefix + t.UTC().Format("20060102T150405Z") + backupSuffix
}

// listBackups returns snapshot file names, oldest first.
func listBackups(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("maintenance: list backups: %w", err)
	}
	var out []string
	for _, e := range entries {
		if n := e.Name(); e.Type().IsRegular() && strings.HasPrefix(n, backupPrefix) && strings.HasSuffix(n, backupSuffix) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Backup writes a snapshot of the database into dir (via a temp file renamed
// into place, so a crash never leaves a partial snapshot under a final name)
// and deletes all but the newest keep snapshots. It returns the new path.
func Backup(ctx context.Context, db *store.DB, dir string, keep int, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("maintenance: backup dir: %w", err)
	}
	// Leftovers of interrupted snapshots never get a final name: drop them all.
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".tmp-") {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	final := filepath.Join(dir, backupName(now))
	tmp := filepath.Join(dir, ".tmp-"+filepath.Base(final))
	_ = os.Remove(tmp) // leftover of an interrupted snapshot
	if err := db.VacuumInto(ctx, tmp); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("maintenance: publish backup: %w", err)
	}
	names, err := listBackups(dir)
	if err != nil {
		return final, err
	}
	for i := 0; i < len(names)-max(keep, 1); i++ {
		if err := os.Remove(filepath.Join(dir, names[i])); err != nil && !errors.Is(err, os.ErrNotExist) {
			return final, fmt.Errorf("maintenance: prune backup: %w", err)
		}
	}
	return final, nil
}

// NextBackup returns when the next snapshot is due: now when there is none,
// otherwise the newest snapshot's time plus interval (so frequent restarts
// neither skip nor multiply backups). ok is false when backups are disabled.
func NextBackup(dir string, interval time.Duration, now time.Time) (due time.Time, ok bool, err error) {
	if interval <= 0 {
		return time.Time{}, false, nil
	}
	names, err := listBackups(dir)
	if err != nil {
		return time.Time{}, false, err
	}
	if len(names) == 0 {
		return now, true, nil
	}
	ts := strings.TrimSuffix(strings.TrimPrefix(names[len(names)-1], backupPrefix), backupSuffix)
	last, err := time.Parse("20060102T150405Z", ts)
	if err != nil {
		return now, true, nil
	}
	return last.Add(interval), true, nil
}

// Run performs the periodic tasks until ctx is cancelled. Failures are
// logged and retried at the next tick; they never stop the server.
func Run(ctx context.Context, c Config) {
	interval := c.Interval
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, _, err := RetentionPass(ctx, c); err != nil && ctx.Err() == nil {
			c.Logger.Error("retention pass failed", "err", err)
		}
		if due, ok, err := NextBackup(c.BackupDir, c.BackupInterval, c.Now()); err != nil {
			c.Logger.Error("backup schedule failed", "err", err)
		} else if ok && !c.Now().Before(due) {
			if p, err := Backup(ctx, c.DB, c.BackupDir, c.BackupKeep, c.Now()); err != nil {
				if ctx.Err() == nil {
					c.Logger.Error("backup failed", "err", err)
				}
			} else {
				c.Logger.Info("backup written", "file", filepath.Base(p))
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
