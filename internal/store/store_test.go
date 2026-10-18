// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestOpenMigratesAndRefusesNewer(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	if err := db.R.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil || v != SchemaVersion() {
		t.Fatalf("user_version = %d (%v), want %d", v, err, SchemaVersion())
	}
	var mode string
	_ = db.R.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode)
	if mode != "wal" {
		t.Fatalf("journal_mode = %q", mode)
	}
	if _, err := db.W.ExecContext(ctx, "PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(ctx, dir); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("err = %v, want ErrSchemaTooNew", err)
	}
}

func TestReaderIsQueryOnly(t *testing.T) {
	db, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.R.Exec(`INSERT INTO meta (key, value) VALUES ('x', 'y')`); err == nil {
		t.Fatal("reader pool accepted a write")
	}
}

func TestFilesOwnerCheck(t *testing.T) {
	db, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.W.Exec(`INSERT INTO files (id, kind, name, mime, sha256, size, created_at) VALUES ('f', 'pdf', 'a.pdf', 'application/pdf', 'x', 1, 'now')`)
	if err == nil {
		t.Fatal("file row without an owner accepted")
	}
}

func TestBlobPutGetIdempotent(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	b, err := NewBlobs(t.TempDir(), clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	s1, err := b.Put([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Hour)
	s2, _ := b.Put([]byte("hello"))
	if s1 != s2 || s1 != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Fatalf("hash = %s / %s", s1, s2)
	}
	p, _ := b.Path(s1)
	fi, _ := os.Stat(p)
	if !fi.ModTime().Equal(clk.Now()) {
		t.Fatalf("mtime not bumped: %v vs %v", fi.ModTime(), clk.Now())
	}
	got, err := b.Get(s1)
	if err != nil || string(got) != "hello" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if _, err := b.Get("../../etc/passwd"); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestSecurityBlobGCGrace(t *testing.T) {
	ctx := context.Background()
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	b, _ := NewBlobs(t.TempDir(), clk.Now)
	grace := time.Hour
	refs := map[string]struct{}{}
	refFn := func(context.Context) (map[string]struct{}, error) { return refs, nil }

	kept, _ := b.Put([]byte("referenced"))
	refs[kept] = struct{}{}
	orphan, _ := b.Put([]byte("orphan"))

	clk.Advance(grace - time.Second)
	if n, _ := b.GC(ctx, grace, refFn); n != 0 {
		t.Fatalf("GC inside grace removed %d blobs", n)
	}
	clk.Advance(2 * time.Second)
	if n, _ := b.GC(ctx, grace, refFn); n != 1 {
		t.Fatalf("GC after grace removed %d, want 1", n)
	}
	if _, err := b.Get(orphan); !errors.Is(err, ErrBlobNotFound) {
		t.Fatal("orphan survived")
	}
	if _, err := b.Get(kept); err != nil {
		t.Fatal("referenced blob deleted")
	}
	// Re-putting an old unreferenced blob restarts its grace window.
	old, _ := b.Put([]byte("old"))
	clk.Advance(2 * grace)
	if _, err := b.Put([]byte("old")); err != nil {
		t.Fatal(err)
	}
	if n, _ := b.GC(ctx, grace, refFn); n != 0 {
		t.Fatalf("re-put blob collected (%d)", n)
	}
	_ = old
}

// TestSecurityBlobGCConcurrentWithRenders (AC16): renders re-put an aged,
// unreferenced blob and then commit a reference while GC runs continuously;
// no committed reference may ever point at a missing blob.
func TestSecurityBlobGCConcurrentWithRenders(t *testing.T) {
	ctx := context.Background()
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	b, _ := NewBlobs(t.TempDir(), clk.Now)
	grace := time.Hour
	var mu sync.Mutex
	refs := map[string]struct{}{}
	refFn := func(context.Context) (map[string]struct{}, error) {
		mu.Lock()
		defer mu.Unlock()
		cp := make(map[string]struct{}, len(refs))
		for k := range refs {
			cp[k] = struct{}{}
		}
		return cp, nil
	}
	const n = 200
	payloads := make([][]byte, n)
	for i := range payloads {
		payloads[i] = []byte(fmt.Sprintf("blob-%d", i))
		if _, err := b.Put(payloads[i]); err != nil {
			t.Fatal(err)
		}
	}
	clk.Advance(3 * grace) // every blob is now old and unreferenced

	stop := make(chan struct{})
	var gcWG sync.WaitGroup
	gcWG.Add(1)
	go func() {
		defer gcWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if _, err := b.GC(ctx, grace, refFn); err != nil {
					t.Error(err)
					return
				}
			}
		}
	}()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sum, err := b.Put(payloads[i]) // render writes its output
			if err != nil {
				t.Error(err)
				return
			}
			time.Sleep(time.Millisecond) // render work between Put and commit (< grace)
			mu.Lock()
			refs[sum] = struct{}{} // commit
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	close(stop)
	gcWG.Wait()
	for k := range refs {
		if _, err := b.Get(k); err != nil {
			t.Fatalf("committed blob %s missing: %v", k, err)
		}
	}
}

// TestOpenConcurrentFreshDir: concurrent Open calls on a new data dir
// serialise on the first migration instead of failing.
func TestOpenConcurrentFreshDir(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	const n = 4
	dbs := make([]*DB, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dbs[i], errs[i] = Open(ctx, dir)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("Open #%d: %v", i, err)
			continue
		}
		var v int
		if err := dbs[i].R.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil || v != SchemaVersion() {
			t.Errorf("Open #%d: user_version = %d (%v), want %d", i, v, err, SchemaVersion())
		}
		_ = dbs[i].Close()
	}
}

// TestOpenPathWithURIMetacharacters: the database path is escaped in the
// SQLite URI, so '?', '#', '%' and spaces in the data dir work.
func TestOpenPathWithURIMetacharacters(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	dir := filepath.Join(parent, "a?b#c %d")
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES ('k', 'v')`); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := db.R.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'k'`).Scan(&got); err != nil || got != "v" {
		t.Fatalf("reader got %q, %v", got, err)
	}
	var mode string
	if err := db.R.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q (%v): DSN parameters lost", mode, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "folio.db")); err != nil {
		t.Fatalf("database not at the requested path: %v", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("stray files next to the data dir: %v", entries)
	}
}
