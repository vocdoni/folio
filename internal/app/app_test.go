// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vocdoni/folio/internal/activity"
	"github.com/vocdoni/folio/internal/config"
	"github.com/vocdoni/folio/internal/httpserver"
	"github.com/vocdoni/folio/internal/sandbox"
	"github.com/vocdoni/folio/internal/store"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "sandbox-exec" {
		os.Exit(sandbox.Main(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func files(m map[string]string) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) {
		if v, ok := m[p]; ok {
			return []byte(v), nil
		}
		return nil, os.ErrNotExist
	}
}

func TestMemoryLimit(t *testing.T) {
	for _, c := range []struct {
		files map[string]string
		want  uint64
		ok    bool
	}{
		{map[string]string{"/sys/fs/cgroup/memory.max": "2147483648\n"}, 2 << 30, true},
		{map[string]string{"/sys/fs/cgroup/memory.max": "max\n"}, 0, false},
		{map[string]string{"/sys/fs/cgroup/memory/memory.limit_in_bytes": "9223372036854771712"}, 0, false},
		{map[string]string{"/sys/fs/cgroup/memory/memory.limit_in_bytes": "4294967296"}, 4 << 30, true},
		{nil, 0, false},
	} {
		got, ok := MemoryLimit(files(c.files))
		if got != c.want || ok != c.ok {
			t.Errorf("%v: got %d %v", c.files, got, ok)
		}
	}
}

func TestMemoryBudgetWarning(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	cfg := &config.Config{RenderConcurrency: 4, RenderMemory: 1 << 30}
	logMemoryBudget(log, cfg, files(map[string]string{"/sys/fs/cgroup/memory.max": "2147483648"}))
	if !strings.Contains(buf.String(), `"level":"WARN"`) || !strings.Contains(buf.String(), `"cgroup_memory_max_bytes":2147483648`) {
		t.Fatalf("expected a warning: %s", buf.String())
	}
	buf.Reset()
	cfg.RenderConcurrency = 1
	logMemoryBudget(log, cfg, files(map[string]string{"/sys/fs/cgroup/memory.max": "2147483648"}))
	if strings.Contains(buf.String(), `"level":"WARN"`) {
		t.Fatalf("unexpected warning: %s", buf.String())
	}
}

// dataDir returns a temp data dir whose read-only runtime tree is made
// writable again before the test framework removes it.
func dataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = os.Chmod(p, 0o700)
			}
			return nil
		})
	})
	return dir
}

func testConfig(t *testing.T, extra map[string]string) *config.Config {
	t.Helper()
	env := map[string]string{
		"FOLIO_PUBLIC_URL": "http://127.0.0.1:1", "FOLIO_DATA_DIR": dataDir(t),
		"FOLIO_TOKENS": "alice:alice-token-0123456789abcdefghijklmnop", "FOLIO_SEED": "false", "FOLIO_LOG_FORMAT": "text",
	}
	for k, v := range extra {
		env[k] = v
	}
	cfg, err := config.Load(func(k string) (string, bool) { v, ok := env[k]; return v, ok }, config.ModeServe)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func requireTools(t *testing.T) {
	t.Helper()
	for _, name := range []string{"pandoc", "typst"} {
		if _, err := sandbox.LookupTool(name); err != nil {
			t.Skipf("SKIPPING: %v (set FOLIO_PANDOC / FOLIO_TYPST)", err)
		}
	}
}

func TestAppServesAndShutsDown(t *testing.T) {
	requireTools(t)
	cfg := testConfig(t, nil)
	var logs bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := New(ctx, cfg, Options{Version: "test", Logger: slog.New(slog.NewTextHandler(&logs, nil)), JobRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.Seeded {
		t.Fatal("FOLIO_SEED=false must not seed")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx, ln) }()
	base := "http://" + ln.Addr().String()
	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	var h httpserver.Health
	_ = json.NewDecoder(resp.Body).Decode(&h)
	resp.Body.Close()
	if h.Status != "ok" || h.Version != "test" || h.SchemaVersion != store.SchemaVersion() || h.Sandbox == "" {
		t.Fatalf("healthz: %+v", h)
	}
	resp, err = http.Post(base+"/mcp", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/mcp without token: %d", resp.StatusCode)
	}
	for _, want := range []string{"sandbox", "render memory budget", "startup maintenance done"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("startup log lacks %q", want)
		}
	}
	if strings.Contains(logs.String(), "alice-token") {
		t.Fatal("a token reached the logs")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

// TestCloseFlushesActivity: the activity writer runs from New (without Run)
// and Close writes the rows still queued before closing the database.
func TestCloseFlushesActivity(t *testing.T) {
	requireTools(t)
	cfg := testConfig(t, nil)
	a, err := New(context.Background(), cfg, Options{Version: "test", Logger: slog.New(slog.DiscardHandler), JobRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"get_guide", "list_documents"} {
		a.Activity.Record(activity.Row{Source: activity.SourceMCP, Actor: "alice", Action: action, Outcome: activity.OutcomeOK})
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if a.Activity.Written() != 2 || a.Activity.Dropped() != 0 {
		t.Fatalf("written %d, dropped %d, want 2 and 0", a.Activity.Written(), a.Activity.Dropped())
	}
	db, err := store.Open(context.Background(), cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, _, err := activity.List(context.Background(), db, activity.Filter{})
	if err != nil || len(rows) != 2 {
		t.Fatalf("stored rows %d, %v", len(rows), err)
	}
}

func TestNewFailsOnBrokenTool(t *testing.T) {
	requireTools(t)
	t.Setenv("FOLIO_PANDOC", "/bin/false")
	cfg := testConfig(t, nil)
	_, err := New(context.Background(), cfg, Options{Version: "test", Logger: slog.New(slog.DiscardHandler), JobRoot: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "could not write a DOCX") {
		t.Fatalf("broken pandoc must fail startup with a clear message: %v", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatal("unexpected cancel")
	}
}
