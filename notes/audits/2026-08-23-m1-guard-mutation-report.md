# M1 — Mutation-testing the contract guards

**Scope**: 359 guards, each mutated at one named production line and run alone.
**Verdicts (from the eight batch tables in `reports/*.tsv`)**: `bites` 333 ·
`bites-weakly` 6 · `decorative` 17 · `unclear` 3. (The brief's summary line said
324 bites; the tables carry 333 unique rows with that verdict — the tables win.)

**Negative control for the pass itself**: 333 guards did go red, most with a
message naming the defect (`"wire calls do not match snapshot for Switch/TurnOn:
want SetValue STATE=true, got []"`). The method can produce a failing result, so
the 17 green ones are a measurement, not an artefact of a broken harness.

**Headline**: 4.7 % of the contract suite is theatre, and it is not randomly
distributed. Two thirds of it clusters in two places: the wiring-pin helper
`tests/contract/wiring_helpers.go` (4 pins, incl. both Matter credential pins),
and tests that re-type a production string/table into the test file (8 pins,
almost all north-bound MQTT/SPA contract). One decorative row is a production
defect wearing a guard's clothes (Lock/Siren replay-on-subscribe).

---


## Caller verification

A batch report is a claim. Three findings were re-checked by hand before this
document was committed, chosen for consequence rather than convenience:

- **`MustFindCallerInFile` ignores its own `calleePackage` argument** —
  confirmed by reading `tests/contract/wiring_helpers.go:114-131`: it searches
  `calleeIdent` occurrences and never consults the package. 46 pins call it.
  This is the single highest-leverage repair in this report.
- **The NOC-verification pin is satisfied by an unrelated identifier** —
  confirmed. `mattercert.NewVerifier` really is called three times in
  `cmd/openccu-loom/daemon_matter.go` (:345, :1267, :1405), so the invariant
  *holds*; but `spake2.NewVerifier` at :2749 — SPAKE2+ PASE, nothing to do with
  certificate chains — shares the identifier, so the pin would keep passing if
  all three real calls were deleted. The invariant is held and unguarded, which
  is not the same as broken.
- **The reported Lock/Siren production defect** — refuted, see §1.4.

Verdict tallies were recounted from the per-batch tables rather than the
agents' self-reported summaries: **359 guards, 333 bite, 6 bite weakly, 17
decorative, 3 unclear**, no duplicates and none missing. The workflow's own log
line said 324 bites; the tables are authoritative.

## 1. What is unprotected right now

Grouped by the invariant, not the file. "Residual" says whether some *other*
guard in this pass still bites for the same invariant.

### 1.1 Matter credential enforcement — nothing pins the daemon's cert path

**Invariant A: an operational certificate chain is verified before a NOC is
accepted.** The only pin is `TestPin_NewVerifier_CalledInDaemon`
(`tests/contract/wiring_pins/chip_noc_verify_test.go:17`). All three real call
sites — `cmd/openccu-loom/daemon_matter.go:345`, `:1267`, `:1405`
(`mattercert.NewVerifier(fabric.RootPublicKey, mattercert.SystemTime{})`) — were
redirected to a local stub. **Green.** The pin matches the bare identifier
`NewVerifier`, and the same file carries `spake2.NewVerifier(vc, nil, nil,
context)` at `daemon_matter.go:2749` — a PAKE verifier with nothing to do with
certificates. The guard has been satisfied by that unrelated call the whole time.
*Residual*: `TestPin_ValidateRCAC_SubjectVsIssuer` and `TestPin_AddNOC_LengthCap`
bite, so RCAC self-consistency and length limits are covered; the chain-signature
step at the daemon call sites is not.
*Cost if violated*: a NOC not signed by the fabric root is admitted — fabric
takeover, i.e. someone else's controller drives the bridge.

**Invariant B: a failed AddNOC leaves no ACL entries behind.**
`TestPin_RevertAddNOC_ACLCleanup`
(`tests/contract/wiring_pins/chip_revert_acl_cleanup_test.go:18`). Deleting the
protected line `internal/north/matter/cluster/core/operational_credentials.go:2083`
(`_ = o.store.ReplaceACL(ctx, fabricIndex, nil)`) left it **green**: the pin
searches the whole file for `ReplaceACL`, and the identifier also appears at
`:218` (interface declaration) and `:1593` (the *insertion* path). The one call
the pin exists for can be deleted and two unrelated occurrences keep it happy.
*Residual*: none.
*Cost if violated*: orphan ACL entries survive on a fabric index that is later
reused — a privilege leak, granted to whoever next occupies that index.

### 1.2 South-bound transport selection (CUxD)

**Invariant: a CUxD interface is wired to the BIN-RPC path, never the generic
XML-RPC one.** `TestPin_wireCUxDInterface_CalledInCCUWiring`
(`tests/contract/wiring_pins/ccu_wiring_test.go:66`). The whole branch at
`internal/central/adapter/ccu_wiring.go:706-712` was replaced with
`_ = wireCUxDInterface` — the function is never invoked and CUxD falls through
to generic wiring. **Green**: the helper only needs the identifier to appear
outside a dead func-literal, and because `internal/central/adapter` is a library
package the reachability half is skipped outright (`wiring_helpers.go:243-247`).
*Residual*: yes, and it matters — `TestCUxDUsesBINRPCBackend`,
`TestPin_CUxDWiring_RecordsAsBINRPC`, `TestPin_CUxDWiring_InstallsSessionHook`,
`TestPin_CUxDWiring_ForwardsToRecordSession` and `TestCUxDIsBINRPCOnly` all bite.
The invariant survives; this specific seam does not.

### 1.3 North-bound MQTT: what HA is told, and whether we listen

**Invariant: every `command_topic` we advertise in HA discovery is actually
subscribed.** `TestServiceDiscoveryShape_TopicSegmentContract`
(`tests/contract/service_discovery_shape_test.go:646`). Disabling the real
8-segment bucket-aware `Subscribe()` at
`internal/north/mqtt/command_subscriber.go:490` left it **green** — the test
never constructs a `CommandSubscriber`. It compares each generated topic against
`knownFilters`, a string slice typed by hand at the same file's `:703`.
*Residual*: none for the subscribe side. The sibling
`TestServiceDiscoveryShape_*_Bucket8Segment` tests bite, but they pin topic
*shape*, not that anything listens.
*Cost if violated*: every command from Home Assistant is silently dropped —
the classic "declared and published must be the same set" failure, inverted.

**Invariant: HA `value_template`s survive the JSON envelope.** Three guards, all
in `tests/contract/discovery_roundtrip_test.go`:
`TestDiscoveryRoundTrip_GuardEmptyEnvelope` (`:820`),
`TestDiscoveryRoundTrip_BoolCapitalisation` (`:856`),
`TestDiscoveryRoundTrip_Event_JSONPayloadNotBroken` (`:751`). Dropping `| lower`
from `internal/north/mqtt/discovery.go:49`
(`valueJSONValueLowerTemplate`) left all three **green**: each renders a *copy*
of the template typed into the test file. The `Event_JSONPayload` one has no
production reference at all — it renders two local literals through `renderJinja`.
The doc comment on the bool test claims it uses "the actual template in the
discovery payload"; it does not.
*Residual*: the 10 per-platform `TestDiscoveryRoundTrip_<Platform>` guards bite,
so a broken payload *structure* is caught; template text drift is not.
*Cost if violated*: HA sees `True` instead of `true` (every binary sensor stuck),
or `mqtt.event` payloads that fail to parse.

**Invariant: the ADR-0011 topic hierarchy is what `mqtt.TopicBuilder` produces.**
`TestMQTTTopicHierarchyShape` (`tests/contract/mqtt_topology_test.go:19`).
Replacing the body of `TopicBuilder.DeviceInfo` (`internal/north/mqtt/topics.go:197`)
with a garbage literal left it **green**. The test file's imports are
`os/path/filepath/strings/testing` — it deliberately does not import `mqtt` and
re-validates hand-typed sample strings against themselves.
*Residual*: `TestMQTTTopicSchemaDoc_{State,Command,BridgeHub,Discovery}Topics`
bite, so doc-vs-code drift is caught. Builder-vs-doc drift is not.

**Invariant: a security-plane attribute payload actually carries its
attributes.** `TestSecurityPlane_AttributePayloadsAreObjects`
(`tests/contract/security_mqtt_plane_test.go:336`). Gutting `classAttributes()` in
`internal/north/mqtt/security_reconcile.go` to `return map[string]any{}` (dropping
known/centrals/since_ms/severity/sources) left it **green**: the test's emptiness
check is `len(body)==0`, and `enqueueJSON` unconditionally injects
`attrs["state"] = state` at `security_reconcile.go:84`, so the payload always has
≥1 key regardless of what the builder produced.
*Residual*: none for attribute content.
*Cost if violated*: the operator's alarm view loses severity and sources while
still showing a state — silent data loss on the safety-relevant plane.

### 1.4 Device model and replay

**Invariant: Lock and Siren publish their current value on `Subscribe`, so a
consumer is not blank until the next CCU push.**
`TestSubscribeReplay_Lock_RFRefreshed` (`tests/contract/subscribe_replay_test.go:270`)
and `TestSubscribeReplay_Siren_AcousticActive` (`:303`). Making `Subscribe()`
return immediately — `internal/model/custom/lock/lock.go:439`,
`internal/model/custom/siren/siren.go:498`, before any hook is wired — left both
**green**, because `IsRefreshed()` / `IsActive()` read the wire DP's own observed
flag, which the test's earlier `OnEvent` call had already set.
The batch flagged this as a possible production defect — neither `Subscribe`
calls `ReplayCurrentValue`, while four sibling profiles do — and asked for it to
be verified before anything was repaired.

**Verified, and refuted.** Lock and Siren deliberately keep no aggregate cache:
`State()` / `Direction()` / `IsJammed()` and the siren's accessors read straight
from the wire data points, so there is nothing to replay. Both `Subscribe`
bodies say so in as many words and register deliberate no-op `OnAnyUpdate`
hooks, whose only job is to make the channel record a registration the event
bridge can re-fire. The siblings that call `ReplayCurrentValue` — cover,
garage, climate, rgbw — each maintain a derived value (`applyDirection`,
`applyState`, `applyOpMode`, `applyMode`) that would otherwise stay stale.

What remains is a sharper finding about the guards, not the code: these two
tests pin an invariant that does not apply to their subject. They should either
assert what Lock and Siren actually promise — that the channel carries an
`OnAnyUpdate` registration after `Subscribe` — or be deleted. Pinning a
behaviour a profile was never meant to have is how a test comes to pass for
reasons unrelated to its name.

**Invariant: an HmIP-PS secondary channel is classified secondary and gets a
`vch<N>` suffix.** `TestNaming_HmIPPS_SecondaryChannels_VchSuffix`
(`tests/contract/naming_contract_test.go:117`). Inverting
`internal/model/device/channel.go:734` (`groupNo != c.Number` → `groupNo ==
c.Number`) left it **green**: the fixture never attaches a CustomDataPoint to
ch4/ch5, and the loop body starts `if ch.CustomDataPoint() == nil { continue }`.
The entire test body is a no-op today.
*Residual*: `TestNaming_SecondaryChannel_NotPrimary` and
`TestNaming_ChannelClassification_Table` bite; the HmIP-PS-specific slot does not.
*Cost if violated*: colliding entity IDs in HA discovery for multi-channel PS.

**Custom-DP wire shape — a name problem, not a coverage hole.**
`TestGenerateWireSnapshots` (`tests/contract/wire_snapshots/generator_test.go:705`)
stayed green after flipping `internal/model/generic/switch.go:130`
(`s.Set(ctx, true, …)` → `false`) because it is a pure generator behind
`//go:build snapshot_gen` that `os.WriteFile`s whatever it observes. Its siblings
`TestWireSnapshots` (`snapshot_pin_test.go:708`) and `TestReferenceCompare`
(`reference_compare_test.go:753`) both bite on that same mutation with precise
messages. So the invariant **is** protected — but the generator silently
rewrote `Switch__TurnOn.json` during the mutation, which is the live hazard:
anyone running the generate tag over broken code re-baselines the golden set
without a word.

### 1.5 Health / connectivity

**Invariant: `ClientStateChangedEvent` → hub reachability mapping.**
`TestClientStateChangedEventToConnectivityMapping`
(`tests/contract/connectivity_state_contract_test.go:48`). Changing the
`ClientStateConnected` case in the real mapping
(`internal/central/adapter/health_wiring.go:109`) to record `false` left it
**green**: the test computes reachability with `stateToReachable`, a hand-copied
mirror of the production switch defined in the test file itself at `:30`, and
applies it to a `hub.Connectivity` it constructs — a textbook bracketing test.
*Residual*: `TestConnectivityOnStateWithInterfaceFromEvent`,
`TestPin_HubSetConnectivity_CalledInHubWiring` and
`TestPin_ConnectivityProbe_WiredInHubWiring` bite.
*Cost if violated*: `/health` reports green while a client is down.

### 1.6 SPA ↔ daemon operation surface

**Invariant: every SPA widget `invoke(op, …)` has a matching case in the
custom-DP dispatcher.** `TestSPACDPOperationsMatchDispatcher`
(`tests/contract/spa_cdp_operation_contract_test.go:27`). Deleting the entire
`"set_brightness"` case from `internal/central/adapter/custom_dp_dispatcher.go:274-280`
left it **green**. The doc comment calls `acceptedOperations` "a
dispatcher-derived whitelist"; it is a hand-written map at the test file's
`:128`. The test checks the SPA against its own mirror, so a dispatcher-side
removal is invisible in both directions of the contract it claims to hold.
*Cost if violated*: a UI control that does nothing, with no error anywhere.

### 1.7 i18n

**Invariant: every locale carries the same key set.** `TestI18nCatalogParity`
(`tests/contract/i18n_catalog_keys_test.go:16`). A key added to `en.json` and
absent from `de.json` stayed **green**: `keysForLocale` (`:52`) only probes
`documentedKeys()` (`:65`), ~40 hand-listed keys, instead of enumerating the
catalogue. A real untranslated string is invisible unless someone also remembers
to add it to the hand list — i.e. exactly when they would not.
*Residual*: `TestConfigFieldsHaveLabelsAndHelp` bites for `cfg:` fields, which is
the SPA catalogue, not `internal/i18n/catalogs/`.

### 1.8 easymode preset decoding (lowest cost)

`TestPin_OptionPresetVal_LabelKey_InEasymodeDecoder`
(`tests/contract/wiring_pins/ccudata_wiring_test.go:17`). Deleting `LabelKey`
from the `OptionPresetVal` struct in `internal/ccudata/easymode.go` stayed
**green**: four other structs in that file also declare a `LabelKey` field, and
`identUsesNotDefinition` (`wiring_helpers.go:571`) only excludes *FuncDecl*
definitions, never struct fields. *Cost*: i18n-keyed preset labels stop
decoding — cosmetic, recoverable.

---

## 2. Recurring failure mechanisms

Ordered by instance count. Anything ≥2 is worth a meta-guard; the singles are
worth a fix.

| # | Mechanism | Instances | Verdict spread |
|---|---|---|---|
| M1 | **Identifier-only source matching in the wiring-pin helper** | 4 | all decorative |
| M2a | **Production string/logic re-typed into the test file** (bracketing by copy) | 5 | all decorative |
| M2b | **Hand-written table where the domain has a catalogue** | 3 | all decorative |
| M3 | **An assertion that cannot execute or cannot fail** | 3 | 2 decorative, 1 weak |
| M4 | **Guard reads a side channel that bypasses the seam** | 2 | decorative (+ production defect) |
| M5 | **Text-substring assertion over raw source** | 2 | weak |
| M6 | **Compile-time check reported as a test** | 2 | weak |
| M7 | **Guard measures a test-file-local artefact** | 3 | unclear |
| M8 | **A generator named `Test*`** | 1 | decorative-by-classification |

**M1 — identifier-only matching (4).** `MustFindCallerInFile`
(`tests/contract/wiring_helpers.go:114-131`) collects every `*ast.Ident` equal to
the callee name, then asks `callIsExecutable` (`:234`). Three independent holes,
each of which alone defeats a pin:
1. *No package qualification*: `spake2.NewVerifier` satisfies a pin about
   `mattercert.NewVerifier`.
2. *No enclosing-function scope*: a pin "about `revertAddNOC`" is satisfied by a
   `ReplaceACL` call in `AddNOC`, or by the interface declaration.
3. *No call shape, and no reachability outside `main`*: `_ = wireCUxDInterface`
   passes, because `callIsExecutable` returns `true, ""` whenever
   `reachable == nil`, which is every library package (`:243-247`). Struct fields
   escape the "definition" filter too (`identUsesNotDefinition`, `:571-594`).
   The helper's own doc comment at `:144-154` explains that a name-only pin once
   let the ACL gate ship unenforced — the fix landed in `MustFindMethodCall`, and
   `MustFindCallerInFile` kept the disease.
**Blast radius: 44 call sites** (`MustFindCallerInFile`), vs 19 for
`MustFindMethodCall`, across 84 pins in `tests/contract/wiring_pins/`. Only 4
were mutation-tested here; the other 40 carry the same three holes untested.
**This is the single highest-leverage repair in the report.**

**M2a — production text copied into the test (5).**
`stateToReachable` (`connectivity_state_contract_test.go:30`), the two template
literals in `discovery_roundtrip_test.go` (`:820`, `:856`), the local
`rawEnvelope`/`brokenTemplate` pair (`:751`), and the sample-string table in
`mqtt_topology_test.go:19`. In each case the test's own doc comment claims it
pins the production artefact by name. The copy cannot drift *visibly*: it drifts
by staying still while production moves. Meta-guard shape: forbid a contract test
from declaring a string literal that is byte-equal to a production constant it
does not import (a source scan can find these).

**M2b — hand table vs. the domain's catalogue (3).** `documentedKeys()`
(`i18n_catalog_keys_test.go:65`), `knownFilters`
(`service_discovery_shape_test.go:703`), `acceptedOperations`
(`spa_cdp_operation_contract_test.go:128`). This is the mechanism the brief
already named, and it is the second-largest cluster. Every one of these three has
a real enumerable source (the `i18n.Catalogs` map, the subscriber's own filter
list, the dispatcher's `case` literals).

**M3 — an assertion that cannot execute or cannot fail (3).** The
`if ch.CustomDataPoint() == nil { continue }` no-op loop
(`naming_contract_test.go:117`); the `len(body)==0` check dominated by
`enqueueJSON`'s unconditional `attrs["state"]` injection
(`security_mqtt_plane_test.go:336` / `security_reconcile.go:84`); and the weak
`TestDeviceTypesAreNormalised` (`device_profile_catalogue_test.go:161`), whose
lower-case half is dead because `Registry.Register` runs `normalizeModel`
(`strings.ToLower`) *before* the value is stored, so a mixed-case literal in
`profiles.go` can never reach the assertion. Meta-guard shape: a coverage probe
asserting that each contract test executes at least one assertion (a counting
`t.Helper` wrapper, or a mutation smoke run in CI).

**M4 — the guard reads a side channel that bypasses the seam (2).** Both
`TestSubscribeReplay_{Lock,Siren}`: the observable they assert
(`IsRefreshed()`, `IsActive()`) is fed by the wire DP's own flag, which the test
sets itself, so the seam under test (`Subscribe`) is out of the causal path.
Distinct from M2a because nothing was copied — the wrong observable was chosen.

**M5 — text-substring over raw source (2, weak).**
`TestCentralMetricsReachTheDiagnosticsDump`
(`tests/contract/wiring_pins/central_metrics_diagnostics_test.go:21`) bites a
deletion but not a `//` comment-out — `readRepoFile` + `strings.Contains` cannot
tell live code from a comment or a duplicate literal elsewhere.
`TestVisibilityReadOnlyDPSkipDocumented`
(`tests/contract/visibility_read_only_audit_test.go:33`) catches renames only;
a rename-preserving inversion of the skip logic passes untouched.

**M6 — compile-time check reported as a test (2, weak).**
`TestSourceCompletenessAcrossModelLayers` (`source_completeness_test.go:41`) and
`TestHADiscoveryPayloadBuilderCompleteness` (`:140`) are empty-bodied interface
assertions; a mutation yields a compile error, not a failing assertion. Per the
brief a compile error is not a bite — but these are deliberate and the compiler
message is precise. Recommend documenting, not repairing.

**M7 — the guard measures a test-file-local artefact (3).** See §4.

**M8 — a generator named `Test*` (1).** `TestGenerateWireSnapshots`. Its
invariant is held elsewhere; the defect is that a routine named like a check
mutates the golden set as a side effect.

---

## 3. Ranked repair list

Ranked by what the *unprotected invariant* costs when violated, not by how easy
the guard is to fix. "Bite" = the one change that makes the guard measure the
thing.

| # | Guard | Invariant left unheld | What would make it bite |
|---|---|---|---|
| 1 | `TestPin_NewVerifier_CalledInDaemon` (`chip_noc_verify_test.go:17`) | The daemon verifies a NOC's chain before accepting it — fabric takeover otherwise | Assert the **effect**: AddNOC with a NOC not signed by the fabric root is rejected; failing that, match a package-qualified `SelectorExpr` (`mattercert.NewVerifier`), not the bare ident |
| 2 | `TestPin_RevertAddNOC_ACLCleanup` (`chip_revert_acl_cleanup_test.go:18`) | A failed AddNOC leaves no ACL entries — privilege leak on index reuse | Drive `revertAddNOC` with a fake `StoreFacade` and assert it recorded `ReplaceACL(idx, nil)`; a source pin must at minimum be scoped to the `revertAddNOC` FuncDecl |
| 3 | *(meta)* `MustFindCallerInFile`, 44 call sites (`wiring_helpers.go:114`) | Every "X is called in Y" pin in the repo | Package-qualified selector match + optional enclosing-function scope + require a `CallExpr`, and make library-package pins declare an effect assertion instead of returning `true` at `:243` |
| 4 | `TestServiceDiscoveryShape_TopicSegmentContract` (`service_discovery_shape_test.go:646`) | Every advertised `command_topic` is subscribed — otherwise all HA writes vanish | Construct the real `CommandSubscriber`, capture the filters it passes to `Subscribe`, match each discovery topic against **those**, delete `knownFilters` |
| 5 | `TestSecurityPlane_AttributePayloadsAreObjects` (`security_mqtt_plane_test.go:336`) | Alarm/security attributes actually reach the operator | Assert named keys (`severity`, `sources`, `centrals`) on the builder's return value, before `enqueueJSON` injects `state` |
| 6 | `TestSubscribeReplay_{Lock,Siren}` (`subscribe_replay_test.go:270`, `:303`) | Lock/Siren state is replayed on Subscribe — HA shows unknown until the next push | Assert via a callback registered *by* `Subscribe` (record invocations during the call), not via the wire DP's flag — **and first check whether `lock.go:439` / `siren.go:498` are missing `ReplayCurrentValue` outright** |
| 7 | `TestSPACDPOperationsMatchDispatcher` (`spa_cdp_operation_contract_test.go:27`) | Every SPA widget operation exists in the dispatcher — dead UI controls otherwise | Derive the accepted set by parsing the `case` literals of `custom_dp_dispatcher.go` (or export a dispatcher operation registry) and diff it against the widget scan, both directions |
| 8 | `TestDiscoveryRoundTrip_{GuardEmptyEnvelope,BoolCapitalisation,Event_JSONPayloadNotBroken}` (`discovery_roundtrip_test.go:820`, `:856`, `:751`) | HA `value_template`s render valid, lowercase JSON | Render `mqtt.valueJSONValueLowerTemplate` itself (export it, or move these into the `mqtt` package) instead of a copied literal |
| 9 | `TestClientStateChangedEventToConnectivityMapping` (`connectivity_state_contract_test.go:48`) | Client state → health reachability — false-green `/health` otherwise | Call the production mapping in `internal/central/adapter/health_wiring.go`; delete `stateToReachable` at `:30` |
| 10 | `TestI18nCatalogParity` (`i18n_catalog_keys_test.go:16`) | Locale key sets are identical — untranslated strings ship otherwise | Enumerate the real key set from `i18n.Catalogs` and diff locale-vs-locale; delete `documentedKeys()` |
| 11 | `TestPin_wireCUxDInterface_CalledInCCUWiring` (`ccu_wiring_test.go:66`) | CUxD is wired to BIN-RPC (residual coverage exists) | Pin the effect through `ccu_wiring`: wire a CUxD interface and assert the BIN-RPC backend was chosen |
| 12 | `TestMQTTTopicHierarchyShape` (`mqtt_topology_test.go:19`) | Topic shape matches `TopicBuilder` — retained-state migrators break otherwise | Import `mqtt` and generate the samples from `TopicBuilder`; the fixture then compares production output against the ADR file |
| 13 | `TestNaming_HmIPPS_SecondaryChannels_VchSuffix` (`naming_contract_test.go:117`) | HmIP-PS secondary channels get `vch<N>` — colliding entity IDs otherwise | `t.Fatal` when the fixture produced no CustomDataPoint, so the empty loop can no longer pass as success |
| 14 | `TestGenerateWireSnapshots` (`wire_snapshots/generator_test.go:705`) | *(none — held by `TestWireSnapshots` + `TestReferenceCompare`)* | Make the generator refuse to write when `TestReferenceCompare` would fail, and have CI fail on a dirty tree after a generate run |
| 15 | `TestCentralMetricsReachTheDiagnosticsDump` (`central_metrics_diagnostics_test.go:21`) | Central metrics reach the diagnostics dump | Parse the file instead of `strings.Contains`, so a commented-out line reads as absent — or assert the dumped JSON contains the key |
| 16 | `TestVisibilityReadOnlyDPSkipDocumented` (`visibility_read_only_audit_test.go:33`) | Read-only DP skip logic | Call the visibility decider with a read-only master parameter and assert it is skipped; the identifier scan is a rename alarm, not a behaviour check |
| 17 | `TestWebSocketAcceptsFragmentedClientMessages` (`ws_framing_test.go:138`) | Continuation-frame reassembly (it *does* bite) | Assert on the server's `ws.malformed` diagnosis rather than letting the failure surface as `read header: i/o timeout` |
| 18 | `TestDeviceTypesAreNormalised` (`device_profile_catalogue_test.go:161`) | DeviceType literals are lower-case (dead half) and whitespace-free (live half) | Read the literals from `profiles.go` source, not from the registry that already lower-cased them |
| 19 | `TestPin_OptionPresetVal_LabelKey_InEasymodeDecoder` (`ccudata_wiring_test.go:17`) | `OptionPresetVal.LabelKey` survives — stale preset labels otherwise | Parse the file and assert the field on the `OptionPresetVal` struct specifically, or decode a fixture and assert the value arrives |
| 20 | `TestSourceCompleteness*` (`source_completeness_test.go:41`, `:140`) | *(held at compile time)* | Nothing to repair; document that these are compile-time assertions so a future reader does not mistake a green run for a measured one |

---

## 4. What this pass could not answer

Three guards where no production line could be named. They share one shape, and
it is a finding in itself: **each asserts over an artefact that lives in the test
file**, so no production change can move them. They are format or type checks
wearing contract-guard names, and they inflate the guard count by three.

1. **`TestClientStateChangedEventInterfaceFieldParity`**
   (`connectivity_state_contract_test.go:110`) — builds an
   `hmevent.ClientStateChangedEvent{Interface: …}`, copies the field into a
   `hub.InterfaceReachability{Interface: e.Interface}` literal and asserts it
   round-tripped. That is compile-time type compatibility asserted at runtime; if
   the field types diverged the package would not build, so the assertion itself
   never speaks. Same family as M6.
2. **`TestGermanWordRuleBites`** (`doc_purity_test.go:35`) — `germanWordRule` is
   defined exactly once, in `doc_purity_test.go` itself, and used only by this
   test and `TestDocPurity`'s rule table in the same file. There is no production
   implementation under `internal/`, `pkg/` or `cmd/` to mutate. As a
   self-check on the sibling guard's regex it has value; as a contract guard it
   protects nothing outside its own file. *Note*: `TestDocPurity` itself bites.
3. **`TestValueSemanticsChangesAreWellFormed`** (`api_surface_bump_test.go:325`)
   — validates the *format* of `valueSemanticsChanges`, a list declared and read
   only inside that test file (version prefix parses, `schema.field: description`
   shape). This is the brief's second known mechanism verbatim: a check on a
   committed artefact's shape, named as though it checked semantics. Nothing
   binds the list to the API surface it describes, so an API value-semantics
   change made without a list entry is invisible.

**Recommendation for all three**: either bind them to a production artefact
(1: delete it, the compiler holds it; 2: keep as a self-check but rename;
3: derive the expected entries from the OpenAPI/WS schema diff) or move them out
of the guard inventory, so the next inventory count is honest.

---

### Appendix — verdict tallies per batch

`b1` 44 · `b2` 43 · `b3` 52 · `b4` 46 · `b5` 43 · `b6` 43 · `b7` 44 · `b8` 44 =
**359 unique guards, no duplicates across batches**. Verdicts: 333 `bites`,
6 `bites-weakly` (3 spelled `bites_weakly`), 17 `decorative`, 3 `unclear`.
Working tree verified clean after the pass (`git status --porcelain` empty at
`00d0259d`) — every mutation was restored, including the golden snapshot the
generator rewrote.
