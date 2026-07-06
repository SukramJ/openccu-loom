// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package observability

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/internal/reqctx"
)

// SpanExporter receives finished spans. Implementations MUST NOT block —
// Span.End is on the hot path; buffer and return immediately.
type SpanExporter interface {
	ExportSpan(*Span)
	Shutdown(ctx context.Context) error
}

// spanExporter holds the process-wide exporter. nil means export is
// disabled (the default). Stored as *SpanExporter so atomic.Pointer
// can distinguish "unset" (nil pointer) from "exporter is nil interface".
var spanExporter atomic.Pointer[SpanExporter]

// SetSpanExporter installs (or, when nil, removes) the process-wide span
// exporter. Returns the previous exporter. Default is nil (export disabled).
// The call is safe for concurrent use; the swap is atomic.
func SetSpanExporter(e SpanExporter) SpanExporter {
	var p *SpanExporter
	if e != nil {
		p = &e
	}
	prev := spanExporter.Swap(p)
	if prev == nil {
		return nil
	}
	return *prev
}

// tracingClock is the time source used by [StartSpan], [Span.End], and
// [Span.AddEvent]. Defaults to the real wall clock; tests inject a
// [clock.Fake] via [SetClock] for deterministic span timestamps.
//
// Storing the clock as an atomic.Pointer keeps reads lock-free on the
// hot path while still allowing test seams to swap the implementation
// without races against producing handlers.
var tracingClock atomic.Pointer[clock.Clock]

func init() {
	c := clock.New()
	tracingClock.Store(&c)
}

// SetClock overrides the package-level clock used by tracing. Pass
// nil to restore the real wall clock. Returns the previous clock so
// tests can defer its restoration.
func SetClock(c clock.Clock) clock.Clock {
	if c == nil {
		c = clock.New()
	}
	prev := tracingClock.Swap(&c)
	if prev == nil {
		return clock.New()
	}
	return *prev
}

func now() time.Time {
	c := tracingClock.Load()
	if c == nil {
		return time.Now()
	}
	return (*c).Now()
}

// Span represents a single unit of work in a distributed trace. Spans
// form a tree: each span carries the trace_id of its root and, when
// nested, the span_id of its parent.
type Span struct {
	// Name is a human-readable label for the operation.
	Name string
	// TraceID is shared by every span in the same trace tree.
	TraceID string
	// SpanID is the unique identifier of this span.
	SpanID string
	// ParentSpanID is the SpanID of the parent, or "" for root spans.
	ParentSpanID string
	// StartedAt records when the span was created.
	StartedAt time.Time
	// EndedAt holds the time the span was ended, or zero if still active.
	EndedAt time.Time

	mu         sync.Mutex
	attributes map[string]any
	events     []spanEvent
}

type spanEvent struct {
	At         time.Time
	Name       string
	Attributes map[string]any
}

// IsRoot reports whether this is a root span (no parent).
func (s *Span) IsRoot() bool { return s.ParentSpanID == "" }

// DurationMS returns the span duration in milliseconds, or -1 when the
// span has not yet ended.
func (s *Span) DurationMS() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.EndedAt.IsZero() {
		return -1
	}
	return float64(s.EndedAt.Sub(s.StartedAt).Milliseconds())
}

// SetAttribute sets a key-value attribute on the span. Thread-safe.
func (s *Span) SetAttribute(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attributes == nil {
		s.attributes = make(map[string]any)
	}
	s.attributes[key] = value
}

// Attributes returns a copy of the current attribute map.
func (s *Span) Attributes() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]any, len(s.attributes))
	maps.Copy(out, s.attributes)
	return out
}

// SpanEvent is a single timestamped event recorded on a span.
type SpanEvent struct {
	At         time.Time
	Name       string
	Attributes map[string]any
}

// Events returns a copy of all events recorded on the span. The
// returned slice and each entry's Attributes map are independent copies
// so callers may hold them past the span's lifetime.
func (s *Span) Events() []SpanEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 {
		return nil
	}
	out := make([]SpanEvent, len(s.events))
	for i, e := range s.events {
		attrs := make(map[string]any, len(e.Attributes))
		maps.Copy(attrs, e.Attributes)
		out[i] = SpanEvent{At: e.At, Name: e.Name, Attributes: attrs}
	}
	return out
}

// AddEvent records a timestamped event within the span.
func (s *Span) AddEvent(name string, attrs map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, spanEvent{At: now(), Name: name, Attributes: attrs})
}

// End marks the span as finished. If a SpanExporter is registered via
// SetSpanExporter, the finished span is handed to it after the lock is
// released. The exporter call is non-blocking by contract (implementations
// buffer and return immediately), so End remains safe on the hot path.
func (s *Span) End() {
	s.mu.Lock()
	if s.EndedAt.IsZero() {
		s.EndedAt = now()
	}
	s.mu.Unlock()

	if p := spanExporter.Load(); p != nil {
		(*p).ExportSpan(s)
	}
}

// String returns a short human-readable representation.
func (s *Span) String() string {
	return fmt.Sprintf("Span(%s trace=%s id=%s parent=%s)", s.Name, s.TraceID[:8], s.SpanID, s.ParentSpanID)
}

// --------------------------------------------------------------------------
// context key
// --------------------------------------------------------------------------

type spanCtxKey struct{}

// --------------------------------------------------------------------------
// Public functions — mirrors py
// --------------------------------------------------------------------------

// StartSpan creates a new Span, optionally inheriting the trace from a
// parent span stored in ctx. The span is stored in the returned context
// so that nested calls to [StartSpan] automatically become children.
//
// The caller is responsible for calling [Span.End] (typically via defer).
// Use the returned context for all downstream work so that child spans
// inherit the trace.
func StartSpan(ctx context.Context, name string, attrs map[string]any) (*Span, context.Context) {
	parent, _ := GetCurrentSpan(ctx)
	var traceID, parentID string
	if parent != nil {
		traceID = parent.TraceID
		parentID = parent.SpanID
	} else {
		// 128-bit random ID (W3C Trace Context shape, [reqctx.NewTraceID])
		// rather than a UUID: a UUID's version/variant nibbles carry no
		// entropy, and truncating one (as SpanID used to) shrinks the
		// collision-resistant space further still.
		traceID = reqctx.NewTraceID()
	}
	sp := &Span{
		Name:    name,
		TraceID: traceID,
		// 64-bit random ID, matching [reqctx.NewSpanID] — a truncated-UUID
		// span id only carried 32 bits of entropy, making birthday-bound
		// collisions likely after tens of thousands of spans.
		SpanID:       reqctx.NewSpanID(),
		ParentSpanID: parentID,
		StartedAt:    now(),
	}
	for k, v := range attrs {
		sp.SetAttribute(k, v)
	}
	return sp, SetCurrentSpan(ctx, sp)
}

// GetCurrentSpan returns the active [Span] stored in ctx, or nil when
// no span is present.
func GetCurrentSpan(ctx context.Context) (*Span, bool) {
	sp, ok := ctx.Value(spanCtxKey{}).(*Span)
	return sp, ok && sp != nil
}

// GetCurrentTraceID returns the trace ID of the active span in ctx, or
// "" when none is present.
func GetCurrentTraceID(ctx context.Context) string {
	if sp, ok := GetCurrentSpan(ctx); ok {
		return sp.TraceID
	}
	return ""
}

// SetCurrentSpan stores sp in ctx and returns the enriched context.
func SetCurrentSpan(ctx context.Context, sp *Span) context.Context {
	return context.WithValue(ctx, spanCtxKey{}, sp)
}

// ResetCurrentSpan removes the active span from ctx.
func ResetCurrentSpan(ctx context.Context) context.Context {
	return context.WithValue(ctx, spanCtxKey{}, (*Span)(nil))
}
