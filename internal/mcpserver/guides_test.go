// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	bundled "github.com/vocdoni/folio/assets"
	"github.com/vocdoni/folio/internal/brandkit"
	"github.com/vocdoni/folio/internal/components"
	"github.com/vocdoni/folio/internal/model"
)

func guide(t *testing.T, topic string) string {
	t.Helper()
	md, ok := Guide(topic)
	if !ok {
		t.Fatalf("guide %s missing", topic)
	}
	return md
}

func requireAll(t *testing.T, topic string, words []string) {
	t.Helper()
	md := guide(t, topic)
	for _, w := range words {
		if !strings.Contains(md, w) {
			t.Errorf("guide %s does not mention %q", topic, w)
		}
	}
}

func TestGuidesAreWellFormed(t *testing.T) {
	for _, topic := range Topics {
		md := guide(t, topic)
		title, summary := guideHead(md)
		if !strings.HasPrefix(md, "# ") || title == "" || len(summary) < 30 || strings.HasPrefix(summary, "#") {
			t.Errorf("%s: needs a '# Title' line followed by a one-line summary", topic)
		}
		if len(md) > 24<<10 {
			t.Errorf("%s: %d bytes; keep guides under 24 KiB", topic, len(md))
		}
		if strings.Count(md, "```")%2 != 0 {
			t.Errorf("%s: unbalanced code fences", topic)
		}
		if strings.Contains(md, "docs/specs") || strings.Contains(md, "TBD.") {
			t.Errorf("%s: references internal material", topic)
		}
	}
	if _, ok := Guide("../deps.go"); ok {
		t.Fatal("Guide must only serve known topics")
	}
}

// Finding and error codes are a public contract: every one is documented.
func TestTroubleshootingCoversEveryCode(t *testing.T) {
	codes := []string{
		model.CodeSchemaViolation, model.CodeInvalidFormat, model.CodeMarkdownTooLarge, model.CodeNestingTooDeep,
		model.CodeTableTooLarge, model.CodeMalformedTable, model.CodeMissingSection, model.CodeUnknownComponent,
		model.CodeComponentNotAllowed, model.CodeComponentNotEmpty, model.CodeMisplacedComponent, model.CodeComponentMissing,
		model.CodeComponentAttribute, model.CodeUnknownDeliverable,
		model.CodeInvalidImage, model.CodeMissingAsset, model.CodeInvalidLink, model.CodeRawContent, model.CodePlaceholder,
		model.CodePandocWarning, model.CodeTypstWarning,
		model.CodePDFInvalid, model.CodeTooManyPages, model.CodeBlankPage, model.CodeEdgeInk, model.CodeDOCXInvalid,
		model.CodeInvalidTypstImport,
	}
	for _, c := range []model.ErrorCode{model.ErrInvalidInput, model.ErrValidationFailed, model.ErrNotFound, model.ErrConflict,
		model.ErrArchived, model.ErrTooLarge, model.ErrBusy, model.ErrTimeout, model.ErrRenderFailed, model.ErrUnavailable, model.ErrInternal} {
		codes = append(codes, string(c))
	}
	var quoted []string
	for _, c := range codes {
		quoted = append(quoted, "`"+c+"`")
	}
	requireAll(t, "troubleshooting", quoted)
}

func TestComponentsGuideCoversSchemas(t *testing.T) {
	words := []string{"send every value exactly as it should appear", "`component_attribute`", "`unknown_deliverable`"}
	for _, name := range components.Names {
		words = append(words, "## "+name, "folio-"+name)
		raw, _ := components.Schema(name)
		var s struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Items      struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"items"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatal(err)
		}
		for p := range s.Properties {
			words = append(words, "`"+p+"`")
		}
		for p := range s.Items.Properties {
			words = append(words, "`"+p+"`")
		}
	}
	requireAll(t, "components", words)
	// Budget item and total-row fields live one level deeper.
	requireAll(t, "components", []string{"`description`", "`quantity`", "`unit`", "`unit_price`", "`discount`", "`amount`",
		"`label`", "`emphasis`"})
}

func jsonFields(v any) []string {
	var out []string
	rt := reflect.TypeOf(v)
	for i := 0; i < rt.NumField(); i++ {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out = append(out, "`"+name+"`")
		}
	}
	return out
}

func TestTemplatesGuideCoversSpecAndLabels(t *testing.T) {
	words := jsonFields(model.Template{})
	words = append(words, jsonFields(model.Section{})...)
	words = append(words, jsonFields(model.LayoutOptions{})...)
	for _, k := range brandkit.LabelKeys {
		words = append(words, "`"+k+"`")
	}
	bases, _ := bundled.BaseLayouts()
	for _, b := range bases {
		words = append(words, "`"+b+"`")
	}
	words = append(words, "folio:component/", "`title`", "`date`", "`language`", "`version_label`", "`confidentiality`")
	requireAll(t, "templates", words)
}

func TestTypstGuideCoversTheLayoutContract(t *testing.T) {
	words := []string{"folio-layout(meta: (:), brand: (:), labels: (:), options: (:), body)", `"/base/common.typ"`,
		"`colors.primary`", "`colors.secondary`", "`colors.accent`", "`colors.text`", "`colors.muted`", "`colors.background`",
		"`fonts.heading`", "`fonts.body`", "`fonts.mono`", "`logo.path`", "`logo.width_mm`", "`header.text`", "`header.show_logo`",
		"`footer.text`", "`footer.legal`", "`footer.page_numbers`", "`cover`", "`toc`", "`page_size`", "`heading_numbering`",
		"folio-page-setup", "folio-cover", "folio-toc", "folio-get", "`edge_ink`", "`blank_page`", "`invalid_typst_import`",
		bundled.MathFont, "get_capabilities.typst_packages"}
	bases, _ := bundled.BaseLayouts()
	for _, b := range bases {
		words = append(words, `"/base/`+b+`.typ"`)
	}
	words = append(words, bundled.FontFamilies...)
	requireAll(t, "typst-layouts", words)
}

func TestBrandsGuideCoversFields(t *testing.T) {
	words := []string{"`colors.primary`", "`colors.background`", "`fonts.heading`", "`logo.asset_id`", "`logo.width_mm`",
		"`header.show_logo`", "`footer.legal`", "`footer.page_numbers`", "`docx.font_fallbacks`", "update_brand", "archive_brand"}
	words = append(words, jsonFields(model.Brand{})...)
	words = append(words, bundled.FontFamilies...)
	requireAll(t, "brands", words)
}

func TestWorkflowGuideNamesEveryTool(t *testing.T) {
	tools := []string{"get_guide", "get_capabilities", "list_brands", "get_brand", "create_brand", "update_brand", "archive_brand",
		"list_templates", "get_template", "create_template", "update_template", "archive_template", "preview_template",
		"upload_asset", "list_assets", "update_asset", "delete_asset", "validate_document", "render_document", "list_documents", "get_document",
		"review_document", "publish_document", "delete_document", "restore_document"}
	var words []string
	for _, n := range tools {
		words = append(words, "`"+n+"`")
	}
	words = append(words, "idempotency_key", "expected_revision", "expected_version", "include_source", "confirm_published", "next_cursor")
	requireAll(t, "workflow", words)
}

func TestMarkdownGuideCoversTheDialect(t *testing.T) {
	requireAll(t, "markdown", []string{"asset:<asset_id>", "mailto:", "`TODO`", "`TBD`", "`FIXME`", "`XXX`", "`[...]`", "lorem ipsum",
		"`invalid_image_target`", "`invalid_link`", "`missing_section`", "markdown-raw_html-raw_tex-raw_attribute-yaml_metadata_block-citations-implicit_header_references",
		"## A complete example"})
}
