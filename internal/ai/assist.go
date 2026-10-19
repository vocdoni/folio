// SPDX-License-Identifier: AGPL-3.0-only

package ai

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vocdoni/folio/internal/model"
)

// Assistant limits (answer tokens and per-call timeouts). The document
// planner makes a small call to pick the template when the user did not,
// then fills that template's concrete schema.
const (
	brandTokens     = 600
	brandTimeout    = 30 * time.Second
	planPickTokens  = 200
	planPickTimeout = 20 * time.Second
	planFillTokens  = 3000
	planFillTimeout = 45 * time.Second
	draftTokens     = 4096
	draftTimeout    = 60 * time.Second

	sampleDataTokens  = 2048
	sampleDataTimeout = 30 * time.Second
)

// RepairPreview bounds the previous answer quoted back to the model in a
// repair round.
const RepairPreview = 2 << 10

// ask runs one JSON call and retries it once when the stream went wrong
// (ErrMalformed: whitespace runaway or stall).
func (c *Client) ask(ctx context.Context, req Request, out any) (string, error) {
	raw, err := c.JSON(ctx, req, out)
	if errors.Is(err, ErrMalformed) && ctx.Err() == nil {
		raw, err = c.JSON(ctx, req, out)
	}
	return raw, err
}

// unusable describes, for a repair round, an answer that cannot be used at
// all (not the expected JSON, cut off, malformed); ok is false for errors
// a repair round cannot fix (service failures, timeouts, cancellation).
func unusable(err error) (problem string, ok bool) {
	switch {
	case errors.Is(err, ErrNotJSON):
		return "the answer was not JSON matching the schema", true
	case errors.Is(err, errCutOff):
		return "the answer was cut off before it was complete (keep every text short)", true
	case errors.Is(err, ErrMalformed):
		return "the answer was malformed (long runs of spaces or no progress)", true
	}
	return "", false
}

// repairTurn returns msgs followed by the repair round: a preview (at most
// RepairPreview bytes) of the previous answer when it was readable JSON
// (raw != ""), and a user message listing the problems. An unreadable
// answer (malformed or cut off) is never sent back: it only confuses the
// model and costs tokens.
func repairTurn(msgs []Message, raw, ask string, problems []string) []Message {
	out := append([]Message(nil), msgs...)
	if raw != "" {
		out = append(out, Message{Role: "assistant", Content: preview(raw, RepairPreview)})
	}
	return append(out, Message{Role: "user", Content: ask + "\n- " + strings.Join(problems, "\n- ")})
}

// preview cuts s to at most n bytes on a rune boundary.
func preview(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// serviceError turns a client error into an agent-facing *model.Error:
// answers that were cut off or not the expected JSON and upstream failures
// are unavailable and retryable, with a plain message (client errors never
// carry content or the key). Context errors and *model.Error pass through.
func serviceError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*model.Error](err); ok {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	msg := "the AI service failed: " + strings.TrimPrefix(err.Error(), "ai: ")
	switch {
	case errors.Is(err, errCutOff):
		msg = "the model's answer was cut off before it was complete; try again with a shorter input"
	case errors.Is(err, ErrNotJSON):
		msg = "the model's answer was not the expected JSON; try again"
	}
	return &model.Error{Code: model.ErrUnavailable, Retryable: true, Message: msg, Err: err}
}

// gaveUp is the error after a failed repair round.
func gaveUp(what string, problems []string) error {
	return &model.Error{Code: model.ErrUnavailable, Retryable: true,
		Message: "the assistant could not produce a usable " + what + ": " + strings.Join(problems, "; ")}
}

// askChecked runs req (see ask), checks a decoded answer with check and
// gives the model one repair round when the answer is unusable or check
// finds problems. It returns the last answer and its remaining problems
// (nil answer when it was unusable); err is a serviceError.
func askChecked[T any](ctx context.Context, c *Client, req Request, again string, check func(*T) []string, step func(string)) (*T, []string, error) {
	try := func(req Request) (*T, string, []string, error) {
		var a T
		raw, err := c.ask(ctx, req, &a)
		if err != nil {
			p, ok := unusable(err)
			if !ok {
				return nil, "", nil, serviceError(err)
			}
			return nil, "", []string{p}, nil
		}
		return &a, raw, check(&a), nil
	}
	a, raw, problems, err := try(req)
	if err != nil || len(problems) == 0 {
		return a, nil, err
	}
	step("asking the model to fix its answer")
	req.Messages = repairTurn(req.Messages, raw, again, problems)
	a, _, problems, err = try(req)
	return a, problems, err
}
