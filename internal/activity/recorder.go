// SPDX-License-Identifier: AGPL-3.0-only

package activity

import (
	"context"
	"database/sql"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/vocdoni/folio/internal/store"
)

// Recorder tuning (the documented bounds).
const (
	QueueSize     = 4096                   // rows waiting for the writer
	BatchSize     = 100                    // rows per write transaction
	BatchInterval = 250 * time.Millisecond // longest wait before a partial batch is written
	dropLogPeriod = time.Minute
	flushTimeout  = 5 * time.Second // final flush after Run's context ends
)

// Recorder buffers rows and writes them in batches from one goroutine (Run),
// then publishes the stored rows (with ids) to the Broadcaster. Record never
// blocks: when the queue is full the row is dropped and counted.
type Recorder struct {
	db      *store.DB
	pub     *Broadcaster // may be nil
	log     *slog.Logger
	now     func() time.Time
	ch      chan Row
	dropped atomic.Int64
	written atomic.Int64
}

// NewRecorder returns a recorder writing to db and publishing to pub (nil = no live feed).
func NewRecorder(db *store.DB, pub *Broadcaster, log *slog.Logger, now func() time.Time) *Recorder {
	return newRecorder(db, pub, log, now, QueueSize)
}

func newRecorder(db *store.DB, pub *Broadcaster, log *slog.Logger, now func() time.Time, size int) *Recorder {
	if now == nil {
		now = time.Now
	}
	return &Recorder{db: db, pub: pub, log: log, now: now, ch: make(chan Row, size)}
}

// Record queues r. A zero TS is set to now. Invalid rows are dropped.
func (r *Recorder) Record(row Row) {
	if r == nil {
		return
	}
	if row.TS.IsZero() {
		row.TS = r.now()
	}
	row, err := Sanitize(row)
	if err != nil {
		r.log.Warn("activity row rejected", "action", row.Action)
		return
	}
	select {
	case r.ch <- row:
	default:
		r.dropped.Add(1)
	}
}

// Dropped is activity_dropped_total: rows lost because the queue was full.
func (r *Recorder) Dropped() int64 { return r.dropped.Load() }

// Written counts rows stored since start (tests use it to wait for the writer).
func (r *Recorder) Written() int64 { return r.written.Load() }

// Run writes queued rows until ctx ends, then flushes what is queued (bounded
// by flushTimeout) and returns. Call it once.
func (r *Recorder) Run(ctx context.Context) {
	tick := time.NewTicker(dropLogPeriod)
	defer tick.Stop()
	var lastDropped int64
	batch := make([]Row, 0, BatchSize)
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	flush := func(wctx context.Context) {
		if len(batch) == 0 {
			return
		}
		rows := batch
		batch = make([]Row, 0, BatchSize)
		if err := r.db.WriteTx(wctx, func(tx *sql.Tx) error { return insert(wctx, tx, rows) }); err != nil {
			r.dropped.Add(int64(len(rows)))
			r.log.Error("activity write failed", "rows", len(rows), "err", err)
			return
		}
		r.written.Add(int64(len(rows)))
		r.pub.Publish(rows...)
	}
	for {
		select {
		case row := <-r.ch:
			if len(batch) == 0 {
				timer.Reset(BatchInterval)
			}
			batch = append(batch, row)
			if len(batch) >= BatchSize {
				timer.Stop()
				flush(ctx)
			}
		case <-timer.C:
			flush(ctx)
		case <-tick.C:
			if d := r.dropped.Load(); d != lastDropped {
				r.log.Warn("activity rows dropped (queue full or write failed)", "activity_dropped_total", d)
				lastDropped = d
			}
		case <-ctx.Done():
			timer.Stop()
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), flushTimeout)
			defer cancel()
			for {
				select {
				case row := <-r.ch:
					batch = append(batch, row)
					if len(batch) >= BatchSize {
						flush(fctx)
					}
					continue
				default:
				}
				break
			}
			flush(fctx)
			return
		}
	}
}
