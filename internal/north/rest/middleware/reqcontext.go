// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package middleware

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/reqctx"
)

// ReqContext installs a [reqctx.RequestContext] for every HTTP request after
// [RequestID] has run. Downstream code (REST handler, domain core, CCU
// transport) inherits the request id, the operation tag (`METHOD path`), and
// the start timestamp. The [reqctx.ContextHandler] in the logging chain picks
// these fields up automatically and emits them as structured slog attributes.
//
// CentralName and InterfaceID are intentionally NOT filled here — at the
// outer middleware position chi has not yet resolved URL parameters, so
// neither value is available. Routes whose path names the central attach
// [CentralScope] instead, which fills the scope once routing has run;
// handlers that resolve it some other way (a session, a body field) call
// [SetCentralName] directly.
//
// The middleware MUST be mounted before [Logger] but after [RequestID] so
// that the request id is already populated when the context is constructed.
func ReqContext(next http.Handler) http.Handler {
	return ReqContextWithCentral("")(next)
}

// ReqContextWithCentral builds a middleware that installs a
// [reqctx.RequestContext] with [centralName] pre-filled. Used by the
// daemon when the REST router is wired to a single central — the
// closure captures the central scope at boot time, so every downstream
// slog record carries `central_name` automatically without each
// handler needing to call [SetCentralName].
//
// Pass an empty string to fall back to the URL-parameter / handler-
// driven model (handlers call [SetCentralName] dynamically).
func ReqContextWithCentral(centralName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			traceID, spanID, parentSpanID := traceFromHeader(r.Header.Get(reqctx.TraceparentHeader))
			rc := reqctx.RequestContext{
				RequestID:    RequestIDFrom(r.Context()),
				Operation:    r.Method + " " + r.URL.Path,
				StartedAt:    time.Now(),
				CentralName:  centralName,
				TraceID:      traceID,
				SpanID:       spanID,
				ParentSpanID: parentSpanID,
			}
			ctx := reqctx.WithRequestContext(r.Context(), rc)
			w.Header().Set(reqctx.TraceparentHeader, reqctx.FormatTraceparent(traceID, spanID, true))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// traceFromHeader parses an incoming `traceparent` header. When the
// header is missing or malformed it generates a fresh trace pair, so
// every request always carries both IDs by the time it reaches the
// handler. The third return value is the parent span ID — the span
// the upstream caller was in when it forwarded to us — or "" when we
// originated the trace.
func traceFromHeader(header string) (traceID, spanID, parentSpanID string) {
	if header != "" {
		if tid, sid, _, ok := reqctx.ParseTraceparent(header); ok {
			return tid, reqctx.NewSpanID(), sid
		}
	}
	return reqctx.NewTraceID(), reqctx.NewSpanID(), ""
}

// SetCentralName replaces (or installs) the [reqctx.RequestContext] in
// r.Context() with CentralName set. Called from handlers that have
// already resolved the central from a URL parameter or session, so
// downstream slog records carry the central scope.
func SetCentralName(r *http.Request, name string) *http.Request {
	rc, ok := reqctx.FromContext(r.Context())
	if !ok {
		traceID, spanID, parentSpanID := traceFromHeader(r.Header.Get(reqctx.TraceparentHeader))
		rc = reqctx.RequestContext{
			RequestID:    RequestIDFrom(r.Context()),
			Operation:    r.Method + " " + r.URL.Path,
			StartedAt:    time.Now(),
			TraceID:      traceID,
			SpanID:       spanID,
			ParentSpanID: parentSpanID,
		}
	}
	rc.CentralName = name
	return r.WithContext(reqctx.WithRequestContext(r.Context(), rc))
}

// CentralFromURL reads the central-scoping URL parameter, calls
// [SetCentralName] with it, and returns the enriched request. Both
// spellings the router uses are accepted (`{central}` on the CCU-host
// and diagnostics routes, `{central_name}` on any route that spells it
// out); a request carrying neither is returned unchanged.
func CentralFromURL(r *http.Request) *http.Request {
	for _, param := range [...]string{"central", "central_name"} {
		if name := chi.URLParam(r, param); name != "" {
			return SetCentralName(r, name)
		}
	}
	return r
}

// CentralScope is [CentralFromURL] as middleware, for routes whose path
// names the central.
//
// It must be attached per route (chi `With`), not with `Use` on a
// router: a `Use` chain runs before that router has matched the request,
// so the URL parameter this reads is not populated yet and the
// middleware silently does nothing.
//
// Without it a multi-CCU daemon logs a CCU reboot with no central scope
// at all — the single-central deployment gets the name from
// [ReqContextWithCentral] at boot, which is exactly the case where the
// missing scope does not show.
func CentralScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, CentralFromURL(r))
	})
}
