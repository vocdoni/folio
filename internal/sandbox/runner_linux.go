// SPDX-License-Identifier: AGPL-3.0-only

package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// maxCapture bounds captured stdout/stderr per process.
const maxCapture = 4 << 20

// Runner starts sandboxed processes.
type Runner struct {
	Self string // path of the Folio binary providing `sandbox-exec`; os.Executable() when empty
}

type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil // keep draining so the child never blocks
}

// Run executes s.Tool through `sandbox-exec`. The process runs in its own
// process group with a cleared environment; on timeout or context
// cancellation the whole group is SIGKILLed.
func (r *Runner) Run(ctx context.Context, s Spec, stdin io.Reader) (Result, error) {
	self := r.Self
	if self == "" {
		var err error
		if self, err = os.Executable(); err != nil {
			return Result{}, fmt.Errorf("sandbox: locate self: %w", err)
		}
	}
	if !filepath.IsAbs(s.Tool) || !filepath.IsAbs(s.JobDir) {
		return Result{}, fmt.Errorf("sandbox: tool and job dir must be absolute")
	}
	l := s.Limits
	if l.Timeout <= 0 || l.MemoryBytes == 0 || l.FileSizeBytes == 0 {
		return Result{}, fmt.Errorf("sandbox: incomplete limits %+v", l)
	}
	cpu := uint64(2 * l.Timeout / time.Second)
	if cpu < 2 {
		cpu = 2
	}
	args := []string{"sandbox-exec",
		"-rw", s.JobDir,
		"-as", strconv.FormatUint(l.MemoryBytes, 10),
		"-cpu", strconv.FormatUint(cpu, 10),
		"-fsize", strconv.FormatUint(l.FileSizeBytes, 10),
		"-nproc-headroom", strconv.FormatUint(l.NProcHeadroom, 10),
		"-ro", filepath.Dir(s.Tool),
	}
	for _, d := range s.ReadOnly {
		args = append(args, "-ro", d)
	}
	args = append(args, "--", s.Tool)
	args = append(args, s.Args...)

	ctx, cancel := context.WithTimeout(ctx, l.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, args...)
	cmd.Dir = s.JobDir
	cmd.Env = append(MergeEnv(BaseEnv(s.JobDir), s.Env), helperEnv...)
	cmd.Stdin = stdin
	stdout := &cappedBuffer{max: maxCapture}
	stderr := &cappedBuffer{max: maxCapture}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// Negative pid: signal the whole process group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second

	start := time.Now()
	err := cmd.Run()
	res := Result{Stdout: stdout.buf.Bytes(), Stderr: stderr.buf.Bytes(), Duration: time.Since(start)}
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return res, ErrTimeout
		}
		return res, ctx.Err()
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return res, &ExitError{Tool: s.Tool, Code: ee.ExitCode(), Stderr: res.Stderr}
		}
		return res, fmt.Errorf("sandbox: run %s: %w", filepath.Base(s.Tool), err)
	}
	return res, nil
}
