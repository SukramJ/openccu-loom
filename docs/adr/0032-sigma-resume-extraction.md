# ADR 0032 — Sigma resumption extraction: already satisfied (finding corrected)

> **Historical paths.** Since 0.74.0 the Matter stack is a dependency, not a
> subtree: it lives in the [go-fabric](https://github.com/SukramJ/go-fabric)
> module and `internal/north/matter/` no longer exists in this repository. The
> paths below are left as they were when the decision was made — a record
> rewritten to match today's tree stops being a record. For where each piece
> lives now, see
> [`SPECIFICATION.md`](https://github.com/SukramJ/openccu-loom/blob/main/SPECIFICATION.md)
> §6.

- **Status**: accepted (no code change)
- **Date**: 2026-06-15
- **Related**:
  `internal/north/matter/secure/sigma/protocol.go`
  (`ProcessSigma1WithResume`, `tryResume`, `processSigma1Locked`),
  `internal/north/matter/secure/sigma/resume_test.go`,
  the analysis items Area 7 [W6]/[P3] in
  `notes/audits/architecture-analysis-2026-06-15.md`

## Context

The architecture analysis (Area 7) proposed ([P3, M]): *"Extract
`processSigma1ResumeLocked` for targeted resumption tests"*, on the
premise ([W6]) that *"`processSigma1Locked` is 941 lines … carrying full
SIGMA-1/2/3 incl. resumption + multi-fabric + cert-chain in one
mutex-held function; resumption error-path tests are thinner than the
happy path."*

Verified against the code, the premise does not hold.

## Finding

**The 941 is the file, not the function.** `protocol.go` is 941 lines;
`processSigma1Locked` is **139 lines** (the `wc -l` of the file was
mistaken for the function span). It handles only the **full** (non-resume)
Sigma1→Sigma2 path.

**The resume path is already a separate function.** Responder
resumption lives in `tryResume` (82 lines) behind the resumption-aware
entry point `ProcessSigma1WithResume`, not inline in
`processSigma1Locked`. `tryResume` is exactly the
"`processSigma1ResumeLocked`" the analysis asked for — already extracted,
already independently callable, already carrying its matter.js
provenance (`CaseServer.ts` line citations on every KDF step:
KDFSR1/KDFSR2/Sigma2ResumeMIC/SessionResumptionKeys).

**The resume error paths are already tested.** `resume_test.go`
(440 lines) covers the happy path and the three fall-through branches
that matter for security:

- `TestSigma_Resume_RoundTrip` — success.
- `TestSigma_Resume_BadMIC_FallsBackToFullSigma` — forged/stale
  `InitiatorResumeMIC` → full Sigma (spec §4.13.2.4).
- `TestSigma_Resume_UnknownResumptionID_FallsBackToFullSigma` — unknown
  id → full Sigma.
- `TestSigma_Resume_NoStore_FallsBackToFullSigma` — no store wired →
  full Sigma.
- plus `ProcessSigma1Compat`, defensive-copy accessors, and the
  local-vs-peer resumption-id salt invariant.

The only `tryResume` branches without a dedicated test are the four
crypto-primitive error wraps (`hkdfDerive` × 3, `sealResumeMIC` × 1).
Those fire only if a standard-library HKDF/AES-CCM call fails on valid
inputs — unreachable in practice. Exercising them would require
injecting a failing crypto primitive, i.e. adding test-only seams into
security-critical code, which is not justified for defensive
error-wraps.

## Decision

No code change. The structural extraction the analysis requested is
already present (`tryResume` / `ProcessSigma1WithResume`), and the
security-relevant resume error paths are already covered by
`resume_test.go`. This ADR records the investigation and corrects the
Area 7 [W6] finding (function vs. file size; resumption is not inline in
`processSigma1Locked`).

## Alternatives considered

- **Rename `tryResume` → `processSigma1ResumeLocked`** to match the
  analysis's proposed name. Rejected — churn for no behavioural or
  testability gain; `tryResume` is the clearer name and is already cited
  across the test suite.
- **Add tests for the HKDF/AES-CCM error wraps via injected failing
  primitives.** Rejected — adds seams to crypto code purely to cover
  unreachable defensive branches; the risk outweighs the coverage.
- **Further split `processSigma1Locked`** (cert-chain validation, TBE2
  sealing). Out of scope for this item (which is specifically about
  *resumption*); deferred — if pursued it would be its own ADR with a
  matter.js read of `CaseServer.ts`, which is not available in this
  environment.

## Consequences

- The Area 7 [P3] item is closed as "already implemented" with the
  existing function + tests named.
- The [W6] finding is corrected so a future reader does not re-attempt an
  extraction that already exists.
