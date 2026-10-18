// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"context"
	"testing"
	"time"

	"github.com/vocdoni/folio/internal/activity"
)

// TestEveryToolCallIsRecorded: one activity row per call, read and write,
// ok and error, with the target, the error code and the allowlisted detail;
// rows reach live subscribers.
func TestEveryToolCallIsRecorded(t *testing.T) {
	e := newEnv(t)
	b := activity.NewBroadcaster()
	sub, err := b.Subscribe("test", 3)
	if err != nil {
		t.Fatal(err)
	}
	rec := activity.NewRecorder(e.d.DB, b, e.d.Logger, e.d.Now)
	e.d.Activity = rec
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rec.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	cs := connect(t, New(e.d), nil)
	callOK(t, cs, "get_guide", map[string]any{}, nil)                                            // read, ok, no target
	callErr(t, cs, "get_document", map[string]any{"id": "0199a000-0000-7000-8000-000000000001"}) // read, error
	callOK(t, cs, "create_brand", map[string]any{"brand": testBrand("acme", "")}, nil)           // write, ok
	callErr(t, cs, "archive_template", map[string]any{"id": "missing", "archived": true})        // write, error

	deadline := time.Now().Add(5 * time.Second)
	for rec.Written() < 4 {
		if time.Now().After(deadline) {
			t.Fatalf("written %d rows, want 4", rec.Written())
		}
		time.Sleep(10 * time.Millisecond)
	}
	rows, _, err := activity.List(context.Background(), e.d.DB, activity.Filter{})
	if err != nil || len(rows) != 4 {
		t.Fatalf("rows %d, %v", len(rows), err)
	}
	want := []struct{ action, kind, id, outcome, code string }{
		{"archive_template", "template", "missing", "error", "not_found"},
		{"create_brand", "brand", "acme", "ok", ""},
		{"get_document", "document", "0199a000-0000-7000-8000-000000000001", "error", "not_found"},
		{"get_guide", "", "", "ok", ""},
	}
	for i, w := range want {
		r := rows[i]
		if r.Source != "mcp" || r.Actor != LocalActor || r.Action != w.action || r.TargetKind != w.kind || r.TargetID != w.id ||
			r.Outcome != w.outcome || r.ErrorCode != w.code {
			t.Errorf("row %d = %+v, want %+v", i, r, w)
		}
	}
	if rows[1].Detail.Version != 1 {
		t.Errorf("create_brand detail %+v", rows[1].Detail)
	}
	for i := 0; i < 4; i++ {
		select {
		case <-sub.C:
		case <-time.After(2 * time.Second):
			t.Fatalf("stream received %d rows, want 4", i)
		}
	}
}

// TestToolCallsNeverBlockOnActivity: with the recorder's queue full (no
// writer running), tool calls still complete and their rows are dropped
// and counted instead of waiting.
func TestToolCallsNeverBlockOnActivity(t *testing.T) {
	e := newEnv(t)
	rec := activity.NewRecorder(e.d.DB, nil, e.d.Logger, e.d.Now)
	for i := 0; i < activity.QueueSize; i++ {
		rec.Record(activity.Row{Source: activity.SourceMCP, Actor: LocalActor, Action: "get_guide", Outcome: activity.OutcomeOK})
	}
	if rec.Dropped() != 0 {
		t.Fatalf("dropped %d rows while filling the queue", rec.Dropped())
	}
	e.d.Activity = rec

	cs := connect(t, New(e.d), nil)
	start := time.Now()
	callOK(t, cs, "get_guide", map[string]any{}, nil)
	callErr(t, cs, "get_document", map[string]any{"id": "0199a000-0000-7000-8000-000000000001"})
	callOK(t, cs, "create_brand", map[string]any{"brand": testBrand("acme", "")}, nil)
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("tool calls took %s with a full activity queue", d)
	}
	if got := rec.Dropped(); got != 3 {
		t.Errorf("dropped %d rows, want 3 (one per call)", got)
	}
	if rec.Written() != 0 {
		t.Errorf("written %d rows without a writer", rec.Written())
	}
}
