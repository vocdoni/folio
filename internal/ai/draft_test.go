// SPDX-License-Identifier: AGPL-3.0-only

package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/vocdoni/folio/internal/components"
	"github.com/vocdoni/folio/internal/model"
)

var draftIn = DraftInput{Description: "A service agreement with fees and signatures.", Brands: []string{"acme"}, Layouts: []string{"report", "proposal"},
	Components: components.Names, Languages: []string{"en", "es"}}

// draftAnswerJSON is a valid answer in the new shape; tweak edits it.
func draftAnswerJSON(tweak func(m map[string]any)) string {
	m := map[string]any{
		"id": "service-agreement", "name": "Service agreement", "description": "A recurring service.", "category": "agreement",
		"brand_id": "acme", "languages": []string{"en"},
		"layout": map[string]any{"base": "report", "options": map[string]any{"cover": true, "toc": false, "page_size": "a4", "heading_numbering": true}},
		"strict": false,
		"sections": []any{
			map[string]any{"title": "Services", "required": true, "aliases": []string{}, "hint": "", "component": ""},
			map[string]any{"title": "Fees", "required": true, "aliases": []string{"Price"}, "hint": "", "component": "budget"},
			map[string]any{"title": "Signatures", "required": false, "aliases": []string{}, "hint": "", "component": "signatures"},
		},
		"components": []string{"budget", "signatures", "info"},
		"metadata_fields": []any{
			map[string]any{"name": "term", "type": "string", "enum": []string{}, "required": true},
			map[string]any{"name": "start_date", "type": "date", "enum": []string{}, "required": false},
			map[string]any{"name": "tier", "type": "enum", "enum": []string{"basic", " premium ", "basic"}, "required": false},
			map[string]any{"name": "client", "type": "string", "enum": []string{}, "required": true},  // core: required by the template
			map[string]any{"name": "author", "type": "string", "enum": []string{}, "required": false}, // core: skipped
		},
		"body_skeleton": "# Services\n\nTODO\n\n# Fees\n\n::: folio-budget\n:::\n",
		"sample": map[string]any{
			"markdown": "# Services\n\nNetwork care.\n\n# Fees\n\n::: folio-budget\n:::\n",
			"metadata": map[string]any{"title": "Network care", "date": "2026-10-18", "language": "en",
				"fields": []any{map[string]any{"name": "term", "value": "12 months"}, map[string]any{"name": "client", "value": "ACME Corp"}}},
			"data": map[string]any{"budget": map[string]any{"items": []any{map[string]any{"description": "Care", "amount": "€5,400.00"}}}},
		},
	}
	if tweak != nil {
		tweak(m)
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// TestComponentExamplesAreValid: the examples shown to the model validate
// against the component schemas.
func TestComponentExamplesAreValid(t *testing.T) {
	for _, n := range components.Names {
		ex, ok := componentExamples[n]
		if n == "info" {
			if ok {
				t.Fatal("info takes no data")
			}
			continue
		}
		raw, _ := components.Schema(n)
		doc, _ := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		c := jsonschema.NewCompiler()
		if err := c.AddResource("c.json", doc); err != nil {
			t.Fatal(err)
		}
		sch, err := c.Compile("c.json")
		if err != nil {
			t.Fatal(err)
		}
		v, _ := jsonschema.UnmarshalJSON(strings.NewReader(ex))
		if err := sch.Validate(v); err != nil {
			t.Errorf("%s example: %v", n, err)
		}
	}
}

// TestDraftTemplateBuildsTheSchemas: the answer schema lists the keys in
// order with the component schemas under sample.data (not strict); the
// prompt shows every component's schema and example; Folio writes the
// metadata_schema from metadata_fields and the data_schema from the
// components.
func TestDraftTemplateBuildsTheSchemas(t *testing.T) {
	c, s := newScripted(t, draftAnswerJSON(nil))
	var checked model.Template
	d, err := c.DraftTemplate(context.Background(), draftIn, func(_ context.Context, tpl model.Template) ([]model.Finding, bool, error) {
		checked = tpl
		return nil, true, nil
	}, func(string) {})
	if err != nil || !d.OK || d.Repaired || len(s.requests) != 1 {
		t.Fatalf("draft %+v %v (%d requests)", d, err, len(s.requests))
	}
	name, strict, schema := s.schemaOf(t, 0)
	if name != "template_draft" || strict || s.requests[0]["max_tokens"] != float64(4096) {
		t.Fatalf("request %s strict=%v max_tokens=%v", name, strict, s.requests[0]["max_tokens"])
	}
	var top struct {
		Required []string `json:"required"`
	}
	_ = json.Unmarshal([]byte(schema), &top)
	if want := "id name description category brand_id languages layout strict sections components metadata_fields body_skeleton sample"; strings.Join(top.Required, " ") != want {
		t.Fatalf("key order %v", top.Required)
	}
	for _, part := range []string{
		`"layout":{"type":"object","properties":{"base":{"type":"string","enum":["report","proposal"]},"options":{"type":"object","properties":{"cover":{"type":"boolean"},"toc":{"type":"boolean"},"page_size":{"type":"string","enum":["a4","letter"]},"heading_numbering":{"type":"boolean"}}`,
		`"metadata_fields":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"type":{"type":"string","enum":["string","date","enum"]},"enum":{"type":"array","items":{"type":"string"}},"required":{"type":"boolean"}}`,
		`"sample":{"type":"object","properties":{"markdown":{"type":"string"},"metadata":{"type":"object","properties":{"title":{"type":"string"},"date":{"type":"string"},"language":{"type":"string","enum":["en","es"]},"fields":`,
		`"data":{"type":"object","description":"the data of the components the template uses (leave out the others)","properties":{"budget":{"type":"object"`,
		`"signatures":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"role":{"type":"string"},"organization":{"type":"string"}},"required":["name"],"additionalProperties":false}}},"required":[],"additionalProperties":false}`,
	} {
		if !strings.Contains(schema, part) {
			t.Fatalf("draft schema lacks %s\nin %s", part, schema)
		}
	}
	prompt := string(mustJSON(s.requests[0]["messages"]))
	for _, n := range []string{"budget", "milestones", "deliverables", "signatures"} {
		if !strings.Contains(prompt, `- `+n+`: schema {`) || !strings.Contains(prompt, strings.ReplaceAll(componentExamples[n], `"`, `\"`)) {
			t.Fatalf("the prompt lacks the %s schema or example", n)
		}
	}

	if string(checked.MetadataSchema) != `{"type":"object","properties":{"term":{"type":"string","maxLength":300},"start_date":{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"},"tier":{"type":"string","enum":["basic","premium"]},"client":{"type":"string","minLength":1}},"required":["term","client"]}` {
		t.Fatalf("metadata_schema %s", checked.MetadataSchema)
	}
	if string(checked.DataSchema) != `{"type":"object","properties":{"budget":{"$ref":"folio:component/budget"},"signatures":{"$ref":"folio:component/signatures"}},"required":["budget"]}` {
		t.Fatalf("data_schema %s", checked.DataSchema)
	}
	if string(checked.Sample.Metadata) != `{"client":"ACME Corp","date":"2026-10-18","language":"en","term":"12 months","title":"Network care"}` ||
		string(checked.Sample.Data) != `{"budget":{"items":[{"amount":"€5,400.00","description":"Care"}]}}` {
		t.Fatalf("sample %s %s", checked.Sample.Metadata, checked.Sample.Data)
	}
	if string(checked.Layout.Options) != `{"cover":true,"toc":false,"page_size":"a4","heading_numbering":true}` || checked.Layout.Typst != nil ||
		strings.Join(checked.Components, ",") != "budget,signatures,info" || len(checked.Sections) != 3 || checked.Sections[1].Component != "budget" {
		t.Fatalf("spec %+v", checked)
	}
}

// TestDraftTemplateRepairQuotesAPreview: a draft with problems gets one
// repair round with the problems and at most RepairPreview bytes of the
// answer.
func TestDraftTemplateRepairQuotesAPreview(t *testing.T) {
	long := draftAnswerJSON(func(m map[string]any) {
		m["metadata_fields"] = []any{map[string]any{"name": "tier", "type": "enum", "enum": []string{}, "required": false},
			map[string]any{"name": "Bad Name", "type": "string", "enum": []string{}, "required": false}}
		m["body_skeleton"] = strings.Repeat("# Section\n\nTODO\n\n", 400)
	})
	c, s := newScripted(t, long, draftAnswerJSON(nil))
	calls := 0
	d, err := c.DraftTemplate(context.Background(), draftIn, func(context.Context, model.Template) ([]model.Finding, bool, error) {
		calls++
		return nil, true, nil
	}, func(string) {})
	if err != nil || !d.OK || !d.Repaired || calls != 1 || len(s.requests) != 2 {
		t.Fatalf("draft %+v %v (%d checks, %d requests)", d, err, calls, len(s.requests))
	}
	m := messagesOf(s.requests[1])
	prev, _ := m[2]["content"].(string)
	if len(m) != 4 || m[2]["role"] != "assistant" || len(prev) > RepairPreview+len("…") || !strings.HasPrefix(long, strings.TrimSuffix(prev, "…")) {
		t.Fatalf("repair round: %d messages, preview of %d bytes", len(m), len(prev))
	}
	ask := fmt.Sprint(m[3]["content"])
	if !strings.Contains(ask, "metadata_fields[0] (tier): type enum needs at least one choice") || !strings.Contains(ask, "metadata_fields[1] (Bad Name): the name must be") {
		t.Fatalf("repair problems: %s", ask)
	}
}

// TestDraftTemplateUnusableAnswers: a cut-off draft is not sent back in the
// repair round; when the repair is unusable too the job fails with a clear,
// retryable unavailable error; when only the repair is unusable the first
// draft is kept with its findings.
func TestDraftTemplateUnusableAnswers(t *testing.T) {
	ok := func(context.Context, model.Template) ([]model.Finding, bool, error) { return nil, true, nil }
	c, s := newScripted(t, answerCutOff, draftAnswerJSON(nil))
	if d, err := c.DraftTemplate(context.Background(), draftIn, ok, func(string) {}); err != nil || !d.OK || !d.Repaired {
		t.Fatalf("draft %+v %v", d, err)
	}
	if m := messagesOf(s.requests[1]); len(m) != 3 || m[2]["role"] != "user" || !strings.Contains(fmt.Sprint(m[2]["content"]), "cut off") {
		t.Fatalf("the repair round must not quote a cut-off answer: %s", mustJSON(m))
	}

	c, _ = newScripted(t, "{not json", answerCutOff)
	_, err := c.DraftTemplate(context.Background(), draftIn, ok, func(string) {})
	if me, isME := errors.AsType[*model.Error](err); !isME || me.Code != model.ErrUnavailable || !me.Retryable || !strings.Contains(me.Message, "cut off") {
		t.Fatalf("two unusable answers: %#v", err)
	}

	finding := model.Finding{Code: model.CodeSchemaViolation, Severity: model.SeverityError, Path: "/sample", Message: "the sample does not render"}
	c, _ = newScripted(t, draftAnswerJSON(nil), "{not json")
	d, err := c.DraftTemplate(context.Background(), draftIn, func(context.Context, model.Template) ([]model.Finding, bool, error) {
		return []model.Finding{finding}, false, nil
	}, func(string) {})
	if err != nil || d.OK || !d.Repaired || d.Spec.ID != "service-agreement" || len(d.Findings) != 1 || d.Findings[0] != finding {
		t.Fatalf("draft %+v %v", d, err)
	}
}

// TestServiceErrors: upstream failures become clear, retryable unavailable
// errors (never internal).
func TestServiceErrors(t *testing.T) {
	c, err := New(Config{APIKey: testKey, BaseURL: "http://127.0.0.1:1"}) // nothing listens on port 1
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.SuggestBrand(context.Background(), BrandInput{Fonts: []string{"Inter"}}, func(string) {})
	if me, ok := errors.AsType[*model.Error](err); !ok || me.Code != model.ErrUnavailable || !me.Retryable || !strings.HasPrefix(me.Message, "the AI service failed: ") {
		t.Fatalf("connection refused: %#v", err)
	}
	if got := serviceError(ErrNotJSON).Error(); !strings.Contains(got, "not the expected JSON") {
		t.Fatalf("not JSON: %s", got)
	}
	if !errors.Is(serviceError(context.Canceled), context.Canceled) {
		t.Fatal("cancellation must pass through")
	}
}
