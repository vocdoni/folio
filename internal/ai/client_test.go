// SPDX-License-Identifier: AGPL-3.0-only

package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vocdoni/folio/internal/model"
)

const testKey = "sk-or-ai-unit-test-key-0123456789"

func TestClientRequestShape(t *testing.T) {
	var got map[string]any
	var raw []byte
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/proxy/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		auth = r.Header.Get("Authorization")
		raw, _ = io.ReadAll(r.Body)
		got = nil
		_ = json.Unmarshal(raw, &got)
		_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"`+"```json\\n{\\\"a\\\":1}\\n```"+`"}}]}`)
	}))
	defer srv.Close()
	c, err := New(Config{APIKey: " " + testKey + " ", Model: "vendor/m", BaseURL: srv.URL + "/proxy/v1/"})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		A int `json:"a"`
	}
	if _, err := c.JSON(context.Background(), Request{Messages: []Message{{Role: "user", Content: []Part{Text("hi"), PNG([]byte{1})}}},
		SchemaName: "x", Schema: Object(Prop("b", Str()), Prop("a", Int())), Strict: true}, &out); err != nil || out.A != 1 {
		t.Fatalf("JSON: %v %+v", err, out)
	}
	p, _ := got["provider"].(map[string]any)
	rf, _ := got["response_format"].(map[string]any)
	js, _ := rf["json_schema"].(map[string]any)
	if auth != "Bearer "+testKey || got["model"] != "vendor/m" || p["data_collection"] != "deny" || p["require_parameters"] != true ||
		p["sort"] != "throughput" || got["stream"] != true || got["max_tokens"] != float64(DefaultMaxTokens) ||
		js["name"] != "x" || js["strict"] != true {
		t.Fatalf("request %v auth %q", got, auth)
	}
	// The schema goes out in declaration order, required in the same order.
	if !bytes.Contains(raw, []byte(`"schema":{"type":"object","properties":{"b":{"type":"string"},"a":{"type":"integer"}},"required":["b","a"],"additionalProperties":false}`)) {
		t.Fatalf("schema order lost: %s", raw)
	}
	if _, err := c.Complete(context.Background(), Request{SchemaName: "x", MaxTokens: 777}); err != nil || got["max_tokens"] != float64(777) {
		t.Fatalf("per-call max_tokens: %v %v", err, got["max_tokens"])
	}
	var strict struct {
		B int `json:"b"`
	}
	if _, err := c.JSON(context.Background(), Request{SchemaName: "x"}, &strict); !errors.Is(err, ErrNotJSON) {
		t.Fatalf("unknown fields must fail: %v", err)
	}
}

func TestClientErrorsNeverLeak(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"status": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(429)
			_, _ = io.WriteString(w, `{"error":{"message":"Rate limit exceeded"}}`)
		},
		"redirect": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://elsewhere.example/", http.StatusTemporaryRedirect)
		},
		"huge": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(make([]byte, MaxResponseBytes+10)) },
		"hang": func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-time.After(3 * time.Second):
			case <-r.Context().Done():
			}
		},
	} {
		srv := httptest.NewServer(h)
		c, _ := New(Config{APIKey: testKey, BaseURL: srv.URL, Timeout: 500 * time.Millisecond})
		_, err := c.Complete(context.Background(), Request{SchemaName: "x"})
		srv.Close()
		if err == nil || strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), srv.URL) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := New(Config{APIKey: testKey, BaseURL: "ftp://x"}); err == nil {
		t.Fatal("non-http base URL accepted")
	}
	if _, err := New(Config{}); err == nil {
		t.Fatal("missing key accepted")
	}
}

// chunk is one streamed delta as OpenRouter sends it.
func chunk(content string) string {
	b, _ := json.Marshal(map[string]any{"id": "gen-1", "provider": "StubCloud", "model": "vendor/m",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": content}, "finish_reason": nil}}})
	return string(b)
}

// final is the last chunk: finish reason and usage.
func final(finish string) string {
	return `{"id":"gen-1","provider":"StubCloud","choices":[{"index":0,"delta":{"content":""},"finish_reason":"` + finish +
		`"}],"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19}}`
}

// streamStub serves an SSE stream; script gets send (one data event,
// flushed) and returns when the stream is over. cancelled reports whether
// the client went away before script finished.
func streamStub(t *testing.T, script func(r *http.Request, send func(string) bool)) (*httptest.Server, *atomic.Bool) {
	t.Helper()
	var cancelled atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		_, _ = io.WriteString(w, ": OPENROUTER PROCESSING\n\n")
		fl.Flush()
		script(r, func(data string) bool {
			if r.Context().Err() != nil {
				cancelled.Store(true)
				return false
			}
			_, _ = io.WriteString(w, "data: "+data+"\n\n")
			fl.Flush()
			return true
		})
		if r.Context().Err() != nil {
			cancelled.Store(true)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &cancelled
}

func newStreamClient(t *testing.T, srv *httptest.Server, timeout time.Duration, log *bytes.Buffer) *Client {
	t.Helper()
	cfg := Config{APIKey: testKey, Model: "vendor/m", BaseURL: srv.URL, Timeout: timeout}
	if log != nil {
		cfg.Logger = slog.New(slog.NewJSONHandler(log, nil))
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// pause waits d unless the request is cancelled.
func pause(r *http.Request, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-r.Context().Done():
		return false
	}
}

func TestClientStreamsAndLogsWithoutContent(t *testing.T) {
	srv, _ := streamStub(t, func(r *http.Request, send func(string) bool) {
		_ = send(chunk(`{"a":`)) && send(chunk(`"SECRET-ANSWER"}`)) && send(final("stop")) && send("[DONE]")
	})
	var log bytes.Buffer
	c := newStreamClient(t, srv, 5*time.Second, &log)
	var out struct {
		A string `json:"a"`
	}
	if _, err := c.JSON(context.Background(), Request{Messages: []Message{{Role: "user", Content: "SECRET-PROMPT"}}, SchemaName: "x",
		Schema: Object(Prop("a", Str()))}, &out); err != nil || out.A != "SECRET-ANSWER" {
		t.Fatalf("stream: %v %+v", err, out)
	}
	var rec map[string]any
	if err := json.Unmarshal(log.Bytes(), &rec); err != nil {
		t.Fatalf("one log record per call: %v %s", err, log.Bytes())
	}
	if rec["model"] != "vendor/m" || rec["provider"] != "StubCloud" || rec["prompt_tokens"] != float64(12) || rec["completion_tokens"] != float64(7) ||
		rec["finish_reason"] != "stop" || rec["outcome"] != "ok" || rec["schema"] != "x" {
		t.Fatalf("log record %v", rec)
	}
	if _, ok := rec["latency_ms"].(float64); !ok {
		t.Fatalf("latency_ms missing: %v", rec)
	}
	for _, secret := range []string{"SECRET-ANSWER", "SECRET-PROMPT", testKey} {
		if strings.Contains(log.String(), secret) {
			t.Fatalf("the log leaks %q: %s", secret, log.String())
		}
	}
}

func TestClientStreamSlowStartIsNotAStall(t *testing.T) {
	srv, _ := streamStub(t, func(r *http.Request, send func(string) bool) {
		// Time to the first token is bounded by the call timeout, not the
		// stall guard.
		_ = pause(r, 400*time.Millisecond) && send(chunk(`{"a":1}`)) && send(final("stop")) && send("[DONE]")
	})
	c := newStreamClient(t, srv, 5*time.Second, nil)
	c.stall = 150 * time.Millisecond
	if got, err := c.Complete(context.Background(), Request{SchemaName: "x"}); err != nil || got != `{"a":1}` {
		t.Fatalf("slow start: %q %v", got, err)
	}
}

func TestClientAbortsWhitespaceRunaway(t *testing.T) {
	srv, cancelled := streamStub(t, func(r *http.Request, send func(string) bool) {
		if !send(chunk(`{"a":`)) {
			return
		}
		for range 2000 { // ~10 s of whitespace unless the client hangs up
			if !send(chunk("\n    ")) || !pause(r, 5*time.Millisecond) {
				return
			}
		}
	})
	var log bytes.Buffer
	c := newStreamClient(t, srv, 30*time.Second, &log)
	start := time.Now()
	_, err := c.Complete(context.Background(), Request{SchemaName: "x"})
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("want ErrMalformed, got %v", err)
	}
	if me, ok := errors.AsType[*model.Error](err); !ok || me.Code != model.ErrUnavailable || !me.Retryable || me.Message != "the model produced a malformed answer" {
		t.Fatalf("malformed error shape: %#v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("the runaway was not cut short (%s)", time.Since(start))
	}
	waitFor(t, cancelled.Load, "the stream was not closed")
	if !strings.Contains(log.String(), `"outcome":"malformed"`) {
		t.Fatalf("log: %s", log.String())
	}
}

func TestClientAbortsStall(t *testing.T) {
	for name, script := range map[string]func(r *http.Request, send func(string) bool){
		"silence": func(r *http.Request, send func(string) bool) {
			_ = send(chunk(`{"a":`)) && pause(r, 10*time.Second)
		},
		"slow whitespace": func(r *http.Request, send func(string) bool) {
			if !send(chunk(`{"a":`)) {
				return
			}
			for range 200 {
				if !send(chunk(" ")) || !pause(r, 50*time.Millisecond) {
					return
				}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			srv, cancelled := streamStub(t, script)
			c := newStreamClient(t, srv, 30*time.Second, nil)
			c.stall = 300 * time.Millisecond
			start := time.Now()
			if _, err := c.Complete(context.Background(), Request{SchemaName: "x"}); !errors.Is(err, ErrMalformed) {
				t.Fatalf("want ErrMalformed, got %v", err)
			}
			if time.Since(start) > 3*time.Second {
				t.Fatalf("the stall was not cut short (%s)", time.Since(start))
			}
			waitFor(t, cancelled.Load, "the stream was not closed")
		})
	}
}

func TestClientDeadlineIsAClearUnavailableError(t *testing.T) {
	for name, script := range map[string]func(r *http.Request, send func(string) bool){
		"no answer": func(r *http.Request, _ func(string) bool) { pause(r, 10*time.Second) },
		"slow answer": func(r *http.Request, send func(string) bool) {
			for range 200 { // steady non-whitespace output: not a stall, just slow
				if !send(chunk("x")) || !pause(r, 50*time.Millisecond) {
					return
				}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := streamStub(t, script)
			var log bytes.Buffer
			c := newStreamClient(t, srv, 30*time.Second, &log)
			c.stall = 5 * time.Second
			start := time.Now()
			_, err := c.Complete(context.Background(), Request{SchemaName: "x", Timeout: 1200 * time.Millisecond})
			me, ok := errors.AsType[*model.Error](err)
			if !ok || me.Code != model.ErrUnavailable || !me.Retryable || me.Message != "the model vendor/m did not answer within 2 s" {
				t.Fatalf("deadline error: %#v", err)
			}
			if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrMalformed) {
				t.Fatalf("deadline error must wrap context.DeadlineExceeded only: %v", err)
			}
			if time.Since(start) > 4*time.Second {
				t.Fatalf("the per-call timeout was not applied (%s)", time.Since(start))
			}
			if !strings.Contains(log.String(), `"outcome":"timeout"`) {
				t.Fatalf("log: %s", log.String())
			}
		})
	}
}

func TestClientCallerCancellationIsNotAModelError(t *testing.T) {
	srv, _ := streamStub(t, func(r *http.Request, _ func(string) bool) { pause(r, 10*time.Second) })
	c := newStreamClient(t, srv, 30*time.Second, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := c.Complete(ctx, Request{SchemaName: "x"})
	if _, ok := errors.AsType[*model.Error](err); ok || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the caller's own deadline must surface as such: %v", err)
	}
}

func TestClientStreamFailures(t *testing.T) {
	cases := []struct {
		name   string
		events []string
		substr string
	}{
		{"error event", []string{chunk(`{"a"`), `{"error":{"code":502,"message":"Provider disconnected"},"choices":[{"index":0,"delta":{"content":""},"finish_reason":"error"}]}`}, "Provider disconnected"},
		{"cut off", []string{chunk(`{"a":`), final("length"), "[DONE]"}, "cut off"},
		{"ended early", []string{chunk(`{"a":`)}, "ended"},
		{"bad event", []string{"{not json"}, "malformed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := streamStub(t, func(_ *http.Request, send func(string) bool) {
				for _, e := range tc.events {
					send(e)
				}
			})
			c := newStreamClient(t, srv, 5*time.Second, nil)
			if _, err := c.Complete(context.Background(), Request{SchemaName: "x"}); err == nil || !strings.Contains(err.Error(), tc.substr) {
				t.Fatalf("want an error containing %q, got %v", tc.substr, err)
			}
		})
	}
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	for range 200 {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}
