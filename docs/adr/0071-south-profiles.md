# ADR 0071 — South profiles: one south-bound strategy per central

- Status: accepted
- Date: 2026-09-27

## Context

Until 0.78 every central was a CCU, and the code said so in a dozen places at
once: readiness was `GET /ise/checkrega.cgi` answering `OK`; the hub came up
over a JSON-RPC session and ReGa scripts; every interface was `/RPC2` with
Basic auth and was announced with XML-RPC `init`; inbound events only arrived
on the shared callback route `/RPC2/<central>`; maintenance, backups and
heating groups found "the CCU backend" by type assertion on the primary
backend. The only branch that existed was per *interface* (CUxD over BIN-RPC).

openccu-lite is a second kind of system. Its daemon, occulited, serves one
HTTP API: a health and interface report, an XML-RPC proxy per interface
(`/api/rpc/v1/xmlrpc/{interface}`, bearer token), an event stream, a metadata
store, and system, auth and backup APIs. It has no ReGa, no JSON-RPC and no
`init` — and `checkrega.cgi` is answered by its single-page shell with
`200 text/html`, so the CCU readiness gate would wait for ever.

Supporting it by `if lite` at each of those places would scatter the second
system through the adapter package and leave no place that says what a
system type decides. The full inventory of the hard-wired places, with
anchors, is in the
[implementation plan](https://github.com/SukramJ/openccu-loom/blob/main/notes/plans/openccu-lite-backend.md) §2.1.

## Decision

A central's configuration names its **system type** — `ccu` (the default, also
for every stored row that predates the field), `openccu-lite`, or `auto`
(identified at bring-up, then persisted). The type selects a **south profile**:
the per-central strategy that supplies everything that differs between the
systems, and nothing else.

- **What a profile supplies** (`internal/central/adapter/south_profile.go`):
  the readiness probe, the event ingress, the hub bring-up (which returns the
  session the per-interface transports and value seeding depend on), the
  liveness probe of the MQTT hub plane, the per-central feature set
  (ADR 0074), and the system-management ports on the unit (power, position,
  firmware, backup, groups, accounts). `south_ccu*.go` wraps the existing CCU
  code behind those ports unchanged; `south_lite.go` and `lite_*.go`
  implement openccu-lite.
- **What stays shared**: the XML-RPC client and codec, `InterfaceClient` with
  its reliability stack, the `backends.Operations` contract (a
  `LiteBackend` joins `CcuBackend`), the device pipeline and registries, the
  callback handlers the event stream feeds, the domain model and every
  north-bound adapter. The boundary sits at *where a fact comes from*, not at
  which protocol carries it.
- **Selection happens in one place.** `southProfileFor` is the only
  production code that compares a system type;
  `TestSystemTypeIsComparedOnlyInSouthProfileFor` fails the build on any
  other comparison. An `auto` central holds its place with a placeholder,
  probes the address, persists the resolved type and is brought up again as
  that type.
- **Layout.** The lite profile stays inside `internal/central/adapter`
  (ADR 0034: one adapter package, a cluster per concern). The wire client for
  occulited's API is a transport package, `internal/client/transport/occulited`,
  like `jsonrpc`, `xmlrpc` and `binrpc`: no domain knowledge.
- **Licence.** occulited is GPL-3.0. OpenCCU-Loom stays MIT (ADR 0001) and is
  a network *client* of occulited's documented HTTP API; implementing a client
  against a documented wire contract is not a derivative work of the server.
  Every wire detail is therefore re-derived from the API documentation; no
  occulited source, fixture or algorithm is copied or translated, and the test
  double `tests/harness/litefake` is our own MIT code written from the same
  documentation. Comments cite occulited's documentation by name, never a
  source path.

## Alternatives considered

- **Talk to openccu-lite over classic XML-RPC with `init` callbacks.** The
  proxy refuses `init`, and the box's own interface processes are not
  reachable from outside. Rejected: it does not work.
- **A JSON-RPC-shaped adapter that makes occulited look like a CCU.** It
  would have to fake ReGa, sysvars and programs the system does not have, and
  every fake is a silent success. Rejected in favour of explicit absence
  (ADR 0074).
- **`if lite` at each call site.** Cheapest to start, impossible to audit;
  the one-place selection plus its guard is the mechanical form of "no
  system-type branch outside the profile".
- **A separate daemon for openccu-lite.** Duplicates the whole north-bound
  surface and loses mixed fleets (ADR 0002). Rejected.

## Consequences

- Adding a third system type is a profile, not a sweep: the ports say what
  it must supply, and the guard keeps the comparison in one place.
- The CCU path moved behind the ports behaviour-preserving (phase A of the
  plan); its tests did not change meaning.
- A lite central registers no callback route: its events arrive on the stream
  (ADR 0072). SPECIFICATION §4.3 records this.
- The occulited API is pre-1.0. The client checks the API majors the box
  reports and refuses unknown majors; the risk and its mitigation are in the
  specification's risk register.
