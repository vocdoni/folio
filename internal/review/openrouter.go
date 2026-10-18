// SPDX-License-Identifier: AGPL-3.0-only

package review

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"golang.org/x/image/draw"
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
	// maxResponseBytes bounds the response body that is read.
	maxResponseBytes = 2 << 20
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
func findingsSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"findings"},
		"properties": map[string]any{
			"findings": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"page", "severity", "category", "message"},
					"properties": map[string]any{
						"page":     map[string]any{"type": "integer", "description": "page number as given before the image"},
						"severity": map[string]any{"type": "string", "enum": Severities},
						"category": map[string]any{"type": "string", "enum": Categories},
						"message":  map[string]any{"type": "string", "description": "the defect and where it is on the page"},
					},
				},
			},
		},
	}
}

// OpenRouterConfig configures the OpenRouter reviewer.
type OpenRouterConfig struct {
	APIKey   string        // required; never logged
	Model    string        // "" = DefaultModel
	MaxPages int           // 0 = DefaultMaxPages
	Timeout  time.Duration // per review call; 0 = DefaultTimeout
	// BaseURL is the API root ("" = DefaultOpenRouterURL). Only tests change
	// it; it is not configurable from the environment.
	BaseURL string
	// HTTPClient overrides the client (tests). The default client has no
	// overall timeout of its own (the per-call context bounds it) and never
	// follows redirects.
	HTTPClient *http.Client
}

// OpenRouter reviews pages with a vision model through OpenRouter's chat
// completions API. Providers that may store or train on data are excluded
// (provider.data_collection = "deny").
type OpenRouter struct {
	key, model, url string
	maxPages        int
	timeout         time.Duration
	client          *http.Client
}

// NewOpenRouter returns an OpenRouter reviewer. It fails without an API key.
func NewOpenRouter(c OpenRouterConfig) (*OpenRouter, error) {
	if strings.TrimSpace(c.APIKey) == "" {
		return nil, errors.New("review: an OpenRouter API key is required")
	}
	o := &OpenRouter{key: strings.TrimSpace(c.APIKey), model: c.Model, url: c.BaseURL, maxPages: c.MaxPages, timeout: c.Timeout, client: c.HTTPClient}
	if o.model == "" {
		o.model = DefaultModel
	}
	if o.url == "" {
		o.url = DefaultOpenRouterURL
	}
	o.url = strings.TrimRight(o.url, "/") + "/chat/completions"
	if o.maxPages <= 0 {
		o.maxPages = DefaultMaxPages
	}
	if o.timeout <= 0 {
		o.timeout = DefaultTimeout
	}
	if o.client == nil {
		o.client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return o, nil
}

// Model returns the configured model id.
func (o *OpenRouter) Model() string { return o.model }

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type chatRequest struct {
	Model          string         `json:"model"`
	Messages       []chatMessage  `json:"messages"`
	ResponseFormat map[string]any `json:"response_format"`
	Provider       map[string]any `json:"provider"`
	Temperature    int            `json:"temperature"`
	MaxTokens      int            `json:"max_tokens"`
}

type chatResponse struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

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
	parts := []contentPart{{Type: "text", Text: fmt.Sprintf("Review these %d rendered page(s). Document language: %s.", len(pages), lang)}}
	for _, p := range pages {
		img, err := downscalePNG(p.PNG, MaxPageWidth)
		if err != nil {
			return nil, fmt.Errorf("review: page %d: %w", p.Number, err)
		}
		parts = append(parts,
			contentPart{Type: "text", Text: fmt.Sprintf("Page %d:", p.Number)},
			contentPart{Type: "image_url", ImageURL: &imageURL{URL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(img)}})
	}
	body, err := json.Marshal(chatRequest{
		Model:    o.model,
		Messages: []chatMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: parts}},
		ResponseFormat: map[string]any{"type": "json_schema", "json_schema": map[string]any{
			"name": "layout_review", "strict": true, "schema": findingsSchema()}},
		Provider:    map[string]any{"data_collection": "deny", "require_parameters": true},
		Temperature: 0,
		MaxTokens:   4096,
	})
	if err != nil {
		return nil, fmt.Errorf("review: encode request: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("review: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+o.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Title", "Folio")
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("review: openrouter request failed: %w", redactURLError(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("review: read response: %w", err)
	}
	if len(raw) > maxResponseBytes {
		return nil, errors.New("review: openrouter response too large")
	}
	var cr chatResponse
	jsonErr := json.Unmarshal(raw, &cr)
	if resp.StatusCode != http.StatusOK {
		msg := ""
		if jsonErr == nil && cr.Error != nil {
			msg = ": " + short(cr.Error.Message)
		}
		return nil, fmt.Errorf("review: openrouter returned HTTP %d%s", resp.StatusCode, msg)
	}
	if jsonErr != nil {
		return nil, errors.New("review: openrouter returned a malformed response")
	}
	if cr.Error != nil {
		return nil, fmt.Errorf("review: openrouter error: %s", short(cr.Error.Message))
	}
	if len(cr.Choices) == 0 {
		return nil, errors.New("review: openrouter returned no choices")
	}
	ch := cr.Choices[0]
	if ch.FinishReason == "length" {
		return nil, errors.New("review: the model answer was cut off (max_tokens)")
	}
	var out struct {
		Findings []Finding `json:"findings"`
	}
	if err := json.Unmarshal([]byte(stripFences(ch.Message.Content)), &out); err != nil {
		return nil, errors.New("review: the model answer is not the expected JSON")
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

// stripFences removes a Markdown code fence some providers wrap JSON in.
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
}

// short bounds an upstream error message and keeps printable characters only.
func short(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return ' '
	}, s)
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}

// redactURLError drops the request URL from transport errors (the URL holds
// no secret today, but errors end up in logs).
func redactURLError(err error) error {
	if ue, ok := errors.AsType[*url.Error](err); ok {
		return ue.Err
	}
	return err
}
