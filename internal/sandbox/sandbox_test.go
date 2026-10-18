// SPDX-License-Identifier: AGPL-3.0-only

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain doubles as `folio sandbox-exec` and as a helper "tool".
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "sandbox-exec" {
		os.Exit(Main(os.Args[2:]))
	}
	if act := os.Getenv("FOLIO_SANDBOX_HELPER"); act != "" {
		os.Exit(helper(act, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func helper(act string, args []string) int {
	switch act {
	case "read":
		if _, err := os.ReadFile(args[0]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 3
		}
	case "write":
		if err := os.WriteFile(args[0], []byte("x"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 3
		}
	case "dial":
		c, err := net.DialTimeout("tcp", args[0], 2*time.Second)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 3
		}
		c.Close()
	case "env":
		fmt.Print(strings.Join(os.Environ(), "\n"))
	case "spin":
		for {
		}
	case "fork-spin":
		self, _ := os.Executable()
		p, err := os.StartProcess(self, []string{self}, &os.ProcAttr{Env: []string{"FOLIO_SANDBOX_HELPER=spin"}})
		if err != nil {
			return 4
		}
		_ = os.WriteFile("child.pid", []byte(strconv.Itoa(p.Pid)), 0o600)
		for {
		}
	case "alloc":
		// The marker proves the helper started before the allocation.
		fmt.Println(allocMarker)
		b := make([]byte, 2<<30)
		for i := 0; i < len(b); i += 4096 {
			b[i] = 1
		}
	}
	return 0
}

func testSpec(t *testing.T, act string, args ...string) (Spec, *Runner) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	l := DefaultLimits()
	l.Timeout = 10 * time.Second
	if raceEnabled {
		l.MemoryBytes = ^uint64(0) // RLIM_INFINITY; see race_on_test.go
	}
	return Spec{
		Tool:     self,
		Args:     args,
		JobDir:   t.TempDir(),
		ReadOnly: testLibDirs, // the -race test binary is dynamically linked
		Env:      []string{"FOLIO_SANDBOX_HELPER=" + act},
		Limits:   l,
	}, &Runner{Self: self}
}

// testLibDirs lets a cgo-linked (-race) test binary load its shared
// libraries under Landlock; production tools are static and never need them.
var testLibDirs = []string{"/lib", "/lib64", "/usr/lib", "/usr/lib64"}

func requireABI(t *testing.T, min int) {
	t.Helper()
	if abi := ProbeABI(); abi < min {
		t.Skipf("SKIPPING: Landlock ABI %d < %d on this kernel", abi, min)
	}
}

func TestSecurityLandlockFilesystem(t *testing.T) {
	requireABI(t, 1)
	s, r := testSpec(t, "read", "/etc/passwd")
	_, err := r.Run(context.Background(), s, nil)
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 3 {
		t.Fatalf("read /etc/passwd: err = %v, want exit 3 (stderr: %s)", err, errStderr(err))
	}
	s, r = testSpec(t, "write", filepath.Join(filepath.Dir(s.JobDir), "escape.txt"))
	if _, err := r.Run(context.Background(), s, nil); !errors.As(err, &ee) || ee.Code != 3 {
		t.Fatalf("write outside job dir: err = %v, want exit 3", err)
	}
	s, r = testSpec(t, "write", "inside.txt")
	if _, err := r.Run(context.Background(), s, nil); err != nil {
		t.Fatalf("write inside job dir: %v", err)
	}
}

func TestSecurityLandlockDeniesTCP(t *testing.T) {
	requireABI(t, 4)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan struct{}, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			c.Close()
			accepted <- struct{}{}
		}
	}()
	s, r := testSpec(t, "dial", ln.Addr().String())
	_, err = r.Run(context.Background(), s, nil)
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 3 {
		t.Fatalf("dial: err = %v, want exit 3", err)
	}
	select {
	case <-accepted:
		t.Fatal("listener received a connection from the sandbox")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestSecurityTimeoutKillsProcessGroup(t *testing.T) {
	s, r := testSpec(t, "fork-spin")
	s.Limits.Timeout = time.Second
	start := time.Now()
	_, err := r.Run(context.Background(), s, nil)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("kill took %v", d)
	}
	raw, err := os.ReadFile(filepath.Join(s.JobDir, "child.pid"))
	if err != nil {
		t.Fatalf("grandchild pid not written: %v", err)
	}
	pid, _ := strconv.Atoi(string(raw))
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d survived the group kill", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestMemoryLimit(t *testing.T) {
	if raceEnabled {
		t.Skip("SKIPPING under -race: the race runtime cannot start under RLIMIT_AS; covered by the non-race run")
	}
	// 1 GiB lets the Go helper start (a smaller limit kills it during runtime
	// init), so the failure below comes from the 2 GiB allocation itself.
	s, r := testSpec(t, "alloc")
	s.Limits.MemoryBytes = 1 << 30
	res, err := r.Run(context.Background(), s, nil)
	if err == nil {
		t.Fatal("2 GiB allocation succeeded under a 1 GiB RLIMIT_AS")
	}
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("err = %v, want *ExitError", err)
	}
	if !strings.Contains(string(res.Stdout), allocMarker) {
		t.Fatalf("helper did not start under RLIMIT_AS (exit %d, stderr: %s)", ee.Code, ee.Stderr)
	}
	if !strings.Contains(string(ee.Stderr), "out of memory") {
		t.Fatalf("exit %d is not an allocation refusal (stderr: %s)", ee.Code, ee.Stderr)
	}
}

// allocMarker is printed by the "alloc" helper right before it allocates.
const allocMarker = "folio-sandbox-alloc-start"

func TestEnvironmentIsCleared(t *testing.T) {
	t.Setenv("FOLIO_TOKEN", "must-not-leak-0123456789abcdef0123456789")
	s, r := testSpec(t, "env")
	res, err := r.Run(context.Background(), s, nil)
	if err != nil {
		t.Fatal(err)
	}
	env := string(res.Stdout)
	if strings.Contains(env, "FOLIO_TOKEN") {
		t.Fatal("parent environment leaked into the sandbox")
	}
	for _, want := range []string{"HOME=" + s.JobDir, "HTTPS_PROXY=http://127.0.0.1:9"} {
		if !strings.Contains(env, want) {
			t.Fatalf("env missing %q:\n%s", want, env)
		}
	}
}

func TestRealToolsRunInSandbox(t *testing.T) {
	for _, name := range []string{"pandoc", "typst"} {
		tool, err := LookupTool(name)
		if err != nil {
			t.Skipf("SKIPPING: %v (set FOLIO_PANDOC / FOLIO_TYPST to the pinned binaries)", err)
		}
		self, _ := os.Executable()
		job := t.TempDir()
		if err := os.WriteFile(filepath.Join(job, "in.typ"), []byte("Hello"), 0o600); err != nil {
			t.Fatal(err)
		}
		args := []string{"--version"}
		if name == "typst" {
			args = []string{"compile", "--root", job, "--ignore-system-fonts", "in.typ", "out.pdf"}
		}
		res, err := (&Runner{Self: self}).Run(context.Background(), Spec{Tool: tool, Args: args, JobDir: job, Limits: DefaultLimits()}, nil)
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, res.Stderr)
		}
	}
}

func errStderr(err error) string {
	var ee *ExitError
	if errors.As(err, &ee) {
		return string(ee.Stderr)
	}
	return ""
}

// TestToolsStartUnderMemoryLimit guards the RLIMIT_AS ordering: the helper
// must lower the limit only immediately before execve, so real tools start
// reliably under the default 1 GiB limit.
func TestToolsStartUnderMemoryLimit(t *testing.T) {
	tool, err := LookupTool("pandoc")
	if err != nil {
		t.Skipf("SKIPPING: %v (set FOLIO_PANDOC)", err)
	}
	self, _ := os.Executable()
	for i := 0; i < 30; i++ {
		res, err := (&Runner{Self: self}).Run(context.Background(), Spec{Tool: tool, Args: []string{"--version"}, JobDir: t.TempDir(), Limits: DefaultLimits()}, nil)
		if err != nil {
			t.Fatalf("run %d: %v\n%s", i, err, res.Stderr)
		}
	}
}
