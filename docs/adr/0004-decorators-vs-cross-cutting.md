# ADR 0004 — Python decorators ↔ Go cross-cutting

- **Status**: accepted
- **Date**: 2026-04-27
- **Related**: `internal/observability/instrument.go`,
  [ADR 0001 — License: MIT](./0001-license-mit.md)

## Context

aiohomematic relies on a thick layer of decorators for cross-cutting
concerns. The most consequential ones (with their files in the Python
reference):

| Decorator | File | Wirkung |
|---|---|---|
| `@inspector` | `aiohomematic/decorators.py:58-262` | exception boundary, structured log, latency metric, counter metric, request-context push/pop, ServiceScope tagging |
| `@measure_execution_time` | `aiohomematic/decorators.py:352` | optional perf log via dedicated logger |
| `@hm_property` / `@config_property` / `@info_property` / `@state_property` | `aiohomematic/property_decorators.py:430-606` | property descriptor with a *kind* tag (config/info/state); used by reflection-based exporters |
| `DelegatedProperty[ValueT]` | `aiohomematic/property_decorators.py:58-212` | read-only descriptor delegating to a nested attribute path |
| `@callback_backend_system` / `@callback_event` | `aiohomematic/central/decorators.py:25-95` | bus-publish wrapper around RPC system / data-point callbacks |
| `@bind_collector` | data-point + coordinators | batch-update collector-context |

The decorator surface answers a simple question: *can I tag a
single Python method with a one-line annotation and have the runtime
attach the right cross-cutting effects?* The answer in Python is yes,
in part because every async method already returns a coroutine and
the wrapper machinery is cheap.

Go does not have decorators. The team must decide how to deliver the
same effects — observability, property classification, batch-update
context — without dragging Python idioms into the Go code.

## Decision

Cross-cutting concerns are delivered through three Go-idiomatic
mechanisms, each owning a *subset* of what the Python decorators
deliver. None of them tries to reproduce the decorator syntax.

### 1. Observability via `internal/observability/instrument.go`

`@inspector`, `@measure_execution_time`, `@service_call` and the
ServiceScope tagging collapse into a single helper:

```go
func Instrument[T any](
    ctx context.Context,
    rec Recorder,
    name string,
    fn func(context.Context) (T, error),
) (T, error)
```

`Recorder` accepts latency, counter, and error events. Production
wiring uses a Prometheus + slog recorder; tests inject a `NoopRecorder`
or a recording one.

**Usage rule**: every public method on a coordinator, a backend, or
a reliability primitive that talks to the CCU is wrapped with
`Instrument` at the top of its body. There is no compile-time
enforcement — the contract is style-guide level — but
`tests/contract/coordinator_size_test.go` flags drift when a
coordinator's instrumented-method count drops below threshold.

`InstrumentValue` is the value-only variant for methods returning
`(T, error)`; `Instrument` (without the value) is the procedural form.

### 2. Property kinds via struct tags

`@config_property` / `@info_property` / `@state_property` map to
struct-tag annotations:

```go
type Channel struct {
    Address string `payload:"info"`
    Reach   int    `payload:"state"`
    Group   string `payload:"config"`
}
```

The `internal/payload` package exposes `For(v) []Field` which uses
reflection to enumerate the fields tagged with a given kind. North-bound
serialisation (REST, MQTT-discovery, snapshot) consumes this list
instead of hand-rolled marshalling.

Generation helper `script/gen_propkinds.go` (QW-11) produces a
compile-time list as a fall-back when reflection cost matters. The
generator is a `go:generate` step that emits a `_propkinds.go` file
per package; it is optional — the reflection path is the default.

### 3. Batch-collector via explicit context type

`@bind_collector` becomes an explicit `Collector` type passed as a
parameter:

```go
func (c *Channel) Update(ctx context.Context, col *Collector, value any) error
```

The collector aggregates writes and flushes them as a single CCU
call when the calling closure returns. There is no implicit binding
through a thread-local — the parameter is mandatory. A `nil` collector
is a sentinel for *"send immediately"*.

### 4. RPC callback wrappers stay close to the call site

`@callback_backend_system` and `@callback_event` are inlined in the
RPC dispatcher (`internal/central/rpcserver/`) — the wrapper effect
(decode, dispatch, publish) lives in one function rather than being
bolted on per handler.

## Why not generate Go decorators

The team considered code-generation that would emit `@inspector`-
equivalent wrappers from a `//openccu-loom:instrument` comment. Three
arguments tipped the decision against it:

- **Visibility**. A reflective wrapper hides the cross-cutting effect
  from `go vet` and `gopls`. Reading a coordinator file then requires
  knowing what every annotation expands to.
- **Cost**. Adding a bespoke generator means a tool to maintain, a
  build-step dependency, and a tax on every contributor. The
  `Instrument` helper does the same work with one explicit call.
- **Linting**. `golangci-lint` already enforces "wrap public methods";
  a custom contract test (`coordinator_size_test.go`) keeps the
  surface honest. Generation would not add coverage.

## Trade-offs

- **Verbosity**. Every method opens with an `Instrument(...)` call.
  Roughly +6 LOC per public method on coordinators. Accepted as the
  price of explicit observability.
- **Drift risk**. Forgetting to instrument a new method is silent.
  The contract test catches gross regressions; the rest is review.
- **Property reflection**. Reflection is slower than the Python
  descriptor path on hot loops; the optional generator (QW-11)
  removes the cost for packages that benchmark sensitive.

## Consequences

- New code in `internal/central/coordinators/`,
  `internal/client/reliability/`, and `internal/client/backends/`
  uses `Instrument` for every public method.
- `pkg/payload` is the single dependency for property-kind
  enumeration. Hand-rolled marshalling is rejected in code review.
- The Python-decorator parity gap is closed by this ADR: every
  cross-cutting effect aiohomematic delivers via decorators has a
  named Go-idiomatic mechanism (`Instrument`, `payload:"…"` tags,
  explicit `Collector` parameter, inline RPC dispatcher).
