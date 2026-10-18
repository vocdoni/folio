// SPDX-License-Identifier: AGPL-3.0-only

package activity

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/vocdoni/folio/internal/store"
)

func openDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func waitWritten(t *testing.T, r *Recorder, n int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for r.Written() < n {
		if time.Now().After(deadline) {
			t.Fatalf("written %d rows, want %d", r.Written(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSanitizeAllowlist(t *testing.T) {
	r, err := Sanitize(Row{Source: SourceMCP, Actor: "alice", Action: "get_document", TargetKind: "document",
		TargetID: "<script>", Outcome: OutcomeOK, ErrorCode: "not_found", DurationMS: -4,
		Detail: Detail{Formats: []string{"pdf", "exe", "docx", "pdf"}}})
	if err != nil {
		t.Fatal(err)
	}
	if r.TargetID != "" || r.TargetKind != "" || r.ErrorCode != "" || r.DurationMS != 0 || len(r.Detail.Formats) != 2 || r.Detail.Formats[1] != "docx" {
		t.Fatalf("not sanitised: %+v", r)
	}
	for _, bad := range []Row{
		{Source: "cli", Actor: "alice", Action: "x", Outcome: OutcomeOK},
		{Source: SourceUI, Actor: "Alice Smith", Action: "x", Outcome: OutcomeOK},
		{Source: SourceUI, Actor: "ui", Action: "Drop Table", Outcome: OutcomeOK},
		{Source: SourceUI, Actor: "ui", Action: "ui.login", Outcome: "maybe"},
	} {
		if _, err := Sanitize(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestRecorderBatchesPublishesAndLists(t *testing.T) {
	db := openDB(t)
	b := NewBroadcaster()
	sub, err := b.Subscribe("s1", 3)
	if err != nil {
		t.Fatal(err)
	}
	rec := NewRecorder(db, b, quiet, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rec.Run(ctx); close(done) }()
	for i := 0; i < 250; i++ {
		outcome, code := OutcomeOK, ""
		if i%10 == 0 {
			outcome, code = OutcomeErr, "not_found"
		}
		rec.Record(Row{Source: SourceMCP, Actor: "alice", Action: "get_document", TargetKind: "document", TargetID: "d1",
			Outcome: outcome, ErrorCode: code, DurationMS: int64(i)})
	}
	waitWritten(t, rec, 250)
	var last int64
	for i := 0; i < SubscriberBuffer; i++ { // the first 64 reach the subscriber, then it is dropped as too slow
		select {
		case r := <-sub.C:
			if r.ID <= last {
				t.Fatalf("ids out of order: %d after %d", r.ID, last)
			}
			last = r.ID
		case <-time.After(time.Second):
			t.Fatalf("received %d rows", i)
		}
	}
	select {
	case <-sub.Done:
	case <-time.After(time.Second):
		t.Fatal("a subscriber that fell behind must be dropped")
	}
	page, next, err := List(context.Background(), db, Filter{Outcome: OutcomeErr, Limit: 10})
	if err != nil || len(page) != 10 || next == 0 || page[0].ErrorCode != "not_found" || page[0].ID < page[9].ID {
		t.Fatalf("page: %d rows next=%d err=%v", len(page), next, err)
	}
	rest, next2, err := List(context.Background(), db, Filter{Outcome: OutcomeErr, Before: next})
	if err != nil || len(rest) != 15 || next2 != 0 || rest[0].ID >= next {
		t.Fatalf("rest: %d rows next=%d err=%v", len(rest), next2, err)
	}
	f, err := ListFacets(context.Background(), db)
	if err != nil || len(f.Actors) != 1 || f.Actions[0] != "get_document" {
		t.Fatalf("facets %+v %v", f, err)
	}
	cancel()
	<-done
}

func TestRecorderDropsWhenFullAndFlushesOnShutdown(t *testing.T) {
	db := openDB(t)
	const size = 64 // a small queue: the shutdown flush is bounded in time
	rec := newRecorder(db, nil, quiet, nil, size)
	for i := 0; i < size+7; i++ {
		rec.Record(Row{Source: SourceUI, Actor: "ui", Action: "ui.session", Outcome: OutcomeOK})
	}
	if rec.Dropped() != 7 {
		t.Fatalf("dropped = %d, want 7", rec.Dropped())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Run must still flush everything queued
	rec.Run(ctx)
	if rec.Written() != size {
		t.Fatalf("written = %d, want %d", rec.Written(), size)
	}
}

func TestSummaryAndPrune(t *testing.T) {
	db := openDB(t)
	now := time.Date(2026, 10, 18, 15, 0, 0, 0, time.UTC)
	rec := NewRecorder(db, nil, quiet, func() time.Time { return now })
	add := func(at time.Time, action, outcome string) {
		rec.Record(Row{TS: at, Source: SourceMCP, Actor: "alice", Action: action, Outcome: outcome})
	}
	add(now.Add(-time.Hour), "render_document", OutcomeOK)
	add(now.Add(-2*time.Hour), "render_document", OutcomeErr)
	add(now.Add(-3*time.Hour), "ui.render", OutcomeOK)
	add(now.Add(-16*time.Hour), "render_document", OutcomeOK) // yesterday (UTC), within 24 h
	add(now.Add(-40*24*time.Hour), "get_document", OutcomeOK)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec.Run(ctx)
	s, err := Summary(context.Background(), db, now)
	if err != nil {
		t.Fatal(err)
	}
	if s.RendersToday != 2 || s.Calls24h != 4 || s.Errors24h != 1 || s.ErrorRate24h != 0.25 {
		t.Fatalf("summary %+v", s)
	}
	n, err := Prune(context.Background(), db, now.Add(-720*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	empty := openDB(t)
	if s, err := Summary(context.Background(), empty, now); err != nil || s.ErrorRate24h != 0 {
		t.Fatalf("empty summary %+v %v", s, err)
	}
}

func TestBroadcasterLimitsAndClose(t *testing.T) {
	b := NewBroadcaster()
	var subs []*Subscription
	for i := 0; i < 3; i++ {
		s, err := b.Subscribe("s1", 3)
		if err != nil {
			t.Fatal(err)
		}
		subs = append(subs, s)
	}
	if _, err := b.Subscribe("s1", 3); !errors.Is(err, ErrTooManyStreams) {
		t.Fatalf("4th stream: %v", err)
	}
	other, err := b.Subscribe("s2", 3)
	if err != nil {
		t.Fatal(err)
	}
	b.CloseKey("s1")
	for _, s := range subs {
		<-s.Done
	}
	b.Unsubscribe(other)
	b.Unsubscribe(other) // idempotent
	last, _ := b.Subscribe("s3", 3)
	b.Close()
	<-last.Done
	if _, err := b.Subscribe("s4", 3); !errors.Is(err, ErrClosed) {
		t.Fatalf("after close: %v", err)
	}
	if b.Len() != 0 {
		t.Fatalf("len = %d", b.Len())
	}
}
