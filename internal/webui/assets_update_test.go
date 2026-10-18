// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/vocdoni/folio/internal/activity"
)

// TestAssetUpdate: PATCH renames, needs the CSRF header and a session,
// validates, refuses deleted assets and records an activity row.
func TestAssetUpdate(t *testing.T) {
	var rec *activity.Recorder
	sv := newServices(t, func(o *Options) {
		rec = activity.NewRecorder(o.DB, nil, o.Logger, o.Now)
		o.Activity = rec
	})
	a := decode[AssetOut](t, sv.upload("logo.svg", []byte(testSVG)), 200)

	body := map[string]string{"name": "Northwind logo", "description": "Green mark"}
	if w := sv.do(http.MethodPatch, "/ui/api/assets/"+a.ID, body, map[string]string{}); w.Code != http.StatusForbidden {
		t.Fatalf("PATCH without the CSRF header: %d", w.Code)
	}
	got := decode[AssetOut](t, sv.do(http.MethodPatch, "/ui/api/assets/"+a.ID, body, nil), 200)
	if got.Name != "Northwind logo" || got.Description != "Green mark" || got.URL != a.URL || got.SHA256 != a.SHA256 {
		t.Fatalf("patch: %+v", got)
	}
	list := decode[struct {
		Items []AssetOut `json:"items"`
	}](t, sv.do("GET", "/ui/api/assets?query=northwind", nil, nil), 200)
	if len(list.Items) != 1 || list.Items[0].Name != "Northwind logo" {
		t.Fatalf("list after rename: %+v", list)
	}
	if w := sv.do(http.MethodPatch, "/ui/api/assets/"+a.ID, map[string]string{"name": " ", "description": ""}, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("empty name: %d %s", w.Code, w.Body)
	}
	if w := sv.do(http.MethodPatch, "/ui/api/assets/"+a.ID, map[string]string{"name": "x", "description": strings.Repeat("d", 501)}, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("long description: %d", w.Code)
	}
	if w := sv.do(http.MethodPatch, "/ui/api/assets/"+a.ID, map[string]any{"name": "x", "extra": 1}, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", w.Code)
	}
	if w := sv.do(http.MethodPatch, "/ui/api/assets/nope", body, nil); w.Code != http.StatusNotFound {
		t.Fatalf("unknown id: %d", w.Code)
	}
	decode[map[string]any](t, sv.do("DELETE", "/ui/api/assets/"+a.ID, nil, nil), 200)
	if w := sv.do(http.MethodPatch, "/ui/api/assets/"+a.ID, body, nil); w.Code != http.StatusNotFound {
		t.Fatalf("deleted asset: %d", w.Code)
	}
	signedOut := newServices(t, nil)
	signedOut.cookie = ""
	if w := signedOut.do(http.MethodPatch, "/ui/api/assets/"+a.ID, body, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", w.Code)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec.Run(ctx)
	rows, _, err := activity.List(context.Background(), sv.opts.DB, activity.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, r := range rows {
		if r.Action == "ui.asset.update" && r.Outcome == "ok" {
			found++
			if r.TargetKind != "asset" || r.TargetID != a.ID {
				t.Fatalf("update row %+v", r)
			}
			if !reflect.DeepEqual(r.Detail, activity.Detail{}) {
				t.Fatalf("the activity detail must stay empty (names are request text): %+v", r.Detail)
			}
		}
	}
	if found == 0 {
		t.Fatalf("no ui.asset.update row in %+v", rows)
	}
}
