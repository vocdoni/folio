// SPDX-License-Identifier: AGPL-3.0-only

package assets

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/vocdoni/folio/internal/model"
)

func TestUpdateRenamesAndAudits(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	a, err := s.Upload(ctx, "alice", UploadRequest{Name: "logo.png", ContentBase64: base64.StdEncoding.EncodeToString(pngBytes(t, 8, 4))})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Update(ctx, "bob", a.ID, "  Northwind logo  ", "  Green mark  ")
	if err != nil || got.Name != "Northwind logo" || got.Description != "Green mark" {
		t.Fatalf("update: %+v %v", got, err)
	}
	if got.SHA256 != a.SHA256 || got.Size != a.Size || got.CreatedBy != "alice" {
		t.Fatalf("update changed the blob or provenance: %+v", got)
	}
	if again, _ := s.Get(ctx, a.ID); again.Name != "Northwind logo" || again.Description != "Green mark" {
		t.Fatalf("not stored: %+v", again)
	}
	if got, _, _ := s.List(ctx, "northwind", 10, ""); len(got) != 1 {
		t.Fatalf("renamed asset not found by its new name: %d", len(got))
	}
	// An empty description clears it.
	if got, err = s.Update(ctx, "bob", a.ID, "Northwind logo", ""); err != nil || got.Description != "" {
		t.Fatalf("clear description: %+v %v", got, err)
	}
	var n int
	if err := s.DB.R.QueryRowContext(ctx, `SELECT count(*) FROM audit WHERE action = 'update_asset' AND actor = 'bob' AND target_kind = 'asset' AND target_id = ? AND outcome = 'ok'`, a.ID).Scan(&n); err != nil || n != 2 {
		t.Fatalf("audit rows = %d, %v", n, err)
	}
}

func TestUpdateRejects(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	png := base64.StdEncoding.EncodeToString(pngBytes(t, 8, 4))
	a, _ := s.Upload(ctx, "alice", UploadRequest{Name: "logo.png", ContentBase64: png})
	deleted, _ := s.Upload(ctx, "alice", UploadRequest{Name: "old.png", ContentBase64: png})
	if err := s.Delete(ctx, "alice", deleted.ID); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		id, name, desc string
		code           model.ErrorCode
	}{
		"empty name": {a.ID, "   ", "", model.ErrInvalidInput},
		"long name":  {a.ID, strings.Repeat("n", 201), "", model.ErrInvalidInput},
		"long desc":  {a.ID, "ok", strings.Repeat("d", 501), model.ErrInvalidInput},
		"missing":    {"nope", "ok", "", model.ErrNotFound},
		"deleted":    {deleted.ID, "ok", "", model.ErrNotFound},
	} {
		var me *model.Error
		if _, err := s.Update(ctx, "bob", c.id, c.name, c.desc); !errors.As(err, &me) || me.Code != c.code {
			t.Errorf("%s: err = %v, want %s", name, err, c.code)
		}
	}
	// The limits count characters, not bytes.
	if _, err := s.Update(ctx, "bob", a.ID, strings.Repeat("é", 200), strings.Repeat("é", 500)); err != nil {
		t.Fatalf("200 runes: %v", err)
	}
	if got, _ := s.Get(ctx, deleted.ID); got.Name != "old.png" {
		t.Fatalf("deleted asset changed: %+v", got)
	}
}
