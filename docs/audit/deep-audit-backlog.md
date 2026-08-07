# OpenCCU-Loom — Deep-Audit Backlog

- **Created**: 2026-07-03
- **Last sweep**: 2026-08-07 — every P1–P3 item below was audited in one
  pass (ten parallel read-only audits, one per item). This document is
  now the register of what those audits found: what was fixed, what is
  still open, and what was examined and deliberately left alone.
- **Purpose**: track which subsystems have had a dedicated deep audit,
  and carry the findings that outlive the release that produced them.
- **Related audit-scope docs**: the Matter-side equivalent of this backlog is
  [`docs/parity/matter_behaviour_findings.md`](../parity/matter_behaviour_findings.md);
  the SPA-vs-panel gap assessment is
  [`docs/parity/webui-frontend-comparison.md`](../parity/webui-frontend-comparison.md).
  The forward-looking product roadmap is [`docs/roadmap.md`](../roadmap.md).

## What the 2026-08 sweep taught

Nearly every confirmed defect looked like success at runtime, and the CI
was green for all of them:

- The reconnect endpoint answered `success` without sending a byte to the
  CCU, and force-closed the circuit breaker on the way.
- A live-adopted CCU sat in the registry and reached no north-bound plane
  at all until the daemon restarted.
- The history rollup never ran on a daemon restarted more often than
  hourly — and the purge then deleted nothing, because its own floor sat
  at the watermark the rollup would have raised.
- A secret masked on stdout was served in cleartext by the diagnostics
  download.

The shared shape: **a green result that nothing cross-checks against the
effect it claims.** The guards added in 0.54.3 (see below) all target
that shape rather than the individual bugs.

## Standing guards that came out of the sweep

| Guard | Location | What it prevents |
|---|---|---|
| `TestDeclaredSilentEventDocsClaimNoConsumers` | `tests/contract/` | An event declared consumerless whose doc still names consumers |
| `TestRatchetReasonsAreNotDeferrals` | `tests/contract/` | Ratchet entries justified with "yet" / "for now" instead of a decision |
| `TestEventBridgeWSDeliveryWithoutMQTT` | `internal/central/adapter/` | An optional plane (MQTT) gating a WebSocket emission |
| `TestAdoptedCentralReachesTheEventBridge` | `cmd/openccu-loom/` | A runtime-adopted central wired to nothing |
| `TestCaptureAndLiveLogCarryRedaction` | `pkg/hmlog/` | Diagnostics artefacts bypassing redaction |
| `TestClearWaitsForInFlightHandlers` | `internal/central/events/` | Teardown freeing what a running handler still uses |
| `TestRollupDailyNeverOutrunsTheHourlyTier` | `internal/store/sqlite/` | A watermark advancing past the data that feeds it |

## Audited subsystems

Each entry states when it was audited and where the findings went.

### P1.1 Daemon lifecycle & live-CCU adopt — audited 2026-08-07
Fixed in 0.54.3: adopted central never reached the event bridge
(CRITICAL); a removed central resurrected by a concurrent cache clear
(CRITICAL); DELETE during an in-flight adopt orphaning a live central
(HIGH); the security domain's unwire running only on rollback (HIGH).
Open: adopted centrals get no descriptor persistence, so they re-pull
the whole inventory over the air after every restart (MEDIUM).
Examined, no action: `AddCentral` check-then-act (transitively guarded
by the registry's own duplicate check); north-bridge stop ordering
(loses last publishes only, no crash).

### P1.2 Persistence & history rollup + retention — audited 2026-08-07
Fixed in 0.54.3: rollup and eviction never ran below an hourly uptime
(HIGH); the daily watermark advanced past the hourly frontier, opening a
permanent hole (HIGH); day and month buckets folded on the UTC calendar
while the UI labelled them local (HIGH); rollup tiers surviving central
and device removal (MEDIUM).
Open: disabling history also disables eviction, so the database freezes
at full size (MEDIUM); tier promotion collapses narrow charts for young
series (MEDIUM); `measurements_daily.bucket_ts` has no index (LOW).
Examined, no action: purge/read TOCTOU — unreachable at any sane
retention, since the delete cutoff sits ~30 days below the read window.

### P1.3 Backup / restore & scheduled backups — audited 2026-08-07
Fixed in 0.54.3: non-atomic save leaving restorable torsos (MEDIUM);
`Prune` running without the per-central lock, able to evict a complete
backup in favour of an in-flight one (MEDIUM); the daemon tarball
swallowing every CCU archive (MEDIUM); `GET /backups` not admin-gated
(LOW).
Open: no content check of the archive before it is uploaded to the CCU
(MEDIUM); the download handler commits a 200 before streaming can fail
(LOW); backup ids collide at one-second resolution (LOW).
Verified fixed earlier: the encryption master key is no longer inside
the backup tarball — the previously confirmed HIGH from the security
audit.

### P2.4 Southbound reliability under multi-CCU churn — audited 2026-08-07
Fixed in 0.54.3: `Recovery.Subscribe` multiplying handler sets and
heartbeat loops per interface and per bring-up generation, defeating the
attempt cap (HIGH); ping-pong pending entries never swept, pinning the
health component DEGRADED until restart and suppressing every mismatch
incident (MEDIUM-HIGH); the reconnect endpoint running a no-op pipeline
and reporting success (MEDIUM).
Open: the readiness gate is not applied to the `activate()` retry loop
(SUSPECTED); the BidCos-RF value-load skip also swallows forced reloads
(LOW).
Examined, no action: callback routing, caller-id collisions, circuit
breaker half-open transitions, per-interface recovery serialization —
all correct.

### P2.5 Event bus & coordinator concurrency — audited 2026-08-07
Fixed in 0.54.3: the `Clear*` family neither retiring handlers nor
waiting for in-flight ones, so central teardown freed adapters under
running handlers (HIGH); `DeviceCreatedEvent` published under the
coordinator mutex, across foreign handler code including broker I/O
(MEDIUM, latent — it deadlocks the daemon the first time a handler
reaches back).
Examined, no action: the dispatcher hand-off argument in `bus.go` holds
— enqueue and take-over both run under `b.mu`, so the window the comment
rules out genuinely does not exist. The documented `device.Channel` lock
order is respected everywhere; no publish happens under a channel lock.
Open: fan-out has no backpressure isolation — one slow handler stalls the
dispatch and grows the deferred queue (MEDIUM, by design today, but the
hub plane publishes inline).

### P2.6 Config / configui validation pipeline — audited 2026-08-07
Fixed in 0.54.3: the `north.rest` row duplicating its nested auth
sub-trees, so resetting the HA-Ingress auth passthrough did not stick
(HIGH, security-relevant); a partial section PUT validating a merged
candidate while persisting the raw fragment (MEDIUM); the restart-flag
lists disagreeing, so `alarm.*` and the auth switches reported "no
restart needed" (MEDIUM).
Open: `Effective()` bases on `Default()` while the daemon bases on YAML,
producing phantom restart-pending states and a permanently stuck banner
for YAML-only fields (MEDIUM-HIGH); `callback.port_range` is unreachable
config (MEDIUM); several fields accept nonsense unvalidated — locale,
webhook URL and timeout, MCP path, Matter listen/discriminator, negative
durations (LOW-MEDIUM).
Examined, no action: `strictUnmarshal` correctly rejects wrong types and
unknown keys recursively; the masked-secret round-trip is intact.

### P2.7 hmcli — audited 2026-08-07
Fixed in 0.54.3: path traversal through a server-supplied
`Content-Disposition` filename (MEDIUM-HIGH); terminal-escape injection
and an uncapped response body on error paths (MEDIUM); undifferentiated
exit codes (LOW).
Open: the password prompt does not suppress echo (LOW); `--insecure`
warns about nothing (LOW); credentials embedded in `--host` appear in
error messages (LOW).
Examined, no action: WebSocket auth travels in a header, not a query
parameter; TLS verification is only ever disabled behind the explicit
flag; every user argument is properly escaped into URLs.

### P3.8 Observability, metrics & tracing — audited 2026-08-07
Fixed in 0.54.3: capture archive and live-log ring bypassing redaction
(HIGH); raw parameter values — including a keypad or lock access code —
persisted into the append-only audit log (MEDIUM).
Examined, no action: **no label-cardinality risk exists** — the metrics
registry has no label dimension at all, and every metric name is static
or bounded by an enum. `/metrics` is authenticated (not admin-only,
which is acceptable for its content).

### P3.9 SPA frontend logic depth — audited 2026-08-07
Fixed in 0.54.3: a stale-response race that could write one channel's
values into another channel's MASTER paramset (HIGH); lock flags set
without a cancel guard and never reset, poisoning a panel for its
lifetime (HIGH); undo not restoring locked fields (MEDIUM); take-over
errors and a failed device fetch failing silently (MEDIUM).
Open: `runAction` reports through a banner instead of a toast (LOW);
`ProfileSelector` may keep its selection across a channel switch
(SUSPECTED); i18n and locale gaps in Energy and AccessControl (LOW).
Examined, no action: no WebSocket subscription leaks; client-side authz
is backed by server-side gating everywhere it matters.

### P3.10 Migration down-path & data-loss safety — audited 2026-08-07
Every one of the 38 migrations has a syntactically real `Down`; the
problem is semantic. Six destroy unrecoverable data (identities with
their password and token hashes, the append-only alarm journal, argon2id
alarm codes, Matter key material, the audit trail, the frozen zone slug
that HA entity IDs are built from), and `020`'s down/up cycle wipes every
config section on the next boot.

`goose down` is offered nowhere — no Make target, no CLI subcommand, no
documentation — so the exposure is theoretical today. **Decision: the
down path is not supported.** Rather than building data-preserving
downs for a path nobody invokes, each destructive down states plainly
what it destroys, the policy is documented, and a contract guard keeps
new destructive downs from landing without that note.

## Open items, by weight

Everything above that is still open, ordered by what it costs an
operator. This list is the input for the next pass.

**Data / correctness**
1. Adopted central without descriptor persistence — full re-pull over
   the air after every restart (P1.1).
2. Disabling history freezes the database at full size (P1.2).
3. No archive validation before a restore reaches the CCU (P1.3).
4. `Effective()` vs YAML — phantom restart-pending and a stuck banner
   (P2.6).

**Robustness**
5. Readiness gate missing on the `activate()` retry loop (P2.4,
   SUSPECTED — verify before fixing).
6. Fan-out backpressure: the hub plane publishes inline (P2.5).
7. Tier promotion collapses narrow charts for young series (P1.2).

**Polish / hygiene**
8. hmcli: password echo, `--insecure` silence, credentials in error text.
9. SPA: banner-instead-of-toast, ProfileSelector selection, i18n gaps.
10. Backup: download status committed early, second-resolution id
    collisions.
11. `callback.port_range` dead config; unvalidated config fields.
12. Missing index on `measurements_daily.bucket_ts`.

## How to run the next sweep

The 2026-08 sweep used ten parallel read-only audit agents, one per item,
each briefed with the subsystem's entry points and a demand for
`file:line` evidence plus a concrete failure scenario per finding —
explicitly forbidding speculation and requiring a CONFIRMED/SUSPECTED
label. Findings then went to separate fix agents working in isolated
worktrees, one per theme.

Two things made the difference and are worth repeating:

- **Ask for the failure scenario, not the smell.** Every finding had to
  name who calls what in which order, and what breaks. That is what
  separated the real defects from the tidy-looking ones.
- **Measure the guard.** For each fix, remove the fix and confirm the
  new test goes red. Several tests that looked like they pinned a defect
  passed with the defect restored.
