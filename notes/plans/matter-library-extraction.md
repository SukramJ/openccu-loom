# Extracting the Matter Implementation into a Reusable Go Module

**Working document — `notes/plans/matter-library-extraction.md`**
Status: the decisions in §7 are taken. **Phases 0 and 1 are implemented and green** (branch `feature/matter-phase0`, ten commits; `make test` and repo-wide `make lint` both clean) — §4.0 records what each milestone actually did and where it diverged. Phases 1 onward are not started. **D18 (2026-09-05) deferred goal 3** — go-fabric is not developed toward matching `home-assistant-matter-bridge` — while keeping the extraction, because the Matter side should be testable independently of the daemon. §0.6.1 carries the reasoning and what it changed.
Every claim about the current code carries a `path:line` or verbatim command output. Claims that were adversarially re-checked are stated plainly; everything else is marked **UNVERIFIED** in those words, and §8 collects them so nothing hides in prose.

**The three goals this roadmap is measured against** (stated 2026-09-04; §0.6 records how far the plan meets them):

1. **The module preserves the status quo in `openccu-loom`.** Extraction changes no behaviour a user or a commissioned controller can observe.
2. **The module is usable, unchanged, in unrelated projects.**
3. **A `go-matter-bridge` can be built on it that is functionally equivalent to `home-assistant-matter-bridge` (hamb) — and that bridge carries no Homematic domain knowledge.**

Goal 3 sets the coverage target: every Home Assistant entity domain hamb bridges today, bridged from Go, at matter.js-level cluster fidelity. Milestones are ordered by how many HA domains each unlocks, not by how tidy the refactor is.

**Reference checkouts:** `../matter.js` @ `75633fafba9f327c198d6c0ad9160f0d1042176b`, tag `v0.18.0-alpha.0-20260903`, 2026-09-03 (`git -C ../matter.js log -1`); `../home-assistant-matter-bridge`; `../core` (Home Assistant) @ `29f9b353c8a9201ace9fb49d7f1138e21c32d102`, 2026-09-02; `../connectedhomeip`. Repo state: branch `fix/south-core-audit-findings`, HEAD `9d3f2f21`.

**The tree measured was dirty.** `git status --short | wc -l` at the time of writing returned **157** — **104** modified and **53** untracked files (`git status --short | awk '{print $1}' | sort | uniq -c` → `53 ??`, `104 M`) across `internal/{alarm,central,client,model,store}` and `pkg/hmenum`. No `path:line` in this document was taken against a clean checkout; re-run §9 before citing any line number.

---

## 0. Executive answer

### 0.1 Is there a clean boundary to split at?

Yes — but it does **not** run at `internal/north/matter/`. It runs one level in, between the protocol/cluster layer and the model-walking layer.

**Measured, this session:**

* 52,061 non-test LOC in 164 files under `internal/north/matter/` (`find … ! -name '*_test.go' -exec cat {} + | wc -l`), against 95,771 test LOC in 301 test files.
* **20,118 LOC** already have zero host imports in production code. They still need the `internal/` move (C16) and, for `store`, a test-side DDL bootstrap — `internal/north/matter/store/testhelper_test.go:14` imports `internal/store/sqlite` for its migrations. "Zero host imports" is not "zero work".
* **14,536 LOC** of `cluster/*` touch exactly three host concepts, all mechanical.
* **15,149 LOC** of `bridge` + `endpoint` + `eligibility` look hopeless at package granularity — `go list -deps` returns 20 host packages for `bridge`, 18 for `endpoint`, 17 for `eligibility` — but **per-file measurement flips the verdict**: only 2 of 21 non-test `bridge` files, 3 of 7 in `endpoint`, and 1 of 3 in `eligibility` name a host package. **19 of the 21 non-test `bridge` files — 9,911 of 11,549 LOC — name no host package at all**; the coupling sits entirely in `bridge.go` (1,536 LOC, three names, C7) and `health_probe.go` (102 LOC, one name, C5).

### 0.2 What must happen to reach matter.js level?

Two separable programmes with different shapes, and they are **not** equally unblocked.

* **Extraction** (Phases 0–2): mechanical, and now unblocked — all fourteen §7 decisions were taken on 2026-09-04. Phase order is the only remaining constraint.
* **Coverage** (Phase 3, the XL half): the application clusters that actually ship on the wire live *inside* `internal/model/custom/*`, bound to Homematic pointers. A second host inherits the wire stack and the sensor clusters and would have to write OnOff / LevelControl / ColorControl / WindowCovering / Thermostat itself.

### 0.3 The coverage quantities, with their provenance separated

**Measured here:**

| Quantity | Command / source |
|---|---|
| **51** non-test `MatterClusterID()` implementations repo-wide | `grep -rn "func ([^)]*) MatterClusterID() uint32" --include='*.go' . \| grep -v _test.go \| wc -l` → 51 |
| **14** of them inside `internal/model/` | same grep scoped to `internal/model/` → 14 |
| **104** non-test `MatterWrite` / `MatterInvoke` implementations repo-wide | `grep -rn "func ([^)]*) MatterWrite(\|func ([^)]*) MatterInvoke(" --include='*.go' . \| grep -v _test.go \| grep -v worktrees \| wc -l` → 104 |
| **273** device types carried in the generated schema table | `grep -c "^\t0x" internal/north/matter/schema/devicetypes.go` |
| **19 + 3** device types loom actually emits | `grep -n "DeviceType:" internal/north/matter/endpoint/*.go \| grep -v _test` → **nine** lines; **six** are `Endpoint` literals (`assembler.go:185,190,644,710,799,843`), `:881` writes a `store.EndpointRecord`, and `materialize.go:262,267` re-render an already-assigned value into `DeviceTypeList`. The 19 is the enumeration in §3.2 (the model-side `MatterDeviceType()` producers plus the 7 measurement-derived and 2 hard-coded group classes, deduplicated), not a count of assignment sites |
| **24** HA domains in hamb's registry | `../home-assistant-matter-bridge/packages/backend/src/matter/endpoints/legacy/create-legacy-endpoint-type.ts:76-104`, read verbatim |
| **135 / 53 / 82** matter.js cluster servers / hand-written / generated stubs | `ls ../matter.js/packages/node/src/behaviors/*/*Server.ts \| wc -l`; `grep -l 'THIS FILE WILL BE REGENERATED' … \| wc -l` |

**Inherited from the prior coverage survey and NOT re-measured here — do not quote as this document's finding:**

* **"38 of the 120 bridge-reachable cluster servers."** The 120-cluster universe is a cross-join of matter.js's device-type requirement blocks against the `classification: "simple"` filter, and the 38 is loom's intersection with it. Neither half was re-run in this session. **UNVERIFIED by me.** What *is* measured is the 51 implementations, the 14 in `internal/model`, and the per-cluster table in §3.3, which is grounded file-by-file.

**Measured, this session (previously inherited):**

* **"HA 2026.9.0 declares 45 entity platforms, hamb leaves 26 unmapped." MEASURED, this session.** `../core` is a sibling checkout at HEAD `29f9b353c8a9201ace9fb49d7f1138e21c32d102` (2026-09-02); `homeassistant/const.py:24-26` pins 2026.9.0. `grep -c '^    [A-Z_0-9]* = "' homeassistant/generated/entity_platforms.py` → **45**. Nineteen of hamb's 24 registry entries are HA entity platforms; the other five — `input_boolean`, `input_button`, `input_select`, `automation`, `script` — are helper integrations that never appear in `EntityPlatforms`. 45 − 19 = **26 unmapped**: `ai_task`, `air_quality`, `assist_satellite`, `calendar`, `camera`, `conversation`, `date`, `datetime`, `device_tracker`, `geo_location`, `image`, `image_processing`, `infrared`, `lawn_mower`, `notify`, `number`, `radio_frequency`, `siren`, `stt`, `text`, `time`, `todo`, `tts`, `update`, `wake_word`, `weather`.

The honest headline is therefore the **domain** score, not the cluster ratio: **13 of hamb's 24 domains fully reachable, 5 partial, 6 absent** (§3.1) — every row of which is grounded in a `path:line`.

### 0.4 The single highest-leverage item

Not a new cluster: `endpoint.Snapshot.Devices []*device.Device` (`internal/north/matter/endpoint/types.go:28`). Until the assembler's input type stops being the CCU device tree, no Home Assistant host can produce a topology at all.

### 0.5 Four corrections that would otherwise generate phantom work

Carried up front because they change decisions.

1. **`valve.Irrigation` → OnOffPlugInUnit 0x010A is sanctioned, not a defect.** `docs/adr/0049-matter-one-endpoint-per-device.md:47-50` reads verbatim: *"**Bridge `valve.Irrigation` as OnOff on its primary channel.** This supersedes ADR 0012's two `valve.Irrigation` "stays MQTT-only" rows (the Out-of-scope table and the Custom-DP mapping table). The valve is a first-class OnOff / OnOffPlugInUnit endpoint like any other switch."* Framing the gap as "an unintended projection ADR 0012 forbids" cites a superseded ADR. The real, narrower gap stands: cluster 0x0081 and device type 0x0042 are not implemented.
2. **The `pkg/interfaces` Matter block is deliberately two-consumer.** `docs/adr/0012-matter-pure-go-implementation.md:381-385` states the placement reason verbatim (*"Lives in `pkg/interfaces` because both the bridge and the model need to declare dependencies on the same types"*), and **nine** packages under `internal/model/` implement or reference `interfaces.Matter*` in production (`calculated`, `custom`, `custom/{climate,cover,light,lock,siren,switch}`, `generic` — `grep -rln 'interfaces\.Matter' --include='*.go' internal/model/ | grep -v _test | sed 's|/[^/]*$||' | sort -u`). So the fix routes to a **second neutral `pkg/` package**, never into `internal/north/matter` — that would invert the import direction the ADR protects. The measurable win is exactly one thing: `go list -deps ./internal/north/matter/cluster/core` lists `pkg/hmapi`, while `grep -rn 'hmapi\.' internal/north/matter/` (non-test) returns **0**.
3. **Ethernet-only `NetworkCommissioning` is a test-pinned scope limit, not a gap.** `cluster/core/network_commissioning.go:22-24` documents it, `:215` returns Ethernet-only FeatureMap, `:238-242` rejects every command, `:253-271` excludes the WI|TH-conformance attributes, and `network_commissioning_test.go:112-128,184-193,244-251` pins all three.
4. **`MatterDeviceTypeName` is a live defect, found in passing.** Its doc at `pkg/interfaces/matter.go:521-525` claims it *"Covers every device type any model package … currently advertises"*. Two emitted types fall to the hex default: **0x0230 Closure** (emitted at `internal/model/custom/cover/matter.go:442`) does not appear anywhere in `pkg/interfaces/matter.go`, and **0x0510 ElectricalSensor** appears only as a return value at `:512`, not in the name table. Meanwhile **0x0043**, which nothing emits, *is* in the table (`:545`). Milestone P1.5.

---

### 0.6 Goal alignment

Measured 2026-09-04 against the three goals stated above. Two were already met by the plan; two gaps were found and closed by D15 and D16.

| Goal | Verdict | Where |
|---|---|---|
| **1. Status quo preserved in loom** | **Met; Phase 0 shipped behaviour-preserving** (see §4.0) — was open for P3.2, now closed | Non-goal 8 already bound Phases 0–2. P3.2 moved 5,796 LOC of production cluster servers on purely structural criteria — a `grep` going empty cannot observe behaviour. **D16** makes a recorded wire comparison the gate and cuts the milestone into P3.2a/b/c, after measurement showed the library servers are stand-ins that return Success without forwarding (R14). |
| **2. Usable unchanged in other projects** — since D18, read as *testable independently of the daemon* | **Met** | P1.6 *is* this goal as a command: `go list -deps ./internal/north/matter/... \| grep openccu-loom \| grep -v internal/north/matter` → empty. Today it returns 18 host packages for `endpoint`, 17 for `eligibility`. It is the go/no-go for Phase 2. |
| **3a. `go-matter-bridge` functionally equal to hamb** | **Deferred (D18)** — the plan would meet it, but it is no longer the target | Phase 3 covers all eleven open domains without a gap: P3.4 (`select`, `input_select`, `alarm_control_panel`), P3.5 (`fan`), P3.6 (`valve`), P3.7 (`media_player`), P3.8 (`vacuum`), P3.3 (`binary_sensor`, `sensor`), P3.2 (`humidifier`, via the host-agnostic LevelControl server), P3.9 / P3.10 (fidelity fill). §3.1 carries the per-domain matrix. |
| **3b. That bridge carries no Homematic domain knowledge** | **Was violated by D4; closed by D15** | Most of the plan points the right way: naming stays host-side (non-goal 10), `eligibility.go` moves host-side (D5), `hmenum.CommandPriority` is dropped (D3), `DataPointUsage` becomes a `VisibilityFilter` (C8). **The exception was `store.EndpointKey`** — `{CentralName, DeviceAddress, ChannelNo, DPKind, DPKey}` across six `Store` methods and two tables, which D4 let travel "byte-for-byte, Homematic-flavoured shape and all". **D15** turns endpoint identity into a port instead. |

Checked and found clean while measuring 3b: `eligibility/vendor_name.go` and `compat.go` carry **CSA vendor-id → controller-name / ecosystem** tables only (Apple 0x1349, Google 0x6006, Amazon 0x1217, …) — Matter ecosystem knowledge, no Homematic content. D5 keeping them library-side is compatible with goal 3.

#### 0.6.1 D18 — goal 3 is deferred; the extraction is kept, for testability

**Decided 2026-09-05.** Goals 1 and 2 follow from loom's own needs. Goal 3 did not, and had never been put to the maintainer. It is now **deferred**: go-fabric is not developed toward matching `home-assistant-matter-bridge`, and no full matter.js port is attempted.

**The extraction itself stands, on a different reason than the one originally written down.** The purpose is that the Matter side can be **tested as independently as possible** — not that a second host exists. That reason is stronger than the one it replaces, because it dissolves two of this document's own high risks instead of routing around them: **R6** (an API designed against a single real caller) and **R8** (a library shipping with no real-commissioner guard) are both answered by the reference daemon, which testability requires anyway. The daemon *is* the second caller.

The rest of this section is the reasoning that led there, kept because the decision should be re-checkable against it.

**The question already has an answer on file, for a narrower version of itself.** `docs/adr/0012-matter-pure-go-implementation.md` (accepted 2026-05-05) weighed exactly this as **Option B — matter.js sidecar**: the daemon spawns and supervises matter.js, talking to it over loopback. It was rejected on three grounds — a Node.js runtime at ~50–100 MB RSS on the CCU add-on path (1 GB RAM, ARMv7/Cortex-A7, `packaging/ccu-addon/`), a third API surface (the sidecar IPC) needing its own versioning and tests, and a second security-patching pipeline. Note what that ADR does *not* claim: it calls the single-static-binary property *"valuable but not load-bearing — a build-output convenience, not a deployment requirement"*. Anyone arguing for Go from the single-binary angle is arguing against our own ADR.

**What has moved since, in both directions.**

* *Toward Go:* Option A's decisive cost — *"no usable Go starting point … realistic effort 12–16 weeks solo"* — is spent. 53,082 non-test LOC exist, pair against real controllers and carry a chip-tool CI leg. That is sunk, and it is not spent again.
* *Toward matter.js:* the coverage gap is now quantified and it is wide. 135 cluster servers against roughly 20 (§0.3); 13 of hamb's 24 domains reachable (§3.1); the schema snapshot 59 commits behind (R5); and P3.2 measured as XL against servers that have never driven a device (R14).

**The argument that actually carries, and it is one.** `CLAUDE.md:64-66` states the product goal: loom serves *"users who want MQTT / REST / UI / **Matter access without HA**"*. Running matter.js inside Home Assistant does not serve that case. Someone bridging their Homematic blinds into Apple Home through loom's CCU add-on has no Home Assistant — otherwise they would use the HA integration. And in the HA case loom's bridge is not needed at all: hamb already exists and is mature.

**The uncomfortable consequence.** The Go implementation is justified by *loom's own use case* — Matter on a CCU, no second runtime, no HA — and **not** by being a hamb replacement. Goal 3 asserts the second. Against hamb, a thin Go stack competes with a mature project that already does the job, and Phase 3 buys at XL cost what has been available there for years.

If that holds, the item in question is not the Go implementation but **the breadth of Phase 3**. Only Phase 3 in its current scope rests on a competition nobody has decided to enter. *(This paragraph originally continued "Phases 0–2 stand either way: they are hygiene." That was true of Phase 1 and false of Phase 2 — the correction is in the decision below, and the sentence is dropped here rather than left to be read as current.)*

**What would flip the answer** — each of these is a real, checkable condition, not a hedge:

| If | Then |
|---|---|
| the CCU add-on path is dropped | the constraint that carried ADR 0012 is gone; a sidecar becomes defensible and goal 3 gets cheap |
| certification ever becomes a goal (D9 reversed) | no path avoids a stack that knows the test suite |
| all 24 domains are genuinely wanted | XL in-house work stands against finished third-party code, and the answer turns |
| a second host materialises that is *not* Home Assistant | goal 3 stops being hamb-shaped and becomes its own thing |

**What D18 settled, concretely:**

* **Phase 1 and Phase 2 proceed.** Not as hygiene alone — an earlier draft of this section claimed "Phases 0–2 stand either way", which was **wrong for Phase 2**: without a second consumer, publishing a module buys a second repository, a version lane and fan-out coordination for one caller. Independent testability is what pays for it.
* **P4.1/P4.2 move to the front.** The reference daemon is not end-of-programme polish; it is the instrument the purpose requires, and `tests/chiptool/` (7,706 LOC) cannot travel without it. It follows Phase 2 immediately — see the note above Phase 4.
* **Phase 3 narrows** to what bites on loom's own devices (§3.1's closing note): `valve`, `binary_sensor`, `media_player`, `select`/`input_select`. The remaining domains are deferred with goal 3.
* **D1 stays as decided in direction, and D19 settles what that means meanwhile.** D1(a) obliges P3.2, which D18 defers; the six stand-in cluster packages therefore travel with the library as a **labelled conformance reference** rather than as servers-in-waiting. What justifies their passage is the matter.js parity coverage they carry, not a future production role — and each must say so in source, with a guard that fires if one ever gains a production caller while P3.2 is still deferred.

---

## 1. Effort scale, and where it comes from

Effort labels are **estimates, not measurements** — no effort figure in this document was read from any source. But the scale is anchored on measured quantities so a reader can check the schedule rather than take it on faith.

| Label | Definition | Anchor measured in this repo |
|---|---|---|
| **S** | ≤ 1 day. A single-file or two-file mechanical edit with no new logic and no signature change. | P0.3 (alias flip) touches exactly two files: `cluster/dataversion.go` is the **only** non-test file in `internal/north/matter/cluster/` importing `pkg/hmtypes`. |
| **M** | 2–5 days. Either a change touching ≤ ~10 files, or **one new cluster server of ≤ ~400 LOC whose shape is already demonstrated in-repo**. | loom's own value-port servers: `cluster/light/colorcontrol_server.go` **246**, `cluster/cover/windowcovering_server.go` **237**, `cluster/lock/doorlock_server.go` **321**, `cluster/closure/closurecontrol_server.go` **392** (`wc -l`). |
| **L** | 2–4 weeks. A change that alters a package's public type surface plus every caller, **or** a cluster server ≥ ~600 LOC, **or** one carrying per-fabric persisted state. | `cluster/thermo/thermostat_server.go` **583**; matter.js `ScenesManagementServer.ts` **1109**, the second-largest server in that tree. P0.2 also lands here on caller count: **104** non-test `MatterWrite`/`MatterInvoke` implementations. |
| **XL** | ≥ 1 month. A redesign spanning ≥ ~5,000 LOC of existing production code, or a new subsystem with no in-repo precedent. | **P3.2 derivation (now P3.2a–c):** the five clusters live in **5,796 non-test LOC across nine files** — `light/matter.go` 1003, `light/matter_color.go` 950, `light/matter_timed_onoff.go` 361, `cover/matter.go` 918, `cover/matter_debounce.go` 249, `climate/matter.go` 905, `switch/matter.go` 415, `siren/matter.go` 617, `generic/switch_matter.go` 378 (`wc -l`, each file re-measured). Three of those files also carry SmokeCOAlarm and ThermostatUI, so 5,796 is an upper bound on the code touched, not a lower bound on the code moved. **And it is not one kind of code:** P3.2b classifies each file as cluster logic (moves), host policy (stays — `cover/matter_debounce.go`), or host binding (becomes a value port). The effort is also not a pure move — the library servers are stand-ins that return Success without forwarding (P3.2a). |

Two scale caveats stated plainly:

* **Ecosystem acceptance is not on this scale.** Whether Alexa / Google / Apple accept ModeSelect, Fan or RVC device types is unmeasured here (§7-Q10) and can dominate any of these estimates.
* **The chip-tool leg is not on this scale either.** `tests/chiptool` (7,706 LOC) execs `./bin/openccu-loom` (`Makefile:222-228`) and cannot travel, so every coverage milestone's real acceptance cost includes a verification path this document cannot price (R8).

---

## 2. Question 1 — the cut line

### 2.1 Measured census

```
$ find internal/north/matter -name '*.go' ! -name '*_test.go' | wc -l
     164
$ find internal/north/matter -name '*.go' ! -name '*_test.go' -exec cat {} + | wc -l
   52061
$ find internal/north/matter -name '*_test.go' | wc -l
     301
```

Per-group non-test LOC, with host coupling measured as `go list -deps ./internal/north/matter/<pkg> | grep openccu-loom | grep -v north/matter`:

| Group | Packages | LOC | Host packages pulled |
|---|---|---|---|
| **A — protocol core** | `tlv` 1121, `im` 4698, `transport` 1532, `secure` 8103, `commissioning` 539, `store` 2176, `schema` 1500, `parity` 27, `conformance` 155, `bootid` 86, `diagevent` 150, `doc.go` 31 | **20,118** | **none** |
| **B — mDNS** | `mdns` | **2,258** | `internal/netutil` only |
| **C — clusters** | `cluster{,/core,/wire,/measurement,/light,/lock,/cover,/thermo,/closure}` | **14,536** | `pkg/{hmenum,hmtypes,interfaces}` (+ `pkg/hmapi` transitively, unused) |
| **D — model-walking** | `bridge` 11,549 · `endpoint` 3,070 · `eligibility` 530 | **15,149** | **bridge 20 · endpoint 18 · eligibility 17** (three distinct closures, re-measured this session — not one shared 20) |

20,118 + 2,258 + 14,536 + 15,149 = 52,061 ✓.

The command prints **nothing** for `tlv`, `im`, `im/subscription`, `transport/{message,mrp,udp}`, `secure/{aesccm,attestation,channel,mattercert,operational,setup,sigma,spake2}`, `commissioning`, `store`, `schema`, `parity`, `conformance`, `bootid`, `diagevent`.

### 2.2 The cut line

> **Library side:** everything under `internal/north/matter/` **except** `bridge/health_probe.go`, `endpoint/assembler.go`, three functions of `endpoint/helpers.go`, three fields of `endpoint/types.go`, and `eligibility/eligibility.go` — plus the port set currently in `pkg/interfaces/matter.go` (617 lines, `wc -l`), and with `bridge/bridge.go`'s two host names replaced by port types.
>
> **Host side:** those four-and-a-bit files, plus everything already outside the subtree.

The subtree root is **not** a valid cut, because the arrow is mutual: `internal/model/custom/{climate,cover,light,lock,siren,switch}/matter*.go` and `internal/model/generic/switch_matter.go` import `internal/north/matter/cluster{,/wire,/lock,/closure}` and `internal/north/matter/im`, while `endpoint` and `bridge` import `internal/model/{device,generic,naming}`. Not a Go compile cycle (different packages), but a single cut at the root would require the library to import the host.

### 2.3 Per-file narrowing — the measurement that flips the feasibility verdict

The package-level closure overstates the residue by two orders of magnitude. Reproduced verbatim this session:

```
$ for f in internal/north/matter/bridge/*.go; do case $f in *_test.go) continue;; esac;
    grep -q 'openccu-loom/internal/\(model\|health\|i18n\|netutil\)' "$f" && echo "$f"; done
internal/north/matter/bridge/bridge.go
internal/north/matter/bridge/health_probe.go
```

* **`bridge/`** — 21 non-test files, 11,549 LOC; only **two** name a host package. `bridge.go` (1,536 LOC): `device.ParameterTranslator` at `:132`; `generic.MatterSwitchEventEmitter` at `:721`, `:860`; `var _ matterSwitchSubscribable = (*generic.ButtonGroup)(nil)` at `:867`. `health_probe.go` (102 LOC): `health.Sample` at `:38,:86,:94,:98`. `receive.go`, `subscribe.go`, `securechannel.go`, `reply.go`, `im_gate.go`, `ackpump.go`, `outbound_reliable.go` import only `pkg/interfaces`. **19 of the 21 files — 9,911 of 11,549 LOC — move untouched**; `health_probe.go` (102 LOC) goes host-side and `bridge.go` (1,536 LOC) moves once its three host names become port types (C7). Re-measured in the main conversation: `wc -l` over the two coupled files returns 1,638, so 11,549 − 1,638 = 9,911. An earlier draft of this line said "11,447 … move untouched", which subtracted only `health_probe.go` and so counted `bridge.go` as untouched although C7 edits it.
* **`endpoint/`** — the same scan returns `assembler.go`, `helpers.go`, `types.go`. Per-file `wc -l`, re-measured: `assembler.go` **954**, `dispatcher.go` **1021**, `materialize.go` **410**, `types.go` **429**, `helpers.go` **166**, `reportable_paths.go` **49**, `doc.go` **41**. `dispatcher.go`, `materialize.go`, `reportable_paths.go` and `doc.go` import **no** model package.
* **`eligibility/`** — `eligibility.go` **333** LOC with **5** `openccu-loom` imports; `compat.go` **151** and `vendor_name.go` **46** with **0** each (`grep -c 'openccu-loom'` per file). The latter two are ecosystem knowledge that belongs *in* the library.

**Correction to the naive version of that claim:** `helpers.go` cannot move as a file. `materialize.go:258` and `:263` call `deviceTypeRevision` (`helpers.go:142` → `customDeviceTypeRevision` `helpers.go:160` → `schema.DeviceTypeRevision`). The split is **by function**: `friendlyName` (`:50`), `parameterSuffix` (`:81`) and `isNotFound` (`:20`) go host-side; `truncateUTF8` (`:96`), `measurementDeviceType` (`:114`), `deviceTypeRevision` (`:142`), `customDeviceTypeRevision` (`:160`) stay library-side. Library residue in `endpoint/` is therefore ≈1,950 LOC, not 1,521.

### 2.4 The residue that stays host-side

| File | LOC | Why |
|---|---|---|
| `internal/north/matter/endpoint/assembler.go` | 954 | walks `*device.Device`, constructs `generic.ButtonGroup` / `generic.ElectricalGroup` |
| `internal/north/matter/eligibility/eligibility.go` | 333 | CCU allowlist policy filed under a Matter name |
| `internal/north/matter/endpoint/helpers.go` (3 funcs) | ~90 | naming authority (`naming.EntityDisplayName`, `device.TranslatedParameterLabel`) |
| `internal/north/matter/bridge/health_probe.go` | 102 | one `health.Sample` literal |

Already host-side and unaffected: `internal/model/**/matter*.go` — **6,109** non-test LOC (`find internal/model -name 'matter*.go' ! -name '*_test.go' -exec wc -l {} +` → 6109 total) plus `generic/switch_matter.go` **378**; `cmd/openccu-loom/*matter*.go` (5,161); `internal/north/rest/handlers/matter.go` 590 + `matter_exposures.go` 450.

**Total production residue ≈ 14,167 LOC** (1,479 + 6,487 + 5,161 + 1,040; the `helpers.go` three-function split at ~90 LOC is the one approximate component) — **roughly 21 % of the combined Matter surface.**

Non-Go residue that does not move: `config.NorthMatter` (`internal/config/config.go:815`, sub-structs at `:1006`, `:1027`, `:1142`), `validateMatter` (`internal/config/validate.go:145`), `matterRestartRules` (`internal/config/restart.go:215-347`), 46 `config.field.north.matter.*` + 46 `config.help.north.matter.*` keys in `assets/ui/src/lib/i18n.ts`, the 11 `matter_*` goose migrations, and `tests/chiptool/` (7,706 LOC, `//go:build chiptool`).

### 2.5 Coupling points that must be resolved first

Kind: **T** trivial · **M** mechanical · **D** design decision · **B** blocker.

| # | Coupling | Where | Kind | Resolution |
|---|---|---|---|---|
| C1 | Matter ports share a package with REST ports | `pkg/interfaces/matter.go` (617 lines) beside `pkg/interfaces/rest_ports.go` (350, imports `pkg/hmapi`) | M | Move the Matter block to a **second neutral `pkg/` package**, never into `internal/north/matter` — see §0.5-2. Measurable win is narrow hygiene, not binary size: `pkg/hmapi` carries no non-stdlib and no intra-repo dependency, and no build-time or binary-size cost was measured. |
| C2 | `hmenum.CommandPriority` in the cluster contract | `pkg/interfaces/matter.go:59` (`MatterWrite`), `:65` (`MatterInvoke`) — the file's only host import (`:10`) | M | The library never reads it. Constructed at exactly two hard-coded sites: `endpoint/dispatcher.go:319`, `:595` (`hmenum.CommandPriorityHigh`). Forwarded unread at 9 named sites (`cluster/closure/closurecontrol_server.go:48,51,192,209,243`; `cluster/lock/doorlock_server.go:88,200`; `cluster/light/colorcontrol_server.go:61,149`). **104** non-test implementations to touch. Options: drop the parameter and carry urgency in `ctx`, or a library-owned `Priority` with an **explicit** host mapping switch — never ordinal equality (`hmenum.CommandPriorityCritical = 0`, `pkg/hmenum/command.go:14`). |
| C3 | `DataVersionTracker` alias points host→library | `internal/north/matter/cluster/dataversion.go:37` (`type DataVersionTracker = hmtypes.DataVersionTracker`), concrete type at `pkg/hmtypes/dataversion.go:53` | T | Flip the alias. `dataversion.go` is the **only** non-test file in `internal/north/matter/cluster/` importing `pkg/hmtypes`, and only for the alias — a two-file edit leaving no residual cycle. Nine model files embed the `hmtypes` name and stay untouched: `siren/sound.go:83`, `siren/smoke.go:44`, `siren/siren.go:51`, `lock/lock.go:113`, `switch/switch.go:70`, `light/light.go:77`, `climate/climate.go:185`, `cover/cover.go:150`, `cover/garage.go:78`. Must be one atomic commit. |
| C4 | `internal/netutil` in mDNS | `mdns/zeroconf.go:18`, `:189` (`netutil.IsVirtualInterfaceName`) | T | `internal/netutil/interfaces.go` is 50 lines importing only `"strings"`. Accept an `InterfaceFilter func(string) bool` on the advertiser config, defaulted to a vendored copy. **Named risk:** loom's own client-discovery advertiser uses the same predicate — a vendored copy can drift silently, so a shared-behaviour test is the honest guard. |
| C5 | `internal/health.Sample` | `bridge/health_probe.go:38,86,94,98`; the file already declares local `Status` (`:27`) and `HealthRecorder` (`:37`) | M | Library-local `{Healthy bool; Note string}`; the host adapter translates. The probe never reads `NoteKey`/`Timestamp`/`Sticky`. |
| C6 | `internal/i18n` inside the assembler | `endpoint/assembler.go:111` (field), `:142` (`i18n.NewCatalogs()`) → `translations.T(cfg.Locale, "channel.title")` | M | Produces exactly one string ("Channel"/"Kanal"). Replace with `ChannelLabel string` on `endpoint.Config`. Moves out with the assembler anyway. |
| C7 | `MatterSwitchEventEmitter` named in a bridge-local interface | `bridge/bridge.go:849-861`, `:867`; type at `internal/model/generic/button.go:78` | M | The emitter must live in the shared port package. `bridge.go:847-858` records the failure mode in its own comment: a bridge-local emitter interface here "can never be satisfied by the model types' method set" — the assertion silently never matches and no press event reaches a commissioner. **Correctness, not tidiness.** |
| C8 | Visibility gating reads CCU entity-creation semantics | `endpoint/assembler.go:747-763` (`hideFromMatter` on `hmenum.DataPointUsage{Ignored,NoCreate,CDPSecondary,CDPState}`), duplicated at `eligibility/eligibility.go:183-199` | M | Half the seam exists — both already read through an anonymous `interface{ Usage() … }` assertion. Replace with a host-supplied `VisibilityFilter func(any) bool`. |
| C9 | Naming is delegated to the host's authority from inside the library | `endpoint/helpers.go:50` (`friendlyName` → `ch.NameData().TranslatedFullName()`), `:81` (`parameterSuffix` → `device.TranslatedParameterLabel` → `naming.EntityDisplayName`); `bridge.Config.Labels` at `bridge/bridge.go:132` | D | `helpers.go:24-38` records the intent: "Matter must not re-derive that rule, or the two planes drift apart." A `NameResolver` port with two methods, implemented host-side. Getting it wrong is a user-visible drift bug, not a compile error. |
| C10 | The assembler **constructs** host aggregates | `endpoint/assembler.go:626` (`generic.NewElectricalGroup`), `:692` (`generic.NewButtonGroup`) | D | The consolidation rule is Matter-driven (ADR 0049, one endpoint per physical device) but its product is a host type. Either an aggregate-factory port, or consolidation moves into the model and the assembler consumes the result. |
| C11 | **The assembler's input type IS the CCU device tree** | `endpoint/types.go:28` `Devices []*device.Device`, `:88` `BridgedDevice *device.Device`, `:93` `Channel *device.Channel` | **B** | Severing needs **three** field removals, not two. `BridgedDevice` is read at `materialize.go:145` (`.Address`), `:194` (`.Available()`) — and a **third** production reader outside the package, `cmd/openccu-loom/daemon_matter.go:4057` (`info.DeviceAddress = ep.BridgedDevice.Address`). `Channel` is read only by `assembler.go:324,327`. Replacement: a flat `EndpointSpec{StableKey, FriendlyName, Reachable, DeviceType, Source, Parent}`. **The single largest blocker for a second host.** |
| C12 | `eligibility/` is host allowlist policy | `eligibility.go:146` `CollectCandidates(centralName string, devices []*device.Device, …)`, `:184-196`, `:294`; verdict prose prescribes ⛔/⚠ glyphs and `/api/v1/matter/exposable` at `pkg/interfaces/matter.go:394-457` | D | `Classify` / `DeriveMatterEligibility` are host-neutral and stay; `CollectCandidates` moves host-side. `compat.go` + `vendor_name.go` (197 LOC, zero host imports) stay library. |
| C13 | **Persistence: schema coupling without Go coupling** | `store/store.go:21` `New(db *sql.DB)` — zero host imports; DDL in 11 goose files `internal/store/sqlite/migrations/{006,007,008,009,010,011,012,013,018,025,036}_matter_*.sql` | D | **None uses `IF NOT EXISTS`.** Reproduced: `grep -c 'IF NOT EXISTS' internal/store/sqlite/migrations/*matter*.sql` returns **0 for all eleven files**, listed individually. A library set numbered from 1 therefore fails on every existing install. See §5.3 for the hard rule. |
| C14 | Endpoint identity is a Homematic 5-tuple pinned by a SQL CHECK | `store/endpoints.go:18-37` `EndpointKey{CentralName, DeviceAddress, ChannelNo, DPKind, DPKey}`; `migrations/007_matter_endpoints.sql:15-26`; six `Store` methods across `endpoints.go` and `exposures.go` | D | **The encoding must not change.** Migration 007's own Down comment records the consequence: reassignment "desyncs every commissioned controller's cached accessory list until each bridged device is removed and re-added." **Resolved by D15:** the key stays host-side and the library holds an opaque `Stringer`, so the encoding is preserved *and* no host vocabulary enters the module API (goal 3). The seam already exists on the rendering side — `materialize.go` `uniqueIDFor` takes `key any` with a `Stringer` fallback (`:357`); D15 extends it to the persistence side, which is still typed. |
| C15 | `FabricScopedReader`'s precondition is unreachable from outside | `pkg/interfaces/matter.go:72-95` cites `im.WithFabricFilter` / `im.FabricFilterFromContext`, defined at `im/fabric_context.go:29,:37` | M | Public interface, non-public precondition. Either export the accessors or widen the signature to `MatterReadFiltered(ctx, attrID, filtered bool, fabricIndex uint8)`. |
| C16 | Everything is under `internal/` | all 164 non-test files | **B** | Go's internal rule makes the whole 52,061-LOC subtree unimportable outside `github.com/SukramJ/openccu-loom`. This is why no API-stability work has an observable consumer today. |

### 2.6 What is already right (needs no work)

* **Logging** is `log/slog` only and injected. `grep -rc hmlog --include='*.go' internal/north/matter/` → no matches; 20 non-test files import `log/slog`; constructors take a `*slog.Logger` with a `nil → slog.Default()` fallback (`endpoint/assembler.go:126-140`). Residual gap: 23 direct `slog.Default()` call sites in 11 files, concentrated in `cluster/core` (which takes no logger at all).
* **Ports already exist** for `mdns.Advertiser` (with a `Noop`), `endpoint.Store`, `ExposureChecker`, `ACLLister`, `bridge.Snapshotter`, `cluster/core.StoreFacade` (`operational_credentials.go:200`), `GroupStoreFacade`, `ACLStoreFacade`, `secure/operational.ResumptionStore` (`manager.go:205`).
* **The target API shape is already demonstrated** twice, by the only two `cluster/` packages with production callers: `cluster/lock/doorlock_server.go:85-88` (`StateSource{IsJammed, IsLocked, LockInvoke}`) and `cluster/light/colorcontrol_server.go:60-61` (`ColorTemperatureWriter{SetColorTemperatureMireds}`) — library owns the cluster server, host implements a 1–3 method value port.
* **Godoc is near-complete — UNVERIFIED.** The claim that every one of the 37 packages containing a `.go` file carries a package doc comment, and that an awk sweep over the 164 non-test files counted 1,364 documented vs 51 undocumented exported declarations, is inherited from the earlier survey. The sweep was not re-run in this session and §9 carries no command for it, so both figures are **UNVERIFIED**.
* **Dependencies are five modules, all permissive:** `filippo.io/nistec` v0.0.4 (BSD-3, `secure/spake2` only), `github.com/grandcat/zeroconf` v1.0.0 (**MIT** — `docs/adr/0012:473` wrongly says Apache-2.0), `github.com/miekg/dns` v1.1.73 (BSD-3), `golang.org/x/net` v0.58.0 (BSD-3), `go.uber.org/goleak` v1.3.0 (MIT, test-only). No copyleft. Crypto is stdlib only (`crypto/hkdf`, `crypto/pbkdf2`); `golang.org/x/crypto` appears **zero** times, diverging from ADR 0012's plan at `:470`.
* The three network dependencies are confined to `mdns/{zeroconf.go,subtype_responder.go}`, so mDNS can ship as an optional second module later.

---

## 3. Where loom stands against the target

### 3.1 HA domain → Matter device type coverage matrix

Baseline: hamb's single registry table, `../home-assistant-matter-bridge/packages/backend/src/matter/endpoints/legacy/create-legacy-endpoint-type.ts:76-104` (24 entries, read verbatim). Matter IDs and revisions from `../matter.js/packages/node/src/devices/*.ts`. loom's emit list from the six production sites that assign `Endpoint.DeviceType` (`assembler.go:185,190,644,710,799,843`) plus the 13 `MatterDeviceType()` implementations (§3.2).

Status: ✅ loom emits it today · ⚠ partial / mapping work only · ❌ absent.

| HA domain | hamb Matter device type | loom today | Missing to reach it | Status |
|---|---|---|---|---|
| `light` | 0x0100 / 0x0101 / 0x010C / 0x010D | 0x0100 / 0x0101 `light/matter.go:158` (branches at `:159-162`), 0x010C `matter_color.go:295`, 0x010D `:309,323` | Groups/Scenes commands (§3.3) | ✅ |
| `switch` | 0x010A OnOffPlugInUnit | `custom/switch/matter.go:93` | — | ✅ |
| `input_boolean` | 0x010A | same server | mapping only | ✅ |
| `automation` | 0x010A | same | mapping only | ✅ |
| `button` | 0x010A | same | mapping only | ✅ |
| `input_button` | 0x010A | same | mapping only | ✅ |
| `scene` | 0x010A | same | mapping only | ✅ |
| `script` | 0x010A | same | mapping only | ✅ |
| `remote` | 0x010A | same | mapping only | ✅ |
| `lock` | 0x000A DoorLock | `custom/lock/matter.go:32`, server `cluster/lock/doorlock_server.go` | — | ✅ |
| `cover` | 0x0202 WindowCovering | `custom/cover/matter.go:415,425` | — | ✅ |
| `climate` | 0x0301 Thermostat | `custom/climate/matter.go:240` | — | ✅ |
| `event` | 0x000F GenericSwitch | `assembler.go:703` (per-channel button group only) | — | ✅ |
| `siren` *(HA platform; **not** in hamb's registry)* | — (unmapped by hamb) | 0x0076 SmokeCOAlarm (`internal/model/custom/siren/matter.go:423`, constant at `:80`), 0x010A OnOffPlugInUnit (`:177`); SmokeCOAlarm 0x005C server at `:438` | — (loom is *ahead* of hamb here) | ✅ |
| `humidifier` | 0x010A + optional LevelControl | 0x010A yes; LevelControl server is `lightLevelServer` bound to `*Light` (`custom/light/matter.go:460`) | host-agnostic LevelControl server | ⚠ |
| `media_player` | **Speaker 0x0022** — mandatory **OnOff + LevelControl**, per `schema/devicetypes.go:299-303` and `../matter.js/packages/node/src/devices/speaker.ts:64,76-79` (`deviceType: 0x22, deviceRevision: 1`) | both servers exist but host-bound; `light.SoundPlayerLED` already reaches Matter as DimmableLight 0x0101 with exactly that cluster set (`light/matter_color.go:22-24`, `light/matter.go:158-181`); no 0x0022 emitted | **device-type relabel plus MediaInput 0x0507** — hamb mounts MediaInput conditionally (`…/media-player/index.ts:53-55`); 0x0506 MediaPlayback belongs to CastingVideoPlayer 0x0023 (`devicetypes.go:305`) and hamb uses it nowhere in its backend | ⚠ |
| `water_heater` | 0x0301 Thermostat, Heating-only (`…/water-heater/index.ts:47-55`) | 0x0301 exists | mapping only; native 0x050F is separate | ⚠ |
| `binary_sensor` | **12 device classes → 5 device types** (`…/binary-sensor/index.ts:23-39`) + **OnOffSensor 0x0850** default (`:44`) | OccupancySensor 0x0107 (`pkg/interfaces/matter.go:482`), ContactSensor 0x0015 (`:502`), **SmokeCOAlarm 0x0076** (`internal/model/custom/siren/matter.go:423`) — **9 of hamb's 12 classes** | 0x0850 fallback; Moisture/Cold/Safety diverge by design (0x0015 instead of 0x0043/0x0041); classifier is closed (`internal/model/generic/matter.go:97-117` defaults to `MatterMeasurementNone`, dropped at `assembler.go:417` and `:524`) | ⚠ |
| `sensor` | **9 device types over 10 device classes** (`…/sensor/index.ts:24-53`; `pressure` and `atmospheric_pressure` both route to `PressureSensorType`), incl. Power / Energy on 0x010A (`…/sensor/devices/power-sensor.ts:23-29`, `energy-sensor.ts:23-29`) | 7 measurement classes: 0x0302, 0x0307, 0x0106, 0x0305, 0x002C, 0x0107, 0x0015 (`pkg/interfaces/matter.go:472-502`), **plus Power 0x0090 / Energy 0x0091 on ElectricalSensor 0x0510** (`:505-512`, `endpoint/assembler.go:626`) | flow / rain / soil / remaining analytes; enum closed at 16 values (`:238-255`). **Power/Energy are not missing — the divergence is the carrier device type (0x0510 vs hamb's 0x010A), a D8 compat question, not a gap.** | ⚠ |
| `alarm_control_panel` | ModeSelect 0x0027 | — | 0x0027 + cluster 0x0050 | ❌ |
| `select` | ModeSelect 0x0027 | — | same | ❌ |
| `input_select` | ModeSelect 0x0027 | — | same | ❌ |
| `fan` | Fan 0x002B | — | 0x002B + FanControl **cluster** 0x0202 | ❌ |
| `valve` | WaterValve 0x0042 | projects as 0x010A via `*generic.Switch` — **intentional**, `docs/adr/0049…:47-50` supersedes ADR 0012's two "MQTT-only" rows | 0x0042 + ValveConfigurationAndControl 0x0081 | ❌ |
| `vacuum` | RoboticVacuumCleaner 0x0074 | — | 0x0074 + RvcRunMode 0x0054 + RvcOperationalState 0x0061 (+ OperationalState 0x0060) | ❌ |

**Score against hamb's 24: 13 fully reachable, 5 partial, 6 absent. Against HA's 45 entity platforms the denominator is different** — hamb's 24 registry entries cover **19** platforms (the other five, `input_boolean`, `input_button`, `input_select`, `automation`, `script`, are helper integrations absent from `EntityPlatforms`). Of those 19, loom fully reaches **9** (`light`, `switch`, `lock`, `cover`, `climate`, `event`, `button`, `scene`, `remote`), partially reaches **5** (`binary_sensor`, `sensor`, `humidifier`, `media_player`, `water_heater`), and additionally serves **`siren`**, which hamb does not map at all.

One note on provenance: these are a *target-surface* count, not a loom fleet count. `grep -o 'hmenum.DeviceProfile("[A-Za-z_]*")' internal/model/custom/profiles.go | sort -u | wc -l` → **31** device profiles, and none is fan- or vacuum-shaped, so `fan` and `vacuum` bite only for a second host. **Four rows bite on loom's own devices today: `valve` (`IPIrrigationValve`), `binary_sensor` (the unclassified-DP drop), `media_player` (`IPSoundPlayer` / `IPSoundPlayerLed` — §3.7 and D8), and `select` / `input_select` (read/write ENUM data points have no carrier — P3.4).**

**Deliberate non-emissions, for the record.** Battery/Power/Energy classes return device type 0 (`pkg/interfaces/matter.go:518`) — PowerSource rides as cluster 0x002F on a host endpoint. WaterLeakDetector 0x0043 is never emitted; leak sources return ContactSensor 0x0015, with the rationale in source at `pkg/interfaces/matter.go:483-502` (a single 0x0043 endpoint renders the whole bridged node unresponsive on Amazon Alexa) pointing at `notes/parity/by_design.md`.

### 3.2 The device-type ceiling is not the schema table

`internal/north/matter/schema/devicetypes.go` already carries **273** device types with revisions and mandatory-cluster lists (`grep -c "^\t0x"` → 273), including every gap type: 0x0027 ModeSelect (`:30`, `:332`), 0x002B Fan (`:34`, `:357`), 0x0042 WaterValve (`:39`, `:401`), 0x0074 RVC (`:47`, `:463`), 0x0850 OnOffSensor (`:104`, `:804`), 0x050F WaterHeater (`:97`, `:764`), 0x010B DimmablePlugInUnit (`:66`, `:585`).

What is missing is **cluster servers**. The 13 device-type producers, verbatim (`grep -rn "func ([^)]*) MatterDeviceType() uint16" --include='*.go' internal/model/ | grep -v _test`):

```
internal/model/generic/switch_matter.go:126      internal/model/custom/light/matter_color.go:295/309/323
internal/model/custom/siren/matter.go:177/423    internal/model/custom/light/matter.go:158
internal/model/custom/lock/matter.go:32          internal/model/custom/climate/matter.go:240
internal/model/custom/switch/matter.go:93        internal/model/custom/cover/matter.go:415/425/442
```

Plus 7 measurement-derived types via `interfaces.MatterMeasurementClassDeviceType` (`pkg/interfaces/matter.go:469`) and 2 hard-coded group classes (`assembler.go:637` ElectricalSensor 0x0510, `:703` GenericSwitch 0x000F).

**Second ceiling:** the measurement path is a closed 16-value enum (`pkg/interfaces/matter.go:238-255`) fed by a closed `hmenum.Parameter` switch (`internal/model/generic/matter.go:40-116`). It is a *cluster selector*, not a quantity vocabulary — Contact and Leak are two constants mapping to one cluster (0x0045), Electrical is three, and `FromMeasurementClass` refuses to build for Power/Energy (`cluster/measurement/measurement.go:955-959`). A sensor kind with no constant there cannot reach an endpoint at all, and adding one requires editing `pkg/interfaces` **and** `internal/model/generic` **and** `internal/north/matter/cluster/measurement`: three packages in two layers. A host with its own entity model cannot add one at all.

**No extension registry exists.** `grep -rn '^func Register\|func (r \*Registry)' --include='*.go' internal/north/matter/ | grep -v _test` → no matches. Mapping is discovery-by-type-assertion in the assembler (`assembler.go:377-391`, `:462-564`). The only registry in the stack is `internal/model/custom/materialize.go:44`, keyed by `hmenum.DeviceProfile` — a CCU-profile registry, not a Matter-mapping registry.

### 3.3 Cluster coverage

**Read the two columns as of different commits.** matter.js server line counts come from HEAD `75633fa` (2026-09-03); loom's mandatory-attribute verdict comes from the embedded snapshot at `sourceCommit c6b188fe` (2026-08-26), **59 commits earlier** (`git -C ../matter.js rev-list --count c6b188fe00bc7d1b97fe22c63cd3a553ad8efd8f..HEAD` → 59). A cluster added or changed in those 59 commits shows as "matter.js ships it, loom is clean" in both columns and is invisible here.

matter.js HEAD ships 135 cluster `*Server` behaviors, 53 hand-written and 82 generated stubs (partitioned by the literal marker `/*** THIS FILE WILL BE REGENERATED IF YOU DO NOT REMOVE THIS MESSAGE ***/`). A generated stub is **not** an empty capability: attributes are served from schema and commands are installed as `Behavior.unimplemented` (`../matter.js/packages/node/src/behavior/Behavior.ts:416-418`), so the 31 stubs covering command-free clusters are complete servers.

loom's server population, measured: **51** non-test `MatterClusterID()` implementations, **14** of them inside `internal/model/`. The "38 of 120 bridge-reachable, 82 absent" figure is the coverage survey's cross-join and was **not re-run here — UNVERIFIED by me** (§0.3).

| Cluster | ID | loom | matter.js | Blocks (HA domains) | Effort |
|---|---|---|---|---|---|
| OnOff | 0x0006 | ✅ but host-bound (`custom/light/matter.go:215`, `custom/switch/matter.go:106`, `custom/siren/matter.go:235`, `generic/switch_matter.go:151`) | hand-written, 345 lines | 11 domains, for a second host | M |
| LevelControl | 0x0008 | ✅ host-bound (`custom/light/matter.go:460`) | hand-written, 875 | light, humidifier, media_player | M |
| ColorControl | 0x0300 | ✅ host-bound ×3 (`matter_color.go:364,487,629`); generic `cluster/light` server (246 LOC) has **zero** production callers | hand-written, 2046 | light | M |
| WindowCovering | 0x0102 | ✅ host-bound (`custom/cover/matter.go:453,619`); generic `cluster/cover` (237 LOC) unused | hand-written, 654 | cover | M |
| Thermostat | 0x0201 | ✅ host-bound (`custom/climate/matter.go:421`); generic `cluster/thermo` (583 LOC) unused | hand-written, 1752 | climate, water_heater | M |
| ThermostatUI | 0x0204 | ✅ (`custom/climate/matter.go:793`) | hand-written, 32 | climate | — |
| DoorLock | 0x0101 | ✅ **library-shaped** (`cluster/lock/doorlock_server.go`, 321 LOC) | hand-written, 1210 | lock | — |
| ClosureControl | 0x0104 | ✅ **library-shaped** (`cluster/closure/`, 392 LOC) | generated stub | cover (garage) | — |
| SmokeCOAlarm | 0x005C | ✅ (`custom/siren/matter.go:438`) | hand-written, 45 | binary_sensor, siren | — |
| Switch | 0x003B | ✅ **library-shaped** (`cluster/wire/genericswitch.go`, constant at `:49`, `MatterClusterID` at `:141`, `MatterAttributes` at `:217`) | hand-written | `event` (GenericSwitch 0x000F, mandatory M per `schema/devicetypes.go:230-234`) | — |
| Identify | 0x0003 | ✅ mounted per bridged endpoint (`endpoint/materialize.go:270-292`, with the Apple `HAPErrorDomain` rationale in source at `:273`) | hand-written | mandatory M on every application device type loom emits | — |
| **Groups** | **0x0004** | ⚠ **stub** — `cluster/wire/groups.go:78-84` rejects every `cmdID`; no `MatterAcceptedCommands`, so `AcceptedCommandList` = `[]` via `endpoint/dispatcher.go:561-565` | hand-written, **232** (`wc -l`) | **conformance-M on every OnOff-family endpoint loom builds** | M |
| **ScenesManagement** | **0x0062** | ⚠ **stub** — `cluster/wire/scenes_management.go:79-81`; same empty `AcceptedCommandList` via `endpoint/dispatcher.go:561-565`, `SceneTableSize` = 0 | hand-written, **1109** (`wc -l`) | same 11 domains | L |
| Measurement family | 0x0402/0405/0400/0403/0045/0406/005B/040D/042A/042D/002F/0090/0091 | ✅ 13 IDs, `cluster/measurement/measurement.go:32-46` (read verbatim) | stubs (command-free ⇒ complete) | sensor | — |
| FlowMeasurement | 0x0404 | ❌ | stub | sensor (`volume_flow_rate`) | S |
| SoilMeasurement | 0x0430 | ❌ (revision already at `schema/clusters.go:118`) | stub | sensor | S |
| Concentration ×7 | 0x040C/0413/0415/042B/042C/042E/042F | ❌ (only CO₂/PM2.5/PM10 present) | stubs | sensor (air quality) | S |
| BooleanStateConfiguration | 0x0080 | ❌ | stub | binary_sensor fidelity | S |
| **ModeSelect** | **0x0050** | ❌ | hand-written, **112** (`wc -l`) | **select, input_select, alarm_control_panel** | M |
| **FanControl** | **0x0202** | ❌ (the `0x0202` hits in `custom/cover/matter.go:21,99,412` are the WindowCovering *device type*, an unrelated numeric collision) | hand-written, **19** (`wc -l`) | **fan** | M |
| **ValveConfigurationAndControl** | **0x0081** | ❌ (schema metadata only: `schema/clusters.go:74,213`, `devicetypes.go:404`) | generated stub | **valve** | M |
| RvcRunMode / RvcOperationalState / RvcCleanMode / ServiceArea | 0x0054/0061/0055/0150 | ❌ | **78 / 98** (`wc -l`) / 60 / 298 | **vacuum**, lawn_mower | L |
| OperationalState | 0x0060 | ❌ | hand-written, 89 | vacuum, appliances | M |
| WaterHeaterManagement / WaterHeaterMode | 0x0094 / 0x009E | ❌ | stub / 70 | water_heater (fidelity only) | M |
| TemperatureControl | 0x0056 | ❌ | stub | appliance cabinets | S |
| **MediaInput** | **0x0507** (`schema/clusters.go:123,262`; `../matter.js/packages/model/src/standard/elements/media-input.element.ts:19` `id: 0x507`) | ❌ | **generated stub, 14 lines** (`wc -l ../matter.js/packages/node/src/behaviors/media-input/MediaInputServer.ts`) | **media_player source selection — the one media cluster hamb mounts** | S |
| Media family | 0x0504–0x0506, 0x0508–0x050F | ❌ | mixed | media_player (beyond Speaker) | XL |
| Actions (on Aggregator) | 0x0025 | ❌ — aggregator mounts only Identify + Descriptor (`cmd/openccu-loom/daemon_matter.go:2412-2431`) | stub | none | M |
| Binding | 0x001E | ⚪ present, unmounted — `core.NewBinding` has no production caller | hand-written, 192 | none (implies the client role loom lacks) | — |
| ICDManagement | 0x0046 | ⚪ present, deliberately unmounted (`daemon_matter.go:1982-1990`) | hand-written, 838 | none | — |
| OTASoftwareUpdateRequestor | 0x002A | ⚪ present, deliberately unmounted (`daemon_matter.go:1959-1966`) | hand-written, 1291 | none (depends on BDX) | — |

**Mandatory attributes: clean.** For every cluster loom implements, `MatterAttributes()` covers every `conformance: "M" | "P, M"` attribute in the pinned snapshot; the 15 core clusters are additionally pinned case-by-case at `cluster/core/parity_matterjs_test.go:118-300` with `Descriptor.TagList` (0x04) as the only reasoned skip.

**Mandatory commands: a measurement that was NOT performable from the stated source.** `internal/north/matter/schema/clusters.go` carries only `ClusterRevisions` and `ClusterNames` — no command table — so the question cannot be answered from that file. Going to the JSON snapshot instead exposes a **lossy extractor**. Reproduced verbatim at `notes/parity/matter/extract-from-matter-js.ts:128`:

```ts
const keyOf = (ch: any) => `${ch.tag}:${typeof ch.id === "number" ? ch.id : ch.name}`;
const merged = new Map<string, any>();
for (const ch of base) merged.set(keyOf(ch), ch);
for (const ch of (node.children ?? [])) merged.set(keyOf(ch), ch); // own wins
```

A request command and a response command sharing an ID therefore collapse (last wins → the response). Measured effect: Groups loses 4 of 6 mandatory requests, ScenesManagement 6 of 7, CommodityTariff 2; and on 14 device types a `serverCluster` requirement is overwritten by the `clientCluster` one — including RootNode, where TimeSynchronization 0x0038 is dropped from `schema/devicetypes.go:261-283` even though `../matter.js/packages/model/src/standard/elements/root-node.element.ts:48-53` declares it as a serverCluster requirement.

**The punchline: the two clusters that lose their commands in the snapshot are exactly the two loom ships as no-command stubs, so a snapshot-driven mandatory-command guard would report them green.** This is why P0.7 is a hard prerequisite for P3.1's conformance guard, and why every mandatory-command claim in this document was measured directly against matter.js element files instead.

**Five further servers advertise `AcceptedCommandList = []` while handling real commands** (`endpoint/dispatcher.go:561-570` returns `[]uint32{}` absent a `MatterClusterCommandLister`): `cluster/cover/windowcovering_server.go:152-180`, `cluster/thermo/thermostat_server.go:367-372`, `cluster/closure/closurecontrol_server.go:194-204`, `cluster/core/time_synchronization.go:105-114` (SetUtcTime 0x00 is conformance M), plus Groups and ScenesManagement. Apple's HAP rebuild reads `AcceptedCommandList`.

**Two constant defects, neither with wire impact today:**

* `cluster/core/access_restriction.go:28` declares `ARLClusterID uint32 = 0x002B`. loom's own generated table says 0x002B is LocalizationConfiguration — **`schema/clusters.go:25`**, verified by reading lines 20–30 (`0x001F` at :20 … `0x002B: 1, // LocalizationConfiguration` at :25). matter.js HEAD has no AccessRestriction cluster — it is `ReviewFabricRestrictions`, command 0x00 on AccessControl 0x001F behind feature `MNGD` (`../matter.js/packages/model/src/standard/elements/access-control.element.ts:26,121-131`). No `MatterClusterID` method, no caller.
* `cluster/wire/schedules.go:17` declares `SchedulesClusterID = 0x0024`, which does not exist in matter.js HEAD (0x0024 is device type ContentApp). Already guarded by `cluster/wire/parity_matterjs_test.go:97-105`.

### 3.4 Protocol completeness — calibrating the yardstick in both directions

**At or above parity with matter.js:** TLV (all 25 element types `tlv/tlv.go:13-38`, all 8 tag forms `:45-53`), UDP/IPv6 + MRP with a counter that returns `ErrCounterExhausted` rather than reusing a nonce (`transport/mrp/counter.go:13-19`), PASE/SPAKE2+, **CASE with Sigma2Resume persisted and wired in production** (`cmd/openccu-loom/daemon_matter.go:897,981,1234`), all 10 IM opcodes with batch invoke and timed-request gating, ReportData chunking for attributes *and* events, **ACL enforcement ported from chip with CATs and device-type targets, fail-closed by default** (`endpoint/dispatcher.go:791-980`, `:62-68`), multi-fabric with UpdateNOC/RemoveFabric and per-fabric CASE identity restore at boot, AdministratorCommissioning wired.

**Absent — loom is a commissionee-only, UDP-only, unicast-only node:** **controller role** (`sigma.NewInitiator`, `spake2.NewProver` have zero non-test callers), **TCP** (`transport/` holds only `message`, `mrp`, `udp`; datagrams capped at `udp/listener.go:33` `MaxDatagramSize = 2048`; the mDNS `T` key is built from `cfg.TCPClient`/`TCPServer` at `mdns/service.go:181-187`, which have **no production setter**, so `T` is always `"0"`), **BLE/BTP** (only two setup-payload capability bits at `secure/setup/setup.go:31,37,39`), **BDX/OTA**, **group sessions** (rejected at `bridge/receive_dispatch.go:77-92`; no multicast join in `transport/udp/listener.go`; `GroupKeyManagement` stores epoch keys but derives no group key), **Thread/Wi-Fi commissioning** (Ethernet-only is deliberate and test-pinned — §0.5-3), **subscription resumption** (`store/subscriptions.go:19-25` documents the table as inert; grep confirms zero callers), **in-attribute list chunking** (`bridge/reply.go:315-325` states the limitation; an oversized `Descriptor.PartsList` is downgraded to `ResourceExhausted` at `:322-325`).

**Where matter.js is itself a stub — the yardstick is not uniformly ahead:**

* `../matter.js/packages/node/src/behaviors/network-commissioning/NetworkCommissioningServer.ts:18` is a 14-line generated stub; so is `TimeSynchronizationServer.ts`. Neither project ships device-side Wi-Fi/Thread radio logic. (matter.js *does* ship a WI-scoped server with a simulated radio in a template: `packages/create/dist/templates/examples-device-onoff-advanced/cluster/DummyWifiNetworkCommissioningServer.ts` — a different statement from "real radio logic".)
* **Neither project implements §10.6.9 device-side subscription resumption** — a grep for `subscriptionResumption|resumeSubscription|persistedSubscription` over `../matter.js/packages/{protocol,node}/src` returns nothing.
* Of matter.js's 135 `*Server.ts` files, **82 carry the regeneration marker and only 53 are hand-written** — consistent with the partition in §3.3.

**The real deficit is conformance *evidence*, not protocol depth**, and two evidence-chain claims are refuted by measurement:

* `notes/reference/matter-conformance.md:63-64` states each manual controller run *"is recorded in `CHANGELOG.md` as `[manually verified]`"* — `grep -c "manually verified" CHANGELOG.md` → **0**.
* The same file at `:54-55` calls chip-tool *"a ship blocker: no green chip-tool pairing run, no release"*, yet `.claude/skills/release/SKILL.md:53` gates on `make lint && make test && make contract` and the skill contains zero occurrences of "matter" or "chiptool", while `.github/workflows/chiptool.yml:58-61` gates all three jobs on schedule / `workflow_dispatch` / the `needs-chiptool` label.

---

## 4. Question 2 — the roadmap

Effort per §1. The **Decision** column names the §7 decision that fixed each milestone's shape; `—` means no decision bore on it. All fourteen are taken, so nothing here waits on an answer.

### Phase 0 — Extraction prep, in loom, in place (nothing moves)

**Phase 0 is DONE.** Implemented on branch `feature/matter-phase0` (ten commits), with `make test` and repo-wide `make lint` green. Every acceptance criterion below was re-measured after the fact, not taken on report. The three §7 decisions that gated it resolved as **D1 (a)**, **D3** drop the parameter, **D2** flat `EndpointSpec`.

**The port package is `pkg/mattercontract`.** The plan said only "a second neutral `pkg/` package"; the name was decided during implementation. `matterport` was tried first and rejected: "port" already carries three meanings in this repo — the portability sense (the subtree *is* a semantic port of matter.js, per `CLAUDE.md`), the hexagonal sense, and the network sense (`rpc_callback.port`, `bin_port`) — and the first is the nearest reading, so the name pointed at the very tree it exists to separate from. `contract` is the word the sources already use for these interfaces (nine occurrences of "the contract" under `internal/north/matter/` alone). It becomes `go-fabric/contract` when the library gets its own module (D17).

**The headline result:** `go list -deps ./pkg/mattercontract | grep openccu-loom` prints exactly one line — the package itself. The port package depends on nothing else in this repo.

| # | Milestone | Acceptance criterion | Effort | Decision | Depends on |
|---|---|---|---|---|---|
| ✅ **P0.1** | Split the Matter ports out of `pkg/interfaces` into a second neutral `pkg/` package, leaving type aliases behind | `go list -deps ./internal/north/matter/cluster/core \| grep hmapi` → empty; all 51 `MatterClusterID` implementations compile unchanged; `make contract` green | M | D1 ✓ (a) — library owns the servers, so `wire`/`im` stay private | — |
| ✅ **P0.2** | Remove `hmenum.CommandPriority` from `MatterWrite`/`MatterInvoke` | `grep -rn 'hmenum' pkg/<newport>/*.go` → no match; both dispatcher call sites (`dispatcher.go:319,595`) compile; the 104 non-test implementations compile; host mapping is an **explicit switch**, not an ordinal cast | L | D3 ✓ — parameter dropped | P0.1 |
| ✅ **P0.3** | Invert the `DataVersionTracker` alias — **one atomic commit** | `grep -rln '"…/pkg/hmtypes"' internal/north/matter/cluster/ \| grep -v _test` → empty; the nine model embedders untouched | S | — | P0.1 |
| ✅ **P0.4** | `netutil` → `InterfaceFilter` on the advertiser config (+ a shared-behaviour test against loom's client-discovery advertiser); `health.Sample` → library-local struct; `i18n` → `ChannelLabel string` on `endpoint.Config` | `go list -deps ./internal/north/matter/mdns` and `…/bridge` list no `internal/netutil`, `internal/health`, `internal/i18n` | S | — | — |
| ✅ **P0.5** | Move `MatterSwitchEventEmitter` into the shared port package; drop the `*generic.ButtonGroup` pin | A **behavioural** wiring pin under `tests/contract/wiring_pins/` asserts a press event reaches a commissioner through the production constructor — not a compile-time `var _`. Bite proof: revert the emitter's package and observe red | S | D1 ✓ (a) | P0.1 |
| ✅ **P0.6** | Convert the three relative-path tests to `go:embed` **before** any move | `bridge/scenario_test.go:16`, `im/wire_fixtures_parity_test.go:47` (also strip the absolute developer path at `:57-58,349-350`), `parity/snapshot_identity_test.go:37-39` — pattern already in use at `tlv/parity_matterjs_test.go:31`; `go test ./internal/north/matter/{bridge,im,parity}/...` green from any cwd | M | — | — |
| ✅ **P0.7** | Fix the snapshot extractor's `${tag}:${id}` collapse | Regenerated snapshot carries 10 Command children for Groups (matching `../matter.js/…/groups.element.ts` lines 34,43,51,62,70,74,83,89,96,105); a new guard asserts `schema.DeviceTypeAllowsServerCluster(0x0016, 0x0038)` | S | — | — |
| ✅ **P0.8** | Fix loom's own notices before anything moves: add `filippo.io/nistec` (`go.mod:37`, used by `secure/spake2/spake2.go:21`), correct `docs/adr/0012:473`'s zeroconf licence to MIT (`THIRD-PARTY-NOTICES.md:176` already says MIT), name the embedded `internal/north/matter/parity/schema.json` in the matter.js entry, and **reproduce `../matter.js/NOTICE` verbatim** (D6) — 38 lines ending *"This NOTICE must be included on all copies of matter.js"*, replacing the hedge at `THIRD-PARTY-NOTICES.md:120-123` that names the wrong file | `grep -n nistec THIRD-PARTY-NOTICES.md` → a match (today: no match); `grep -n 'parity/schema.json' THIRD-PARTY-NOTICES.md` → a match; `ls licenses/NOTICE-matter.js.txt` → exists (today `ls licenses/` shows only `Apache-2.0.txt` and `MIT.txt`) | S | D6 ✓ | — |

#### 4.0 What Phase 0 actually did

Implemented 2026-09-04 on `feature/matter-phase0`. Each criterion below was re-run after the work, in the main conversation, rather than accepted from a report.

| # | Criterion, re-measured | Result |
|---|---|---|
| P0.1 | `go list -deps ./internal/north/matter/cluster/core \| grep hmapi` | 0 hits |
| P0.2 | `pkg/mattercontract` free of `hmenum`; its repo dependencies | 0 hits; exactly itself |
| P0.3 | `internal/north/matter/cluster/` free of `pkg/hmtypes` (non-test) | 0 files |
| P0.4 | subtree closure free of `internal/{netutil,health,i18n}` | 0 hits |
| P0.5 | behavioural pin: a press reaches the Matter event log | green, bites |
| P0.6 | no absolute developer paths under `internal/north/matter/` | 0 files |
| P0.7 | extractor discriminates same-id elements (Groups 6 → 10) | fixed |
| P0.8 | `nistec` listed; `licenses/` carries the NOTICE and the BSD text | both present |

**Divergences from the plan, and why.**

* **P0.1 needed the call sites after all.** The plan asked both for compatibility aliases *and* for `hmapi` to leave `cluster/core`'s closure. Those exclude each other: while a file says `interfaces.Matter*` it imports `pkg/interfaces`, which pulls `hmapi` through `rest_ports.go`. Resolved by switching the **65 subtree files** to `mattercontract` while the ~70 files outside it keep the aliases untouched. Three subtree packages — `bridge`, `endpoint`, `eligibility` — still reach `pkg/interfaces`, but only through `internal/model/{generic,device}`: that is the model-walking coupling of C11/C12, and hiding it behind a port move would have been the wrong kind of green.
* **P0.2 was measured by AST, not by grep.** A line-wise count of signatures says 19 forwarders; the real number is **22**, because three have multi-line signatures. Full count: 132 declarations, 108 ignoring the parameter, 22 forwarding, 2 unnamed.
* **P0.4 stopped at the composition root, as it should.** Severing `health.Sample` broke exactly one call site in `cmd/openccu-loom/`, and the translating adapter was written there by the caller rather than by the agent that found it.
* **P0.6 embedded one of three fixtures, not all three.** `bridge/scenario_test.go` keeps reading the corpus from `notes/`, because `tests/contract/matter_scenario_gate_test.go:32` gates coverage on that directory and a `testdata` copy would let a new scenario satisfy the gate without ever replaying. `parity/snapshot_identity_test.go` likewise: embedding both copies would reduce it to comparing a file with itself. Both now locate the repo root from `runtime.Caller` instead of counting dot-dots, which is what package-move independence actually requires.
* **P0.7 deliberately did not regenerate the snapshot.** The extractor fix is committed; the snapshot stays pinned. Regenerating against the local checkout would have folded an upstream version bump into a bug fix. Doing it, and against which commit, belongs with R5.

**Three findings the work turned up, none of them refactoring.**

1. **The extractor was losing 35 elements.** 16 commands across 4 clusters (Groups, ScenesManagement, DoorLock, CommodityTariff) and 19 device-type requirements across 14 device types. Groups declared 10 commands and the snapshot carried 6. Any conformance check built on that snapshot was blind to the difference — which is precisely the hazard §5's rule about guessed inputs describes.
2. **The zero-value trap was already sprung.** `CommandPriorityCritical` is the zero value, and `timedOnOffState` carried a `priority` field filled by a bare `var` declaration, documented as holding "the urgency the controller requested". It held whatever the dispatcher hard-coded. Field, declaration and comment are gone; every forwarder now names `CommandPriorityHigh` through a `const`, which cannot be left unset.
3. **`custom/switch`'s `MatterWrite` OnOff branch is unreachable.** `OnOff.OnOff` is read-only in the Matter schema, so `endpoint/dispatcher.go:252` answers `UnsupportedWrite` before any cluster server runs. Pre-existing, untouched, and recorded in §8.

**Also repaired in passing:** the event-priority parity guard read priorities by trimming a hard-coded qualifier off the source text, so the package rename made all fourteen events read as unprioritised while the test stayed green. It now accepts either spelling and fails loudly on a third. Fourteen files carried `[interfaces.MatterX]` godoc links while no longer importing `pkg/interfaces`; those were repointed, but only where the link genuinely resolved to nothing.

---

### Phase 1 — Split the model-walking layer (0 new domains, unlocks a second host)

**Check each row's premise against the current tree before implementing it.** P1.5 turned out to be void: it described a defect that **#692** had already fixed, because this plan was measured while that 713-file change sat uncommitted in the working tree — the condition the header warns about. The other rows were re-checked and hold, but the lesson generalises: a row here is a claim about a tree from 2026-09-03, not a standing fact.

**Phase 1 is DONE.** The gate below prints one line. What the phase actually cost, and where it diverged:

* **`internal/north/matteradapter` is the new host-side package** — the model walk, the allowlist policy, the name resolver. It is what stays in loom when the subtree becomes `go-fabric`.
* **The bridge was the last coupling, and it pointed the wrong way.** It imported the host adapter because it *built* the assembler; one import dragged seventeen packages. The snapshotter now returns a finished topology and the daemon assembles. Assembly timing was verified unchanged, not assumed.
* **A Phase 0 decision had to be reversed.** The `ButtonGroup` compile-time assertion was kept in production code on the argument that it "costs nothing". It cost seventeen packages. It now lives in a `_test.go`, where it still catches drift but stays out of the import closure.
* **`Reachable bool` became `Availability func() bool`** — `materialize` reads availability live on every dispatch, so a captured bool would have frozen it at assembly time. That would have compiled and passed every test.
* **The plan's line numbers were wrong three times** and it missed two call sites, one of them in the composition root. Every one was caught by an agent instructed to verify the premise first. §4.0's warning generalises: a row here describes a tree from one afternoon.
* **Vestigial, recorded not fixed:** `bridge.New`'s `store` parameter and `Bridge.store` are nil-checked and never read.

**Ahead of everything else in this phase**, `internal/north/matteradapter/topology_golden_test.go` records the assembled topology — endpoint ids, device types, names, unique ids, cluster sets — over a fixed four-device fleet. Every step in this phase left it byte-identical — verified after each, and git records the move as a rename with no content change. A changed endpoint id costs every paired controller a re-add (`migrations/007_matter_endpoints.sql`, Down comment), and no structural criterion can see it.

| # | Milestone | Acceptance criterion | Effort | Decision | Depends on |
|---|---|---|---|---|---|
| ✅ **P1.1** | Sever `internal/model/device` from `endpoint/types.go`: remove `Devices` (`:28`), flatten `BridgedDevice` (`:88`) to plain fields, remove `Channel` (`:93`) | `go list -deps ./internal/north/matter/endpoint` lists **zero** `internal/model/*` (today: 18 host packages); the three production readers — `materialize.go:145`, `:194`, **and `cmd/openccu-loom/daemon_matter.go:4057`** — compile against the flat fields | L | — | P0.* |
| ✅ **P1.2** | Move `assembler.go` + `friendlyName`/`parameterSuffix`/`isNotFound` to a host package; keep `truncateUTF8`/`measurementDeviceType`/`deviceTypeRevision`/`customDeviceTypeRevision` library-side | `go build ./...`; endpoint IDs byte-identical before/after on a fixture snapshot (a diff of `matter_endpoints` rows) | L | D2 ✓ — flat `EndpointSpec` | P1.1 |
| ✅ **P1.3** | Split `eligibility/`: `eligibility.go` host-side, `compat.go` + `vendor_name.go` library-side | `go list -deps ./internal/north/matter/eligibility` → empty (today: 17 host packages for 530 LOC); the six consumers (`matter_status_adapter.go:198-207`, `matter_event_publisher.go:131`, `daemon_matter.go:4113,4137`, `handlers/matter.go:279`, `handlers/matter_exposures.go:37`) compile | M | D5 ✓ — package split | P1.1 |
| ✅ **P1.4** | Define `EndpointSpec` as the library's assembly input and a `NameResolver` port | A table test builds a three-tier topology from hand-written `EndpointSpec`s with no `internal/model` import | L | D2 ✓ — flat `EndpointSpec` | P1.1 |
| ~~**P1.5**~~ | ~~Fix `MatterDeviceTypeName`~~ — **void: already done before this plan was written** | Every one of the 19 device types loom can emit is named in `pkg/mattercontract/matter.go:552` — including 0x0230 and 0x0510, which this row claimed fall through to the hex default. The generator already emits names (`script/generate_matter_schema.go:181-188` → `schema.DeviceTypeNames`, `schema/lookup.go:44`), and the acceptance test this row asks for already exists: `tests/contract/w2pkg_matter_device_type_name_coverage_test.go:123`, added by **#692** (`fix: remediate the 2026-09-03 south-core audit`). The plan was measured against a tree in the middle of that fix — see the dirty-tree warning in the header. 0x0043 sitting unreached is the one true half, and it is deliberate (Alexa, `pkg/mattercontract/matter.go:489-508`). | — | — | — |
| ✅ **P1.6** | **Gate:** the whole subtree is host-free | `go list -deps ./internal/north/matter/... \| grep openccu-loom \| grep -v internal/north/matter` → **empty**. This single command is the go/no-go for Phase 2 | — | — | P0.*, P1.1–P1.5 |

### Phase 2 — Publish (0 new domains, makes everything else observable)

**Then P4.1 immediately.** Since D18 the purpose of the extraction is independent testability, so the reference daemon is not deferred quality work — it is the instrument that delivers the purpose, and the only way `tests/chiptool/` (7,706 LOC, which execs `./bin/openccu-loom`) can travel. Milestone IDs stay put so existing references keep resolving; only the order changes: **P2.1 → … → P2.7 → P4.1 → the rest as needed.**

| # | Milestone | Acceptance criterion | Effort | Decision | Depends on |
|---|---|---|---|---|---|
| **P2.1** | Create the module **`github.com/SukramJ/go-fabric`** (D17), including the six stand-in cluster packages as a labelled conformance reference (D19): package names kept verbatim minus the `internal/north/matter/` prefix, so every intra-subtree rewrite is a pure prefix substitution; `pkg/mattercontract` becomes `go-fabric/contract`; new `ecosystem` package; `go.mod` requiring the five modules of §2.6 | `go build ./...` in the new repo; `go test ./...` green standalone | L | D6 ✓, D7 ✓ | P1.6 |
| **P2.2** | One loom commit: delete the subtree, rewrite ~100 consumer import paths, add the `require` at a pseudo-version | `make test && make contract && make lint` green on the same commit; **no filesystem `replace`** (loom CI checks out one repo, `.github/workflows/ci.yml`; `ls go.work*` → no matches today) | M | — | P2.1 |
| **P2.3** | Relocate the schema pipeline: extractor, snapshot, generator, embed. `Makefile:496-514` and the four hard-coded paths in `script/generate_matter_schema.go:35-39` re-pointed **in the same commit** | `make generate-matter-schema` reproduces `schema/{clusters.go,devicetypes.go,schema_provenance_gen.go}` byte-identically (snapshot 516,843 bytes, `sourceCommit` unchanged); the six model-side `parity_matterjs_test.go` files still call `parity.SchemaJSON()` through the module | M | — | P2.1 |
| **P2.4** | Persistence split implemented — see §5.3, the hard rule | A fresh library-only DB passes the store test suite; an existing loom DB migrates with **zero DDL change**; `matter_endpoints` and `matter_exposures` are reached only through the host port, and a golden test shows the rendered endpoint key is **byte-identical** to the pre-split output for a fixture fleet | L | D4 ✓, D15 ✓ | P2.1 |
| **P2.5** | Replace the lost guards | `tests/contract/matter_schema_sync_test.go:26-40` compares two loom paths and becomes meaningless — a library-side stale-embed guard must exist; `tests/contract/test_shard_script_test.go` passes with re-tuned shards (`script/test_shard.sh:22` states the subtree "alone is a third of the tree"); `script/reachability/main.go:571,626,641` key on the literal path and go dead; `notes/parity/dead-code-inventory.json` (6,144 lines matching `north/matter`) rebaselined | M | — | P2.2 |
| **P2.6** | Library `LICENSE` (MIT), `THIRD-PARTY-NOTICES` correct where loom's is not, license-header guard | `filippo.io/nistec` listed; zeroconf recorded as **MIT**; the embedded snapshot named explicitly; a license-header guard test exists (`grep -rn 'SPDX-License-Identifier: MIT"' tests/` → no match today, so none is inherited) | M | D6 ✓, D7 ✓ | P2.1, P0.8 |
| **P2.7** | Independent module version + release lane | A Matter fix reaches a consumer without a daemon release. Today `internal/build/version.go:22` is the single version carrier and `.claude/skills/release/SKILL.md:20` enumerates five carriers, none a module | M | D14 ✓ — own SemVer lane | P2.2 |

### Phase 3 — Domains, ordered by unlock count

**Narrowed by D18.** In scope are only the rows that bite on loom's own device population (§3.1's closing note): **P3.3** (`binary_sensor`), **P3.4** (`select` / `input_select` — the only honest carrier for read/write ENUM data points), **P3.6** (`valve`, for `IPIrrigationValve`) and **P3.7** (`media_player`, for `IPSoundPlayer`). **P3.1, P3.5, P3.8, P3.9 and P3.10 are deferred with goal 3**; **P3.2a–c is what D19 asks about**. The table stays whole so the deferred work remains costed rather than forgotten.

| # | Milestone | HA domains unlocked | Acceptance criterion | Effort | Decision | Depends on |
|---|---|---|---|---|---|---|
| **P3.1** | **Real Groups (0x0004) + ScenesManagement (0x0062) servers** | 0 new — repairs conformance on **11** already-claimed domains | `AcceptedCommandList` on a bridged OnOff endpoint returns all six Groups commands and all seven Scenes commands; a chip-tool `groups add-group` / `scenesmanagement recall-scene` round-trip succeeds against the reference slot. **A conformance guard built on the snapshot before P0.7 lands reports both green** | L | — | **P0.7** |
| **P3.2a** | **Raise the three library cluster servers to production capability.** They are conformance stand-ins today, not implementations: `cluster/cover/windowcovering_server.go:147-151` states in source that all four commands *"accept the request and update internal state; they return Success **without forwarding to a CCU backend**. Callers that need live-CCU control use the rich-model projections in `internal/model/custom/cover/matter.go` instead."* | 0 — prerequisite | Each of the three forwards to a value port instead of mutating internal state. Measured feature gaps to close: **ColorControl is CT-only** — `cluster/light/colorcontrol_server.go:197-212` excludes CurrentHue / CurrentSaturation / CurrentX / CurrentY by design, and names them 3× against 99× in `custom/light/matter_color.go`; **WindowCovering has no tilt at all** — `Tilt` appears 0× in the library server against 42× in `custom/cover/matter.go`. The Thermostat delta was **not measured** — P3.2a establishes it first | L | D1 ✓ (a) | P2.2 |
| **P3.2b** | **Draw the host-policy line before moving anything.** Not every LOC in the nine files is cluster logic | 0 — scoping | `cover/matter_debounce.go` (249 LOC) stays host-side and is struck from the move: it is duty-cycle protection for the Homematic radio, and says so at `:16-26` — *"HM cover actuators sit behind a duty-cycle-limited radio … matter.js has no debounce here."* Conversely `light/matter_timed_onoff.go` (361 LOC) **does** move: it is the Matter §1.5.8 OnOff-LT timed-command engine ported from matter.js `OnOffServer.ts:230/:258`. Every one of the nine files is classified as cluster logic, host policy, or host binding before P3.2c starts | S | D1 ✓ (a) | — |
| **P3.2c** | **Switch loom over** to the library servers behind the value ports | 13 domains, **for a second host** | The recorded wire comparison of D16 is green — and its fixtures **must exercise the measured gaps**, i.e. an HS and an XY colour device and a tilt-capable blind, or it cannot observe the regression it exists to catch. Then: the 14 `MatterClusterID` implementations under `internal/model/` reduce to value-port adapters, and `grep -rn 'north/matter/cluster/wire' internal/model/` → empty | L | D1 ✓ (a), D16 ✓ | P3.2a, P3.2b |
| **P3.3** | **OnOffSensor 0x0850 fallback + open the measurement registry** | `binary_sensor` fully; `sensor` breadth | A host can register a new measurement kind without editing the library; the closed enum at `pkg/interfaces/matter.go:238-255` and its four switches (`:469`, `:533`, `:581`, `cluster/measurement/measurement.go:887`) are no longer the extension point; an unclassified boolean DP produces a 0x0850 endpoint instead of being dropped at **both** `assembler.go:417` **and** `:524` | L | D2 ✓ — flat `EndpointSpec` | P1.4 |
| **P3.4** | **ModeSelect cluster 0x0050 + device type 0x0027** | **3** — `select`, `input_select`, `alarm_control_panel` | A chip-tool `modeselect read supported-modes` returns a host-supplied list; `ChangeToMode` reaches the host binding. Also the only honest carrier for read/write ENUM data points on the loom side | M | — | P3.2c |
| **P3.5** | **FanControl 0x0202 + Fan 0x002B** | **1** — `fan` (plus AirPurifier 0x002D, RoomAirConditioner 0x0072, MicrowaveOven 0x0079, ExtractorHood 0x007A) | Features MultiSpeed/Step/Auto/AirflowDirection selectable per endpoint, matching hamb's derivation at `…/fan/index.ts:23-43` | M | — | P3.2c |
| **P3.6** | **ValveConfigurationAndControl 0x0081 + WaterValve 0x0042** | **1** — `valve` | Mandatory attributes **OpenDuration 0x0, DefaultOpenDuration 0x1, RemainingDuration 0x3, CurrentState 0x4, TargetState 0x5** served; commands **Open 0x0 / Close 0x1** handled. **0x2 is `AutoCloseTime`, conformance `"TS"` — feature-gated, NOT mandatory.** Read verbatim from `../matter.js/packages/model/src/standard/elements/valve-configuration-and-control.element.ts:27-38,:60,:64`; a list taken off the lossy snapshot would not give this. **Does not change loom's own valves** — `docs/adr/0049…:47-50` decides 0x010A for `valve.Irrigation` | M | — | P3.2c |
| **P3.7** | **Speaker 0x0022 for media_player** | **1** — `media_player` | 0x0022 emitted with the OnOff + LevelControl servers it mandates (`schema/devicetypes.go:299-303`; `../matter.js/packages/node/src/devices/speaker.ts:64,76-79`). **No MediaPlayback server involved.** Residual loom-side gap is narrow: `siren.SoundPlayer` has no Matter projection, and re-labelling MP3P from DimmableLight to Speaker is a device-type change over servers that already exist. Plus **MediaInput 0x0507** served when the host reports a source list: hamb mounts it conditionally at `…/media-player/index.ts:53-55` (behavior at `…/media-player/behaviors/media-player-media-input-server.ts:4`), and matter.js's server is a 14-line generated stub, so this is S-effort and does **not** justify the XL Media-family label | M | D8 ✓ — clean-sheet | P3.2c |
| **P3.8** | **RVC family: 0x0054 + 0x0061 (+0x0060, 0x0055, 0x0150) + device type 0x0074** | **1** — `vacuum` (+ `lawn_mower` by extension) | Device pairs and responds to run-mode changes on at least one controller. Ecosystem acceptance is a **separate unmeasured gate** — hamb's own docs flag vacuum as controller-dependent | L | D10 ✓ — unlock order, acceptance per milestone | P3.2c |
| **P3.9** | Sensor-cluster fill: FlowMeasurement 0x0404, SoilMeasurement 0x0430, seven concentration clusters, BooleanStateConfiguration 0x0080 | 0 new (all under `sensor`), fidelity + breadth | For each new cluster, a parity test asserts `schema.ClusterRevision(id)` matches the snapshot and every `conformance: "M"` attribute appears in `MatterAttributes()`; bite proof: hand-edit one revision and observe the test red. Each server reuses the parameterised `concentrationServer` shape (`cluster/measurement/measurement.go:1407-1470`) | S each | — | P3.3 |
| **P3.10** | Fidelity types: WaterLeakDetector 0x0043, WaterFreezeDetector 0x0041, DimmablePlugInUnit 0x010B, native WaterHeater 0x050F, root-endpoint optional 0x0034/0x0037/0x002B/0x002C/0x002D, Aggregator Actions 0x0025 | 0 new | Each device type is reachable only through an explicit host opt-in flag, and re-enabling 0x0043 is gated on a recorded Alexa pairing run — the ceiling written into `pkg/interfaces/matter.go:483-502` ("a single endpoint advertising 0x0043 renders the whole bridged node unresponsive") — not on a green unit test | M | D11 ✓, D12 ✓ | P3.3 |

### Phase 4 — Library-grade quality

| # | Milestone | Acceptance criterion | Effort | Decision | Depends on |
|---|---|---|---|---|---|
| **P4.1** | Runnable examples **and the reference daemon — one artefact** (D13 merged P4.2 into this row) | ≥1 `examples/` app pairing a fake host to a controller **without importing anything the README does not document**. Today: `grep -rn "^func Example" --include='*_test.go' internal/ pkg/` → **0**, and no `examples/` directory. matter.js ships 21 (`ls -d ../matter.js/examples/*/ \| wc -l`), including the bridge-shaped `device-bridge-onoff`. **An example that cannot be written without reaching into internals is the honest test that the seam is wrong.** | M | — | P2.1 |
| **P4.2** | *(merged into P4.1 by D13)* — the chip-tool leg of that same daemon | A green chip-tool pairing run against the reference daemon in the library's own CI. Without it the library ships with **no real-commissioner guard** — `tests/chiptool/` (7,706 LOC) execs `./bin/openccu-loom` (`Makefile:222-228`) and cannot move | XL | D13 ✓ | P4.1 |
| **P4.3** | Downstream-usable test harness | A consumer can regression-test its own cluster servers. Today every harness is `_test.go`-private or `//go:build chiptool`. matter.js publishes `packages/testing/` | L | — | P4.1 |
| **P4.4** | Path-filter the chiptool trigger on `internal/north/matter/**` | `.github/workflows/chiptool.yml:58-61` carries a `paths:` filter on `internal/north/matter/**`; bite proof: a one-line change under that path opens a PR **without** the `needs-chiptool` label and the chiptool job runs | M | — | — |
| **P4.5** | Close the fuzz gap | `Makefile:365` lists `./internal/north/matter/tlv/...` but `grep -rn Fuzz internal/north/matter/tlv/` → **no match** (reproduced), so that leg silently runs nothing. Add TLV, `transport/message`, `secure/channel`, `secure/mattercert` (535 lines of DER), `secure/setup` — the last two parse attacker-supplied bytes. Commit seed corpora. Today the only Matter fuzz targets are four in `im/fuzz_invoke_test.go:18,33,45,56` | M | — | — |
| **P4.6** | Coverage floor + benchmarks for the subtree | `script/coverage_per_package.sh` names **no** `internal/north/matter/*` package and sets `FLOOR=0` for anything unlisted (`:94`); `grep -rn Benchmark internal/north/matter/` → **no match**. Note the subtree is heavily tested in absolute terms (301 test files, 95,771 test LOC vs 52,061 non-test) — this is a missing ratchet, not missing tests | M | — | P2.2 |
| **P4.7** | Fix the two evidence-chain claims | Either start recording the manual controller runs in `CHANGELOG.md`, or delete the claims at `notes/reference/matter-conformance.md:54-55` and `:63-64` (§3.4) | S | — | — |
| **P4.8** | Matter-specific threat model | A `notes/audits/matter-threat-model.md` exists covering PASE passcode brute-force budget, fail-safe abuse, fabric isolation and group-key handling, and each of the 16 `//nolint` directives under `internal/north/matter/secure/` carries a one-line justification a reviewer signed off. Context: `docs/SECURITY.md` is daemon-scoped (Matter is one asset row at `:20`, one danger note at `:71-72`), there is no root `SECURITY.md`, no crypto review among the 19 files under `notes/audits/`, CodeQL is explicitly non-blocking (`.github/workflows/codeql.yml:4-6`), and 130 `//nolint` sit in non-test Matter code | M | — | P2.1 |
| **P4.9** | Go-module API-stability + deprecation policy | The 6 Matter WS broadcasts in `assets/wsapi.json` carry `payload` schema references (today recorded TBD *"pending Matter-surface stabilisation"*, `docs/adr/0020:140`), and a named deprecation window appears in the module README. Context: the **REST** surface is already pinned (`tests/contract/testdata/api_surface.json` + `TestAPISurfaceChangesCarryTheRightBump`, `tests/contract/api_surface_bump_test.go:91`, against `internal/north/rest/handlers/info.go:13-19`); ADR 0050:61-75 has the right shape but is scoped to `go-mqtt` | S | D14 ✓ — own SemVer lane | P2.1 |
| **P4.10** | Widen the CI matrix + add an upstream-freshness signal | The CI matrix builds and tests on at least one 32-bit GOARCH covering the shipped `goarm: ["7"]` target (`.goreleaser.yaml:29-31`), and a scheduled job fails when `git -C ../matter.js rev-list --count <sourceCommit>..HEAD` exceeds a declared threshold. Context: `GO_VERSION: "1.26.6"` hard-coded identically in all 10 workflow files; PR legs ubuntu + macos; Windows nightly-only and gates nothing (`nightly.yml:44-49`); armv7 is **shipped but never tested**; `tests/contract/matter_schema_sync_test.go:27-41` compares two in-repo copies and cannot detect upstream drift; the pin is **59 commits behind** | M | — | P2.2 |
| **P4.11** | **`Test_TC_*` as conformance regression** — certification itself stays a non-goal | At least one chip `Test_TC_*` case runs green in the library's CI against the reference daemon, and the set grows per release. A `pics.properties` is written only as far as those cases need it, **not** as a certification artefact. Repo-wide today: no PICS file anywhere (`find -iname '*pics*'` matches only MQTT *topics* files) and no cert test; matter.js runs `chip-cert-tests.yml` with a 178-line `matter-js-pics.properties`. D9 kept `SPECIFICATION.md:175` intact and rescoped this row from an XL certification programme to incremental adoption | M, then S per added case | D9 ✓ | P4.1 |

---

## 5. Risk register

| # | Risk | Evidence | Severity | Mitigation |
|---|---|---|---|---|
| **R1** | **matter.js NOTICE obligation on the embedded snapshot** | `internal/north/matter/parity/schema.json` is 516,843 bytes of machine-extracted `@matter/model`, `//go:embed`-ed at `parity/parity.go:18-20`. `../matter.js/NOTICE` final line, verbatim: *"This NOTICE must be included on all copies of matter.js."* loom hedges at `THIRD-PARTY-NOTICES.md:120-123` that the NOTICE "travels with the matter.js repository", and the hedge names `notes/parity/matter/matter-schema-snapshot.json` — **not** `internal/north/matter/parity/schema.json`, which is the copy embedded into every binary. The redistribution obligation attaches to the file the notices document does not name. `ls licenses/` shows only `Apache-2.0.txt` and `MIT.txt`; there is no `NOTICE` at the repo root | **High** | P0.8 names the embedded file; P2.6 decides the obligation explicitly rather than inheriting the hedge. Options: embed + carry matter.js's LICENSE and NOTICE; regenerate at build time from a consumer-supplied checkout; or freeze it host-side and take the tables as data. Whether the Go port is a Derivative Work is a legal question this document cannot resolve |
| **R2** | **CSA trademark clause vs. the module name** | Same NOTICE: *"Only the Alliance and its members may use Alliance trademarks and logos, including, without limitation, the Matter trademarks and logos"* | ~~High~~ **Resolved** | **D7:** the module takes an own name in the repo's house form and uses "Matter" descriptively in the README only, with matter.js's non-certification disclaimer. The clause is no longer load-bearing on the module path. It had never been considered in this repo before — no document addressed it |
| **R3** | **Missing third-party notice** | `filippo.io/nistec` is a production dependency (`go.mod:37`, used by `secure/spake2/spake2.go:21`); `grep -n nistec THIRD-PARTY-NOTICES.md` → no match. `docs/adr/0012:473` mis-states zeroconf as Apache-2.0 while `THIRD-PARTY-NOTICES.md:176` says MIT | Medium | Fix in loom (**P0.8**) and in the library's own notices (P2.6) |
| **R4** | **Certification** | `SPECIFICATION.md:175` declares "No full Matter certification" a non-goal; the binary ships the CSA **test** vendor block by default (`internal/config/config.go:972-975` → VID 0xFFF1, PID 0x8000) with the embedded CSA test PAA chain (`secure/attestation/testpaa.go`) | Medium | **D9:** certification stays a non-goal. A published module that says "not certified, test VID by default" is honest and usable — say it in the README rather than implying otherwise. Conformance is nevertheless measured, via `Test_TC_*` against the reference daemon (P4.11) |
| **R5** | **matter.js drift** | Snapshot pinned at `sourceCommit c6b188fe…`, 2026-08-26; checkout HEAD `75633fa…`, 2026-09-03 — **59 commits behind**. Regeneration needs a *built* `../matter.js` and Node (`Makefile:496-514`) | Medium | P4.10. The existing sync guard compares two **in-repo** copies only and cannot detect upstream drift |
| **R6** | **Two consumers, one API** | Today the API would be designed against **one real caller**. hamb is TypeScript and cannot import Go interfaces, so the "second host" is currently hypothetical | **High** | P4.1's example app is the cheapest proxy for a second caller. Without it, every port shape is a guess dressed as a design. Do not tag v1 before P4.1 |
| **R7** | **API churn against a frozen shape** | Publishing before Phase 1 locks `hmenum.CommandPriority`, `endpoint.Snapshot`, the closed measurement enum and `FabricScopedReader`'s hidden precondition into a v1. Today P0.2 costs one scripted commit across 104 implementations; after publication it costs a major bump in every consumer | **High** if the phase order is inverted | The phase order **is** the mitigation |
| **R8** | **The library ships with no real-commissioner guard** | `tests/chiptool` (7,706 LOC) execs `./bin/openccu-loom` (`Makefile:222-228`) and cannot move; its CI is label-gated off the default PR path | **High** | P4.2 is not optional. Until it lands, the library's correctness rests on loom's binary |
| **R9** | **Migration renumbering breaks every existing install** | Reproduced: `grep -c 'IF NOT EXISTS' internal/store/sqlite/migrations/*matter*.sql` returns **0 for all eleven files**. `goose.SetTableName` exists (`goose v3.27.3 version.go:50`) but adopting a second sequence on a live DB needs an explicit baseline step | **Critical** | §5.3, hard rule |
| **R10** | **Endpoint-key change silently desyncs every commissioned controller** | `migrations/007_matter_endpoints.sql:15-26` pins the 5-tuple PRIMARY KEY and a `dp_kind` CHECK; its Down comment records that reassignment "desyncs every commissioned controller's cached accessory list until each bridged device is removed and re-added" | **Critical** | §5.3, hard rule. Any milestone that touches it needs a re-pair plan, not a migration |
| **R11** | **Silent-no-op class of bug during the move** | `bridge/bridge.go:847-858` records that a duplicated emitter interface never matches and no press event reaches a commissioner — a compile-clean, test-clean failure | Medium/High | P0.5 requires a *behavioural* wiring pin with a bite proof, never a compile-time `var _` |
| **R12** | **The vendored `netutil` predicate forks** from loom's client-discovery advertiser | C4 | Medium/Low | P0.4 adds a shared-behaviour test over the same input table in both repos |
| **R13** | **CI legs go quietly green** | `make fuzz` already walks `internal/north/matter/tlv`, which has **zero** Fuzz functions (reproduced: `grep -rn Fuzz internal/north/matter/tlv/` → no match), so the leg passes without running anything. chip-tool is label-gated. CodeQL "does not fail the build on alerts" (`.github/workflows/codeql.yml:4-6`) | **Certain / Medium** | P4.4, P4.5. **Every new CI leg needs a negative control before it counts** |
| **R14** | **The stand-in servers are a conformance reference, and must read as one** (D19) | `cluster/light` (246), `cluster/cover` (237), `cluster/thermo` (583) have **0** non-test importers (reproduced; `cluster/lock` returns 2 with the same pattern, so the measurement bites). `windowcovering_server.go:147-151` says in source that its commands return Success *without forwarding to a CCU backend* | Medium → **High if P3.2 is read as a move** | D18 defers P3.2 and D19 keeps the packages as a labelled reference: each states in source that it does not forward, and a guard fails if one acquires a production caller while P3.2 is deferred. Should P3.2 ever be taken up, P3.2a raises them to production capability *before* P3.2c switches loom over — reading it as "the servers already exist, just re-point the callers" would swap field-proven code for code that has never driven a device |
| **R15** | **Release lane** | `internal/build/version.go:22` is the single version carrier; `.claude/skills/release/SKILL.md:20` enumerates five carriers, none a module | Medium | P2.7; adopt the go-mqtt pattern (`docs/adr/0050:61-75`, exact pins, squash-only fan-out) plus a dependabot entry |

### 5.3 The persistence trap — a hard, non-negotiable rule

Two measured facts make SQL the one part of this extraction that must **not** be treated as portable, even though `store/store.go:21` takes nothing but `*sql.DB` and pulls zero host packages:

1. **None of the eleven `matter_*` migrations uses `IF NOT EXISTS`.** Reproduced verbatim, all eleven returning 0: `006_matter_persistence`, `007_matter_endpoints`, `008_matter_resumption`, `009_matter_exposures`, `010_matter_resumption_cats`, `011_matter_diagnostics`, `012_matter_fabric_root_cert`, `013_matter_metadata`, `018_matter_persistent_subscriptions`, `025_matter_settings`, `036_matter_next_endpoint_id`. A library migration set renumbered from 1 therefore fails on every existing install.
2. **`matter_endpoints`' PRIMARY KEY is the 5-tuple `(central_name, device_address, channel_no, dp_kind, dp_key)` with a `CHECK` on `dp_kind`** (`migrations/007_matter_endpoints.sql:15-26`), and that migration's own Down comment states reassignment *"desyncs every commissioned controller's cached accessory list until each bridged device is removed and re-added."*

**The rule (P2.4):**

* The library exports an **idempotent `EnsureSchema(ctx, db)`** with `CREATE TABLE IF NOT EXISTS` DDL copied verbatim, **for greenfield embedders only**.
* **loom keeps its 11 numbered migrations untouched and never calls it.**
* **Endpoint identity is a port, not a library type** (D15). The nine protocol-generic tables travel; `matter_endpoints` and `matter_exposures` stay host-side behind an interface, because their key is host vocabulary — `EndpointKey{CentralName, DeviceAddress, ChannelNo, DPKind, DPKey}` with `DPKind ∈ {custom, generic, calculated, combined, measurement}` (`store/endpoints.go:18-37`), spanning six `Store` methods in `endpoints.go` and `exposures.go`. The library holds an **opaque key** (a string / `Stringer`); loom renders its 5-tuple host-side to **exactly the string it renders today**, pinned by a golden test. Byte-identical output is what keeps migration 007's warning inert: nothing re-pairs.

Extraction plans habitually treat SQL as the portable part. Here it is the part with a Critical failure mode on both sides.

---

## 6. Non-goals

Explicitly **out of scope**. Each is a deliberate boundary, not an omission.

1. **Controller / initiator role.** The crypto is role-complete (`sigma.NewInitiator`, `spake2.NewProver`) but has zero non-test callers, and nothing above it exists — no peer set, no outbound exchange manager, no client subscriptions. `secure/mattercert/` is decode+verify only. Adding it reshapes `im` and `secure/operational` substantially. **Confirmed by D11**; the role-complete crypto stays in place, costing nothing and keeping the decision reversible.
2. **Thread and BLE/BTP.** BLE is unreachable in pure Go without a CGo-free peripheral stack, and this repo forbids CGo (`CLAUDE.md`, "No CGo"). Thread additionally needs OpenThread. This is a platform-binding problem, not a protocol one.
3. **CSA certification.** `SPECIFICATION.md:175`, **confirmed by D9** and unchanged. Chip's `Test_TC_*` cases are nevertheless adopted as a conformance regression (P4.11) — running them requires no membership and is independent of certifying anything.
4. **BDX / OTA.** Depends on TCP and a transfer layer neither project's bridge use case needs.
5. **Group-cast.** Currently rejected rather than unimplemented (`bridge/receive_dispatch.go:77-92`). Adding it means new session types, operational group-key derivation and IPv6 multicast membership. **Confirmed by D12** — which nevertheless keeps the Groups and ScenesManagement *cluster servers* in scope (P3.1), since their commands arrive over unicast.
6. **Changing the persisted endpoint-key encoding.** §5.3 makes this a hard constraint, not a preference.
7. **Backwards-compatibility shims for hamb's numeric contracts.** hamb's illuminance conversion, humidity ×100 and battery ×2 scalings are wire-visible. **Settled by D8 (clean-sheet):** they are not reproduced. Migrating from hamb requires re-pairing regardless, so there are no existing pairings the shims would protect.
8. **No behaviour change to `openccu-loom` as a side effect of extraction** — goal 1. Every Phase 0–2 milestone is behaviour-preserving, and so is every Phase 3 milestone that *moves* code rather than adding coverage: **P3.2c carries a recorded wire comparison** (D16), and P3.2b keeps host policy — the cover debounce — out of the library entirely. Feature work is the rest of Phase 3.
9. **No `internal/model` redesign to please the library.** The rich-model / dumb-bridge split (ADR 0012) stands; the library adapts to it.
10. **The library does not own naming.** Daemon-owned entity naming is a standing rule; `endpoint/helpers.go:24-38` records why. The library takes a `FriendlyName` string.
11. **No `go.work` in CI.** Local dev may use one; the CI path is a pseudo-version `require`.
12. **Reproducing matter.js's feature-variance machinery** (`packages/model/src/logic/cluster-variance/`) or its runtime class generation (`packages/general/src/util/GeneratedClass.ts`). A Go port fixes feature sets per device profile at build time; an architectural choice, not a measurement.

---

## 7. Decisions taken

All fourteen were decided on **2026-09-04**. Each row records the choice, the reason that carried it, and what it obliges. The `Decision` column in §4 points back here.

| # | Question | Chosen | Why, and what it obliges |
|---|---|---|---|
| **D1** | Who owns the cluster servers? | **(a) The library owns them; hosts implement narrow value ports** | The pattern is already in production twice — `cluster/lock/doorlock_server.go:85-88` (`StateSource`) and `cluster/light/colorcontrol_server.go:60-61` (`ColorTemperatureWriter`) — so this generalises what works rather than inventing a shape. Deletes the 14 model-side `MatterClusterID` implementations and the `model → north/matter` import edge. **Obliged P3.2a–c (XL)**, which D18 defers and D19 resolves for the meantime (the servers travel as a labelled conformance reference) — — and note what that obligation contains: the library servers are conformance stand-ins today (R14), so this is a build, not a re-point. Keeps `wire` payload structs and `im` status codes *out* of the public API permanently. |
| **D2** | Does the library keep an assembler? | **No — a flat `EndpointSpec` is the assembly input** | `assembler.go` (954 LOC) is exactly the model-walking layer the cut line separates. Moving it host-side resolves C11 alone, instead of turning C9, C10 and C11 into three separate port designs. The library keeps topology construction and endpoint-ID persistence. |
| **D3** | What happens to `hmenum.CommandPriority`? | **Drop the parameter** | The only option needing no compatibility story for the 104 existing implementations — they lose a parameter they already forward unread. Under D1(a) the host sets urgency inside its own value port, where it has the context. Removes the last host import from the port file. |
| **D4** | Who owns the SQL? | **The concrete `Store` travels; `EnsureSchema` is greenfield-only** — *narrowed by D15 to the nine protocol-generic tables* | `store/store.go:21` already takes nothing but `*sql.DB` and pulls zero host packages. loom keeps its eleven numbered migrations untouched and never calls `EnsureSchema`. The clause that `store.EndpointKey` travels byte-for-byte **was revised the same day by D15**, once the goal alignment showed it would put host vocabulary in the module API. §5.3 is the hard rule this implements. |
| **D5** | Does `eligibility/` belong in the library? | **Split it** | `eligibility.go` (333 LOC) is CCU allowlist policy filed under a Matter name and goes host-side. `compat.go` + `vendor_name.go` (197 LOC, zero host imports) are ecosystem knowledge every host needs equally and stay library-side. |
| **D6** | How is the matter.js lineage handled legally? | **Reproduce the NOTICE; leave the Derivative-Work question open** | `../matter.js/NOTICE` exists (38 lines) and ends *"This NOTICE must be included on all copies of matter.js."* Carrying it costs nothing and holds even if the legal answer later turns out to be "yes". It also closes the existing gap: `THIRD-PARTY-NOTICES.md:120-123` hedges that the NOTICE "travels with the matter.js repository" and names `notes/parity/matter/matter-schema-snapshot.json` — not `internal/north/matter/parity/schema.json`, the copy actually embedded in every binary (R1). |
| **D7** | May the module carry "Matter" in its name? | **No — own name, descriptive subtitle** | The NOTICE is explicit: *"Only the Alliance and its members may use Alliance trademarks and logos, including, without limitation, the Matter trademarks and logos."* `SPECIFICATION.md:175` rules out certification anyway, so the mark is never legitimately claimable. The module takes a name in the repo's house form (`go-mqtt`, `go-openccu-data`) and uses "Matter" descriptively in the README only, carrying matter.js's own non-certification disclaimer. **Resolves R2**; the concrete name is D17. **Corrected afterwards:** this decision was argued against the precedent that matter.js carries the mark while being uncertified. That precedent is weaker than it looked. matter.js began inside `project-chip` — the Connectivity Standards Alliance's own GitHub organisation, which is where `connectedhomeip` lives — and only later moved to a `matter-js` organisation describing itself as an unofficial implementation. The name plausibly predates the move, under the very membership exemption the NOTICE names. We have no such starting position, so the cautious reading stands. |
| **D8** | hamb-compatible, or clean-sheet? | **Clean-sheet, native device types** | The compatibility argument does not hold here: migrating from hamb means re-pairing regardless — different vendor/node identity, different endpoint keys. There are no existing pairings to preserve, only inherited workarounds. hamb is a witness per mapping, not a specification. Where its choice is the better one it is adopted individually, with the reason recorded. |
| **D9** | Is CSA certification ever in scope? | **No — but selected `Test_TC_*` run as conformance regression** | `SPECIFICATION.md:175` stands and the README says so plainly. Independently of membership, chip's `Test_TC_*` cases find real conformance defects, and once P4.1 exists they have something to run against. This turns P4.11 from an XL certification programme into incremental test adoption. |
| **D10** | How is Phase 3 prioritised? | **Build by unlock count; measure acceptance per milestone** | Which controller accepts which device type is measured nowhere in this repo. Making it a gate per milestone — no device-type milestone is done before its acceptance is measured against a real controller — turns the unknown into a check that can fail, instead of a guess made up front. |
| **D11** | Controller/initiator role, and WaterLeakDetector 0x0043? | **Role stays a non-goal; 0x0043 behind a host opt-in** | The crypto is role-complete (`sigma.NewInitiator`, `spake2.NewProver`) but has zero non-test callers and nothing above it; adding the role reshapes `im` and `secure/operational`. It stays where it is — costing nothing and keeping the decision reversible. For 0x0043, P3.10 already carries the right shape: reachable only through an explicit host flag, with re-enabling gated on a *recorded* Alexa pairing run rather than a green unit test (`pkg/interfaces/matter.go:483-502`). |
| **D12** | Are group sessions in scope? | **Cluster servers yes, group-cast no** | Groups (0x0004) and ScenesManagement (0x0062) commands arrive over unicast, so real servers repair the conformance gap on 11 already-claimed domains without any group addressing. New session types, operational group-key derivation and IPv6 multicast membership stay out (non-goal 5). |
| **D13** | Does the library get a reference daemon? | **Yes, and it is the example** | `tests/chiptool/` (7,706 LOC) execs `./bin/openccu-loom` and cannot move, so without one the library ships with no real-commissioner guard (R8). P4.1 and P4.2 are nearly the same artefact: an example that cannot be written without reaching into internals is the honest test that the seam is wrong. **They merge.** |
| **D19** | Who owns the actuator cluster servers once P3.2 is deferred? | **The library carries the genuine stand-ins as a labelled conformance reference** | D1(a) put them in the library, which obliged P3.2; deferring P3.2 would have left unexplained weight behind. **Corrected 2026-09-05 while building the module:** the premise counted six packages and 1,892 LOC with zero production callers. Only three qualify — `cluster/{light,cover,thermo}`, 1,068 LOC, measured 0 callers each. `cluster/lock` has **2** (`internal/model/custom/lock/matter.go:73`), `cluster/closure` **1** (`matter_closure.go:91`) and `cluster/onoff` **4**: those drive real devices and are ordinary servers needing no justification to travel. The error was mine — Phase 0 used `cluster/lock` as the **negative control** for exactly this measurement, and the decision then folded all six together anyway. So: the three real stand-ins travel and say in source that they are a conformance reference and do not drive a device; the other three travel as what they are. The real actuator servers stay in `internal/model/custom/*`. |
| **D18** | Does goal 3 (hamb equivalence) stand? | **Deferred; the extraction is kept, for testability** | Goals 1 and 2 follow from loom's needs; goal 3 did not, and was never put to the maintainer. `docs/adr/0012:74-90` already rejected a matter.js sidecar on the CCU add-on's resource envelope, and `CLAUDE.md:64-66` makes "Matter without HA" the product goal — which is what justifies a Go stack, rather than out-competing hamb. **Settled 2026-09-05:** goal 3 deferred, Phases 1–2 kept because independent testability pays for them, P4.1 pulled forward, Phase 3 narrowed to the four rows that bite on loom's own devices. Full record and the four conditions that would reopen it: §0.6.1. |
| **D17** | Which name, concretely? *(the instance D7 left open)* | **`github.com/SukramJ/go-fabric`** | A *fabric* is Matter's own term for the trust domain a controller and its devices share — specification vocabulary, not a mark, and already 85 occurrences in `secure/operational/manager.go`. It reads as Matter to anyone who knows the protocol and as weaving to anyone who does not, which keeps the family tie to `loom`. House form matches `go-mqtt` and `go-openccu-data`. Discoverability rides on the description, topics and README, which Apache-2.0 §6 explicitly permits as *"reasonable and customary use in describing the origin of the Work"* — the module path itself stays free of the mark. Packages become `go-fabric/contract`, `go-fabric/cluster/...`, `go-fabric/bridge`. |
| **D14** | Version lane? | **Independent, SemVer** | `go-mqtt` already demonstrates the pattern (`docs/adr/0050:61-75`). A Matter fix must reach a consumer without a daemon release — that is the point of the extraction. Without its own lane the library is published in form only and still chained to the daemon's cadence. |
| **D15** | Does the endpoint key travel? *(raised by the goal alignment; narrows D4)* | **No — endpoint identity becomes a port** | `EndpointKey{CentralName, DeviceAddress, ChannelNo, DPKind, DPKey}` is host vocabulary: `DPKind ∈ {custom, generic, calculated, combined, measurement}` is loom's datapoint taxonomy, and channels do not exist in Home Assistant. Letting it travel would oblige a `go-matter-bridge` to populate fields that mean nothing to it — **goal 3 violated in the module's own API**. So the nine protocol-generic tables travel as D4 decided; `matter_endpoints` and `matter_exposures` stay host-side behind an interface, and the library holds an opaque key (string / `Stringer`). loom renders its 5-tuple to **exactly today's string**, pinned by a golden test, so migration 007's re-pair warning stays inert. Fits D5, which already puts allowlist policy host-side. |
| **D16** | How is goal 1 held through P3.2? | **A recorded wire comparison gates the switch-over, and P3.2 is cut into three** | The criteria were purely structural — a `grep` going empty cannot observe a behaviour change. The gate becomes a captured Read / Subscribe / Invoke sequence against fixed device fixtures, byte-identical before and after, following `tests/contract/wire_snapshots`. **The fixtures must exercise the measured gaps** (HS and XY colour, tilt), or the comparison cannot see the regression it exists to catch. Measuring the target servers then showed the milestone was mis-shaped — they return Success without forwarding (R14) — so P3.2 became **P3.2a** raise, **P3.2b** classify, **P3.2c** switch. |

### 7.1 What the decisions changed in this document

* **§4's `Gated by` column became `Decision`** — it now records the shape each milestone was decided into. Nothing waits on an answer.
* **P4.1 and P4.2 merge** (D13). The reference daemon *is* the example app; R6's "cheapest proxy for a second caller" and R8's commissioner guard become one deliverable.
* **P4.11 changes character** (D9). No longer "exists only if D9 reverses `SPECIFICATION.md:175`", but incremental adoption of `Test_TC_*` cases against the reference daemon, with certification remaining a non-goal.
* **P0.8 gains the NOTICE** (D6) — reproduce `../matter.js/NOTICE`, and name the *embedded* `internal/north/matter/parity/schema.json`, which is the file the current notices document does not name.
* **R2 is resolved** (D7); **R4 is narrowed** (D9).
* **Non-goals 1, 3, 5 and 7 are confirmed** rather than provisional. Non-goal 7 is settled by D8 (clean-sheet), not "D14-adjacent" as it previously read.

**Raised later the same day, by the goal alignment (§0.6):**

* **D15 narrows D4** — `matter_endpoints` and `matter_exposures` stay host-side; C14 and §5.3 are rewritten accordingly, and P2.4 gains a byte-identity criterion.
* **D16 adds a behaviour gate to P3.2 and re-cuts it** — non-goal 8 previously covered Phases 0–2 only, leaving the plan's largest move without a criterion that could fail. Measuring the move's *target* then showed it was not a move at all: P3.2 is now P3.2a (raise the library servers to production capability), P3.2b (classify each of the nine files as cluster logic, host policy or host binding) and P3.2c (switch over against the wire comparison).

**Open decisions remaining: none.** D17 named the module, D18 settled the goal question and D19 the shape of go-fabric, all on 2026-09-05. A question that arises during the work becomes a new dated row here. A question that arises during the work becomes a new dated row here.

---

## 8. Register of things this document could not measure

Stated as **UNVERIFIED** in those words, with what is missing named.

* **The 38-of-120 bridge-reachable cluster figure.** The 51 `MatterClusterID` implementations and the 14 inside `internal/model/` are measured. The cross-join against matter.js's device-type requirement blocks is the coverage survey's, not re-run here. **UNVERIFIED by me** (§0.3).
* **The godoc completeness figures** in §2.6 (37 packages with a package doc; 1,364 documented vs 51 undocumented exported declarations). The awk sweep was not re-run and §9 carries no command for it. **UNVERIFIED.**
* **Whether `bridge/` splits internally** along protocol vs. model-walking. Its 11,549 LOC were narrowed to two host-coupled files by a reproduced per-file scan, but the remaining 11,447 were not partitioned by *concern*. **UNVERIFIED.**
* **Whether `secure/attestation` accepts certificate bytes or only file paths.** Only `config.NorthMatterAttestation` (`internal/config/config.go:1006`, "All four files are PEM- or DER-encoded") was read; the loader was not. **UNVERIFIED.**
* **Per-cluster command completeness of any server against matter.js.** No per-cluster read sweep was run, and the snapshot's command table is lossy (§3.3) so it cannot answer the question either. **Not performable from the current source** — the extractor (P0.7) must be fixed first.
* **Whether every attribute a server lists in `MatterAttributes()` is actually served by its `MatterRead`** (i.e. no over-advertising). **Not measured.**
* **Actual test-coverage percentages** for the Matter packages — `make coverage` was not run (read-only session). **UNVERIFIED.**
* **Whether the chiptool suite's 90 tests currently pass.** Not run (macOS host cannot). **UNVERIFIED.**
* **Whether any manual controller smoke run was ever executed.** The plan says to look in `CHANGELOG.md`; `grep -c "manually verified" CHANGELOG.md` → 0, so there is no record to check. **UNVERIFIED.**
* **Whether `valve.Irrigation`'s OnOff endpoint appears on a live bridge.** The code path was verified structurally; the allowlist could still keep it off every real installation. **UNVERIFIED end-to-end.**
* **Whether serving cluster 0x0081 is what the HA `valve` domain requires end-to-end.** That mapping was not traced. **UNVERIFIED.**
* **Whether the 11 migrations can be renumbered without breaking existing installs.** The `IF NOT EXISTS` absence is measured; the `goose.SetTableName` adoption path was not exercised. **UNVERIFIED.**
* **Whether loom re-checks NOC validity periodically** (as opposed to at each CASE handshake) — no expiry timer was measured. **UNVERIFIED.**
* **Whether the 1100-byte chunking budget** (`bridge/reply.go:293`) is correct for a TCP or large-payload transport, since neither exists. **UNVERIFIED.**
* **Whether a published library can carry the scenario-corpus regeneration path** (43 files under `notes/parity/matter/scenarios/`, sidecars regenerated by a Node script requiring an npm-installed `../matter.js`), or must ship frozen fixtures. **UNVERIFIED.**
* **Whether the CSA trademark clause has ever been considered in this repo** — no document addresses it. **UNVERIFIED.**
* **`custom/switch`'s `MatterWrite` OnOff branch is dead code.** Found while pinning the delivered command priority: `OnOff.OnOff` is read-only in the Matter schema, so `internal/north/matter/endpoint/dispatcher.go:252` returns `UnsupportedWrite` before any cluster server is reached. The branch therefore cannot execute through the real dispatcher. Pre-existing and untouched by Phase 0; **UNVERIFIED** whether any other path reaches it, and whether the branch should be deleted or the gate widened.
* **`eligibility/compat.go:107,118,127` gate findings on device types loom never emits** (0x0042 WaterValve, 0x0043 WaterLeakDetector). Those branches look unreachable in production. Found while verifying P1.5; **UNVERIFIED** as a defect — the compatibility reporter may be intended to cover types a *future* host emits, in which case they are dormant rather than dead.
* **Housekeeping found in passing, not a roadmap item:** `internal/north/matter/secure/operational/manager_test.go:1194` cites `[interfaces.OperationalSessionLookup]`; that symbol does not exist in `pkg/interfaces` — the type is `bridge.OperationalSessionLookup` (`internal/north/matter/bridge/handlers.go:723`). Candidate for the next comment-claims sweep.

---

## 9. Appendix — measurement commands

Every number in §0–§3 is reproducible from the repo root.

```sh
# repo state at the time of writing — the tree was DIRTY
git log --oneline -1                                                          # 9d3f2f21
git status --short | wc -l                                                    # 157
git status --short | awk '{print $1}' | sort | uniq -c                        # 53 ??, 104 M

# subtree census
find internal/north/matter -name '*.go' ! -name '*_test.go' | wc -l          # 164
find internal/north/matter -name '*.go' ! -name '*_test.go' -exec cat {} + | wc -l   # 52061
find internal/north/matter -name '*_test.go' | wc -l                          # 301

# host coupling, per package — three distinct closures, not one
for p in bridge endpoint eligibility; do
  echo -n "$p "
  go list -deps ./internal/north/matter/$p | grep SukramJ/openccu-loom \
    | grep -v north/matter | wc -l
done                                    # bridge 20 / endpoint 18 / eligibility 17

# the Phase 1 gate (P1.6)
go list -deps ./internal/north/matter/... | grep openccu-loom \
  | grep -v internal/north/matter        # must become EMPTY

# per-FILE host coupling — the measurement that flips the verdict
for f in internal/north/matter/bridge/*.go internal/north/matter/endpoint/*.go; do
  case "$f" in *_test.go) continue;; esac
  grep -q 'openccu-loom/internal/\(model\|health\|i18n\|netutil\)' "$f" && echo "HOST $f"
done                                    # 2 of 21 in bridge, 3 of 7 in endpoint
for f in internal/north/matter/eligibility/*.go; do
  echo -n "$f "; grep -c 'openccu-loom' "$f"
done                                    # compat 0, eligibility 5, vendor_name 0

# the nine model packages that reference the Matter ports (C1 / §0.5-2)
grep -rln 'interfaces\.Matter' --include='*.go' internal/model/ | grep -v _test \
  | sed 's|/[^/]*$||' | sort -u                                               # 9 packages

# cluster servers, device-type sources, priority reach
grep -rn "func ([^)]*) MatterClusterID() uint32" --include='*.go' . | grep -v _test.go | wc -l   # 51
grep -rn "func ([^)]*) MatterClusterID() uint32" --include='*.go' internal/model/ | grep -v _test.go | wc -l  # 14
grep -rn "func ([^)]*) MatterWrite(\|func ([^)]*) MatterInvoke(" --include='*.go' . \
  | grep -v _test.go | grep -v worktrees | wc -l                                # 104
grep -rn "func ([^)]*) MatterDeviceType() uint16" --include='*.go' internal/model/ \
  | grep -v _test                                                               # 13 producers
grep -n "DeviceType:" internal/north/matter/endpoint/*.go | grep -v _test       # 9 lines, 6 Endpoint literals
grep -c "^\t0x" internal/north/matter/schema/devicetypes.go                     # 273

# the two unclassified-DP drop sites (P3.3)
grep -n 'MatterMeasurementNone' internal/north/matter/endpoint/assembler.go     # 417, 524

# P3.2 effort anchor — the five clusters' current home
wc -l internal/model/custom/light/matter.go internal/model/custom/light/matter_color.go \
      internal/model/custom/light/matter_timed_onoff.go internal/model/custom/cover/matter.go \
      internal/model/custom/cover/matter_debounce.go internal/model/custom/climate/matter.go \
      internal/model/custom/switch/matter.go internal/model/custom/siren/matter.go \
      internal/model/generic/switch_matter.go                                   # 5796 total

# the loom device-profile fleet (§3.1 closing note)
grep -o 'hmenum.DeviceProfile("[A-Za-z_]*")' internal/model/custom/profiles.go | sort -u | wc -l  # 31

# the persistence trap
ls internal/store/sqlite/migrations/ | grep -i matter                           # 11 files
grep -c 'IF NOT EXISTS' internal/store/sqlite/migrations/*matter*.sql           # 0 for all eleven

# the lossy extractor and the constant defect
sed -n '126,132p' notes/parity/matter/extract-from-matter-js.ts
sed -n '20,30p'  internal/north/matter/schema/clusters.go                       # 0x002B at :25

# the silently-empty CI leg and the missing notice
grep -rn Fuzz internal/north/matter/tlv/                                        # no match
grep -c "manually verified" CHANGELOG.md                                        # 0
grep -n nistec THIRD-PARTY-NOTICES.md                                           # no match (P0.8)
wc -c internal/north/matter/parity/schema.json                                  # 516843

# Home Assistant baseline
git -C ../core log -1 --format='%H %cI'
grep -n 'MAJOR_VERSION\|MINOR_VERSION\|PATCH_VERSION' ../core/homeassistant/const.py  # 2026.9.0
grep -c '^    [A-Z_0-9]* = "' ../core/homeassistant/generated/entity_platforms.py     # 45

# hamb baseline
sed -n '76,104p' ../home-assistant-matter-bridge/packages/backend/src/matter/endpoints/legacy/create-legacy-endpoint-type.ts
sed -n '22,44p'  ../home-assistant-matter-bridge/packages/backend/src/matter/endpoints/legacy/binary-sensor/index.ts
sed -n '24,53p'  ../home-assistant-matter-bridge/packages/backend/src/matter/endpoints/legacy/sensor/index.ts
sed -n '53,55p'  ../home-assistant-matter-bridge/packages/backend/src/matter/endpoints/legacy/media-player/index.ts

# matter.js baseline
git -C ../matter.js log -1 --format='%H %cI %s'
ls ../matter.js/packages/node/src/behaviors/*/*Server.ts | wc -l                # 135
grep -l 'THIS FILE WILL BE REGENERATED' ../matter.js/packages/node/src/behaviors/*/*Server.ts | wc -l  # 82
wc -l ../matter.js/packages/node/src/behaviors/media-input/MediaInputServer.ts  # 14
git -C ../matter.js rev-list --count c6b188fe00bc7d1b97fe22c63cd3a553ad8efd8f..HEAD   # 59
sed -n '25,40p' ../matter.js/packages/model/src/standard/elements/valve-configuration-and-control.element.ts
```