// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Canonical returns canonical JSON for v: object keys sorted, no insignificant
// whitespace, no HTML escaping, numbers kept as their original decimal text.
func Canonical(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("canonical: marshal: %w", err)
	}
	return CanonicalBytes(raw)
}

// CanonicalBytes canonicalises a JSON document.
func CanonicalBytes(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var x any
	if err := dec.Decode(&x); err != nil {
		return nil, fmt.Errorf("canonical: decode: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("canonical: trailing data")
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(x); err != nil { // maps encode with sorted keys
		return nil, fmt.Errorf("canonical: encode: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// DecodeJSON decodes raw into an untyped value with json.Number for numbers
// (never float64), so numbers keep their original text through the schema
// validator and the canonical input.
func DecodeJSON(raw []byte) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var x any
	if err := dec.Decode(&x); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("decode json: trailing data")
	}
	return x, nil
}
