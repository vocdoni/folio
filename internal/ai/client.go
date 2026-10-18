// SPDX-License-Identifier: AGPL-3.0-only

// Package ai is Folio's OpenRouter client (streamed chat completions with
// JSON output, used by the layout reviewer and the web UI assistants) and the
// three web UI assistants: template drafts, document plans and brand
// suggestions. Model output is always untrusted data: it is decoded into
// closed structures, validated, and shown to the user as a proposal; it is
// never executed, fetched or saved without confirmation.
package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/vocdoni/folio/internal/model"
)

// Defaults and limits.
const (
	DefaultBaseURL = "https://openrouter.ai/api/v1"
	// DefaultModel is the model when Config.Model is empty.
	DefaultModel   = "google/gemini-3.1-flash-lite"
	DefaultTimeout = 60 * time.Second
	// DefaultMaxTokens caps the answer when Request.MaxTokens is 0.
	DefaultMaxTokens = 4096
	// MaxResponseBytes bounds a non-streamed response body and the answer
	// text of a streamed one.
	MaxResponseBytes = 2 << 20
	// MaxStreamBytes bounds the raw event stream (events wrap every token
	// in JSON framing).
	MaxStreamBytes = 16 << 20
	// RunawayWhitespace is the longest run of consecutive whitespace
	// characters a streamed answer may contain; a longer run aborts the
	// call with ErrMalformed (some models pad with whitespace until
	// max_tokens).
	RunawayWhitespace = 64
	// StallTimeout aborts a streamed answer with ErrMalformed when it has
	// produced no non-whitespace output for this long. The guard starts
	// with the first answer text: the time to the first token is bounded by
	// the call timeout only.
	StallTimeout = 15 * time.Second
)

// ErrMalformed means the model's streamed output went wrong (whitespace
// runaway or stall) and the call was aborted. It is an unavailable,
// retryable *model.Error; callers may retry once. Test it with errors.Is;
// never modify it.
var ErrMalformed error = &model.Error{Code: model.ErrUnavailable, Retryable: true, Message: "the model produced a malformed answer"}

// ErrNotJSON means the answer did not decode into the expected structure.
var ErrNotJSON = errors.New("ai: the model answer is not the expected JSON")

var (
	errCutOff  = errors.New("ai: the model answer was cut off (max_tokens)")
	errRunaway = errors.New("whitespace runaway")
	errStalled = errors.New("no output")
)

// Config configures a Client.
type Config struct {
	APIKey  string        // required; only ever sent in the Authorization header
	Model   string        // "" = DefaultModel
	BaseURL string        // API root, "" = DefaultBaseURL (FOLIO_OPENROUTER_BASE_URL)
	Timeout time.Duration // per call unless Request.Timeout is set; 0 = DefaultTimeout
	// HTTPClient overrides the client (tests). The default client never
	// follows redirects; the per-call context bounds it.
	HTTPClient *http.Client
	// Title is sent as X-Title ("" = "Folio").
	Title string
	// Logger gets one record per call: model, schema name, provider,
	// latency, token counts, finish reason and outcome; never content or
	// the key. nil = no logging.
	Logger *slog.Logger
}

// Client calls OpenRouter's chat completions API and streams the answer.
// Providers that may store or train on data are excluded
// (provider.data_collection = "deny"), only endpoints supporting every
// parameter are used (require_parameters) and the fastest of them is
// preferred (sort = "throughput").
type Client struct {
	key, model, url, title string
	timeout                time.Duration
	stall                  time.Duration // StallTimeout; shorter in tests
	client                 *http.Client
	log                    *slog.Logger
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
		timeout: c.Timeout, stall: StallTimeout, client: c.HTTPClient, log: c.Logger}
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
	if cl.log == nil {
		cl.log = slog.New(slog.DiscardHandler)
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
	SchemaName string  // response_format.json_schema.name (also logged)
	Schema     *Schema // the expected output (Object, Str, Enum, ...)
	Strict     bool    // strict structured output (closed schemas only)
	// MaxTokens caps the answer (sent as max_tokens); 0 = DefaultMaxTokens.
	MaxTokens int
	// Timeout bounds this call; 0 = the client's timeout. When it expires
	// the call fails with an unavailable, retryable *model.Error that names
	// the model.
	Timeout time.Duration
}

type jsonSchemaFormat struct {
	Name   string  `json:"name"`
	Strict bool    `json:"strict"`
	Schema *Schema `json:"schema"`
}

type responseFormat struct {
	Type       string           `json:"type"`
	JSONSchema jsonSchemaFormat `json:"json_schema"`
}

type providerPrefs struct {
	DataCollection    string `json:"data_collection"`
	RequireParameters bool   `json:"require_parameters"`
	Sort              string `json:"sort"`
}

type chatRequest struct {
	Model          string         `json:"model"`
	Messages       []Message      `json:"messages"`
	ResponseFormat responseFormat `json:"response_format"`
	Provider       providerPrefs  `json:"provider"`
	Temperature    int            `json:"temperature"`
	MaxTokens      int            `json:"max_tokens"`
	Stream         bool           `json:"stream"`
}

type upstreamError struct {
	Message string `json:"message"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type chatResponse struct {
	Provider string `json:"provider"`
	Choices  []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage *usage         `json:"usage"`
	Error *upstreamError `json:"error"`
}

type streamChunk struct {
	Provider string `json:"provider"`
	Choices  []struct {
		FinishReason string `json:"finish_reason"`
		Delta        struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *usage         `json:"usage"`
	Error *upstreamError `json:"error"`
}

// callInfo is what a call logs besides its outcome.
type callInfo struct {
	status   int
	provider string
	finish   string
	usage    usage
}

// Call outcomes (logged).
const (
	outcomeOK        = "ok"
	outcomeHTTP      = "http_error"
	outcomeCutOff    = "cut_off"
	outcomeMalformed = "malformed"
	outcomeNotJSON   = "not_json"
	outcomeTimeout   = "timeout"
	outcomeCancelled = "cancelled"
	outcomeError     = "error"
)

// Complete sends req and returns the model's answer text (fences removed).
// The answer is streamed; a whitespace runaway or a stall aborts it with
// ErrMalformed, and an expired timeout is an unavailable, retryable
// *model.Error. Other errors carry the HTTP status and OpenRouter's short
// error message only: never the API key, the request content or the
// model's answer.
func (c *Client) Complete(ctx context.Context, req Request) (string, error) {
	return c.call(ctx, req, nil)
}

// JSON completes req and decodes the answer into out (unknown fields
// rejected). It returns the raw answer for a repair round.
func (c *Client) JSON(ctx context.Context, req Request, out any) (string, error) {
	return c.call(ctx, req, func(answer string) error {
		dec := json.NewDecoder(strings.NewReader(answer))
		dec.DisallowUnknownFields()
		if err := dec.Decode(out); err != nil {
			return ErrNotJSON
		}
		return nil
	})
}

// call runs one completion, decodes it when decode is set, and logs it.
func (c *Client) call(ctx context.Context, req Request, decode func(string) error) (string, error) {
	start := time.Now()
	var info callInfo
	answer, outcome, err := c.complete(ctx, req, &info)
	if err == nil && decode != nil {
		if err = decode(answer); err != nil {
			outcome = outcomeNotJSON
		}
	}
	attrs := []slog.Attr{slog.String("model", c.model), slog.String("schema", req.SchemaName), slog.String("provider", info.provider),
		slog.Int64("latency_ms", time.Since(start).Milliseconds()), slog.Int("prompt_tokens", info.usage.PromptTokens),
		slog.Int("completion_tokens", info.usage.CompletionTokens), slog.String("finish_reason", info.finish),
		slog.String("outcome", outcome), slog.Int("status", info.status)}
	level := slog.LevelInfo
	if err != nil {
		level = slog.LevelWarn
		attrs = append(attrs, slog.String("err", err.Error())) // errors never carry content or the key
	}
	c.log.LogAttrs(ctx, level, "ai call", attrs...)
	return answer, err
}

// complete runs one call and returns the answer, the outcome and the error.
func (c *Client) complete(ctx context.Context, req Request, info *callInfo) (string, string, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = c.timeout
	}
	body, err := json.Marshal(chatRequest{
		Model:          c.model,
		Messages:       req.Messages,
		ResponseFormat: responseFormat{Type: "json_schema", JSONSchema: jsonSchemaFormat{Name: req.SchemaName, Strict: req.Strict, Schema: req.Schema}},
		Provider:       providerPrefs{DataCollection: "deny", RequireParameters: true, Sort: "throughput"},
		Temperature:    0,
		MaxTokens:      maxTokens,
		Stream:         true,
	})
	if err != nil {
		return "", outcomeError, fmt.Errorf("ai: encode request: %w", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	streamCtx, abort := context.WithCancelCause(callCtx)
	defer abort(nil)
	// interrupted classifies a failure that may come from a cancelled
	// context: the stall guard, the caller, or the call timeout.
	interrupted := func(err error) (string, string, error) {
		switch cause := context.Cause(streamCtx); {
		case errors.Is(cause, errStalled):
			return "", outcomeMalformed, fmt.Errorf("ai: %w (%w)", ErrMalformed, cause)
		case ctx.Err() != nil:
			return "", outcomeCancelled, fmt.Errorf("ai: %w", ctx.Err())
		case errors.Is(callCtx.Err(), context.DeadlineExceeded):
			return "", outcomeTimeout, &model.Error{Code: model.ErrUnavailable, Retryable: true, Err: context.DeadlineExceeded,
				Message: fmt.Sprintf("the model %s did not answer within %d s", c.model, int(math.Ceil(timeout.Seconds())))}
		}
		return "", outcomeError, err
	}
	hreq, err := http.NewRequestWithContext(streamCtx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return "", outcomeError, fmt.Errorf("ai: %w", err)
	}
	hreq.Header.Set("Authorization", "Bearer "+c.key)
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "text/event-stream, application/json")
	hreq.Header.Set("X-Title", c.title)
	resp, err := c.client.Do(hreq)
	if err != nil {
		return interrupted(fmt.Errorf("ai: openrouter request failed: %w", redactURLError(err)))
	}
	defer resp.Body.Close()
	info.status = resp.StatusCode
	if resp.StatusCode == http.StatusOK && strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		answer, outcome, err := c.readStream(resp.Body, abort, info)
		if err != nil && outcome == "" {
			return interrupted(err)
		}
		return answer, outcome, err
	}
	// A whole JSON body: errors raised before the stream starts, or a
	// server that does not stream (test stubs, proxies).
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return interrupted(fmt.Errorf("ai: read response: %w", err))
	}
	if len(raw) > MaxResponseBytes {
		return "", outcomeError, errors.New("ai: openrouter response too large")
	}
	var cr chatResponse
	jsonErr := json.Unmarshal(raw, &cr)
	if resp.StatusCode != http.StatusOK {
		msg := ""
		if jsonErr == nil && cr.Error != nil {
			msg = ": " + Short(cr.Error.Message)
		}
		return "", outcomeHTTP, fmt.Errorf("ai: openrouter returned HTTP %d%s", resp.StatusCode, msg)
	}
	if jsonErr != nil {
		return "", outcomeError, errors.New("ai: openrouter returned a malformed response")
	}
	info.provider = cr.Provider
	if cr.Usage != nil {
		info.usage = *cr.Usage
	}
	if cr.Error != nil {
		return "", outcomeError, fmt.Errorf("ai: openrouter error: %s", Short(cr.Error.Message))
	}
	if len(cr.Choices) == 0 {
		return "", outcomeError, errors.New("ai: openrouter returned no choices")
	}
	ch := cr.Choices[0]
	info.finish = ch.FinishReason
	if ch.FinishReason == "length" {
		return "", outcomeCutOff, errCutOff
	}
	return StripFences(ch.Message.Content), outcomeOK, nil
}

// readStream reads an OpenRouter event stream and returns the answer. It
// aborts the call on a whitespace runaway and arms the stall guard (which
// cancels the request through abort) with the first answer text. A read
// error comes back with an empty outcome for the caller to classify
// (stall, cancellation or timeout).
func (c *Client) readStream(body io.Reader, abort context.CancelCauseFunc, info *callInfo) (string, string, error) {
	sc := bufio.NewScanner(io.LimitReader(body, MaxStreamBytes))
	sc.Buffer(make([]byte, 0, 64<<10), MaxResponseBytes)
	var out strings.Builder
	var stall *time.Timer
	defer func() {
		if stall != nil {
			stall.Stop()
		}
	}()
	spaces := 0
	for sc.Scan() {
		data, ok := bytes.CutPrefix(sc.Bytes(), []byte("data:"))
		if !ok {
			continue // comments (": OPENROUTER PROCESSING"), blank lines, other fields
		}
		data = bytes.TrimSpace(data)
		if string(data) == "[DONE]" {
			break
		}
		var ch streamChunk
		if err := json.Unmarshal(data, &ch); err != nil {
			return "", outcomeError, errors.New("ai: openrouter returned a malformed stream event")
		}
		if ch.Provider != "" {
			info.provider = ch.Provider
		}
		if ch.Usage != nil {
			info.usage = *ch.Usage
		}
		if ch.Error != nil {
			info.finish = "error"
			return "", outcomeError, fmt.Errorf("ai: openrouter error: %s", Short(ch.Error.Message))
		}
		if len(ch.Choices) == 0 {
			continue
		}
		if f := ch.Choices[0].FinishReason; f != "" {
			info.finish = f
		}
		text := ch.Choices[0].Delta.Content
		if text == "" {
			continue
		}
		if out.Len()+len(text) > MaxResponseBytes {
			return "", outcomeError, errors.New("ai: openrouter response too large")
		}
		out.WriteString(text)
		if stall == nil {
			stall = time.AfterFunc(c.stall, func() { abort(errStalled) })
		}
		for _, r := range text {
			if !unicode.IsSpace(r) {
				spaces = 0
				stall.Reset(c.stall)
				continue
			}
			if spaces++; spaces > RunawayWhitespace {
				abort(errRunaway)
				return "", outcomeMalformed, fmt.Errorf("ai: %w (%w)", ErrMalformed, errRunaway)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return "", "", fmt.Errorf("ai: read stream: %w", err)
	}
	switch info.finish {
	case "":
		return "", outcomeError, errors.New("ai: the openrouter stream ended before the answer was complete")
	case "length":
		return "", outcomeCutOff, errCutOff
	case "error":
		return "", outcomeError, errors.New("ai: openrouter error")
	}
	return StripFences(out.String()), outcomeOK, nil
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
