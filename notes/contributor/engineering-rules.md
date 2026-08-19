# Engineering rules — the long form

The root [`CLAUDE.md`](../../CLAUDE.md) states each of these rules in one
or two lines and names the guard that enforces it. This document keeps the
full reasoning: the defect each rule was written after, why the obvious
shape of the fix does not work, and what the guard actually checks.

Read it when you are about to touch one of these areas, when a guard fires
and you want to know what it is protecting, or when you are tempted to add
an entry to one of the ratchets.

### A test that constructs the collaboration proves nothing about the wiring

This is the failure mode that has cost this project the most, twice, in
the same quarter: the hub notifiers in 0.52.12, then two critical and
several high defects across the Security & Safety series. In every case
the CI was green on every PR.

The shape is always the same. A test constructs collaborator A, hands it
collaborator B itself, and asserts they work together. That proves the
collaboration **can** happen. It never proves that anything in a running
daemon **makes** it happen. `hub_notifier_wiring_test.go` documents the
canonical instance: the coordinator tests called `SetHubModel`
themselves, so they stayed green while no production path ever called
it and every hub push event was silently lost.

Call it a **bracketing test** and treat it as a defect, not a style
preference.

The four rules below exist because of it. Each names the guard that
enforces it — a rule without a guard becomes decoration within a
release.

#### Wiring is pinned through the composition root, never at the setter

Adding a `Set*` / `Attach*` / `Register*` method that production **must**
call obliges you to add a pin under `tests/contract/wiring_pins/` that

- constructs through the real constructor (`New`, `wireXService`, the
  daemon's composition root), and
- asserts the **effect** — the event arrives, the state is populated —
  never that the setter was called.

`internal/central/hub_notifier_wiring_test.go` is the reference: it goes
through `New` alone and touches only the surfaces the real daemon
touches.

Guard: `TestEveryWiringSetterHasAProductionCaller` (`tests/contract/`).

It checks the defect signature rather than the pin, because no test can
verify that a pin asserts the right thing, while the signature is exact:
in 0.52.12 `SetHubModel` had **no production caller at all**. The guard
resolves every `Set*` / `Attach*` / `Register*` that injects a
collaborator — interface, func value or pointer — and fails on those
production never calls, through a direct call or an interface it
dispatches on. Test files are excluded from the load, so a seam only its
own tests call counts as unwired, which is the point.

The pin half of this rule remains reviewed, not enforced. Both halves
matter: a seam can have a caller and still be unpinned.

Two ratchets carry the current surface: `wiringSettersWithoutCaller` for
seams verified as deliberately silent, and `wiringSeamsUnderInvestigation`
for seams nobody has classified. They are separate on purpose — merging
them would let "we looked and it is fine" and "we have not looked" wear
the same face.

#### Walking the central registry once is walking it at boot

A subsystem that subscribes to every central it finds in
`central.Registry.List()` sees only the CCUs present when it ran. A CCU
adopted at runtime is silent on that plane until the daemon restarts, and
nothing anywhere reports it — the boot walk is correct, its tests are green,
and the gap is invisible. Thirteen instances were found by hand in one audit.

Do not write the walk. Register a `central.Registry.OnRegister` observer
instead: it replays over every central already registered and runs again for
every central registered afterwards, and its unwire is run when that central
leaves the registry. Boot and runtime adopt become one registration, so
there is no second half to forget — which is what the previous shape
(a boot walk plus a hook registered on the orchestrator) kept losing.

The exception is a subsystem whose attach order relative to the south-bound
bring-up is load-bearing. Those keep a named seam on `centralOrchestrator`
(`setMatterCentralHook`, `setAlarmCentralHook`, …) that runs at a defined
point in `adoptCentral`, and the composition root calls it with a
`*central.Unit`.

Guard: `TestEveryRegistryWalkerHasAnAdoptSeam` (`tests/contract/`), with
`registryWalkersWithoutAdoptSeam` as its ratchet for the walkers that
deliberately re-run the whole walk instead.

#### A lifecycle test uses the production order

If production starts a service and *then* feeds it asynchronously, the
test must do the same. Pre-seeding state that production populates later
inverts the order and hides exactly the bug it should catch — a
Security & Safety integration test registered a fully loaded central
*before* `Start`, so an index that is permanently empty in production
looked correct for months.

Every daemon-level subsystem carries a boot-order assertion: start the
real daemon, let the model arrive **afterwards**, assert the subsystem
reports non-empty state.

The middle clause is the whole test. Against a CCU that answers
instantly the daemon finishes the south-bound bring-up before the domain
services start, so every subsystem reads a populated model and the test
passes however broken the wiring is — measured, not assumed: the first
version of this guard stayed green with the historical fix removed. Boot
the simulated CCU **not ready** (`harness.Options{StartCCUNotReady:
true}`), then flip it, and the real order is restored.

Guard: `tests/e2e/boot_order_test.go`
(`TestE2EDaemonLevelSubsystemsReportNonEmptyStateAfterBoot`) — black-box,
against the built binary, one table entry per subsystem. It is black-box
because boot order is a property of the composition root: any test that
assembles the collaborators itself gets to choose the order, and will
choose the working one.

#### Declared and published must be the same set

Any north-bound plane that declares entities (MQTT discovery above all)
needs a round-trip test: collect every topic named in a discovery
payload, collect every topic the publisher actually writes, assert the
two sets match.

Declaring `security/class_smoke` while publishing `security/class/smoke`
produces entities that appear in Home Assistant and stay `unavailable`
forever. Payload-shape tests and publish-call tests both passed; nothing
compared them.

Guard: one `Test*PlaneTopicsRoundTrip` per plane, in
`internal/north/mqtt/`.

#### An event nobody consumes is a dead feature, and it looks identical to a live one

Two shapes of the same defect.

**The switch that drops.** A sink or dispatcher that type-switches over a
domain's events needs a table test that publishes **every** event type the
domain defines and asserts each one arrives. The `default:` branch is a
test failure, not a log line. `internal/alarm/service.go` logged `alarm
sink dropped unknown event type` for `AlarmDuressEvent` — a duress code
under coercion produced one hidden journal row and nothing else, on every
surface, under every configured visibility level.

**The event with no subscriber at all.** The bus has no wildcard
subscription, so an event nothing subscribes to reaches nothing — and
every surrounding test still passes, because the producer's test asserts
it published and the would-be consumer's test publishes onto its own bus.
This shape reliably leaves a comment behind claiming consumers that do not
exist: `engine.go` announced `AlarmDuressEvent` "for the MQTT/webhook
consumers" when only the webhook subscribed, and `device_pipeline.go`
announced `WeekProfileChangedEvent` "so MQTT/WS subscribers" receive it
when neither did. **A comment naming a consumer is a hypothesis; write
the check instead.**

Guards: one `Test*SinkFansOutEveryEventType` per fan-out, driven from the
domain's `EventType*` constants — and `TestEveryEventTypeHasASubscriber`
(`tests/contract/`), which resolves every `events.Subscribe` through the
type checker and fails on any event type that has no consumer and no
declared reason in `eventsWithoutSubscriber`. Declaring the silence is
allowed; leaving it undeclared is not.


### Comments in code

Comments must offer **durable value to a future reader** — explain the
*why* of the code, not the audit-row or wave that requested the change.
Internal tracking codes are illegible to anyone who joins the project
later, and the documents they point at decay fast. `make test` blocks
on `TestDocPurity` (under `tests/contract/`) which enforces these
rules mechanically.

**Forbidden in `//` comments (TestDocPurity):**

- Wave / Welle / phase tags: `Wave-3`, `W6-A`, `Welle 4`, `Phase-3`,
  `Phase 4`, `migration step N`.
- Audit item IDs: `A3-L05`, `L7.4`, `M1234`, `G-24`, `V8-N29`, `Q-23`,
  `QW-23`.
- Drift IDs in every observed shape:
  - `Drift L0-D01`, `drift L1-NEW-2` (with the literal `Drift` prefix)
  - `L9-D8`, `L2-D06`, `L10-D02` (bare layer-drift IDs)
  - `L9-NEW-5`, `L5-NEW-D03` (NEW-suffix forms)
  - `L3-D6-FUTURE`, `L0x-D_FUTURE_OBSERVER` (skip-placeholder suffixes)
  - `L0-OC-01` (sub-system-specific IDs)
- Audit-run references: `audit run #02`, `parity audit`,
  `parity sweep`, `parity_audit.md`, `parity_request.md`.
- Audit date stamps: `\b2026-0[456]-\d{2}\b` and any peer pattern.
- German/English audit hybrids: `MANDATORY-FEHLT`.
- Legacy-project provenance tokens in code comments: `aiohomematic`,
  `homematicip_local`, `pydevccu`, `openccu-data`, etc. — these belong
  in the markdown documentation, not in production code.
- Short German function-words: `darf`, `soll`, `muss`, `nicht`, `über`,
  `dürfen`, `müssen`, `während`, `damit`, `dafür`, `daher`, `liefert`,
  `enthält`, `erlaubt`, `ergänzt`, `bzw.`, `z.B.` — code comments stay
  in English.

**Markdown references must point at durable documents.**
`TestDocPurity_MarkdownRefsExist` walks every `.md` reference in a
`//` comment and fails when the cited file is missing on disk. Cite:

- ✅ Permanent docs: `CLAUDE.md`, `SPECIFICATION.md`,
  `docs/adr/*.md` (ADRs are immutable once landed),
  `notes/parity/by_design.md`, `notes/reference/matter-conformance.md`,
  `notes/concepts/matter-ui-concept.md`, and the matter.js / chip source-file
  references (`packages/.../X.ts:line`, `src/.../Y.cpp:line`).

Do NOT cite transient audit-trail files in code comments: audit-run
reports, hand-off memories, todo files, ad-hoc parity sweeps. The
audit-trail lives in Git history + `notes/parity/by_design.md` (the
living catalogue of intentional divergences); code comments should
reference neither.

**Rewrite pattern** when removing audit-tracking from an existing
comment — preserve the rationale, drop the tracking tag:

```go
// Before:
// Drift L8-D01 (parity audit 2026-05-12): FeatureMap (0xFFFC) and
// ClusterRevision (0xFFFD) must be enumerated so the initial
// Subscribe pre-populates Apple's HAP-mapper cache.

// After:
// FeatureMap (0xFFFC) and ClusterRevision (0xFFFD) must be enumerated
// so the initial Subscribe pre-populates Apple's HAP-mapper cache.
// Mirrors chip AdministratorCommissioningCluster.cpp:53-56.
```

What *stays* in the comment: the invariant, the matter.js / chip
provenance with `path:line`, the spec section (`Matter §11.18.6.4`),
the observable symptom when broken. What *goes*: the audit row, the
date, the wave number, the FUTURE-skip placeholder ID.

**Exceptions:**

- `ha_`-prefixed files (legacy HA-compat zone) — out of scope.
- `tests/integration/testdata/` — golden wire data, untouched.
- `tests/contract/doc_purity_test.go` itself — it enumerates the
  forbidden patterns in its own doc-comment.

If you need to discuss audit provenance, write it into the commit
message body or a Markdown doc — both survive code churn far better
than a comment that names a row in a deleted spreadsheet.


### Documents in markdown

Markdown docs (`*.md` under the repo) are deliberately held to a
**looser** standard than production code comments — they are the
home of audit metadata, drift catalogues, and timestamped tracking.
The one rule that *does* transfer cleanly is **link integrity**.

**`TestMarkdownLinksValid`** (in `tests/contract/`) walks every
`.md` file and fails when a Markdown-style link (square brackets
followed by a parenthesised target) points at a file that does
not exist on disk. Anchor fragments (`#section`) are tolerated
against the file but anchor existence is NOT verified (would
require a Markdown parser).

What is checked:
- Relative-link targets (e.g. `./sibling.md`, `../parent.md`)
  resolved against the linking file's directory.
- Absolute targets (leading `/`) resolved against the repo root.
- Directory targets (trailing `/`) count as satisfied if the
  directory exists.

What is NOT checked:
- Bare path tokens in prose (`see by_design.md`) — would
  false-positive on every mention.
- External URLs, `mailto:`, `tel:`, `ftp:` — ignored.
- Reference-style Markdown links (`[text][ref]` + `[ref]: url`) — rare in
  this repo; ignored.

Exclusions:
- `node_modules/`, `spa_dist/`, `.git/` — vendored or out of scope.

What is *not* a markdown-purity rule (and why):
- **Drift-IDs, audit dates, "parity sweep" mentions** — `by_design.md`
  is the audit-trail itself; banning these tokens would break the
  document it exists to populate.
- **Legacy-project names (`aiohomematic`, `pydevccu`, …)** —
  `CLAUDE.md`, `SPECIFICATION.md`, ADRs need to name these projects.
- **German words** — beispielhafte deutschsprachige Zitate sind in
  Doku ok, even though they would trip `TestDocPurity` in code.
- **Audit date stamps** — "Last update: 2026-05-12" headers are
  normal markdown metadata.

The asymmetry is deliberate: code is the durable artefact, markdown
is the conversation about that artefact.

---

## A verification without a negative control measures nothing

The wiring rules above are all one defect wearing different clothes: a check
whose outcome does not depend on the thing it claims to check. The bracketing
test passes whether or not production wires the seam. The boot-order test
passes whether or not the fix is present, when the CCU answers instantly. The
payload-shape test passes whether or not the published topic matches the
declared one.

The same defect governs verification itself, and it is the one that turns an
honest agent into a confabulating one — because the untethered check reliably
returns the answer that was expected.

A worked example from the session that produced this document. The question
was whether a `disabledMcpServers` entry actually suppresses a user-scope MCP
server in one project. The check: start a headless session and ask whether the
server's tools are reachable. Answer: no. That looks like confirmation.

The negative control — the same question with the entry removed — also
answered no. Headless sessions do not load those tools at all, so the check
was measuring the harness, not the setting. Both runs were consistent with
"the setting works" and equally consistent with "the setting does nothing".
The correct report was *unverified*, and that is what was written.

The rule:

> Before reporting something as verified, name the result the check would have
> produced had the claim been false. If you cannot name it, or the check cannot
> produce it, the finding is unverified.

Three practical consequences:

- **A passing check is half an experiment.** The other half is the control.
  For a config switch: flip it back. For a guard: remove the production line.
  For a log line: make the code path not run.
- **Watch for shared confounders.** A control run inside the same process, the
  same daemon, or the same warm cache as the test run may share the very thing
  that decides the outcome. The MCP probe above had exactly this problem twice
  — the second attempt counted processes that the *asking* session was itself
  keeping alive.
- **"Unverified" is a finding, not a failure.** It tells the reader precisely
  what they still have to check. A false "verified" removes that information
  and cannot be distinguished from a true one afterwards.
