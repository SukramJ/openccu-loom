# Round 8 — live verification of what rounds 5–7 shipped

Date: 2026-08-24. Target: a real CCU at `172.18.4.39` (11 devices, 293 Matter
exposure candidates) and the chip-tool CI suite against `main`.

## Why this round is not another reading round

Rounds 5–7 added roughly forty guards, four capability tokens, two `/health`
components, a config key and an API minor — and a measurement taken at the
start of this round found that **none of it appeared anywhere in
`tests/chiptool/` or `tests/integration/`**. The last commit touching
`tests/chiptool/` was three audit rounds old. We had been building an
increasingly precise model of a system nobody had held against reality.

Every check below is a **read**. No CCU write was performed, so no device
approval was required.

## Results

| What | Verified | How |
|---|---|---|
| `/health` component `security` | ✅ live, `healthy` | `GET /health` against the real daemon |
| `/health` component `discovery.mdns` | ✅ live, `healthy` | plus `dns-sd -B _openccu-loom._tcp` finding the instance on two interfaces — the component agrees with the network |
| Four capability tokens | ✅ all four | negative control first (two absent while their features were off), then positive on a fresh `data_dir` with them on |
| `/diagnostics/wiring` (ADR 0065) | ✅ 30 seams at runtime, **0 ordering violations** | 33 declared in source; three are config-gated |
| Prometheus parser (round 7) | ✅ real body parses clean | the parser had only ever seen the hermetic harness body |
| `north.matter.include_measurements` | ✅ **materialises an endpoint** | see below |
| chip-tool suite | ✅ green on `main` | `workflow_dispatch`, both jobs incl. `matter-smoke (compose PASE)` |

### The Matter measurement flag, end to end

The round-6 finding and the round-7 fix, verified on a real device:

- `GET /matter/exposable` offers `OPERATING_VOLTAGE_LEVEL` on an `HmIP-SAM`
  (`000F1A498D01EE`) as `mappable`, `clusters=[47]`, no own device type — the
  "rides on a host endpoint" shape.
- Allowlisted with `include_measurements: true` → `endpoints=0 → 1`, and
  `GET /matter/endpoints` returns three: RootNode, Aggregator, and one with
  **device type 0** — the measurement.
- **Negative control**: fresh `data_dir`, same data point allowlisted,
  `include_measurements: false` → `endpoints=0`, two endpoints only.
- The endpoint survives a daemon restart.

## Nothing was found wrong

No defect. That is the honest result, and it is worth as much as a finding
would have been: the features rounds 5–7 shipped behave against a real CCU the
way the hermetic tests said they would.

## What the round did find: eight instrument errors, all mine

Every apparent defect during this session dissolved under its own control.
They are recorded because the pattern is now the most reliable finding this
audit series produces.

1. **`/devices` returns `{"items": […]}`**, not a bare list or `devices`. My
   parser read zero devices for most of the session while eleven were loaded,
   and I built and half-believed a whole hypothesis about the Matter assembler
   on that reading.
2. **A YAML edit after first boot does nothing.** `north.mqtt.enabled: true`
   in the file, `False` in the effective config — because the database tier
   wins and is seeded on the first run. This is documented verbatim in
   `docs/admin/configuration.md`: "editing the YAML alone will not change a
   setting that is already seeded". I had to read the daemon's own SQLite to
   find what the documentation says on page one.
3. **Matter endpoints persist**, so flipping the flag off and restarting does
   not retract an assembled endpoint. My first negative control measured the
   database, not the flag.
4. **Device ingest is slower than a 90-second poll**, which fed error 1.
5. A `nohup`-started daemon dies with the tool call that started it unless it
   is detached properly; `setsid` does not exist on macOS.
6–8. Three earlier ones from the same session's effect-test work: an unmatched
   replacement string in a `cp`-based revert reports green; `runHubDiscoveryRestart`
   recovers panics, so a broken fixture looks like a dead seam; an unstarted
   alarm engine makes `Reload` return before the hook.

The rule this earns, stated once more because it keeps being the answer:
**an instrument that cannot be shown to produce the other answer has measured
nothing** — and across this whole audit series the instrument has been wrong
at least as often as the code.

## Consequence for the release

The 15 unreleased entries are now verified against a real CCU and a real
commissioner. The release they belong in is **0.65.0**, not a patch: four new
capability tokens, a new config key and two new `/health` components are
additive features.
