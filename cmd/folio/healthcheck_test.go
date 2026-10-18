// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthURL(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want string
	}{
		{nil, "http://127.0.0.1:8080/healthz"},
		{map[string]string{"PORT": "9123"}, "http://127.0.0.1:9123/healthz"},
		{map[string]string{"PORT": "9123", "FOLIO_ADDR": "127.0.0.2:7000"}, "http://127.0.0.2:7000/healthz"},
		{map[string]string{"FOLIO_ADDR": "0.0.0.0:7001"}, "http://127.0.0.1:7001/healthz"},
		{map[string]string{"FOLIO_ADDR": "[::]:7002"}, "http://127.0.0.1:7002/healthz"},
	}
	for _, c := range cases {
		got, err := healthURL(func(k string) string { return c.env[k] })
		if err != nil || got != c.want {
			t.Errorf("%v: got %q, %v; want %q", c.env, got, err, c.want)
		}
	}
	if _, err := healthURL(func(k string) string { return map[string]string{"FOLIO_ADDR": "nonsense"}[k] }); err == nil {
		t.Error("an address without a port must fail")
	}
}

func TestHealthcheckCmd(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	t.Setenv("FOLIO_ADDR", strings.TrimPrefix(srv.URL, "http://"))
	if code := healthcheckCmd(nil); code != 0 {
		t.Fatalf("healthy server: exit %d", code)
	}
	status = http.StatusServiceUnavailable
	if code := healthcheckCmd(nil); code != 1 {
		t.Fatalf("degraded server: exit %d, want 1", code)
	}
	if code := healthcheckCmd([]string{"extra"}); code != 2 {
		t.Fatalf("extra arguments: exit %d, want 2", code)
	}
	if err := probe("http://127.0.0.1:1/healthz", time.Second); err == nil {
		t.Fatal("a closed port must fail")
	}
}

func TestBuildVersionOverrides(t *testing.T) {
	old := buildVersion
	t.Cleanup(func() { buildVersion = old })
	buildVersion = "v9.9.9"
	if v := version(); v != "v9.9.9" {
		t.Fatalf("version() = %q", v)
	}
}
