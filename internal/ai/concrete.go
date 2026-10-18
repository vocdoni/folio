// SPDX-License-Identifier: AGPL-3.0-only

package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/vocdoni/folio/internal/components"
)

// jsonObject is a decoded JSON object that keeps its key order (template
// authors' schemas list properties in a meaningful order, and models answer
// in the order the schema lists them).
type jsonObject []jsonMember

type jsonMember struct {
	Key   string
	Value any // jsonObject, []any, string, json.Number, bool or nil
}

func (o jsonObject) get(k string) (any, bool) {
	for _, m := range o {
		if m.Key == k {
			return m.Value, true
		}
	}
	return nil, false
}

// MarshalJSON writes the members in order.
func (o jsonObject) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, err := json.Marshal(m.Key)
		if err != nil {
			return nil, err
		}
		v, err := json.Marshal(m.Value)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// decodeOrdered decodes one JSON value, objects as jsonObject.
func decodeOrdered(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, err := readOrdered(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err == nil {
		return nil, errors.New("trailing data")
	}
	return v, nil
}

func readOrdered(dec *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, errors.New("JSON nested too deeply")
	}
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := t.(json.Delim)
	if !ok {
		return t, nil
	}
	switch d {
	case '{':
		o := jsonObject{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			k, _ := kt.(string)
			v, err := readOrdered(dec, depth+1)
			if err != nil {
				return nil, err
			}
			o = append(o, jsonMember{k, v})
		}
		_, err := dec.Token()
		return o, err
	case '[':
		a := []any{}
		for dec.More() {
			v, err := readOrdered(dec, depth+1)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		_, err := dec.Token()
		return a, err
	}
	return nil, errors.New("unexpected JSON delimiter")
}

// maxSchemaDepth bounds $ref resolution and nesting in concreteSchema.
const maxSchemaDepth = 16

// concreteSchema rewrites a template or component JSON Schema into a
// structured-output schema: "folio:component/<name>" references become the
// component's schema, only type, description, enum, items, properties and
// required are kept (pattern, maxLength and the other validation keywords
// stay with Folio's own validation, which runs on every render), and objects
// with properties are closed. Anything else (other references, boolean
// schemas) becomes the empty schema.
func concreteSchema(node any) (jsonObject, error) {
	return concreteAt(node, 0)
}

func concreteAt(node any, depth int) (jsonObject, error) {
	if depth > maxSchemaDepth {
		return nil, errors.New("ai: the schema is nested too deeply")
	}
	o, ok := node.(jsonObject)
	if !ok {
		return jsonObject{}, nil
	}
	if ref, ok := o.get("$ref"); ok {
		s, _ := ref.(string)
		name, ok := strings.CutPrefix(s, "folio:component/")
		raw, known := components.Schema(name)
		if !ok || !known {
			return jsonObject{}, nil
		}
		v, err := decodeOrdered(raw)
		if err != nil {
			return nil, err
		}
		return concreteAt(v, depth+1)
	}
	out := jsonObject{}
	if t, ok := o.get("type"); ok {
		out = append(out, jsonMember{"type", t})
	}
	if d, _ := o.get("description"); d != nil {
		if s, ok := d.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, jsonMember{"description", Short(s)})
		}
	}
	if e, ok := o.get("enum"); ok {
		out = append(out, jsonMember{"enum", e})
	}
	if it, ok := o.get("items"); ok {
		if _, isObj := it.(jsonObject); isObj {
			c, err := concreteAt(it, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, jsonMember{"items", c})
		}
	}
	if p, _ := o.get("properties"); p != nil {
		if props, ok := p.(jsonObject); ok {
			cp := make(jsonObject, 0, len(props))
			for _, m := range props {
				c, err := concreteAt(m.Value, depth+1)
				if err != nil {
					return nil, err
				}
				cp = append(cp, jsonMember{m.Key, c})
			}
			req := []any{}
			if r, _ := o.get("required"); r != nil {
				if list, ok := r.([]any); ok {
					for _, x := range list {
						if s, ok := x.(string); ok && hasKey(cp, s) && !containsAny(req, s) {
							req = append(req, s)
						}
					}
				}
			}
			out = append(out, jsonMember{"properties", cp}, jsonMember{"required", req}, jsonMember{"additionalProperties", false})
		}
	}
	return out, nil
}

func hasKey(o jsonObject, k string) bool {
	_, ok := o.get(k)
	return ok
}

func containsAny(list []any, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// closedSchema reports whether strict structured output accepts s: every
// node is typed and every object lists all of its properties as required.
func closedSchema(s jsonObject) bool {
	t, ok := s.get("type")
	if !ok {
		return false
	}
	if p, _ := s.get("properties"); p != nil {
		props, _ := p.(jsonObject)
		r, _ := s.get("required")
		req, _ := r.([]any)
		if len(req) != len(props) {
			return false
		}
		for _, m := range props {
			c, _ := m.Value.(jsonObject)
			if !closedSchema(c) {
				return false
			}
		}
	} else if t == "object" {
		return false // a free-form object
	}
	if it, ok := s.get("items"); ok {
		c, _ := it.(jsonObject)
		return closedSchema(c)
	}
	return true
}

// rawSchema embeds an ordered schema node.
func rawSchema(o jsonObject) *Schema {
	b, _ := o.MarshalJSON() // members are decoded JSON values: cannot fail
	return Raw(b)
}

// componentSchema is the concrete schema of a built-in component's data.
func componentSchema(name string) (jsonObject, bool) {
	raw, ok := components.Schema(name)
	if !ok {
		return nil, false
	}
	v, err := decodeOrdered(raw)
	if err != nil {
		return nil, false
	}
	c, err := concreteSchema(v)
	if err != nil {
		return nil, false
	}
	return c, true
}
