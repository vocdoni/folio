// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// jobPrefix names per-job directories under the job root.
const jobPrefix = "folio-job-"

// Job is a private per-render working directory. Folio writes every file in
// it itself with O_EXCL|O_NOFOLLOW; nothing is ever copied or linked in, so
// the directory never contains a symlink.
type Job struct {
	Dir string
}

// NewJob creates <root>/folio-job-<random> with mode 0700. root is resolved
// to its canonical path first (so diagnostics can strip it) and must be a
// real directory.
func NewJob(root string) (*Job, error) {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("render: job root: %w", err)
	}
	fi, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("render: job root: %w", err)
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("render: job root %s is not a real directory", root)
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, fmt.Errorf("render: job id: %w", err)
	}
	dir := filepath.Join(root, jobPrefix+hex.EncodeToString(b[:]))
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, fmt.Errorf("render: create job dir: %w", err)
	}
	return &Job{Dir: dir}, nil
}

// Path returns the absolute path of a validated relative name.
func (j *Job) Path(rel string) (string, error) {
	if !filepath.IsLocal(rel) || strings.ContainsRune(rel, 0) {
		return "", fmt.Errorf("render: invalid job path %q", rel)
	}
	return filepath.Join(j.Dir, filepath.Clean(rel)), nil
}

// MkdirAll creates rel (and parents) inside the job dir, refusing to traverse
// anything that is not a real directory.
func (j *Job) MkdirAll(rel string) error {
	if _, err := j.Path(rel); err != nil {
		return err
	}
	cur := j.Dir
	for _, part := range strings.Split(filepath.Clean(rel), string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := os.Mkdir(cur, 0o700); err != nil {
				return fmt.Errorf("render: mkdir %s: %w", part, err)
			}
		case err != nil:
			return fmt.Errorf("render: stat %s: %w", part, err)
		case !fi.IsDir():
			return fmt.Errorf("render: %s exists and is not a directory", part)
		}
	}
	return nil
}

// WriteFile creates rel with mode 0600. It fails if rel exists (O_EXCL) or
// its final component is a symlink (O_NOFOLLOW); parents are created with
// MkdirAll's checks.
func (j *Job) WriteFile(rel string, data []byte) error {
	p, err := j.Path(rel)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(filepath.Clean(rel)); dir != "." {
		if err := j.MkdirAll(dir); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("render: create %s: %w", rel, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("render: write %s: %w", rel, err)
	}
	return f.Close()
}

// ReadFile reads a tool output, refusing symlinks and non-regular files.
func (j *Job) ReadFile(rel string) ([]byte, error) {
	p, err := j.Path(rel)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("render: open %s: %w", rel, err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("render: %s is not a regular file", rel)
	}
	data, err := io.ReadAll(io.LimitReader(f, fi.Size()+1))
	if err != nil {
		return nil, fmt.Errorf("render: read %s: %w", rel, err)
	}
	return data, nil
}

// Remove deletes the job directory.
func (j *Job) Remove() error { return os.RemoveAll(j.Dir) }

// SweepJobs removes leftover job directories older than maxAge (startup).
func SweepJobs(root string, maxAge time.Duration, now time.Time) (int, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, fmt.Errorf("render: sweep: %w", err)
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), jobPrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < maxAge {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err == nil {
			n++
		}
	}
	return n, nil
}
