// SPDX-License-Identifier: AGPL-3.0-only

package review

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testKey = "sk-or-test-key-0123456789"

func pagePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		img.Set(x, h/2, color.Black)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// stub is a fake OpenRouter chat completions endpoint.
type stub struct {
	mu      sync.Mutex
	status  int
	body    string
	delay   time.Duration
	auth    string
	path    string
	request map[string]any
	calls   int
}

func (s *stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.calls++
	s.auth, s.path = r.Header.Get("Authorization"), r.URL.Path
	b, _ := io.ReadAll(r.Body)
	s.request = nil
	_ = json.Unmarshal(b, &s.request)
	status, body, delay := s.status, s.body, s.delay
	s.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func answer(content string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop",
		"message": map[string]any{"role": "assistant", "content": content}}}})
	return string(b)
}

func newStub(t *testing.T, s *stub) *OpenRouter {
	t.Helper()
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	o, err := NewOpenRouter(OpenRouterConfig{APIKey: testKey, BaseURL: srv.URL + "/api/v1", MaxPages: 3, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestOpenRouterRequestAndFindings(t *testing.T) {
	s := &stub{status: 200, body: answer(`{"findings":[{"page":2,"severity":"warning","category":"table","message":"The budget table header is separated from its rows."}]}`)}
	o := newStub(t, s)
	pages := []Page{{Number: 1, PNG: pagePNG(t, 1191, 1684)}, {Number: 2, PNG: pagePNG(t, 2480, 3508)}}
	fs, err := o.Review(context.Background(), pages, Info{DocumentID: "d", Revision: 1, Title: "Secret title", Language: "es"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0] != (Finding{Page: 2, Severity: "warning", Category: "table", Message: "The budget table header is separated from its rows."}) {
		t.Fatalf("findings: %+v", fs)
	}
	s.mu.Lock()
	path, auth, req := s.path, s.auth, s.request
	s.mu.Unlock()
	if path != "/api/v1/chat/completions" || auth != "Bearer "+testKey {
		t.Fatalf("path %q auth %q", path, auth)
	}
	if req["model"] != DefaultModel || req["temperature"] != float64(0) {
		t.Fatalf("model/temperature: %v %v", req["model"], req["temperature"])
	}
	prov, _ := req["provider"].(map[string]any)
	if prov["data_collection"] != "deny" || prov["require_parameters"] != true {
		t.Fatalf("provider preferences must deny data collection: %v", prov)
	}
	rf, _ := req["response_format"].(map[string]any)
	js, _ := rf["json_schema"].(map[string]any)
	if rf["type"] != "json_schema" || js["strict"] != true || js["schema"] == nil {
		t.Fatalf("response_format: %v", rf)
	}
	schema, _ := json.Marshal(js["schema"])
	for _, c := range append(slices.Clone(Categories), Severities...) {
		if !strings.Contains(string(schema), `"`+c+`"`) {
			t.Errorf("schema lacks %q", c)
		}
	}
	msgs, _ := req["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" {
		t.Fatalf("messages: %v", msgs)
	}
	var widths []int
	var texts []string
	for _, p := range msgs[1].(map[string]any)["content"].([]any) {
		part := p.(map[string]any)
		switch part["type"] {
		case "text":
			texts = append(texts, part["text"].(string))
		case "image_url":
			u := part["image_url"].(map[string]any)["url"].(string)
			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(u, "data:image/png;base64,"))
			if !strings.HasPrefix(u, "data:image/png;base64,") || err != nil {
				t.Fatalf("image part is not a PNG data URI: %.40s", u)
			}
			cfg, err := png.DecodeConfig(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			widths = append(widths, cfg.Width)
		}
	}
	if !slices.Equal(widths, []int{1191, MaxPageWidth}) {
		t.Fatalf("image widths %v, want [1191 %d] (downscaled to ≤ %d px)", widths, MaxPageWidth, MaxPageWidth)
	}
	joined := strings.Join(texts, "\n")
	if !strings.Contains(joined, "Page 1:") || !strings.Contains(joined, "Page 2:") || !strings.Contains(joined, "es") {
		t.Fatalf("page labels and language missing: %q", joined)
	}
	if strings.Contains(joined, "Secret title") {
		t.Fatal("the document title must not be sent")
	}
}

func TestOpenRouterFailures(t *testing.T) {
	page := []Page{{Number: 1, PNG: pagePNG(t, 100, 140)}}
	cases := []struct {
		name   string
		s      *stub
		substr string
	}{
		{"http error with message", &stub{status: 429, body: `{"error":{"message":"Rate limit exceeded"}}`}, "HTTP 429: Rate limit exceeded"},
		{"http error without json", &stub{status: 502, body: `<html>bad gateway</html>`}, "HTTP 502"},
		{"error in a 200 body", &stub{status: 200, body: `{"error":{"message":"No endpoints found matching your data policy"}}`}, "data policy"},
		{"no choices", &stub{status: 200, body: `{"choices":[]}`}, "no choices"},
		{"cut off", &stub{status: 200, body: `{"choices":[{"finish_reason":"length","message":{"content":"{\"findings\":["}}]}`}, "cut off"},
		{"not json", &stub{status: 200, body: answer("The pages look great! SECRET-MODEL-TEXT")}, "not the expected JSON"},
		{"malformed body", &stub{status: 200, body: `{"choices":`}, "malformed"},
		{"timeout", &stub{status: 200, body: answer(`{"findings":[]}`), delay: 5 * time.Second}, "deadline exceeded"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := newStub(t, c.s)
			start := time.Now()
			fs, err := o.Review(context.Background(), page, Info{Language: "en"})
			if err == nil || fs != nil || !strings.Contains(err.Error(), c.substr) {
				t.Fatalf("got %v, %v; want an error containing %q", fs, err, c.substr)
			}
			if strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), "SECRET-MODEL-TEXT") {
				t.Fatalf("error leaks the key or the model answer: %v", err)
			}
			if time.Since(start) > 4*time.Second {
				t.Fatal("the per-call timeout was not applied")
			}
		})
	}
}

func TestOpenRouterFencedAnswerAndEmptyFindings(t *testing.T) {
	o := newStub(t, &stub{status: 200, body: answer("```json\n{\"findings\":[]}\n```")})
	fs, err := o.Review(context.Background(), []Page{{Number: 1, PNG: pagePNG(t, 10, 10)}}, Info{})
	if err != nil || fs == nil || len(fs) != 0 {
		t.Fatalf("got %v, %v; want an empty, non-nil slice", fs, err)
	}
}

func TestOpenRouterRefusesBadInput(t *testing.T) {
	if _, err := NewOpenRouter(OpenRouterConfig{APIKey: "  "}); err == nil {
		t.Fatal("an empty API key must be refused")
	}
	s := &stub{status: 200, body: answer(`{"findings":[]}`)}
	o := newStub(t, s)
	four := make([]Page, 4)
	for i := range four {
		four[i] = Page{Number: i + 1, PNG: pagePNG(t, 10, 10)}
	}
	if _, err := o.Review(context.Background(), four, Info{}); err == nil || !strings.Contains(err.Error(), "limit of 3") {
		t.Fatalf("too many pages: %v", err)
	}
	if _, err := o.Review(context.Background(), nil, Info{}); err == nil {
		t.Fatal("no pages must fail")
	}
	if _, err := o.Review(context.Background(), []Page{{Number: 1, PNG: []byte("not a png")}}, Info{}); err == nil {
		t.Fatal("a non-PNG page must fail")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls != 0 {
		t.Fatalf("invalid input must not reach OpenRouter (%d calls)", s.calls)
	}
}

func TestOpenRouterTrimsKey(t *testing.T) {
	s := &stub{status: 200, body: answer(`{"findings":[]}`)}
	srv := httptest.NewServer(s)
	defer srv.Close()
	o, err := NewOpenRouter(OpenRouterConfig{APIKey: " \t" + testKey + "\n", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.Review(context.Background(), []Page{{Number: 1, PNG: pagePNG(t, 10, 10)}}, Info{}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.auth != "Bearer "+testKey {
		t.Fatal("the API key must be trimmed before it is sent")
	}
}

func TestOpenRouterDoesNotFollowRedirects(t *testing.T) {
	var hit atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit.Store(true) }))
	defer other.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	o, err := NewOpenRouter(OpenRouterConfig{APIKey: testKey, BaseURL: redirect.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.Review(context.Background(), []Page{{Number: 1, PNG: pagePNG(t, 10, 10)}}, Info{}); err == nil || !strings.Contains(err.Error(), "HTTP 307") {
		t.Fatalf("redirect: %v", err)
	}
	if hit.Load() {
		t.Fatal("the request (and its API key) must not follow a redirect")
	}
}

func TestOpenRouterHonoursContext(t *testing.T) {
	o := newStub(t, &stub{status: 200, body: answer(`{"findings":[]}`), delay: 5 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := o.Review(ctx, []Page{{Number: 1, PNG: pagePNG(t, 10, 10)}}, Info{}); err == nil {
		t.Fatal("a cancelled context must fail the review")
	}
	if time.Since(start) > time.Second {
		t.Fatal("Review did not stop when the context was cancelled")
	}
}
