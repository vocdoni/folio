// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vocdoni/folio/internal/activity"
)

func TestStatsActivityAndCapabilities(t *testing.T) {
	var rec *activity.Recorder
	f := newFixture(t, func(o *Options) {
		rec = activity.NewRecorder(o.DB, nil, o.Logger, o.Now)
		o.Activity = rec
	})
	f.login()
	for _, row := range []activity.Row{
		{Source: "mcp", Actor: "alice", Action: "render_document", TargetKind: "document", TargetID: "d1", Outcome: "ok"},
		{Source: "mcp", Actor: "bob", Action: "get_document", TargetKind: "document", TargetID: "d2", Outcome: "error", ErrorCode: "not_found"},
	} {
		rec.Record(row)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec.Run(ctx) // flushes the two rows and the login row
	var st StatsOut
	w := f.do(http.MethodGet, "/ui/api/stats", nil, nil)
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil || w.Code != 200 {
		t.Fatalf("stats: %d %s", w.Code, w.Body)
	}
	if st.RendersToday != 1 || st.Calls24h != 3 || st.Errors24h != 1 || st.Version != "test" || st.SandboxLevel != "rlimits-only" {
		t.Fatalf("stats %+v", st)
	}
	var act ActivityOut
	w = f.do(http.MethodGet, "/ui/api/activity?source=mcp&outcome=error", nil, nil)
	if err := json.Unmarshal(w.Body.Bytes(), &act); err != nil || len(act.Items) != 1 || act.Items[0].Actor != "bob" ||
		len(act.Actors) != 3 || act.NextBefore != 0 {
		t.Fatalf("activity: %s", w.Body)
	}
	w = f.do(http.MethodGet, "/ui/api/activity?limit=1", nil, nil)
	if err := json.Unmarshal(w.Body.Bytes(), &act); err != nil || len(act.Items) != 1 || act.NextBefore == 0 {
		t.Fatalf("paged activity: %s", w.Body)
	}
	for _, bad := range []string{"?limit=0", "?limit=500", "?before=x"} {
		if w := f.do(http.MethodGet, "/ui/api/activity"+bad, nil, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, w.Code)
		}
	}
	var caps CapabilitiesOut
	w = f.do(http.MethodGet, "/ui/api/capabilities", nil, nil)
	if err := json.Unmarshal(w.Body.Bytes(), &caps); err != nil || len(caps.Layouts) == 0 || len(caps.Components) != 5 ||
		caps.MCPURL != "https://folio.example.test/mcp" || caps.Fonts[0].Regular != "/ui/fonts/Inter-Regular.ttf" {
		t.Fatalf("capabilities: %s", w.Body)
	}
}

// TestActivityStream (B-AC3): rows reach the SSE stream; at most three
// streams per session; logout and shutdown end streams with "bye".
func TestActivityStream(t *testing.T) {
	bc := activity.NewBroadcaster()
	f := newFixture(t, func(o *Options) { o.Broadcaster = bc })
	f.s.heartbeat = 50 * time.Millisecond
	srv := httptest.NewServer(f.s)
	defer srv.Close()
	f.s.origin = srv.URL // the test server's origin
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login, _ := http.NewRequest(http.MethodPost, srv.URL+"/ui/api/login", strings.NewReader(`{"password":"`+testPassword+`"}`))
	login.Header.Set(HeaderUI, "1")
	resp, err := client.Do(login)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("login %v %v", resp, err)
	}
	resp.Body.Close()
	u, _ := url.Parse(srv.URL + "/ui/api/")
	if len(jar.Cookies(u)) != 1 {
		t.Fatal("no session cookie")
	}
	open := func() (*http.Response, *bufio.Reader) {
		t.Helper()
		resp, err := client.Get(srv.URL + "/ui/api/activity/stream")
		if err != nil {
			t.Fatal(err)
		}
		return resp, bufio.NewReader(resp.Body)
	}
	readEvent := func(br *bufio.Reader) (event, data string) {
		t.Helper()
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				t.Fatalf("stream ended: %v", err)
			}
			line = strings.TrimRight(line, "\n")
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			case line == "" && event != "":
				return event, data
			}
		}
	}
	r1, br1 := open()
	defer r1.Body.Close()
	if r1.StatusCode != 200 || r1.Header.Get("Content-Type") != "text/event-stream" || r1.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("stream headers: %d %v", r1.StatusCode, r1.Header)
	}
	r2, _ := open()
	defer r2.Body.Close()
	r3, _ := open()
	defer r3.Body.Close()
	for bc.Len() < 3 {
		time.Sleep(5 * time.Millisecond)
	}
	if r4, _ := open(); r4.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("4th stream: %d", r4.StatusCode)
	} else {
		r4.Body.Close()
	}
	bc.Publish(activity.Row{ID: 7, Source: "mcp", Actor: "alice", Action: "render_document", Outcome: "ok"})
	ev, data := readEvent(br1)
	var row activity.Row
	if ev != "activity" || json.Unmarshal([]byte(data), &row) != nil || row.ID != 7 || row.Action != "render_document" {
		t.Fatalf("event %q %s", ev, data)
	}
	bc.Close() // shutdown
	if ev, _ := readEvent(br1); ev != "bye" {
		t.Fatalf("shutdown event %q", ev)
	}
}

func TestLogoutEndsSessionStreams(t *testing.T) {
	bc := activity.NewBroadcaster()
	f := newFixture(t, func(o *Options) { o.Broadcaster = bc })
	f.login()
	sub, err := bc.Subscribe(keyOf(f.cookie), MaxStreamsPerSession)
	if err != nil {
		t.Fatal(err)
	}
	if w := f.do(http.MethodPost, "/ui/api/logout", nil, nil); w.Code != 200 {
		t.Fatalf("logout %d", w.Code)
	}
	select {
	case <-sub.Done:
	case <-time.After(time.Second):
		t.Fatal("logout must end the session's live streams")
	}
}
