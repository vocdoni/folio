// SPDX-License-Identifier: AGPL-3.0-only

package documents

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/vocdoni/folio/internal/model"
)

func TestSetReviewReplacesAndAudits(t *testing.T) {
	s, _, _ := setup(t)
	ctx := context.Background()
	res, err := s.Render(ctx, "alice", RenderRequest{TemplateID: "proposal", Input: model.DocumentInput{Markdown: "a",
		Metadata: json.RawMessage(`{"title":"T","date":"2026-10-18","language":"en"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, rv := range []string{`{"advisory":true,"findings":[{"page":1}]}`, `{"advisory":true,"findings":[]}`} {
		if err := s.SetReview(ctx, "bob", res.DocumentID, 1, json.RawMessage(rv)); err != nil {
			t.Fatal(err)
		}
	}
	d, err := s.Get(ctx, res.DocumentID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if string(d.Revision.Review) != `{"advisory":true,"findings":[]}` {
		t.Fatalf("review not replaced: %s", d.Revision.Review)
	}
	var n int
	if err := s.DB.R.QueryRow(`SELECT count(*) FROM audit WHERE action = 'review_document' AND actor = 'bob'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("audit rows = %d, %v", n, err)
	}
	var me *model.Error
	if err := s.SetReview(ctx, "bob", res.DocumentID, 7, json.RawMessage(`{}`)); !errors.As(err, &me) || me.Code != model.ErrNotFound {
		t.Fatalf("missing revision: %v", err)
	}
	if err := s.Trash(ctx, "bob", res.DocumentID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetReview(ctx, "bob", res.DocumentID, 1, json.RawMessage(`{}`)); !errors.As(err, &me) || me.Code != model.ErrConflict {
		t.Fatalf("trashed: %v", err)
	}
}
