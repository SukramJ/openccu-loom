# ADR 0017 — Logging and Diagnostics

- **Status**: Accepted
- **Date**: 2026-05-22
- **Related**:
  `pkg/hmlog/`, `pkg/hmreqctx/`, `internal/diagnostics/`,
  `internal/north/rest/handlers/diagnostics*.go`,
  `assets/ui/src/routes/Diagnostics.svelte`,
  `SPECIFICATION.md` §Observability.

## Context

The pre-existing logging surface in `OpenCCU-Loom` covered the basics
— structured slog records with `request_id`/`operation`/`central_name`
plumbed through a `reqctx.RequestContext`, a `Health.Tracker` snapshot
exposed via `GET /api/v1/health`, an incidents table, and a generic
SPA panel showing the three lists side by side. An audit of the daemon
against the goal "a Claude (or human) agent can reconstruct a
scenario without further user interaction" surfaced several gaps:

1. No durable trace identity across the REST → coordinator → client →
   transport hop. `request_id` covered the REST stage only; downstream
   slog records dropped it as soon as the call left `chi`.
2. No redaction. Sensitive fields (`password`, `client_secret`,
   session tokens) leaked into stdout / file sinks verbatim.
3. No per-subsystem level control. The daemon supported a single
   global `level: info|debug|…` knob; debug-tracing one component
   meant cranking the whole daemon to debug and drowning in noise.
4. No "ship-the-state" artefact. Support tickets had to be assembled
   by hand from `/health`, `/incidents`, `/snapshot`, plus log
   excerpts.
5. No live capture. Reproducing an intermittent issue required either
   leaving debug logging on permanently (disk pressure) or shoving
   `ssh ... tail -f` at the operator.

We chose to fix all five in one coordinated change rather than patch
each separately, because every piece depends on a common backbone:
the trace context, the redaction filter, and the per-subsystem level
registry are exactly what the diagnostics dump, the capture archive,
and the UI surface consume.

## Decision

We adopted a five-pillar observability backbone, all controlled from
a single Svelte SPA panel and exposed via REST so external tooling
(Claude, CI, support scripts) can consume the same surface.

### 1. Structured logging with W3C trace propagation

Every log record now carries:

- `request_id` — REST middleware-assigned UUID (unchanged).
- `trace_id` — 32-hex W3C trace ID, generated at the request entry
  or copied from the incoming `traceparent` header. Survives
  uninterrupted through coordinator / client / transport calls via
  `reqctx.RequestContext`.
- `span_id` / `parent_span_id` — 16-hex W3C span IDs, refreshed on
  every `reqctx.StartChildSpan` boundary (REST handler entry,
  coordinator method, RPC dispatch).
- `central_name` / `interface_id` / `device_address` — domain-scope
  tags carried by `reqctx`.

The REST middleware emits a `traceparent` response header so a client
can correlate its own logs against the daemon's. Adopting W3C
verbatim — rather than a shorter compact format — keeps the path
open for future OpenTelemetry interop without a translation layer.

### 2. Redaction at the slog handler boundary

`pkg/hmlog.RedactingHandler` masks the value of every attribute whose
key matches a substring in a default allowlist (`password`, `token`,
`api_key`, `client_secret`, `authorization`, `cookie`, `session_id`,
`refresh_token`, `id_token`, `bearer`, `private_key`). Matching is
case-insensitive and substring-based so nested keys (`oidc.client_secret`,
`X-Api-Key`) are caught without per-call configuration. Group
attributes are recursed; map / struct values reaching the handler as
`slog.AnyValue` are NOT introspected — callers expose individual
fields via `slog.Group` or `slog.Attr`.

### 3. Per-subsystem level registry

`pkg/hmlog.LevelRegistry` holds a `slog.LevelVar` per dotted logger
path (`openccu-loom.client.transport.xmlrpc`, `openccu-loom.north.matter`,
…). Lookups walk from the most specific override up to the configured
default, so an override on `openccu-loom.client` cascades to every
descendant. Overrides may carry a TTL (max 24 h) so a forgotten
debug-window cannot leak indefinitely.

Operators configure the registry in three ways:

- **Static**: `logging.overrides` map in `config.yaml`.
- **Runtime**: `PUT /api/v1/diagnostics/log-levels/{path}` with a
  TTL. The diagnostics REST endpoint records every change in the
  audit ledger (`logging.override_set` / `logging.override_reset`).
- **Capture-bound**: the capture endpoint installs overrides that
  auto-expire when the recording stops.

### 4. Integration diagnostics dump

`GET /api/v1/diagnostics?anonymize=0|1` returns a single JSON envelope
covering build metadata, the health snapshot + numeric score, every
interface, recent incidents, the system-status ring, and the current
log-level overrides. Anonymisation hashes device-address-shaped
fields with a 12-hex SHA-256 prefix (`anon:…`); structural relationships
stay intact.

This artefact is the "ship-the-state" output the operator attaches to
support tickets and an agent consumes when triaging.

### 5. RAM-buffered capture sessions

`internal/diagnostics.Manager` owns at most one running capture at a
time. Start:

- Allocates a `hmlog.CaptureSink` (default 64 MiB ring; FIFO eviction
  by complete ndjson line, never partial lines).
- Attaches the sink to the daemon's `hmlog.TeeHandler`. The tee sits
  between reqctx and redact in the handler chain so the capture sees
  the same redaction guarantees as stdout.
- Optionally applies TTL-bounded `LevelRegistry` overrides for the
  capture window only.

Stop (or expiry):

- Detaches the sink, applies redaction-aware ndjson encoding (the
  `TeeHandler` re-encodes per record so anonymisation can re-hash
  device addresses), and packs the result plus a `capture.meta.json`
  sidecar into a tar.gz blob held in memory.
- Resets the captures' temporary level overrides.

Limits:

- ≤ 1 parallel capture (`ErrCaptureBusy` on Start while running).
- ≤ 30 minutes per capture (`MaxCaptureDuration`).
- ≤ 5 rotating archived captures (`MaxArchivedCaptures`).
- Archives expire 24 h after Stop (`ArchiveRetention`) — `Sweep`
  enforces both rules.

### Score weighting (Health)

The numeric score on `(*health.Tracker).Score()` retained the
existing simple mix (`Healthy=1.0`, `Degraded=0.5`,
`Unhealthy/Unknown=0`). We considered the aiohomematic 40/30/30
weighting (`state-machine + circuit-breakers + recent activity`) but
chose against importing it: OpenCCU-Loom's `Tracker` is broader (any
component, not only CCU clients with circuit-breakers), and the
existing mix already lines up with the Prometheus surface
(`MetricsHealthSummary`). The new `IsAvailable` / `IsDegraded` /
`IsFailed` / `CanReceiveEvents` properties mirror the aiohomematic
public surface for callers that want the boolean variant.

## Consequences

**Positive**

- A single `GET /api/v1/diagnostics` request reconstructs the daemon
  state at the moment of capture, with sensitive values redacted by
  default.
- An agent can correlate a multi-hop request end-to-end via
  `trace_id`, even when the call straddles REST / coordinator / RPC.
- Operators can dial up debug logging for one subsystem (e.g.
  `openccu-loom.client.transport.xmlrpc`) from the SPA without a
  restart and without affecting the rest of the daemon.
- The capture archive contains exactly the time window of interest,
  packaged for upload to an issue tracker.

**Negative**

- The handler chain has one more wrapper (`TeeHandler`) — one
  `atomic.Pointer.Load` per record on the hot path when no capture
  is running. Benchmarked below 30 ns; acceptable.
- The capture archive is held in RAM until Stop. A 64 MiB cap +
  one-active rule bounds the impact; the chosen storage mode trades
  crash-resilience for I/O simplicity (see `AskUserQuestion`
  decision 1).
- The diagnostics surface is admin-gated. SPA-side admin enforcement
  was already in place; no new RBAC plumbing was needed, but
  operator-tier users cannot capture without an admin escalation.

**Risks accepted**

- Anonymisation runs on the per-record encoder, not at attribute
  creation time. A buggy caller that uses a non-allowlisted key
  (`peer_addr` instead of `device_address`) would slip raw values
  into the archive. Mitigation: the diagnostics dump path applies
  the same anonymisation so reviewers see the gap when comparing
  the dump against the capture.

## Alternatives considered

- **Compact 8-hex trace IDs (aiohomematic style)** — rejected for
  the OpenTelemetry interop tradeoff. We did not need the brevity
  enough to give up the standard.
- **Disk-backed capture (NDJSON file on `data_dir`)** — would
  preserve a crash mid-capture, but a debug-capture during an I/O
  pathology is exactly the case where the disk-write feedback loop
  becomes the symptom. RAM-ring keeps the capture independent.
- **SQLite-backed capture** — gives a queryable archive but doubles
  the implementation complexity and does not match the "share the
  archive on an issue ticket" workflow. Postponed.
- **Per-call log-level toggle in URL query** — would dodge the
  registry, but loses TTL safety and audit trace. Rejected.

## References

- `pkg/hmlog/{redact,levels,factory,capture,op,slowquery}.go`
- `internal/reqctx/{reqctx,trace,handler}.go`
- `internal/diagnostics/capture.go`
- `internal/north/rest/handlers/diagnostics{,_loglevels,_capture}.go`
- `internal/health/tracker.go` (`IsAvailable`/`IsDegraded`/`IsFailed`/`CanReceiveEvents`)
- `assets/ui/src/routes/Diagnostics.svelte` (Health-score, logging
  tab, capture tab, dump button)
- W3C Trace Context level 1: https://www.w3.org/TR/trace-context/

## Amendment (2026-09-03) — the request-context package moved to `pkg/`

`internal/reqctx` is `pkg/hmreqctx`. Nothing about the decision changed;
the package's own name stayed `reqctx` in prose and became `hmreqctx` in
code, following the `pkg/hm*` convention every sibling there follows.

The move was forced by this ADR's own layering. `pkg/hmlog` installs the
request-context handler and reads the span in its operation logger, so it
imported `internal/reqctx` — and Go refuses an `internal/` path to an
importer outside the module. An external program that embedded
openccu-loom as a library and imported `pkg/hmlog` therefore could not
compile, while `go build ./...` stayed green, because the internal rule
never applies to importers inside the module and every importer here is
inside it.

`TestPkgDoesNotImportInternal` (tests/contract) is what notices now. It
carries no exception list on purpose: one entry for `hmlog` would have
made it blind to the single case it exists for.
