# OpenCCU-Loom — Deep-Audit Backlog

- **Created**: 2026-07-03
- **Purpose**: A prioritized list of subsystems that still warrant a
  dedicated deep audit. Baseline: since the last full architecture
  re-assessment (`architecture-reassessment-2026-06-16.md`, overall 8.8),
  ~141 non-merge commits and ~33k LOC have landed — mostly in areas that
  have not had a dedicated deep audit since.
- **Related audit-scope docs**: the Matter-side equivalent of this backlog is
  [`docs/parity/matter_behaviour_findings.md`](../parity/matter_behaviour_findings.md);
  the SPA-vs-panel gap assessment is
  [`docs/parity/webui-frontend-comparison.md`](../parity/webui-frontend-comparison.md).
  The forward-looking product roadmap is [`docs/roadmap.md`](../roadmap.md).

## Already covered (no re-audit needed now)

- **Matter bridge** — freshly deep-audited by the 0.24.0 code-vs-code
  re-audit against matter.js; parity held by standing guards
  (`docs/matter-parity-contract.md`).
- **MQTT transport** — extracted to the shared `go-mqtt` module (v0.2.0),
  shared codebase, recently touched.
- **Security / Authz (northbound)** — deep-audited 2026-07-03 (five
  parallel sub-audits: AuthN chain, route-authz matrix, token/session
  lifecycle, credential crypto, input/SSRF/WS). All four HIGH findings and
  the load-bearing MEDIUM/LOW items were remediated on
  `fix/northbound-security-audit`. Deliberately deferred, each with a
  rationale (see that branch's CHANGELOG + PR notes):
  - **HA-ingress admin-by-default** (spoofable `X-Ingress-Path` from the
    shared Supervisor subnet). The real fix is Supervisor
    `POST /ingress/validate_session` on the `ingress_session` cookie — a
    feature that needs its own ADR (per-request Supervisor round-trip,
    failure-mode handling). Flipping the passthrough default off would break
    the add-on's seamless ingress UX, so it was NOT changed. **Follow-up.**
  - **CSRF token session-binding** and **OIDC `nonce`** — defense-in-depth
    already covered by the double-submit + SameSite baseline and by PKCE +
    the new state-cookie binding respectively.
  - **GCM AAD binding for at-rest secrets** — conflicts with the ADR 0027
    plaintext-passthrough design and needs an `enc:v2` re-seal migration;
    threat requires DB-write access.

## P1 — High (new, high-risk, unaudited)

### 1. Daemon lifecycle & live-CCU adopt
The newest and most concurrency-heavy code path — `bridge.Registry`
(phased start/teardown), `BringUpManager.AddCentral/RemoveCentral`, live
adopt/remove without a daemon restart. Never audited. Focus: teardown
ordering, goroutine leaks when RemoveCentral races an in-flight bring-up,
adopt-vs-callback races, partially-registered centrals on error.
Entry points: `cmd/openccu-loom/daemon.go`, `internal/north/bridge/`,
`internal/central/adapter/`.

### 2. Persistence & history rollup + retention
The history/energy wave: raw→hourly→daily rollup tiers, retention/eviction,
energy aggregation. Data-integrity and growth sensitive. Focus: rollup
correctness across time zones / DST, retention deletion vs. concurrent
aggregation (TOCTOU), unbounded growth when eviction is missing, energy
aggregation accuracy. Entry points: `internal/store/sqlite/measurements.go`,
the history/rollup scheduler, the energy REST handler.

### 3. Backup / restore & scheduled backups
Scheduled/automatic CCU backups with rotation. Restore is destructive.
Focus: rotation correctness (off-by-one, concurrent schedules), archive
bombs / path traversal on restore, secret handling inside the backup blob,
behavior on a partial/corrupt backup. **Cross-links to a confirmed HIGH
from the security audit** (the encryption master key `secret.key` is
included in the backup tarball alongside the ciphertext).
Entry points: `internal/north/rest/handlers/backup.go`, backup adapter,
scheduler.

## P2 — Medium (grown or only partially checked)

### 4. Southbound reliability under multi-CCU reconnect churn
Circuit-breaker / retry / throttle / coalescer / ping-pong scored 9/10 in
2026-06, but behavior fixes (BidCos-RF value-load skip, HmIP-DLD/FWI) and
the new adopt/remove path have landed since. Re-verify under central
churn rather than re-score from scratch. Entry points: `internal/client/`
(reliability), `internal/client/backends/`.

### 5. Event bus & coordinator concurrency
The internal typed/generic event bus (priority, no re-entrancy) across all
eight coordinators. Focus: unsubscribe lifecycle under load, lost events on
central teardown, deadlock potential in the documented lock order
(`device.Channel`), fan-out backpressure. Entry points:
`internal/central/events/`, the coordinators under `internal/central/`.

### 6. Config / configui validation pipeline
`ClassifyFields` → schema → section editor → save roundtrip, field reset.
The secret half is covered by the security audit; what remains is the
validation/type depth: strict type validation of complex fields, merge
semantics on partial saves, restart-required flagging, i18n completeness
(label + help, en + de). Entry points: `internal/config/`,
`internal/configui/`, `admin_config.go`, `config_field_reset.go`.

### 7. hmcli (new Cobra CLI)
Brand-new surface (devices / sysvar / program / paramset / events-tail).
Never audited. Focus: credential handling (how the token is supplied, does
it leak into shell history / the process list), secret output in `--json`,
error/exit-code semantics, WS event-tail auth. Entry point: `cmd/hmcli/`.

## P3 — Low / opportunistic

### 8. Observability, metrics & tracing
Focus: label-cardinality explosion (per-device / per-DP Prometheus labels),
sensitive data in traces/logs, PII/secrets in audit rows. An operational /
privacy risk, not a correctness one. Entry points: `internal/metrics/`,
`internal/observability/`, `internal/audit/`.

### 9. SPA frontend logic depth
Playwright e2e + visual regression lock the operating concept, but the
logic layer of the newer views (energy, fleet, access-control, session-based
MASTER editing with undo/redo/dirty-tracking) is unaudited. Focus: stale
cache / race in session editing, error paths without a toast, client-side
authz assumptions. Entry points: `assets/ui/src/lib/` (stores), view
components.

### 10. Migration down-path & data-loss safety
A completeness sweep of every goose migration: `Down` truly reversible, no
silent data loss, `migrateMu` semantics on parallel start. Partially
addressed previously but not swept as a whole. Entry point:
`internal/store/sqlite/migrations/`.

## Suggested order

Start with **P1.1 (daemon lifecycle / live-adopt)** — the largest, newest,
most concurrency-risky surface with no audit history. Then **P1.2 / P1.3**
(history integrity, backup/restore safety).
