// SPDX-License-Identifier: AGPL-3.0-only

// Package seed embeds Folio's neutral starter content: the "example" brand
// (with a generated text logo) and the starter templates. Apply stores them,
// and later upgrades them, through the normal catalog validation (including
// sample renders).
package seed

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"

	"github.com/vocdoni/folio/internal/model"
)

//go:embed brand/example.json brand/logo.svg templates/*.json
var files embed.FS

// Actor records seeded items.
const Actor = "folio"

// Version is the version of the embedded starter content. Bump it with every
// change to brand/ or templates/, so Apply upgrades deployed instances.
const Version = 2

// LogoPlaceholderID is the logo asset id inside brand/example.json; Apply
// replaces it with the id of the uploaded logo. Tests resolve it directly.
const LogoPlaceholderID = "00000000-0000-7000-8000-00000000c0de"

// TemplateIDs lists the starter templates in seeding order.
var TemplateIDs = []string{"proposal", "requirements", "quote", "technical-report", "sow"}

func decode(name string, v any) error {
	b, err := files.ReadFile(name)
	if err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("seed: %s: %w", name, err)
	}
	return nil
}

// Brand returns the example brand (logo.asset_id = LogoPlaceholderID).
func Brand() (model.Brand, error) {
	var b model.Brand
	return b, decode("brand/example.json", &b)
}

// Logo returns the example brand's SVG logo.
func Logo() []byte {
	b, _ := files.ReadFile("brand/logo.svg")
	return b
}

// Template returns one starter template by id.
func Template(id string) (model.Template, error) {
	var t model.Template
	return t, decode("templates/"+id+".json", &t)
}

// Templates returns every starter template in TemplateIDs order.
func Templates() ([]model.Template, error) {
	out := make([]model.Template, 0, len(TemplateIDs))
	for _, id := range TemplateIDs {
		t, err := Template(id)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}
