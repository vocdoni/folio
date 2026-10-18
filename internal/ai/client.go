// SPDX-License-Identifier: AGPL-3.0-only

// Package ai is Folio's OpenRouter client (chat completions with JSON
// output, used by the layout reviewer and the web UI assistants) and the
// three web UI assistants: template drafts, document plans and brand
// suggestions. Model output is always untrusted data: it is decoded into
// closed structures, validated, and shown to the user as a proposal; it is
// never executed, fetched or saved without confirmation.
package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

// Defaults.
const (
	DefaultBaseURL = "https://openrouter.ai/api/v1"
	DefaultModel   = "google/gemma-4-31b-it"
	DefaultTimeout = 60 * time.Second
	// MaxResponseBytes bounds the response body that is read.
	MaxResponseBytes = 2 << 20
)

// Config configures a Client.
type Config struct {
	APIKey  string        // required; only ever sent in the Authorization header
	Model   string        // "" = DefaultModel
	BaseURL string        // API root, "" = DefaultBaseURL (FOLIO_OPENROUTER_BASE_URL)
	Timeout time.Duration // per call; 0 = DefaultTimeout
	// HTTPClient overrides the client (tests). The default client never
	// follows redirects; the per-call context bounds it.
	HTTPClient *http.Client
	// Title is sent as X-Title ("" = "Folio").
	Title string
}

// Client calls OpenRouter's chat completions API. Providers that may store
// or train on data are excluded (provider.data_collection = "deny") and only
// endpoints supporting every parameter are used (require_parameters).
type Client struct {
	key, model, url, title string
	timeout                time.Duration
	client                 *http.Client
}

// New returns a client; it fails without an API key or with a bad base URL.
func New(c Config) (*Client, error) {
	key := strings.TrimSpace(c.APIKey)
	if key == "" {
		return nil, errors.New("ai: an OpenRouter API key is required")
	}
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return nil, errors.New("ai: the OpenRouter base URL must be an absolute http(s) URL")
	}
	cl := &Client{key: key, model: c.Model, url: strings.TrimRight(base, "/") + "/chat/completions", title: c.Title,
		timeout: c.Timeout, client: c.HTTPClient}
	if cl.model == "" {
		cl.model = DefaultModel
	}
	if cl.timeout <= 0 {
		cl.timeout = DefaultTimeout
	}
	if cl.title == "" {
		cl.title = "Folio"
	}
	if cl.client == nil {
		cl.client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return cl, nil
}

// Model returns the configured model id.
func (c *Client) Model() string { return c.model }

// Part is one piece of a user message: text or an image.
type Part struct {
	Type     string    `json:"type"` // text | image_url
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL carries a data: URI.
type ImageURL struct {
	URL string `json:"url"`
}

// Text is a text part.
func Text(s string) Part { return Part{Type: "text", Text: s} }

// PNG is an image part with a PNG data URI.
func PNG(b []byte) Part {
	return Part{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(b)}}
}

// Message is a chat message; Content is a string or []Part.
type Message struct {
	Role    string `json:"role"` // system | user | assistant
	Content any    `json:"content"`
}

// Request is one JSON-output completion.
type Request struct {
	Messages   []Message
	SchemaName string         // response_format.json_schema.name
	Schema     map[string]any // the expected output
	Strict     bool           // strict structured output (closed schemas only)
	MaxTokens  int            // 0 = 4096
}

type chatRequest struct {
	Model          string         `json:"model"`
	Messages       []Message      `json:"messages"`
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

// Complete sends req and returns the model's answer text (fences removed).
// Errors carry the HTTP status and OpenRouter's short error message only:
// never the API key, the request content or the model's answer.
func (c *Client) Complete(ctx context.Context, req Request) (string, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	body, err := json.Marshal(chatRequest{
		Model:    c.model,
		Messages: req.Messages,
		ResponseFormat: map[string]any{"type": "json_schema", "json_schema": map[string]any{
			"name": req.SchemaName, "strict": req.Strict, "schema": req.Schema}},
		Provider:    map[string]any{"data_collection": "deny", "require_parameters": true},
		Temperature: 0,
		MaxTokens:   maxTokens,
	})
	if err != nil {
		return "", fmt.Errorf("ai: encode request: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ai: %w", err)
	}
	hreq.Header.Set("Authorization", "Bearer "+c.key)
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("X-Title", c.title)
	resp, err := c.client.Do(hreq)
	if err != nil {
		return "", fmt.Errorf("ai: openrouter request failed: %w", redactURLError(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return "", fmt.Errorf("ai: read response: %w", err)
	}
	if len(raw) > MaxResponseBytes {
		return "", errors.New("ai: openrouter response too large")
	}
	var cr chatResponse
	jsonErr := json.Unmarshal(raw, &cr)
	if resp.StatusCode != http.StatusOK {
		msg := ""
		if jsonErr == nil && cr.Error != nil {
			msg = ": " + Short(cr.Error.Message)
		}
		return "", fmt.Errorf("ai: openrouter returned HTTP %d%s", resp.StatusCode, msg)
	}
	if jsonErr != nil {
		return "", errors.New("ai: openrouter returned a malformed response")
	}
	if cr.Error != nil {
		return "", fmt.Errorf("ai: openrouter error: %s", Short(cr.Error.Message))
	}
	if len(cr.Choices) == 0 {
		return "", errors.New("ai: openrouter returned no choices")
	}
	ch := cr.Choices[0]
	if ch.FinishReason == "length" {
		return "", errors.New("ai: the model answer was cut off (max_tokens)")
	}
	return StripFences(ch.Message.Content), nil
}

// ErrNotJSON means the answer did not decode into the expected structure.
var ErrNotJSON = errors.New("ai: the model answer is not the expected JSON")

// JSON completes req and decodes the answer into out (unknown fields
// rejected). It returns the raw answer for a repair round.
func (c *Client) JSON(ctx context.Context, req Request, out any) (string, error) {
	answer, err := c.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	dec := json.NewDecoder(strings.NewReader(answer))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return answer, ErrNotJSON
	}
	return answer, nil
}

// StripFences removes a Markdown code fence some providers wrap JSON in.
func StripFences(s string) string {
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

// Short bounds an upstream message and keeps printable characters only.
func Short(s string) string {
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

// redactURLError drops the request URL from transport errors.
func redactURLError(err error) error {
	if ue, ok := errors.AsType[*url.Error](err); ok {
		return ue.Err
	}
	return err
}
