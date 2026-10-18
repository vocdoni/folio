// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/vocdoni/folio/internal/activity"
	"github.com/vocdoni/folio/internal/config"
)

// Fake tokens for tests only.
const (
	connTokA = "connect-test-token-aaaaaaaaaaaaaaaaaaaa"
	connTokB = "connect-test-token-bbbbbbbbbbbbbbbbbbbb"
)

func TestConnectionNeedsSessionAndIsNotCached(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.Config.Tokens = []config.Token{{Name: "agent", Value: connTokA}} })
	if w := f.do(http.MethodGet, "/ui/api/connection", nil, nil); w.Code != http.StatusUnauthorized || strings.Contains(w.Body.String(), connTokA) {
		t.Fatalf("signed out: %d %s", w.Code, w.Body)
	}
	f.login()
	w := f.do(http.MethodGet, "/ui/api/connection", nil, nil)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d cache-control %q", w.Code, w.Header().Get("Cache-Control"))
	}
	var out ConnectionOut
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.MCPURL != "https://folio.example.test/mcp" || out.SkillURL != "https://folio.example.test/skills/folio/SKILL.md" ||
		len(out.Tokens) != 1 || out.Tokens[0].Name != "agent" || out.Tokens[0].Token != connTokA {
		t.Fatalf("connection %+v", out)
	}
}

func TestConnectionRecordsRevealWithoutToken(t *testing.T) {
	var rec *activity.Recorder
	f := newFixture(t, func(o *Options) {
		rec = activity.NewRecorder(o.DB, nil, o.Logger, o.Now)
		o.Activity = rec
		o.Config.Tokens = []config.Token{{Name: "alice", Value: connTokA}, {Name: "bob", Value: connTokB}}
	})
	f.login()
	for range 2 {
		if w := f.do(http.MethodGet, "/ui/api/connection", nil, nil); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec.Run(ctx) // flushes the rows
	rows, _, err := activity.List(context.Background(), f.opts.DB, activity.Filter{Action: "ui.connection.reveal"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Source != "ui" || rows[0].Actor != Actor || rows[0].Outcome != "ok" {
		t.Fatalf("rows %+v", rows)
	}
	b, _ := json.Marshal(rows)
	if strings.Contains(string(b), connTokA) || strings.Contains(string(b), connTokB) {
		t.Fatalf("a token reached the activity row: %s", b)
	}
}

func TestConnectionWithoutTokensWhenAuthIsOff(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.Config.InsecureNoAuth = true; o.Config.Tokens = nil })
	f.login()
	w := f.do(http.MethodGet, "/ui/api/connection", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"tokens":[]`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// Configured tokens guard /mcp even with FOLIO_INSECURE_NO_AUTH set, so the
// UI must still show them.
func TestConnectionListsTokensEvenWithInsecureFlag(t *testing.T) {
	f := newFixture(t, func(o *Options) {
		o.Config.InsecureNoAuth = true
		o.Config.Tokens = []config.Token{{Name: "alice", Value: connTokA}}
	})
	f.login()
	w := f.do(http.MethodGet, "/ui/api/connection", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), connTokA) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
