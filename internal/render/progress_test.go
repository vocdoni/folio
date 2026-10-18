// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	"context"
	"slices"
	"sync"
	"testing"
)

func TestPipelineReportsProgressSteps(t *testing.T) {
	p, tpl, brand := testPipeline(t)
	var mu sync.Mutex
	var steps []string
	ctx := WithProgress(context.Background(), func(s string) {
		mu.Lock()
		steps = append(steps, s)
		mu.Unlock()
	})
	if _, err := p.Render(ctx, Request{Template: tpl, TemplateVersion: 1, Brand: brand, BrandVersion: 1, Input: input()}); err != nil {
		t.Fatal(err)
	}
	want := []string{StepValidate, StepQueue, StepParse, StepDOCX, StepPDF, StepChecks}
	if !slices.Equal(steps, want) {
		t.Fatalf("render steps = %q, want %q", steps, want)
	}
	steps = nil
	if _, err := p.Render(ctx, Request{Template: tpl, TemplateVersion: 1, Brand: brand, BrandVersion: 1, Input: input(), ValidateOnly: true}); err != nil {
		t.Fatal(err)
	}
	if want := []string{StepValidate, StepQueue, StepParse}; !slices.Equal(steps, want) {
		t.Fatalf("validate steps = %q, want %q", steps, want)
	}
	// Without a ProgressFunc nothing is reported and nothing panics.
	if _, err := p.Render(context.Background(), Request{Template: tpl, TemplateVersion: 1, Brand: brand, BrandVersion: 1, Input: input(), ValidateOnly: true}); err != nil {
		t.Fatal(err)
	}
}
