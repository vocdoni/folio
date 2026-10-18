// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

const tok = "0123456789abcdefghijklmnopqrstuv" // 32 chars

func TestDefaultsRenderMode(t *testing.T) {
	c, err := Load(env(nil), ModeRender)
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":8080" || c.RenderConcurrency != 2 || c.RenderTimeout != 60*time.Second || c.RenderMemory != 1<<30 ||
		c.MaxMarkdownBytes != 1<<20 || c.BlobGCGrace != time.Hour || c.PreviewPagesStored != 3 || c.DataDir != "/data" {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestServeModeRequiresTokenAndURL(t *testing.T) {
	_, err := Load(env(nil), ModeServe)
	if err == nil || !strings.Contains(err.Error(), "FOLIO_PUBLIC_URL") || !strings.Contains(err.Error(), "no token") {
		t.Fatalf("err = %v", err)
	}
	c, err := Load(env(map[string]string{"FOLIO_PUBLIC_URL": "https://folio.example.com/", "FOLIO_TOKENS": " alice:" + tok + " , bob: " + strings.ToUpper(tok) + " "}), ModeServe)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Tokens) != 2 || c.Tokens[1].Value != strings.ToUpper(tok) || c.PublicURL != "https://folio.example.com" {
		t.Fatalf("tokens = %v url=%s", c.Tokens, c.PublicURL)
	}
	if s := fmt.Sprint(c.Tokens[0]); strings.Contains(s, tok) {
		t.Fatal("token value printed")
	}
}

func TestTokenRules(t *testing.T) {
	base := map[string]string{"FOLIO_PUBLIC_URL": "https://x.example"}
	for name, extra := range map[string]map[string]string{
		"short":   {"FOLIO_TOKEN": "short"},
		"dup":     {"FOLIO_TOKENS": "a:" + tok + ",b:" + tok},
		"badname": {"FOLIO_TOKENS": "Alice:" + tok},
		"badurl":  {"FOLIO_TOKEN": tok, "FOLIO_PUBLIC_URL": "folio.example.com"},
		"gcgrace": {"FOLIO_TOKEN": tok, "FOLIO_BLOB_GC_GRACE": "2m"},
		"badsize": {"FOLIO_TOKEN": tok, "FOLIO_RENDER_MEMORY": "1GB"},
	} {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		if _, err := Load(env(m), ModeServe); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := Load(env(map[string]string{"FOLIO_PUBLIC_URL": "http://localhost:8080", "FOLIO_INSECURE_NO_AUTH": "true"}), ModeServe); err != nil {
		t.Fatalf("insecure no-auth: %v", err)
	}
}

func TestPortAndSizes(t *testing.T) {
	c, err := Load(env(map[string]string{"PORT": "9000", "FOLIO_RENDER_MEMORY": "512MiB", "FOLIO_MAX_ASSET_BYTES": "1048576"}), ModeRender)
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":9000" || c.RenderMemory != 512<<20 || c.MaxAssetBytes != 1<<20 {
		t.Fatalf("%+v", c)
	}
}
