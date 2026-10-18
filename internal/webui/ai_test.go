// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vocdoni/folio/internal/ai"
)

// aiStub is a stub OpenRouter: answers are queued per json_schema name;
// gate (when set) holds every answer until closed.
type aiStub struct {
	mu      sync.Mutex
	answers map[string][]string
	calls   map[string]int
	gate    chan struct{}
	reqs    []map[string]any
}

func (s *aiStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	var req map[string]any
	_ = json.Unmarshal(b, &req)
	rf, _ := req["response_format"].(map[string]any)
	js, _ := rf["json_schema"].(map[string]any)
	name, _ := js["name"].(string)
	s.mu.Lock()
	gate := s.gate
	s.reqs = append(s.reqs, req)
	if s.calls == nil {
		s.calls = map[string]int{}
	}
	s.calls[name]++
	q := s.answers[name]
	a := `{}`
	if len(q) > 0 {
		a = q[0]
		if len(q) > 1 {
			s.answers[name] = q[1:]
		}
	}
	s.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
	}
	out, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": a}}}})
	_, _ = w.Write(out)
}

func (s *aiStub) count(name string) int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls[name] }

// newAIServices is newServices with an AI client pointed at a stub.
func newAIServices(t *testing.T, stub *aiStub, tweak func(*Options)) *svc {
	t.Helper()
	srv := httptest.NewServer(stub)
	t.Cleanup(srv.Close)
	client, err := ai.New(ai.Config{APIKey: "sk-or-test-xxxxxxxxxxxxxxxx", BaseURL: srv.URL + "/api/v1", Model: "stub/model"})
	if err != nil {
		t.Fatal(err)
	}
	sv := newServices(t, func(o *Options) {
		o.AI = client
		if tweak != nil {
			tweak(o)
		}
	})
	t.Cleanup(sv.s.Close)
	return sv
}

// waitJob polls a job until it leaves queued/running.
func (sv *svc) waitJob(id string) JobOut {
	sv.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		j := decode[JobOut](sv.t, sv.do("GET", "/ui/api/ai/jobs/"+id, nil, nil), 200)
		if j.State == jobDone || j.State == jobError {
			return j
		}
		if time.Now().After(deadline) {
			sv.t.Fatalf("job %s still %s (%s)", id, j.State, j.Step)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func planJSON(meta, data map[string]any) string {
	m, _ := json.Marshal(meta)
	d, _ := json.Marshal(data)
	b, _ := json.Marshal(map[string]any{"template_id": "memo", "metadata_json": string(m), "data_json": string(d), "warnings": []string{}})
	return string(b)
}

func TestAIOffWithoutKey(t *testing.T) {
	sv := newServices(t, nil)
	st := decode[AIStatusOut](t, sv.do("GET", "/ui/api/ai/status", nil, nil), 200)
	if st.Enabled {
		t.Fatal("assistants enabled without a key")
	}
	if w := sv.do("POST", "/ui/api/ai/document-plan", DocumentPlanIn{Markdown: "# Hi"}, nil); w.Code != http.StatusServiceUnavailable || errorCode(t, w) != "unavailable" {
		t.Fatalf("plan without a key: %d %s", w.Code, w.Body)
	}
}

// TestDocumentPlanJob (B-AC5): an async job; values not in the Markdown are
// flagged inferred; the Markdown is never part of the result.
func TestDocumentPlanJob(t *testing.T) {
	stub := &aiStub{answers: map[string][]string{"document_plan": {planJSON(
		map[string]any{"title": "Hello memo", "date": "2026-10-18", "language": "en", "client": "Globex"}, map[string]any{})}}}
	sv := newAIServices(t, stub, nil)
	decode[map[string]any](t, sv.do("POST", "/ui/api/brands", BrandIn{Brand: brandSpec("acme")}, nil), 200)
	decode[TemplateSavedOut](t, sv.do("POST", "/ui/api/templates", TemplateIn{Template: templateSpec("memo", "acme")}, nil), 200)
	st := decode[AIStatusOut](t, sv.do("GET", "/ui/api/ai/status", nil, nil), 200)
	if !st.Enabled || st.Model != "stub/model" || !st.Vision || st.DailyRemaining != 200 || st.SessionRemaining != 30 {
		t.Fatalf("status %+v", st)
	}
	md := "# Summary\n\nHello memo, written 2026-10-18 for ACME."
	started := decode[JobStartedOut](t, sv.do("POST", "/ui/api/ai/document-plan", DocumentPlanIn{Markdown: md}, nil), http.StatusAccepted)
	j := sv.waitJob(started.JobID)
	if j.State != jobDone {
		t.Fatalf("job %+v", j)
	}
	raw, _ := json.Marshal(j.Result)
	var p ai.Plan
	_ = json.Unmarshal(raw, &p)
	if p.TemplateID != "memo" || len(p.Inferred) != 1 || p.Inferred[0] != "/metadata/client" || strings.Contains(string(raw), "Hello memo, written") {
		t.Fatalf("plan %s", raw)
	}
	if st := decode[AIStatusOut](t, sv.do("GET", "/ui/api/ai/status", nil, nil), 200); st.SessionRemaining != 29 || st.DailyRemaining != 199 {
		t.Fatalf("quota after one job %+v", st)
	}
}

// TestAIJobLimits: one running job per session (ai_busy), the per-session
// hourly quota (ai_quota), jobs are private to their session and cancelled
// on logout.
func TestAIJobLimits(t *testing.T) {
	stub := &aiStub{gate: make(chan struct{})}
	sv := newAIServices(t, stub, func(o *Options) {
		c := *o.Config
		c.AISessionCalls = 2
		o.Config = &c
	})
	decode[map[string]any](t, sv.do("POST", "/ui/api/brands", BrandIn{Brand: brandSpec("acme")}, nil), 200)
	first := decode[JobStartedOut](t, sv.do("POST", "/ui/api/ai/document-plan", DocumentPlanIn{Markdown: "# A"}, nil), http.StatusAccepted)
	if w := sv.do("POST", "/ui/api/ai/document-plan", DocumentPlanIn{Markdown: "# B"}, nil); w.Code != http.StatusTooManyRequests || errorCode(t, w) != "ai_busy" {
		t.Fatalf("second running job: %d %s", w.Code, w.Body)
	}
	other := newFixture(t, nil)
	other.s = sv.s
	other.login()
	if w := other.do("GET", "/ui/api/ai/jobs/"+first.JobID, nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("another session read the job: %d", w.Code)
	}
	for stub.count("document_plan") < 1 {
		time.Sleep(5 * time.Millisecond)
	}
	cookie := sv.cookie
	sv.do("POST", "/ui/api/logout", nil, nil)
	sv.cookie = cookie
	deadline := time.Now().Add(5 * time.Second)
	for {
		sv.s.jobs.mu.Lock()
		j := sv.s.jobs.jobs[first.JobID]
		state, apiErr := j.State, j.Error
		sv.s.jobs.mu.Unlock()
		if state == jobError {
			if apiErr.Code != "cancelled" {
				t.Fatalf("logout must cancel the job: %+v", apiErr)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job still %s after logout", state)
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(stub.gate)
	sv.login()
	decode[JobStartedOut](t, sv.do("POST", "/ui/api/ai/document-plan", DocumentPlanIn{Markdown: "# C"}, nil), http.StatusAccepted)
	for i := 0; i < 50; i++ { // wait for the job to finish (busy otherwise)
		if w := sv.do("POST", "/ui/api/ai/document-plan", DocumentPlanIn{Markdown: "# D"}, nil); w.Code == http.StatusTooManyRequests && errorCode(t, w) == "ai_quota" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the per-session quota was not enforced")
}

func TestQuotaDailyReset(t *testing.T) {
	clk := &testClock{t: time.Date(2026, 10, 18, 23, 0, 0, 0, time.UTC)}
	q := newQuotas(2, 10, clk.Now)
	if q.take("a") != nil || q.take("b") != nil {
		t.Fatal("first calls refused")
	}
	if err := q.take("c"); err == nil || !strings.Contains(err.Error(), "resets at 00:00") {
		t.Fatalf("daily limit: %v", err)
	}
	clk.Add(2 * time.Hour)
	if err := q.take("c"); err != nil {
		t.Fatalf("after midnight UTC: %v", err)
	}
}
