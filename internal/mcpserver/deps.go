// SPDX-License-Identifier: AGPL-3.0-only

// Package mcpserver registers Folio's MCP tools, resources and prompts on a
// go-sdk server, maps engine errors to structured tool errors and serves the
// stateless Streamable HTTP handler.
package mcpserver

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/vocdoni/folio/internal/activity"
	"github.com/vocdoni/folio/internal/assets"
	"github.com/vocdoni/folio/internal/catalog"
	"github.com/vocdoni/folio/internal/config"
	"github.com/vocdoni/folio/internal/documents"
	"github.com/vocdoni/folio/internal/model"
	"github.com/vocdoni/folio/internal/render"
	"github.com/vocdoni/folio/internal/review"
	"github.com/vocdoni/folio/internal/store"
)

// Rasterizer renders one page of a stored PDF (render.Engine implements it).
type Rasterizer interface {
	RasterizePDFPage(ctx context.Context, pdf []byte, page, ppi int) ([]byte, error)
}

// Deps are the services the MCP layer calls. Every field except Reviewer is
// required; New panics on a missing one (a wiring bug, not external input).
type Deps struct {
	Catalog   *catalog.Service
	Documents *documents.Service
	Assets    *assets.Service
	Validator catalog.Renderer // render.Pipeline; validate_document runs it with ValidateOnly
	Raster    Rasterizer       // pages beyond the stored previews
	Queue     *render.Queue    // on-demand rasterisation takes a render slot
	Blobs     *store.Blobs
	DB        *store.DB
	Reviewer  review.Reviewer    // nil: review_document returns unavailable
	Activity  *activity.Recorder // nil: tool calls are not recorded in the activity feed
	Config    *config.Config
	Version   string
	Sandbox   string   // sandbox level reported by get_capabilities
	Packages  []string // vendored typst packages (@preview/name:version)
	Logger    *slog.Logger
	Now       func() time.Time
	// ProgressInterval is the heartbeat period of progress notifications
	// during long calls; 0 means 15 s (proxies idle-close streams after a
	// few minutes without data).
	ProgressInterval time.Duration
}

// LocalActor is the actor recorded when the server runs without auth
// (FOLIO_INSECURE_NO_AUTH, local development only).
const LocalActor = "local"

// audit records a failed mutating call. Successful calls are audited by the
// services inside their own transactions.
func (d *Deps) audit(ctx context.Context, actor, action, kind, id, outcome string) {
	actx := context.WithoutCancel(ctx) // record the failure even if the client went away
	err := d.DB.WriteTx(actx, func(tx *sql.Tx) error {
		return store.Audit(actx, tx, d.Now(), actor, action, kind, id, outcome, nil)
	})
	if err != nil {
		d.Logger.Error("audit write failed", "action", action, "err", err)
	}
}

// record queues one activity row for a tool call (errorCode "" = ok). It
// never blocks the call.
func (d *Deps) record(tool, actor, kind, id, errorCode string, dur time.Duration, detail activity.Detail) {
	if d.Activity == nil {
		return
	}
	outcome := activity.OutcomeOK
	if errorCode != "" {
		outcome = activity.OutcomeErr
	}
	d.Activity.Record(activity.Row{TS: d.Now(), Source: activity.SourceMCP, Actor: actor, Action: tool, TargetKind: kind, TargetID: id,
		Outcome: outcome, ErrorCode: errorCode, DurationMS: dur.Milliseconds(), Detail: detail})
}

// findingCounts summarises findings for the activity detail.
func findingCounts(fs []model.Finding) activity.Detail {
	var d activity.Detail
	for _, f := range fs {
		if f.Severity == model.SeverityError {
			d.FindingsErrors++
		} else {
			d.FindingsWarnings++
		}
	}
	return d
}
