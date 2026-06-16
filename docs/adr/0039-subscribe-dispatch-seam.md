# ADR 0039 — Extract cohesive sub-helpers from `handleSubscribeRequest`

- **Status**: accepted
- **Date**: 2026-06-16
- **Related**:
  `internal/north/matter/bridge/subscribe.go` (`handleSubscribeRequest`),
  `internal/north/matter/bridge/subscribe_dispatch.go`,
  `docs/adr/0031-im-opcode-dispatch-seam.md` (style template),
  the analysis item Area 7 in
  `docs/audit/architecture-reassessment-2026-06-15.md`,
  [matter.js as the Matter gold standard](../../CLAUDE.md)

## Context

`handleSubscribeRequest` (`subscribe.go`, 200+ lines, carrying
`//nolint:gocognit,gocyclo,funlen`) is the bridge's inbound Subscribe
handler. It chains four coherent concerns in one function:

1. **Initial ReportData assembly** — per-path dispatcher reads with
   DataVersionFilter evaluation, EventRequests merging, sort by
   (endpoint, cluster, attribute), diagnostic log-per-attribute.
2. **Subscription registration** — manager `Subscribe` call with
   KeepSubscriptions teardown (CASE and PASE paths), `captureSubTarget`,
   and `HasSubscription` stamping on the report.
3. **Chunked initial-report streaming** — `chunkReportData` split,
   per-chunk encode + TLV strict-validate + disk-dump diagnose hook,
   atomic arm-StatusResponseWait → sendReplyReliable → block-or-timeout
   loop (the Apple-pairing-critical per-chunk ack sequence).
4. **SubscribeResponse send** — MaxInterval cap from manager, piggyback
   ack counter refresh (`LookupAndDischarge`), `sendReplyReliable`,
   `TouchLastReport`, final diagnostic log.

The reassessment (Area 7) identified this as the residual W4 item not
reached by the ADR 0031 extraction: `handleIMOpcode` was decomposed but
`handleSubscribeRequest` retained its `gocognit,gocyclo,funlen`
suppressions.

This is gold-standard-sensitive code (the Matter side mirrors matter.js
HEAD). matter.js is **not** checked out in this environment, so this ADR
is deliberately a **behaviour-preserving refactor only** — it moves
existing code and carries the existing matter.js citations verbatim
(e.g. `InteractionMessenger.ts:sendDataReportMessage`,
`InteractionServer.ts:549-566`, `ReadClient.cpp:541`). It introduces
no new cluster schema, wire shape, status code, or dispatch decision.

## Decision

Extract four `*Bridge` methods into `subscribe_dispatch.go`, mirroring
the ADR 0031 pattern of moving opcode bodies into `receive_dispatch.go`:

### 1. `buildInitialReport(subCtx, dispatcher, req) im.ReportData`

Verbatim body of the attribute-reading, DataVersionFilter evaluation,
EventRequests merging, sort, and per-attribute diagnostic log block.
Returns the assembled `im.ReportData` with `HasSubscription=false`.

### 2. `registerSubscription(src, requestHdr, proto, req, *initialReport) uint32`

Verbatim body of the manager Subscribe block (KeepSubscriptions teardown
for both CASE and PASE paths, `m.Subscribe(SubscribeArgs{...})`,
`captureSubTarget`, `HasSubscription`/`SubscriptionID` stamp). Mutates
`initialReport` in place; returns `subID` (0 when no manager or error).

### 3. `streamInitialReportChunks(src, requestHdr, proto, subID, initialReport) error`

Verbatim body of the chunk loop: `chunkReportData`, per-chunk
`EncodeReportData`, TLV strict-validate hook, diagnose log + disk dump,
`armStatusResponseWait` → `sendReplyReliable` → `select`-on-waitCh-or-
timeout. Keeps `//nolint:gocognit` on this method because the atomic
arm→send→wait sequence cannot be decomposed without introducing a race;
the comment documents the reason.

### 4. `sendSubscribeResponse(src, requestHdr, proto, req, subID, initialReport) error`

Verbatim body of the SubscribeResponse path: MaxInterval cap from manager,
`EncodeSubscribeResponse`, `LookupAndDischarge` piggyback counter,
`sendReplyReliable`, no-`dischargeOwedAck` rationale comment,
no-primer-ReportData rationale comment, `TouchLastReport`,
`matter.rx.im.subscribe` diagnostic log.

`handleSubscribeRequest` becomes a thin orchestrator (~40 lines) calling
the four methods in sequence. The `gocognit,gocyclo,funlen` nolints are
removed from the orchestrator; `gocognit` is retained on
`streamInitialReportChunks` alone.

## Alternatives considered

- **Single extracted method for chunks + subscribe-response.** Rejected:
  the chunk streaming and the subscribe-response send are semantically
  distinct (different Matter spec sections §10.6.6 vs §10.6.5); merging
  them would conflate concerns that are individually legible when separate.
- **Expose `buildInitialReport` as a non-method package-level function.**
  Rejected: the function reads `b.eventLog` and calls `b.logger`; a
  method is the natural shape and aligns with the `dispatchReadRequest`
  / `dispatchWriteRequest` / `dispatchInvokeRequest` pattern from ADR 0031.
- **Wait for a matter.js checkout to re-verify.** Unnecessary: this is a
  pure code move with no semantic change, so the existing citations remain
  valid by construction. A behaviour change would require the read; a
  relocation does not.

## Consequences

- `handleSubscribeRequest` is a ~40-line orchestrator; the per-concern
  bodies are individually legible without scrolling.
- `streamInitialReportChunks` is now unit-testable without a full
  subscribe round-trip at the chunk-budget level (e.g. a fake report
  with known attribute count can assert the correct number of chunks).
- Behaviour is identical — locked by the existing end-to-end subscribe
  test suite (`subscribe_encryption_test.go`,
  `subscribe_initiated_test.go`, `subscribe_extra_test.go`,
  `scenario_test.go`) plus `go test -race ./internal/north/matter/bridge/`
  passing unchanged.
- No new matter.js semantics are introduced; existing citations are
  preserved verbatim. A future Matter behaviour change in these paths
  still follows the matter.js-first workflow once the reference is
  available.
