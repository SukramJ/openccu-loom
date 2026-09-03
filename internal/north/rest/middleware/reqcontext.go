// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package middleware

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/pkg/hmreqctx"
)

// ReqContext installs a [hmreqctx.RequestContext] for every HTTP request after
// [RequestID] has run. Downstream code (REST handler, domain core, CCU
// transport) inherits the request id, the operation tag (`METHOD path`), and
// the start timestamp. The [hmreqctx.ContextHandler] in the logging chain picks
// these fields up automatically and emits them as structured slog attributes.
//
// CentralName and InterfaceID are intentionally NOT filled here — at the
// outer middleware position chi has not yet resolved URL parameters, so
// neither value is available. Two paths fill the central scope instead:
// routes whose path names the central attach [CentralScope], which reads
// the URL parameter once routing has run, and a daemon wired to a single
// central captures the name at boot through [ReqContextWithCentral].
// A request that matches neither is logged without a central scope.
//
// The middleware MUST be mounted before [Logger] but after [RequestID] so
// that the request id is already populated when the context is constructed.
func ReqContext(next http.Handler) http.Handler {
	return ReqContextWithCentral("")(next)
}

// ReqContextWithCentral builds a middleware that installs a
// [hmreqctx.RequestContext] with [centralName] pre-filled. Used by the
// daemon when the REST router is wired to a single central — the
// closure captures the central scope at boot time, so every downstream
// slog record carries `central_name` automatically.
//
// Pass an empty string to fall back to the URL-parameter model, where
// [CentralScope] fills the scope per route once chi has matched.
func ReqContextWithCentral(centralName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			traceID, spanID, parentSpanID := traceFromHeader(r.Header.Get(hmreqctx.TraceparentHeader))
			rc := hmreqctx.RequestContext{
				RequestID:    RequestIDFrom(r.Context()),
				Operation:    r.Method + " " + r.URL.Path,
				StartedAt:    time.Now(),
				CentralName:  centralName,
				TraceID:      traceID,
				SpanID:       spanID,
				ParentSpanID: parentSpanID,
			}
			ctx := hmreqctx.WithRequestContext(r.Context(), rc)
			w.Header().Set(hmreqctx.TraceparentHeader, hmreqctx.FormatTraceparent(traceID, spanID, true))
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
		if tid, sid, _, ok := hmreqctx.ParseTraceparent(header); ok {
			return tid, hmreqctx.NewSpanID(), sid
		}
	}
	return hmreqctx.NewTraceID(), hmreqctx.NewSpanID(), ""
}

// SetCentralName replaces (or installs) the [hmreqctx.RequestContext] in
// r.Context() with CentralName set, so downstream slog records carry the
// central scope. [CentralFromURL] is its caller once the URL parameter is
// resolved; it stays exported because it is the only way to install the
// scope on a request that reached the handler some other way, and it
// rebuilds the whole request context when [ReqContext] never ran rather
// than dropping the scope silently.
func SetCentralName(r *http.Request, name string) *http.Request {
	rc, ok := hmreqctx.FromContext(r.Context())
	if !ok {
		traceID, spanID, parentSpanID := traceFromHeader(r.Header.Get(hmreqctx.TraceparentHeader))
		rc = hmreqctx.RequestContext{
			RequestID:    RequestIDFrom(r.Context()),
			Operation:    r.Method + " " + r.URL.Path,
			StartedAt:    time.Now(),
			TraceID:      traceID,
			SpanID:       spanID,
			ParentSpanID: parentSpanID,
		}
	}
	rc.CentralName = name
	return r.WithContext(hmreqctx.WithRequestContext(r.Context(), rc))
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
