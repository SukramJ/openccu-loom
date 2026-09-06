# ADR 0031 — Extract a testable gate + per-opcode seam from `handleIMOpcode`

> **Historical paths.** Since 0.74.0 the Matter stack is a dependency, not a
> subtree: it lives in the [go-fabric](https://github.com/SukramJ/go-fabric)
> module and `internal/north/matter/` no longer exists in this repository. The
> paths below are left as they were when the decision was made — a record
> rewritten to match today's tree stops being a record. For where each piece
> lives now, see
> [`SPECIFICATION.md`](https://github.com/SukramJ/openccu-loom/blob/main/SPECIFICATION.md)
> §6.

- **Status**: accepted
- **Date**: 2026-06-15
- **Related**:
  `internal/north/matter/bridge/receive.go` (`handleIMOpcode`),
  `internal/north/matter/bridge/receive_test.go`,
  the analysis item Area 7 [W4]/[P1] in
  `notes/audits/architecture-analysis-2026-06-15.md`,
  [matter.js as the Matter gold standard](https://github.com/SukramJ/openccu-loom/blob/main/CLAUDE.md)

## Context

`handleIMOpcode` (`receive.go`, ~390 lines, carrying
`//nolint:gocognit,gocyclo,funlen`) is the bridge's inbound
Interaction-Model entry point. It interleaves four concerns in one
function:

1. **Pre-dispatch gates** — absorb a `StatusResponse` (MRP-level ack,
   no dispatch); reject a non-request opcode; reject Read / Subscribe /
   Timed over a Secure Group session (Matter §8.5.7); drop silently when
   no dispatcher is attached.
2. **TLV decode** — per-opcode `im.Unmarshal*TLV`.
3. **Per-opcode handling** — Read / Write / Invoke / Subscribe / Timed,
   each ~60–120 lines of fabric/subject context stamping, timed-gate
   checks, dispatch, reply encoding, ack discharge, and diagnostics.

The analysis (Area 7 [W4]) flagged that the gate + decode +
session-validation logic has **no unit-testable seam**: every path
requires a fully-wired `*Bridge`, so the rejection rules (which opcodes
are valid, which are group-session-legal) can only be exercised
end-to-end. Subscribe is already extracted to `handleSubscribeRequest`;
the timed gate is already `checkTimedGate`. The rest is inline.

This is gold-standard-sensitive code (the Matter side mirrors matter.js
HEAD). matter.js is **not** checked out in this environment, so this ADR
is deliberately a **behaviour-preserving refactor only** — it moves
existing code and carries the existing matter.js citations verbatim
(e.g. the group-session reject already cites
`InteractionMessenger.ts` for the `InvalidAction` rule). It introduces
no new cluster schema, wire shape, status code, or dispatch decision.

## Decision

Two extractions, both behaviour-preserving:

### 1. A pure gate-decision function

```go
// imGateAction is the pre-dispatch routing decision for an inbound IM
// message, computed from opcode + session type alone — no Bridge state,
// so it is table-testable in isolation.
type imGateAction int

const (
    imGateProceed           imGateAction = iota // decode + dispatch
    imGateAbsorbStatusResp                       // StatusResponse: MRP ack, no dispatch
    imGateRejectUnsupported                      // non-request opcode
    imGateRejectGroupSession                     // Read/Subscribe/Timed over a group session (§8.5.7)
)

func classifyIMOpcode(opcode uint8, sessionType message.SessionType) imGateAction
```

`classifyIMOpcode` holds the exact decision tree currently inlined at the
top of `handleIMOpcode`. Being free of `*Bridge`, it is unit-tested with
a table over every opcode × {unicast, group} → expected action — the
testability win the analysis asked for. The matter.js citation for the
group-session rule moves onto this function.

### 2. Per-opcode dispatch methods

Each inline opcode body moves **verbatim** into a `*Bridge` method
mirroring the existing `handleSubscribeRequest`:
`dispatchReadRequest` / `dispatchWriteRequest` / `dispatchInvokeRequest`
/ `dispatchTimedRequest`. The router decodes (as today) and hands the
decoded request to the method. All logging, comments, matter.js
citations, fabric/subject stamping, timed-gate checks, and ack discharge
move unchanged.

`handleIMOpcode` then reads as a thin router:

```go
switch classifyIMOpcode(proto.Opcode, requestHdr.SessionType) {
case imGateAbsorbStatusResp:  return b.absorbStatusResponse(src, proto)
case imGateRejectUnsupported: return b.rejectUnsupportedOpcode(src, proto)
case imGateRejectGroupSession: return b.rejectGroupSession(src, requestHdr, proto)
}
dispatcher := b.Dispatcher()
if dispatcher == nil { /* drop silently */ return nil }
dec := tlv.NewDecoder(payload)
switch proto.Opcode {
case im.OpcodeReadRequest:   /* decode */ return b.dispatchReadRequest(...)
case im.OpcodeWriteRequest:  /* decode */ return b.dispatchWriteRequest(...)
case im.OpcodeInvokeRequest: /* decode */ return b.dispatchInvokeRequest(...)
case im.OpcodeSubscribeRequest: /* decode */ return b.handleSubscribeRequest(...)
case im.OpcodeTimedRequest:  /* decode */ return b.dispatchTimedRequest(...)
}
return nil
```

The `gocognit,gocyclo,funlen` nolints come off `handleIMOpcode`; each
extracted method keeps only the nolints it individually needs.

## Alternatives considered

- **Gate function only, leave opcode bodies inline.** Rejected: the
  decode-and-route monolith (the bulk of the 390 lines) would keep its
  `funlen`/`gocyclo` suppressions and stay un-decomposed. The gate
  function alone is a partial answer.
- **A generic decode seam (`decodeAndRoute`).** Rejected: each opcode
  decodes to a distinct typed request and dispatches to a distinct
  handler; a generic seam would need type erasure (`any`) and lose the
  compile-time typing the per-opcode methods keep.
- **Wait for a matter.js checkout to re-verify.** Unnecessary: this is a
  pure code move with no semantic change, so the existing citations
  remain valid by construction. A behaviour change would require the
  read; a relocation does not.

## Consequences

- The opcode-validity and group-session rules become unit-testable
  without a `Bridge` (`classifyIMOpcode` table test).
- Each opcode handler is independently testable and individually
  legible; `handleIMOpcode` is a ~30-line router.
- Behaviour is identical — locked by the existing end-to-end
  `receive_test.go` cases (`TestDispatch_IMReadRoutes`,
  `TestDispatch_IMSubscribeReplies`, `TestDispatch_IMInvalidOpcode`,
  `TestDispatch_IMResponseOpcodeIsUnsupported`,
  `TestDispatch_NoDispatcherDropsSilently`) plus the new gate table test.
- No new matter.js semantics are introduced; existing citations are
  preserved verbatim. A future Matter behaviour change in these paths
  still follows the matter.js-first workflow once the reference is
  available.
