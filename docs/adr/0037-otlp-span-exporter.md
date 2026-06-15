# ADR 0037 — Pluggable span exporter with a lean OTLP/HTTP exporter (not the OTel-gRPC SDK)

- **Status**: accepted
- **Date**: 2026-06-15
- **Related**:
  `internal/observability/tracing.go`,
  `internal/config/config.go` (`NorthREST`),
  the analysis item Area 9 [W7]/[P3] in
  `docs/audit/architecture-analysis-2026-06-15.md`

## Context

The daemon has a full in-process trace model (`observability.Span`:
trace/span/parent IDs, attributes, events, context propagation) but no
way to emit traces to an external collector — `Span.End()` only stamps
the end time. The analysis (Area 9 [W7]/[P3]) asked for *"an OTLP-gRPC
exporter behind `north.rest.tracing.otlp_endpoint` — the span model
already exists; only export is missing."*

Two design questions fall out: **how to collect finished spans**, and
**which OTLP transport / dependency** to take on.

### Dependency weight

A literal OTLP-**gRPC** exporter means pulling the OpenTelemetry SDK
(`go.opentelemetry.io/otel`, `/sdk`, `/exporters/otlp/otlptrace/...`)
**and** gRPC (`google.golang.org/grpc` + `protobuf` + `x/net/http2` + …)
— on the order of 50 transitive packages. That is a heavy commitment for
a project that is deliberately lean: `CGO_ENABLED=0`, a single static
binary, and a "stop and discuss before adding heavy dependencies" policy
(CLAUDE.md). OTLP also defines a **JSON-over-HTTP** encoding
(`/v1/traces`) that every major collector (otel-collector, Jaeger,
Grafana Agent, Tempo) accepts, and that needs **zero** new dependencies —
`net/http` + `encoding/json`.

## Decision

### 1. A pluggable `SpanExporter` seam

```go
type SpanExporter interface {
    // ExportSpan is handed each finished span. Implementations MUST NOT
    // block (Span.End is on the hot path) — buffer and return.
    ExportSpan(*Span)
    Shutdown(ctx context.Context) error // flush + stop
}
func SetSpanExporter(SpanExporter)      // nil disables export (default)
```

`Span.End()` enqueues the finished span to the registered exporter (if
any) via a non-blocking call, after stamping `EndedAt`. The exporter is
its own collector — no separate span registry is added. When no exporter
is set (the default) `End()` is unchanged, so the hot path and every
existing test are unaffected.

The seam is the durable contract: an operator or fork that *wants* the
full OTel-gRPC SDK can implement `SpanExporter` against it without the
daemon carrying the dependency.

### 2. A lean OTLP/HTTP exporter

`otlpHTTPExporter` implements `SpanExporter` with the standard library
only: a buffered channel (bounded; drop-oldest on overflow so a slow
collector never blocks or OOMs the daemon), a background goroutine that
batches by size/interval, marshals the batch to the OTLP
`ExportTraceServiceRequest` **JSON** shape, and POSTs it to
`<endpoint>/v1/traces`. `Shutdown` drains the buffer and stops the
goroutine.

### 3. Config toggle (default off)

`NorthREST.Tracing.OTLPEndpoint string` (`cfg:"expert"`). Empty (the
default) = disabled. The daemon wires the exporter at boot only when the
endpoint is set, and defers `Shutdown`. Documented in
`example.config.yaml`.

## Alternatives considered

- **Full OTel SDK + OTLP-gRPC** (the literal ask). Rejected as the
  default: ~50 transitive deps + gRPC for a P3 feature conflicts with the
  lean-binary value. The `SpanExporter` seam keeps it available as an
  opt-in implementation.
- **OTLP/HTTP with protobuf** (`go.opentelemetry.io/proto/otlp`).
  Rejected: still adds the protobuf dep tree; JSON is wire-compatible
  with collectors and needs nothing new.
- **Export synchronously in `End()`.** Rejected: network I/O on the hot
  path. The bounded-buffer + background-batch design keeps `End()`
  non-blocking.

This is a deliberate divergence from the analysis's "gRPC" wording,
recorded here with rationale; the observable result (OTLP traces in a
collector) is the same.

## Consequences

- Operators get OTLP trace export via one config line, with no new
  dependency and no change to the default (export-off) behaviour.
- The hot path is untouched when export is disabled and non-blocking when
  enabled.
- A heavier OTel-gRPC exporter, if ever needed, plugs into the same
  `SpanExporter` interface without touching the trace model.
