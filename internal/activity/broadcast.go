// SPDX-License-Identifier: AGPL-3.0-only

package activity

import (
	"errors"
	"sync"
)

// SubscriberBuffer is the per-subscriber buffer; a subscriber that falls this
// far behind is dropped (its Done channel closes) instead of slowing others.
const SubscriberBuffer = 64

// Errors returned by Subscribe.
var (
	ErrTooManyStreams = errors.New("activity: too many live streams for this session")
	ErrClosed         = errors.New("activity: broadcaster closed")
)

// Subscription is one live consumer (an SSE stream).
type Subscription struct {
	C    <-chan Row      // new rows, in id order
	Done <-chan struct{} // closed when the subscriber was dropped or the broadcaster closed
	c    chan Row
	done chan struct{}
	key  string
	once sync.Once
}

func (s *Subscription) stop() { s.once.Do(func() { close(s.done) }) }

// Broadcaster fans published rows out to subscribers without ever blocking
// the publisher.
type Broadcaster struct {
	mu     sync.Mutex
	subs   map[*Subscription]struct{}
	closed bool
}

// NewBroadcaster returns an open broadcaster.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: map[*Subscription]struct{}{}}
}

// Subscribe adds a subscriber for key (a session id); at most maxPerKey
// subscriptions may exist per key.
func (b *Broadcaster) Subscribe(key string, maxPerKey int) (*Subscription, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, ErrClosed
	}
	n := 0
	for s := range b.subs {
		if s.key == key {
			n++
		}
	}
	if n >= maxPerKey {
		return nil, ErrTooManyStreams
	}
	s := &Subscription{c: make(chan Row, SubscriberBuffer), done: make(chan struct{}), key: key}
	s.C, s.Done = s.c, s.done
	b.subs[s] = struct{}{}
	return s, nil
}

// Unsubscribe removes s (idempotent).
func (b *Broadcaster) Unsubscribe(s *Subscription) {
	b.mu.Lock()
	delete(b.subs, s)
	b.mu.Unlock()
	s.stop()
}

// Publish delivers rows to every subscriber; a subscriber whose buffer is
// full is dropped.
func (b *Broadcaster) Publish(rows ...Row) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subs {
		for _, r := range rows {
			select {
			case s.c <- r:
				continue
			default:
			}
			delete(b.subs, s)
			s.stop()
			break
		}
	}
}

// CloseKey ends every subscription of key (logout).
func (b *Broadcaster) CloseKey(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subs {
		if s.key == key {
			delete(b.subs, s)
			s.stop()
		}
	}
}

// Close ends every subscription and refuses new ones (server shutdown, so
// open streams never hold up the drain).
func (b *Broadcaster) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	for s := range b.subs {
		delete(b.subs, s)
		s.stop()
	}
}

// Len returns the number of live subscriptions.
func (b *Broadcaster) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
