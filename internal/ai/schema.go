// SPDX-License-Identifier: AGPL-3.0-only

package ai

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Schema is a JSON Schema node for structured output whose JSON keeps
// object properties, and the required list, in declaration order.
//
// Order matters: models generate the answer in the order the schema lists
// the properties, and schemas marshalled from Go maps come out alphabetical
// (with "required" in random order), which made some models emit whitespace
// until max_tokens. Build every structured-output schema with these
// constructors, never from map[string]any.
//
// Objects are closed (additionalProperties false) and require every
// property, as strict structured output demands. Nodes are immutable once
// built; Describe returns a copy, so a node can be shared.
type Schema struct {
	typ         string
	description string
	enum        []string
	hasEnum     bool
	items       *Schema
	props       []Property
	raw         json.RawMessage
}

// Property is one named object property.
type Property struct {
	Name   string
	Schema *Schema
}

// Prop is an object property for Object.
func Prop(name string, s *Schema) Property { return Property{Name: name, Schema: s} }

// Str is {"type":"string"}.
func Str() *Schema { return &Schema{typ: "string"} }

// Int is {"type":"integer"}.
func Int() *Schema { return &Schema{typ: "integer"} }

// Bool is {"type":"boolean"}.
func Bool() *Schema { return &Schema{typ: "boolean"} }

// Enum is a string restricted to vs (in the given order).
func Enum(vs ...string) *Schema {
	if vs == nil {
		vs = []string{}
	}
	return &Schema{typ: "string", enum: vs, hasEnum: true}
}

// Array is an array of items.
func Array(items *Schema) *Schema { return &Schema{typ: "array", items: items} }

// Object is a closed object with props, all required, in this order.
func Object(props ...Property) *Schema { return &Schema{typ: "object", props: props} }

// Raw embeds an existing JSON Schema verbatim (e.g. a template's stored
// schema, whose bytes already carry the author's property order).
func Raw(raw json.RawMessage) *Schema { return &Schema{raw: raw} }

// Describe returns a copy of s with a description.
func (s *Schema) Describe(d string) *Schema {
	c := *s
	c.description = d
	return &c
}

// MarshalJSON writes type, description, enum, items, properties, required
// and additionalProperties, in that order.
func (s *Schema) MarshalJSON() ([]byte, error) {
	if s == nil {
		return []byte("null"), nil
	}
	if s.raw != nil {
		var b bytes.Buffer
		if err := json.Compact(&b, s.raw); err != nil {
			return nil, err
		}
		if s.description == "" {
			return b.Bytes(), nil
		}
		v, err := decodeOrdered(b.Bytes())
		o, _ := v.(jsonObject)
		if err != nil || o == nil {
			return nil, errors.New("ai: Describe needs a raw object schema")
		}
		// The description goes first and replaces any the raw schema carries.
		out := jsonObject{{"description", s.description}}
		for _, m := range o {
			if m.Key != "description" {
				out = append(out, m)
			}
		}
		return out.MarshalJSON()
	}
	var b bytes.Buffer
	b.WriteString(`{"type":`)
	writeJSON(&b, s.typ)
	if s.description != "" {
		b.WriteString(`,"description":`)
		writeJSON(&b, s.description)
	}
	if s.hasEnum {
		b.WriteString(`,"enum":`)
		writeJSON(&b, s.enum)
	}
	if s.items != nil {
		b.WriteString(`,"items":`)
		v, err := s.items.MarshalJSON()
		if err != nil {
			return nil, err
		}
		b.Write(v)
	}
	if s.typ == "object" {
		b.WriteString(`,"properties":{`)
		names := make([]string, 0, len(s.props))
		for i, p := range s.props {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSON(&b, p.Name)
			b.WriteByte(':')
			v, err := p.Schema.MarshalJSON()
			if err != nil {
				return nil, err
			}
			b.Write(v)
			names = append(names, p.Name)
		}
		b.WriteString(`},"required":`)
		writeJSON(&b, names)
		b.WriteString(`,"additionalProperties":false`)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// writeJSON writes a string or string slice (cannot fail).
func writeJSON(b *bytes.Buffer, v any) {
	j, _ := json.Marshal(v)
	b.Write(j)
}
