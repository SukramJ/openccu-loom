# Device projection — how one device appears on each plane, and why not

- **Status**: Accepted by the owner on 2026-11-12 (issue #1068, step 1). Slices 1 (Matter, REST route, MCP tool), 2 (measurement forwarding), 3 (Home Assistant discovery) and 4 (raw MQTT topics) are built. The view (slice 6) is built for these three planes, ahead of slices 4 and 5 by the owner's choice of 2026-11-12.
- **Date**: 2026-11-12
- **Scope**: `openccu-loom` — `internal/north/{mqtt,matteradapter,measurements,rest,mcp}`,
  `internal/central/adapter`, `assets/ui/`
- **Related**:
  [ADR 0088 — the Config UI is the bridge's console](../../docs/adr/0088-config-ui-is-the-bridges-console.md)
  (decision 8),
  [ADR 0067 — the north surface is a model API](../../docs/adr/0067-north-surface-is-a-model-api.md)

---

## Recommendation

> For one device, the daemon answers on every north-bound plane: **what is published,
> under which identifiers, and for what is not published, the named reason.**
>
> - **One decision function per plane, used twice.** Each plane gets a function that
>   returns a verdict with a named reason. The publisher publishes by that verdict; the
>   projection calls the same function on demand. What is shown and what is published
>   cannot drift, because there is one set of rules (owner decision, 2026-11-12).
> - **Five planes in the concept, built in slices**: Home Assistant discovery, the raw
>   MQTT topics, Matter, REST and measurement forwarding (owner decision, 2026-11-12).
>   Matter first, because its verdict already exists.
> - **API first**: `GET /devices/{address}/projection`, the MCP tool
>   `get_device_projection`, then a Projection tab in the device detail, registered as
>   the surface `device.projection`.
> - **Every plane's slice ends with a round-trip test** that compares the projection
>   with what a running daemon published.

---

## 1. What exists today

Read from the code at `33097976`. The inventory behind this section is a map made by a
reading agent; the lines marked *read* were read again for this concept, the others
are to be read again by the slice that relies on them.

| Plane | Who decides | Is the reason kept? | Per-device answer today |
|---|---|---|---|
| Home Assistant discovery | `DefaultDiscoveryBuilder.Build` returns `ok=false` on seven paths (`internal/north/mqtt/discovery.go:400-784`, *read*); upstream gates in `internal/central/adapter/eventbridge.go` (the `Visible()` gate at `:1829`, *read*) | No. Decided and dropped; only per-central counters | None |
| Raw MQTT topics | `RawEnabled` in the `Publish*` helpers of `internal/north/mqtt/bridge.go`, plus the event bridge's gates | No | None |
| Matter | `CollectCandidates` (`matteradapter/candidates.go:68`) and `Assembler.channelSpecs` (`assembler.go:322`) | Partly: an eligibility verdict with state and reason, served as exposure items under `/node/exposure`; the allow-list, secondary-channel and measurement gates of the assembler are not part of it | A list of all items; no query for one device, no endpoint id |
| REST / WebSocket | the model: usage marks (`pkg/hmenum/datapoint.go:150-167`) and `Visible()` | In the model | `GET /devices/{address}` shows what is there, not what was left out |
| Measurement forwarding | `Forwarder.onValueChanged` (`internal/north/measurements/forwarder.go:157`): paramset, numeric, pattern, override, provenance | No; `GET /measurements/recording` answers from pattern and override only | One data point at a time, without the other gates |

The device detail shows two plane-related controls and no plane-related fact: the
recording picker and the channel's `hidden` toggle.

So an operator who asks "why is this device not in Home Assistant" has the log and
nothing else, and an assistant on MCP has not even that.

## 2. The model

A **projection** is the answer for one device on one central:

```
Projection
  device        address, central
  planes[]      one entry per plane
    plane       ha_discovery | mqtt_raw | matter | rest | measurements
    state       active | inactive        (the plane itself, for this central)
    reason      set when inactive        (plane_disabled, central_excluded, …)
    entries[]   one per subject the plane could carry
      subject   device | channel | data point (channel no, paramset, parameter)
      verdict   published | withheld
      reason    set when withheld; one value of the plane's closed set
      identity  what it is called there (see below)
      folded_into  set when the subject is carried by another entry
```

**Identity per plane**

- Home Assistant discovery: component, node id, object id, discovery topic, state topic.
- Raw topics: the topics, with the retained flag.
- Matter: the source key, the device type name, the endpoint id once assigned.
- REST: the path of the data point.
- Measurements: measurement name and tags as the exporter writes them.

**Reasons are a closed set per plane**, named after the rule, never free text. The
draft list below is derived from the gates in section 1; each slice fixes its plane's
list from the code it refactors and may not ship a reason called `other`.

| Kind | Examples |
|---|---|
| The plane | `plane_disabled`, `central_excluded` |
| The model | `channel_hidden`, `data_point_not_visible`, `usage_excludes`, `no_mapping` |
| The plane's own rules | `folded_into_channel_entity`, `not_on_allow_list`, `secondary_channel`, `not_numeric`, `pattern_excludes`, `override_off` |
| Runtime | `central_not_ready`, `device_not_released`, `not_observed_yet` |

**Declared and runtime reasons are kept apart.** A declared reason follows from
configuration and model and holds until one of them changes. A runtime reason describes
now (the central is still coming up, a value was never observed). The view shows them
differently: the first is an answer, the second is a state that passes.

**Folding is not withholding.** A `PRESS_SHORT` that Home Assistant receives as part of
one channel event entity is `published` with `folded_into` naming that entity, not
`withheld`.

## 3. Mechanics

1. **Each plane exposes `Decide`.** It takes the subject and the plane's configuration
   and returns the verdict, the reason and the identity. It is pure: no publish, no
   I/O beyond reading the model and stored settings (the Matter allow-list, the
   recording overrides).
2. **The publisher calls `Decide` and acts on the verdict.** The seven `ok=false`
   returns of the discovery builder and the gates of the event bridge become verdicts
   with a reason; publishing happens after the verdict, in one place per plane.
3. **The projection service** lives in `internal/central/adapter`, walks the device's
   channels and data points, and asks every plane. It reaches the planes through an
   interface declared where it is consumed; a plane that is not built in or not enabled
   answers `inactive` with its reason.
4. **Nothing is recorded.** The answer is computed when asked. A projection is
   therefore also correct for a device that has published nothing since start.

The wiring follows the repository's rules: the projection service receives the planes
through the composition root, pinned under `tests/contract/wiring_pins/`, and the
round-trip tests boot the simulated CCU not ready and flip it.

## 4. Contract

- `GET /api/v1/devices/{address}/projection?central=…` returns the projection.
  Optional `plane=` narrows it. Role: viewer, like the device itself.
- Capability token `projection.v1`. An additive minor of the REST API.
- MCP: `get_device_projection` with the same answer. It is the tool an assistant needs
  for "why is this device missing in Home Assistant".
- No write. Changing what is published stays where it is today (the hidden toggle, the
  Matter exposure editor, the recording overrides); the view links there.
- No WebSocket event in the first version; the view reloads.

## 5. The view

A **Projection** tab in the device detail, registered as surface `device.projection`,
visible in every profile.

- One section per plane, with the plane's state first: an inactive plane says why and
  shows no entries.
- Per entry: the subject, the verdict, the identity or the reason. A reason is a
  translated sentence (en + de) that names the rule and, where one exists, links to the
  setting that governs it.
- Runtime reasons are shown as pending, not as an answer.
- Copy buttons on topics and identifiers.

## 6. Tests

Per plane, one round-trip test in `tests/e2e/` against the built daemon and the
simulated CCU, booted not ready and then flipped:

- **Shown ⊆ published and published ⊆ shown.** Every entry the projection calls
  `published` is found on the plane under its stated identity, and everything the plane
  carries for the device is an entry of the projection.
- **A withheld entry is absent**, and it becomes present when the setting its reason
  names is changed. This is the negative control of the reason itself.
- **Bite proof**: with the publisher's use of the verdict removed, the test is observed
  red, then restored.

Sources of "published": the broker's retained and received topics for the two MQTT
planes, the bridge's endpoint list for Matter, the fake InfluxDB endpoint for
measurements, the REST answer itself for REST.

Unit level: every reason of a plane's closed set has a table case, and a guard fails
when a `Decide` returns a reason outside the set.

## 7. Slices

| # | Slice | Why this order |
|---|---|---|
| 1 | The model, the service, the REST route and MCP tool, with **Matter** | The verdict exists; the slice adds the assembler's gates and the endpoint id |
| 2 | **Measurements** | One function with five gates; smallest refactor |
| 3 | **Home Assistant discovery** | The largest refactor: seven silent returns and the event bridge's gates |
| 4 | **Raw MQTT topics** | Shares the event bridge's gates with slice 3 |
| 5 | **REST** | Mostly the model's own marks |
| 6 | **The view** | After the API carries at least Matter and discovery |

Each slice is one pull request with its round-trip test. The feature is audited as a
whole before it is called done.

## 8. Is this the best architecture?

**Better than the comparison targets?** Home Assistant shows an entity or it does not;
the integration and the CCU WebUI have no answer to "why not". Matter controllers show
nothing at all about a missing device. A named reason per plane is something none of
them offers.

**Better than the state before?** Before, the rules existed once per plane and their
outcome was visible only as presence or absence, plus log lines. Afterwards the rules
still exist once, their outcome is data, and the same data is on REST, MCP and in the
view. The alternative of recording what the publisher did would show history instead
of rules, would be empty after a start and would cost memory per data point. The
alternative of a separate explaining function would be a second copy of the rules.

**Is the cut right?** The rule stays in the plane that owns it. The projection service
owns only the walk over one device and the common shape. The SPA owns presentation.
go-fabric keeps its eligibility verdict; this repository adds only what its own
assembler decides on top. Nothing moves into the clients.

The cost: the discovery builder and the event bridge are restructured so that a
verdict precedes every publish. That is the riskiest code of the MQTT plane, and the
discovery goldens and the plane round-trip tests are what holds it.

## 9. Open points

- **Slice 3 is built by separating describing from publishing** (owner
  decision, 2026-11-12). The device snapshot of the event bridge
  announces on about nine paths and interleaves announcing with
  registering and loading state; the decision is spread over the
  builder, the MQTT bridge and the event bridge. The snapshot is split
  into a description without side effects and its publication, and the
  plane reads the description. The steps are tracked in issue #1068.
- **Slice 1 as built differs from section 7 in two points.** The Matter
  verdict that "already exists" is the candidate walk of the exposure
  list, which is not what the bridge is built from: the assembler
  decides, with folding and the allow-list. The slice therefore
  instruments the assembler (`Assembler.DecideDevice`) and leaves the
  candidate walk alone. And the round-trip test of section 6 runs at
  the assembler level (`TestPublishedDecisionsAreTheDescribedEndpoints`,
  over the golden fleet): the hermetic e2e harness cannot run the Matter
  bridge, so the e2e test covers the route and the wiring with the plane
  inactive. A round trip against a running bridge needs the harness to
  enable Matter, or a case in the chip-tool suite.
- The reason vocabulary of go-fabric's eligibility package was not read; slice 1 reads
  it and maps it instead of renaming it.
- Whether the usage marks gate the raw plane was not traced; slice 4 establishes it.
- How the Matter endpoint id is obtained for a source key (the reconciler was not read).
- The cost of computing a projection for a device with many data points was not
  measured; if it matters, the answer is narrowed by `plane=`, not cached.
- A "what would change if" preview is out of scope. Matter has one for its allow-list;
  the projection states what is.
