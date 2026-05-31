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
// the start timestamp. The [hmlog.RequestContextFilter] picks these fields up
// automatically and emits them as structured slog attributes.
//
// CentralName and InterfaceID are intentionally NOT filled here — at the
// outer middleware position chi has not yet resolved URL parameters, so
// neither value is available. Handlers that know the scope (e.g. after
// `chi.URLParam(r, "central_name")`) call [SetCentralName] /
// [reqctx.RequestContext.WithCentralName] to enrich the stored context with
// the central scope.
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

// CentralFromURL is a convenience that reads the chi `central_name`
// URL parameter, calls [SetCentralName] with it, and returns the
// enriched request. Handlers mounted on a route shaped like
// `/api/v1/centrals/{central_name}/...` should call this on entry.
func CentralFromURL(r *http.Request) *http.Request {
	name := chi.URLParam(r, "central_name")
	if name == "" {
		return r
	}
	return SetCentralName(r, name)
}
