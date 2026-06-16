# OpenCCU-Loom — Architecture Re-assessment (10-point scale, 2026-06-16)

- **Date**: 2026-06-16
- **Baseline**: [`architecture-reassessment-2026-06-15.md`](./architecture-reassessment-2026-06-15.md)
  (the 9-area, code-grounded re-assessment that scored **7.6 / 10**).
- **Method**: the 7.6 baseline's prioritised next-round was worked through
  as a series of small, independently-verified PRs; this document re-scores
  each area against the *current* tree (HEAD after the merges below) using
  the same nine code-grounded sub-audits and the same rubric. Every "fixed"
  claim was re-verified against source, not against prose — several baseline
  findings had already been resolved by earlier PRs and were **not** redone.
- **Rubric**: 10 = exemplary, no significant issues · 8–9 = strong, only
  minor improvements · 6–7 = solid with real gaps · 4–5 = functional but
  notable weaknesses · 1–3 = significant problems.

## Scorecard

| # | Area | 2026-06-15 | 2026-06-16 |
|---|------|:---------:|:----------:|
| 1 | Domain Core, Hexagonal Architecture & Multi-CCU | 7 | **8** |
| 2 | Southbound Clients, Backends & Transports | 7 | **9** |
| 3 | Reliability, Recovery & Concurrency | 8 | **9** |
| 4 | Persistence & Caching | 7 | **9** |
| 5 | Northbound REST + WebSocket API | 8 | **9** |
| 6 | MQTT Bridge & Payload Assembly | 8 | **9** |
| 7 | Matter Bridge (native-Go, matter.js parity) | 8 | **9** |
| 8 | SPA Frontend | 7 | **8** |
| 9 | Cross-cutting: Security, Config, Obs, Build, Test & Parity | 8 | **9** |
| — | **Overall (mean)** | **7.6** | **8.78 ≈ 8.8** |

> The 7.6 figure in the baseline header was the *first* code-grounded
> re-score; the 2026-06-15 column above reflects the calibrated per-area
> scores as they stood once the security-critical round had merged but
> before the structural round below.

## What changed since the 7.6 baseline

The baseline's prioritised next-round landed as independently-verified PRs.

**Security / correctness tail (closed the entire P1/P2 list):**
- #111 — `Groups.MatterInvoke` → `StatusUnsupportedCommand` (0x81).
- #112 — values-cache WAL checkpoint, audit purge-counter ordering, `List` cap.
- #113 — decoupled `internal/model/custom` from `internal/north/matter`,
  documented `device.Channel` lock order + the throttle design.
- #114 — six boundary-hardening fixes: `ErrNoConnection` → 502 (+ handler
  sweep), 1 MiB body cap, `values_batch` decoder, dummy-bcrypt anti-oracle,
  SHA-256-only token residency, BIN-RPC `PeerAllowlist`.
- #115 — SPA i18n (`api.error.unauthorized`), cast hygiene (`asCdpWidget`),
  openapi-TS TODO tightening.
- #116 — goroutine-lifecycle discipline (`safeFire` WaitGroup, `hub_retry`
  cancellable timer, eventbridge `SafeGo`, MasterPoller drain).

**Structural round (this assessment's deltas):**
- #118 — `SetMasterValue` interfaceID guard + CombinedDP graceful-drop *(Area 6 → 9)*.
- #119 — `handleSubscribeRequest` decomposed into four cited helpers *(Area 7 → 9)*.
- #120 — `Operations` 58-method god-interface segmented into 6 embedded
  capability sub-interfaces *(Area 2)*.
- #121 — SPA component tests (DeviceCard / ChannelStatusBadge / DeviceList,
  42 cases) *(Area 8 → 8)*.
- #122 — migration `Down` completeness (004, 011) + `migrateMu` documented
  load-bearing *(Area 4 → 9)*.
- #123 — **keystone**: the adapter→handlers reverse coupling broken. 58 REST
  contract types relocated — DTOs → `pkg/hmapi`, service interfaces →
  `pkg/interfaces`, model-coupled types → new `internal/restapi`; `handlers`
  keeps `type X = …` aliases so nothing else changed. `grep -rl
  'north/rest/handlers' internal/central/adapter/` is now empty (was 29
  files); no import cycles *(Area 1 → 8)*.

## Per-area deltas (only the areas that moved)

**Area 1 — 7 → 8.** The adapter→handlers reverse coupling (#123) was the
harder, actively-growing violation; it is now cleanly resolved through a
proper `internal/restapi` intermediate plus `pkg/hmapi` / `pkg/interfaces`,
confirmed by a clean `go build ./...`. The remaining item — the ~1226-line
`Unit` god-object (`internal/central/central.go`, 10 typed `Set*Fn` setters
+ `ServiceWiringStatus`) — is deliberately left intact: the heterogeneous
typed setters resist table-driving without surrendering Go's compile-time
type safety. One documented, stable structural item does not hold the score
at 7, but it is the reason this is an 8 and not a 9.

**Area 2 — 7 → 9.** All four previously-open items are closed or
by-design: `Operations` segmented (#120, `backend.go:345` embeds 6
sub-interfaces), BIN-RPC `PeerAllowlist` (#114), MasterPoller WaitGroup
drain (#116), and per-class throttles documented as an intentional
shared-throttle fallback (`ccu_wiring.go:550`, `by_design.md`).

**Area 4 — 7 → 9.** Migration `Down` completeness (#122) plus the
already-landed values-cache WAL checkpoint, purge-counter ordering and
`List` cap (#112). `migrateMu` is confirmed load-bearing (goose v3
package-level globals) and now documented rather than churned.

Areas 3, 5, 6, 7, 8, 9 are unchanged from their post-merge scores; the
contributing PRs are listed above.

## Verdict

**Overall 8.78 ≈ 8.8 / 10**, up from 7.6. Every security-critical finding
is closed, the highest-value structural debt (the reverse-coupling layering
violation) is eliminated, and the Matter / MQTT / persistence surfaces are
each at 9. The remaining gap to a 9.0 mean is concentrated in two
*deliberately deferred* places, both documented rather than hidden:

- the `Unit` god-object (Area 1) and the Matter `Bridge` decomposition
  (ADR 0036) — structural concentrations that are stable, bounded, and
  type-safe as-is; decomposing them buys little and risks regression;
- the 8 `openapi-typescript` reconciliation TODOs (Area 8) — each documents
  a genuine spec-vs-SPA divergence whose correct fix is a spec/back-end
  reconciliation, not a cosmetic edit.

None block deployment on a trusted home LAN. Pushing beyond 8.8 is a
deliberate-investment decision, not a correctness one.
