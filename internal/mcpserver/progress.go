// SPDX-License-Identifier: AGPL-3.0-only

package mcpserver

import (
	"context"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vocdoni/folio/internal/render"
)

// progress sends notifications/progress for a call whose client supplied a
// progress token: one notification per reported step and a heartbeat with
// the last step every interval, so proxies never see an idle stream during a
// long render. Without a token every method is a no-op.
type progress struct {
	ss    *mcp.ServerSession
	token any
	mu    sync.Mutex
	n     float64
	last  string
	stop  chan struct{}
	done  chan struct{}
}

// startProgress starts the heartbeat; the caller must call Stop.
func (c *call) startProgress(ctx context.Context) *progress {
	// last starts as "working" so a heartbeat before the first Step still
	// reads as a sentence.
	p := &progress{last: "working", stop: make(chan struct{}), done: make(chan struct{})}
	if c.req == nil || c.req.Session == nil || c.req.Params == nil {
		close(p.done)
		return p
	}
	p.token = c.req.Params.GetProgressToken()
	if p.token == nil {
		close(p.done)
		return p
	}
	p.ss = c.req.Session
	interval := c.d.ProgressInterval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	go func() {
		defer close(p.done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-p.stop:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				p.mu.Lock()
				msg := p.last
				p.mu.Unlock()
				p.send(ctx, msg+" (still working)")
			}
		}
	}()
	return p
}

// Step reports a named step immediately.
func (p *progress) Step(ctx context.Context, msg string) {
	if p.token == nil {
		return
	}
	p.mu.Lock()
	p.last = msg
	p.mu.Unlock()
	p.send(ctx, msg)
}

func (p *progress) send(ctx context.Context, msg string) {
	p.mu.Lock()
	defer p.mu.Unlock() // serialises notifications and keeps progress increasing
	p.n++
	_ = p.ss.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: p.token, Progress: p.n, Message: msg})
}

// Context returns ctx wired so that render pipeline steps become progress
// notifications.
func (p *progress) Context(ctx context.Context) context.Context {
	if p.token == nil {
		return ctx
	}
	return render.WithProgress(ctx, func(step string) { p.Step(ctx, step) })
}

// Stop ends the heartbeat and waits for it.
func (p *progress) Stop() {
	if p.token != nil {
		close(p.stop)
	}
	<-p.done
}
