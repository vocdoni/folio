// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/vocdoni/folio/internal/config"
	"github.com/vocdoni/folio/internal/review"
)

type fakeReviewer struct{}

func (fakeReviewer) Review(context.Context, []review.Page, review.Info) ([]review.Finding, error) {
	return nil, nil
}

func TestNewReviewer(t *testing.T) {
	const key = "sk-or-unit-test-key-0123456789"
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	load := func(env map[string]string) *config.Config {
		cfg, err := config.Load(func(k string) (string, bool) { v, ok := env[k]; return v, ok }, config.ModeRender)
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}

	rv, err := newReviewer(load(nil), nil, log)
	if err != nil || rv != nil {
		t.Fatalf("no key: got %v, %v; want a disabled reviewer", rv, err)
	}

	rv, err = newReviewer(load(map[string]string{"FOLIO_OPENROUTER_API_KEY": key, "FOLIO_REVIEW_MODEL": "vendor/model-x"}), nil, log)
	or, ok := rv.(*review.OpenRouter)
	if err != nil || !ok || or.Model() != "vendor/model-x" {
		t.Fatalf("with a key: got %T %v, %v", rv, rv, err)
	}

	rv, err = newReviewer(load(map[string]string{"FOLIO_OPENROUTER_API_KEY": key}), fakeReviewer{}, log)
	if _, ok := rv.(fakeReviewer); err != nil || !ok {
		t.Fatalf("an injected reviewer must win: %T %v", rv, err)
	}
	if strings.Contains(logs.String(), key) {
		t.Fatal("the OpenRouter API key reached the logs")
	}
	if !strings.Contains(logs.String(), "vendor/model-x") {
		t.Fatal("enabling the reviewer must log the model")
	}
}
