# OpenCCU-Loom — open roadmap items

This file tracks deliverables that are scoped but not yet started —
both work prioritised for an upcoming cycle and work we have explicitly
deferred. Items land here when we commit to revisiting them later.
Completed items are moved to `CHANGELOG.md`; the roadmap keeps only what
is still open, blocked, or a recorded decision *not* to do something.

Accepted items link to a detailed, transferable implementation plan
alongside this file where one exists.

**This is the canonical forward-looking plan.** Two neighbouring backlogs
cover a different scope and are not alternatives to this one:
[`doc-backlog.md`](../doc-backlog.md) tracks documentation gaps only, and
[`../audits/deep-audit-backlog.md`](../audits/deep-audit-backlog.md) carries
the findings of the subsystem deep-audit passes. When in doubt about what is
still open, trust this file plus `CHANGELOG.md`.

**Round 5 of the full-codebase audit has its own strategy document.**
[`round-5-audit-strategy.md`](./round-5-audit-strategy.md) records why a fifth
instance sweep is not the plan — the density data, the six measures that
replace it, and the metrics that say whether it worked.

## Open development items

### Composition root

- **Declare the remaining ordered seams in `cmd/openccu-loom`**
  ([ADR 0065](../../docs/adr/0065-composition-root-wiring-is-checkable.md),
  accepted 2026-08-23).

  Two adoptions are done. The first covers the per-central registry observer:
  eighteen call sites attach through `Registry.OnRegisterDeclared`. The second
  covers ordering, which is the class the audits actually keep hitting — a
  collaborator handed over after the thing that reads it has already started.
  `wiring.Mark` names a boot boundary, an ordered seam declares which marks it
  must precede and follow, and `Manifest.Attach` evaluates that at the moment
  the seam attaches. Six seams are declared today, and moving one across its
  boundary turns the end-to-end test red with the consequence spelled out.

  What is left is volume, not mechanism: `cmd/openccu-loom` carries more
  constraints of exactly this shape, each currently a comment some distance
  from the step it talks about. `webhook.alarm_bus` is the pattern — a setter
  whose prose said "before the PhaseLate StartAll" five hundred lines from the
  `StartAll` in question.

  Also still open: struct-field seams, where a collaborator arrives as a field
  of a deps literal rather than through a call. Those need a shape decision
  first — a literal has no attach point to hang a declaration on.

  And one named gap: a mark has to be an unconditional boot boundary, so a
  constraint relative to an optional subsystem's start is not expressible. The
  Matter bridge's per-central readiness latch, which must precede its first
  assembly, is the instance. Closing it means marks that may be absent, and
  then a third answer beside satisfied and violated — see the ADR's
  consequences for why that is a shape to avoid rather than reach for.

  `wiringSettersWithoutCaller` (19 entries) should keep shrinking toward
  deletion as each seam becomes exactly checkable.

### Matter

- **Matter IM protocol depth.** The cluster/schema layer is broad, and
  the *subscribe state machine* (cadence enforcement, max-subscription
  caps, teardown) **and** the *Timed-interaction gate* are already
  implemented and wired (`cmd/openccu-loom/daemon_matter.go`,
  `internal/north/matter/im/receive_dispatch.go`,
  `StatusNeedsTimedInteraction` in `internal/north/matter/im/receive.go`).
  The genuine remaining gaps, in tractability order:
  - *OTA Provider* — only `OtaSoftwareUpdateRequestor` exists; no
    provider cluster (`internal/north/matter/cluster/core/`). **Decided
    scope: a schema-correct "NotAvailable" responder** (QueryImage
    replies schema-conformant `NotAvailable`), **not** BDX firmware
    hosting — mirrors matter.js `OtaSoftwareUpdateProviderServer`.
  - *GroupKeyManagement persistence* — the group table returns empty;
    persistence is unwired
    (`internal/north/matter/cluster/core/group_key_management.go`).
  - *Subscribe refinements* — small follow-ups (e.g. whether event
    replay must survive a restart); see the plan.
  - *Not a gap:* ACL enforcement is complete (`CheckACL` in
    `internal/north/matter/endpoint/dispatcher.go`, attached in
    production at `cmd/openccu-loom/daemon_matter.go`).
  *Reads matter.js first; mirrors behaviour; extends the parity guards
  per [`docs/matter-parity-contract.md`](../../docs/matter-parity-contract.md).*
  *Plan: [`notes/plans/A1-matter-im-depth.md`](A1-matter-im-depth.md).*

- **CASE resume and the session id it reuses.** *Blocked on hardware, not on
  effort.* The resume fast path (`Responder.tryResume`,
  `internal/north/matter/secure/sigma/protocol.go`) keeps the session id the
  responder already announced, where matter.js allocates a fresh one
  (`packages/protocol/src/session/case/CaseServer.ts` `#resume` calls
  `getNextAvailableSessionId`). Both answers carry a real risk: reusing an id
  can conflate the peer's previous message counters and subscriptions with the
  new session, and renewing one burns an id per **MRP retransmit** of a resume
  Sigma1, which would drain the 16-bit space on a lossy network. Neither can be
  settled from the spec text — both failure shapes are interop failures that
  look fine in isolation — so it needs a capture from a live controller across a
  bridge restart or a partition. The observability that makes such a capture
  readable already shipped (a debug record per resume, an `occupancy` block on
  `GET /api/v1/matter/sessions` and in the Matter diagnostics view, and a resume
  row in the live-controller brief). What remains open is the decision itself,
  the retransmit-idempotence guard either answer needs, and the
  `by_design.md` entry recording the divergence.
  *Plan: [`notes/plans/matter-case-resume-session-id.md`](matter-case-resume-session-id.md).*

### Device model

- **Custom-DP fields still bound by a fixed parameter name.** A custom data
  point resolves the wire fields it composes by a constant parameter on its own
  channel, while the device profile's channel-group schema states both the
  parameter and the channel per device family. 0.64.0 moved the climate family
  onto the schema; three further families are confirmed broken by the same
  mechanism, each measured against the device descriptions a real CCU sends:
  HM-LC-JaX materialises without its slat axis (`LEVEL_SLATS`, not `LEVEL_2`),
  HmIP-DLD never reports a jammed motor (`ERROR_JAMMED` sits on the channel
  before the lock), and HmIP locks never report a direction (`ACTIVITY_STATE`,
  not `DIRECTION`). *Effort: M. Priority: medium — each is a feature that is
  silently absent, not a crash.*
  *Plan: [`notes/plans/custom-dp-profile-schema-binding.md`](custom-dp-profile-schema-binding.md).*

### REST API

- **Two pagination envelopes across list endpoints.** `/devices` returns
  a `{items, page, per_page, total}` body, while the hub lists return a
  bare array plus an `X-Total-Count` header (`assets/openapi.yaml`).
  Pick one — the header form is the more common REST convention — and
  align the other. *Effort: S. Priority: low.* Note the constraint that
  blocked the sibling rename below: changing a response *shape* is a
  breaking change for the `api contract guard` CI job, so this lands
  bundled with a deliberate API-version step, not as a drive-by.

## Reviewed and deferred

Considered in review and explicitly **not** scheduled now, with the
reason — so they are not re-proposed without new information:

- **Observability ecosystem (OTel metrics/logs, shipped dashboards).**
  Deferred. Metrics already render Prometheus exposition format
  (scrapeable); a full OTel SDK fights the dependency-lean stance. If
  revisited, ship Grafana dashboards + alert rules for existing gauges
  (`events.DeferredHighWaterAlert`) first, hand-rolled OTLP later — no
  SDK.
- **Discovery hardening.** Deferred — no field symptom; the
  serial-match path is wired, backfilled (`ccu_wiring.go`) and tested
  (`routingkey/canonical_test.go`, ~660 LoC discovery tests).
- **MQTT discovery state → SQLite.** Dropped — the in-memory state
  (`wiring.go`, `lastDiscovered` hash cache) is an optimisation whose
  loss is harmless (`RepublishDiscovery` re-emits everything on
  reconnect regardless).
- **Generic hot-reload extension.** Dropped — most sections are already
  hot-applied; the restart-required set (`internal/config/restart.go`)
  is genuinely structural and deliberately excluded
  (`internal/config/watcher.go`). The one valuable case (live CCU adopt)
  has since shipped.
- **O(1) program/sysvar lookup index.** Deferred — premature; gated on
  profiling per `by_design.md` §A5; linear scan
  (`coordinators/hub.go::HubStatePaths`) is fine at fleet ≤ 300.
- **Configurable optimistic burst window.** Deferred — the timeout is
  already per-DP configurable (`DataPoint.OptimisticTimeout`); the
  burst window (`model/optimistic/tracker.go`) is a placeholder for an
  unimplemented time-bounding feature with no user pain.
- **RPC-handler semaphore cap.** Deferred — premature; gated on
  profiling per `by_design.md` (net/http goroutine-per-connection
  already isolates slow handlers; CCU throttle bounds input rate).
- **Dead-code harvest.** Deferred — the curated genuine dead set is only
  ~9 functions (`notes/parity/dead-code-genuine.json`); the large
  "unreachable" count is by-design API mirrors. Trivial cleanup at best,
  not a project.
- **Cross-stack diff in CI.** Deferred — the Go side already runs
  (`.github/workflows/cross-stack-parity.yml`); the full bidirectional
  diff is intentionally local/release (no aiohomematic venv in CI), and
  tightening a parity guard runs counter to the de-prioritisation of
  parity.
- **Path-naming drift: snake_case + colon-action segments.** Deferred,
  and the rename was reverted once already. `week_profile` (and its
  children) plus `/devices/values:batch` break the kebab-case /
  plain-segment convention, but renaming a *served* path removes the old
  one, which `oasdiff` (the `api contract guard` CI job) classifies as
  BREAKING — disproportionate for a cosmetic rename inside an additive
  release. Revisit only bundled with a real deprecation cycle: serve both
  paths, mark the old one `deprecated`, remove it a major later.
- **Local event-driven automation / rules engine (cross-CCU).** Deferred
  — large, and it competes directly with Home Assistant's own automation
  engine for users who already run one.
- **Scenes: saved multi-device value presets with one-call execution.**
  Deferred — medium effort, no operator demand recorded yet.
- **Built-in push sinks (ntfy / Pushover / Telegram).** Deferred — the
  webhook plane already carries these via an external relay.
- **Energy cost / tariff tracking alongside kWh.** Deferred — small, but
  tariff modelling belongs in the consuming system, not the bridge.
- **`LinkCoordinator.SetLinkInfo` / `GetLinkInfo` wiring.** Deferred —
  the backend supports both, but no consumer exists. Wire them only
  together with a REST/WS caller that needs them.
- **Boot-order e2e coverage for the central-registry observer.** Deferred —
  `central.Registry.OnRegister` and its guard shipped, and the ordering
  guarantees (replay over the centrals already registered, attach in
  registration order, unwire in reverse, attach before `Unit.Start` on adopt)
  are pinned in package and composition-root tests. Extending
  `tests/e2e/boot_order_test.go` as well, as the plan's risk section suggested,
  is the one remaining hardening; it is not scheduled. The order-sensitive
  hooks that stayed named on `centralOrchestrator` are a decision, not a
  leftover — see
  [`central-registry-observer.md`](central-registry-observer.md) and
  [`CLAUDE.md`](../../CLAUDE.md) §"Walking the central registry once is
  walking it at boot".
- **Issuer-scoped identity for federated logins (`iss` + `sub`).** Deferred —
  the OIDC subject is folded and a federated principal is scoped by scheme, so
  the reported revocation defect is closed. Re-keying the identity on the
  issuer plus `sub` is the architecturally cleaner answer, but it rewrites the
  subject of every existing federated session and every audit row that
  references one, and it needs a migration for the audit trail. Revisit only
  with that migration; the residual it would also close — an API token carries
  no scheme, so a token minted for a federated subject is purged with a local
  account of the same name — is recorded in
  [`oidc-subject-canonicalisation.md`](oidc-subject-canonicalisation.md).

## Depth-parity execution plan (aiohomematic)

**Status**: structural port (phases 0–10) complete; the depth-parity
follow-up (guard hardening, cheap model closes, resolver hardening, and
the Homegear track) is **concluded**. Homegear is at parity with the
defined target (aiohomematic's *Homegear* support, not the CCU):
sysvars load + refresh over XML-RPC, and programs/rooms/functions are
empty by design on both stacks. Going beyond that target (full CCU-like
depth, non-HomeMatic Homegear families, a Homegear-native sysvar-create
path) is the explicit non-goal in `SPECIFICATION.md` §2.2 and is not
planned. Matter parity is a separate track against matter.js — out of
scope here (see
[`docs/matter-parity-contract.md`](../../docs/matter-parity-contract.md)).

One depth-parity item remains open, blocked upstream:

- **`valve.Modulating` profile-registry wiring — at parity, blocked.**
  The type and constructor exist and are largely complete
  (`internal/model/custom/valve/`, Info/Config/HA-discovery), but no
  device profile maps onto it — `valve/init.go` registers only
  `DeviceProfileIPIrrigationValve`. Crucially, the reference stack has
  **no modulating-valve device either** — it ships only an irrigation
  valve (`IP_IRRIGATION_VALVE`). So leaving `valve.Modulating`
  unregistered is parity, not a gap; wiring it would mean inventing a
  device mapping the reference does not have. The divergence is recorded
  in `notes/parity/by_design.md` (entry `A2-BD03`).
  Wire it in `init.go` only if such a device appears upstream.
  *Effort: S. Blocked on a real device.*

## Upstream pin: openccu-data

**Status**: steady state, fully automated — nothing open.

The CCU metadata archives are consumed as the versioned Go module
`github.com/SukramJ/go-openccu-data`, not vendored into this repo. An
upstream release fires a `repository_dispatch` that regenerates the
module, and dependabot delivers the bump here as a reviewable PR;
`make bump-ccudata` is the manual fallback. See
[ADR 0053](../../docs/adr/0053-go-openccu-data-module.md) for the decision and
[ADR 0003](../../docs/adr/0003-embed-occu-extracts.md) for the underlying embed
rationale.

Two items that earlier revisions of this roadmap carried as open are
now closed and are recorded here so they are not re-proposed:

- *A companion Go module for the archives* — **done**, that is exactly
  what `go-openccu-data` is (ADR 0053). It no longer waits on a second
  Go consumer.
- *A native Go re-implementation of the CCU extractors* — **dropped**.
  openccu-data is the single source of truth for the whole ecosystem;
  the port would duplicate ~3500 LoC of curated heuristics for no
  incremental benefit.
