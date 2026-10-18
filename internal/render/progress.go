// SPDX-License-Identifier: AGPL-3.0-only

package render

import "context"

// Pipeline progress steps, reported in this order. Validation-only requests
// stop after StepParse; StepDOCX is skipped when DOCX is not requested.
const (
	StepValidate = "validating input"
	StepQueue    = "waiting for a render slot"
	StepParse    = "parsing markdown"
	StepDOCX     = "writing docx"
	StepPDF      = "typesetting pdf"
	StepChecks   = "checking output"
)

// ProgressFunc receives one of the Step* names. It must not block for long:
// the pipeline calls it synchronously.
type ProgressFunc func(step string)

type progressKey struct{}

// WithProgress returns a context whose Render calls report their steps to fn.
func WithProgress(ctx context.Context, fn ProgressFunc) context.Context {
	return context.WithValue(ctx, progressKey{}, fn)
}

// reportProgress calls the context's ProgressFunc, if any.
func reportProgress(ctx context.Context, step string) {
	if fn, ok := ctx.Value(progressKey{}).(ProgressFunc); ok && fn != nil {
		fn(step)
	}
}
