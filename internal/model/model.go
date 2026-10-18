// SPDX-License-Identifier: AGPL-3.0-only

// Package model holds the domain types shared by the engine packages: brand
// and template specs, document input, findings and structured errors, plus
// canonical JSON. It imports nothing from Folio.
package model

import (
	"encoding/json"
	"regexp"
)

// SpecVersion is the implicit version of stored brand/template spec JSON.
const SpecVersion = 1

// SlugRE validates brand and template IDs.
var SlugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

// Brand is one immutable brand version.
type Brand struct {
	SpecVersion int         `json:"spec_version,omitempty"`
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Colors      BrandColors `json:"colors"`
	Fonts       BrandFonts  `json:"fonts"`
	Logo        *BrandLogo  `json:"logo,omitempty"`
	Header      BrandHeader `json:"header"`
	Footer      BrandFooter `json:"footer"`
	DOCX        BrandDOCX   `json:"docx"`
}

type BrandColors struct {
	Primary    string `json:"primary"`
	Secondary  string `json:"secondary"`
	Accent     string `json:"accent"`
	Text       string `json:"text"`
	Muted      string `json:"muted"`
	Background string `json:"background"`
}

type BrandFonts struct {
	Heading string `json:"heading"`
	Body    string `json:"body"`
	Mono    string `json:"mono"`
}

type BrandLogo struct {
	AssetID string `json:"asset_id"`
	WidthMM int    `json:"width_mm"`
}

type BrandHeader struct {
	Text     string `json:"text"`
	ShowLogo bool   `json:"show_logo"`
}

type BrandFooter struct {
	Text        string `json:"text"`
	Legal       string `json:"legal"`
	PageNumbers bool   `json:"page_numbers"`
}

type BrandDOCX struct {
	FontFallbacks map[string]string `json:"font_fallbacks,omitempty"` // keys: heading, body, mono
}

// Template is one immutable template version.
type Template struct {
	SpecVersion    int                          `json:"spec_version,omitempty"`
	ID             string                       `json:"id"`
	Name           string                       `json:"name"`
	Description    string                       `json:"description"`
	Category       string                       `json:"category"`
	BrandID        string                       `json:"brand_id"`
	Languages      []string                     `json:"languages"`
	Layout         Layout                       `json:"layout"`
	MetadataSchema json.RawMessage              `json:"metadata_schema,omitempty"`
	DataSchema     json.RawMessage              `json:"data_schema,omitempty"`
	Sections       []Section                    `json:"sections"`
	Components     []string                     `json:"components"`
	Strict         bool                         `json:"strict"`
	BodySkeleton   string                       `json:"body_skeleton"`
	Sample         Sample                       `json:"sample"`
	Labels         map[string]map[string]string `json:"labels,omitempty"`
}

// Layout selects a base layout or a custom Typst layout source.
type Layout struct {
	Base    string          `json:"base"`              // report | proposal | quote | letter
	Typst   *string         `json:"typst"`             // custom layout source, or null
	Options json.RawMessage `json:"options,omitempty"` // validated against the base layout's options schema
}

// LayoutOptions is the decoded, validated layout.options.
type LayoutOptions struct {
	Cover            bool   `json:"cover"`
	TOC              bool   `json:"toc"`
	PageSize         string `json:"page_size"` // a4 | letter
	HeadingNumbering bool   `json:"heading_numbering"`
}

type Section struct {
	Title     string   `json:"title"`
	Required  bool     `json:"required"`
	Aliases   []string `json:"aliases,omitempty"`
	Hint      string   `json:"hint,omitempty"`
	Component string   `json:"component,omitempty"`
}

type Sample struct {
	Markdown string          `json:"markdown"`
	Metadata json.RawMessage `json:"metadata"`
	Data     json.RawMessage `json:"data,omitempty"`
}

// Format is an output format.
type Format string

const (
	FormatPDF  Format = "pdf"
	FormatDOCX Format = "docx"
)

// DocumentInput is what an agent sends to validate or render.
type DocumentInput struct {
	Markdown string          `json:"markdown"`
	Metadata json.RawMessage `json:"metadata"`
	Data     json.RawMessage `json:"data,omitempty"`
	Formats  []Format        `json:"formats,omitempty"` // default pdf+docx
}

// Ref is a resolved id@version.
type Ref struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}
