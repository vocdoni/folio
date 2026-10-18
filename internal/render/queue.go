// SPDX-License-Identifier: AGPL-3.0-only

package render

import (
	"context"
	"sync"

	"github.com/vocdoni/folio/internal/model"
)

// Queue bounds concurrent renders: `slots` run at once, at most `waiting`
// callers wait; beyond that Acquire fails fast with a retryable busy error.
type Queue struct {
	slots   chan struct{}
	mu      sync.Mutex
	waiting int
	max     int
}

// NewQueue returns a queue with concurrency slots and maxWaiting waiters.
func NewQueue(concurrency, maxWaiting int) *Queue {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Queue{slots: make(chan struct{}, concurrency), max: maxWaiting}
}

// ErrBusy is returned when the wait queue is full.
var ErrBusy = &model.Error{Code: model.ErrBusy, Retryable: true,
	Message: "the render queue is full; retry in a few seconds"}

// Acquire takes a slot, waiting while the queue has room. The returned
// release function must be called exactly once.
func (q *Queue) Acquire(ctx context.Context) (func(), error) {
	select {
	case q.slots <- struct{}{}:
		return q.release, nil
	default:
	}
	q.mu.Lock()
	if q.waiting >= q.max {
		q.mu.Unlock()
		return nil, ErrBusy
	}
	q.waiting++
	q.mu.Unlock()
	defer func() {
		q.mu.Lock()
		q.waiting--
		q.mu.Unlock()
	}()
	select {
	case q.slots <- struct{}{}:
		return q.release, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (q *Queue) release() { <-q.slots }
