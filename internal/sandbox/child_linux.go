// SPDX-License-Identifier: AGPL-3.0-only

package sandbox

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/landlock-lsm/go-landlock/landlock"
	llsys "github.com/landlock-lsm/go-landlock/landlock/syscall"
	"golang.org/x/sys/unix"
)

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// ProbeABI returns the Landlock ABI version (≤ 0 when unavailable).
func ProbeABI() int {
	v, err := llsys.LandlockGetABIVersion()
	if err != nil {
		return 0
	}
	return v
}

// HostLevel reports the sandbox level for this kernel.
func HostLevel() Level {
	if ProbeABI() >= 1 {
		return LevelLandlock
	}
	return LevelRlimitOnly
}

// Main implements `folio sandbox-exec`. Order: rlimits, no_new_privs,
// Landlock (best effort), execve. It returns only on failure; the caller
// passes the result to os.Exit.
func Main(args []string) int {
	fs := flag.NewFlagSet("sandbox-exec", flag.ContinueOnError)
	var ro multiFlag
	fs.Var(&ro, "ro", "read-only directory (repeatable)")
	rw := fs.String("rw", "", "read-write job directory")
	as := fs.Uint64("as", 0, "RLIMIT_AS bytes")
	cpu := fs.Uint64("cpu", 0, "RLIMIT_CPU seconds")
	fsize := fs.Uint64("fsize", 0, "RLIMIT_FSIZE bytes")
	headroom := fs.Uint64("nproc-headroom", 256, "RLIMIT_NPROC headroom over current tasks")
	if err := fs.Parse(args); err != nil {
		return 125
	}
	rest := fs.Args()
	if *rw == "" || *as == 0 || *cpu == 0 || *fsize == 0 || len(rest) == 0 || !strings.HasPrefix(rest[0], "/") {
		fmt.Fprintln(os.Stderr, "sandbox-exec: usage: sandbox-exec -rw DIR -as N -cpu N -fsize N [-ro DIR]... -- /abs/tool [args]")
		return 125
	}
	// RLIMIT_AS is applied last, immediately before execve (see execWithAS):
	// the Go runtime of this helper already maps more than the limit, so any
	// allocation after lowering it would crash the helper.
	limits := []struct {
		res int
		val uint64
	}{
		{unix.RLIMIT_CORE, 0},
		{unix.RLIMIT_CPU, *cpu},
		{unix.RLIMIT_FSIZE, *fsize},
		{unix.RLIMIT_NPROC, uint64(countUserTasks(os.Getuid())) + *headroom},
	}
	for _, l := range limits {
		if err := unix.Setrlimit(l.res, &unix.Rlimit{Cur: l.val, Max: l.val}); err != nil {
			fmt.Fprintf(os.Stderr, "sandbox-exec: setrlimit %d: %v\n", l.res, err)
			return 125
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox-exec: no_new_privs: %v\n", err)
		return 125
	}
	rules := []landlock.Rule{
		landlock.RWDirs(*rw),
		landlock.RWFiles("/dev/null"),
		landlock.ROFiles("/dev/urandom").IgnoreIfMissing(),
	}
	if len(ro) > 0 {
		rules = append(rules, landlock.RODirs(ro...).IgnoreIfMissing())
	}
	// V6 best effort: filesystem, TCP bind/connect (ABI ≥ 4, no net rules
	// means all denied) and IPC scoping (ABI ≥ 6); silently weaker on older
	// kernels, a no-op without Landlock (level reported as rlimits-only).
	if err := landlock.V6.BestEffort().Restrict(rules...); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox-exec: landlock: %v\n", err)
		return 125
	}
	tool := rest[0]
	if err := execWithAS(tool, rest, toolEnv(os.Environ()), *as); err != nil {
		fmt.Fprintf(os.Stderr, "sandbox-exec: exec %s: %v\n", tool, err)
		return 126
	}
	return 0 // unreachable
}

// helperEnv are set by Runner for this helper only (no GC, one P, so the
// runtime does not allocate between lowering RLIMIT_AS and execve); they are
// removed from the tool's environment.
var helperEnv = []string{"GOGC=off", "GOMAXPROCS=1"}

func toolEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if !slices.Contains(helperEnv, kv) {
			out = append(out, kv)
		}
	}
	return out
}

// execWithAS prepares every execve argument first, then lowers RLIMIT_AS and
// calls execve with raw syscalls so nothing is allocated in between. On
// success it never returns.
func execWithAS(tool string, argv, env []string, as uint64) error {
	path, err := syscall.BytePtrFromString(tool)
	if err != nil {
		return err
	}
	argvp, err := syscall.SlicePtrFromStrings(argv)
	if err != nil {
		return err
	}
	envp, err := syscall.SlicePtrFromStrings(env)
	if err != nil {
		return err
	}
	lim := unix.Rlimit{Cur: as, Max: as}
	runtime.LockOSThread()
	if _, _, e := syscall.RawSyscall6(unix.SYS_PRLIMIT64, 0, unix.RLIMIT_AS, uintptr(unsafe.Pointer(&lim)), 0, 0, 0); e != 0 {
		return fmt.Errorf("setrlimit RLIMIT_AS: %w", e)
	}
	_, _, e := syscall.RawSyscall(syscall.SYS_EXECVE,
		uintptr(unsafe.Pointer(path)), uintptr(unsafe.Pointer(&argvp[0])), uintptr(unsafe.Pointer(&envp[0])))
	return e
}

// countUserTasks counts tasks (threads) owned by uid, which is what
// RLIMIT_NPROC compares against. Unreadable entries are skipped.
func countUserTasks(uid int) int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	want := []byte(strconv.Itoa(uid))
	n := 0
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		status, err := os.ReadFile("/proc/" + e.Name() + "/status")
		if err != nil {
			continue
		}
		var uidLine, threads []byte
		for _, line := range bytes.Split(status, []byte("\n")) {
			if v, ok := bytes.CutPrefix(line, []byte("Uid:")); ok {
				uidLine = v
			} else if v, ok := bytes.CutPrefix(line, []byte("Threads:")); ok {
				threads = bytes.TrimSpace(v)
			}
		}
		f := bytes.Fields(uidLine)
		if len(f) == 0 || !bytes.Equal(f[0], want) {
			continue
		}
		t, err := strconv.Atoi(string(threads))
		if err != nil {
			t = 1
		}
		n += t
	}
	return n
}
