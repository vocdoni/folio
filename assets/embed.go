// SPDX-License-Identifier: AGPL-3.0-only

// Package bundled embeds Folio's runtime files (fonts, base Typst layouts,
// vendored Typst packages, the Lua filter and pandoc's reference.docx) and
// materialises them into a read-only, content-addressed runtime directory
// that the sandboxed tools can read.
package bundled

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
)

//go:embed all:fonts all:typst all:docx all:lua
var files embed.FS

// FS exposes the embedded tree (read-only).
func FS() fs.FS { return files }

// FontFamilies are the family names (as reported by `typst fonts`) of the
// bundled fonts; brand fonts must be one of the first four. "Noto Sans Math"
// is used only for math.
var FontFamilies = []string{"Inter", "Source Serif 4", "JetBrains Mono", "Noto Sans"}

// MathFont is the bundled math font family.
const MathFont = "Noto Sans Math"

// Runtime describes a materialised runtime directory.
type Runtime struct {
	Root        string // <dir>/<tree-hash>
	FontsDir    string
	PackagesDir string // the only --package-path
	BaseDir     string // base layouts (<base>.typ)
	LuaFilter   string // absolute path of embed.lua
	TreeHash    string
}

// ReferenceDOCX returns pandoc 3.12.1's default reference.docx.
func ReferenceDOCX() ([]byte, error) { return files.ReadFile("docx/reference.docx") }

// TreeHash hashes every embedded path and its content in sorted order.
func TreeHash() (string, error) {
	var names []string
	err := fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			names = append(names, p)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("bundled: walk: %w", err)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		b, err := files.ReadFile(n)
		if err != nil {
			return "", fmt.Errorf("bundled: read %s: %w", n, err)
		}
		sum := sha256.Sum256(b)
		fmt.Fprintf(h, "%s\x00%x\n", n, sum)
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// Materialize writes the embedded tree to dir/<tree-hash>/ (files 0444,
// directories 0555) unless a complete copy already exists, and returns its
// layout. The copy is built in a sibling temp directory and renamed into
// place, so concurrent processes never see a partial tree. dir itself must
// not be a symlink.
func Materialize(dir string) (Runtime, error) {
	if fi, err := os.Lstat(dir); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return Runtime{}, fmt.Errorf("bundled: runtime dir %s is a symlink", dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Runtime{}, fmt.Errorf("bundled: create runtime dir: %w", err)
	}
	hash, err := TreeHash()
	if err != nil {
		return Runtime{}, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Runtime{}, fmt.Errorf("bundled: %w", err)
	}
	root := filepath.Join(abs, hash)
	rt := Runtime{
		Root:        root,
		FontsDir:    filepath.Join(root, "fonts"),
		PackagesDir: filepath.Join(root, "typst", "packages"),
		BaseDir:     filepath.Join(root, "typst", "base"),
		LuaFilter:   filepath.Join(root, "lua", "embed.lua"),
		TreeHash:    hash,
	}
	if _, err := os.Stat(filepath.Join(root, ".complete")); err == nil {
		return rt, nil
	}
	tmp, err := os.MkdirTemp(abs, ".tmp-runtime-")
	if err != nil {
		return Runtime{}, fmt.Errorf("bundled: temp dir: %w", err)
	}
	defer os.RemoveAll(tmp) //nolint:errcheck // best effort after rename
	err = fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(tmp, filepath.FromSlash(p))
		if d.IsDir() {
			if p == "." {
				return nil
			}
			return os.Mkdir(dst, 0o700)
		}
		b, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, b, 0o600); err != nil {
			return err
		}
		return os.Chmod(dst, 0o444) // explicit: independent of the process umask
	})
	if err != nil {
		return Runtime{}, fmt.Errorf("bundled: write tree: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".complete"), []byte(hash+"\n"), 0o600); err != nil {
		return Runtime{}, fmt.Errorf("bundled: marker: %w", err)
	}
	// Directories become read-only only after all files are written.
	if err := lockDirs(tmp); err != nil {
		return Runtime{}, err
	}
	if err := os.Rename(tmp, root); err != nil {
		if _, statErr := os.Stat(filepath.Join(root, ".complete")); statErr == nil {
			_ = unlockDirs(tmp) // let the deferred RemoveAll succeed
			return rt, nil      // another process won the race
		}
		return Runtime{}, fmt.Errorf("bundled: publish runtime: %w", err)
	}
	return rt, nil
}

func unlockDirs(root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			return os.Chmod(p, 0o700)
		}
		return err
	})
}

func lockDirs(root string) error {
	var dirs []string
	if err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("bundled: walk: %w", err)
	}
	for i := len(dirs) - 1; i >= 0; i-- { // children first
		if err := os.Chmod(dirs[i], 0o555); err != nil {
			return fmt.Errorf("bundled: chmod: %w", err)
		}
	}
	return nil
}

// BaseLayouts lists the base layout names present (file names without .typ).
func BaseLayouts() ([]string, error) {
	entries, err := fs.ReadDir(files, "typst/base")
	if err != nil {
		return nil, fmt.Errorf("bundled: base layouts: %w", err)
	}
	var out []string
	for _, e := range entries {
		if n := e.Name(); path.Ext(n) == ".typ" && n != "common.typ" {
			out = append(out, n[:len(n)-len(".typ")])
		}
	}
	return out, nil
}

// VendoredPackages lists allowlisted packages as "@preview/<name>:<version>"
// by scanning dir (normally Runtime.PackagesDir; tests pass a fixture dir).
func VendoredPackages(dir string) ([]string, error) {
	var out []string
	ns, err := os.ReadDir(filepath.Join(dir, "preview"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("bundled: packages: %w", err)
	}
	for _, n := range ns {
		if !n.IsDir() {
			continue
		}
		vs, err := os.ReadDir(filepath.Join(dir, "preview", n.Name()))
		if err != nil {
			return nil, fmt.Errorf("bundled: packages: %w", err)
		}
		for _, v := range vs {
			if v.IsDir() {
				out = append(out, "@preview/"+n.Name()+":"+v.Name())
			}
		}
	}
	sort.Strings(out)
	return out, nil
}
