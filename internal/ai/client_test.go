// SPDX-License-Identifier: AGPL-3.0-only

package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testKey = "sk-or-ai-unit-test-key-0123456789"

func TestClientRequestShape(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/proxy/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
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
		SchemaName: "x", Schema: map[string]any{"type": "object"}, Strict: true}, &out); err != nil || out.A != 1 {
		t.Fatalf("JSON: %v %+v", err, out)
	}
	p, _ := got["provider"].(map[string]any)
	rf, _ := got["response_format"].(map[string]any)
	js, _ := rf["json_schema"].(map[string]any)
	if auth != "Bearer "+testKey || got["model"] != "vendor/m" || p["data_collection"] != "deny" || p["require_parameters"] != true ||
		js["name"] != "x" || js["strict"] != true {
		t.Fatalf("request %v auth %q", got, auth)
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
