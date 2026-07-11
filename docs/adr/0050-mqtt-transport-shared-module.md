# ADR 0050 — MQTT transport extracted into the shared `go-mqtt` module

- **Status**: Accepted
- **Date**: 2026-07-11
- **Related**:
  [ADR 0011 — MQTT topic & payload architecture](./0011-mqtt-topic-and-payload-architecture.md),
  [ADR 0047 — north-bound bridges as `Service`s owned by a `Registry`](./0047-northbound-bridge-registry.md),
  [ADR 0001 — license: MIT](./0001-license-mit.md)

## Context

OpenCCU-Loom's MQTT bridge (ADR 0011) needs a robust client transport:
connect/reconnect with backoff, TLS, LWT, an in-flight publish queue, and a
supervisor that re-publishes retained state on every reconnect. That transport
is generic — it knows nothing about Homematic, CCUs, or the HA-Discovery
payloads layered on top of it. The same connect/reconnect/publish machinery is
needed, verbatim, by the developer's other MQTT-emitting daemons:
`go-mtec2mqtt`, `go-daikin2mqtt`, `go-zendure2mqtt`, and `go-homeconnect2mqtt`.

Keeping a private copy of the transport inside each daemon meant fixing the
same reconnect race, the same TLS-config wiring, and the same queue-drain bug
five times, and letting the five copies drift. The transport is exactly the
kind of code that belongs behind a stable, independently-versioned module
boundary rather than duplicated per-consumer.

## Decision

**The MQTT transport is extracted out of the OpenCCU-Loom tree into a
standalone, MIT-licensed shared module, `github.com/SukramJ/go-mqtt`, consumed
as an ordinary `go.mod` dependency (currently pinned at `v1.2.0`).** All five
daemons — `mtec`, `daikin`, `zendure`, `homeconnect`, and `loom` — depend on
the same module; the transport lives, is tested, and is released once.

What stays in OpenCCU-Loom and what moves out:

1. **Out (into `go-mqtt`):** the connection lifecycle — dial, TLS,
   authenticate, subscribe, publish with an in-flight queue, LWT, and the
   reconnect/backoff loop. This is the domain-agnostic wire client.
2. **Stays in `internal/north/mqtt`:** everything Homematic-specific — the
   topic schema (ADR 0011), HA-Discovery payload assembly, the value→payload
   projection, and the runtime supervisor that re-publishes retained CCU state
   on reconnect. The supervisor drives the shared client's `OnConnect` hook; it
   is an internal concern of the MQTT `bridge.Service` (ADR 0047), not part of
   the shared module.

The boundary is deliberately drawn so that the shared module has **no**
knowledge of OpenCCU-Loom's domain: it exposes a connection + publish/subscribe
surface and connection-state callbacks, and the daemon supplies all topics and
payloads.

## License and dependency policy

The shared module is **MIT** — the same license as OpenCCU-Loom's own source
(ADR 0001) — so it sits cleanly within the dependency-licensing rules in
`CLAUDE.md` (MIT/Apache-2.0/BSD are fine; GPL/LGPL/MPL/AGPL would require a
stop-and-discuss). Extracting first-party code into a first-party MIT module
introduces no new copyleft obligation and no CGo. It is an ordinary
`require` line in `go.mod`, pinned to an exact version, and it participates in
the normal `go mod` supply chain like any other dependency.

## Version-pin and coupling policy

- **Exact version pins, updated deliberately.** Each consumer pins an exact
  `go-mqtt` version; a transport change lands in the module, is released, and
  each daemon bumps its pin in its own release. There is no floating/`latest`
  dependency — a transport regression cannot silently reach a daemon.
- **Squash-only cross-repo release fan-out.** A change that spans the module
  and its consumers is landed module-first (squash-merged, tagged), then each
  consumer bumps the pin in a separate squash-merged PR. The five consumers are
  released independently; there is no lockstep monorepo release. Tags on the
  module follow its own semver; consumers reference them by pin.
- **Backwards-compatible module surface.** Because five daemons share it, the
  module's public surface is treated as a real API: additive changes are
  minor/patch bumps; a breaking change is a major bump that each consumer
  adopts on its own schedule.

## Alternatives considered

- **Keep a private per-daemon copy.** Rejected: five drifting copies of the
  same reconnect/queue code, each carrying its own latent bug fixes. The
  transport is generic enough that duplication buys nothing.
- **A shared internal package inside a monorepo.** Rejected: the five daemons
  are separate repositories with independent release cadences; a monorepo would
  force lockstep releases and couple unrelated products.
- **Depend on a third-party MQTT client library directly, per daemon.**
  Rejected as the *primary* boundary: the supervisor/reconnect-republish
  behaviour and the connection-state callback shape are shared product
  requirements worth owning behind one first-party module (which may itself
  wrap a third-party client internally), rather than re-deriving the same
  glue five times.

## Consequences

- The MQTT transport is written, tested, and fixed **once** in `go-mqtt`; a
  reconnect or queue fix reaches every daemon by a version bump instead of five
  hand-ports.
- OpenCCU-Loom's `internal/north/mqtt` shrinks to the Homematic-specific layer;
  the domain-agnostic wire client is no longer in-tree.
- A new external dependency enters `go.mod` (`github.com/SukramJ/go-mqtt`), MIT
  and CGo-free, pinned to an exact version.
- Cross-repo releases follow the squash-only, module-first, pin-bump-second
  fan-out; a transport change is a two-step (module release → consumer bump)
  rather than a single edit.
- The five consumers stay decoupled: each adopts a new module version on its
  own release, so a module change never force-releases an unrelated daemon.
