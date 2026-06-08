# ADR 0025 — MCP server as a north-bound adapter

- **Status**: accepted
- **Date**: 2026-06-04
- **Related**:
  [ADR 0002 — multi-CCU first class](./0002-multi-ccu-first-class.md),
  [ADR 0011 — MQTT topic and payload architecture](./0011-mqtt-topic-and-payload-architecture.md),
  [ADR 0012 — Matter pure-Go implementation](./0012-matter-pure-go-implementation.md),
  [ADR 0020 — external-client wire contract](./0020-external-client-wire-contract.md),
  [ADR 0021 — mDNS self-advertisement](./0021-mdns-self-advertisement.md),
  [ADR 0026 — MCP dev-mode introspection surface](./0026-mcp-dev-mode.md),
  `SPECIFICATION.md` §6 (north-bound surface)

## Context

The Model Context Protocol (MCP) is an open, JSON-RPC-2.0-based
protocol that lets LLM agents call **tools** (actions), read
**resources** (context), and run **prompts** (templated workflows)
against an external system. Anthropic published it; the major agent
runtimes (Claude Code / Desktop, and a growing set of third-party
hosts) speak it. A server that exposes a domain over MCP becomes
directly drivable by an agent in natural language.

OpenCCU-Loom already projects its domain core through several
north-bound adapters: REST (~80 endpoints), WebSocket (85 commands),
MQTT (HA Discovery + raw planes), and the Matter bridge. The
question this ADR settles: **should the daemon also expose an MCP
server, and if so, in what shape?**

The use-case is concrete and not covered by the existing surfaces:

- **Agent-driven diagnosis** — "why is the bathroom thermostat
  unreachable?" An agent reads device health, incidents, paramset
  state, and the audit log, then reasons over them. MQTT/HA-Discovery
  exposes *state* but no diagnostic affordance; REST exposes the data
  but requires a human (or a hand-written client) to orchestrate the
  calls.
- **Natural-language control** — "set all living-room blinds to 40 %".
  Today this needs either the SPA, a REST client, or HA automations.
- **Operator tooling** — an agent that triages a fleet of CCUs,
  proposes config changes, and explains them.

None of the existing adapters is the right substrate. MQTT is a
state-sync plane, not a request/response RPC with tool descriptions.
REST/WS are the right *data* but carry no machine-readable tool
catalogue an agent can discover and reason about. Matter is a
device-control protocol for commissioners, not a reasoning surface.

### What MCP is, in this project's terms

An MCP server is **another projection of the same domain core** — the
same role REST, WS, MQTT, and Matter already play. It is *not* a new
data path: every MCP tool resolves to capabilities that already exist
behind the REST/WS handlers and the central/model/client core. The
work is adapter-shaped (map domain capabilities to MCP tool/resource
shapes), not core-shaped.

This is the same "rich model, dumb adapter" principle ADR 0011 (MQTT)
and ADR 0012 (Matter) established: the adapter owns the wire protocol;
the model owns meaning.

## Options considered

### Option A — MCP server as a first-class north-bound adapter (`internal/north/mcp`)

A native Go MCP server, default-off, opt-in via
`cfg.North.MCP.Enabled` (mirrors the Matter flag). It sits on the
same `CentralRegistry` and EventBus as the other adapters and maps
domain capabilities to MCP tools/resources/prompts.

- **Pros**: uniform architecture — Matter, MQTT, REST, WS, and MCP
  are all north-bound adapters over the same core. No second runtime
  (a pure-Go MCP server keeps `CGO_ENABLED=0` and the single static
  binary). Tools reuse the exact authorization, validation, and
  multi-CCU scoping the REST handlers already enforce, because they
  call the same service layer. Capability handshake (`GET
  /info.capabilities`) already has a slot for advertising it.
- **Cons**: a new dependency (the Go MCP SDK) whose maturity must be
  vetted; a new auth/transport surface to secure; ongoing parity work
  to keep the tool catalogue in step with the REST/WS surface.

### Option B — standalone external MCP sidecar process

A separate binary (or third-party process) that talks to the daemon
over its existing REST/WS contract (ADR 0020) and re-exposes it as MCP.

- **Pros**: zero new code in the daemon; ships and versions
  independently; the wire contract (ADR 0020) is explicitly designed
  to support exactly this kind of external client.
- **Cons**: a second deployment unit, a second auth configuration
  (the sidecar needs a daemon token *and* its own MCP auth), and a
  second process to supervise on the resource-constrained CCU3 /
  RaspberryMatic Addon path. The tool catalogue lives outside the
  repo, so it drifts from the REST/WS surface with no in-tree guard.

### Option C — do nothing (defer)

Leave MCP out; document that the ADR-0020 wire contract makes an
external MCP bridge (Option B) buildable by anyone who wants it.

- **Pros**: zero scope cost now; the contract surface already
  supports external clients.
- **Cons**: no first-party agent affordance; every consumer
  re-implements the same mapping.

## Decision

**Adopt Option A — MCP as a first-class, default-off north-bound
adapter.** This ADR records the decision and the binding constraints;
implementation lands on a later line, sequenced behind the depth-parity
work, not as part of this ADR.

Rationale:

1. **Architectural fit is exact.** MCP is the same adapter shape the
   project already runs four times. Building it as an external
   sidecar (Option B) would duplicate auth, supervision, and the
   tool↔capability mapping outside the one place that can keep them in
   sync — exactly the divergence ADR 0012 rejected for the Matter
   sidecar option, for the same reasons (second runtime, second
   security pipeline, broken single-binary property on the CCU Addon
   path).
2. **The marginal cost is low** because it is a projection, not a new
   data path. The tools call the existing service layer; the
   authorization and multi-CCU scoping come for free.
3. **Default-off matches the Matter precedent**, not the mDNS one.
   MCP exposes write-capable tools over a new surface; like Matter
   (and unlike mDNS self-advertisement, ADR 0021), it should require
   explicit operator opt-in.
4. **Sequencing**: MCP is a new north-bound bridge, gated behind its
   capability flag and default-off. It is sequenced after the
   depth-parity work against aiohomematic — a new reasoning surface,
   not a skeleton gap — the same way Matter was prioritised relative
   to the core build-out.

## Binding constraints (apply when the adapter is built)

These are the non-negotiables a future implementation PR must honour.

### Multi-CCU scoping is first-class in every tool

Per ADR 0002, there is no "the single CentralUnit". Every MCP tool
that touches a central takes `central_name` as a required, explicit
argument — never an implicit default. A `list_centrals` tool
enumerates the configured CCUs so an agent can discover the scoping
dimension before acting. Tools that omit `central_name` where one is
required fail with a structured error, not a silent fallback to the
first central.

### Reads are free; writes are gated and explicit

The split mirrors the live-CCU-writes rule in `CLAUDE.md`:

- **Read tools** (`list_devices`, `get_datapoint`, `read_paramset`,
  `get_device_health`, `list_incidents`, resources for topology /
  health / audit) are always available when the adapter is enabled.
- **Write tools** (`set_datapoint`, `write_paramset`,
  `trigger_program`, anything that reaches `setValue` on a real
  device) are gated behind a separate config switch
  (`cfg.North.MCP.AllowWrites`, default **false**). With writes
  disabled the adapter advertises only the read tools.

The default posture is therefore read-only: enabling MCP does not by
itself let an agent change device state. An operator who wants
agent-driven control opts in twice — `Enabled` and `AllowWrites`.

### Authorization reuses the existing auth chain

MCP tools resolve to the same service layer the REST handlers call,
so they inherit the same authorization checks (`internal/auth`). The
MCP transport authenticates the *connection* (API token, mirroring
the bearer-token path REST already supports — see ADR 0020); the tool
layer enforces per-action authorization through the existing chain.
No new privilege path is introduced — an MCP client can do exactly
what the same token can do over REST, no more.

### Every write is audited

Writes driven through MCP flow through the same audit append path
(`internal/audit`) as REST/WS writes, tagged with the MCP origin so
the change-log distinguishes agent-driven mutations. An agent that
cycles a switch leaves the same audit trail a human would.

### Capability advertisement

The adapter surfaces a `mcp.v1` capability in `GET
/info.capabilities` (the same conditional-capability mechanism that
already gates `matter.bridge`, `mqtt.discovery`, `oidc`, and
`supervised.restart` in `internal/north/rest/handlers/info.go`). The
capability is emitted only when the adapter is enabled. Whether
writes are permitted is a separate, finer-grained capability token
(`mcp.write.v1`) so a client can reason about the posture before
attempting a write tool.

### Transport

The daemon is a long-running service, so the primary transport is
**Streamable HTTP** (the HTTP/SSE MCP transport), mounted alongside
the existing north-bound HTTP listeners rather than on a new port
where avoidable. stdio transport is out of scope for the daemon (it
suits a locally-spawned sidecar, which is Option B's shape, not
ours). The exact mount path and listener wiring are an implementation
detail for the build PR.

### Pure Go, no CGo

The Go MCP SDK and its dependency tree must be MIT/Apache-2.0/BSD and
pure-Go (`CGO_ENABLED=0`), consistent with the dependency-licensing
and no-CGo rules. The SDK choice is vetted in the implementation PR;
if no acceptable pure-Go SDK exists at build time, MCP's JSON-RPC-2.0
framing is small enough to hand-roll over the existing HTTP stack
(the same calculus ADR 0012 applied to the Matter substrate).

### Tool catalogue stays in step with the wire contract

A contract test under `tests/contract/` pins the MCP tool catalogue
against the capabilities it projects, so a tool cannot reference a
REST/service capability that has been removed, and a removed
capability cannot silently orphan a tool. This is the in-tree guard
Option B structurally cannot provide.

## Consequences

### Positive

- First-party agent affordance: an LLM can discover, read, and
  (opt-in) control the fleet through one self-describing surface.
- Architecture stays uniform — MCP is a north-bound adapter on the
  same core, registry, EventBus, auth chain, and audit path as the
  other four.
- Single static binary and `CGO_ENABLED=0` preserved across all three
  distribution channels; no second runtime on the CCU3 Addon path.
- Reuses the ADR-0020 capability handshake and token auth rather than
  inventing a parallel privilege model.

### Negative

- A new dependency (MCP SDK) to vet and patch, or a hand-rolled
  JSON-RPC layer to maintain.
- A new attack surface: a write-capable MCP server reachable by an
  agent is a meaningful authorization boundary. The two-switch
  default-read-only posture mitigates but does not eliminate this;
  operators must understand what enabling `AllowWrites` grants.
- Ongoing parity work: the tool catalogue must track the REST/WS
  surface, enforced by the contract test above.

### Migration

- `internal/north/mcp/` is created on the implementing line, joining
  the existing north-bound adapters under `SPECIFICATION.md` §6.
- `internal/config/config.go` gains `NorthMCP` with `Enabled *bool`
  (default-false) and `AllowWrites *bool` (default-false), wired into
  `NorthConfig` next to `Matter` and `Discovery`.
- `GET /info.capabilities` gains the conditional `mcp.v1` /
  `mcp.write.v1` tokens.
- `cmd/openccu-loom/daemon.go` gains a `startMCPServer` helper guarded
  by the enabled flag, mirroring the Matter and mDNS start/stop
  wiring; registration failure is logged and does not abort startup.
- `example.config.yaml` documents the two switches and the read-only
  default.

## Follow-ups

- **SDK gate — resolved (2026-06-04): use the official
  [`modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk),
  not a hand-rolled framing layer.** Vetting outcome: the official SDK
  reached **v1.0.0 GA on 2025-09-30** (now v1.6.x) with an explicit
  no-breaking-changes compatibility guarantee, is Apache-2.0/MIT
  (no copyleft), pulls no CGo (`CGO_ENABLED=0` clean, light pure-Go
  dependency tree), and exposes Streamable HTTP via
  `NewStreamableHTTPHandler`, which returns a plain `http.Handler`
  the daemon mounts alongside REST/WS on its existing listener — no
  second server, no second runtime. It is maintained by Anthropic +
  the Go team, giving the best longevity / spec-tracking guarantee
  for a long-lived single-binary daemon. Hand-rolling was rejected:
  JSON-RPC-2.0 framing is the easy part, but session management,
  Streamable-HTTP GET/POST + SSE resumability, capability
  negotiation, and spec-revision tracking are not, and the SDK owns
  them under a stability guarantee. Runner-up `mark3labs/mcp-go`
  (MIT, pure-Go, `http.Handler`) is the fallback if the official
  SDK's ergonomics disappoint, at the cost of pre-1.0 churn;
  `metoro-io/mcp-golang` is unsuitable (pre-1.0, no first-class
  Streamable HTTP). Because the SDK path was chosen, **no ADR-0025a
  is needed** — that placeholder was contingent on a hand-rolled
  framing layer becoming a standing component.
- **Audit-log exposure — resolved (2026-06-04): mirror REST.** The
  MCP adapter exposes the audit log as a read-only tool / resource,
  available whenever the adapter is enabled (including read-only
  mode), inheriting the exact authorization the REST `GET /audit`
  route already uses — authenticated, *not* admin-scoped
  (`internal/north/rest/router.go` mounts it as `pr.Get`, not
  `pr.With(admin)`). This follows the ADR's central principle: an MCP
  client can do exactly what the same token can do over REST, no
  more. Gating a *read* behind the `AllowWrites` *write* switch was
  rejected as semantically inconsistent. A separate opt-out
  (`ExposeAudit`) was considered — the `AuditEntry` does carry the
  `user` identity field plus before/after change history, and an LLM
  agent is a qualitatively different consumer than a REST client —
  but rejected for 0.1.x: being more restrictive than REST is
  permitted by the principle ("no *more*"), yet the audit read is
  already available to every authenticated REST token, so MCP
  withholding it would surprise rather than protect. Revisit a
  per-resource opt-out only if operator feedback asks to withhold
  identity-bearing resources from agent surfaces specifically.
