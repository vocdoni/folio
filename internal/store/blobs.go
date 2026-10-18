// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var shaRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ErrBlobNotFound is returned by Get for a missing blob.
var ErrBlobNotFound = errors.New("store: blob not found")

// Blobs is a content-addressed store at <root>/sha256/<aa>/<rest>.
//
// GC safety: Put holds mu for reading while it writes a blob or refreshes its
// mtime; GC holds mu for writing for its whole pass (reference query, scan,
// delete). A blob is deleted only if it is unreferenced and its mtime is older
// than the grace period. Because renders commit their references less than
// one grace period after Put, and Put of an existing blob refreshes its mtime,
// GC can never delete a blob that is about to be referenced.
type Blobs struct {
	root string
	now  func() time.Time
	mu   sync.RWMutex
}

// NewBlobs creates the store under root. now is the clock (time.Now in production).
func NewBlobs(root string, now func() time.Time) (*Blobs, error) {
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(filepath.Join(root, "sha256"), 0o700); err != nil {
		return nil, fmt.Errorf("store: create blob dir: %w", err)
	}
	return &Blobs{root: root, now: now}, nil
}

// Path returns the on-disk path of a blob hash.
func (b *Blobs) Path(sum string) (string, error) {
	if !shaRE.MatchString(sum) {
		return "", fmt.Errorf("store: invalid blob hash %q", sum)
	}
	return filepath.Join(b.root, "sha256", sum[:2], sum[2:]), nil
}

// Put stores data and returns its lowercase hex SHA-256. Writing an existing
// blob does not rewrite it but bumps its mtime to now.
func (b *Blobs) Put(data []byte) (string, error) {
	h := sha256.Sum256(data)
	sum := hex.EncodeToString(h[:])
	p, _ := b.Path(sum)
	b.mu.RLock()
	defer b.mu.RUnlock()
	now := b.now()
	if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
		if err := os.Chtimes(p, now, now); err != nil {
			return "", fmt.Errorf("store: refresh blob: %w", err)
		}
		return sum, nil
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("store: blob dir: %w", err)
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", fmt.Errorf("store: blob temp: %w", err)
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("store: write blob: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("store: sync blob: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("store: close blob: %w", err)
	}
	if err := os.Chtimes(tmp, now, now); err != nil {
		return "", fmt.Errorf("store: blob mtime: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return "", fmt.Errorf("store: publish blob: %w", err)
	}
	ok = true
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return sum, nil
}

// Get reads a blob.
func (b *Blobs) Get(sum string) ([]byte, error) {
	p, err := b.Path(sum)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrBlobNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: read blob: %w", err)
	}
	return data, nil
}

// GC deletes blobs (and stale temp files) that are not in the set returned by
// referenced and whose mtime is older than now−grace. referenced is called
// while the GC lock is held. It returns the number of files removed.
func (b *Blobs) GC(ctx context.Context, grace time.Duration, referenced func(context.Context) (map[string]struct{}, error)) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	refs, err := referenced(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: gc references: %w", err)
	}
	cutoff := b.now().Add(-grace)
	removed := 0
	base := filepath.Join(b.root, "sha256")
	err = filepath.WalkDir(base, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		sum := strings.ReplaceAll(rel, string(filepath.Separator), "")
		isTemp := strings.HasPrefix(d.Name(), ".tmp-")
		if !isTemp {
			if !shaRE.MatchString(sum) {
				return nil // not ours; leave it alone
			}
			if _, ok := refs[sum]; ok {
				return nil
			}
		}
		info, err := d.Info()
		if err != nil {
			return nil // vanished
		}
		if !info.ModTime().Before(cutoff) {
			return nil
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("store: remove blob: %w", err)
		}
		removed++
		return nil
	})
	return removed, err
}
