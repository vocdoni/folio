// SPDX-License-Identifier: AGPL-3.0-only

package ai

import (
	"encoding/json"
	"testing"
)

func TestSchemaKeepsDeclarationOrder(t *testing.T) {
	s := Object(
		Prop("name", Str()),
		Prop("colors", Object(Prop("primary", Str()), Prop("background", Str().Describe("page colour")))),
		Prop("logo_index", Int()),
		Prop("fonts", Array(Enum("Inter", "Arial"))),
		Prop("cover", Bool()),
		Prop("extra", Raw(json.RawMessage(`{ "type": "object", "properties": {"z": {}, "a": {}} }`))),
	)
	got, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"type":"object","properties":{` +
		`"name":{"type":"string"},` +
		`"colors":{"type":"object","properties":{"primary":{"type":"string"},"background":{"type":"string","description":"page colour"}},` +
		`"required":["primary","background"],"additionalProperties":false},` +
		`"logo_index":{"type":"integer"},` +
		`"fonts":{"type":"array","items":{"type":"string","enum":["Inter","Arial"]}},` +
		`"cover":{"type":"boolean"},` +
		`"extra":{"type":"object","properties":{"z":{},"a":{}}}},` +
		`"required":["name","colors","logo_index","fonts","cover","extra"],"additionalProperties":false}`
	if string(got) != want {
		t.Fatalf("schema bytes\n got %s\nwant %s", got, want)
	}
	// Marshalling is deterministic (no map iteration anywhere).
	for range 20 {
		again, _ := json.Marshal(s)
		if string(again) != want {
			t.Fatal("schema marshalling is not deterministic")
		}
	}
	if !json.Valid(got) {
		t.Fatal("invalid JSON")
	}
}

func TestSchemaDescribeCopies(t *testing.T) {
	str := Str()
	d := str.Describe("x")
	a, _ := json.Marshal(str)
	b, _ := json.Marshal(d)
	if string(a) != `{"type":"string"}` || string(b) != `{"type":"string","description":"x"}` {
		t.Fatalf("Describe must not change the shared node: %s %s", a, b)
	}
	if e, _ := json.Marshal(Enum()); string(e) != `{"type":"string","enum":[]}` {
		t.Fatalf("empty enum: %s", e)
	}
	if o, _ := json.Marshal(Object()); string(o) != `{"type":"object","properties":{},"required":[],"additionalProperties":false}` {
		t.Fatalf("empty object: %s", o)
	}
	var nilSchema *Schema
	if n, _ := json.Marshal(nilSchema); string(n) != "null" {
		t.Fatalf("nil schema: %s", n)
	}
}
