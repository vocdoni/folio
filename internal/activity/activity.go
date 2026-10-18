// SPDX-License-Identifier: AGPL-3.0-only

// Package activity records every MCP tool call and web UI action in the
// activity table and fans new rows out to live subscribers (the web UI's
// server-sent event stream). Rows carry ids, names, outcomes, durations and
// an allowlisted detail object: never request bodies, document content or
// tokens. Recording never blocks the caller: rows go through a bounded
// channel and are dropped (and counted) when it is full.
package activity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/vocdoni/folio/internal/store"
)

// Sources and outcomes.
const (
	SourceMCP  = "mcp"
	SourceUI   = "ui"
	OutcomeOK  = "ok"
	OutcomeErr = "error"
)

// TimeFormat is the stored timestamp format (sortable, millisecond precision).
const TimeFormat = "2006-01-02T15:04:05.000Z"

// Detail is the allowlisted detail object: integers, booleans and the
// formats enum only, never strings taken from a request.
type Detail struct {
	Revision         int      `json:"revision,omitempty"`
	Version          int      `json:"version,omitempty"`
	Pages            int      `json:"pages,omitempty"`
	Bytes            int64    `json:"bytes,omitempty"`
	Formats          []string `json:"formats,omitempty"` // pdf | docx
	Unchanged        bool     `json:"unchanged,omitempty"`
	Purge            bool     `json:"purge,omitempty"`
	Archived         *bool    `json:"archived,omitempty"`
	FindingsErrors   int      `json:"findings_errors,omitempty"`
	FindingsWarnings int      `json:"findings_warnings,omitempty"`
}

// Row is one activity entry.
type Row struct {
	ID         int64     `json:"id"`
	TS         time.Time `json:"ts"`
	Source     string    `json:"source"` // mcp | ui
	Actor      string    `json:"actor"`
	Action     string    `json:"action"` // MCP tool name or ui.<action>
	TargetKind string    `json:"target_kind,omitempty"`
	TargetID   string    `json:"target_id,omitempty"`
	Outcome    string    `json:"outcome"`              // ok | error
	ErrorCode  string    `json:"error_code,omitempty"` // model.ErrorCode of a failed call
	DurationMS int64     `json:"duration_ms"`
	Detail     Detail    `json:"detail"`
}

var (
	idRE      = regexp.MustCompile(`^[A-Za-z0-9._:@-]{1,128}$`)
	nameRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	kindRE    = regexp.MustCompile(`^[a-z]{1,32}$`)
	actorRE   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	codeRE    = regexp.MustCompile(`^[a-z_]{1,32}$`)
	errBadRow = errors.New("activity: invalid row")
)

// Sanitize bounds every field to its allowlist: identifiers that do not look
// like ids are dropped and detail formats are reduced to pdf/docx. It fails
// only for rows without a valid source, actor, action or outcome.
func Sanitize(r Row) (Row, error) {
	if (r.Source != SourceMCP && r.Source != SourceUI) || !actorRE.MatchString(r.Actor) || !nameRE.MatchString(r.Action) ||
		(r.Outcome != OutcomeOK && r.Outcome != OutcomeErr) {
		return r, errBadRow
	}
	if !kindRE.MatchString(r.TargetKind) || !idRE.MatchString(r.TargetID) {
		r.TargetKind, r.TargetID = "", ""
	}
	if !codeRE.MatchString(r.ErrorCode) || r.Outcome == OutcomeOK {
		r.ErrorCode = ""
	}
	r.DurationMS = max(r.DurationMS, 0)
	var fs []string
	for _, f := range r.Detail.Formats {
		if (f == "pdf" || f == "docx") && len(fs) < 2 {
			fs = append(fs, f)
		}
	}
	r.Detail.Formats = fs
	return r, nil
}

// insert writes rows inside tx and sets their ids.
func insert(ctx context.Context, tx *sql.Tx, rows []Row) error {
	for i := range rows {
		r := &rows[i]
		detail, err := json.Marshal(r.Detail)
		if err != nil {
			return fmt.Errorf("activity: detail: %w", err)
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO activity (ts, source, actor, action, target_kind, target_id, outcome, error_code, duration_ms, detail)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
			r.TS.UTC().Format(TimeFormat), r.Source, r.Actor, r.Action, r.TargetKind, r.TargetID, r.Outcome, r.ErrorCode, r.DurationMS, string(detail)).
			Scan(&r.ID); err != nil {
			return fmt.Errorf("activity: insert: %w", err)
		}
	}
	return nil
}

// Filter selects rows for List. Empty fields match everything.
type Filter struct {
	Source  string
	Actor   string
	Action  string
	Outcome string
	Before  int64 // cursor: only rows with id < Before (0 = newest)
	Limit   int   // default 50, max 200
}

// List returns rows newest first and the cursor of the next page (0 = none).
func List(ctx context.Context, db *store.DB, f Filter) ([]Row, int64, error) {
	limit := f.Limit
	switch {
	case limit <= 0:
		limit = 50
	case limit > 200:
		limit = 200
	}
	q := `SELECT id, ts, source, actor, action, target_kind, target_id, outcome, error_code, duration_ms, detail FROM activity WHERE 1=1`
	var args []any
	if f.Before > 0 {
		q += ` AND id < ?`
		args = append(args, f.Before)
	}
	for _, c := range []struct{ col, v string }{{"source", f.Source}, {"actor", f.Actor}, {"action", f.Action}, {"outcome", f.Outcome}} {
		if c.v != "" {
			q += ` AND ` + c.col + ` = ?`
			args = append(args, c.v)
		}
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := db.R.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("activity: list: %w", err)
	}
	defer rows.Close()
	out := []Row{}
	for rows.Next() {
		var r Row
		var ts, detail string
		if err := rows.Scan(&r.ID, &ts, &r.Source, &r.Actor, &r.Action, &r.TargetKind, &r.TargetID, &r.Outcome, &r.ErrorCode, &r.DurationMS, &detail); err != nil {
			return nil, 0, fmt.Errorf("activity: scan: %w", err)
		}
		r.TS, _ = time.Parse(TimeFormat, ts)
		_ = json.Unmarshal([]byte(detail), &r.Detail)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var next int64
	if len(out) > limit {
		out = out[:limit]
		next = out[limit-1].ID
	}
	return out, next, nil
}

// Facets are the distinct actors and actions seen, for the UI's filters.
type Facets struct {
	Actors  []string `json:"actors"`
	Actions []string `json:"actions"`
}

// ListFacets returns up to 200 distinct actors and actions, sorted.
func ListFacets(ctx context.Context, db *store.DB) (Facets, error) {
	f := Facets{Actors: []string{}, Actions: []string{}}
	for _, c := range []struct {
		col string
		dst *[]string
	}{{"actor", &f.Actors}, {"action", &f.Actions}} {
		rows, err := db.R.QueryContext(ctx, `SELECT DISTINCT `+c.col+` FROM activity ORDER BY 1 LIMIT 200`)
		if err != nil {
			return f, fmt.Errorf("activity: facets: %w", err)
		}
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				rows.Close()
				return f, err
			}
			*c.dst = append(*c.dst, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return f, err
		}
	}
	return f, nil
}

// RenderActions are the actions counted as renders in Summary.
var RenderActions = []string{"render_document", "ui.render"}

// Stats are the activity-derived figures of the overview.
type Stats struct {
	RendersToday int     `json:"renders_today"`  // ok renders since 00:00 UTC
	Calls24h     int     `json:"calls_24h"`      // rows in the last 24 h
	Errors24h    int     `json:"errors_24h"`     // error rows in the last 24 h
	ErrorRate24h float64 `json:"error_rate_24h"` // Errors24h / Calls24h (0 without rows)
}

// Summary computes Stats at now.
func Summary(ctx context.Context, db *store.DB, now time.Time) (Stats, error) {
	var s Stats
	day := now.UTC().Truncate(24 * time.Hour).Format(TimeFormat)
	since := now.UTC().Add(-24 * time.Hour).Format(TimeFormat)
	ph := strings.TrimSuffix(strings.Repeat("?,", len(RenderActions)), ",")
	args := []any{day}
	for _, a := range RenderActions {
		args = append(args, a)
	}
	if err := db.R.QueryRowContext(ctx, `SELECT count(*) FROM activity WHERE ts >= ? AND outcome = 'ok' AND action IN (`+ph+`)`, args...).Scan(&s.RendersToday); err != nil {
		return s, fmt.Errorf("activity: renders today: %w", err)
	}
	if err := db.R.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(outcome = 'error'), 0) FROM activity WHERE ts >= ?`, since).Scan(&s.Calls24h, &s.Errors24h); err != nil {
		return s, fmt.Errorf("activity: 24h: %w", err)
	}
	if s.Calls24h > 0 {
		s.ErrorRate24h = float64(s.Errors24h) / float64(s.Calls24h)
	}
	return s, nil
}

// Prune removes rows older than before and returns how many it removed.
func Prune(ctx context.Context, db *store.DB, before time.Time) (int64, error) {
	var n int64
	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM activity WHERE ts < ?`, before.UTC().Format(TimeFormat))
		if err != nil {
			return fmt.Errorf("activity: prune: %w", err)
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return n, err
}
