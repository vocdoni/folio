// SPDX-License-Identifier: AGPL-3.0-only

package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/vocdoni/folio/internal/components"
	"github.com/vocdoni/folio/internal/model"
)

// InlineName is the name of every inline template.
const InlineName = "Inline template"

// inlineCover is the layout.options.cover default of an inline template per
// base layout (the other options default to off and a4).
var inlineCover = map[string]bool{"report": true, "proposal": true}

// InlineTemplate turns spec (render_document.template) into an inline
// template: it enforces the size limits (too_large), fills the defaults
// (normalizeInline), names the brand (spec brand_id, else callBrand, else
// DefaultBrand), resolves it and validates the normalised spec. Invalid specs
// fail with validation_failed and findings under /template. It returns the
// normalised template (ID inline-<hash>) and its canonical spec JSON, the
// exact bytes of the template row; it writes nothing. metadata is the
// document's metadata (its language joins the default languages).
func (s *Service) InlineTemplate(ctx context.Context, spec model.Template, callBrand string, metadata json.RawMessage) (model.Template, []byte, error) {
	if err := s.typstLimit(spec, "/template"); err != nil {
		return model.Template{}, nil, err
	}
	var meta struct {
		Language string `json:"language"`
	}
	_ = json.Unmarshal(metadata, &meta) // invalid metadata is reported by the pipeline
	t := normalizeInline(spec, callBrand, meta.Language)
	if raw, err := model.Canonical(t); err != nil {
		return model.Template{}, nil, err
	} else if s.MaxInlineBytes > 0 && len(raw) > s.MaxInlineBytes {
		return model.Template{}, nil, &model.Error{Code: model.ErrTooLarge, Message: fmt.Sprintf(
			"the inline template is %d bytes as canonical JSON; the limit is %d (get_capabilities.limits.max_inline_template_bytes): store it with create_template instead",
			len(raw), s.MaxInlineBytes)}
	}
	if t.BrandID == "" {
		id, err := s.DefaultBrand(ctx)
		if err != nil {
			return model.Template{}, nil, err
		}
		t.BrandID = id
	}
	if _, _, err := s.ResolveBrand(ctx, t.BrandID, 0); err != nil {
		return model.Template{}, nil, err
	}
	id, err := inlineID(t)
	if err != nil {
		return model.Template{}, nil, err
	}
	t.ID = id
	if fs := s.ValidateTemplate(t); model.HasErrors(fs) {
		for i := range fs {
			fs[i].Path = "/template" + fs[i].Path
		}
		return model.Template{}, nil, &model.Error{Code: model.ErrValidationFailed, Details: fs,
			Message: "the inline template is invalid; fix the findings under /template (the document was not checked yet)"}
	}
	canon, err := model.Canonical(t)
	if err != nil {
		return model.Template{}, nil, err
	}
	return t, canon, nil
}

// normalizeInline fills the defaults of an inline spec. The result is
// deterministic, so equal specs hash to the same id: fixed id (set later),
// name, description, category, skeleton and sample; brand_id from the call;
// languages ["en"] plus the document language; base report with per-base
// options; every built-in component when components is absent or null; and
// null schemas, options and sections folded to their empty form.
func normalizeInline(spec model.Template, callBrand, language string) model.Template {
	t := spec
	t.SpecVersion = model.SpecVersion
	t.ID, t.Name, t.Description, t.Category, t.BodySkeleton = "", InlineName, "", "", ""
	t.Sample = model.Sample{Metadata: json.RawMessage(`{}`)}
	if t.BrandID == "" {
		t.BrandID = callBrand
	}
	if t.Languages == nil {
		t.Languages = []string{"en"}
		if langRE.MatchString(language) && language != "en" {
			t.Languages = append(t.Languages, language)
		}
	}
	if t.Layout.Base == "" {
		t.Layout.Base = "report"
	}
	t.Layout.Options = inlineOptions(t.Layout.Base, t.Layout.Options)
	if t.Components == nil {
		t.Components = slices.Clone(components.Names)
	}
	if t.Sections == nil {
		t.Sections = []model.Section{}
	}
	t.MetadataSchema, t.DataSchema = nullToNil(t.MetadataSchema), nullToNil(t.DataSchema)
	if len(t.Labels) == 0 {
		t.Labels = nil
	}
	return t
}

// inlineOptions merges the given layout.options over the per-base defaults
// and returns them complete. Options that do not decode are returned as
// given so ValidateTemplate reports them at /template/layout/options.
func inlineOptions(base string, raw json.RawMessage) json.RawMessage {
	o := model.LayoutOptions{Cover: inlineCover[base], PageSize: "a4"}
	if raw = nullToNil(raw); raw != nil {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&o); err != nil {
			return raw
		}
	}
	out, _ := json.Marshal(o)
	return out
}

func nullToNil(raw json.RawMessage) json.RawMessage {
	if t := bytes.TrimSpace(raw); len(t) == 0 || bytes.Equal(t, []byte("null")) {
		return nil
	}
	return raw
}

// inlineID is "inline-" plus the first 16 hex characters of the SHA-256 of
// the canonical normalised spec with an empty id.
func inlineID(t model.Template) (string, error) {
	t.ID = ""
	raw, err := model.Canonical(t)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return model.InlinePrefix + hex.EncodeToString(sum[:])[:16], nil
}

// SaveInlineInput names the stored template that SaveInline creates.
type SaveInlineInput struct {
	ID, Name, Description, Category string
	Sample                          model.Sample
	Note                            string
}

// SaveInline creates stored template in.ID (version 1) from inline template
// inlineID: the inline spec with the given id, name, description, category
// and sample, through CreateTemplate's checks (reserved id, validation,
// sample render, preview files, audit). Unlike CreateTemplate it accepts
// every sample warning (the inline template already rendered with them);
// the warnings are returned.
func (s *Service) SaveInline(ctx context.Context, actor, inlineID string, in SaveInlineInput) (*TemplateResult, error) {
	if !model.IsInlineID(inlineID) {
		return nil, &model.Error{Code: model.ErrInvalidInput, Message: fmt.Sprintf("%q is not an inline template id (inline-…)", inlineID)}
	}
	t, _, err := s.ResolveTemplate(ctx, inlineID, 1, true)
	if err != nil {
		return nil, err
	}
	t.ID, t.Name, t.Description, t.Category, t.Sample = in.ID, in.Name, in.Description, in.Category, in.Sample
	return s.putTemplate(ctx, actor, t, in.Note, true, 0, true)
}
