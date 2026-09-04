# Extracting the Matter Implementation into a Reusable Go Module

**Working document — `notes/plans/matter-library-extraction.md`**
Status: proposal, not a decision. Every claim about the current code carries a `path:line` or verbatim command output. Claims that were adversarially re-checked are stated plainly; everything else is marked **UNVERIFIED** in those words, and §8 collects them so nothing hides in prose.

Target this roadmap works backwards from: **a Go module that can replace `home-assistant-matter-bridge` (hamb) — i.e. every Home Assistant entity domain hamb bridges today, bridged from Go, at matter.js-level cluster fidelity.** Milestones are ordered by how many HA domains each unlocks, not by how tidy the refactor is.

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

* **Extraction** (Phases 0–2): mechanical once the decisions in §7 are answered. Three of the fourteen open decisions gate Phase 0 and Phase 1 *directly*, so "blocked on nothing but sequencing" would be wrong — see the `Gated by` column in §4.
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

## 1. Effort scale, and where it comes from

Effort labels are **estimates, not measurements** — no effort figure in this document was read from any source. But the scale is anchored on measured quantities so a reader can check the schedule rather than take it on faith.

| Label | Definition | Anchor measured in this repo |
|---|---|---|
| **S** | ≤ 1 day. A single-file or two-file mechanical edit with no new logic and no signature change. | P0.3 (alias flip) touches exactly two files: `cluster/dataversion.go` is the **only** non-test file in `internal/north/matter/cluster/` importing `pkg/hmtypes`. |
| **M** | 2–5 days. Either a change touching ≤ ~10 files, or **one new cluster server of ≤ ~400 LOC whose shape is already demonstrated in-repo**. | loom's own value-port servers: `cluster/light/colorcontrol_server.go` **246**, `cluster/cover/windowcovering_server.go` **237**, `cluster/lock/doorlock_server.go` **321**, `cluster/closure/closurecontrol_server.go` **392** (`wc -l`). |
| **L** | 2–4 weeks. A change that alters a package's public type surface plus every caller, **or** a cluster server ≥ ~600 LOC, **or** one carrying per-fabric persisted state. | `cluster/thermo/thermostat_server.go` **583**; matter.js `ScenesManagementServer.ts` **1109**, the second-largest server in that tree. P0.2 also lands here on caller count: **104** non-test `MatterWrite`/`MatterInvoke` implementations. |
| **XL** | ≥ 1 month. A redesign spanning ≥ ~5,000 LOC of existing production code, or a new subsystem with no in-repo precedent. | **P3.2 derivation:** the five clusters it moves live in **5,796 non-test LOC across nine files** — `light/matter.go` 1003, `light/matter_color.go` 950, `light/matter_timed_onoff.go` 361, `cover/matter.go` 918, `cover/matter_debounce.go` 249, `climate/matter.go` 905, `switch/matter.go` 415, `siren/matter.go` 617, `generic/switch_matter.go` 378 (`wc -l`, each file re-measured). Three of those files also carry SmokeCOAlarm and ThermostatUI, so 5,796 is an upper bound on the code touched, not a lower bound on the code moved. |

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
| C14 | Endpoint identity is a Homematic 5-tuple pinned by a SQL CHECK | `store/endpoints.go:18-37` `EndpointKey{CentralName, DeviceAddress, ChannelNo, DPKind, DPKey}`; `migrations/007_matter_endpoints.sql:15-26` | D | **The encoding must not change.** Migration 007's own Down comment records the consequence: reassignment "desyncs every commissioned controller's cached accessory list until each bridged device is removed and re-added." `store.EndpointKey` travels to the library unchanged despite its Homematic flavour. Partially mitigated already: `materialize.go` `uniqueIDFor` takes `key any` with a `Stringer` fallback. |
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

Effort per §1. The **Gated by** column names the §7 decision each milestone needs before it can start; `—` means genuinely unblocked.

### Phase 0 — Extraction prep, in loom, in place (nothing moves)

**Phase 0 is not unblocked.** Three §7 decisions gate it: **D1** shapes what the port package contains, **D3** *is* P0.2, and **D2** decides whether P1.2 and P1.4 have a target shape at all. P0.4, P0.6, P0.7 and P0.8 are the only genuinely decision-free items.

| # | Milestone | Acceptance criterion | Effort | Gated by | Depends on |
|---|---|---|---|---|---|
| **P0.1** | Split the Matter ports out of `pkg/interfaces` into a second neutral `pkg/` package, leaving type aliases behind | `go list -deps ./internal/north/matter/cluster/core \| grep hmapi` → empty; all 51 `MatterClusterID` implementations compile unchanged; `make contract` green | M | **D1** (option (b) requires the package to also publish `wire` payload structs and `im` status codes) | — |
| **P0.2** | Remove `hmenum.CommandPriority` from `MatterWrite`/`MatterInvoke` | `grep -rn 'hmenum' pkg/<newport>/*.go` → no match; both dispatcher call sites (`dispatcher.go:319,595`) compile; the 104 non-test implementations compile; host mapping is an **explicit switch**, not an ordinal cast | L | **D3 — this milestone is decision 3** | P0.1 |
| **P0.3** | Invert the `DataVersionTracker` alias — **one atomic commit** | `grep -rln '"…/pkg/hmtypes"' internal/north/matter/cluster/ \| grep -v _test` → empty; the nine model embedders untouched | S | — | P0.1 |
| **P0.4** | `netutil` → `InterfaceFilter` on the advertiser config (+ a shared-behaviour test against loom's client-discovery advertiser); `health.Sample` → library-local struct; `i18n` → `ChannelLabel string` on `endpoint.Config` | `go list -deps ./internal/north/matter/mdns` and `…/bridge` list no `internal/netutil`, `internal/health`, `internal/i18n` | S | — | — |
| **P0.5** | Move `MatterSwitchEventEmitter` into the shared port package; drop the `*generic.ButtonGroup` pin | A **behavioural** wiring pin under `tests/contract/wiring_pins/` asserts a press event reaches a commissioner through the production constructor — not a compile-time `var _`. Bite proof: revert the emitter's package and observe red | S | **D1** | P0.1 |
| **P0.6** | Convert the three relative-path tests to `go:embed` **before** any move | `bridge/scenario_test.go:16`, `im/wire_fixtures_parity_test.go:47` (also strip the absolute developer path at `:57-58,349-350`), `parity/snapshot_identity_test.go:37-39` — pattern already in use at `tlv/parity_matterjs_test.go:31`; `go test ./internal/north/matter/{bridge,im,parity}/...` green from any cwd | M | — | — |
| **P0.7** | Fix the snapshot extractor's `${tag}:${id}` collapse | Regenerated snapshot carries 10 Command children for Groups (matching `../matter.js/…/groups.element.ts` lines 34,43,51,62,70,74,83,89,96,105); a new guard asserts `schema.DeviceTypeAllowsServerCluster(0x0016, 0x0038)` | S | — | — |
| **P0.8** | Fix loom's own notices before anything moves: add `filippo.io/nistec` (`go.mod:37`, used by `secure/spake2/spake2.go:21`), correct `docs/adr/0012:473`'s zeroconf licence to MIT (`THIRD-PARTY-NOTICES.md:176` already says MIT), and name the embedded `parity/schema.json` in the matter.js entry | `grep -n nistec THIRD-PARTY-NOTICES.md` → a match (today: no match); `grep -n 'parity/schema.json' THIRD-PARTY-NOTICES.md` → a match | S | — | — |

### Phase 1 — Split the model-walking layer (0 new domains, unlocks a second host)

| # | Milestone | Acceptance criterion | Effort | Gated by | Depends on |
|---|---|---|---|---|---|
| **P1.1** | Sever `internal/model/device` from `endpoint/types.go`: remove `Devices` (`:28`), flatten `BridgedDevice` (`:88`) to plain fields, remove `Channel` (`:93`) | `go list -deps ./internal/north/matter/endpoint` lists **zero** `internal/model/*` (today: 18 host packages); the three production readers — `materialize.go:145`, `:194`, **and `cmd/openccu-loom/daemon_matter.go:4057`** — compile against the flat fields | L | — | P0.* |
| **P1.2** | Move `assembler.go` + `friendlyName`/`parameterSuffix`/`isNotFound` to a host package; keep `truncateUTF8`/`measurementDeviceType`/`deviceTypeRevision`/`customDeviceTypeRevision` library-side | `go build ./...`; endpoint IDs byte-identical before/after on a fixture snapshot (a diff of `matter_endpoints` rows) | L | **D2** | P1.1 |
| **P1.3** | Split `eligibility/`: `eligibility.go` host-side, `compat.go` + `vendor_name.go` library-side | `go list -deps ./internal/north/matter/eligibility` → empty (today: 17 host packages for 530 LOC); the six consumers (`matter_status_adapter.go:198-207`, `matter_event_publisher.go:131`, `daemon_matter.go:4113,4137`, `handlers/matter.go:279`, `handlers/matter_exposures.go:37`) compile | M | **D5** | P1.1 |
| **P1.4** | Define `EndpointSpec` as the library's assembly input and a `NameResolver` port | A table test builds a three-tier topology from hand-written `EndpointSpec`s with no `internal/model` import | L | **D2** | P1.1 |
| **P1.5** | Fix `MatterDeviceTypeName` — generate the table from the schema snapshot instead of hand-curating | A test asserts every emitted device type has a name. Today 0x0230 and 0x0510 fall to the hex default at `pkg/interfaces/matter.go:575` while never-emitted 0x0043 sits in the table at `:545`, against the doc claim at `:521-525` | S | — | P0.1 |
| **P1.6** | **Gate:** the whole subtree is host-free | `go list -deps ./internal/north/matter/... \| grep openccu-loom \| grep -v internal/north/matter` → **empty**. This single command is the go/no-go for Phase 2 | — | — | P0.*, P1.1–P1.5 |

### Phase 2 — Publish (0 new domains, makes everything else observable)

| # | Milestone | Acceptance criterion | Effort | Gated by | Depends on |
|---|---|---|---|---|---|
| **P2.1** | Create the module: package names kept verbatim minus the `internal/north/matter/` prefix, so every intra-subtree rewrite is a pure prefix substitution; new `port` and `ecosystem` packages; `go.mod` requiring the five modules of §2.6 | `go build ./...` in the new repo; `go test ./...` green standalone | L | **D6, D7** | P1.6 |
| **P2.2** | One loom commit: delete the subtree, rewrite ~100 consumer import paths, add the `require` at a pseudo-version | `make test && make contract && make lint` green on the same commit; **no filesystem `replace`** (loom CI checks out one repo, `.github/workflows/ci.yml`; `ls go.work*` → no matches today) | M | — | P2.1 |
| **P2.3** | Relocate the schema pipeline: extractor, snapshot, generator, embed. `Makefile:496-514` and the four hard-coded paths in `script/generate_matter_schema.go:35-39` re-pointed **in the same commit** | `make generate-matter-schema` reproduces `schema/{clusters.go,devicetypes.go,schema_provenance_gen.go}` byte-identically (snapshot 516,843 bytes, `sourceCommit` unchanged); the six model-side `parity_matterjs_test.go` files still call `parity.SchemaJSON()` through the module | M | — | P2.1 |
| **P2.4** | Persistence decision implemented — see §5.3, the hard rule | A fresh library-only DB passes the store test suite; an existing loom DB migrates with **zero DDL change** | L | **D4** | P2.1 |
| **P2.5** | Replace the lost guards | `tests/contract/matter_schema_sync_test.go:26-40` compares two loom paths and becomes meaningless — a library-side stale-embed guard must exist; `tests/contract/test_shard_script_test.go` passes with re-tuned shards (`script/test_shard.sh:22` states the subtree "alone is a third of the tree"); `script/reachability/main.go:571,626,641` key on the literal path and go dead; `notes/parity/dead-code-inventory.json` (6,144 lines matching `north/matter`) rebaselined | M | — | P2.2 |
| **P2.6** | Library `LICENSE` (MIT), `THIRD-PARTY-NOTICES` correct where loom's is not, license-header guard | `filippo.io/nistec` listed; zeroconf recorded as **MIT**; the embedded snapshot named explicitly; a license-header guard test exists (`grep -rn 'SPDX-License-Identifier: MIT"' tests/` → no match today, so none is inherited) | M | **D6, D7** | P2.1, P0.8 |
| **P2.7** | Independent module version + release lane | A Matter fix reaches a consumer without a daemon release. Today `internal/build/version.go:22` is the single version carrier and `.claude/skills/release/SKILL.md:20` enumerates five carriers, none a module | M | **D14** | P2.2 |

### Phase 3 — Domains, ordered by unlock count

| # | Milestone | HA domains unlocked | Acceptance criterion | Effort | Gated by | Depends on |
|---|---|---|---|---|---|---|
| **P3.1** | **Real Groups (0x0004) + ScenesManagement (0x0062) servers** | 0 new — repairs conformance on **11** already-claimed domains | `AcceptedCommandList` on a bridged OnOff endpoint returns all six Groups commands and all seven Scenes commands; a chip-tool `groups add-group` / `scenesmanagement recall-scene` round-trip succeeds against the reference slot. **A conformance guard built on the snapshot before P0.7 lands reports both green** | L | — | **P0.7** |
| **P3.2** | **Move OnOff, LevelControl, ColorControl, WindowCovering, Thermostat into the library** behind narrow value ports, using the `lock.StateSource` / `light.ColorTemperatureWriter` shape | 13 domains, **for a second host** | The 14 `MatterClusterID` implementations under `internal/model/` reduce to value-port adapters; `grep -rn 'north/matter/cluster/wire' internal/model/` → empty; the three currently-unused generic servers (`cluster/{light,cover,thermo}`) gain production callers. **XL derivation: 5,796 non-test LOC across nine files (§1)** | XL | **D1** | P2.2 |
| **P3.3** | **OnOffSensor 0x0850 fallback + open the measurement registry** | `binary_sensor` fully; `sensor` breadth | A host can register a new measurement kind without editing the library; the closed enum at `pkg/interfaces/matter.go:238-255` and its four switches (`:469`, `:533`, `:581`, `cluster/measurement/measurement.go:887`) are no longer the extension point; an unclassified boolean DP produces a 0x0850 endpoint instead of being dropped at **both** `assembler.go:417` **and** `:524` | L | **D2** | P1.4 |
| **P3.4** | **ModeSelect cluster 0x0050 + device type 0x0027** | **3** — `select`, `input_select`, `alarm_control_panel` | A chip-tool `modeselect read supported-modes` returns a host-supplied list; `ChangeToMode` reaches the host binding. Also the only honest carrier for read/write ENUM data points on the loom side | M | — | P3.2 |
| **P3.5** | **FanControl 0x0202 + Fan 0x002B** | **1** — `fan` (plus AirPurifier 0x002D, RoomAirConditioner 0x0072, MicrowaveOven 0x0079, ExtractorHood 0x007A) | Features MultiSpeed/Step/Auto/AirflowDirection selectable per endpoint, matching hamb's derivation at `…/fan/index.ts:23-43` | M | — | P3.2 |
| **P3.6** | **ValveConfigurationAndControl 0x0081 + WaterValve 0x0042** | **1** — `valve` | Mandatory attributes **OpenDuration 0x0, DefaultOpenDuration 0x1, RemainingDuration 0x3, CurrentState 0x4, TargetState 0x5** served; commands **Open 0x0 / Close 0x1** handled. **0x2 is `AutoCloseTime`, conformance `"TS"` — feature-gated, NOT mandatory.** Read verbatim from `../matter.js/packages/model/src/standard/elements/valve-configuration-and-control.element.ts:27-38,:60,:64`; a list taken off the lossy snapshot would not give this. **Does not change loom's own valves** — `docs/adr/0049…:47-50` decides 0x010A for `valve.Irrigation` | M | — | P3.2 |
| **P3.7** | **Speaker 0x0022 for media_player** | **1** — `media_player` | 0x0022 emitted with the OnOff + LevelControl servers it mandates (`schema/devicetypes.go:299-303`; `../matter.js/packages/node/src/devices/speaker.ts:64,76-79`). **No MediaPlayback server involved.** Residual loom-side gap is narrow: `siren.SoundPlayer` has no Matter projection, and re-labelling MP3P from DimmableLight to Speaker is a device-type change over servers that already exist. Plus **MediaInput 0x0507** served when the host reports a source list: hamb mounts it conditionally at `…/media-player/index.ts:53-55` (behavior at `…/media-player/behaviors/media-player-media-input-server.ts:4`), and matter.js's server is a 14-line generated stub, so this is S-effort and does **not** justify the XL Media-family label | M | **D8** | P3.2 |
| **P3.8** | **RVC family: 0x0054 + 0x0061 (+0x0060, 0x0055, 0x0150) + device type 0x0074** | **1** — `vacuum` (+ `lawn_mower` by extension) | Device pairs and responds to run-mode changes on at least one controller. Ecosystem acceptance is a **separate unmeasured gate** — hamb's own docs flag vacuum as controller-dependent | L | **D10** | P3.2 |
| **P3.9** | Sensor-cluster fill: FlowMeasurement 0x0404, SoilMeasurement 0x0430, seven concentration clusters, BooleanStateConfiguration 0x0080 | 0 new (all under `sensor`), fidelity + breadth | For each new cluster, a parity test asserts `schema.ClusterRevision(id)` matches the snapshot and every `conformance: "M"` attribute appears in `MatterAttributes()`; bite proof: hand-edit one revision and observe the test red. Each server reuses the parameterised `concentrationServer` shape (`cluster/measurement/measurement.go:1407-1470`) | S each | — | P3.3 |
| **P3.10** | Fidelity types: WaterLeakDetector 0x0043, WaterFreezeDetector 0x0041, DimmablePlugInUnit 0x010B, native WaterHeater 0x050F, root-endpoint optional 0x0034/0x0037/0x002B/0x002C/0x002D, Aggregator Actions 0x0025 | 0 new | Each device type is reachable only through an explicit host opt-in flag, and re-enabling 0x0043 is gated on a recorded Alexa pairing run — the ceiling written into `pkg/interfaces/matter.go:483-502` ("a single endpoint advertising 0x0043 renders the whole bridged node unresponsive") — not on a green unit test | M | **D11, D12** | P3.3 |

### Phase 4 — Library-grade quality

| # | Milestone | Acceptance criterion | Effort | Gated by | Depends on |
|---|---|---|---|---|---|
| **P4.1** | Runnable examples | ≥1 `examples/` app pairing a fake host to a controller **without importing anything the README does not document**. Today: `grep -rn "^func Example" --include='*_test.go' internal/ pkg/` → **0**, and no `examples/` directory. matter.js ships 21 (`ls -d ../matter.js/examples/*/ \| wc -l`), including the bridge-shaped `device-bridge-onoff`. **An example that cannot be written without reaching into internals is the honest test that the seam is wrong.** | M | — | P2.1 |
| **P4.2** | A minimal reference daemon in the library, for chip-tool | A green chip-tool pairing run against the reference daemon in the library's own CI. Without it the library ships with **no real-commissioner guard** — `tests/chiptool/` (7,706 LOC) execs `./bin/openccu-loom` (`Makefile:222-228`) and cannot move | XL | **D13** | P4.1 |
| **P4.3** | Downstream-usable test harness | A consumer can regression-test its own cluster servers. Today every harness is `_test.go`-private or `//go:build chiptool`. matter.js publishes `packages/testing/` | L | — | P4.1 |
| **P4.4** | Path-filter the chiptool trigger on `internal/north/matter/**` | `.github/workflows/chiptool.yml:58-61` carries a `paths:` filter on `internal/north/matter/**`; bite proof: a one-line change under that path opens a PR **without** the `needs-chiptool` label and the chiptool job runs | M | — | — |
| **P4.5** | Close the fuzz gap | `Makefile:365` lists `./internal/north/matter/tlv/...` but `grep -rn Fuzz internal/north/matter/tlv/` → **no match** (reproduced), so that leg silently runs nothing. Add TLV, `transport/message`, `secure/channel`, `secure/mattercert` (535 lines of DER), `secure/setup` — the last two parse attacker-supplied bytes. Commit seed corpora. Today the only Matter fuzz targets are four in `im/fuzz_invoke_test.go:18,33,45,56` | M | — | — |
| **P4.6** | Coverage floor + benchmarks for the subtree | `script/coverage_per_package.sh` names **no** `internal/north/matter/*` package and sets `FLOOR=0` for anything unlisted (`:94`); `grep -rn Benchmark internal/north/matter/` → **no match**. Note the subtree is heavily tested in absolute terms (301 test files, 95,771 test LOC vs 52,061 non-test) — this is a missing ratchet, not missing tests | M | — | P2.2 |
| **P4.7** | Fix the two evidence-chain claims | Either start recording the manual controller runs in `CHANGELOG.md`, or delete the claims at `notes/reference/matter-conformance.md:54-55` and `:63-64` (§3.4) | S | — | — |
| **P4.8** | Matter-specific threat model | A `notes/audits/matter-threat-model.md` exists covering PASE passcode brute-force budget, fail-safe abuse, fabric isolation and group-key handling, and each of the 16 `//nolint` directives under `internal/north/matter/secure/` carries a one-line justification a reviewer signed off. Context: `docs/SECURITY.md` is daemon-scoped (Matter is one asset row at `:20`, one danger note at `:71-72`), there is no root `SECURITY.md`, no crypto review among the 19 files under `notes/audits/`, CodeQL is explicitly non-blocking (`.github/workflows/codeql.yml:4-6`), and 130 `//nolint` sit in non-test Matter code | M | — | P2.1 |
| **P4.9** | Go-module API-stability + deprecation policy | The 6 Matter WS broadcasts in `assets/wsapi.json` carry `payload` schema references (today recorded TBD *"pending Matter-surface stabilisation"*, `docs/adr/0020:140`), and a named deprecation window appears in the module README. Context: the **REST** surface is already pinned (`tests/contract/testdata/api_surface.json` + `TestAPISurfaceChangesCarryTheRightBump`, `tests/contract/api_surface_bump_test.go:91`, against `internal/north/rest/handlers/info.go:13-19`); ADR 0050:61-75 has the right shape but is scoped to `go-mqtt` | S | **D14** | P2.1 |
| **P4.10** | Widen the CI matrix + add an upstream-freshness signal | The CI matrix builds and tests on at least one 32-bit GOARCH covering the shipped `goarm: ["7"]` target (`.goreleaser.yaml:29-31`), and a scheduled job fails when `git -C ../matter.js rev-list --count <sourceCommit>..HEAD` exceeds a declared threshold. Context: `GO_VERSION: "1.26.6"` hard-coded identically in all 10 workflow files; PR legs ubuntu + macos; Windows nightly-only and gates nothing (`nightly.yml:44-49`); armv7 is **shipped but never tested**; `tests/contract/matter_schema_sync_test.go:27-41` compares two in-repo copies and cannot detect upstream drift; the pin is **59 commits behind** | M | — | P2.2 |
| **P4.11** | CSA certification suite (PICS, `Test_TC_*`) | A `pics.properties` file exists and at least one `Test_TC_*` case runs green in the library's CI against the reference daemon. Repo-wide today: no PICS file anywhere (`find -iname '*pics*'` matches only MQTT *topics* files) and no cert test; matter.js runs `chip-cert-tests.yml` with a 178-line `matter-js-pics.properties`. Explicitly a non-goal today (`SPECIFICATION.md:175`) — **this milestone exists only if D9 reverses that** | XL | **D9** | P4.3 |

---

## 5. Risk register

| # | Risk | Evidence | Severity | Mitigation |
|---|---|---|---|---|
| **R1** | **matter.js NOTICE obligation on the embedded snapshot** | `internal/north/matter/parity/schema.json` is 516,843 bytes of machine-extracted `@matter/model`, `//go:embed`-ed at `parity/parity.go:18-20`. `../matter.js/NOTICE` final line, verbatim: *"This NOTICE must be included on all copies of matter.js."* loom hedges at `THIRD-PARTY-NOTICES.md:120-123` that the NOTICE "travels with the matter.js repository", and the hedge names `notes/parity/matter/matter-schema-snapshot.json` — **not** `internal/north/matter/parity/schema.json`, which is the copy embedded into every binary. The redistribution obligation attaches to the file the notices document does not name. `ls licenses/` shows only `Apache-2.0.txt` and `MIT.txt`; there is no `NOTICE` at the repo root | **High** | P0.8 names the embedded file; P2.6 decides the obligation explicitly rather than inheriting the hedge. Options: embed + carry matter.js's LICENSE and NOTICE; regenerate at build time from a consumer-supplied checkout; or freeze it host-side and take the tables as data. Whether the Go port is a Derivative Work is a legal question this document cannot resolve |
| **R2** | **CSA trademark clause vs. the module name** | Same NOTICE: *"Only the Alliance and its members may use Alliance trademarks and logos, including, without limitation, the Matter trademarks and logos"* | **High** | **UNVERIFIED** whether this has ever been considered in this repo — no document addresses it. Resolve before choosing a module path (D7) |
| **R3** | **Missing third-party notice** | `filippo.io/nistec` is a production dependency (`go.mod:37`, used by `secure/spake2/spake2.go:21`); `grep -n nistec THIRD-PARTY-NOTICES.md` → no match. `docs/adr/0012:473` mis-states zeroconf as Apache-2.0 while `THIRD-PARTY-NOTICES.md:176` says MIT | Medium | Fix in loom (**P0.8**) and in the library's own notices (P2.6) |
| **R4** | **Certification** | `SPECIFICATION.md:175` declares "No full Matter certification" a non-goal; the binary ships the CSA **test** vendor block by default (`internal/config/config.go:972-975` → VID 0xFFF1, PID 0x8000) with the embedded CSA test PAA chain (`secure/attestation/testpaa.go`) | Medium | A published module that says "not certified, test VID by default" is honest and usable. Say it in the README rather than implying otherwise |
| **R5** | **matter.js drift** | Snapshot pinned at `sourceCommit c6b188fe…`, 2026-08-26; checkout HEAD `75633fa…`, 2026-09-03 — **59 commits behind**. Regeneration needs a *built* `../matter.js` and Node (`Makefile:496-514`) | Medium | P4.10. The existing sync guard compares two **in-repo** copies only and cannot detect upstream drift |
| **R6** | **Two consumers, one API** | Today the API would be designed against **one real caller**. hamb is TypeScript and cannot import Go interfaces, so the "second host" is currently hypothetical | **High** | P4.1's example app is the cheapest proxy for a second caller. Without it, every port shape is a guess dressed as a design. Do not tag v1 before P4.1 |
| **R7** | **API churn against a frozen shape** | Publishing before Phase 1 locks `hmenum.CommandPriority`, `endpoint.Snapshot`, the closed measurement enum and `FabricScopedReader`'s hidden precondition into a v1. Today P0.2 costs one scripted commit across 104 implementations; after publication it costs a major bump in every consumer | **High** if the phase order is inverted | The phase order **is** the mitigation |
| **R8** | **The library ships with no real-commissioner guard** | `tests/chiptool` (7,706 LOC) execs `./bin/openccu-loom` (`Makefile:222-228`) and cannot move; its CI is label-gated off the default PR path | **High** | P4.2 is not optional. Until it lands, the library's correctness rests on loom's binary |
| **R9** | **Migration renumbering breaks every existing install** | Reproduced: `grep -c 'IF NOT EXISTS' internal/store/sqlite/migrations/*matter*.sql` returns **0 for all eleven files**. `goose.SetTableName` exists (`goose v3.27.3 version.go:50`) but adopting a second sequence on a live DB needs an explicit baseline step | **Critical** | §5.3, hard rule |
| **R10** | **Endpoint-key change silently desyncs every commissioned controller** | `migrations/007_matter_endpoints.sql:15-26` pins the 5-tuple PRIMARY KEY and a `dp_kind` CHECK; its Down comment records that reassignment "desyncs every commissioned controller's cached accessory list until each bridged device is removed and re-added" | **Critical** | §5.3, hard rule. Any milestone that touches it needs a re-pair plan, not a migration |
| **R11** | **Silent-no-op class of bug during the move** | `bridge/bridge.go:847-858` records that a duplicated emitter interface never matches and no press event reaches a commissioner — a compile-clean, test-clean failure | Medium/High | P0.5 requires a *behavioural* wiring pin with a bite proof, never a compile-time `var _` |
| **R12** | **The vendored `netutil` predicate forks** from loom's client-discovery advertiser | C4 | Medium/Low | P0.4 adds a shared-behaviour test over the same input table in both repos |
| **R13** | **CI legs go quietly green** | `make fuzz` already walks `internal/north/matter/tlv`, which has **zero** Fuzz functions (reproduced: `grep -rn Fuzz internal/north/matter/tlv/` → no match), so the leg passes without running anything. chip-tool is label-gated. CodeQL "does not fail the build on alerts" (`.github/workflows/codeql.yml:4-6`) | **Certain / Medium** | P4.4, P4.5. **Every new CI leg needs a negative control before it counts** |
| **R14** | **Maintenance burden of dead public-shaped API** | `cluster/light` (246), `cluster/cover` (237), `cluster/thermo` (583) have **no** non-test importer outside the subtree; they are kept alive by parity tests while the production servers live in `internal/model/custom/*` | Medium | P3.2 resolves it by making them the production servers. Until then they are duplicates that parity tests keep green |
| **R15** | **Release lane** | `internal/build/version.go:22` is the single version carrier; `.claude/skills/release/SKILL.md:20` enumerates five carriers, none a module | Medium | P2.7; adopt the go-mqtt pattern (`docs/adr/0050:61-75`, exact pins, squash-only fan-out) plus a dependabot entry |

### 5.3 The persistence trap — a hard, non-negotiable rule

Two measured facts make SQL the one part of this extraction that must **not** be treated as portable, even though `store/store.go:21` takes nothing but `*sql.DB` and pulls zero host packages:

1. **None of the eleven `matter_*` migrations uses `IF NOT EXISTS`.** Reproduced verbatim, all eleven returning 0: `006_matter_persistence`, `007_matter_endpoints`, `008_matter_resumption`, `009_matter_exposures`, `010_matter_resumption_cats`, `011_matter_diagnostics`, `012_matter_fabric_root_cert`, `013_matter_metadata`, `018_matter_persistent_subscriptions`, `025_matter_settings`, `036_matter_next_endpoint_id`. A library migration set renumbered from 1 therefore fails on every existing install.
2. **`matter_endpoints`' PRIMARY KEY is the 5-tuple `(central_name, device_address, channel_no, dp_kind, dp_key)` with a `CHECK` on `dp_kind`** (`migrations/007_matter_endpoints.sql:15-26`), and that migration's own Down comment states reassignment *"desyncs every commissioned controller's cached accessory list until each bridged device is removed and re-added."*

**The rule (P2.4):**

* The library exports an **idempotent `EnsureSchema(ctx, db)`** with `CREATE TABLE IF NOT EXISTS` DDL copied verbatim, **for greenfield embedders only**.
* **loom keeps its 11 numbered migrations untouched and never calls it.**
* **`store.EndpointKey` (`store/endpoints.go:18-37`) travels to the library byte-for-byte**, Homematic-flavoured shape and all, with a golden test pinning the rendered key.

Extraction plans habitually treat SQL as the portable part. Here it is the part with a Critical failure mode on both sides.

---

## 6. Non-goals

Explicitly **out of scope**. Each is a deliberate boundary, not an omission.

1. **Controller / initiator role.** The crypto is role-complete (`sigma.NewInitiator`, `spake2.NewProver`) but has zero non-test callers, and nothing above it exists — no peer set, no outbound exchange manager, no client subscriptions. `secure/mattercert/` is decode+verify only. Adding it reshapes `im` and `secure/operational` substantially. (D11 can reverse this.)
2. **Thread and BLE/BTP.** BLE is unreachable in pure Go without a CGo-free peripheral stack, and this repo forbids CGo (`CLAUDE.md`, "No CGo"). Thread additionally needs OpenThread. This is a platform-binding problem, not a protocol one.
3. **CSA certification.** `SPECIFICATION.md:175`. P4.11 exists only if D9 reverses it.
4. **BDX / OTA.** Depends on TCP and a transfer layer neither project's bridge use case needs.
5. **Group-cast.** Currently rejected rather than unimplemented (`bridge/receive_dispatch.go:77-92`). Adding it means new session types, operational group-key derivation and IPv6 multicast membership. (D12.)
6. **Changing the persisted endpoint-key encoding.** §5.3 makes this a hard constraint, not a preference.
7. **Backwards-compatibility shims for hamb's numeric contracts.** hamb's illuminance conversion, humidity ×100 and battery ×2 scalings are wire-visible; whether to reproduce them bit-for-bit is D14-adjacent (D8/Q on numerics), not an assumed goal.
8. **No behaviour change to `openccu-loom` as a side effect of extraction.** Every Phase 0–2 milestone is behaviour-preserving; feature work lives in Phase 3.
9. **No `internal/model` redesign to please the library.** The rich-model / dumb-bridge split (ADR 0012) stands; the library adapts to it.
10. **The library does not own naming.** Daemon-owned entity naming is a standing rule; `endpoint/helpers.go:24-38` records why. The library takes a `FriendlyName` string.
11. **No `go.work` in CI.** Local dev may use one; the CI path is a pseudo-version `require`.
12. **Reproducing matter.js's feature-variance machinery** (`packages/model/src/logic/cluster-variance/`) or its runtime class generation (`packages/general/src/util/GeneratedClass.ts`). A Go port fixes feature sets per device profile at build time; an architectural choice, not a measurement.

---

## 7. Open decisions — questions for the maintainer

Each changes the shape of the work; none can be resolved by reading more code. The **Gates** column names the milestones that cannot start without an answer.

| # | Decision | Gates |
|---|---|---|
| **D1** | **Who owns the cluster servers?** Either (a) the library ships them and hosts implement narrow value ports (the `lock.StateSource` / `light.ColorTemperatureWriter` pattern), which deletes all 14 model-side implementations and the `model → north/matter` import edge; or (b) hosts keep writing cluster servers, which forces the library to publish `wire` payload structs and `im` status codes as public API forever. **Mutually exclusive; the answer determines the whole surface.** | P0.1, P0.5, P3.2 |
| **D2** | **Does the library keep an assembler at all,** or only topology + endpoint-ID persistence over a flat `[]EndpointSpec`? | P1.2, P1.4, P3.3 |
| **D3** | **Priority:** drop the parameter, a library-owned `Priority`, or carry urgency in `ctx`? Only "drop" is free of a compatibility story for the 104 existing implementations. | **P0.2** (the milestone *is* the decision) |
| **D4** | **Do the 11 `matter_*` migrations ship with the library** (which then owns table naming and version numbering), or does `Store` become an interface the host implements? §5.3 recommends `EnsureSchema` for greenfield only. | P2.4 |
| **D5** | **Does `eligibility/` belong in a Matter library at all?** It reads `hmenum.DataPointUsage` and prescribes this daemon's UI glyphs and REST route. **UNVERIFIED** whether any other consumer would want it. | P1.3 |
| **D6** | **Is the Go port a Derivative Work of matter.js** for Apache-2.0 §4(d) purposes, and does the embedded 516 KB snapshot ship with the module? | P2.1, P2.6 |
| **D7** | **Can a public Go module carry "matter" in its name** given the CSA trademark clause? | P2.1, P2.6 |
| **D8** | **Domain-compatible with hamb, or clean-sheet?** Should `water_heater` use Thermostat 0x0301 (hamb's choice, `…/water-heater/index.ts:47-55`) so existing controller pairings survive, or native WaterHeater 0x050F? Should power/energy ride hamb's OnOffPlugInUnit 0x010A or loom's ElectricalSensor 0x0510? Same question for reproducing hamb's numeric conversions bit-for-bit. Also: is `siren.SoundPlayer` meant to reach Matter at all? `docs/adr/0012:197` justifies its omission with *"No Speaker / audio-playback cluster in Matter 1.5.1"*, contradicted by loom's own table at `schema/devicetypes.go:27` (Speaker 0x0022 rev 1). | P3.7, Phase 3 ordering |
| **D9** | **Is CSA certification ever in scope?** `SPECIFICATION.md:175` says no. If that holds, PICS and `Test_TC_*` never get built and the module README must say so plainly. | P4.11 |
| **D10** | **Which controllers must accept which device types?** hamb only links vendor documentation and warns that media_player, vacuum and WaterLeakDetector are controller-dependent. **That gate, not Matter, decides what is worth building first**, and it is unmeasured here. | P3.8, Phase 3 ordering |
| **D11** | **Does `WaterLeakDetector 0x0043` get re-enabled,** and is `hamb`'s `binary_sensor` `safety` → WaterFreezeDetector 0x0041 a bug to fix in the port or a compatibility choice to preserve? Also: does the library support the controller/initiator role? | P3.10, Non-goal 1 |
| **D12** | **Are group sessions in scope,** or is per-fabric unicast a permanent constraint? Groups/ScenesManagement (P3.1) imply group addressing. | P3.1's scope, Non-goal 5 |
| **D13** | **Does the library get a minimal reference daemon** so a chip-tool suite can travel with it? | P4.2 |
| **D14** | **Independent module version lane, or lockstep with the daemon** — and what deprecation window? | P2.7, P4.9 |

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