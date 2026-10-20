// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"fmt"
	"net/netip"
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

func TestWebUIAndAIDefaults(t *testing.T) {
	c, err := Load(env(map[string]string{"FOLIO_REVIEW_MODEL": "vendor/model-a"}), ModeRender)
	if err != nil {
		t.Fatal(err)
	}
	if c.UIEnabled() || c.UISessionTTL != 12*time.Hour || c.UIPreviewConcurrency != 1 || c.ActivityRetention != 720*time.Hour ||
		c.AIModel != DefaultAIModel || c.ReviewModel != "vendor/model-a" || c.AIDailyCalls != 200 || c.AISessionCalls != 30 ||
		c.OpenRouterBaseURL != "https://openrouter.ai/api/v1" || len(c.TrustedProxies) != len(DefaultTrustedProxies) {
		t.Fatalf("defaults: %+v", c)
	}
	if !c.TrustedProxies[0].Contains(netip.MustParseAddr("10.1.2.3")) {
		t.Fatal("private ranges must be trusted by default")
	}
	// The assistants no longer default to the reviewer's model.
	if c, err := Load(env(nil), ModeRender); err != nil || c.AIModel != "google/gemini-3.1-flash-lite" || c.ReviewModel != "google/gemma-4-31b-it" {
		t.Fatalf("model defaults: %v %+v", err, c)
	}
}

func TestWebUIVariables(t *testing.T) {
	const pw = "correct horse battery"
	c, err := Load(env(map[string]string{"FOLIO_UI_PASSWORD": pw, "FOLIO_UI_SESSION_TTL": "30m", "FOLIO_AI_MODEL": "vendor/model-b",
		"FOLIO_TRUSTED_PROXIES": "10.0.0.0/8, 2001:db8::1", "FOLIO_OPENROUTER_BASE_URL": "http://stub:8181/api/v1/",
		"FOLIO_AI_DAILY_CALLS": "5", "FOLIO_AI_SESSION_CALLS": "2", "FOLIO_UI_PREVIEW_CONCURRENCY": "2", "FOLIO_ACTIVITY_RETENTION": "48h"}), ModeRender)
	if err != nil {
		t.Fatal(err)
	}
	if !c.UIEnabled() || string(c.UIPassword) != pw || c.UISessionTTL != 30*time.Minute || c.AIModel != "vendor/model-b" ||
		c.OpenRouterBaseURL != "http://stub:8181/api/v1" || c.AIDailyCalls != 5 || c.AISessionCalls != 2 ||
		c.UIPreviewConcurrency != 2 || c.ActivityRetention != 48*time.Hour || len(c.TrustedProxies) != 2 ||
		c.TrustedProxies[1] != netip.MustParsePrefix("2001:db8::1/128") {
		t.Fatalf("%+v", c)
	}
	for _, s := range []string{fmt.Sprint(c.UIPassword), fmt.Sprintf("%v %+v %#v", *c, *c, c.UIPassword)} {
		if strings.Contains(s, pw) {
			t.Fatal("the UI password was printed")
		}
	}
	for name, m := range map[string]map[string]string{
		"short password": {"FOLIO_UI_PASSWORD": "short"},
		"bad proxies":    {"FOLIO_TRUSTED_PROXIES": "10.0.0.0/8,banana"},
		"bad base url":   {"FOLIO_OPENROUTER_BASE_URL": "openrouter.ai"},
		"creds in url":   {"FOLIO_OPENROUTER_BASE_URL": "https://u:p@openrouter.ai/api/v1"},
		"zero ttl":       {"FOLIO_UI_SESSION_TTL": "0s"},
	} {
		if _, err := Load(env(m), ModeRender); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	c, err = Load(env(map[string]string{"FOLIO_TRUSTED_PROXIES": "none"}), ModeRender)
	if err != nil || len(c.TrustedProxies) != 0 {
		t.Fatalf("none: %v %v", c.TrustedProxies, err)
	}
}

func TestInlineTemplateSettings(t *testing.T) {
	c, err := Load(env(nil), ModeRender)
	if err != nil {
		t.Fatal(err)
	}
	if c.DefaultBrand != "" || c.MaxInlineTemplateBytes != 256<<10 || c.MaxLayoutTypstBytes != 64<<10 {
		t.Fatalf("defaults: %q %d %d", c.DefaultBrand, c.MaxInlineTemplateBytes, c.MaxLayoutTypstBytes)
	}
	c, err = Load(env(map[string]string{"FOLIO_DEFAULT_BRAND": " acme ", "FOLIO_MAX_INLINE_TEMPLATE_BYTES": "1MiB",
		"FOLIO_MAX_LAYOUT_TYPST_BYTES": "4096"}), ModeRender)
	if err != nil {
		t.Fatal(err)
	}
	if c.DefaultBrand != "acme" || c.MaxInlineTemplateBytes != 1<<20 || c.MaxLayoutTypstBytes != 4096 {
		t.Fatalf("overrides: %q %d %d", c.DefaultBrand, c.MaxInlineTemplateBytes, c.MaxLayoutTypstBytes)
	}
	if _, err := Load(env(map[string]string{"FOLIO_MAX_LAYOUT_TYPST_BYTES": "64KB"}), ModeRender); err == nil || !strings.Contains(err.Error(), "FOLIO_MAX_LAYOUT_TYPST_BYTES") {
		t.Fatalf("bad size accepted: %v", err)
	}
}

func TestOIDCVariables(t *testing.T) {
	const secret = "fake-client-secret"
	base := map[string]string{"FOLIO_PUBLIC_URL": "https://folio.example.com", "FOLIO_TOKEN": tok,
		"FOLIO_OIDC_ISSUER": "https://id.example.com", "FOLIO_OIDC_CLIENT_ID": "folio", "FOLIO_OIDC_CLIENT_SECRET": secret,
		"FOLIO_OIDC_ALLOWED_DOMAINS": " Example.COM , example.org", "FOLIO_OIDC_ALLOWED_EMAILS": "Guest.User@Partner.example"}
	with := func(extra map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			if v == "" {
				delete(m, k)
			} else {
				m[k] = v
			}
		}
		return m
	}
	c, err := Load(env(base), ModeServe)
	if err != nil {
		t.Fatal(err)
	}
	if !c.UIEnabled() || c.UIPassword != "" || c.OIDCIssuer != "https://id.example.com" || c.OIDCClientID != "folio" ||
		string(c.OIDCClientSecret) != secret || c.OIDCName != DefaultOIDCName ||
		strings.Join(c.OIDCAllowedDomains, " ") != "example.com example.org" || strings.Join(c.OIDCAllowedEmails, " ") != "guest.user@partner.example" {
		t.Fatalf("%+v", c)
	}
	if s := fmt.Sprintf("%v %+v %#v", *c, *c, c.OIDCClientSecret); strings.Contains(s, secret) {
		t.Fatal("the client secret was printed")
	}
	// Either list alone is enough; the label is configurable.
	for _, m := range []map[string]string{
		with(map[string]string{"FOLIO_OIDC_ALLOWED_EMAILS": ""}),
		with(map[string]string{"FOLIO_OIDC_ALLOWED_DOMAINS": "", "FOLIO_OIDC_NAME": "Sign in with Example ID"}),
	} {
		if _, err := Load(env(m), ModeServe); err != nil {
			t.Fatalf("%v: %v", m, err)
		}
	}
	// http issuers only for a plain-http (local) Folio.
	if _, err := Load(env(with(map[string]string{"FOLIO_PUBLIC_URL": "http://localhost:8080", "FOLIO_OIDC_ISSUER": "http://localhost:5556"})), ModeServe); err != nil {
		t.Fatalf("local http issuer: %v", err)
	}
	for name, m := range map[string]map[string]string{
		"http issuer":     with(map[string]string{"FOLIO_OIDC_ISSUER": "http://id.example.com"}),
		"relative issuer": with(map[string]string{"FOLIO_OIDC_ISSUER": "id.example.com"}),
		"issuer query":    with(map[string]string{"FOLIO_OIDC_ISSUER": "https://id.example.com/?tenant=a"}),
		"wildcard domain": with(map[string]string{"FOLIO_OIDC_ALLOWED_DOMAINS": "*.example.com"}),
		"at domain":       with(map[string]string{"FOLIO_OIDC_ALLOWED_DOMAINS": "@example.com"}),
		"bad email":       with(map[string]string{"FOLIO_OIDC_ALLOWED_EMAILS": "not-an-email"}),
		"long name":       with(map[string]string{"FOLIO_OIDC_NAME": strings.Repeat("x", 65)}),
	} {
		if _, err := Load(env(m), ModeServe); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Every problem at once.
	_, err = Load(env(with(map[string]string{"FOLIO_OIDC_CLIENT_ID": "", "FOLIO_OIDC_CLIENT_SECRET": "",
		"FOLIO_OIDC_ALLOWED_DOMAINS": "", "FOLIO_OIDC_ALLOWED_EMAILS": ""})), ModeServe)
	for _, want := range []string{"FOLIO_OIDC_CLIENT_ID", "FOLIO_OIDC_CLIENT_SECRET", "FOLIO_OIDC_ALLOWED_DOMAINS or FOLIO_OIDC_ALLOWED_EMAILS"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
	// Without an issuer the other variables are ignored (unsetting the issuer turns SSO off).
	c, err = Load(env(with(map[string]string{"FOLIO_OIDC_ISSUER": "", "FOLIO_OIDC_ALLOWED_DOMAINS": "*"})), ModeServe)
	if err != nil || c.UIEnabled() || c.OIDCClientID != "" || len(c.OIDCAllowedDomains) != 0 {
		t.Fatalf("no issuer: %v %+v", err, c)
	}
}
