// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/vocdoni/folio/internal/activity"
	"github.com/vocdoni/folio/internal/config"
)

// withOIDC turns SSO on in c (issuer is not contacted until a sign-in).
func withOIDC(c *config.Config, issuer string) *config.Config {
	c.OIDCIssuer, c.OIDCClientID, c.OIDCClientSecret = issuer, "folio", "fake-client-secret"
	c.OIDCAllowedDomains, c.OIDCName = []string{"example.com"}, "Sign in with Example ID"
	return c
}

func probeSession(t *testing.T, f *fixture) SessionOut {
	t.Helper()
	var s SessionOut
	w := f.do(http.MethodGet, "/ui/api/session", nil, nil)
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil || w.Code != http.StatusOK {
		t.Fatalf("session: %d %s", w.Code, w.Body)
	}
	return s
}

// TestSignInMethods: the session probe lists the configured methods, and
// the password route exists only with a password.
func TestSignInMethods(t *testing.T) {
	// Password only: unchanged, no SSO button.
	f := newFixture(t, nil)
	if s := probeSession(t, f); !s.Methods.Password || s.Methods.OIDC != nil || s.Authenticated {
		t.Fatalf("password only: %+v", s)
	}
	f.login()
	if s := probeSession(t, f); !s.Authenticated || s.Method != "password" || s.Email != "" {
		t.Fatalf("password session: %+v", s)
	}
	// Password and SSO.
	both := newFixture(t, func(o *Options) {
		o.Config = withOIDC(testConfig("https://folio.example.test"), "https://id.example.test")
	})
	if s := probeSession(t, both); !s.Methods.Password || s.Methods.OIDC == nil || s.Methods.OIDC.Name != "Sign in with Example ID" {
		t.Fatalf("password and SSO: %+v", s)
	}
	both.login()
	// SSO only: no password route at all, not even for an empty password.
	only := newFixture(t, func(o *Options) {
		o.Config = withOIDC(testConfig("https://folio.example.test"), "https://id.example.test")
		o.Config.UIPassword = ""
	})
	if s := probeSession(t, only); s.Methods.Password || s.Methods.OIDC == nil {
		t.Fatalf("SSO only: %+v", s)
	}
	for _, pw := range []string{"", testPassword} {
		w := only.do(http.MethodPost, "/ui/api/login", LoginIn{Password: pw}, nil)
		if w.Code != http.StatusNotFound || errorCode(t, w) != "not_found" || len(w.Result().Cookies()) != 0 {
			t.Fatalf("SSO only, password %q: %d %v %s", pw, w.Code, w.Header(), w.Body)
		}
	}
	if only.s.passwordOK("") {
		t.Fatal("an unset password must match nothing")
	}
}

// TestEmailActor: actions of an SSO session are recorded with its e-mail;
// password sessions keep the actor "ui".
func TestEmailActor(t *testing.T) {
	var rec *activity.Recorder
	f := newFixture(t, func(o *Options) {
		o.Config = withOIDC(testConfig("https://folio.example.test"), "https://id.example.test")
		rec = activity.NewRecorder(o.DB, nil, o.Logger, o.Now)
		o.Activity = rec
	})
	_, cookie, err := f.s.sessions.create(methodOIDC, "jane.doe@example.com")
	if err != nil {
		t.Fatal(err)
	}
	f.cookie = cookie
	if s := probeSession(t, f); !s.Authenticated || s.Method != "oidc" || s.Email != "jane.doe@example.com" {
		t.Fatalf("SSO session: %+v", s)
	}
	if w := f.do(http.MethodGet, "/ui/api/connection", nil, nil); w.Code != http.StatusOK {
		t.Fatalf("connection: %d %s", w.Code, w.Body)
	}
	if w := f.do(http.MethodPost, "/ui/api/logout", nil, nil); w.Code != http.StatusOK {
		t.Fatalf("logout: %d %s", w.Code, w.Body)
	}
	f.cookie = ""
	f.login()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec.Run(ctx)
	rows, _, err := activity.List(context.Background(), f.opts.DB, activity.Filter{})
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows %+v %v", rows, err)
	}
	for i, want := range []struct{ action, actor string }{{"ui.login", "ui"}, {"ui.logout", "jane.doe@example.com"}, {"ui.connection.reveal", "jane.doe@example.com"}} {
		if rows[i].Action != want.action || rows[i].Actor != want.actor {
			t.Errorf("row %d: %s by %s, want %s by %s", i, rows[i].Action, rows[i].Actor, want.action, want.actor)
		}
	}
	facets, err := activity.ListFacets(context.Background(), f.opts.DB)
	if err != nil || len(facets.Actors) != 2 || facets.Actors[0] != "jane.doe@example.com" {
		t.Fatalf("facets %+v %v", facets, err)
	}
}
