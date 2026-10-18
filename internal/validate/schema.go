// SPDX-License-Identifier: AGPL-3.0-only

package validate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/vocdoni/folio/internal/components"
	"github.com/vocdoni/folio/internal/model"
)

// componentLoader serves "folio:component/<name>" and nothing else: no file,
// http or other loaders are registered, so schemas can never read server
// files or reach the network.
type componentLoader struct{}

func (componentLoader) Load(u string) (any, error) {
	name, ok := strings.CutPrefix(u, "folio:component/")
	if !ok {
		return nil, fmt.Errorf("only folio:component/<name> references can be resolved, not %q", u)
	}
	b, ok := components.Schema(name)
	if !ok {
		return nil, fmt.Errorf("unknown component schema %q", name)
	}
	return jsonschema.UnmarshalJSON(bytes.NewReader(b))
}

func newCompiler() *jsonschema.Compiler {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(jsonschema.SchemeURLLoader{"folio": componentLoader{}})
	c.AssertFormat()
	return c
}

// CoreMetadataSchema returns the core metadata schema for a template's
// languages (always combined with the template's metadata_schema).
func CoreMetadataSchema(languages []string) []byte {
	langs, _ := json.Marshal(languages)
	return []byte(`{
  "type": "object",
  "required": ["title", "date", "language"],
  "properties": {
    "title": {"type": "string", "minLength": 1, "maxLength": 300},
    "date": {"type": "string", "pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}$"},
    "language": {"enum": ` + string(langs) + `},
    "author": {"type": "string", "maxLength": 300},
    "client": {"type": "string", "maxLength": 300},
    "project": {"type": "string", "maxLength": 300},
    "reference": {"type": "string", "maxLength": 100},
    "version_label": {"type": "string", "maxLength": 50},
    "confidentiality": {"type": "string", "maxLength": 100}
  }
}`)
}

// Schemas are a template's compiled schemas.
type Schemas struct {
	metadata   *jsonschema.Schema
	data       *jsonschema.Schema // nil when the template has no data_schema
	components map[string]*jsonschema.Schema
}

func compile(c *jsonschema.Compiler, url string, raw []byte) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	if err := c.AddResource(url, doc); err != nil {
		return nil, err
	}
	return c.Compile(url)
}

// CompileSchemas compiles the effective metadata schema (core allOf
// template), the data schema and the component schemas. Errors are
// returned as findings on /metadata_schema or /data_schema.
func CompileSchemas(t model.Template) (*Schemas, []model.Finding) {
	c := newCompiler()
	s := &Schemas{components: map[string]*jsonschema.Schema{}}
	var fs []model.Finding
	bad := func(path string, err error) {
		fs = append(fs, model.Finding{Code: model.CodeSchemaViolation, Severity: model.SeverityError, Path: path,
			Message: "invalid JSON Schema: " + err.Error()})
	}
	core, err := compile(c, "folio:core-metadata", CoreMetadataSchema(t.Languages))
	if err != nil {
		bad("/languages", err)
		return nil, fs
	}
	s.metadata = core
	if len(bytes.TrimSpace(t.MetadataSchema)) > 0 {
		eff := []byte(`{"allOf":[{"$ref":"folio:core-metadata"},` + string(t.MetadataSchema) + `]}`)
		if m, err := compile(c, "folio:template-metadata", eff); err != nil {
			bad("/metadata_schema", err)
		} else {
			s.metadata = m
		}
	}
	if len(bytes.TrimSpace(t.DataSchema)) > 0 {
		if d, err := compile(c, "folio:template-data", t.DataSchema); err != nil {
			bad("/data_schema", err)
		} else {
			s.data = d
		}
	}
	for _, name := range components.Names {
		raw, _ := components.Schema(name)
		sc, err := compile(c, "folio:component-root/"+name, raw)
		if err != nil {
			bad("/components", err) // unreachable: embedded schemas are tested
			continue
		}
		s.components[name] = sc
	}
	return s, fs
}

var printer = message.NewPrinter(language.English)

func schemaFindings(err error, prefix string) []model.Finding {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return []model.Finding{{Code: model.CodeSchemaViolation, Severity: model.SeverityError, Path: prefix, Message: err.Error()}}
	}
	var out []model.Finding
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			p := prefix
			for _, tok := range e.InstanceLocation {
				p += "/" + strings.ReplaceAll(strings.ReplaceAll(tok, "~", "~0"), "/", "~1")
			}
			out = append(out, model.Finding{Code: model.CodeSchemaViolation, Severity: model.SeverityError, Path: p,
				Message: e.ErrorKind.LocalizedString(printer)})
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	return out
}

// ValidateMetadata validates decoded metadata (json.Number numbers) and
// checks that date is a real calendar date.
func (s *Schemas) ValidateMetadata(meta any) []model.Finding {
	if err := s.metadata.Validate(meta); err != nil {
		return schemaFindings(err, "/metadata")
	}
	m, _ := meta.(map[string]any)
	if d, _ := m["date"].(string); d != "" {
		if _, err := time.Parse("2006-01-02", d); err != nil {
			return []model.Finding{{Code: model.CodeSchemaViolation, Severity: model.SeverityError, Path: "/metadata/date",
				Message: fmt.Sprintf("%q is not a valid date (YYYY-MM-DD)", d)}}
		}
	}
	return nil
}

// ValidateData validates data against the template data schema and, for
// each component used, data.<component> against the component schema.
func (s *Schemas) ValidateData(data map[string]any, uses []components.Use) []model.Finding {
	var out []model.Finding
	if s.data != nil {
		if err := s.data.Validate(any(data)); err != nil {
			out = append(out, schemaFindings(err, "/data")...)
		}
	}
	for _, u := range uses {
		sc := s.components[u.Name]
		v, ok := data[u.Name]
		if u.Name == "info" || sc == nil {
			continue
		}
		if !ok {
			out = append(out, model.Finding{Code: model.CodeSchemaViolation, Severity: model.SeverityError, Path: "/data/" + u.Name,
				Message: fmt.Sprintf("data.%s is required by the folio-%s component", u.Name, u.Name)})
			continue
		}
		if err := sc.Validate(v); err != nil {
			out = append(out, schemaFindings(err, "/data/"+u.Name)...)
		}
	}
	return out
}
