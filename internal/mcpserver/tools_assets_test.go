// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"strings"
	"testing"

	"github.com/vocdoni/folio/internal/model"
)

func TestUpdateAsset(t *testing.T) {
	e := newEnv(t)
	cs := connect(t, New(e.d), nil)
	id := uploadLogo(t, cs)

	var a AssetOut
	callOK(t, cs, "update_asset", map[string]any{"id": id, "name": "Northwind logo", "description": "Green mark"}, &a)
	if a.AssetID != id || a.Name != "Northwind logo" || a.Description != "Green mark" || a.MIME != "image/svg+xml" {
		t.Fatalf("update: %+v", a)
	}

	// Only the fields that are sent change.
	callOK(t, cs, "update_asset", map[string]any{"id": id, "description": "Updated"}, &a)
	if a.Name != "Northwind logo" || a.Description != "Updated" {
		t.Fatalf("description only: %+v", a)
	}
	callOK(t, cs, "update_asset", map[string]any{"id": id, "name": "Renamed"}, &a)
	if a.Name != "Renamed" || a.Description != "Updated" {
		t.Fatalf("name only: %+v", a)
	}
	// An empty description clears it (the output omits the field, so decode
	// into a fresh value).
	a = AssetOut{}
	callOK(t, cs, "update_asset", map[string]any{"id": id, "description": ""}, &a)
	if a.Description != "" || a.Name != "Renamed" {
		t.Fatalf("clear: %+v", a)
	}

	var list ListAssetsOut
	callOK(t, cs, "list_assets", map[string]any{"query": "renamed"}, &list)
	if len(list.Assets) != 1 || list.Assets[0].AssetID != id {
		t.Fatalf("list: %+v", list)
	}

	if er := callErr(t, cs, "update_asset", map[string]any{"id": id}); er.Code != model.ErrInvalidInput || !strings.Contains(er.Message, "name or description") {
		t.Fatalf("no fields: %+v", er)
	}
	if er := callErr(t, cs, "update_asset", map[string]any{"id": id, "name": " "}); er.Code != model.ErrInvalidInput {
		t.Fatalf("blank name: %+v", er)
	}
	if er := callErr(t, cs, "update_asset", map[string]any{"id": "missing", "name": "x"}); er.Code != model.ErrNotFound {
		t.Fatalf("unknown id: %+v", er)
	}
	callOK(t, cs, "delete_asset", map[string]any{"id": id}, nil)
	if er := callErr(t, cs, "update_asset", map[string]any{"id": id, "name": "x"}); er.Code != model.ErrNotFound {
		t.Fatalf("deleted: %+v", er)
	}
}
