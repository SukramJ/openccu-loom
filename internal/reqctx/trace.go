// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reqctx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// W3C `traceparent` header (Trace Context, level 1):
//
//	version "-" trace_id "-" parent_id "-" trace_flags
//	    00      32-hex      16-hex        2-hex
//
// openccu-loom only consumes/produces version `00`; other versions are
// accepted on input (forward-compat) but emitted as `00`.

// TraceparentHeader is the canonical name of the W3C header.
const TraceparentHeader = "traceparent"

// traceFlagSampled marks a trace as recorded. We always set it because
// openccu-loom is the trace originator for self-generated requests.
const traceFlagSampled = "01"

// NewTraceID returns a freshly generated 32-character lowercase hex
// trace identifier. The returned string is never the all-zero
// identifier (`00000000000000000000000000000000`) — the W3C spec
// forbids that as a valid trace ID, so we retry on the (astronomically
// unlikely) zero draw.
func NewTraceID() string {
	for {
		var b [16]byte
		// crypto/rand.Read only errors when the OS entropy source is
		// unavailable, a condition this loop could not recover from either
		// way. A failed read leaves b at its zero value, which the
		// all-zero check below already retries, so there is nothing more
		// useful to do with the error here.
		_, _ = rand.Read(b[:])
		id := hex.EncodeToString(b[:])
		if id != "00000000000000000000000000000000" {
			return id
		}
	}
}

// NewSpanID returns a freshly generated 16-character lowercase hex
// span identifier. Mirrors [NewTraceID] for the smaller span size and
// guards against the W3C-forbidden all-zero value.
func NewSpanID() string {
	for {
		var b [8]byte
		// See the matching comment in [NewTraceID]: a failed read leaves b
		// zeroed, which the all-zero check below already retries.
		_, _ = rand.Read(b[:])
		id := hex.EncodeToString(b[:])
		if id != "0000000000000000" {
			return id
		}
	}
}

// ParseTraceparent extracts the trace_id, span_id (parent_id field in
// the header), and trace_flags from a W3C `traceparent` header value.
// Returns ok=false when the header is malformed; callers should then
// generate a fresh trace via [NewTraceID]/[NewSpanID] instead.
//
// The parser is lenient on the version field: any 2-hex version is
// accepted, but the remaining segments are checked for the exact
// lengths (32 / 16 / 2) and hex validity that level-1 mandates.
func ParseTraceparent(header string) (traceID, spanID, flags string, ok bool) {
	parts := strings.Split(strings.TrimSpace(header), "-")
	if len(parts) != 4 {
		return "", "", "", false
	}
	version, traceID, spanID, flags := parts[0], parts[1], parts[2], parts[3]
	if !isHex(version, 2) || !isHex(traceID, 32) || !isHex(spanID, 16) || !isHex(flags, 2) {
		return "", "", "", false
	}
	if traceID == "00000000000000000000000000000000" || spanID == "0000000000000000" {
		return "", "", "", false
	}
	return strings.ToLower(traceID), strings.ToLower(spanID), strings.ToLower(flags), true
}

// FormatTraceparent emits a W3C-compliant `traceparent` header for the
// given trace/span pair. Sampled controls the trace-flags byte; pass
// true for normal recorded traces.
func FormatTraceparent(traceID, spanID string, sampled bool) string {
	flags := "00"
	if sampled {
		flags = traceFlagSampled
	}
	return "00-" + traceID + "-" + spanID + "-" + flags
}

// StartChildSpan returns a context derived from ctx whose
// [RequestContext] carries a freshly generated SpanID and whose
// ParentSpanID points at the previous SpanID. The TraceID is preserved
// unchanged. If ctx has no [RequestContext] yet, a new one is created
// with a freshly generated TraceID + SpanID (no parent).
//
// Use this at every span boundary that should appear as a separate
// node in the trace tree: coordinator entry, client RPC dispatch,
// transport call, scheduled job iteration. Logging within the span
// will then carry the child SpanID, and metrics aggregation can
// reconstruct the call tree.
func StartChildSpan(ctx context.Context) context.Context {
	rc, ok := FromContext(ctx)
	if !ok {
		rc = RequestContext{
			TraceID: NewTraceID(),
			SpanID:  NewSpanID(),
		}
		return WithRequestContext(ctx, rc)
	}
	if rc.TraceID == "" {
		rc.TraceID = NewTraceID()
	}
	rc.ParentSpanID = rc.SpanID
	rc.SpanID = NewSpanID()
	return WithRequestContext(ctx, rc)
}

// TraceparentFromContext renders the current span as a W3C
// `traceparent` header value, or returns "" when the context carries
// no trace identifiers yet.
func TraceparentFromContext(ctx context.Context) string {
	rc, ok := FromContext(ctx)
	if !ok || rc.TraceID == "" || rc.SpanID == "" {
		return ""
	}
	return FormatTraceparent(rc.TraceID, rc.SpanID, true)
}

func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
