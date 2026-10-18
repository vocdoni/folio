// SPDX-License-Identifier: AGPL-3.0-only

// Package sandbox runs external tools (pandoc, typst) with rlimits,
// no_new_privs and, when the kernel supports it, a Landlock ruleset. The
// parent side (Runner.Run) re-executes the Folio binary as
// `folio sandbox-exec ... -- <tool> <args>`; the child side (Main) applies the
// restrictions to itself and then execve()s the tool.
package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Level is the sandbox strength available on this host.
type Level string

const (
	LevelLandlock   Level = "landlock+rlimits"
	LevelRlimitOnly Level = "rlimits-only"
)

// Limits bound one external process.
type Limits struct {
	Timeout       time.Duration // wall clock, enforced by the parent
	MemoryBytes   uint64        // RLIMIT_AS
	FileSizeBytes uint64        // RLIMIT_FSIZE
	NProcHeadroom uint64        // RLIMIT_NPROC = tasks owned by the uid + headroom
}

// DefaultLimits mirrors the documented defaults.
func DefaultLimits() Limits {
	return Limits{Timeout: 60 * time.Second, MemoryBytes: 1 << 30, FileSizeBytes: 256 << 20, NProcHeadroom: 256}
}

// Spec describes one sandboxed execution.
type Spec struct {
	Tool     string   // absolute path to the tool binary
	Args     []string // arguments (without argv[0])
	JobDir   string   // the only read-write directory; also the working directory and HOME
	ReadOnly []string // read-only directories (tool dir is added automatically)
	Env      []string // extra KEY=VALUE entries (SOURCE_DATE_EPOCH, TYPST_*)
	Limits   Limits
}

// Result is the captured output of a successful run.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	Duration time.Duration
}

var (
	// ErrTimeout means the process group was killed at the wall-clock limit.
	ErrTimeout = errors.New("sandbox: process timed out")
	// ErrToolNotFound means a tool binary could not be located.
	ErrToolNotFound = errors.New("sandbox: tool not found")
)

// ExitError is a non-zero exit of the tool (or of sandbox-exec itself).
// Exit codes 125 and 126 are sandbox-exec's own failures (usage/limits/landlock and exec); any other code is the tool's.
type ExitError struct {
	Tool   string
	Code   int
	Stderr []byte
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("sandbox: %s exited with code %d", filepath.Base(e.Tool), e.Code)
}

// deadProxy makes every proxied download fail fast (nothing listens there).
const deadProxy = "http://127.0.0.1:9"

// BaseEnv returns the cleared environment every tool gets.
func BaseEnv(jobDir string) []string {
	return []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + jobDir,
		"LANG=C.UTF-8",
		"HTTPS_PROXY=" + deadProxy, "HTTP_PROXY=" + deadProxy,
		"https_proxy=" + deadProxy, "http_proxy=" + deadProxy,
		"ALL_PROXY=" + deadProxy, "NO_PROXY=",
	}
}

// MergeEnv returns base with every KEY=VALUE in extra replacing the entry
// with the same key (tools read the first match, so duplicates must not exist).
func MergeEnv(base, extra []string) []string {
	idx := map[string]int{}
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range append(append([]string{}, base...), extra...) {
		k, _, _ := strings.Cut(kv, "=")
		if i, ok := idx[k]; ok {
			out[i] = kv
			continue
		}
		idx[k] = len(out)
		out = append(out, kv)
	}
	return out
}

// LookupTool finds pandoc or typst: $FOLIO_PANDOC / $FOLIO_TYPST when set,
// else PATH. The result is absolute and symlinks are resolved so the Landlock
// read-only rule covers the real binary's directory.
func LookupTool(name string) (string, error) {
	env := "FOLIO_" + strings.ToUpper(name)
	p := os.Getenv(env)
	if p == "" {
		var err error
		if p, err = exec.LookPath(name); err != nil {
			return "", fmt.Errorf("%w: %s (set %s or add it to PATH)", ErrToolNotFound, name, env)
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %w", ErrToolNotFound, name, err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %w", ErrToolNotFound, name, err)
	}
	return real, nil
}
