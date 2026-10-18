// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/render"
)

func TestToolErrorMapping(t *testing.T) {
	cases := []struct {
		err       error
		code      model.ErrorCode
		retryable bool
		internal  bool
		contains  string
	}{
		{render.ErrBusy, model.ErrBusy, true, false, "retry in a few seconds"},
		{&model.Error{Code: model.ErrValidationFailed, Message: "2 problem(s)"}, model.ErrValidationFailed, false, false, "retrying unchanged will fail the same way"},
		{&model.Error{Code: model.ErrConflict, Message: "template x is at version 3; fetch it, re-apply your change and pass expected_version=3"}, model.ErrConflict, false, false, "expected_version=3"},
		{&model.Error{Code: model.ErrUnavailable, Message: "the AI reviewer is not configured"}, model.ErrUnavailable, true, false, "retry later"},
		{fmt.Errorf("documents: %w", context.DeadlineExceeded), model.ErrTimeout, false, false, "time limit"},
		{errors.New("open /data/folio.db: permission denied"), model.ErrInternal, false, true, "internal error"},
		{&model.Error{Code: model.ErrInternal, Message: "template schemas do not compile"}, model.ErrInternal, false, true, "report it"},
	}
	for _, c := range cases {
		te, internal := toolError(c.err)
		if te.Code != c.code || te.Retryable != c.retryable || internal != c.internal {
			t.Errorf("%v: got %+v internal=%v", c.err, te, internal)
		}
		if !strings.Contains(strings.ToLower(te.Message), strings.ToLower(c.contains)) {
			t.Errorf("%v: message %q lacks %q", c.err, te.Message, c.contains)
		}
		if te.Details == nil {
			t.Errorf("%v: details must be an empty array, not null", c.err)
		}
		if strings.Contains(te.Message, "/data/folio.db") {
			t.Errorf("an internal cause leaked to the agent: %q", te.Message)
		}
	}
	// A message that already explains retrying is not extended.
	te, _ := toolError(&model.Error{Code: model.ErrTimeout, Message: "typst exceeded the limit; retrying unchanged will time out again"})
	if strings.Count(strings.ToLower(te.Message), "retry") != 1 {
		t.Errorf("hint duplicated: %q", te.Message)
	}
}

type echoIn struct {
	Name string          `json:"name" jsonschema:"who to greet"`
	N    int             `json:"n,omitempty"`
	Meta json.RawMessage `json:"meta,omitempty"`
	Fail string          `json:"fail,omitempty"`
}

type echoOut struct {
	Greeting string          `json:"greeting"`
	Actor    string          `json:"actor"`
	Meta     json.RawMessage `json:"meta,omitempty"`
}

func echoTool(access access) tool[echoIn, echoOut] {
	return tool[echoIn, echoOut]{
		Name: "echo", Title: "Echo", Description: "Echoes.", Access: access,
		Target: func(in echoIn) (string, string) { return "thing", in.Name },
		Handle: func(_ context.Context, c *call, in echoIn) (*echoOut, error) {
			switch in.Fail {
			case "conflict":
				return nil, &model.Error{Code: model.ErrConflict, Message: "thing is at version 2; pass expected_version=2"}
			case "boom":
				return nil, errors.New("disk on fire at /data/blobs")
			case "badout":
				return &echoOut{Greeting: "x", Meta: json.RawMessage(`[1]`)}, nil // meta must be an object
			}
			c.Summary("greeted %s", in.Name)
			return &echoOut{Greeting: "hello " + in.Name, Actor: c.Actor, Meta: in.Meta}, nil
		},
	}
}

func TestRegisterTypedTool(t *testing.T) {
	e := newEnv(t)
	s := mcp.NewServer(&mcp.Implementation{Name: "folio"}, nil)
	register(s, e.d, echoTool(write))
	cs := connect(t, s, nil)
	ctx := context.Background()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 1 {
		t.Fatalf("list: %v %v", tools, err)
	}
	tl := tools.Tools[0]
	if tl.Annotations.ReadOnlyHint || tl.Annotations.DestructiveHint == nil || *tl.Annotations.DestructiveHint ||
		tl.Annotations.OpenWorldHint == nil || *tl.Annotations.OpenWorldHint {
		t.Errorf("annotations: %+v", tl.Annotations)
	}
	in, _ := json.Marshal(tl.InputSchema)
	out, _ := json.Marshal(tl.OutputSchema)
	if !strings.Contains(string(in), `"required":["name"]`) || !strings.Contains(string(in), `"who to greet"`) ||
		!strings.Contains(string(in), `"additionalProperties":false`) {
		t.Errorf("input schema: %s", in)
	}
	if !strings.Contains(string(out), `"error":{`) || strings.Contains(string(out), `"required":["greeting"`) {
		t.Errorf("output schema must carry an optional error and no root required list: %s", out)
	}

	var got echoOut
	res := callOK(t, cs, "echo", map[string]any{"name": "ana", "meta": json.RawMessage(`{"amount":1.10,"big":12345678901234567890}`)}, &got)
	if got.Greeting != "hello ana" || got.Actor != LocalActor {
		t.Fatalf("output %+v", got)
	}
	if txt := res.Content[0].(*mcp.TextContent).Text; txt != "greeted ana" {
		t.Errorf("summary %q", txt)
	}
	if txt := res.Content[1].(*mcp.TextContent).Text; !strings.Contains(txt, `"amount":1.10`) || !strings.Contains(txt, `12345678901234567890`) {
		t.Errorf("numbers must keep their decimal text: %s", txt)
	}

	if te := callErr(t, cs, "echo", map[string]any{"name": "ana", "colour": "red"}); te.Code != model.ErrInvalidInput || len(te.Details) != 1 {
		t.Errorf("unknown argument: %+v", te)
	}
	if te := callErr(t, cs, "echo", map[string]any{"name": 7}); te.Code != model.ErrInvalidInput {
		t.Errorf("wrong type: %+v", te)
	}
	if te := callErr(t, cs, "echo", map[string]any{}); te.Code != model.ErrInvalidInput {
		t.Errorf("missing required: %+v", te)
	}
	if te := callErr(t, cs, "echo", map[string]any{"name": "ana", "fail": "conflict"}); te.Code != model.ErrConflict || te.Retryable {
		t.Errorf("conflict: %+v", te)
	}
	te := callErr(t, cs, "echo", map[string]any{"name": "ana", "fail": "boom"})
	if te.Code != model.ErrInternal || strings.Contains(te.Message, "/data/blobs") {
		t.Errorf("internal: %+v", te)
	}
	if !strings.Contains(e.logs.String(), "disk on fire") {
		t.Error("the internal cause must be logged")
	}
	if te := callErr(t, cs, "echo", map[string]any{"name": "ana", "fail": "badout"}); te.Code != model.ErrInternal {
		t.Errorf("output schema violation: %+v", te)
	}
	var n int
	if err := e.d.DB.R.QueryRow(`SELECT count(*) FROM audit WHERE action = 'echo' AND actor = ? AND outcome = 'conflict' AND target_kind = 'thing' AND target_id = 'ana'`, LocalActor).Scan(&n); err != nil || n != 1 {
		t.Errorf("failure audit rows = %d, %v", n, err)
	}
	if strings.Contains(e.logs.String(), "12345678901234567890") {
		t.Error("arguments reached the logs")
	}
}

func TestReadOnlyToolsAreNotAudited(t *testing.T) {
	e := newEnv(t)
	s := mcp.NewServer(&mcp.Implementation{Name: "folio"}, nil)
	register(s, e.d, echoTool(readOnly))
	cs := connect(t, s, nil)
	callErr(t, cs, "echo", map[string]any{"name": "ana", "fail": "conflict"})
	var n int
	_ = e.d.DB.R.QueryRow(`SELECT count(*) FROM audit`).Scan(&n)
	if n != 0 {
		t.Fatalf("read-only failures must not be audited: %d rows", n)
	}
}

type tokenTransport struct{ token string }

func (tt tokenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+tt.token)
	return http.DefaultTransport.RoundTrip(r)
}

// TestProgressAndActorOverStreamableHTTP runs a slow tool through the real
// stateless Streamable HTTP handler behind bearer auth.
func TestProgressAndActorOverStreamableHTTP(t *testing.T) {
	e := newEnv(t)
	s := New(e.d)
	register(s, e.d, tool[echoIn, echoOut]{
		Name: "slow", Description: "Slow.", Access: readOnly,
		Handle: func(ctx context.Context, c *call, in echoIn) (*echoOut, error) {
			p := c.startProgress(ctx)
			defer p.Stop()
			p.Step(ctx, "step one")
			time.Sleep(180 * time.Millisecond) // > 3 heartbeats at 50 ms
			p.Step(ctx, "step two")
			return &echoOut{Greeting: "done", Actor: c.Actor}, nil
		},
	})
	verify := func(_ context.Context, tok string, _ *http.Request) (*auth.TokenInfo, error) {
		if tok != "secret-token-for-tests-0123456789abcdef" {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{UserID: "carol"}, nil
	}
	h := auth.RequireBearerToken(verify, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(Handler(s, 1<<20, e.d.Logger))
	srv := httptest.NewServer(h)
	defer srv.Close()

	var mu sync.Mutex
	var msgs []string
	var last float64
	increasing := true
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, r *mcp.ProgressNotificationClientRequest) {
			mu.Lock()
			defer mu.Unlock()
			msgs = append(msgs, r.Params.Message)
			if r.Params.Progress <= last {
				increasing = false
			}
			last = r.Params.Progress
		},
	})
	ctx := context.Background()
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL,
		HTTPClient: &http.Client{Transport: tokenTransport{"secret-token-for-tests-0123456789abcdef"}}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if got := cs.InitializeResult(); got == nil || got.Instructions != Instructions || got.ServerInfo.Name != "folio" {
		t.Fatalf("initialize result: %+v", got)
	}
	params := &mcp.CallToolParams{Name: "slow", Arguments: map[string]any{"name": "x"}}
	params.SetProgressToken("tok-1")
	res, err := cs.CallTool(ctx, params)
	if err != nil || res.IsError {
		t.Fatalf("call: %v %+v", err, res)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(raw), `"actor":"carol"`) {
		t.Errorf("actor must be the token name: %s", raw)
	}
	complete := func() bool {
		mu.Lock()
		defer mu.Unlock()
		joined := strings.Join(msgs, "|")
		return strings.Contains(joined, "step one") && strings.Contains(joined, "step two") && strings.Contains(joined, "(still working)")
	}
	if !eventually(complete) {
		mu.Lock()
		t.Fatalf("progress notifications: %q", msgs)
	}
	mu.Lock()
	if !increasing {
		t.Fatalf("progress must increase: %q", msgs)
	}
	mu.Unlock()
	// Unauthenticated requests never reach the server.
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
}

// TestHeartbeatBeforeFirstStep checks that a heartbeat firing before any Step
// still carries a readable message, and that a call without a progress token
// sends nothing.
func TestHeartbeatBeforeFirstStep(t *testing.T) {
	e := newEnv(t)
	s := mcp.NewServer(&mcp.Implementation{Name: "folio"}, nil)
	register(s, e.d, tool[echoIn, echoOut]{
		Name: "idle", Description: "Idle.", Access: readOnly,
		Handle: func(ctx context.Context, c *call, _ echoIn) (*echoOut, error) {
			p := c.startProgress(ctx)
			defer p.Stop()
			time.Sleep(120 * time.Millisecond) // > 2 heartbeats at 50 ms, no Step
			return &echoOut{Greeting: "done"}, nil
		},
	})
	var mu sync.Mutex
	var msgs []string
	cs := connect(t, s, &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, r *mcp.ProgressNotificationClientRequest) {
			mu.Lock()
			defer mu.Unlock()
			msgs = append(msgs, r.Params.Message)
		},
	})
	callOK(t, cs, "idle", map[string]any{"name": "x"}, nil)
	params := &mcp.CallToolParams{Name: "idle", Arguments: map[string]any{"name": "x"}}
	params.SetProgressToken("tok-2")
	if res, err := cs.CallTool(context.Background(), params); err != nil || res.IsError {
		t.Fatalf("call: %v %+v", err, res)
	}
	if !eventually(func() bool { mu.Lock(); defer mu.Unlock(); return len(msgs) > 0 }) {
		t.Fatal("no heartbeat was sent")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, m := range msgs {
		if m != "working (still working)" {
			t.Fatalf("heartbeat message %q", m)
		}
	}
}

func TestImageContentDownscales(t *testing.T) {
	img, err := imageContent(testPNG(1600, 1000))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(img.Data))
	if err != nil || cfg.Width != MaxImageWidth || cfg.Height != 500 || img.MIMEType != "image/png" {
		t.Fatalf("downscaled: %+v %v", cfg, err)
	}
	small := testPNG(400, 300)
	if img, _ := imageContent(small); !bytes.Equal(img.Data, small) {
		t.Fatal("small pages are passed through")
	}
	if _, err := imageContent([]byte("not a png")); err == nil {
		t.Fatal("invalid png accepted")
	}
}

func TestInstructionsLimits(t *testing.T) {
	if n := len([]rune(Instructions)); n > 1200 {
		t.Fatalf("instructions are %d characters; the limit is 1200", n)
	}
	first, _, _ := strings.Cut(Instructions, "\n\n")
	if n := len([]rune(first)); n > 512 {
		t.Fatalf("first paragraph is %d characters; it must stand alone within 512", n)
	}
	for _, want := range []string{"list_templates", "get_template", "upload_asset", "validate_document", "render_document", "never compute", "get_guide"} {
		if !strings.Contains(first, want) {
			t.Errorf("first paragraph lacks %q", want)
		}
	}
}
