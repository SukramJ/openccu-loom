// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/pkg/hmreqctx"
)

// TestReqContextPopulatesContext verifies that the middleware
// installs a [hmreqctx.RequestContext] with the request id, the raw
// path as Operation, and a non-zero StartedAt.
func TestReqContextPopulatesContext(t *testing.T) {
	var seen hmreqctx.RequestContext
	var ok bool

	leaf := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, ok = hmreqctx.FromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})

	r := chi.NewRouter()
	r.Use(RequestID)
	r.Use(ReqContext)
	r.Get("/api/v1/info", leaf)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
	req.Header.Set("X-Request-ID", "rid-test-1")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d want 204", rr.Code)
	}
	if !ok {
		t.Fatal("RequestContext not present in handler context")
	}
	if seen.RequestID != "rid-test-1" {
		t.Errorf("RequestID=%q want rid-test-1", seen.RequestID)
	}
	if seen.Operation != "GET /api/v1/info" {
		t.Errorf("Operation=%q want GET /api/v1/info", seen.Operation)
	}
	if seen.StartedAt.IsZero() {
		t.Error("StartedAt was not set")
	}
	if seen.CentralName != "" {
		t.Errorf("CentralName=%q want empty (the scope arrives per route via CentralScope)", seen.CentralName)
	}
}

// TestCentralFromURLPopulatesScope verifies the helper behind
// [CentralScope]: it enriches the request with the chi-resolved
// central_name URL parameter without losing the rest of the context.
func TestCentralFromURLPopulatesScope(t *testing.T) {
	var seen hmreqctx.RequestContext

	leaf := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CentralScope calls this once chi has matched the route.
		r = CentralFromURL(r)
		seen, _ = hmreqctx.FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	r := chi.NewRouter()
	r.Use(RequestID)
	r.Use(ReqContext)
	r.Get("/api/v1/centrals/{central_name}/devices", leaf)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/centrals/ccu-01/devices", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if seen.CentralName != "ccu-01" {
		t.Fatalf("CentralName=%q want ccu-01", seen.CentralName)
	}
	if seen.RequestID == "" {
		t.Error("RequestID was lost during enrichment")
	}
}

// TestCentralScopeFillsTheScopePerRoute pins the production mechanism:
// the scope reaches the log records because the route was assembled with
// [CentralScope], not because a handler asked for it. A sibling route
// without the middleware is logged unscoped — which is what makes the
// per-route attachment the thing worth asserting.
func TestCentralScopeFillsTheScopePerRoute(t *testing.T) {
	var seen hmreqctx.RequestContext

	leaf := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = hmreqctx.FromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})

	r := chi.NewRouter()
	r.Use(RequestID)
	r.Use(ReqContext)
	r.With(CentralScope).Get("/api/v1/centrals/{central_name}/devices", leaf)
	r.Get("/api/v1/centrals/{central_name}/programs", leaf)

	for _, tc := range []struct {
		path string
		want string
	}{
		{path: "/api/v1/centrals/ccu-01/devices", want: "ccu-01"},
		{path: "/api/v1/centrals/ccu-01/programs", want: ""},
	} {
		seen = hmreqctx.RequestContext{}
		req := httptest.NewRequest(http.MethodGet, tc.path, http.NoBody)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("%s: status=%d want 204", tc.path, rr.Code)
		}
		if seen.CentralName != tc.want {
			t.Errorf("%s: CentralName=%q want %q", tc.path, seen.CentralName, tc.want)
		}
	}
}

// TestCentralScopeAcceptsBothURLParamSpellings pins that the middleware
// covers the `{central}` routes as well as the spelled-out
// `{central_name}` ones — the router uses both.
func TestCentralScopeAcceptsBothURLParamSpellings(t *testing.T) {
	var seen hmreqctx.RequestContext

	leaf := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = hmreqctx.FromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})

	r := chi.NewRouter()
	r.Use(RequestID)
	r.Use(ReqContext)
	r.With(CentralScope).Get("/api/v1/system/ccu/{central}/reboot", leaf)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/ccu/ccu-02/reboot", http.NoBody)
	r.ServeHTTP(httptest.NewRecorder(), req)

	if seen.CentralName != "ccu-02" {
		t.Fatalf("CentralName=%q want ccu-02", seen.CentralName)
	}
}

// TestSetCentralNameWithoutPriorContext exercises the fallback path
// where SetCentralName is called on a request that never went through
// ReqContext (e.g. from a deeply-mounted handler bypassing the chain).
func TestSetCentralNameWithoutPriorContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, "/api/v1/centrals/ccu-99/devices/X", http.NoBody)
	enriched := SetCentralName(req, "ccu-99")
	rc, ok := hmreqctx.FromContext(enriched.Context())
	if !ok {
		t.Fatal("RequestContext missing after SetCentralName")
	}
	if rc.CentralName != "ccu-99" {
		t.Errorf("CentralName=%q want ccu-99", rc.CentralName)
	}
	if rc.Operation != "PUT /api/v1/centrals/ccu-99/devices/X" {
		t.Errorf("Operation=%q want PUT path", rc.Operation)
	}
}

// TestReqContextGeneratesTraceIDs verifies that, in the absence of an
// incoming `traceparent` header, the middleware fabricates a fresh
// W3C trace + span pair and echoes it in the response header so a
// client can correlate logs against its own request.
func TestReqContextGeneratesTraceIDs(t *testing.T) {
	var seen hmreqctx.RequestContext
	leaf := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = hmreqctx.FromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})

	r := chi.NewRouter()
	r.Use(RequestID)
	r.Use(ReqContext)
	r.Get("/api/v1/info", leaf)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if len(seen.TraceID) != 32 {
		t.Errorf("TraceID len=%d want 32 (%q)", len(seen.TraceID), seen.TraceID)
	}
	if len(seen.SpanID) != 16 {
		t.Errorf("SpanID len=%d want 16 (%q)", len(seen.SpanID), seen.SpanID)
	}
	if seen.ParentSpanID != "" {
		t.Errorf("ParentSpanID=%q want empty for self-originated trace", seen.ParentSpanID)
	}
	tp := rr.Header().Get(hmreqctx.TraceparentHeader)
	tid, sid, _, ok := hmreqctx.ParseTraceparent(tp)
	if !ok {
		t.Fatalf("response traceparent malformed: %q", tp)
	}
	if tid != seen.TraceID || sid != seen.SpanID {
		t.Errorf("response trace mismatch: got %s/%s want %s/%s", tid, sid, seen.TraceID, seen.SpanID)
	}
}

// TestReqContextAdoptsIncomingTraceparent verifies that a valid
// upstream `traceparent` header is honoured: the trace ID is reused,
// a fresh span ID is generated, and the upstream span becomes the
// parent.
func TestReqContextAdoptsIncomingTraceparent(t *testing.T) {
	const upstreamTrace = "abcdef0123456789abcdef0123456789"
	const upstreamSpan = "1122334455667788"

	var seen hmreqctx.RequestContext
	leaf := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = hmreqctx.FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	r := chi.NewRouter()
	r.Use(RequestID)
	r.Use(ReqContext)
	r.Get("/api/v1/info", leaf)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
	req.Header.Set(hmreqctx.TraceparentHeader, "00-"+upstreamTrace+"-"+upstreamSpan+"-01")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if seen.TraceID != upstreamTrace {
		t.Errorf("TraceID=%q want %q (adopted)", seen.TraceID, upstreamTrace)
	}
	if seen.ParentSpanID != upstreamSpan {
		t.Errorf("ParentSpanID=%q want %q (adopted)", seen.ParentSpanID, upstreamSpan)
	}
	if seen.SpanID == "" || seen.SpanID == upstreamSpan {
		t.Errorf("SpanID=%q must be fresh, not the upstream span", seen.SpanID)
	}
}

// TestReqContextMalformedTraceparentFallsBack verifies that an
// invalid incoming `traceparent` header is ignored and a fresh trace
// is generated, rather than the request inheriting a malformed
// identifier.
func TestReqContextMalformedTraceparentFallsBack(t *testing.T) {
	var seen hmreqctx.RequestContext
	leaf := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = hmreqctx.FromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})

	r := chi.NewRouter()
	r.Use(RequestID)
	r.Use(ReqContext)
	r.Get("/api/v1/info", leaf)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
	req.Header.Set(hmreqctx.TraceparentHeader, "garbage-not-a-traceparent")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if len(seen.TraceID) != 32 {
		t.Errorf("TraceID len=%d want 32 (%q)", len(seen.TraceID), seen.TraceID)
	}
	if seen.ParentSpanID != "" {
		t.Errorf("ParentSpanID=%q want empty (no parent inherited from malformed header)", seen.ParentSpanID)
	}
}
