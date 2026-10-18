// SPDX-License-Identifier: AGPL-3.0-only

package brandkit

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/vocdoni/folio/internal/model"
)

// BuiltinLanguages have built-in label tables.
var BuiltinLanguages = []string{"en", "es", "ca"}

// LabelKeys is the complete label key set; a template language outside the
// built-ins needs all of them in template.labels to be used.
var LabelKeys = func() []string {
	var ks []string
	for k := range builtin["en"] {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}()

// RetiredLabelKeys labelled the budget total rows that Folio used to compute.
// Templates may still carry them: they are accepted and ignored (total-row
// labels now come from data.budget.totals).
var RetiredLabelKeys = []string{"budget.subtotal", "budget.tax", "budget.total"}

var builtin = map[string]map[string]string{
	"en": {
		"budget.description": "Description", "budget.quantity": "Qty", "budget.unit": "Unit",
		"budget.unit_price": "Unit price", "budget.discount": "Discount", "budget.amount": "Amount",
		"milestones.name": "Milestone", "milestones.date": "When", "milestones.description": "Description",
		"milestones.deliverables": "Deliverables", "milestones.payment": "Payment",
		"deliverables.id": "ID", "deliverables.name": "Deliverable", "deliverables.description": "Description",
		"deliverables.due": "Due", "deliverables.acceptance": "Acceptance criteria",
		"signatures.signature": "Signature", "signatures.date": "Date",
		"info.client": "Client", "info.project": "Project", "info.reference": "Reference", "info.date": "Date",
		"info.author": "Author", "info.version_label": "Version",
		"cover.prepared_for": "Prepared for", "toc.title": "Contents", "page": "Page",
	},
	"es": {
		"budget.description": "Descripción", "budget.quantity": "Cant.", "budget.unit": "Unidad",
		"budget.unit_price": "Precio unitario", "budget.discount": "Descuento", "budget.amount": "Importe",
		"milestones.name": "Hito", "milestones.date": "Cuándo", "milestones.description": "Descripción",
		"milestones.deliverables": "Entregables", "milestones.payment": "Pago",
		"deliverables.id": "ID", "deliverables.name": "Entregable", "deliverables.description": "Descripción",
		"deliverables.due": "Fecha", "deliverables.acceptance": "Criterios de aceptación",
		"signatures.signature": "Firma", "signatures.date": "Fecha",
		"info.client": "Cliente", "info.project": "Proyecto", "info.reference": "Referencia", "info.date": "Fecha",
		"info.author": "Autor", "info.version_label": "Versión",
		"cover.prepared_for": "Preparado para", "toc.title": "Índice", "page": "Página",
	},
	"ca": {
		"budget.description": "Descripció", "budget.quantity": "Quant.", "budget.unit": "Unitat",
		"budget.unit_price": "Preu unitari", "budget.discount": "Descompte", "budget.amount": "Import",
		"milestones.name": "Fita", "milestones.date": "Quan", "milestones.description": "Descripció",
		"milestones.deliverables": "Lliurables", "milestones.payment": "Pagament",
		"deliverables.id": "ID", "deliverables.name": "Lliurable", "deliverables.description": "Descripció",
		"deliverables.due": "Data", "deliverables.acceptance": "Criteris d'acceptació",
		"signatures.signature": "Signatura", "signatures.date": "Data",
		"info.client": "Client", "info.project": "Projecte", "info.reference": "Referència", "info.date": "Data",
		"info.author": "Autor", "info.version_label": "Versió",
		"cover.prepared_for": "Preparat per a", "toc.title": "Índex", "page": "Pàgina",
	},
}

// ResolveLabels returns the label table for lang. Built-in languages start
// from their table and apply template overrides; other languages use their
// template table only if it defines every key, else fall back to en. Keys
// outside LabelKeys (including RetiredLabelKeys) are ignored.
func ResolveLabels(lang string, overrides map[string]map[string]string) map[string]string {
	base, ok := builtin[lang]
	if !ok {
		if t := overrides[lang]; complete(t) {
			base = t
		} else {
			base, lang = builtin["en"], "en"
		}
	}
	out := make(map[string]string, len(builtin["en"]))
	for k := range builtin["en"] {
		out[k] = base[k]
	}
	for k, v := range overrides[lang] {
		if _, known := builtin["en"][k]; known {
			out[k] = v
		}
	}
	return out
}

func complete(t map[string]string) bool {
	if t == nil {
		return false
	}
	for k := range builtin["en"] {
		if t[k] == "" {
			return false
		}
	}
	return true
}

// BrandJSON is brand.json for Typst: the brand with logo.asset_id replaced
// by logo.path (relative inside the job dir).
func BrandJSON(b model.Brand, logoPath string) ([]byte, error) {
	type logoJSON struct {
		Path    string `json:"path"`
		WidthMM int    `json:"width_mm"`
	}
	out := struct {
		Name   string            `json:"name"`
		Colors model.BrandColors `json:"colors"`
		Fonts  model.BrandFonts  `json:"fonts"`
		Logo   *logoJSON         `json:"logo"`
		Header model.BrandHeader `json:"header"`
		Footer model.BrandFooter `json:"footer"`
	}{Name: b.Name, Colors: b.Colors, Fonts: b.Fonts, Header: b.Header, Footer: b.Footer}
	if b.Logo != nil && logoPath != "" {
		out.Logo = &logoJSON{Path: logoPath, WidthMM: b.Logo.WidthMM}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("brandkit: brand.json: %w", err)
	}
	return model.CanonicalBytes(raw)
}
