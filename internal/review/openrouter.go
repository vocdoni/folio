// SPDX-License-Identifier: AGPL-3.0-only

package review

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"golang.org/x/image/draw"

	"github.com/vocdoni/folio/internal/ai"
)

// OpenRouter defaults.
const (
	DefaultOpenRouterURL = "https://openrouter.ai/api/v1"
	DefaultModel         = "google/gemma-4-31b-it"
	DefaultMaxPages      = 20
	DefaultTimeout       = 2 * time.Minute
	// MaxPageWidth is the widest page image sent to the model, in pixels.
	MaxPageWidth = 1240
	// maxPagePixels bounds a page image before it is decoded.
	maxPagePixels = 40_000_000
)

// systemPrompt is fixed: the reviewer only looks for layout defects and never
// comments on or rewrites the content.
const systemPrompt = `You are a meticulous print-layout QA reviewer for business documents (proposals, quotes, reports, statements of work). You receive rendered pages as images, each preceded by its page number.

Report only visual and layout defects that a careful human proofreader would flag before sending the document to a client:
- blank: a page that is empty or almost empty when it should not be;
- overflow: text, tables or images cut off at a page edge or overflowing their box;
- overlap: elements drawn on top of each other;
- table: broken tables (misaligned or missing columns, a header separated from its rows, rows split awkwardly, unreadable cells);
- typography: wrong or missing glyphs, inconsistent fonts or sizes, very poor line breaking or hyphenation, a heading stranded at the bottom of a page;
- image: missing, distorted, pixelated or badly cropped images or logos;
- branding: inconsistent colours, logo or header/footer between pages;
- other: any other clear layout defect.

Do not judge, summarise, translate or rewrite the content, and do not check facts, spelling or arithmetic. Never invent problems: if a page looks fine, report nothing for it. Use severity "problem" for defects that make the document unfit to send, "warning" for visible flaws, "info" for minor polish. Write each message in English, at most two sentences, naming the element and where it is on the page. Answer only with JSON matching the schema; an empty findings array is a valid answer.`

// findingsSchema is the response_format JSON schema (strict mode: every
// property required, no additional properties).
func findingsSchema() *ai.Schema {
	return ai.Object(ai.Prop("findings", ai.Array(ai.Object(
		ai.Prop("page", ai.Int().Describe("page number as given before the image")),
		ai.Prop("severity", ai.Enum(Severities...)),
		ai.Prop("category", ai.Enum(Categories...)),
		ai.Prop("message", ai.Str().Describe("the defect and where it is on the page")),
	))))
}

// OpenRouterConfig configures the OpenRouter reviewer.
type OpenRouterConfig struct {
	APIKey   string        // required; never logged
	Model    string        // "" = DefaultModel
	MaxPages int           // 0 = DefaultMaxPages
	Timeout  time.Duration // per review call; 0 = DefaultTimeout
	// BaseURL is the API root ("" = DefaultOpenRouterURL); set from
	// FOLIO_OPENROUTER_BASE_URL (tests and outbound proxies).
	BaseURL string
	// HTTPClient overrides the client (tests). The default client has no
	// overall timeout of its own (the per-call context bounds it) and never
	// follows redirects.
	HTTPClient *http.Client
	// Logger gets one record per model call (no content, no key); nil =
	// no logging.
	Logger *slog.Logger
}

// OpenRouter reviews pages with a vision model through OpenRouter's chat
// completions API (the shared internal/ai client: providers that may store
// or train on data are excluded).
type OpenRouter struct {
	ai       *ai.Client
	maxPages int
}

// NewOpenRouter returns an OpenRouter reviewer. It fails without an API key.
func NewOpenRouter(c OpenRouterConfig) (*OpenRouter, error) {
	if strings.TrimSpace(c.APIKey) == "" {
		return nil, errors.New("review: an OpenRouter API key is required")
	}
	if c.BaseURL == "" {
		c.BaseURL = DefaultOpenRouterURL
	}
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}
	if c.Model == "" {
		c.Model = DefaultModel
	}
	client, err := ai.New(ai.Config{APIKey: c.APIKey, Model: c.Model, BaseURL: c.BaseURL, Timeout: c.Timeout, HTTPClient: c.HTTPClient, Logger: c.Logger})
	if err != nil {
		return nil, fmt.Errorf("review: %w", err)
	}
	o := &OpenRouter{ai: client, maxPages: c.MaxPages}
	if o.maxPages <= 0 {
		o.maxPages = DefaultMaxPages
	}
	return o, nil
}

// Model returns the configured model id.
func (o *OpenRouter) Model() string { return o.ai.Model() }

// Review sends the pages to the model and returns its findings. It returns
// an error, never partial findings, when the call cannot complete. Errors
// carry the HTTP status and OpenRouter's short error message only: never the
// API key, the page images or the model's answer.
func (o *OpenRouter) Review(ctx context.Context, pages []Page, info Info) ([]Finding, error) {
	if len(pages) == 0 {
		return nil, errors.New("review: no pages to review")
	}
	if len(pages) > o.maxPages {
		return nil, fmt.Errorf("review: %d pages exceed the limit of %d per review", len(pages), o.maxPages)
	}
	lang := info.Language
	if lang == "" {
		lang = "unknown"
	}
	parts := []ai.Part{ai.Text(fmt.Sprintf("Review these %d rendered page(s). Document language: %s.", len(pages), lang))}
	for _, p := range pages {
		img, err := downscalePNG(p.PNG, MaxPageWidth)
		if err != nil {
			return nil, fmt.Errorf("review: page %d: %w", p.Number, err)
		}
		parts = append(parts, ai.Text(fmt.Sprintf("Page %d:", p.Number)), ai.PNG(img))
	}
	var out struct {
		Findings []Finding `json:"findings"`
	}
	_, err := o.ai.JSON(ctx, ai.Request{
		Messages:   []ai.Message{{Role: "system", Content: systemPrompt}, {Role: "user", Content: parts}},
		SchemaName: "layout_review", Schema: findingsSchema(), Strict: true, MaxTokens: 4096,
	}, &out)
	if err != nil {
		return nil, fmt.Errorf("review: %w", err)
	}
	if out.Findings == nil {
		out.Findings = []Finding{}
	}
	return out.Findings, nil
}

// downscalePNG returns png unchanged when it is at most maxWidth pixels wide
// and otherwise a proportionally scaled copy.
func downscalePNG(b []byte, maxWidth int) ([]byte, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("not a PNG: %w", err)
	}
	if cfg.Width <= maxWidth {
		return b, nil
	}
	if cfg.Width*cfg.Height > maxPagePixels {
		return nil, fmt.Errorf("image of %dx%d pixels is too large", cfg.Width, cfg.Height)
	}
	src, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("decode PNG: %w", err)
	}
	h := cfg.Height * maxWidth / cfg.Width
	if h < 1 {
		h = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, maxWidth, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, fmt.Errorf("encode PNG: %w", err)
	}
	return buf.Bytes(), nil
}
