// SPDX-License-Identifier: AGPL-3.0-only

package bundled

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// runtimeDir returns a temp dir whose read-only runtime tree is made
// writable again before t.TempDir's cleanup removes it.
func runtimeDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.Walk(dir, func(p string, _ os.FileInfo, _ error) error { return os.Chmod(p, 0o700) })
	})
	return dir
}

func TestMaterializeIdempotentAndReadOnly(t *testing.T) {
	dir := runtimeDir(t)
	rt, err := Materialize(dir)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Materialize(dir)
	if err != nil || again.Root != rt.Root {
		t.Fatalf("second materialize: %v %s", err, again.Root)
	}
	fi, err := os.Stat(filepath.Join(rt.FontsDir, "Inter-Regular.ttf"))
	if err != nil || fi.Mode().Perm() != 0o444 {
		t.Fatalf("font file: %v %v", err, fi)
	}
	if err := os.WriteFile(filepath.Join(rt.FontsDir, "x"), []byte("x"), 0o600); err == nil {
		t.Fatal("runtime dir is writable")
	}
	t.Cleanup(func() {
		_ = filepath.Walk(dir, func(p string, _ os.FileInfo, _ error) error { return os.Chmod(p, 0o700) })
	})
}

func TestReferenceDOCXIsPandocDefault(t *testing.T) {
	b, err := ReferenceDOCX()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != "19c46de4ba4a07f67e877b7217263b680b9f20a046bca24a8e45829a9996f0a4" {
		t.Fatalf("reference.docx sha256 = %s (regenerate with SOURCE_DATE_EPOCH=0 pandoc 3.12.1 -o … --print-default-data-file reference.docx)", got)
	}
}

func TestVendoredPackagesEmptyByDefault(t *testing.T) {
	rt, err := Materialize(runtimeDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if pk, err := VendoredPackages(rt.PackagesDir); err != nil || len(pk) != 0 {
		t.Fatalf("packages = %v, %v", pk, err)
	}
	fix := t.TempDir()
	if err := os.MkdirAll(filepath.Join(fix, "preview", "hello", "0.1.0"), 0o700); err != nil {
		t.Fatal(err)
	}
	if pk, _ := VendoredPackages(fix); len(pk) != 1 || pk[0] != "@preview/hello:0.1.0" {
		t.Fatalf("fixture packages = %v", pk)
	}
}

func TestFontFamiliesMatchTypst(t *testing.T) {
	typst := os.Getenv("FOLIO_TYPST")
	if typst == "" {
		var err error
		if typst, err = exec.LookPath("typst"); err != nil {
			t.Skip("SKIPPING: typst not found; set FOLIO_TYPST to typst 0.15.1")
		}
	}
	rt, err := Materialize(runtimeDir(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(typst, "fonts", "--font-path", rt.FontsDir, "--ignore-system-fonts", "--ignore-embedded-fonts").Output()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(out)), " ", "_"))
	want := append([]string{}, FontFamilies...)
	want = append(want, MathFont)
	for i := range want {
		want[i] = strings.ReplaceAll(want[i], " ", "_")
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("typst fonts = %v, want %v", got, want)
	}
}
