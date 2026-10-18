// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// AI job limits.
const (
	jobsPerSession  = 1                // running jobs per session
	jobsPerInstance = 4                // running jobs per instance
	jobTTL          = 15 * time.Minute // results kept after completion
	jobBudget       = 4 * time.Minute  // hard wall-clock budget per job
)

// Job states.
const (
	jobQueued  = "queued"
	jobRunning = "running"
	jobDone    = "done"
	jobError   = "error"
)

// JobOut is GET /ui/api/ai/jobs/{id}.
type JobOut struct {
	ID        string    `json:"job_id"`
	Kind      string    `json:"kind"` // template-draft | document-plan | brand-suggest
	State     string    `json:"state"`
	Step      string    `json:"step,omitempty"`
	StartedAt time.Time `json:"started_at"`
	Result    any       `json:"result,omitempty"`
	Error     *APIError `json:"error,omitempty"`
}

type job struct {
	JobOut
	session  string
	finished time.Time
	cancel   context.CancelFunc
}

// jobRunner runs AI jobs in memory: at most one running per session and
// four per instance, a hard four-minute budget each, results kept fifteen
// minutes, cancelled on logout and lost on restart.
type jobRunner struct {
	mu   sync.Mutex
	now  func() time.Time
	jobs map[string]*job
	wg   sync.WaitGroup
	base context.Context
	log  *slog.Logger
}

func newJobRunner(base context.Context, now func() time.Time, log *slog.Logger) *jobRunner {
	return &jobRunner{now: now, jobs: map[string]*job{}, base: base, log: log}
}

var errAIBusy = apiErr(http.StatusTooManyRequests, "ai_busy", "the assistant is already working on a request; wait for it to finish")

// sweep forgets finished jobs older than jobTTL; the caller holds mu.
func (r *jobRunner) sweep() {
	now := r.now()
	for id, j := range r.jobs {
		if !j.finished.IsZero() && now.Sub(j.finished) > jobTTL {
			delete(r.jobs, id)
		}
	}
}

// start runs fn as a job owned by session. admit is called under the lock
// once the concurrency limits allow the job (quota accounting); an error
// from it refuses the job.
func (r *jobRunner) start(session, kind string, admit func() error, fn func(ctx context.Context, step func(string)) (any, error)) (string, error) {
	id, err := randomID()
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	r.sweep()
	mine, all := 0, 0
	for _, j := range r.jobs {
		if j.finished.IsZero() {
			all++
			if j.session == session {
				mine++
			}
		}
	}
	if mine >= jobsPerSession || all >= jobsPerInstance {
		r.mu.Unlock()
		return "", errAIBusy
	}
	if err := admit(); err != nil {
		r.mu.Unlock()
		return "", err
	}
	ctx, cancel := context.WithTimeout(r.base, jobBudget)
	j := &job{JobOut: JobOut{ID: id, Kind: kind, State: jobQueued, StartedAt: r.now()}, session: session, cancel: cancel}
	r.jobs[id] = j
	r.wg.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		defer cancel()
		r.update(j, func() { j.State = jobRunning })
		res, err := fn(ctx, func(s string) { r.update(j, func() { j.Step = s }) })
		r.update(j, func() {
			j.finished = r.now()
			if err != nil {
				switch {
				case errors.Is(ctx.Err(), context.Canceled):
					err = apiErr(http.StatusServiceUnavailable, "cancelled", "the request was cancelled (you signed out or the server restarted)")
				case errors.Is(ctx.Err(), context.DeadlineExceeded):
					err = apiErr(http.StatusGatewayTimeout, "timeout", fmt.Sprintf("the assistant took longer than %s; try again with a shorter input", jobBudget))
				}
				ae, internal := toAPIError(err)
				if internal {
					r.log.Error("web UI assistant job failed", "kind", kind, "err", err)
				}
				j.State, j.Error, j.Step = jobError, ae, ""
				return
			}
			j.State, j.Result, j.Step = jobDone, res, ""
		})
	}()
	return id, nil
}

func (r *jobRunner) update(j *job, f func()) {
	r.mu.Lock()
	f()
	r.mu.Unlock()
}

// get returns a snapshot of the job when session owns it.
func (r *jobRunner) get(id, session string) (JobOut, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweep()
	j, ok := r.jobs[id]
	if !ok || j.session != session {
		return JobOut{}, false
	}
	return j.JobOut, true
}

// result returns the finished result of a job of session.
func (r *jobRunner) result(id, session string) (any, bool) {
	out, ok := r.get(id, session)
	return out.Result, ok && out.State == jobDone
}

// cancelSession cancels every running job of session (logout).
func (r *jobRunner) cancelSession(session string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, j := range r.jobs {
		if j.session == session && j.finished.IsZero() {
			j.cancel()
		}
	}
}

// quotas enforces FOLIO_AI_DAILY_CALLS (per UTC day, whole instance) and
// FOLIO_AI_SESSION_CALLS (per session, sliding hour).
type quotas struct {
	mu         sync.Mutex
	now        func() time.Time
	daily      int
	perSession int
	day        time.Time
	usedToday  int
	bySession  map[string][]time.Time
}

func newQuotas(daily, perSession int, now func() time.Time) *quotas {
	return &quotas{daily: daily, perSession: perSession, now: now, bySession: map[string][]time.Time{}}
}

// QuotaOut is the quota part of GET /ui/api/ai/status.
type QuotaOut struct {
	DailyLimit       int       `json:"daily_limit"`
	DailyRemaining   int       `json:"daily_remaining"`
	DailyResetsAt    time.Time `json:"daily_resets_at"`
	SessionLimit     int       `json:"session_limit"` // per hour
	SessionRemaining int       `json:"session_remaining"`
}

// roll resets the daily counter at 00:00 UTC; the caller holds mu.
func (q *quotas) roll() time.Time {
	now := q.now()
	day := now.UTC().Truncate(24 * time.Hour)
	if !day.Equal(q.day) {
		q.day, q.usedToday = day, 0
	}
	return now
}

func (q *quotas) status(session string) QuotaOut {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.roll()
	recent := prune(q.bySession[session], now.Add(-time.Hour))
	q.bySession[session] = recent
	return QuotaOut{DailyLimit: q.daily, DailyRemaining: max(0, q.daily-q.usedToday), DailyResetsAt: q.day.Add(24 * time.Hour),
		SessionLimit: q.perSession, SessionRemaining: max(0, q.perSession-len(recent))}
}

// take consumes one call or explains which quota is exhausted.
func (q *quotas) take(session string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.roll()
	recent := prune(q.bySession[session], now.Add(-time.Hour))
	if q.usedToday >= q.daily {
		reset := q.day.Add(24 * time.Hour)
		return apiErr(http.StatusTooManyRequests, "ai_quota",
			fmt.Sprintf("the daily assistant limit (%d) is used up; it resets at %s UTC", q.daily, reset.Format("15:04")))
	}
	if len(recent) >= q.perSession {
		return apiErr(http.StatusTooManyRequests, "ai_quota",
			fmt.Sprintf("you used the assistant %d times in the last hour; try again after %s UTC", q.perSession, recent[0].Add(time.Hour).Format("15:04")))
	}
	q.usedToday++
	q.bySession[session] = append(recent, now)
	return nil
}
