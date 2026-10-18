// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/vocdoni/folio/internal/activity"
	"github.com/vocdoni/folio/internal/model"
)

// CSP is the Content-Security-Policy of every /ui response.
const CSP = "default-src 'self'; img-src 'self' data: blob:; style-src 'self'; script-src 'self'; connect-src 'self'; " +
	"font-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

// HeaderUI is the CSRF header every non-GET API request must carry.
const HeaderUI = "X-Folio-UI"

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", CSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("X-Robots-Tag", "noindex, nofollow")
		next.ServeHTTP(w, r)
	})
}

// APIError is the error body of the UI API, the same shape as MCP tool
// errors: {"error": {code, message, retryable, details}}.
type APIError struct {
	Status    int             `json:"-"`
	Code      string          `json:"code"`
	Message   string          `json:"message"`
	Retryable bool            `json:"retryable"`
	Details   []model.Finding `json:"details"`
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

func apiErr(status int, code, msg string) *APIError {
	return &APIError{Status: status, Code: code, Message: msg, Retryable: status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable}
}

// statusOf maps engine error codes to HTTP statuses.
var statusOf = map[model.ErrorCode]int{
	model.ErrInvalidInput: http.StatusBadRequest, model.ErrValidationFailed: http.StatusUnprocessableEntity,
	model.ErrNotFound: http.StatusNotFound, model.ErrConflict: http.StatusConflict, model.ErrArchived: http.StatusConflict,
	model.ErrTooLarge: http.StatusRequestEntityTooLarge, model.ErrBusy: http.StatusServiceUnavailable,
	model.ErrTimeout: http.StatusGatewayTimeout, model.ErrRenderFailed: http.StatusUnprocessableEntity,
	model.ErrUnavailable: http.StatusServiceUnavailable, model.ErrInternal: http.StatusInternalServerError,
}

// toAPIError maps any error to an APIError. Only *model.Error and *APIError
// messages are shown; anything else is a generic internal error (logged).
func toAPIError(err error) (*APIError, bool) {
	if ae, ok := errors.AsType[*APIError](err); ok {
		return ae, false
	}
	if me, ok := errors.AsType[*model.Error](err); ok {
		st := statusOf[me.Code]
		if st == 0 {
			st = http.StatusInternalServerError
		}
		d := me.Details
		if d == nil {
			d = []model.Finding{}
		}
		return &APIError{Status: st, Code: string(me.Code), Message: me.Message, Retryable: me.Retryable || me.Code == model.ErrBusy, Details: d}, me.Code == model.ErrInternal
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return apiErr(http.StatusGatewayTimeout, string(model.ErrTimeout), "the operation exceeded its time limit"), false
	}
	if errors.Is(err, context.Canceled) {
		return apiErr(http.StatusServiceUnavailable, string(model.ErrUnavailable), "the request was cancelled"), false
	}
	if mbe, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return apiErr(http.StatusRequestEntityTooLarge, string(model.ErrTooLarge), fmt.Sprintf("the request body exceeds %d bytes", mbe.Limit)), false
	}
	return apiErr(http.StatusInternalServerError, string(model.ErrInternal), "internal error"), true
}

type errLoggerKey struct{}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	ae, internal := toAPIError(err)
	if ae.Details == nil {
		ae.Details = []model.Finding{}
	}
	if internal {
		if log, ok := r.Context().Value(errLoggerKey{}).(func(error)); ok {
			log(err)
		}
	}
	if rw, ok := w.(*recWriter); ok {
		rw.code = ae.Code
	}
	if ae.Status == http.StatusServiceUnavailable && ae.Retryable && w.Header().Get("Retry-After") == "" {
		w.Header().Set("Retry-After", "5")
	}
	writeJSON(w, ae.Status, map[string]*APIError{"error": ae})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(true)
	if err := enc.Encode(v); err != nil {
		http.Error(w, `{"error":{"code":"internal","message":"internal error","retryable":false,"details":[]}}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// decodeJSON decodes one JSON object into v, rejecting unknown fields and
// trailing data.
func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if mbe, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return mbe
		}
		return &model.Error{Code: model.ErrInvalidInput, Message: "the request body is not valid JSON for this route: " + err.Error()}
	}
	if dec.More() {
		return &model.Error{Code: model.ErrInvalidInput, Message: "the request body has trailing data"}
	}
	return nil
}

// route describes an API route: public routes need no session; action names
// the activity entry of non-GET requests; kind is the target kind (the target
// id is the {id} path value unless the handler notes another one).
type route struct {
	public bool
	action string
	kind   string
}

// recWriter captures the status and error code for the activity row.
type recWriter struct {
	http.ResponseWriter
	status int
	code   string
}

func (rw *recWriter) WriteHeader(code int) {
	if rw.status == 0 {
		rw.status = code
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *recWriter) Write(b []byte) (int, error) {
	if rw.status == 0 {
		rw.status = http.StatusOK
	}
	return rw.ResponseWriter.Write(b)
}

func (rw *recWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rw *recWriter) Unwrap() http.ResponseWriter { return rw.ResponseWriter }

type noteKey struct{}

type note struct {
	targetID string
	detail   activity.Detail
}

// noteActivity sets the activity target id ("" keeps the path id) and detail
// of the current request.
func noteActivity(r *http.Request, targetID string, d activity.Detail) {
	if n, ok := r.Context().Value(noteKey{}).(*note); ok {
		if targetID != "" {
			n.targetID = targetID
		}
		n.detail = d
	}
}

// sameOrigin reports whether an Origin header matches FOLIO_PUBLIC_URL
// (scheme, host and port).
func (s *Server) sameOrigin(origin string) bool {
	return strings.EqualFold(strings.TrimRight(origin, "/"), s.origin)
}

// api registers an API route with its body limit, CSRF checks, session
// check and activity recording.
func (s *Server) api(mux *http.ServeMux, pattern string, rt route, h http.HandlerFunc) {
	mux.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		start := s.o.Now()
		ctx := context.WithValue(r.Context(), errLoggerKey{}, func(err error) {
			s.o.Logger.ErrorContext(r.Context(), "web UI request failed", "route", pattern, "err", err)
		})
		n := &note{targetID: r.PathValue("id")}
		ctx = context.WithValue(ctx, noteKey{}, n)
		r = r.WithContext(ctx)
		rw := &recWriter{ResponseWriter: w}
		mutating := r.Method != http.MethodGet && r.Method != http.MethodHead
		if mutating && rt.action != "" {
			defer func() { s.recordUI(rt, n, rw, start) }()
		}
		if mutating {
			if r.Header.Get(HeaderUI) != "1" {
				writeError(rw, r, apiErr(http.StatusForbidden, "forbidden", "missing the "+HeaderUI+" header"))
				return
			}
			if o := r.Header.Get("Origin"); o != "" && !s.sameOrigin(o) {
				writeError(rw, r, apiErr(http.StatusForbidden, "forbidden", "cross-origin request refused"))
				return
			}
			r.Body = http.MaxBytesReader(rw, r.Body, s.o.Config.MaxRequestBytes)
		}
		if !rt.public {
			ss, ok := s.currentSession(r)
			if !ok {
				writeError(rw, r, apiErr(http.StatusUnauthorized, "unauthorized", "sign in to continue"))
				return
			}
			r = r.WithContext(withSession(r.Context(), ss))
		}
		h(rw, r)
	}))
}

func (s *Server) recordUI(rt route, n *note, rw *recWriter, start time.Time) {
	if s.o.Activity == nil {
		return
	}
	outcome, code := activity.OutcomeOK, ""
	if rw.status >= 400 {
		outcome, code = activity.OutcomeErr, rw.code
	}
	kind := rt.kind
	if n.targetID == "" {
		kind = ""
	}
	s.o.Activity.Record(activity.Row{TS: s.o.Now(), Source: activity.SourceUI, Actor: Actor, Action: rt.action, TargetKind: kind,
		TargetID: n.targetID, Outcome: outcome, ErrorCode: code, DurationMS: s.o.Now().Sub(start).Milliseconds(), Detail: n.detail})
}
