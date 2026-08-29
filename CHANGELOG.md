# Changelog

All notable changes to OpenCCU-Loom are recorded in this file.
The project follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.68.0] - 2026-08-29

### Changed

- **System variables and programs are keyed on the CCU's id, not on their
  name.** Renaming either in the CCU WebUI moved its `unique_id`, which took
  the consumer's entity history, area, customisations and every automation
  built on it. `Sysvar.CanonicalUniqueID` now uses the numeric `Vid` and
  `Program.CanonicalUniqueID` the program `ID`; the name slug stands in only
  while an id is unresolved, which is the shape a consumer must accept during
  the rollover.

  **Every system variable and program entity re-keys once.** A consumer that
  builds its registry from `unique_id` needs a migration pass shipping with
  this release, or those entities orphan instead of moving.

  The reference implementation adopted the same rule in 2026.8.8, so the
  shared golden fixtures move together rather than declaring a divergence —
  the opposite order would have rebuilt the CUxD construction that 0.67.1
  retired. `script/requirements/reference-stack.txt` moves to 2026.8.8 with
  it, and `make routing-key-parity` stays green.

  `docs/external-clients/ha-unique-id-migration.md` carried the target shape
  for sysvars already, written ahead of the implementation; it now describes
  what both sides actually do, covers programs, and states the version from
  which it holds.

  The rule reaches the MQTT discovery plane too. The sysvar builder was
  already keyed on the numeric id; the program builder was not, so the same
  program was `loom_<serial10>_program_prg-42` over REST/WS and
  `loom_<serial10>_program_morning-lights` over the broker — two keys for one
  CCU object, both landing in the same Home Assistant registry, and a
  migration note that described a rule only half the daemon followed. Nothing
  failed, because each plane had a test asserting its own answer.
  `TestHubUniqueIDMatchesAcrossPlanes` now pins the two against each other.

## [0.67.1] - 2026-08-28

### Changed

- **The CUxD routing-key divergence retired.** `CUX*` addresses were namespaced
  by the central here and left bare by the Python reference implementation,
  because CUxD hands out the same synthetic addresses on every CCU and two
  bridged CCUs would otherwise declare identical `unique_id`s for them. The
  reference has adopted the same rule, which is the retirement condition
  `notes/parity/by_design.md` had named for itself.

  Nothing the daemon emits changes — this side already applied the rule. What
  changes is that the two implementations now agree, so the divergence fixture
  that carried both answers is gone and its cases moved into the shared golden
  fixtures replayed by `make routing-key-parity`. The pin in
  `script/requirements/reference-stack.txt` moves with them; without that the
  next bump would have turned both halves of the guard red at once, which is
  precisely what the guard was built to do and would have read as a regression.

  `TestRoutingKeyCUxDScopingGolden` becomes `TestRoutingKeyCentralScopedFamilies`
  and keeps the one assertion that does not follow from the golden values: every
  centrally scoped address family must answer true to `NeedsCentralScope`, which
  the north-bound planes read to decide whether to withhold an id until the CCU
  serial is known.

- **The published external-client pages say so.** Both
  `docs/external-clients/ha-drop-in-identity-and-scoping.md` and
  `ha-unique-id-migration.md` described the divergence as live and named its
  retirement condition. They now describe it as retired, and both keep the
  caveat that a client pinned to an older reference still rebuilds the bare key
  and has to scope it itself.

## [0.67.0] - 2026-08-28

### Added

- **Every route in the OpenAPI document carries an `operationId`.** 37 of the
  295 operations had none, and `operationId` is what a generator keys its
  client methods on: `openapi-typescript` leaves a route without one out of its
  `operations` table entirely, so the SPA's generated types described 258 of
  295 routes and the other 37 — `GET /devices`, `GET /info`, `PUT
  …/data-points/{param}/value` among them — were simply absent from it. The
  same document is vendored by `node-red-contrib-openccu-loom`, and a contract
  test in `openccu-loom-client` needs the ids to address an operation at all.

  Purely additive: the field was absent, never differently named. Measured with
  `oasdiff breaking --fail-on ERR`, which reports no breaking change; the
  APIVersion guard therefore asks for a minor bump, not a major, and no client
  is locked out by a major mismatch.

- **The two open token sets on `CustomDPSummary` are published in full.**
  `assets/schemas/enums.json` gained a `vocabularies` block listing every
  `kind` token (22) and every `capabilities` key (26). Both are documented
  rather than closed, and the document says so: `kind` has a reachable empty
  value, and a missing capability key means the flag does not apply to that
  category rather than that it is false — a closed enum generated from either
  one would reject a conforming daemon response. The lists come from the
  `cdpkind` package itself, pinned to its constants and helpers by two guards.

- **`firmware` and `availability` are on every device summary row.** They were
  served only by `GET /devices/{addr}`, which is what forced a bootstrapping
  client into one detail request per device on top of the list request. Both
  accessors read the in-memory model — no southbound query, no extra CCU radio
  traffic — so a row costs a struct copy, not a wire call, and a client's
  bootstrap drops from N+1 requests to 1. `GET /snapshot` carries them for the
  same reason.

  `GET /devices/{addr}` is unchanged: `DeviceDetail` embeds the summary, so
  both fields keep the same keys in the same place. Its only remaining
  exclusive member is `channels`, which the nested snapshot already delivers —
  the endpoint stays for refreshing a single device.

- **The WebSocket envelope's `kind` has a named type.** `initial|change|refresh`
  existed only as prose in `assets/wsapi.json`, so a generated client had to
  spell the three literals itself. It is `hmenum.WSEnvelopeKind` now and ships
  in `assets/schemas/enums.json` like every other wire vocabulary. The wire is
  unchanged.

### Changed

- **The MQTT entity-description table is maintained here, not generated.**
  `internal/north/mqtt/entity_descriptions_generated.go` carried a
  `DO NOT EDIT` header, a generator name and the stamp `2026-05-02T15:22:37Z`.
  All three were false: the generator appeared in no make target and no
  workflow, and re-running it does not reproduce the file — it emits a
  different set of symbol names, and the result fails to compile against the
  rest of the package. The file had been hand-edited for months while wearing
  a machine-output label, which is also why two sentences in its own header
  broke off mid-clause, where a project name had been cut out to satisfy the
  comment guard.

  Following ADR 0063 and ADR 0067 the header comes off, the generator is
  deleted, and the 147 rules are ordinary daemon-owned source. The file is
  `entity_descriptions_table.go` now, since the old name was the same claim.
  Provenance, the scope decision and the reachability findings move to
  `notes/reference/ha-entity-description-table.md`.

  The upstream drift was measured before the generator went, not assumed:
  159 rules upstream against 147 here, differing by exactly the 12 keys the
  plan named, with nothing extra on this side. All 12 stay out of scope, and
  the note says why per rule — nine `SECURITY_*` rules describe a second
  security surface over a domain this daemon already owns, `DAEMON_CONNECTION`
  and `DAEMON_LATENCY` describe a Python client's connection to its own
  daemon, and `event_doorbell` is already implemented from a better source
  (`ccudata.DoorbellModels()`, the full CCU model set rather than three
  hard-coded entries).

- **The description table's coherence guard compares content, not a count.**
  `TestHARegistryDescriptionRulesCountUnchanged` pinned `len(rules) == 147`,
  which catches a truncated slice and misses every edit that keeps the count —
  a changed `device_class`, a device list that loses a model, a priority that
  reorders two rules. Those are the edits that change a discovery payload. It
  is replaced by `TestHARegistryDescriptionRulesMatchTheGolden`, which
  compares every field of every rule against
  `tests/contract/testdata/ha_registry_description_rules.json` and names the
  rule that moved.

### Fixed

- **The API-surface guard read a newly named route as a rename.** Giving a bare
  route an `operationId` moved it from the `(unnamed)` sentinel to a real name,
  which `TestAPISurfaceChangesCarryTheRightBump` classified as breaking and so
  demanded a major bump — the one thing that would have locked existing clients
  out over a purely additive change. A generator never emitted a symbol for an
  unnamed route, so nothing can break; the move is additive now. Dropping an
  existing name stays breaking, which is the direction that does remove a
  symbol.

## [0.66.1] - 2026-08-27

### Added

- **A server-side onboarding filter, opt-in on both planes.** REST
  (`?released_only=true` on `GET /devices` and `GET /snapshot`) and WebSocket
  (`released_only: true` on a subscribe frame) now omit devices that have not
  finished onboarding. It completes what 0.66.1 started: that release exposed
  the state so a consumer could filter itself, this one lets it ask the daemon
  to filter instead — including the race where a `device.created` push arrives
  before the consumer's snapshot read completes.

  Off by default and per connection, deliberately. This surface serves two
  kinds of consumer at once: the Config UI, which **must** see a device that is
  still being onboarded in order to configure it, and an ecosystem client,
  which must not adopt it before it is named. Filtering by default would blind
  the first and make a device silently vanish from every existing client's
  list.

  The two planes are meant to be used together — filtering the snapshot while
  leaving the socket unfiltered produces exactly the inconsistency the option
  exists to avoid: absent from the snapshot, arriving as a push. The
  `device.released` frame is never dropped; it is what lifts the filter.

- **The onboarding release state is visible to REST and WebSocket consumers.**
  0.66.0 enforced the hold on MQTT, Matter and the outbound webhook, and
  deliberately left this API showing an unreleased device — the Config UI has
  to see it to configure it. That conflated transport with role: a consumer of
  this API can be an ecosystem just as much as a configuration client, and one
  that adopts devices had no way to tell the two states apart. For it the
  release step did nothing.

  The state now travels with the device instead of being inferred from the
  channel:

  - `released` on `DeviceSummary` (`GET /devices`, `GET /devices/{addr}`,
    `GET /snapshot`).
  - `released` on the `device.created` WebSocket payload — on the frame
    itself, because looking it up separately is a race the consumer cannot
    win: the push can arrive before its snapshot read completes.
  - a new `device.released` broadcast on `device.{address}.lifecycle`. This
    is the piece a client cannot compensate for: the consumer that needs it
    is precisely the one that was already connected and filtered the device
    out, and without a push it would learn nothing until its next full
    reload.

  `released` is `true` for every device that never entered the onboarding
  wizard, so an installation that does not use it reads `true` throughout and
  no existing client needs to change.

- **The MCP surface carries the state too.** `list_devices` entries gain
  `released` and the inbox listing gains `awaiting_release`. An assistant
  driving the daemon is a device consumer like any other, and it was seeing a
  withheld device exactly like one in service.

- The `datapoint.value_changed` plane is now documented as **not** filtered by
  release state. It never was, and it should not be — the Config UI needs those
  values to verify a device before releasing it — but a consumer that filtered
  only the device list was still receiving them, with nothing saying so.

### Fixed

- **The onboarding filter only covered half the frames it should have.** It
  matched on a type switch that listed five of the eleven broadcast payloads
  naming a device, so a `released_only` subscriber still received, for a device
  it had explicitly asked not to see: custom-data-point state (covers roller
  shutters, climate, lights), **device triggers** — the worst of them, because a
  client that turns those into automations would have fired them — optimistic
  rollbacks, metadata changes, schedule changes and Matter exposure changes.

  The switch is gone. A payload now says for itself whether it is device-scoped
  (`ws.DeviceScopedPayload`), and a contract test requires every broadcast
  payload carrying a `device_address` to implement that or to be listed with a
  reason — so the next payload cannot slip past silently. The guard found the
  Matter exposure frame on its first run, which neither the report nor I had on
  the list.

- **A system variable or program bound to a withheld device is no longer
  mishandled.** Such an entity exists on the CCU regardless of whether the
  device has been released here, so dropping the frame would take something the
  operator has, while passing it through unchanged makes a filtering client
  attach the entity to a device it does not have. The entity now survives with
  its device association stripped, which the payload contract already defines
  as "attach it to the central hub" — a shape every client already handles.

- `device.metadata_changed` and `schedules.changed` moved their payload structs
  into the `ws` package. As unexported adapter types they were invisible both
  to the new filter and to the existing payload-field parity guard, which had
  carried them as documented holes.

- **`delay_new_device_creation` was never applied without a restart, while the
  config surface said it was.** The field is not classified restart-required,
  so saving it reported `restart_required: false` — but it was read at exactly
  two points, both during bring-up. Switching it off changed nothing until the
  next restart. That was tolerable while the toggle only gated future parking;
  it stopped being tolerable in 0.66.0, when a persisted queue started hanging
  off the same path and every already-held device stayed invisible to the
  ecosystems with nothing to explain why. A config reload now re-applies the
  setting per central and, when it goes off, releases the whole queue and
  announces each device — a silent release would leave MQTT, Matter and every
  connected API consumer withholding them anyway.

- **The colour saturation scale is documented on both directions.** A client
  reading `state.color.s` from the custom-data-point plane and scaling it for
  Home Assistant ends up with every colour fully saturated: the value is
  **already** the 0..100 scale HA expects, converted from the wire
  `SATURATION` fraction 0..1 by the daemon. `set_color` takes the same 0..100,
  so neither direction needs converting. Both scales lived only in Go comments
  until now, which is what made the wrong assumption possible; they are in the
  contract assets now, together with the reason the raw data-point plane still
  reports the wire value.

## [0.66.0] - 2026-08-27

### Added

- **Onboarding is a wizard now, and a device reaches the ecosystems only at
  its end.** Pairing a device on the CCU no longer makes it appear in Home
  Assistant, on a Matter controller or on an outbound webhook the moment the
  daemon notices it. The device walks three states instead:

  1. **Waiting** — announced by the CCU, held out of the model entirely. It has
     no ise_id and no channels, so there is nothing to configure yet.
  2. **Accepted, not released** — the operator confirms it with a name, it
     leaves the CCU inbox and is materialised here. Now it has its ise_id, its
     channels and its data points, and it is fully visible and configurable in
     this daemon's own REST / WebSocket surfaces — which is where the name,
     rooms and functions are set. The ecosystems still do not see it.
  3. **Released** — `POST /devices/{addr}/release` ends the hold, and MQTT,
     Matter and the webhook publish it.

  The order is what makes the naming stick. An ecosystem that sees a device
  first and is corrected afterwards keeps the identity it saw: Home Assistant
  its entity ids, a Matter controller its endpoint number — endpoint ids are
  assigned in assembly order and persisted, so a rename does not take them
  back.

  Only a device that entered through the wizard is ever withheld
  (`central.behavior.delay_new_device_creation`). Absence of a hold means
  released, so an existing installation publishes exactly what it published
  before and nothing disappears from Home Assistant on upgrade.

- **The Config UI drives the wizard.** A device between the accept and the
  release appears on the inbox surface with its own badge and its own action:
  *Release* instead of *Accept*, plus a shortcut into the device view to finish
  naming and placing it. Offering "accept" there would ask the operator to
  accept a device that is already accepted, so the two states never show the
  same controls, and *Replace* — which swaps a device not yet in service — is
  hidden for it.

  The German label for the first state moved from *Wartet auf Freigabe* to
  *Wartet auf Übernahme* in the process: with a real release step the old
  wording named the wrong one of the two.

### Changed

### Changed

- **`delay_new_device_creation` is a gate now, not a notice.** The toggle
  promised to hold a newly-paired device back until an operator accepts it, and
  for the callback that announced the pairing it did. Everything else ignored
  it: the deferred-creation queue lived only in memory, and the boot pull never
  consulted it — so an unaccepted device was materialised by the next restart,
  fully usable, while its inbox entry disappeared with the process. The
  operator's pending decision vanished without a trace.

  The decision is now persisted per central (`pending_devices`) and the boot
  pull honours it, so a held-back device stays out of the model, out of REST,
  MQTT, Matter and the WebSocket, and on the inbox surface until it is
  accepted. Accepting it carries its first-time configuration — name, rooms,
  functions — so it is named at the moment it becomes usable.

  Only the decision is stored, never the descriptions: the CCU delivers a full
  set on every pull, so a stored copy would be a duplicate that can go stale
  and would resurrect a device unpaired while the daemon was down. A parked
  entry the CCU stops reporting is swept by the next pull.

  The gate **honours** the parked set; it never adds to it. A device enters it
  through the `newDevices` callback alone, which is what a pairing is. Deciding
  "this pull returned an address I do not know, therefore it is new" would park
  an entire installation on the first boot after an upgrade.

  Turning the toggle off releases the queue rather than leaving it behind:
  "stop asking" rather than devices stranded in a state whose only explanation
  is a setting that is no longer on.

### Fixed

- A store failure while parking a device now holds it back for that run anyway
  instead of dropping the decision — the safe direction, because a run that
  forgets across a restart is recoverable while a device that appears without
  approval is the failure the gate exists to prevent. A failed *restore* is the
  deliberate opposite: nothing is held back, because a database hiccup
  presenting the whole installation as pending is indistinguishable from a real
  defect.

## [0.65.4] - 2026-08-27

### Fixed

- **The device inbox presented every paired device as waiting for approval.**
  With `delay_new_device_creation` enabled, an installation's entire fleet
  appeared on the inbox surface while simultaneously being fully materialised
  and visible in the device list — two lists, same devices, fed from different
  sources. The cause was one missing write: the device-ingest pipeline built
  the whole model without recording a single description in the device-
  description registry. That registry is the warm-boot cache, and its
  persistence sink is what fills the SQLite table, so on an installation whose
  descriptions had never arrived over a `newDevices` callback the table stayed
  empty for good — every boot logged `wire.descriptors.hydrated devices=0`
  beside a healthy `paramsets=N`, because the paramset half of the same
  pipeline had been recording its descriptors since 0.26.0 and the device half
  never had. The deferred-creation filter skips an announcement whose device is
  known *and* whose description is cached; with the cache permanently empty the
  second condition never held, so the CCU's post-init inventory announcement —
  which covers the complete fleet, since the daemon answers `listDevices` with
  an empty array — was parked in full. The pull now records every description,
  roots and channels, keyed by the canonical wire id. One restart repopulates
  the cache and empties the inbox; no manual step is needed.
- The same empty cache silently degraded everything else keyed on a device
  description: the firmware domain iterates the description registry to find
  devices to check, channel-team resolution reads a channel's description, and
  `CheckAndCreateDevicesFromCache` had nothing to restore from on a warm boot.

## [0.65.3] - 2026-08-27

### Fixed

- **`DeviceCreatedPayload` in `assets/openapi.yaml` still described the
  pre-0.65.2 behaviour, and named a `source` value that does not exist.** The
  schema said the broadcast fires on "cache reload or initial-snapshot import"
  and listed `NEW_DEVICE` among the sources; the enum has `CACHE`, `INIT`,
  `MANUAL`, `NEW` and `REFRESH`, and 0.65.2 had already stopped announcing the
  CCU's full re-announcement. `assets/wsapi.json` was corrected in that release
  and this schema was not, so the two contract assets disagreed — on exactly
  the field a client filters on, and the OpenAPI half is the one generated
  client type packages carry as documentation. Both now describe the same
  behaviour and the same five values, `INIT` marked as having no producer on
  this broadcast. No field, status or schema shape changed — but ADR 0028 ties
  the contract version to the assets rather than to their semantics, so any
  edit to `openapi.yaml` is a minor bump: `api_version` 7.14.0 → 7.15.0, and
  the schema digest is regenerated with it.

## [0.65.2] - 2026-08-27

### Fixed

- **A CCU reconnect announced every device in the installation as newly
  created.** The daemon answers the CCU's `listDevices` with an empty array, so
  the CCU re-announces its complete inventory through `newDevices` after every
  reconnect. That announcement was passed straight through as one
  `device.created` event per device: the WebSocket `device.{address}.lifecycle`
  plane broadcast the whole fleet to every subscriber, and a client had no way
  to tell a genuine pairing from the CCU repeating itself. The creation source
  made it worse by being inverted — a re-announced device was reported as `NEW`
  while a device the daemon had never seen was reported as `REFRESH`, so a
  client filtering on `NEW` received exactly the noise and missed every real
  arrival. Only an address the device registry does not already hold is
  announced now, and the source says which kind of news it is: `NEW` for a
  pairing, `REFRESH` for a factory-reset re-pair where the device kept its
  address but rebuilt its channels. Descriptions carried by a re-announcement
  are still stored, so an updated firmware revision still reaches the model.

### Added

- **`expires_at` on `GET /api/v1/auth/me` and the login response.** A client
  that resolves its credential once and keeps the snapshot — a WebSocket
  captures its identity at the upgrade — could not learn when that credential
  dies, even though the daemon closes the connection the instant it does. Both
  responses now carry the deadline in UTC, absent when the credential has no
  server-side expiry. `POST /auth/login` reports the lifetime of the session it
  just issued. The admin token list carries the same information but is
  addressed by a fingerprint a client does not know for itself, so a rotation
  could only ever be discovered through the resulting 401.
- **`expires_at` on the WebSocket `{op:"reauth_ok"}` acknowledgement**, so a
  long-lived connection that has just refilled its credential in-band can
  schedule the next refill without a REST round trip. The `reauth` op itself is
  now documented in `assets/wsapi.json`, alongside the other envelope
  operations.
- **The Config UI warns before the session ends.** A session is an absolute
  12-hour window that activity does not extend, so the first sign of a lapsed
  one used to be a bounce to the login screen mid-task — and, because the
  daemon closes the WebSocket at the same instant, a reconnect loop against a
  socket that could never come back. The SPA now reads the deadline from the
  identity, shows a banner for the last 15 minutes with a "sign in again"
  action, and hands over to the login view exactly when the credential lapses.
  A deployment whose credential has no server-side expiry — HA Ingress, Basic
  auth — never sees the banner.

### Removed

- `EventCoordinator.EmitDevicesCreatedEvents`, which had no caller outside its
  own test.

## [0.65.1] - 2026-08-27

### Fixed

- **Bridged endpoints served clusters their Matter device type does not
  specify.** The Device Library says, per device type, which clusters an
  endpoint may serve and which it only consumes from another endpoint as a
  client; serving a client-only cluster is as non-conformant as serving one the
  type never names. Three endpoint shapes did it. Thermostat endpoints served
  TemperatureMeasurement and RelativeHumidityMeasurement, both named for
  0x0301 as client clusters — the readings already reach controllers as their
  own TemperatureSensor and HumiditySensor endpoints, and Apple takes the
  temperature from the Thermostat cluster's own LocalTemperature, so both are
  simply gone. The switch endpoint of a metering plug served
  ElectricalPowerMeasurement and ElectricalEnergyMeasurement, which
  OnOffPlugInUnit does not name at all; the metering channel now projects its
  own ElectricalSensor endpoint (0x0510) carrying both plus the PowerTopology
  cluster that device type makes mandatory. And a battery data point
  materialised as an endpoint with no device type — its DeviceTypeList was
  BridgedNode alone, which Apple files under "Other"; PowerSource now rides on
  one of the device's own endpoints, where BridgedNode specifies it. Alexa
  recognises a bridged endpoint only by the clusters its device type
  specifies, so each of these cost visibility there.

  Controllers re-learn the affected devices once: a metering plug gains an
  ElectricalSensor endpoint whose readings used to sit on its switch endpoint,
  and a battery device loses the typeless endpoint its battery level used to
  occupy. Existing exposure allowlist rows keep working — the electrical
  parameters are still gated individually, they now feed one shared endpoint.

### Added

- **Voltage, current and frequency reach the electrical cluster.** They were
  declared in the ElectricalPowerMeasurement attribute table and hardcoded to
  null, pending a multi-source projection. A metering channel's POWER, VOLTAGE,
  CURRENT and FREQUENCY parameters are now consolidated into one measurement
  group behind the single ElectricalSensor endpoint, so all four attributes
  carry live readings instead of one.
- **Device types carry their cluster requirements, and clusters their own
  surface.** The matter.js schema snapshot now records, per device type, which
  clusters the Device Library specifies for it and on which side, and per
  feature its FeatureMap bit position. Guards hold the Matter surface against
  it at three levels: which clusters an endpoint may mount, whether every
  mandatory and feature-gated attribute of a mounted cluster answers a read,
  and whether every mandatory command reaches `AcceptedCommandList`. They walk
  the whole hydrated device fleet, so a drift fails the build rather than a
  pairing.

  The attribute levels came up clean across 2772 mandatory and 1965
  feature-gated reads. The command level found the Groups and ScenesManagement
  stubs: both clusters are mandatory on the light and plug device types and
  neither accepts its mandatory commands, because Matter models groups and
  scenes as node-level tables a bridge keeps itself rather than as anything a
  CCU provides. Recorded as declared gaps that say so.

- **Connection latency measured a fraction of the path it was named after.**
  The hub's `connection_latency_ms` metric — the sensor MQTT discovery
  declares, the hub data-point list enumerates and `/system/ccu` reports —
  carried the duration of one JSON-RPC `Interface.listInterfaces` call taken on
  the reconciler's five-minute cadence. That is a single one-way surface: it
  covers neither XML-RPC, nor BIN-RPC (CUxD), nor the callback leg on which
  every event actually returns. It is now fed by the matched PING→PONG pair,
  which leaves over each interface's own transport and returns as an event on
  the callback server, on the 30-second connection-check cadence. Every backend
  that declares the ping/pong capability contributes, so the reading spans both
  transports.
- **The measurement that did cover the full round-trip was never recorded.**
  `metrics.MetricKeys.PingPongRTT` existed and `Aggregator.RPC()` read the
  `ping_pong.rtt` prefix back for its `avg_latency_ms` / `max_latency_ms`
  fields, but no production code ever wrote that key — the ping/pong tracker
  computed the round-trip and kept it to itself. Both fields therefore reported
  a constant zero in the diagnostics `rpc` section for every deployment. The
  matched-PONG path now files the sample per interface.
- **Probe durations were truncated to whole milliseconds.** A CCU answering in
  under a millisecond on the same LAN read as `0`, which is indistinguishable
  from "never measured" and defeats the value-equality dedup on any aggregate
  storing it. Connectivity-probe durations are now fractional milliseconds.

### Added

- **Client→daemon latency over the WebSocket.** The heartbeat now carries an
  opaque `echo` token; a client that copies it into its `pong` gets the
  measured round-trip back on the following `ping` as `rtt_ms`, and the SPA
  shows it in the connection badge's tooltip. Echoing is optional — a bare
  `{"op":"pong"}` remains a valid heartbeat and simply stays untimed, so
  clients written against wsapi 1.1 are unaffected. This leg is deliberately
  not a hub data point or an MQTT sensor: the distance belongs to the viewer,
  not to the CCU, and one daemon serves a LAN tab, an Ingress-tunnelled add-on
  and a public host at once.
- **Broker and controller latency in diagnostics.** New gauges
  `ws.heartbeat_rtt_ms`, `mqtt.publish_ack_ms` and `matter.controller_rtt_ms`.
  Each is paired with a companion so "nothing measured" stays distinguishable
  from "measured as zero": a connection count for the WebSocket, and a
  cumulative `*_total` for the two windowed probes — window occupancy
  saturates after the first burst and then reads identically whether the
  median describes the last minute or the bring-up of a daemon that has been
  running for days. The MQTT probe times only acknowledged (QoS ≥ 1)
  publishes — a QoS 0 publish is never answered by the broker, so timing one
  would report near-zero however sick the broker is. Note that state topics
  default to QoS 0, so on a stock configuration this gauge reports the
  discovery plane rather than the state plane. The Matter probe times only
  first-try acknowledgements (Karn's algorithm): after a retransmit the ACK
  cannot be attributed to either transmission.

### Changed

- The north-bound API contract moves to **7.13.0** and the WebSocket contract
  to **1.2** — additive only. `connection_latency_ms` keeps its shape and name
  but now documents the round-trip it actually measures; `heartbeat.echo` and
  `heartbeat.rtt_ms` are new and optional on both ends.

## [0.65.0] - 2026-08-24

Round 7 of the defect audit, and the round-8 live verification that followed
it. The audit's premise was that the remaining defects live in the seams
*between* artefacts rather than inside any one of them, and it held: the check
standing between the daemon and Prometheus could not fail for the reason it
existed, half the wiring seams described a consequence that does not follow,
and two subsystems could die leaving one boot warning behind.

Round 8 then held all of it against a real CCU and a real Matter commissioner
for the first time in three audit rounds. Nothing was found wrong — the
features behave the way the hermetic tests said they would, including the
derived-sensor exposure this release makes reachable at all.

The north-bound API contract moves to **7.12.0** — additive only: four
capability tokens, and the documented meaning of a token pinned down as
*configured*, not running. Liveness is what `/health` answers, and it gained
two components for it.

### Added

- **`north.matter.include_measurements`** — exposes the daemon's calculated
  sensors as Matter measurement endpoints alongside the device's own
  projection. Off by default: these are derived from data the CCU already
  reports rather than read from the device, so a controller showing all of
  them turns one wall thermostat into a row of sensors most users did not ask
  for. Matter-only — MQTT, HA-Discovery and REST have always carried derived
  sensors. See Fixed: until now there was no way to turn this on at all.

- **Four capability tokens for surfaces a client could not discover** —
  `mqtt.raw.v1`, `webhook.inbound.v1`, `diagrams.v1` and
  `admin.persistence.v1`. Each mirrors the condition that mounts its own
  surface. The SPA had been gating its diagram panel on `history.v1` as a
  stand-in, which reads as correct and fails in one direction: with recording
  on and no database the view renders, the editor opens, and every save is
  refused. It now requires both tokens, because it needs both things.

  The `Info.capabilities` description also gained the four tokens that already
  reached every client and appeared in no spec at all — `auth.ccu.v1`,
  `history.v1`, `mcp.write.v1` and `addon_self_update`.

  API **7.12.0** — additive: the field is an array of strings either way, so
  no schema diff sees this. A client that hardcoded the token set learns of
  the additions from the value-vocabulary register.

  What a token means is now written into the contract: **configured**, not
  running. A briefly unreachable broker is not a missing capability, and a
  token that came and went with connectivity would force every client to
  re-derive its feature set on each poll. Liveness is `/health`'s answer.

### Fixed

- **The Security & Safety domain could be absent for a whole daemon lifetime
  with nothing but one boot warning to show for it.** `wireSecurityService`
  returns nil when persistence is missing or construction fails, and both exits
  were log-only — so every hazard and fault surface answered as if the
  installation had no sensors while `/health` stayed green. The alarm service
  wired twelve lines away had recorded on the health tracker since it was
  written; security now does too, as the new `security` component.

- **A failed mDNS advertiser was equally silent**, and its symptom is remote
  from its cause: Matter commissioners find the bridge over mDNS, so pairing by
  QR code stops working with no other surface showing anything wrong. The new
  `discovery.mdns` component reports all three outcomes, including the one that
  produced no log line at all — an unusable listen port, where mDNS is enabled
  and nothing is announced. Recorded only while `north.discovery.mdns` is on.

- **Derived sensors were offered to Matter and then dropped.** Eight
  calculated sensor types — apparent temperature, dew point, dew-point spread,
  enthalpy, frost point, vapor concentration, operating-voltage level and
  derived binary state — are reported as mappable by `GET /api/v1/matter/exposable`,
  so the SPA listed them and an operator could allowlist them. The assembler
  then skipped every one, because the flag that admits them was never handed
  to the bridge and no config key existed to set it — while the field's own
  comment claimed operators enable it "via the config UI or daemon config
  flag". The new `north.matter.include_measurements` is that flag (off by
  default, Matter-only); a data point allowlisted while it was off starts
  materialising the moment it is switched on. The assembler had tests for both
  values of the flag, which is why nothing caught this: they set the flag
  themselves, proving the assembler could honour a value and never that the
  daemon supplied one.

- **`mqtt.BridgeConfig.DiscoveryObjectIDFmt` was a dead field with a false
  comment.** Its declaration was the only occurrence in the repo — no reader,
  and nothing applied the default its comment advertised. Discovery object IDs
  are built by dedicated code instead. Removed.

- **The exported schemas had no freshness gate**, so 71 of the 73 enums the
  Python types package is generated from could drift silently: a new wire value
  reached neither `assets/schemas/*.json` nor any client until somebody ran
  `make export-schemas` by hand, and nothing failed meanwhile. CI now
  regenerates and fails on a diff — the SPA's own generated types have carried
  that gate since they drifted a whole feature behind.

- **Three wiring pins were satisfied by `Field: nil`** — the field spelled out
  and the collaborator not handed over, which is exactly the state a pin exists
  to rule out. They asserted that a key appeared, not that anything arrived.

- **`AllChannelKeys` and `channelKeyBitmask` were two hand-maintained copies of
  the same twenty-four schedule channel keys**, with nothing connecting them. A
  ninth actor in the loop, or one renamed key, and the daemon offers a channel
  it cannot lock — with no failure anywhere; the schedule just stops locking
  one channel. Now pinned in both directions.

- **`assets/schemas/types.json` referenced three definitions it does not
  contain.** Its one composite type pointed at `Interface`, `ParamsetKey` and
  `Parameter`, which are enums published in a different document with a
  different shape that no same-document `$ref` can reach. A strict JSON-Schema
  consumer would have failed to resolve the type; nothing said so because
  there is no such consumer yet. The fields now declare their wire type and
  name the vocabulary, and a guard fails on any local `$ref` that does not
  resolve.

- A reason the visibility classifier can match but its precedence list cannot
  order was matched and then silently dropped, leaving the operator told the
  daemon does not know why a parameter is hidden. Nothing at unit level caught
  that; now something does.

- Two exemptions in the contract ledgers were hiding missing wiring seams
  rather than describing absent ones: an add-on update's progress never reached
  a WebSocket client, and no central polled the CCU for device firmware. Both
  declare a seam now.

- **58 request and response bodies were written inline in the OpenAPI paths,
  so no generated client could see them.** Every client produced from this
  document — `openccu-loom-types` among them — is generated from
  `components/schemas` alone, so an inline body reaches none of them: the
  endpoint works, the JSON is right, and the typed client simply has no model.
  It is the same shape as the `display_value` gap types 0.5.2 closed, and it
  had grown to 28 of 146 responses and 30 request bodies.

  All 58 are now named components reached by `$ref`. **The wire is unchanged** —
  verified by dereferencing both specs and comparing the path trees, which are
  identical. What changes is that a client can finally hold a type for them:
  `WiringSeam`, `ReliabilityState`, `DiscoveredCentral`, `IgnoredCentral` and 54
  request/response models named after their operation.

  A guard now fails on a body that declares properties inline, so the gap
  cannot grow a third time. Free-form bodies (`additionalProperties` with no
  properties), scalars and file uploads are deliberately not covered: there is
  nothing to generate, and forcing them into components would mint an empty
  type per endpoint.

  API **7.10.0** — additive: 58 schemas, no path or operation changed.

- **Three `DataPointKey` properties in `assets/schemas/types.json` pointed at
  definitions that were not there.** `interface`, `parameter` and `paramset_key`
  each carried a `$ref` into `#/definitions/`, which holds only `DataPointKey`,
  `ParamValue` and `ValueKind` — so no generator could resolve them and the
  Python types package saw three unusable properties. They are plain strings
  now, each naming the enum in `enums.json` that supplies its vocabulary.

  API **7.11.0** — additive: an unresolvable reference becoming a resolvable
  type takes nothing away from a consumer that could not resolve it.

## [0.64.2] - 2026-08-24

The round-5 measures, finished. The composition root now states its wiring as
data a test can read, the MCP/REST parity backlog is empty, and the seventeen
contract guards that stayed green when what they protect was removed now
either bite or are gone. The two findings round 4 carried forward are closed
too — a backup restore that left the daemon serving the old configuration, and
an MQTT save that reported success and changed nothing.

The manifest now covers ordering, which is the class two audits kept hitting: a
collaborator handed over after the thing that reads it has already started. That
compiles, runs, reports nothing, and leaves the feature off. The north-bound API
contract moves to **7.9.0** — additive only: one admin-only diagnostics
endpoint, two fields on the config-section save response, and four on a
diagnostics payload.

### Added

- **`GET /api/v1/diagnostics/wiring`** (admin-only) — the seams the running
  daemon declared as it wired them (ADR 0065). Each entry names the seam, the
  collaborator, its ordering constraint relative to south-bound bring-up, and
  what stops working when it is absent.

  Absence is what the endpoint is for. A wiring line that is deleted, skipped
  by a nil guard or never reached leaves nothing else behind: the daemon
  starts, reports healthy and serves every endpoint. Two full-codebase audits
  found that class of defect repeatedly in `cmd/`, which carries roughly twice
  the repository's average defect density, and the reason it kept surviving is
  that "is X wired" was only ever answerable by reading. It also distinguishes
  a seam this build never wires from one this operator has not switched on —
  `history.recorder` and `webhook.outbound` are config-gated and simply absent
  from a default deployment.

- **Eight MCP tools** — `list_groups`, `list_areas`, `list_interfaces`,
  `get_measurements`, `list_hidden_parameters`, `get_energy`, `list_links`,
  `list_schedules`. Each closes a REST domain an assistant could not read at
  all; every one is read-only, and the write halves of those facades
  (interface reconnect, un-ignore edits, schedule writes) are deliberately not
  projected. The declared backlog they were tracked in is now empty.

- **Ordered wiring seams.** A once-only seam can now declare which boot
  boundaries it must be attached before and after, and the manifest evaluates
  that at the moment it attaches — the only moment at which the answer is a
  fact rather than a reading of the source. `GET /diagnostics/wiring` reports
  the constraints and any that were already broken.

  Thirty-one seams are declared across the daemon — nineteen per-central
  observers, six ordered, six with no constraint — and every wiring function in
  the composition root now either declares one or records why it has none. That
  includes the config-store crypto, whose absence writes CCU passwords to the
  database in cleartext while every surface reports success; it sits before the
  central registry exists, which is why the manifest is now built ahead of the
  registry rather than by it. The one to know about is
  the webhook's alarm bus:
  `Outbound.Start` reads it once and subscribes, so a bus handed over after the
  north bridges start is stored and never read. No alarm or security event
  would ever be forwarded, the setter returns nothing, the daemon reports
  healthy, and every static guard stays green. That constraint used to live in
  a comment five hundred lines from the `StartAll` it talked about; moving the
  call across the boundary now turns an end-to-end test red with the
  consequence spelled out.

- A guard over the test helper every router-level contract guard builds on.
  `fullyWiredRouterDeps` fills 68 of `rest.Deps`' 140 fields, so those guards
  are blind to whatever the rest govern — which already cost one silently
  vacuous test. Every unfilled field now carries the reason its absence is
  harmless, and a new dep joining the nil set fails until somebody decides.

### Changed

- Every per-central registry observer now attaches through
  `Registry.OnRegisterDeclared`, which records the seam before wiring it.
  Three guards keep it honest: a raw `OnRegister` anywhere in production
  fails, so does a duplicated seam name, and an end-to-end test boots a daemon
  against a not-yet-ready CCU and compares what it declares against what the
  composition root is supposed to attach.

- The `hub` REST domain is now recorded as a deliberate MCP exemption rather
  than a backlog item: it is a single-fetch aggregate for clients building hub
  singleton entities, and every part of it is already projected individually.

- `central.Unit` no longer registers an `accept_device_inbox` service method.
  The hook behind it had no production caller, so the only path that could
  reach it answered "not wired"; the live accept runs through
  `DeviceAdminDomain.AcceptInboxDevice`. The central's remaining
  `payload.Source` implementation stays — ADR 0007 makes it mandatory — and now
  says plainly that nothing dispatches central-level services yet, instead of
  naming adapters that do not read it.

### Fixed

- Four service hooks on `central.Unit` that nothing ever wired are gone:
  `SaveFiles`, `ValidateConfigAndGetSystemInformation`, the hub-logout hook,
  and the `QueryFacade` state-path surface behind
  `SetHubStatePathProvider`. Descriptor persistence moved to a write-through
  sink long ago, so the shutdown flush step in `Stop()` sat behind a nil check
  that was always true; the JSON-RPC logout runs from the closer `WireHub`
  returns. As a result `ServiceWiringComplete` was permanently false in a
  running daemon — it demanded six hooks of which production wires three, and
  its unit test wired all six itself and so never saw it.

- Seventeen contract guards stayed green when the production line each one
  names was removed. Ten now bite — among them the Home Assistant discovery
  templates, which were checked by substring and are now rendered, exposing a
  test-side Jinja evaluator that skipped the `is not none` clause it was meant
  to be checking. The rest pinned behaviour nothing wires and were deleted.

## [0.64.1]

A contract correction and the guard that would have caught it. The
north-bound API moves to **7.7.0** — additive: one payload field that the
daemon had been sending all along without documenting it.

### Fixed

- The `datapoint.value_changed` broadcast documents `display_value`. The
  field shipped with 7.2.0 on both planes — the REST data-point summary and
  the WebSocket push — but only the REST half was written into
  `assets/openapi.yaml`. Generated client packages take their models from
  those components, so the typed WebSocket payload had no such field and no
  client could read it however faithfully the daemon sent it. A consumer
  that seeds a reading from REST and updates it from the push was then
  stuck with the seeded projection: a dimmer whose raw `LEVEL` moves to
  0.8 kept announcing the bootstrap "42 %", which is the exact
  disagreement `display_value` exists to prevent. The wire is unchanged;
  what changes is that the field is now part of the contract.

### Added

- A field-level parity guard over every WebSocket broadcast payload
  (`tests/contract/ws_payload_field_parity_test.go`). The existing
  cross-asset guard proved a payload schema *exists*; nothing compared what
  was inside it, which is why a field could be emitted for a whole API line
  without being documented. It now fails in both directions — a Go field the
  spec omits reaches no generated client, a documented property nothing
  emits arrives as a permanent null — and a second guard fails when a new
  broadcast joins the contract with no such check at all. Eight payloads
  whose Go type lives outside `internal/north/rest/ws` (the Matter family,
  and two unexported adapter structs) are recorded as documented coverage
  holes rather than quietly skipped.

## [0.64.0]

Three feature requests from the field and the two fix rounds that never
shipped on their own. The Backups view now says where the archives live and
lets an operator delete one, MQTT finally exposes the daemon's own liveness
as an entity, and a garage door's ventilation position gets a control Home
Assistant can see. Underneath: a bring-up that reconnected a CCU that was
never gone, Matter boot events that were evicted before a controller could
read them, and classic BidCos thermostats whose composed values never bound.

It also carries the tail of the round-4 full-codebase audit: the 43 findings
that 0.62.0 and 0.63.0 left open, re-verified against the tree before being
worked. Two of them turned out to be decisions rather than defects and are
recorded as such. The north-bound API contract moves to **7.6.0** — additive
only: `covered_ms` on a history bucket, `index_healthy` on the security
snapshot, two WebSocket broadcasts and three read-only MCP tools.

### Added

- The daemon's own liveness is now visible on every north-bound surface, not
  just as an absence. A daemon that goes away — a CCU reboot, an add-on
  restart, a killed process — used to leave every Home Assistant entity
  unavailable with nothing anywhere saying why, and nothing an automation
  could act on.
    - **MQTT**: a "Daemon connection" binary sensor per central, reading the
      retained bridge status the broker also carries the last will on. It
      deliberately carries no availability block of its own, because pointing
      it at the topic it reads its state from would make it unavailable in
      exactly the situation it exists to report.
    - **WebSocket**: a `daemon_status.changed` broadcast on the daemon-level
      topic `system.daemon_status`, emitted during a graceful shutdown before
      the servers stop. A WebSocket client has no broker holding a last will,
      so a stopping daemon and a dropped network looked identical to it; now
      the daemon says which it is while it still can. A killed daemon cannot,
      and that stays the client's own job to detect.
    - **REST**: `GET /api/v1/hub/data-points` declares a `daemon_connection`
      singleton, so a client can build the entity — and name it — without
      hard-coding a name the daemon owns. Its value is true by construction
      there; the negative state comes from the broadcast above or from the
      client's own connection.
    - **Config UI**: the connection badge distinguishes an announced shutdown
      from the ordinary self-healing reconnect, which read as "wait a moment"
      when the thing being waited for had gone.
- Backups can now be deleted from the Backups view, one archive at a time.
  Until now the only way an archive left the daemon's storage was the
  scheduled-backup rotation, so an operator who imported the wrong file, or
  filled a USB stick with manual backups, had to reach for a shell on the
  host. The confirmation names the archive rather than asking about "this
  backup" — the list holds several per CCU and the delete is unrecoverable.
  Every delete is recorded in the audit log. New endpoint
  `DELETE /api/v1/backups/{id}`.
- The Backups view now names the directory the archives are kept in, together
  with how many there are and how much space they take. That path was
  effectively unknowable from outside the daemon: `backup.dir` is empty in the
  common case, and on a CCU add-on install the service script resolves it at
  every start from the CCU's own backup target — so it varies per installation,
  changes when a USB stick is plugged in, and appeared nowhere but the start-up
  log. A daemon that could not create its archive directory at all now says so
  in the same place, instead of showing an empty list that looks like "no
  backups taken yet". New endpoint `GET /api/v1/backups/storage`.
- A vent-capable garage drive (HmIP-MOD-HO, HmIP-MOD-TM) now exposes its door
  mode as a select entity offering closed, ventilation and open. Home
  Assistant's cover platform has no ventilation state, so the position was
  previously reachable only by setting a cover position that happens to land
  between the closed and open thresholds — an interaction nothing can discover,
  label, or read back. The select reads `DOOR_STATE` and writes `DOOR_COMMAND`,
  and holds the mode just commanded while the door travels rather than dropping
  to unknown for the whole movement.

### Changed

- **Breaking for paired garage doors:** a garage drive now bridges to Matter as
  a Closure (device type 0x0230) carrying the ClosureControl cluster, not as a
  WindowCovering. WindowCovering has only a lift percentage, so the ventilation
  stop had to be encoded as a position near the middle — a slider region no
  controller can label, and one a read cannot tell apart from a door resting
  halfway. ClosureControl names the stop. Because an endpoint's device type
  changing reads to every ecosystem as a different accessory, a drive already
  paired as a window covering has to be re-added. See ADR 0064.
- Combined data points now describe their own north-bound projection instead of
  being dispatched by concrete type. Adding one used to mean remembering to
  extend a type switch in the event bridge, a discovery builder, and the command
  sink; a combined data point that missed any of them attached to its channel,
  published nothing, and was indistinguishable from a working one. A new guard
  fails when a combined type declares no projection. Existing entities — the
  timer number, the level and colour sensors — are unchanged.
- The garage cover's discovery body no longer declares `vent_command_topic`.
  Home Assistant validates the cover body against a closed key schema and has no
  field for a vent command, so the key never reached an entity. The select above
  replaces it.

### Fixed

- Unpairing a device now takes its Hidden/Locked channel overrides with it. The
  store method and the doc comment saying it runs on unpair had both existed
  for releases; nothing called them. A CCU reuses channel addresses, so a
  replacement paired in after a hardware swap silently inherited the previous
  device's visibility decisions — a channel hidden years ago stayed hidden on
  hardware that had never been configured. A cache-clear re-init is excluded:
  it removes every device without the operator asking for any of them to go.
- A device the CCU no longer reports disappears from the north-bound planes at
  cache-clear time instead of lingering until the next daemon start. The
  re-init used to announce every removal, which also told the persistent-cache
  evictors to run — so a clear scoped to one device wiped the whole central's
  cache. Suppressing the announcement fixed that and broke the other half:
  MQTT never retracted the discovery config, the WebSocket never reported the
  deletion, and the event bridge never released the device's live
  subscriptions. The removal event now carries a teardown marker, so the two
  groups of consumers can want opposite things.
- History and energy averages are time-weighted. CCU parameters arrive
  push-driven, so sample spacing is irregular by construction and a mean over
  the sample count reported a mostly-idle POWER series as several times its
  true average. The weighting lives in its own columns (migration **007** of
  the history schema): `count` keeps meaning a sample count, and rows written
  before the change keep reporting the sample mean they were computed with
  rather than being silently re-weighted to nothing.
- Two system variables whose names differ only in punctuation no longer
  collide. `Alarm: Küche` and `Alarm Küche` both slugged to the same
  `unique_id`, and Home Assistant kept whichever discovery config arrived
  first and dropped the other variable's entity outright — permanently, since
  the payload is retained. Sysvar identity is now the CCU's own numeric
  variable id. Every sysvar entity re-keys once on this upgrade; in exchange, a
  rename in the CCU WebUI no longer orphans the entity's history and every
  automation built on it. The client-side migration is in
  `docs/external-clients/ha-unique-id-migration.md`.
- CCU coordinates — host, serial, ports, WebUI URL — are admin-only on every
  surface that carries them. `GET /centrals` had narrowed them for non-admins
  since the role model landed; `GET /config`, `GET /system/ccu` and the LAN
  discovery list never did. `GET /service-messages/suppressed` is
  operator-tier, matching what the specification has always published, and
  `GET /devices/{addr}/icon` is behind authentication: the artwork is not
  sensitive, but the route answered differently for a known and an unknown
  address and was an unauthenticated existence oracle for the whole inventory.
- A browser replaying a stale HTTP Basic credential no longer locks its own
  source out of logging in. The per-IP credential throttle was mounted on the
  whole HTTP surface, so one page load's worth of asset requests drained the
  budget before the page finished rendering. It now covers the API subtree
  only; the budget stays shared with the login route on purpose, because it is
  one credential space.
- A renamed device reaches every plane. MQTT already learned about it; the
  WebSocket now broadcasts `device.metadata_changed`, and the Matter bridge
  reassembles, so an accessory no longer keeps its old name in Apple Home and
  Google Home until the daemon restarts. A week-profile change likewise
  broadcasts `schedules.changed` — and only when the profile actually changed:
  a re-load that fetched the same profile back is not a change, which is what
  the boot, reconnect and rename warm-up passes were reporting as one.
- `PUT /centrals/{name}` keeps the fields a request omits instead of resetting
  them to zero. A client following the published schema, where those fields are
  optional, silently wiped port, TLS settings, credentials and serial.
- Smaller: the security snapshot reports whether its classification index is
  still current; a failing alarm output marks only the panels it actually
  serves unavailable, not every zone plus the master during an alarm; removing
  a central retracts its raw hub topics as well as its discovery configs; the
  access log stopped emitting `request_id` twice, which some log pipelines
  reject outright; MCP gained read tools for Matter, backups and add-on
  updates; an invalid `north.mcp.path` no longer aborts the boot with no way
  back in; and the un-ignore withdrawal runs before the suppression pass that
  used to hide its effect.

- A CCU backup triggered through the hub's `backup.trigger` command no longer
  leaves its archive on the CCU. The command runs the CCU's own backup tool
  into `/usr/local/tmp/last_backup.sbk`, and that file stayed there until the
  next backup overwrote it — several megabytes nothing ever read, since the
  daemon downloads its own copy through a different path. The status poll now
  records the archive's name and size before removing it, and answers later
  polls from that record, so a finished backup still reports as finished with
  the name it was given. Reported in #584.
- The daemon no longer runs a connection recovery against a CCU that was never
  gone. Bringing an interface up walks its client from `CREATED` to
  `CONNECTED`, and each intermediate step was announced on the event bus. While
  the walk runs no interface reports connected, so the central-state evaluation
  read an ordinary bring-up step as an outage, demoted the central to `FAILED`,
  and the recovery coordinator reconnected the interface it was still
  establishing. The visible cost was on every daemon start: the reconnect
  forced all devices unavailable and back, so MQTT, REST, the WebSocket and the
  Matter bridge each announced the whole fleet dropping offline and returning a
  second later — Home Assistant entities went unavailable on every restart. The
  bring-up walk now publishes a single event for its outcome, and the recovery
  coordinator only accepts an interface once its bring-up has reported a
  result; before that the interface belongs to the bring-up, which carries its
  own retry.
- Matter controllers can read the `BasicInformation.StartUp` and
  `GeneralDiagnostics.BootReason` events again. Both are emitted once at boot
  and both are Critical, but the event buffer capped each priority class
  separately — 64 Critical records, evicted oldest-first, regardless of how
  empty the other classes were. The reconnect above flipped every bridged
  device's `Reachable` twice, and on a 36-device central those 72 events were
  enough to push the two boot events out. Apple Home waits for them as part of
  its Subscribe-Initial state machine. The buffer now follows matter.js'
  harvesting model: one buffer across all priorities, non-critical classes
  floored so neither can starve the other, and Critical records dropped last.
- Four Matter events carried the wrong priority against matter.js HEAD:
  `BridgedDeviceBasicInformation.ReachableChanged` (both emit paths) and
  `Switch.InitialPress` / `Switch.LongPress` were Critical where the gold
  standard declares them `info` — which is also what their own sibling
  emitters, `ShortRelease` and `LongRelease`, already used. Two of the unit
  tests asserting the wrong value cited the matter.js line that says the
  opposite. Every emitted event's priority is now checked against a table read
  from matter.js, and an emitter whose event is not in that table fails the
  build rather than picking a priority unreviewed.

- Classic HM-CC-TC thermostats (and the ZEL STG RM FWT that shares their
  profile) now report a temperature and a target temperature. Their setpoint is
  `SETPOINT` on the regulator channel while the thermostat entity is
  materialised on the weather channel, and their current temperature is
  `TEMPERATURE`, not the `ACTUAL_TEMPERATURE` every other thermostat family
  uses. Neither value bound, so the entity showed no temperatures at all, its HA
  discovery named state topics nothing publishes to, and `set_temperature` wrote
  a parameter the device does not have.
- The classic RF wall thermostats (HM-TC-IT-WM-W-EU, HM-CC-VG-1) now report
  their humidity on the thermostat entity — they publish it as
  `ACTUAL_HUMIDITY`, which the fixed `HUMIDITY` lookup never found.
- A wire value that belongs to a custom data point on *another* channel now
  updates that data point's aggregate on the WebSocket and MQTT planes. The
  fan-out keyed on the channel the value arrived on, so an HM-CC-TC setpoint
  change reached no aggregate surface until an unrelated parameter on the
  thermostat's own channel happened to change.

### Internal

- Four new guards, each written because something had already gone past every
  existing one:
  - `TestRESTRouteTiersMatchOpenAPIScopes` compares the authorization scope the
    specification publishes against the gate the router actually wraps a route
    in — measured by walking the real middleware chain, not by matching names
    in source. It found four further divergences on its first run; all four
    turned out to be legitimate and are recorded with the middleware that
    guards them.
  - `TestAPISurfaceChangesCarryTheRightBump` holds the API version to the
    policy written on the constant itself: a removal, rename or retype demands
    a major, an addition at least a minor. It carries a hand-maintained
    register for the one thing no schema diff can see — a field that keeps its
    name and type and changes meaning.
  - `TestEveryWireFunctionHasAProductionCaller` closes a hole in its sibling
    setter guard, which models a wiring seam as a method and so could not see a
    free `Wire*` constructor. That is exactly how the channel-flags eviction
    above shipped complete and unwired.
  - The reachability snapshot tests are renamed to say what they check — the
    committed snapshot's shape — and gained the ceiling they were missing, so a
    regenerated snapshot carrying more dead identifiers than the last one fails
    instead of passing quietly.
- `payload.ExtraProperties` gives a mutex-guarded field a way back into the
  payload harvest. Moving `Device.name` behind a lock was correct — the rename
  path writes it while four north-bound planes read it — and took it out of the
  reflection that builds the HA-Discovery device block, so every device fell
  back to its raw address. Nothing failed; one test happened to notice.
- `north.mqtt.payload_format` is removed. It was validated, editable in the web
  UI and read by no production code. Migration **040** drops the orphaned key
  from stored config sections rather than bumping the section schema version,
  which would have discarded every operator's persisted configuration.
- Two audit findings are closed as decisions rather than defects, recorded in
  `notes/parity/by_design.md`: `raw_enabled` governs the raw topic plane and
  deliberately does not silence the state topics an entity's own discovery
  payload names, and failed Basic credentials deliberately share the login
  route's rate-limit budget.

- The boot-lifecycle events are pinned through the composition root: a test
  wires the Matter runtime and reads both events back over the same path the
  receive dispatcher uses for a controller's `ReadRequest`. Their only previous
  coverage was the nightly chip-tool job, which is why the regression stood for
  four days.
- Ten of the sixteen `crypto/ecdsa` deprecations Go 1.26 introduced are
  migrated to `ParseUncompressedPublicKey` / `ParseRawPrivateKey` /
  `PublicKey.Bytes`: Matter certificate decoding and root-key parsing, the CASE
  identity reconstruction, the CSA Test PAA fixtures, and five test helpers.
  Parsing is what validates, so this closed a real hole rather than only
  silencing a warning — the CASE identity was rebuilt from a stored scalar with
  a length check and nothing else, and a zero scalar (an empty or truncated
  column) produced a key whose public point is the identity. The daemon would
  have run the whole handshake and signed with it.
- SPAKE2+ moves its point arithmetic to `filippo.io/nistec` (BSD-3-Clause), the
  module Go's own deprecation notice names for low-level curve operations. The
  standard library has no replacement for what PASE needs — addition of
  arbitrary points, negation, an identity test — so `crypto/elliptic`'s
  deprecated methods were carrying it with a justification that claimed they
  were "the supported path". Two hand-rolled pieces disappear with the move:
  the negation, done by subtracting the Y coordinate from the field prime, and
  the RFC 9383 identity abort, which read `crypto/elliptic`'s `(0, 0)`
  convention. They are now `Negate()` and `IsInfinity()`. The wire output is
  unchanged, pinned by the matter.js parity vectors and the golden handshake,
  which pass byte for byte.
- `hmcli cache clear` takes a `--timeout` and applies it to the whole
  operation, defaulting to 60 s with `0` meaning no deadline — the same
  spelling every other command group uses. Both paths carried a fixed number
  before, and the offline one had them the wrong way round: `sqlite.Open` was
  capped at five seconds while the deletes that follow ran with no deadline at
  all. Opening the database is where any pending schema migrations run, so on
  slow storage the cap fired on the step the operator cannot influence, and an
  offline clear reported `open DB: context deadline exceeded` while nothing
  was wrong.
- The lint gate no longer floats on `@latest`. `gofumpt` and `golangci-lint`
  are pinned in `ci.yml`, so the job reports on the diff rather than on the
  calendar: `main` went from green to red overnight on an unchanged tree when
  golangci-lint 2.13.0 landed with staticcheck's Go 1.26 deprecation checks.
  `govulncheck` and `go-licenses` keep floating on purpose — a new
  vulnerability database *should* turn the build red without a commit. Three
  files are reformatted in the same change: local `make fmt` and CI had drifted
  apart on a rule gofumpt relaxed in v0.11.0, and they now satisfy both
  versions.

## [0.63.0]

The medium and low findings of the round-4 full-codebase audit — the tail
behind 0.62.0's critical and high ones. Around 130 of the 212 were fixed; the
rest were either already closed by the 0.62.0 wave, refuted on inspection, or
left open with the reason recorded, because a safe fix needed a design decision
rather than a local edit. Ships database migration **039**.

The north-bound API contract moves to **7.1.0**. Nothing the daemon *does*
changed incompatibly — the major step is because the specification was wrong
and now is not: `GET /diagnostics/capture` has always answered with an array
while the schema declared an object, and the nullable fields were written in
the OpenAPI 3.0 spelling inside a 3.1 document, where it has no meaning. A
client that generated code from the old schema gets different types out of the
new one, which is exactly what a major version is for. Everything else in the
contract is additive: new MCP tools, argument schemas for nineteen WebSocket
commands, and a few response fields — among them `filename` on a backup entry
and `display_value` on a data point, which carry the minor steps to 7.2.0.

### Added

- A data point now reports `display_value` — its value expressed in the unit it
  names, i.e. `value * multiplier` — on the REST summary, the WebSocket
  value-changed push and the channel ui-schema. `value` keeps its meaning: it is
  the raw CCU wire value and the write path sends it back unchanged. Render
  `display_value` when present and `value` otherwise; no client needs to do the
  arithmetic, and the two planes are guaranteed to agree so a reading seeded
  from REST does not jump on the first push.
- A backup entry now carries `filename`, the archive's name in the CCU's own
  convention (`<hostname>-<CCU firmware version>-<YYYY-MM-DD-HHMM>.sbk`),
  recorded when the archive was taken. The download is served under it, and a
  client no longer has to rebuild a name from an id that carries no firmware
  version — one of them stamped the daemon's build version into it instead.
- `backup.dir` moves the downloaded CCU archives off the daemon's data
  directory — for a mount that is not the daemon's state directory, or for the
  CCU's own backup target when running as add-on software.
- MCP gained tools to open and close an edit session, without which writing a
  MASTER paramset over MCP was unreachable — the write is fail-closed on an edit
  token and no tool could mint one. Also a schedule read tool.

### Changed

- MASTER paramset saves succeed for every writable parameter the configuration
  UI offers. The write was gated by the data-point-creation whitelist, which is
  not an authorisation list, so almost everything the read surface offered was
  refused on save.
- Sixteen WebSocket commands that the composition root never wires now answer
  not-implemented instead of unknown-command, which was indistinguishable from a
  typo, and a malformed frame gets an error answer instead of silence.

### Fixed

- Values whose CCU unit disagrees with the wire scale are no longer rendered a
  hundred times too small. A dimmer at 42 % arrives as `0.42` with unit `%` —
  the daemon reported the converting factor and expected every consumer to
  apply it, and none did. 499 visible data points across the fleet are affected
  (LEVEL, LEVEL_REAL, SATURATION, LEVEL_2 and friends). The conversion now
  happens once, in the north-bound projection; MQTT already did it in its
  discovery templates and is unchanged.
- `TIME_OF_OPERATION` reports days, the unit its factor converts into, instead
  of the CCU's seconds. The two disagreed, so a consumer applying the factor
  showed a day count labelled `s`. A guard now fails the build for any
  multiplier whose declared unit does not match what the daemon reports.
- Downloaded CCU archives no longer end up inside the CCU's own backups
  ([#584](https://github.com/SukramJ/openccu-loom/issues/584)). Installed as CCU
  add-on software the daemon kept them under `/usr/local/addons/`, which is
  exactly the tree the CCU packs into an `.sbk` — so every CCU backup carried
  all previously downloaded archives, and the next one carried those again. The
  archive directory now gets the `.nobackup` marker the CCU's own tar honours,
  and the add-on's service script points the daemon at the CCU's configured
  backup target (`CronBackupPath`, otherwise external storage) so the archives
  land beside the CCU's own instead of on its internal flash.
- Creating a CCU backup no longer builds it twice. The flow started
  `createBackup.sh` on the CCU, waited for it, and then downloaded an archive
  the maintenance CGI had built independently — the first one was never read. It
  cost a second full tar, gzip and signature run of the CCU's whole state, left
  a multi-megabyte file behind in `/usr/local/tmp` until the next backup, and
  spent most of the timeout budget on the half that was discarded.
- A dead CCU's leftovers: removing a central now also deletes its visibility
  overrides, channel flags, recording overrides and Matter exposures, so a CCU
  re-adopted under the same name no longer inherits the previous incarnation's
  settings.
- A MASTER value change is published on the master state topic and the
  WebSocket stream, so configuration entities no longer revert to their boot
  value after every write.
- Colour changes dirty-mark their Matter attributes; a colour set at the wall
  was never reported to a controller.
- Turning the raw plane off now silences the alarm and security raw topics too.
- The update entities no longer ship options Home Assistant drops, so an install
  in progress is visible; and a level data point whose descriptor reports no unit
  reaches Home Assistant as a percentage rather than a raw fraction.
- Wrong values reaching a device: switching a HmIP thermostat to a week program
  left it in manual mode with boost still on; the classic-RF offset surfaced the
  raw enum index; a setpoint was clamped a second time against a capability the
  descriptor does not know; ending an away period on classic RF submitted an
  empty window; the light on-time setter wrote a parameter pair that exists on no
  device; and the text display was offered icons and sounds it does not have.
- Alarm: a fault whose source left the model could never be closed, a walk-test
  session survived arming and afterwards swallowed real sensor events, a
  restored silenced incident kept a running counter, and a zone slug repaired by
  the migration was re-derived on every boot instead of being written back.
- Matter: a second subscription on one session destroyed the first because the
  keep-subscriptions flag was ignored, an oversized report aborted the whole
  subscription instead of downgrading one path, the access-control extension
  always read back as null, and removing a fabric left its extension data in
  memory for the next fabric to inherit.
- A non-finite float from the wire was accepted on the read path and then broke
  every north-bound JSON encoding — the collection endpoint answered 200 with an
  empty body, losing the healthy values alongside the bad one.
- Health: three boot-time components decayed to unknown ninety seconds after
  every healthy boot, because staleness was applied to one-shot facts.
- The daemon no longer aborts its boot over a stale MCP path while MCP is
  disabled; SQLite pragmas reach the whole connection pool rather than one
  connection; the tiered measurement deletes are transactional; and a config
  section written before the embedded-scope change is repaired by migration 039.
- The SPA stops discarding unsaved work when the locale, the expert-mode
  checkbox or a Settings tab changes, and shows localized text where it
  previously leaked raw tokens — including the CCU password dialog, which
  pre-filled the mask the API sends in place of the real value.
- On real CCUs: alarm system variables report their state, assigning a channel
  to a system variable works, device and channel rename works, suppressed
  service messages are shown, and programs report when they last ran.

### Internal

- The device-profile catalogue is maintained in this repository instead of being
  generated from the reference implementation ([ADR 0063](docs/adr/0063-self-maintained-device-profiles.md)).
  A profile that cannot express the truth for two devices sharing it — the HmIP
  door locks place the same field on different channels — was previously only
  correctable upstream or by a runtime workaround. `script/generate_profiles.py`
  is gone, the three catalogue files are ordinary source, and the pins that
  compared their size against the generator's own output are replaced by
  invariants over the catalogue itself. No behaviour changes; the profile data is
  byte-identical to what the last generator run produced.
- The text-display wire snapshots pinned an icon value the device does not have,
  so the guard was recording the defect rather than preventing it.
- Two batches independently added the same health flag and the same pair of MCP
  edit-session tools, and both extended the central-purge routine with different
  cleanups; the merge keeps one implementation of each and combines the cleanups.

## [0.62.0]

The critical and high findings of the round-4 full-codebase audit
(`notes/audits/2026-08-17-round4-audit-findings.json`). The north-bound API
contract moves to **6.3.0** (two endpoint descriptions clarified; no shape
change). Ships database migration **038**.

### Changed

- **Matter access control now fails closed.** A bridge with no ACL source
  attached refuses every operational (CASE) read, write, invoke and subscribe
  instead of granting them at any privilege. Commissioning over PASE and a
  commissioned controller with stored `AccessControl` entries are unaffected.
  Setups that deliberately run without stored entries can opt out explicitly.
- **MASTER paramset saves now succeed for every writable parameter the
  configuration UI offers.** Writability comes from the parameter descriptor
  rather than from the data-point-creation whitelist, which was never an
  authorisation list.
- More settings correctly report `restart_required`. Saving OIDC, the rate
  limiter, TLS paths, locale, logging, CCU data, reliability, persistence and
  several Matter and REST fields now lights the restart banner instead of
  silently doing nothing.

### Fixed

- **MQTT reconnects after a reload again.** Reloading MQTT from the SPA or by
  editing the config file bound the reconnect loop to the request, so the next
  broker restart took the whole plane down until the daemon was restarted. The
  hub publish worker was bound the same way.
- **A CCU adopted at runtime keeps its periodic jobs.** All 18 per-central jobs
  were started on the adopting HTTP request and died when it completed —
  including on the first-run wizard's path — so that CCU's hub data froze at
  its bring-up values and its health decayed to unknown.
- **A dead CCU is now reported as dead.** REST, MQTT and Matter kept every
  device online with frozen values for as long as the CCU stayed down.
- **A device going unreachable produces an availability broadcast** and a
  retained `offline` topic; the announcement was gated on the visibility of the
  very parameters that carry reachability, which are suppressed by default.
- **A graceful stop publishes `bridge/status = offline`.** Only a hard kill did,
  through the broker's last will.
- **Upgrading an install first started on v0.4.0–v0.25.x no longer disables
  HTTP Basic and Bearer authentication** (401 for the CLI, the Node-RED contrib,
  API-token WebSocket upgrades and every REST automation, with a green health
  endpoint and a working SPA login). Same for restoring a backup from those
  releases. Migration 038 repairs the affected rows.
- **Logout ends the session server-side for federated logins too**, and closes
  the caller's WebSocket connections in every scheme.
- **A WebSocket connection no longer outlives its credential.** Session TTL and
  bearer-token expiry were only evaluated on HTTP requests, so an expired
  credential kept full command authority — including alarm disarm — for as long
  as the client answered the ping.
- HTTP Basic no longer runs a bcrypt verification ahead of the rate limiter.
- **A CCU reboot no longer runs the alarm central-loss policy** — no fault entry
  per armed zone, and no siren on `central_loss: trigger`.
- **Stopping the daemon silences smoke-detector sounders and alarm lights**
  instead of leaving them latched with their only bound removed.
- **An enrolled sensor whose device disappears from the CCU is marked
  unavailable**, so a zone no longer reports ready-to-arm with an unmonitored
  opening.
- Alarm code verification no longer stalls sensor ingest and countdown timers.
- **CUxD entities survive the upgrade.** The stale retained discovery config is
  cleared before republishing, so Home Assistant keeps one entity per data point
  instead of an unavailable orphan beside a duplicate. Installs already on
  0.61.3/0.61.4 that have since restarted Home Assistant must delete the
  orphaned entities by hand.
- **Door and window contacts report their state** instead of staying `unknown`:
  enum-typed binary sensors published the value-list label while declaring
  `true`/`false` payloads.
- **Hub entities stop disappearing after a restart.** The discovery orphan sweep
  retracted configs the daemon was still publishing.
- Discovery emits `default_entity_id`; Home Assistant removed `object_id` from
  its schema in 2026.3 and dropped it silently.
- **A MASTER value change is published** on the master state topic and the
  WebSocket stream, so configuration entities no longer revert to their boot
  value after every write.
- **CCU alarm system variables report their real state.** Both real CCUs answer
  with an empty value for the alarm type; the state lives in a different
  accessor.
- **Assigning a channel to a system variable works**, and device/channel rename
  with it: both called an interface method no real CCU implements.
- Suppressed service messages are no longer dropped; programs report
  `last_executed`.
- Schedules of the HmIP-RGBW/LSC/DRG-DALI lights and the HmIP-HDM shading
  actuators are classified correctly instead of appearing as switches.
- Blind slat and level targets are no longer re-sent from a stale staged value.
- The HmIP-MP3P plays every tone it advertises; the HM-LC-RGBW-WM accepts colour
  commands and offers the effect names it really has.
- A stored central whose name the callback router rejects is no longer started
  silently receiving no push events.

### Internal

- Three guard families were shown by mutation testing to stay green while the
  defect they exist to prevent is reintroduced, and were repaired: the MQTT
  plane round-trip guards now observe the real publisher instead of a restated
  copy of it, the registry-walker guard follows the data flow rather than one
  syntactic form, and the dormant-capability pins assert that a capability is
  reachable at runtime rather than that a method name appears in the source.
- Every `cfg:` leaf is now either restart-required or carries a declared live
  read site, enforced by a completeness guard.
- The persisted config-section payload shape is pinned by a golden covering
  every leaf, so a change that makes old rows decode wrong is separated from a
  harmless new key.

## [0.61.4]

Follow-ups to the 0.61.3 post-release audit. The north-bound API contract moves
to **6.2.1** (a backward-compatible value correction, no shape change).

### Fixed

- **Classic-RF thermostat profile writes reach the CCU again.** `SetProfile`
  now writes `WEEK_PROGRAM_POINTER` to the device-root `MASTER` paramset (where
  the classic-RF thermostat family exposes it) instead of bundling it into the
  climate channel's `VALUES` paramset, where the CCU rejected it. The wire
  snapshots pin the two-call `VALUES`+`MASTER` split so the shape cannot
  regress. (Re-lands a fix that an earlier snapshot mismatch had reverted.)
- **Install-mode sensors seed correctly on a fresh start.**
  `GET /hub/data-points` reported `install_mode[].interface_id` as the bare
  interface name while the sibling `connectivity[].interface_id` and
  `GET /interfaces` use the wire id `<central>-<interface>`. A client keying the
  aggregate onto its interface list never matched, so the install-mode sensors
  stayed at their initial value until a pairing window happened to fire. The
  aggregate now reports the wire id (the dedicated
  `GET/POST /install-mode/interfaces` surface keeps its bare interface + separate
  central field). The field is now documented in the OpenAPI spec.

## [0.61.3]

Post-release audit of the 0.61.x fix wave: a full-codebase re-review found
defects the 0.61.0/0.61.1 audit-fix squash left behind, introduced, or never
covered. Every fix below carries a test that fails when the fix is reverted,
and each was confirmed by an independent adversarial pass. The north-bound API
contract gains a few backward-compatible fields and moves to **6.2.0**.

### Security

- **HTTP Basic credential guessing is now rate-limited.** The login limiter
  guarded only `POST /auth/login`, so `Authorization: Basic` on any route (e.g.
  `GET /auth/me`) could be brute-forced at unlimited rate; failed Basic
  verification now shares the same per-IP limiter.
- **A system-variable value can no longer inject ReGa script.** A value
  containing a newline broke out of the generated script's `!#` comment and ran
  on the CCU's privileged service session (reachable from REST/WS/MQTT sysvar
  writes). Control characters in script placeholder values are now rejected.
- **Logout closes the user's open WebSockets**, so a revoked session no longer
  keeps a privileged socket alive.
- **The Matter PASE brute-force lockout can no longer unlock itself** while a
  commissioning window is open.
- **MCP is properly gated**: `write_paramset` honours the MASTER/LINK edit lock,
  and value writes go through descriptor coercion like REST.
- **The WS `recording.*` commands are admin-gated**, audited, and arm the
  auto-stop timer, matching the REST route.

### Fixed

- **Connectivity plane** — the 0.61.1 wire-id move for #574 left the MQTT
  discovery seed and the alarm domain on the bare interface name, producing one
  dead HA entity plus a duplicate per radio and stopping the alarm domain from
  noticing a lost radio; both now use the wire id the event carries.
- **MQTT value encoding** — select/enum writes forwarded the option label
  instead of the VALUE_LIST index (every ENUM in the fleet is index-based), and
  VALUES writes skipped descriptor coercion entirely; both now coerce against
  the parameter descriptor.
- **Alarm code plane** — a code-free master DISARM and an ordinary correct PIN
  no longer lock out the code plane or mute the covert duress channel; a failed
  siren fire stays degraded until its own condition clears; a hidden duress
  journal row is recoverable by an admin.
- **Multi-CCU** — the HA device card, the CUxD `unique_id`, and the
  discovery-orphan sweep are all central-scoped, so two CCUs no longer merge,
  collide, or evict each other's entities; removing a CCU now retracts its whole
  hub plane.
- **System variables** — INTEGER bounds report their real min/max, a rename
  republishes the HA entity instead of freezing it, the WS list no longer races
  the refresh, and Create/Update surface the CCU's result (404/409) instead of
  a false 202.
- **Schedules** — door-lock schedules resolve to the lock domain (the action
  picker instead of an on/off switch), non-climate schedules refresh on
  CONFIG_PENDING, and the MQTT `schedule_domain` reports the real bucket.
- **Central bring-up** — a boot-time MQTT connect recovered on retry keeps its
  reconnect loop, an XML-RPC ingest that exhausts its retries no longer sticks
  in CREATED, and HmIP-DRG-DALI / HmIP-MP3P-LED are controllable again.
- **Matter** — the WebSocket broadcasts carry the `matter.` prefix their
  consumers dispatch on, structured attribute writes (GroupKeyMap,
  AccessControl.Extension) decode instead of failing, and the fabric-added
  broadcast carries the exact 64-bit ids.
- **Backups no longer lose the history database** — `history.db` is snapshotted
  consistently instead of copied live, so a restore keeps its rows.
- **Health & diagnostics honesty** — `system_health` and per-interface
  connectivity report "unknown" during a CCU outage instead of a frozen last
  value; a failed security-index rebuild reports a degraded state instead of a
  false all-clear; an operator's diagnostics-capture anonymise choice is
  honoured.
- **Config UI** — the Navigation & views editor no longer discards unsaved row
  edits on a mode toggle, the live log tail resumes after a daemon restart, a
  security-source exclusion is not silently re-included, and the schedule editor
  follows the device's real slot count.
- **Contract sync** — the locked custom-DP invoke returns 423 (not 502), the
  first-run wizard actually publishes MQTT entities, the config edit session is
  central-scoped on write, and the WS command schema documents the arguments its
  handlers require.

## [0.61.2]

### Fixed

- **The "Systemzustand" diagnostic sensor read 0 %, not the real score.** The
  0.61.1 fix for #575 wired the system-health metric to the daemon-global
  health tracker's per-central lookup, but the per-central health components
  (the heartbeat, the scheduler, per-interface reachability) are recorded on
  the central's own tracker, so that lookup found nothing and scored 0. The
  metric is now the central's own health-tracker aggregate, so it reflects the
  actual state; while that tracker is still empty at boot the metric is
  withheld (the sensor stays "unknown" briefly) rather than reporting a
  spurious 0.

## [0.61.1]

Three fixes for the Home Assistant integration "Homematic(IP) Local for
OpenCCU", all reported against 0.61.0. Each is a daemon-side correctness fix —
the daemon is the authority the integration reads — and none was covered by
the 0.60.0/0.61.0 audit, because all three cross the boundary into the
integration's own client and only surface there.

### Fixed

- **An already-integrated OpenCCU is no longer re-discovered on every Home
  Assistant restart.** HA de-dupes discovery by matching each CCU serial in
  the mDNS `ccus=` advertisement against the config entry's id (the
  `GET /system/ccu` serial), and the compare is case-sensitive. The daemon
  advertised the lower-cased routing form of the serial while `/system/ccu`
  reports it case-preserved, so an upper-case-hex CCU serial never matched and
  HA raised a fresh discovery card every restart. The advertisement now
  carries the exact `/system/ccu` serial.
- **The per-interface connectivity sensors no longer read "disconnected"
  forever.** The integration builds those sensors from `GET /interfaces`
  (keyed by the wire id `<central>-<interface>`) and looks their value up by
  the same id, but the daemon reported connectivity under the bare interface
  name (`HmIP-RF`), so the lookup never matched. Connectivity now carries the
  wire id on both the REST snapshot and the WebSocket push, matching
  `/interfaces`.
- **The "Systemzustand" (system state) diagnostic sensor no longer reads
  "unknown" forever.** Its value is the `system_health` hub metric, whose only
  producer — the reconcile pass — was never given a health probe, so the
  metric was never emitted. The reconcile pass is now wired to the daemon's own
  per-central health score, so the metric is produced on its slow cadence.

### Changed

- North-bound API contract version 6.0.0 → 6.1.0: the connectivity
  `interface_id` (REST `GET /hub/data-points` and the `connectivity.changed`
  WebSocket payload) is documented as the wire id `<central>-<interface>`.

## [0.61.0]

The follow-up to the 0.60.0 audit. A fresh full-codebase pass found defects
the previous audit had missed — the failure classes were different:
teardown/disable paths (the earlier audit hardened the setup side), success
reported before the effect landed, unvalidated config-flag combinations, and
half-applied fixes. Everything below was verified against the code, most of it
through a failing reproducer, before the fix landed; an adversarial review of
the whole branch caught and reverted the regressions the fixes themselves
introduced.

### Security

- **Revoking a bearer token now closes the WebSocket sessions it opened.**
  `DELETE` of an API token previously left any live WS connection that token
  had authenticated fully privileged until it disconnected on its own.
- **The operator channel lock is enforced on every write path.** A channel
  marked locked against control writes still accepted commands over MQTT and
  through the SPA tiles (REST/WS/MQTT/Matter custom data-point writes); the
  lock now gates them all. `MASTER`-paramset writes stay exempt by design.
- **HTTP Basic no longer bypasses CSRF**, and the WebSocket origin check no
  longer waives itself for a Basic `Authorization` header — a browser can
  replay cached Basic credentials cross-site, so the per-request-credential
  exemption was unsound for that scheme (bearer and the ingress scheme keep it).
- **Config-read and diagnostics endpoints match their declared admin gating.**
  `GET /config/effective`, `GET /config/sections/{section}`,
  `GET /diagnostics/rpc-recording` and the RSSI/sysvar-fetch WS commands are
  admin/operator-gated in line with the spec; central connection details
  (host, ports, username) are masked from non-admin identities.
- **A wrong alarm code aimed at an already-disarmed zone can no longer lock
  out the code plane** for every zone of that source (an MQTT/keypad
  denial-of-service vector), while duress detection on that path is kept.

### Changed

- **North-bound API contract version is now 6.0.0** (a breaking bump for
  generated clients). Two operations now state the request body they have
  always required — `DELETE /sessions/edit` and
  `POST /sessions/edit/heartbeat` both demand `key` and `token` — and the
  startup-capture schema splits into an honest read shape (responses always
  carry `anonymise`) and a write shape (an omitted `anonymise` still means
  "anonymise", the privacy-preserving default). No daemon behaviour changed;
  the spec now matches what the handlers already did.
- **The MQTT transport moves to `go-mqtt` v1.3.0** (its own 42-finding audit
  release; API additive). Flapping broker connections now back off
  exponentially, a broker-sent Server Keep Alive of 0 disables pinging per
  MQTT 5 §3.1.2.10, and shared-subscription filters match their delivery
  topics.

### Added

- **The Matter fabric list carries exact 64-bit identifiers.** Each fabric
  now reports `fabric_id_hex` / `node_id_hex` alongside the numeric fields.
  A JSON number holds only 53 bits, so every operational node id a real
  controller assigns was being rounded before it reached the UI; the hex
  fields are the 16-digit values controllers and chip-tool print, and the
  fabric view renders and sorts on them.
- **The change log distinguishes who wrote a paramset.** A write driven by
  an AI assistant over MCP is now recorded as such, told apart from a REST
  or WS edit, so an unexplained configuration change can be attributed.

### Fixed

- **`config import` of a default export no longer wipes stored secrets.** A
  plain `config export` redacts every secret and the CCU passwords; importing
  that document back (a rollback, say) used to overwrite the real values with
  null. Import now keeps each stored secret unless the document explicitly
  carries a replacement, and marks a redacted export so it is recognised.
- **`backup restore` removes the target database's stale WAL/SHM sidecars**,
  so SQLite can no longer replay the previous database's write-ahead log over
  the restored file; **`backup create` can no longer migrate or create the
  live database** — it opens the source strictly read-only.
- **The Home Assistant discovery plane no longer publishes entities that stay
  permanently unavailable.** Turning discovery on now implies the raw state
  plane (with a logged warning) instead of registering thousands of entities
  whose availability topics were never written; a retracted availability topic
  is republished after a broker restart; the first-run wizard's MQTT step now
  actually enables the bridge, and enabling MQTT at runtime publishes without
  a restart.
- **A disarmed alarm zone seeds its sensor state at boot.** After a restart an
  open window that had not pushed a fresh value left the zone reporting ready,
  so it could be armed with a contact standing open; the open-contact blocker
  now sees reality. The SPA alarm views no longer write one zone's policies,
  outputs or sensors into another when the zone selector changes mid-edit.
- **A single-interface CCU that goes offline now reports failed** instead of
  staying `RUNNING` forever, and a device replaced on the CCU is evicted from
  the domain model so it stops appearing on REST and in the SPA.
- **Matter lifecycle and protocol correctness.** Fabric revoke / factory reset
  run the full session-and-subscription teardown (not just a store delete);
  `BasicInformation.UniqueID` stays stable across a bridge rename; the PASE
  brute-force cap is enforced with an expiry so a LAN host cannot permanently
  disable pairing; SPAKE2+ rejects degenerate verifiers without panicking;
  unsupported commands answer `UNSUPPORTED_COMMAND`.
- **Custom device coverage.** HmIP wall thermostats surface humidity in HA and
  the Matter humidity cluster; HA JSON light commands apply colour, colour
  temperature and effect; button-lock commands reach the parameter the CCU
  actually carries; per-user access-permission switches reach MQTT/HA; a timed
  switch-on that fails on the wire is reported as a failure instead of success.
- **Config, metrics and diagnostics.** Settings that only apply at boot now
  report `restart_required`; the diagnostics dump reports real client-side RPC
  metrics (failures, circuit-breaker rejections, coalesced calls, per-method
  timings) that previously stayed at zero; a down interface names its cause
  (auth / timeout / network) from the actual error rather than always blaming
  the network; MQTT retained-topic cleanup follows CCUs adopted at runtime;
  OIDC discovery survives a boot-time identity-provider outage; MCP sessions
  are reaped and admin tools gate on the calling request.
- The full third defect wave across the UI, MQTT, Matter, central, client,
  REST and store layers, plus the two earlier waves and a round of review-fix
  regressions, all from the 2026-08-16 code-base audit.

## [0.60.0]

### Added

- **The boot seed is covered by tests for the first time.** The CCU
  simulator now answers `fetch_all_device_data` with the object shape
  the real script emits, so the path that fills every data point before
  the first event arrives can finally be exercised without a CCU. Two
  tests run the production ingest against it: one asserts a seeded value
  reaches the model decoded, the other that a button's stored keypress
  does **not** — a boot that seeds it publishes a press nobody made.
  Both were verified to fail when the behaviour they describe is
  removed.

- **The hub's JSON-RPC decode is tested against the CCU's own payload
  shapes.** `SysVar.getAll` reports its values as strings and carries
  only the fields that apply to each variable type; rooms and functions
  report their members as ReGa object ids. The simulator used to answer
  with Go-native types and every field populated — the one shape that
  cannot expose a decode assuming a bool or a float — so the typed DTO
  had never met a realistic payload. A sysvar whose value fails to
  decode is not a loud failure: it spawns with the zero value, and the
  operator sees a switch that is off. Ingest is also driven once per
  interface process against a CCU that serves each protocol family on
  its own port, which is the only arrangement in which a device filed
  under the wrong interface can show up at all.

  Two of these tests found defects in the simulator rather than the
  daemon — object ids sent as JSON numbers, where a live CCU sends
  strings, and the pairing automaton missing from JSON-RPC, the very
  transport the daemon reads install mode over. Both are fixed
  upstream and both tests run against it.

- **Four CCU behaviours the simulator could not previously produce are
  now covered.** Batched event delivery: a burst arrives as one
  `system.multicall`, and the production callback server must deliver
  every entry — dropping everything after the first loses values
  silently while the transport call reports success. The ping/pong round
  trip the connection monitor is built on: the simulator answers a ping
  with the CENTRAL PONG carrying the caller id back, so send, echo and
  match are exercised together instead of the handler being fed its own
  echo. The unreachability latch: a device that stops answering must
  produce UNREACH *and* STICKY_UNREACH, because without the sticky flag a
  device that dropped out overnight looks untroubled by morning. And the
  readiness gate: a CCU that has not finished booting now refuses its
  remote API, not just its web API, which is the half the ingest actually
  reads. Each was measured against the defect it describes.

- **A refused login is classified as an authentication failure under
  both error envelopes.** Which shape a CCU answers an error in — its
  own `version: "1.1"` / `JSONRPCError` form or the JSON-RPC 2.0 codes —
  is not something the daemon chooses, and the auth path must not depend
  on it. A wrong password classified as an ordinary client error becomes
  a login retried through the full backoff, and the operator reads a
  slow, flaky CCU instead of a credential they can fix.

- **Fault-code classification is tested on the wire.** The simulator can
  answer failures with the HomeMatic catalogue instead of a blanket −1,
  so the retrier's decision — repeat this call or surface the error —
  is now driven by a real failure rather than a hand-built error value.
  That is what surfaced the fix below.


- **A trace of what happened, next to the state that is true now.**
  **Matter → Diagnostics** gains a list of the moments that explain a
  failed pairing: a commissioner refused because another was already
  mid-handshake, a commissioning window revoked after repeated failures,
  a session closed. The existing diagnostics report the current state and
  therefore cannot answer "what happened thirty seconds ago" — the
  question left after a controller gives up and goes quiet. Until now
  those moments existed only in the daemon log, which meant having log
  access, knowing what to grep for, and still having the log. The trace
  is deliberately bounded and does not survive a restart: it is a
  diagnostic, not an audit trail. (REST API 5.33.0)
- **The pairing card says which pairing this is, and its codes can be
  copied.** Opening a commissioning window on a bridge three
  controllers already hold is a different act from first-time pairing,
  and the card read identically either way — an operator whose bridge
  was already in Apple Home had no way to tell "my setup did not stick"
  from "I am adding a fourth". It now names the count and offers to add
  a controller. The manual code (eleven digits) and the QR payload each
  get a copy button; a browser that refuses clipboard access — which is
  every page served over plain HTTP, including the Config UI behind
  Home Assistant's ingress — says so instead of reporting success and
  sending the operator to another device with an empty clipboard.

- **Adding a controller now runs in one place.** The fabrics tab had a
  second implementation of the same flow: it opened a commissioning
  window and printed the codes as plain text, with no QR, no countdown,
  and no way to close the window it had just opened — the close control
  only ever existed on the pairing tab. Navigating away lost the codes
  while the window stayed open on the daemon. The tab now points at the
  pairing card, which runs the whole flow. `POST /matter/share` is
  unchanged for external clients; its now-unused wrapper in the SPA's
  API client is gone.

- **Two maintenance actions for the Matter bridge.** *Re-sync topology*
  rebuilds the exposed endpoints from the current devices without
  touching any pairing — until now the only way to reconcile a bridge
  whose endpoint list had drifted from the model was restarting the
  daemon, which drops every controller session to fix a list. *Remove
  all pairings* returns the bridge to its unpaired state. Both live on
  the Matter → Fabrics tab, both are admin-only and written to the audit
  log. The reset additionally requires the caller to name the action
  (`{"confirm":"remove-all-fabrics"}`), so neither an empty POST nor a
  replayed generic `{"confirm":"yes"}` can unpair an installation, and a
  fabric that fails to revoke is reported as an error rather than
  silently skipped — a partial reset would leave the bridge paired to a
  controller the operator believes they removed.

### Changed

- **Two unused parameter-channel indexes are gone.** The question "does
  this parameter appear on more than one channel of the device?" decides
  the channel suffix in an entity name, and three implementations of it
  existed. Only one is used: the one that walks the live model's sibling
  channels. The other two — an in-memory index in the paramset registry
  and a second one in the SQLite store — were maintained on every write
  and read by nobody. Removing them drops a per-write index update from
  device ingestion, a full-table scan from start-up, and one database
  round-trip from every channel delete. It also removes a trap: the
  store's index was keyed by bare device address while its SQL fallback
  scoped by central and interface, so two CCUs holding the same address
  would have answered for each other the moment anything started reading
  it.

A minor rather than a patch release: three of the fixes below change
behaviour an operator may be relying on.

- **The assistant (MCP) interface now enforces a role.** It previously
  checked only that a request was authenticated, so a viewer-role token
  reached the device-write and alarm-control tools. With
  `north.mcp.allow_writes` set, the mount now requires the operator role;
  read-only stays viewer-level. A viewer token that drives writes today
  will start receiving 403.
- **`GET /api/v1/matter/setup-payload` is admin-only.** It hands out the
  commissioning passcode, so any authenticated identity could commission
  the bridge into its own fabric.
- **Three exported client seams were removed** rather than wired:
  `ValueWriter.SetRetrier`, `ValueWriter.CancelInterface` and
  `WriteOptions.PurgeAddresses`. None had a production caller, and
  `CancelInterface` ignored both of its arguments while the effect it
  advertised already happens in `InterfaceClient.Close()`.

The REST contract version moves 5.29.0 → 5.31.0.

### Fixed

- **A call against a device the CCU does not know is no longer retried.**
  Fault code −2 sat in the retryable set as "timeout", with a comment
  saying it was not a CCU-native code and that the daemon's own
  transports raised it. Nothing in the daemon ever raised it: every −2
  reaching the classifier came off the wire, where the published
  catalogue and a live CCU both give it one meaning — the device or
  channel does not exist. A call against a device removed on the CCU,
  or an address a stale automation still holds, therefore ran the full
  exponential backoff before failing, spending duty cycle on a question
  whose answer cannot change and delaying the error the operator needs
  to see. It is permanent now.

  The rest of the table was reconciled against the specification in the
  same pass and verified against a live CCU on both interface
  processes: −3 (unknown paramset), −4 (device address expected), −6
  (operation not supported by the parameter) and −7 (interface cannot
  update) are named and documented rather than falling through as
  unknown codes, and −1 is the catalogue's general fault rather than
  "unreach" — it stays retryable and stays the only code that triggers
  the circuit-recovery wait. The retryable set is now the four codes
  that describe a condition which can pass on its own.

- **Two ecosystems were classified under vendor ids belonging to someone
  else.** A Matter fabric declares the vendor id of the controller that
  created it, and the bridge uses that to decide whether an operator sees
  a compatibility warning — "this device type will not appear in Google
  Home" is only emitted for a fabric it can place. Aqara was mapped to
  `0x1037`, which the CSA ledger assigns to NXP Semiconductors, and Home
  Assistant to `0x125D`, which belongs to Tuya. Both mistakes cost twice:
  the real ecosystem's fabric fell through to "unknown" and was never
  warned about, and a fabric from the vendor who actually owns the id
  would have been labelled as an ecosystem it has nothing to do with.
  Apple's second, management fabric was unrecognised for the same reason.

- **The fabric list names the controller instead of a hex id.** The name
  now comes from the daemon, which is also what removes the second vendor
  table the Config UI kept: the two disagreed about Amazon and
  SmartThings, so a fabric could read as one vendor in the list and be
  classified as another ecosystem on the compatibility tab.
  (REST API 5.32.0)

- **A code comment can no longer cite a catalogue entry nobody wrote.**
  The reference guard confirmed that a cited markdown file exists and
  stopped there, so a comment pointing at a specific entry inside it —
  the shape used to record a deliberate divergence — passed even when the
  entry had never been written. Such a citation reads as evidence that
  someone weighed the question and decided; three comments carried one
  that did not exist. The guard now checks the entry too, and found a
  fourth the manual sweep had missed.

A full-codebase audit found 272 verified defects — every one confirmed by
a second reviewer whose job was to refute it, out of 338 raised. Every
linter was green on all of them, so none of this was mechanically
detectable; the defects are semantic.

Of the 53 rated high severity, 52 are fixed. Of the remaining 219,
roughly 150 are fixed and the rest were declined with a stated reason or
refuted on a second look. Declining was a real outcome rather than a
formality: for a low-severity finding whose honest fix meant converting
an exported field to a guarded accessor across four packages, the churn
is the larger risk.

They are not 272 unrelated bugs. Most are instances of a handful of
habits, and the entries below are grouped that way.

What changes for an operator:

- **Security.** Bearer tokens were authenticated on a 64-bit prefix of the
  digest rather than the whole value, and the comparison was not
  constant-time. The MCP `list_audit` tool was readable by any viewer.
  An SSDP response could name its own host and have the daemon believe it.
  A Matter certificate with an absent or empty EC public key panicked the
  decoder.
- **Alarm.** An arm attempt the engine *refused* notified nobody: the
  failure hook had no production caller, so a nightly auto-arm blocked by
  an open contact was silent on every surface. The engine mutex is no
  longer held across the CCU round-trip and the SQLite write of the sysvar
  mirror, and the mirror no longer latches "ensured" after a failed
  create.
- **Device names.** A failed `Device.listAllDetail` left the daemon
  serving an address-named fleet indefinitely instead of aborting the
  bring-up and retrying, the way a failed serial already did.
- **Matter.** A single failed event report permanently unrouted a live
  subscription. Subscription heartbeats could exceed the session idle
  timeout. OccupancySensing advertised the wrong sensor-type bit,
  ElectricalEnergy omitted the cumulative-energy feature, and PowerSource
  advertised a battery-replacement feature it did not serve — each
  corrected against the matter.js element definitions, which also refuted
  the comment that had justified one of them. Root and aggregator cluster
  servers are now published atomically instead of being mutated under
  live readers.
- **REST and the SPA.** The global 30-second request deadline no longer
  tears down the SSE log stream every 30 seconds. CORS and the WebSocket
  handshake now normalise an operator's configured origin the same way, so
  a trailing slash no longer matches one gate and not the other. The
  diagnostics dump's `anonymize` flag, which reported `true` while
  anonymising nothing, now actually hashes host and address-shaped values.
  A partial `PUT` of a central no longer wipes the stored CCU password,
  and an env-resolved secret is no longer persisted into the database as
  if the operator had typed it.
- **Assistant surface.** `list_service_messages` and `list_alarm_messages`
  returned the raw CCU code and dropped the localized label that REST has
  carried all along, so an assistant could only answer "LOWBAT" where it
  meant "battery low".

Two contract guards turned out to be blind, and both were widened by the
findings they had missed: the wiring-setter guard matched a bare
`func(...)` literal but not a *named* function type, and it dropped any
seam declared as `Set*(x any)` because `any` is an alias. Between them
they hid three seams production never called.

- **Device configuration writes never reached the MASTER paramset.**
  `Channel.Set` resolved the data point with the paramset key it was
  given and then dispatched through `setValue`, which is VALUES-only on
  the wire; the collector did the same whenever a batch happened to hold
  exactly one parameter, which is what the SPA's channel editor always
  sends. A whole-number value for a FLOAT parameter was rejected outright
  because the request body was converted without consulting the
  descriptor — the same field saved fine as `2.5`.
- **Several subsystems read a collaborator once, before it existed.** The
  connectivity plane was dead in its entirety: no per-interface state on
  REST, MQTT or the WebSocket, and no down-detection for a radio that
  dies mid-flight. A capture started at runtime never reached any logger
  derived at boot, so the support archive was missing all south-bound
  logging. Per-subsystem log levels were inert. Cache reset, the
  callback source-IP allowlist and the diagnostics health scores were
  blind to a CCU adopted at runtime.
- **Authorization and secrets.** The MCP mount checked only that someone
  had authenticated, so a viewer token reached the device-write and
  alarm-control tools. `GET /matter/setup-payload` handed the
  commissioning passcode to any authenticated identity. `config export`
  wrote the MQTT, OIDC and Matter credentials in cleartext regardless of
  `--include-secrets`. A token-only deployment counted as "no
  authentication configured", leaving the unauthenticated first-run
  setup endpoint open. WebSocket connections kept their identity
  forever, so revoking a credential never reached an open socket. CASE
  Sigma3 committed the peer's node ID and authenticated tags out of a
  certificate it had not yet verified.
- **Data loss and hangs.** The values-cache GC deleted every persisted
  row of a CCU that was merely offline. The daily history rollup spun
  forever in time zones whose DST transition lands on local midnight.
  A broker that was down at boot disabled MQTT for the process lifetime.
  The circuit breaker counted a cancelled caller as a transport failure
  and tripped against a healthy CCU.
- **Wrong values on normal paths.** `IsHeating()` was permanently true
  and derived binary sensors never fired, both because an ENUM arrives
  as a number on HmIP and as a string on BidCos and each site assumed
  one. Boost/comfort/eco were rejected before the code implementing
  them. A multi-level `topic_base` silently disabled the entire inbound
  MQTT command plane. On a CCU with CUxD but no BidCos interface, the
  "primary" backend resolved to CUxD and produced empty backups reported
  as successful. Matter advertised air-quality sensors without the
  mandatory cluster, measurement endpoints without a device type, window
  coverings with an empty command list, and pressure ten times too
  large.
- **The alarm domain.** A partial interface recovery marked sensors
  behind a still-dead radio as available; an optical siren row silenced
  the acoustic row on the same channel; a rejected arm/disarm left the
  CCU mirror variable asserting the state the engine had refused.
- **The SPA.** After any WebSocket drop every view showed pre-outage
  data behind a green connection badge, because the client never used
  the resume protocol its own schema documents. Channel pickers were
  permanently empty. The Diagnostics page went blank exactly when the
  daemon was unhealthy. Colour sliders wrote to the CCU on every mouse
  move. The confirm dialog accepted Enter while Cancel had focus. A
  persisted CCU filter could hide every alarm message with no control to
  clear it.

Known limitations, both deliberately left in place:

- Device lookups still key on the CCU address alone, so two CCUs that
  share a device address resolve to the first one. Fixing it is a REST
  contract change rather than a local fix, and is tracked separately.
- A bridged endpoint carrying only a Battery, Power or Energy
  measurement still advertises a DeviceTypeList of BridgedNode alone,
  with no application device type. Suppressing those endpoints — the
  obvious fix — removes the Electrical{Power,Energy}Measurement clusters
  that controllers read today, which the chip-tool suite caught as a
  regression. The correct fix gives them their real device types,
  PowerSource (0x11) and ElectricalSensor (0x510); the latter mandates
  the PowerTopology cluster, which this daemon does not implement yet.
  That is a feature, not a bug fix, so it is out of scope here.


- **The pre-release comment sweep corrected 31 inaccurate code
  comments.** Nothing about the daemon's behaviour changes; what changes
  is that its documentation no longer describes a system that does not
  exist. The ones worth naming: a package doc listed a Matter cluster
  under the wrong ID and claimed it sits on the root endpoint when
  nothing mounts it; a handler doc named an error symbol that does not
  exist; the MCP surface was said to be authorized by the REST listener
  when the authorization actually comes from middleware wrapped around
  its mount, and a comment that invites a future mount to skip that
  wrapper is a security problem in waiting; a telemetry endpoint was
  documented as anonymous although it sits behind the auth gate; two
  constructors were marked as having no production caller when both are
  called; and several file inventories named files, packages and
  directories that had been renamed or removed. The page the daemon
  serves when its web bundle is missing told the operator that login and
  setup were still reachable — they live in that very bundle, so the one
  situation in which the page appears is the one in which the statement
  is false.

## [0.59.1]

### Added

- **The diagnostics dump now carries the counters the daemon was already
  collecting.** Every CCU has had a metrics aggregator running since boot
  — outbound RPC totals and failures, callback-server traffic, event-bus
  and cache sizes, recovery attempts, device / channel / data-point counts
  per category, service-call timings — and none of it left the process.
  `GET /api/v1/diagnostics` now includes a `metrics` block with one
  snapshot per central, so the support artefact the SPA downloads answers
  "how much is this CCU actually doing" without a Prometheus scraper. The
  block holds counters only and names no device, so it is unaffected by
  `anonymize`. (REST API 5.26.0)

- **A device that becomes unreachable now reaches the Config UI while you
  are looking at it.** The daemon published every per-device availability
  transition on its internal bus, but nothing carried it north: the MQTT
  availability topic flipped, while the WebSocket stream stayed silent and
  the device list only learned about the change on a full reload. A new
  `device.availability_changed` broadcast rides the existing
  `device.{address}.lifecycle` topic, and the SPA applies it in place — the
  status column and the available / unavailable filter follow a device that
  drops out. The transition is announced for both causes: an interface that
  loses its connection to the CCU, and a device that reports UNREACH or
  STICKY_UNREACH on its own. (REST API 5.27.0)

- **A Matter controller that resumes its session is no longer invisible.**
  CASE resumption re-establishes a session from a cached secret in one
  round trip, and until now nothing recorded that it happened: the daemon
  logged the same establishment line as a full handshake, so an operator
  report could not say whether a controller resumed, which cached record it
  resumed from, or what the resume did to the session table. Running at
  `logging.level: debug` now emits one `matter.bridge.case.session_resumed`
  record per resume, carrying the resumption id the controller presented,
  the id handed back for its next resume, the session id before and after,
  and the sessions the install displaced. The record is built only when
  debug logging is on, so the handshake path is unchanged otherwise.

  `GET /api/v1/matter/sessions` gained an `occupancy` block — live sessions,
  ids still reserved by handshakes that never completed, the capacity of the
  16-bit id space and what is left of it — and the Matter diagnostics view
  shows the same line above the controller table. A reserved id appears in
  no session row and holds its slot for twenty minutes, so a space filling
  up used to look exactly like a quiet bridge until the next controller was
  refused. (REST API 5.29.0)

### Fixed

- **A tone picked in Home Assistant now reaches the siren.** The siren
  advertises its tone list, so HA renders a selector and sends the choice
  back as `tone` — and the command handler read only the domain's own
  name for it. Every tone chosen in HA was dropped without a trace and
  the siren fired with its default, which on an HmIP-ASIR is the value
  that silences it. The MP3 player had the same gap: its tone list is
  soundfile labels and the handler expected a numeric index, so the
  chosen file never played. Both accept the names HA sends now, and the
  original names keep working unchanged.

- **Siren tones and light effects are readable over MQTT.** They reached
  Home Assistant as raw wire tokens — `FREQUENCY_RISING`,
  `SLOW_COLOR_CHANGE` — because a discovered entity has no translation
  file behind it. Both lists are localised now, and a label the operator
  picks is resolved back to the token the device speaks on the way in.
  The raw token stays valid on the command path, so an automation written
  against `FREQUENCY_RISING` keeps working and one written against the
  label works too.

- **Two flush-mount switch actuators are recognised properly.** The
  HmIP-FS6 resolved to no device profile at all, so it appeared only as a
  raw state value instead of a switch you can operate as one. And on the
  HmIP-FSI6, the operating mode of its wired input — push-button, switch,
  normally-open or normally-closed — was hidden, so the input could not be
  set up from the UI at all. Both now behave like the rest of their family.

- **Week-program presets on thermostats are readable over MQTT.** A
  thermostat's preset list mixes two kinds of entry. Home Assistant
  defines `boost`, `eco`, `comfort` and `away` and shows them in the
  language of the HA instance; `week_program_1` … `week_program_6` are
  this daemon's own, so HA printed the slug and the dropdown read
  `week_program_3` next to translated neighbours. The week programs now
  carry a label, and the payload carries the templates that map the label
  back to the slug the device speaks — without them the entity would
  report a state absent from its own option list and send a label to a
  command that only knows slugs. The standard presets are deliberately
  left as slugs: replacing them would take away HA's own translation and
  freeze one language into a retained discovery payload.

- **The stale-paramset check now runs on a CCU the daemon has never seen
  before.** After a firmware update the CCU's HmIP service can keep serving
  descriptor files that list parameters the device no longer has, and the
  daemon checks for that on every bring-up. The check enumerated the device's
  channels from the device-description cache, which on a first-ever start is
  still empty at that point — the CCU only announces its devices moments
  later — so it examined nothing and reported a clean bill of health. It now
  reads the channels from the paramset cache the hydration pass has just
  filled, which is also the cache the comparison is against, and so reports
  the same findings on a first start as on every later one.

- **One person signing in through the identity provider is now one
  principal.** The session subject came from `preferred_username` — a claim
  the OpenID Connect spec guarantees to be neither stable nor unique, and
  that directories return in whatever casing they happen to hold. Signing in
  as `Markus@example.com` and later as `markus@example.com` produced two
  subjects, so the audit trail attributed one person's actions to two
  identities and every subject-keyed lookup split in two. That claim is now
  trimmed and lower-cased, exactly like a local login name. The `sub` claim,
  used when the provider omits `preferred_username`, is still passed through
  byte-for-byte: it is opaque and case-sensitive, so folding it could merge
  two different people.

  A federated login is now also explicitly a **different principal** from a
  local account that happens to carry the same name. Such a session reports
  `scheme: oidc` on `GET /api/v1/auth/me` (a value the API has always
  declared), it can no longer change the local account's password through
  `PATCH /api/v1/auth/me/password` (409 — the provider owns those
  credentials), and deleting or resetting that local account no longer ends
  it. Ending a federated session remains the provider's job, plus the
  session TTL. CSRF protection is unchanged: the session still rides a
  browser cookie and still requires the double-submit token.

  OIDC sessions that already existed when the daemon was upgraded keep the
  spelling and the local-account treatment they were issued with; they are
  not invalidated on upgrade and simply expire within their remaining TTL
  (12 h by default).

- **A CCU added while the daemon is running is now wired the same way a
  CCU present at boot is — structurally, not by remembering to.** Every
  subsystem that works per CCU used to attach itself by walking the list of
  configured CCUs once during start-up, which left a CCU adopted afterwards
  silent on that plane until the next restart: no measurement history, no
  webhook deliveries, no WebSocket status or device-trigger frames, no MQTT
  system-status messages, no scheduled backups, no reliability incidents.
  Those subsystems now register themselves once with the CCU registry, which
  wires them for the CCUs already present and for every CCU added later, and
  unwires them again when a CCU is removed. The second registration step that
  had to be remembered per subsystem — and that was the actual defect — no
  longer exists.

- **`delay_new_device_creation` now has the operator surface it promises.**
  The toggle held a newly paired device back — and there it ended: the
  announced descriptions were parked in memory, no list showed them, and
  nothing ever emptied the queue, so the device stayed without a single
  data point until the daemon was restarted. Accepting it in the Config UI
  only flipped the flag on the CCU. Devices held back this way are now
  listed in the inbox (`GET /api/v1/inbox`, the WS `inbox.list` command,
  the MQTT inbox sensor and the SPA inbox view) with an "awaiting approval"
  marker, an open Config UI learns about a newly paired one through the
  existing `hub.<central>.inbox` broadcast, and accepting it
  (`POST /api/v1/devices/{addr}/accept`, WS `inbox.accept`) hands the
  parked descriptions to the same materialiser a hot-plugged device runs
  through — so the accepted device arrives with its channels, data points
  and values. A failed materialisation leaves the device listed so the
  accept can be retried. The queue also no longer fills with the whole
  installation: the CCU re-announces its complete inventory after every
  reconnect, and descriptions that add nothing are ignored. (REST API
  5.28.0)

- **Changing only a user's role now works.** The Config UI leaves the
  password field blank when an admin just moves an account between roles,
  and the daemon answered that request with "password is required" — the
  one surface that offers a role change could not perform one. A role-only
  update now changes the role and keeps the stored password, so the user
  keeps signing in with the credentials they already have. The lockout
  guard is unchanged: demoting the only remaining admin is still refused.
  A role that actually changed now also revokes the account's live
  sessions **and** its API tokens, because a token carries the role it was
  minted with and would otherwise keep the pre-demotion privileges usable.
  A password reset that names no role no longer moves the account to one.

- **A deleted account no longer keeps a usable API token.** Tokens issued
  through `POST /api/v1/auth/tokens` were stored under the exact spelling
  of the subject that was typed and only in the in-memory store, while the
  purge that runs when the account is deleted addressed the canonical
  spelling in the database alone. Both halves are fixed: the token is
  bound to the canonical subject, and the purge now covers every store a
  bearer token can authenticate against.

- **A radio interface that disappears from the CCU is now reported as
  unreachable.** The CCU answers with the interfaces it currently serves,
  so an interface that dies signals it by dropping out of that answer
  rather than by an explicit flag. The reconciliation pass looked only at
  the entries it received, so a vanished interface kept its last known
  state indefinitely: its MQTT connectivity sensor stayed on, the REST hub
  data points still called it reachable, and the alarm domain kept
  trusting every sensor behind it while armed. The pass now compares each
  answer against the previous one, emits exactly one unreachable event for
  an interface that left, and a reachable one when it returns. A failed or
  empty answer — an unreachable or rebooting CCU — counts as no
  information, so a CCU restart does not report every interface as lost.

- **A cover's Open and Close buttons in Home Assistant move the cover
  again.** Home Assistant gives an MQTT cover one command topic and tells
  Open, Close and Stop apart by the payload alone, but discovery pointed
  that topic at the STOP parameter. Pressing Open wrote a truthy value
  there — the cover halted instead of raising — and pressing Close wrote
  a falsy one, which the actuator ignores entirely; only the position
  slider ever moved a shutter or blind. The topic now addresses a cover
  command service that maps the three payloads back onto the real Open /
  Close / Stop operations, so a blind still drives both axes through its
  combined parameter and an inverted cover still inverts.

- **Changing a device's rooms or functions no longer risks a garbled
  device list.** Both assignments were plain fields on the device: the
  admin write rewrote them from one request while another request read
  them for the device list, the snapshot export, MQTT discovery or an MCP
  tool. A reader could observe a half-written list — the new length
  against the old entries — and in the worst case read past its end. Both
  sets now live behind the model's lock and every reader receives its own
  copy. The single area Home Assistant is told to suggest is derived
  under the same lock, so it follows a room reassignment instead of
  keeping whatever the device carried at boot.

- **CUxD push callbacks no longer stop for good after a transient accept
  failure.** The BIN-RPC callback listener ended its accept loop on any
  error, including the recoverable ones a busy host produces — a peer
  that resets between SYN and accept, a momentary file-descriptor
  shortage, an interrupted syscall. Nothing restarted it: the port
  stayed bound, `/health` stayed green, and every CUxD event, device
  addition and device removal silently stopped arriving until the daemon
  was restarted. The loop now backs off and retries those failures, the
  way the XML-RPC callback listener already did, and unbinds the socket
  if it ever does have to give up.

- **A heating schedule no longer changes shape when it is merely
  reloaded.** Reading a climate week profile picks the temperature that
  fills most of the day as the day's base; that segment is then implicit
  and only the remaining ones are listed as periods. When two
  temperatures share the day evenly — a 12 h / 12 h day-and-night
  schedule is the common case — the winner was whichever one an
  unordered lookup happened to visit first, so the same untouched CCU
  data could come back with either half listed, differing between two
  reloads of the same channel. The earliest of the tied temperatures now
  wins every time, so the schedule the UI and the REST API show stays
  put and no longer produces a phantom difference on the next save.

- **Curated value labels now reach the last translation fallback.** When a
  value has no label of its own for the parameter reporting it, the daemon
  falls back to the label the same value carries on any other parameter.
  That reverse index was built from the raw CCU stringtable before the
  curated corrections were merged on top, so a label that exists only in
  the curated set — the sound-file names of the acoustic signal devices,
  among others — was invisible to it, and the SPA, the REST API and MQTT
  showed the raw value instead. The curated set exists to close exactly
  those gaps.

- **A derived reading is no longer computed from half of one measurement
  and half of the next.** Dew point, dew-point spread, frost point, vapor
  concentration, enthalpy and apparent temperature are each computed from
  two or three separate readings of the same channel, and every one of
  those readings arrives on its own connection from the CCU. Nothing kept
  the writers apart, so a burst that carried temperature and humidity
  together could publish a value mixing the fresh humidity with the
  temperature it was replacing — or silently drop the update instead. The
  battery level had the same problem across a different pair: the
  low-battery limit comes from the device configuration while the voltage
  comes with the live events, so a configuration re-read landing beside a
  voltage report could publish a percentage measured against a reference
  that no longer applied. Window, smoke and intrusion states could lose an
  update the same way.

- **Saving a device setting twice in quick succession no longer leaves the
  old value on screen.** Classic HomeMatic interfaces do not report MASTER
  changes back, so the daemon reads the paramset again a couple of seconds
  after each write. A second save inside that window replaced the pending
  read — and then the replaced one, on its way out, cancelled its own
  replacement. Neither read happened, so the device had the new setting
  while the daemon, the API and the UI kept showing the previous one until
  a later single write or a restart corrected it.

- **A CCU reconnect can no longer make a supported operation look
  unsupported.** The set of things an interface can do is re-probed when the
  connection comes back, on a different goroutine from the ones asking about
  it — a backup, a firmware update, or the ingest of a newly announced
  device. The hand-off between them was unsynchronised, so on the 32- and
  64-bit ARM builds a request landing in that instant could read a
  half-published profile and be turned away, or a device that does support
  push callbacks could be recorded as one that does not.

- **The daemon stops hoarding device announcements nobody can read.** It
  answers the CCU's `listDevices` with an empty array, so after every
  reconnect the CCU re-announces its complete inventory — and every one
  of those announcements was also copied into the pending-accept inbox,
  which only the manual accept flow behind `delay_new_device_creation`
  empties. With that option off, which is the default, nothing could ever
  read the copies and nothing removed them: on a 400-device installation
  every CCU reboot or network flap added another few thousand
  descriptions to a daemon that then held all of them until it was
  restarted. The inbox is filled only when deferred creation is on now,
  and a re-announcement replaces a pending device instead of stacking a
  second copy on top of it.

- **A callback can no longer make the daemon remember an interface it
  never had.** The keepalive PONG is stamped before the check that a
  callback names a device we mirror, so whatever interface id it carried
  went straight into the per-interface liveness clocks — which are
  cleared only when the CCU is torn down. The callback listener takes no
  authentication and accepts a 10 MiB request body, so any host on the
  network could grow those clocks with fabricated ids until the daemon
  was out of memory. A PONG for an interface no connection of ours
  registered is dropped now, and the clocks refuse an id that cannot name
  one of our interfaces.

- **A CCU whose name is not a valid URL segment is refused at the door.**
  A central name becomes a path segment of the callback URL the daemon
  announces to that CCU, and the callback router only accepts letters,
  digits, `-` and `_`. Nothing enforced that on the way in, so a CCU
  adopted from discovery as `CCU Wohnzimmer` produced a daemon that
  started cleanly, looked healthy on REST and in the SPA, and never
  received a single push event. The allowlist is now shared between the
  two sides and enforced at config load, in the admin API, in the store
  and in the onboarding wizard; the SPA states the rule next to the field
  and sanitises the name it prefills from a discovered CCU.

- **HmIP devices pick up a completed configuration write again.** After
  a MASTER paramset write, the CCU signals completion with
  `CONFIG_PENDING` going true → false, which is what triggers the
  week-profile reload, the operation-mode visibility re-apply and the
  targeted MASTER read that keeps the on-disk cache current. The handler
  compared the interface id the CCU echoes back against the bare
  interface name, so it matched on no configured CCU at all and every one
  of those steps was silently skipped. HmIP has no polling fallback, so
  nothing else covered it.

- **The replace-device dialog lists candidates again.** The candidate
  lookup resolved the southbound backend with the bare interface name
  while the registry is keyed by the central-scoped one, so
  `GET /api/v1/devices/{address}/replace-candidates` and the matching
  WebSocket command returned an empty list with HTTP 200 on every
  interface and every CCU — no device could ever be selected for
  replacement.

- **Firmware updates show their progress again.** The firmware refresh
  looked device descriptions up under the bare interface name while the
  description registry is keyed by the central-scoped one, so a device
  whose available version or update state changed on the CCU kept the
  values it was created with at boot. The MQTT update entity and the SPA
  firmware view stayed frozen for the life of the daemon.

- **A per-interface `remote_path` now reaches the wire.** The field
  validated, persisted and showed up in the UI while nothing read it, so
  a reverse-proxied CCU kept being addressed on `/RPC2` and never
  connected. It is honoured, and shape-checked at load so a typo cannot
  quietly point the interface somewhere else. `rpc_type` is documented
  for what it is — a pin on the transport the interface name already
  implies — and a contradicting value is now refused instead of silently
  ignored.

- **Two CCUs coming back together no longer risk taking the daemon with
  them.** Each central wires its own backup restorer from its own
  bring-up, and the map holding them was unguarded — two centrals
  clearing their readiness gate in the same window could abort the
  process with `concurrent map writes`, and a restore issued while a
  central re-gated could do the same on the read side.

- **CUxD no longer leaves a stale callback registration behind.** The
  shutdown path asked the CCU to drop the registration on a context that
  had already been cancelled, so the call never left the process. The old
  registration survived, the next start added another, and CUxD then
  delivered every event twice — each one acted on twice by MQTT, the
  alarm intent routing and CCU program triggers.

- **Unpairing a device now clears everything it left behind.** The
  cleanup after a successful unpair deleted the device address under the
  bare interface name, while the description and paramset caches are keyed
  by the central-scoped one and hold one entry per channel, not one per
  device. Nothing matched, so every channel's descriptions survived and
  the persistence layer was never asked to drop the corresponding rows —
  which the next start read back and could re-materialise into a device
  the CCU no longer has.

- **Reloading a device or channel configuration writes where the rest of
  the daemon reads.** "Reload device config" stored the refreshed
  descriptions and paramsets under a second, bare key space nothing else
  looks in: the reloaded data was invisible, duplicate rows were persisted,
  and the periodic firmware sweep then asked for a southbound backend under
  that bare name and logged a failure on every run for the life of the
  daemon. The on-demand link-peer refresh that rides along found no
  descriptions at all. The same mismatch is fixed in the eager
  model refresh after a device replacement.

- **Smoke-detector teams can be assigned again.** The team-candidate
  lookup resolved the target channel under the bare interface name, so
  `GET /api/v1/devices/{address}/channels/{no}/team-candidates` returned an
  empty list with HTTP 200 on every CCU and no channel could ever be joined
  to a team from the UI.

- **A device list served during a CCU reconnect no longer reads
  half-built channels.** A reconnect or a hot-plug re-ingests the device
  inventory and rebuilds every channel of a device the REST, MQTT and
  Matter surfaces are already serving. The channel went into the live
  device empty and its name, rooms, functions and CCU id were written
  afterwards, unsynchronised — so a reader that hit that window saw a
  blank channel, and a concurrent read of the room or function list could
  return garbage or fault outright. Channels are now finished before they
  are published, and those four fields are read and written under the
  lock the channel already had.

- **Snapshot publishing no longer loses track of what it subscribed
  to.** Every broker reconnect and every CCU that finishes its bring-up
  runs a snapshot pass, on two different goroutines, and each pass wires
  live callbacks that shutdown has to release. The bookkeeping was
  unsynchronised, so concurrent passes overwrote each other's records:
  the lost callbacks kept publishing into a bridge that had already been
  torn down, for the rest of the process lifetime.

- **Interface health reflects that the CCU is actually pushing events.**
  Thirty percent of an interface's health score is how recently it last
  carried traffic, and the only thing that feeds it was a bus event
  nothing ever published. A perfectly healthy interface delivering
  callbacks for hours reported a "last event received" of never and
  scored 0.70 out of 1.00 permanently. The raw callback path publishes it
  now, before the unchanged-value filter, because a device re-reporting a
  stable reading is still a live interface. A build-time guard fails on
  any future event type that has subscribers but no producer.

- **Device-trigger events carry the CCU they came from.** The event
  coordinator's central scope was never set, so every device trigger and
  every raw-parameter trace left the bus unattributed. The WebSocket
  device-trigger plane derived its entity id from that empty name, which
  made the ids of two CCUs collide.

- **Home Assistant gets one connectivity sensor per radio, and it works.**
  The sensor declared at startup pointed at a state topic nothing ever
  wrote, because the declaration used the internal, central-scoped
  interface id while the reachability updates are published under the
  name the CCU itself reports. The entity stayed unavailable for good,
  and the first reachability change created a second one next to it.

- **A device paired while the daemon runs reads its configuration back
  through its own radio.** After a configuration write, classic
  HomeMatic devices need a delayed re-read to keep the cached values
  honest, and that read is bound to one interface's connection. Devices
  materialised at runtime were all given the connection of whichever
  interface was configured last: on a CCU running HmIP-RF and BidCos-RF
  together, a new HmIP device sent its re-reads through the BidCos
  connection, where the CCU rejects them and the failures count against
  that interface's circuit breaker. In the reverse configuration the
  re-read was skipped entirely.

- **The HmIP paramset-consistency check looks at real data again.** The
  post-start sweep that detects stale descriptor files left behind by an
  HmIP firmware update looked its channels up under the bare interface
  name while the caches are keyed by the central-scoped one. Every lookup
  missed, so the sweep compared nothing, reported no problem and logged
  nothing — it has been blind, while looking healthy, since it was added.

- **Text values read at startup keep their umlauts.** The bulk value read
  the daemon issues at startup percent-decodes the strings the CCU
  returns but skipped the ISO-8859-1 conversion every other CCU-script
  reader applies. A string data point holding `Spüle` was stored as
  invalid UTF-8 and rendered as `Sp?le` on MQTT, REST and in the SPA
  until the device happened to report the value again.

- **Shutdown no longer races the schedule warm-up it is waiting for.**
  Stopping the north-bound event bridge waits for its background
  schedule loads to finish, but a broker reconnect landing in that window
  could start another one — Go detects that as a wait-group misuse and
  aborts the process, and in the timings it does not detect, the load
  outlived the bridge and published into a torn-down stack. A pass that
  starts after teardown now warms nothing up.

- **A reconnect no longer multiplies week-profile, schedule, firmware and
  timer updates.** Every broker reconnect, every CCU that clears its
  readiness gate and every hot-plugged device re-walks the model and wired
  another callback onto the same week profiles, schedules, firmware
  trackers and combined data points. After the third reconnect a single
  profile change wrote its MQTT topic three times, and the list of
  callbacks waiting to be released grew for as long as the daemon ran.
  Each callback is now installed once per object: a reconnect that hands
  back the same objects changes nothing, while a device re-read that
  rebuilds a channel subscribes the rebuilt objects and releases the
  callbacks on the ones they replaced — so a re-read does not silently
  stop the updates either. A device that leaves the CCU releases its
  callbacks with it.

- **A CCU added while the daemon runs gets its values persisted.** The
  periodic value-cache flush and the row cleanup after an unpair both
  subscribe per CCU, and both did so once at startup: a CCU adopted later
  was skipped by every flush tick from then on. Its values reached disk
  only through the flush on a clean shutdown, so a container kill, an OOM
  kill or a power cut left its cache exactly as empty as it was at
  adoption, and the next start had nothing to restore for it.

### Security

- **A chunked request body is capped like any other.** The OpenAPI
  request validator capped bodies whose length the client announced, and
  `Transfer-Encoding: chunked` announces none: such a request streamed
  into an unbounded read before authentication, CSRF or the login
  brute-force limiter ever saw it, so an unauthenticated `POST
  /api/v1/auth/login` could exhaust the daemon's memory with a
  one-header change.

- **Two per-request tables are now hard-bounded.** The login
  brute-force limiter's per-IP table only evicted buckets idle for ten
  minutes, so a source rotating through an address range — an IPv6 /64
  offers 2^64 of them — grew it by one limiter per request without
  limit. The `Idempotency-Key` response cache had no eviction at all:
  its key carries a client-chosen header value, so a caller minting a
  fresh key per request added one full cached response body per request
  for the life of the process. Both are now capped; the response cache
  additionally stops caching statuses that never reached a handler and
  responses too large to be worth replaying.

### Fixed

- **A notification output no longer freezes the alarm system.** Enrolling
  an output of class *notification* — the documented way to get a
  messenger or webhook alert — was enough to stop the alarm engine dead
  the first time the zone triggered. The alert was assembled from inside
  the trigger itself and asked the engine for the zone it was already
  triggering, which cannot answer until the trigger finishes. Everything
  after that point stopped: no state change was published, no further
  sensor event was handled, and *Disarm* and *Silence* never returned —
  with the siren already sounding. The alert now carries the zone name
  and the contributing detectors with it and asks the engine for nothing.

- **A bound panic key raises an alarm.** Binding a keyfob long-press to
  the `panic` action — a hold-up or medical key — produced nothing when
  pressed: no incident, no siren, no notification, only a journal entry
  saying the engine had no panic path. It had one; the router simply
  never reached it. Every installation with a panic key was affected.

- **The pre-alarm window is quiet again.** A zone with `pre_alarm_s`
  configured is supposed to spend that window on the chime, the alarm
  light and the notification so a resident can silence a false alarm
  before the sirens sound. Instead every enrolled siren fired at full
  volume from the first second and again when the window elapsed, which
  also spent the incident's acoustic budget twice.

- **An alarm report names the detector that caused it.** Three trigger
  routes recorded no contributing data point: an expired entry delay —
  the most common real intrusion path, a door opening while nobody
  disarms — a sensor that stopped answering while armed, and a sensor
  found open after a restart. For those incidents the source list stayed
  empty everywhere: in the notification, in the `{sensor}` placeholders
  of the rendered report, and in the after-the-fact audit.

- **A lost radio interface reaches the alarm system.** When a CCU stopped
  serving one of its interfaces, the alarm service compared the CCU's
  own interface name against its internally qualified one and matched
  nothing. Every window and door contact behind that radio kept
  reporting its last known — usually closed — state while armed, and
  losing every interface of a CCU never ran the zone's central-loss
  policy. The per-data-point unreachable path still worked, which is why
  the gap was invisible.

- **A failed siren command turns the alarm health surface red.** A fire
  that failed or a stop the watchdog could not verify was recorded on
  `/api/v1/health` but published nowhere, so the MQTT alarm panels, the
  SPA health surface, the outbound webhook and the Security overview all
  kept reporting a healthy alarm system while a siren was stuck on.

- **A WebSocket command split across several frames is executed.** A
  client library is free to fragment a large message — most do above
  some size threshold — and the daemon handled only whole frames: the
  first half of such a command was logged as malformed JSON and the
  remainder was discarded without a trace. The command never ran and no
  answer was ever sent for its correlation id, so the caller waited
  until it timed out. Fragmented messages are now reassembled, with the
  assembled size held to the same 1 MiB ceiling a single frame has, and
  a client that genuinely breaks the framing rules is closed with the
  matching WebSocket status code instead of having its frames silently
  dropped.

- **A CCU added while the daemon is running reaches the outbound
  webhook.** The webhook bridge subscribes its CCUs once, when it
  starts. A CCU adopted afterwards over the admin API delivered nothing
  at all — no value change, no interface status, no incident — while the
  CCUs present at start-up kept delivering, so the endpoint looked
  healthy and the new CCU looked idle. Removing a CCU now detaches it
  again, too.

- **Channel configuration export and import work.** `GET` and `POST
  /api/v1/devices/{address}/channels/{n}/config/export|import` are
  published in the OpenAPI spec and appear in the generated client, but
  the daemon never assigned the backend they depend on: every build
  answered `503 service_unready`, and no configuration could change
  that. Both endpoints now route through the same gated paramset path
  the REST paramset write uses, so an import cannot set a parameter that
  a `PUT` would refuse — and the export offers exactly that set, which
  is what makes the snapshot importable again.

- **A CCU added while the daemon is running reports device triggers,
  device lifecycle and optimistic rollbacks.** Three WebSocket
  subscribers walked the configured CCUs once, at start-up. For a CCU
  adopted afterwards over the admin API, every keypress on one of its
  remotes and wall switches, every pairing and removal, and every rolled
  back optimistic write was lost to every WebSocket client. The topics
  are declared in `wsapi.json`, so clients subscribed and waited
  forever; only a daemon restart repaired it.

- **A WebSocket client resuming with a cursor from before a daemon
  restart is told it lost events.** The sequence counter is
  process-local and restarts at 0, so a reconnecting client's stored
  cursor sits above everything the fresh daemon can produce. It received
  `replay_done` carrying its own cursor back, concluded it had missed
  nothing, never issued the documented `/snapshot` resync, and kept
  rendering pre-restart device state. A cursor the daemon cannot place —
  above its current top, or against a disabled replay buffer — now
  yields `replay_lost`. (The bundled UI was unaffected: it never sends a
  cursor.) (REST API 5.23.0)

- **A security source override with a `%` in its name is applied to the
  source it names.** The handler percent-decoded the reference a second
  time on top of the router's own decode. A central name containing a
  literal `%` was rejected as an invalid reference, and one whose
  decoded form still looked like an escape was silently rewritten — so
  the override landed on a different data point than the operator chose.

- **A `topic_base` with a trailing slash no longer takes every alarm and
  security entity offline.** The broker's last-will was assembled from
  the configured topic base verbatim, while the bridge publishes its
  `online` counterpart from the normalised base — with `topic_base:
  "loom/"` the retained `offline` landed on `loom//bridge/status` and
  nothing ever overwrote it. Every alarm panel and every Security &
  Safety entity names that topic as an availability source and requires
  all sources to be online, so all of them stayed permanently
  unavailable in Home Assistant. The will and the status topic are now
  built by the same builder, and the availability source both planes
  declare goes through it as well.

- **A CCU added while the daemon is running reports its system status.**
  The `<base>/<CCU>/system/status` topic an operator's alerting watches
  for interface degradation was published by a subscriber that walked
  the configured CCUs once, at start-up. A CCU adopted afterwards over
  the admin API was never subscribed, so its interfaces could go down in
  silence — no MQTT event, no WebSocket broadcast, and nothing in
  `GET /api/v1/system/status` — while the alerting kept working for the
  CCUs present at boot. All three surfaces are attached when the CCU is
  adopted now, and detached when it is removed.

- **A removed device stops leaving retained topics behind.** Unpairing a
  device cleared its per-data-point state but not its firmware-update,
  week-profile, schedule, aggregate or combined-data-point topics: those
  publishers never recorded what they had written, and no shape the
  boot-time sweep recognises matches them. Raw-plane consumers kept
  reading the gone device's last known profile and firmware state
  indefinitely. Every device-scoped retained publisher records its topic
  now, and only once the broker has accepted the publish.

- **Entities missing after a broker outage during start-up.** Home
  Assistant discovery configs were marked as published before the
  publish was attempted, so a broker that was briefly unreachable — or a
  circuit breaker that had opened — lost every config of that pass while
  the daemon considered them all declared. The identical payload rebuilt
  from the next value change was then suppressed as a duplicate, and the
  entities stayed absent until Home Assistant itself was restarted. The
  same held for the raw plane's `/config` companions. Both cache what
  the broker accepted now, and the replay Home Assistant triggers on its
  own restart continues past a failing topic instead of abandoning the
  rest of the fleet.

- **Choosing a heating profile no longer writes a phantom parameter to
  the CCU.** The week-profile command topic has the same number of
  levels as the legacy data-point command topic, and a broker delivers a
  message to every matching subscription — so each profile change
  switched the profile and additionally issued a CCU write for a
  parameter named `week_profile`, which no channel has. One wasted wire
  call and one warning per profile change.

- **Devices nest under their CCU in Home Assistant again for CCU names
  that are not bare slugs.** The card of each physical device pointed at
  its parent CCU with a differently-spelled identifier than the one the
  CCU's own card is registered under — the two spellings only coincide
  while the name is plain ASCII without spaces. With a name like `Haus
  CCU` the link could not resolve, so every device floated at the top
  level of the Devices view instead of nesting, and the system
  variables and programs bound to a device lost their link too.

- **The raw-plane orphan sweep tolerates a `topic_base` with a trailing
  slash.** Every publisher goes through the topic builder, which trims
  the base's slashes; the sweep assembled its own prefix from the raw
  configured value. With `topic_base: openccu-loom/` it therefore
  subscribed to a prefix nothing writes and evicted nothing on every
  boot, leaving retained topics of retired data points feeding stale
  values forever.

- **MQTT commands reach a CCU whose name contains a space.** Every topic
  the daemon publishes escapes the CCU name — `Wohn Zimmer` becomes
  `Wohn_Zimmer` — but the inbound handlers read that segment back
  verbatim and looked it up as a configured name, which never matched.
  Every MQTT write for such a CCU was dropped ("no backend") while its
  state topics kept updating, so the plane looked healthy. Data points,
  MASTER writes, system variables, programs, install mode, week
  profiles, schedules, combined data points and custom-DP invokes all
  resolve the segment back to the configured CCU now. Two CCU names that
  escape to the same segment are ambiguous on the wire and are rejected
  at start-up.

- **System variables whose name contains a space are writable over
  MQTT.** The command topic the daemon advertises for `Außen Temperatur`
  is `…/hub/sysvars/Außen_Temperatur/set`; a write there was resolved
  against the CCU's variable list verbatim, missed, and died as "unknown
  sysvar" — while the entity kept rendering as writable. The escaped
  segment now resolves back to the real variable, and the CCU-side write
  carries its real name.

- **The System-health and Connection-latency sensors report again for
  CCUs whose name is not plain ASCII.** The two sensors declared one
  state topic and the daemon published to another, so both stayed
  unavailable forever while the Last-event-age sensor beside them
  worked. All three now share one topic builder, and the central segment
  of these three topics is spelled like every other topic on the plane
  (escaped, case preserved) instead of lower-cased.

- **The retained-discovery cleanup pass works for CCU names that are not
  bare slugs.** The once-per-boot sweep that removes the configs of
  entities this build no longer publishes derived its scope with a third
  spelling of the CCU name, which matched neither the per-device nor the
  hub configs it was meant to find. For a CCU named `CCU Küche` it
  evicted nothing at all, so retired entities kept being re-created by
  Home Assistant on every restart. Producers and sweep now share one
  identifier normaliser, and the sweep also still recognises the
  spelling earlier builds wrote, so those leftovers are cleaned up once.
  The raw-plane sweep matches the escaped CCU name it actually
  publishes under.

- **A deleted alarm zone stops haunting Home Assistant.** The alarm
  plane never told the orphan sweep that it had published, so its
  retained panel configs were exempt from the sweep forever: a zone
  removed while the daemon was down kept a permanently unavailable panel
  in Home Assistant with no automatic way to clear it. The plane now
  declares once the engine has really loaded its zone set — declaring
  earlier, on the empty pass Start triggers, would have wiped the live
  alarm surface instead.

- **A CCU that reports its serial while values are flowing no longer
  risks aborting the daemon.** The discovery builder's per-CCU metadata
  map was written from the boot path, the snapshot pass and the hub
  publisher's worker while the event path read it for every value event.
  Go treats that as fatal, not as a benign race, and each additional CCU
  widened the window.

- **Acknowledging a fault no longer re-announces it as a new one.** On
  the outbound webhook an acknowledgement arrived byte-identical to the
  original raise — the condition still stands after an acknowledgement,
  and the payload said only that — so a messenger integration fired
  "smoke detector fault" a second time because somebody had pressed
  acknowledge. The webhook body now names the transition (`raised`,
  `acknowledged`, `cleared`), carries the acknowledgement as its own
  flag, and includes the fault's id, so two reports of one standing
  fault are no longer indistinguishable from two independent faults.
  The standing-fault tally moves from `entry_id` — a journal entry id on
  every other alarm event — to its own `open_count`, matching the
  WebSocket broadcast of the same event.

- **A running intrusion keeps its start time when another zone is
  disarmed.** The security domain records when the intrusion class
  became active once, for the whole installation, while the state
  machine runs per zone. Any zone leaving the triggered state released
  that single record — so in a multi-zone installation, disarming a
  quiet zone while an incident ran in another one left the incident
  reported as active with a start time of zero, and MQTT, the REST
  snapshot and the Config UI all showed the break-in as having begun at
  the Unix epoch. The start time is now released only once no zone is
  triggered any more and no intrusion sensor is still active.

- **A CCU added while the daemon runs is now wired like one that was
  there at boot.** Adding a second CCU through the SPA brought it up,
  its devices appeared, and its values flowed — while measurement
  history for it stayed empty forever, no webhook was ever sent for it,
  no `device.trigger`, `device.created` / `device.removed`,
  `datapoint.optimistic_rolled_back` or `system.<central>.status`
  WebSocket frame was emitted, its interface transitions never reached
  `GET /system-status` or the MQTT system-status plane, no recorded
  session was persisted and no reliability incident registered. Each of
  those subscribes by walking the list of CCUs exactly once, at start.
  Nothing failed and nothing logged, and a daemon restart made all of it
  work — which made the gap read like a transient.

- **A CCU added while the daemon runs is backed up, watched and
  audited.** Three more wirings that ran once at boot and therefore
  skipped it: its automatic backup — `backup.schedule` applies
  daemon-wide, so the configured CCUs kept producing daily backups while
  the added one produced none and pruned nothing, discoverable only by
  noticing its backup list stayed empty, typically once a restore was
  already needed; its health seed, its event-bus, audit and scheduler
  gauges and its metrics aggregator, so its section of `/health` and the
  diagnostics dump had nothing in it, which reads like an idle CCU
  rather than an unwatched one; and the record of every program the
  daemon runs on it, which is what tells a duplicate execution the
  daemon sent from one the CCU produced on its own — for that CCU the
  record was simply empty, while it worked for its neighbours.

- **Issuing and revoking an API token leaves a trace.** Both entries
  went to the in-memory ring only. With a database present the audit
  view and its CSV export read exclusively from the database, so
  `GET /api/v1/audit` returned rows that contained neither, and a
  restart erased them entirely — the creation and revocation of a
  credential were the two events with no record at all.

- **A WebSocket connection can refresh its identity with a token from
  the SPA.** The in-band `reauth` frame resolved the supplied token
  against the tokens from the configuration file alone. A token minted
  through the admin surface lives in the database, so it authenticated
  the connection at upgrade time and was then rejected on refresh, which
  closes the connection — a credential valid everywhere else dropped the
  socket.

- **A Matter controller that reuses a session id mid-handshake no longer
  silences itself.** A session id reserved for an in-flight (or aborted)
  CASE handshake resolved as an established session with nothing behind
  it. A controller that echoed the id it read out of Sigma2 in an
  ordinary encrypted packet made the receive path fault on the absent
  session; the per-packet recovery swallowed it without a log line, so
  the only symptom was a controller whose messages stopped arriving.

- **Deleting or disabling a CCU takes effect without a restart.** For a
  CCU the daemon had loaded at boot — the normal case after the
  onboarding wizard, once the daemon has been restarted — `DELETE
  /api/v1/admin/centrals/{name}` returned 204 and the entry vanished
  from the list while the CCU stayed completely live: still polled,
  still publishing to MQTT and WebSocket, still holding its callback
  routes, still writing cache rows. A second delete answered 404. Only a
  restart made the deletion real.

- **"Clear caches and re-pull" clears something.** The scoped cache
  clear (and `hmcli cache clear`, and the cache purge that runs when a
  CCU is removed) addressed the cached rows by the bare interface name
  from the config, while every row is stored under the CCU-qualified
  interface id. Every delete matched zero rows: the report said
  devices/paramsets/values/master = 0, no error was raised, and the
  re-pull re-hydrated from exactly the stale rows the operator had asked
  to discard. Both spellings are accepted now.

- **`basic_enabled: false` / `bearer_enabled: false` switch the scheme
  off.** Both gates were discarded on every deployment with a database —
  which is every normal one — when the daemon layered its persistent
  user and token stores onto the login chain. A disabled scheme kept
  authenticating and granting the stored role, with no signal that the
  setting had done nothing.

- **A hidden channel disappears from MQTT too.** The per-channel
  operator override took effect on REST and Matter, but the MQTT bridge
  kept publishing the channel's state and its Home Assistant discovery
  config: the gate was installed on the supervisor after the boot bridge
  had already been built. It only began working after some unrelated
  `north.mqtt.*` edit rebuilt the bridge, so the same installation
  behaved differently before and after any MQTT config change.

- **A daemon that cannot open its database serves REST instead of
  exiting.** A missing or read-only `data_dir`, a failed migration or a
  migration-lock timeout is logged and boot continues in a degraded,
  REST-only mode — except the REST mount then dereferenced the absent
  config service and took the process down with it.

- **A CCU added while the daemon runs keeps its `primary_interface`.**
  The pin decides which interface the CCU's primary-client health
  verdict and score are computed from. It reached only the CCUs listed
  in the configuration file, so for one added through the SPA the daemon
  fell back to its built-in "an interface whose name contains HmIP-RF"
  rule — which matches nothing on a wired-only or BidCos-only CCU.
  `/health` and the health tile then reported that CCU against an
  interface it does not have.

- **The values-cache endpoints answer instead of crashing when the cache
  is off.** With `persistence.values_cache.enabled: false`,
  `GET /api/v1/admin/values-cache/stats` and both reset endpoints
  panicked on every call — recovered into a 500 with a stack trace in
  the log — because the absent store still passed the handler's
  "is it wired" check. They now return the documented 503, and the
  per-device reset no longer reports "device not found" about a device
  it never looked for.

- **Audit entries written just before shutdown survive it.** The audit
  trail persists through a background queue. Shutdown closed the
  database handle without stopping that queue first, so a burst of
  changes followed by a restart lost whatever had not been written yet —
  the trail ends mid-burst, with a warning line as the only trace. The
  queue is now drained and its worker joined before the handle closes.

- **The MQTT last will lands on the topic every entity listens to.** A
  `topic_base` written with a leading or trailing slash — `loom/` — was
  used verbatim for the last-will topic while every declared topic had
  it trimmed. The broker then published `offline` to `loom//bridge/status`
  while all entities watch `loom/bridge/status`: when the daemon died or
  the connection dropped, Home Assistant kept every bridged entity
  available with its last value, forever. The base is now normalised
  once, when the configuration is loaded, so every consumer of it —
  including the retained-topic cleanup — agrees.

- **`hmcli cache clear --offline` fails when it could not clear.** A
  delete that the database rejected — a locked file, a table missing
  after an interrupted migration, a read-only data directory — was
  printed as an `error:` line on standard output and then followed by
  exit code 0. Anything that reads the exit code instead of the text —
  a maintenance script, an `ExecStartPre` hook, a CI job — treated the
  run as done and the next daemon start read the cache it believed was
  gone. The offline clear now exits 1 and names every store that
  failed, matching what the daemon-side clear already reports over
  REST.

- **A damaged encrypted backup is rejected with an error on 32-bit
  builds too.** The reader for encrypted backups checks the length each
  frame declares before it allocates for it, but compared that length as
  a signed machine word. On the 32-bit builds — armv7 above all — a
  declared length above 2 GiB wrapped negative and slipped through the
  very check meant to stop it, so restoring a truncated or bit-flipped
  archive ended in an allocation panic and a stack trace instead of the
  descriptive "length exceeds bound" error the same file produces on
  64-bit.

- **The recurring add-on update check no longer stops after one stalled
  request.** The release check and the tarball download went out on the
  process-wide default HTTP client, which carries no request deadline at
  all. A server that accepts the connection and then never answers — a
  network partition, a transparent proxy that swallows the response —
  parked the single goroutine that drives the daily check for the rest
  of the daemon's uptime: no log line, no error, and no further check
  until a restart. A stalled download had a louder consequence, latching
  the updater in `downloading` so every later check or install answered
  "busy". Both now use a client of their own with an explicit deadline,
  and each check on the recurring cadence is additionally bounded on its
  own so no single call can wedge the loop. The calls to an OIDC
  provider (discovery, JWKS refresh, code exchange) ran unbounded on the
  same shared client and are bounded now too.

- **An API token now carries the same identity as the account it was
  issued for.** Tokens were stored with whatever spelling the operator
  typed into the subject field, while user accounts are keyed
  lower-cased. A token issued for `Admin` therefore authenticated as a
  different subject than a login as `admin`: the per-user preferences
  and the privately owned diagrams of the two never met, and the audit
  trail recorded them as separate actors. The subject is canonicalised
  on write now — for freshly created tokens and for the ones migrated
  out of the config file — the create response reports the spelling that
  was stored rather than the one that was typed, and a migration folds
  the rows an earlier version wrote.

- **`security.allow_plaintext_secrets` now governs what it documents.**
  The setting promised that the daemon refuses to persist a CCU password
  in cleartext, and no code read it. When the at-rest master key could
  not be resolved — `secret.key` missing after a restore, a read-only
  data directory — the daemon logged one warning and wrote every CCU
  password into the database in the clear, at the default that says it
  will not. Saving a central in that state is now rejected with `400`
  naming the setting, on every path that persists one (the CCUs view,
  the onboarding wizard, live adoption, the config-file seed), and an
  operator who wants the old behaviour can still opt in by saving the
  `security` section with the flag set. With a master key present
  nothing changes: the password is sealed either way.

- **A config section that cannot be read no longer half-applies the
  rest.** The boot-time merge of the database config sections walked
  them in order and aborted on the first failure, after writing the
  earlier ones into the running config and before the defaulting pass —
  so a single unreadable section (a sealed value whose master key is
  gone, a row from a newer version) left the daemon on a config that was
  part database, part config file, never defaulted or validated. An auth
  scheme the operator had switched off in the SPA came back up because
  its section was merged after the failing one. The merge is
  all-or-nothing now: on failure the daemon runs on the config file
  exactly as loaded, says so at error level, and reports `config.overlay`
  as degraded on `/health` — the same failure also makes
  `GET /api/v1/config` fail, so there is no in-UI hint otherwise.

- **Disabling the last CCU keeps it disabled across a restart.** A
  centrals table in which every row is parked read as "the database has
  nothing to say about centrals", so the daemon fell back to the
  `centrals:` list in the config file — the very entry the first-run
  seed had copied into that table — and reconnected to the CCU the
  operator had just parked. `GET /api/v1/config` agreed with the wrong
  answer, attributing the resurrected central to the config file. A
  table with rows is now authoritative whether or not any of them is
  enabled; an empty table still leaves the config file in charge.

- **`bootstrap.allow_first_run_setup: false` now closes the onboarding
  surface it names.** The toggle has been documented as a hardening
  control since the first release and no code ever read it. An operator
  who set it and later ended up with an empty users table — a restored
  volume, a wiped data directory — still had `GET /api/v1/setup/status`
  reporting first-run and `POST /api/v1/setup` accepting an anonymous
  request, so anyone who could reach the listener registered themselves
  as the admin. The probe now honours the flag, and the finalize call
  answers `403` naming the setting instead of a `409` claiming an
  account exists. The deliberate consequence, now documented and logged
  at boot as `setup.onboarding.dormant`: with the flag false and no
  authentication source at all there is no way in except editing the
  YAML and restarting. (REST API 5.24.0)

- **A per-interface `remote_path` reaches the CCU.** The override was
  parsed, validated, stored, exported in the OpenAPI schema and rendered
  in the config editor, while the endpoint composer hard-coded `/RPC2`
  (`/groups` for VirtualDevices). Anyone whose CCU sits behind a reverse
  proxy that exposes XML-RPC elsewhere watched every call 404 with no
  hint that the configured path had been discarded. The composed URL now
  honours it, and a path that is not absolute — or the bare `/`, which
  crashes the CCU's putParamset handler — is rejected at config load
  rather than at the first RPC.

- **`rpc_type` no longer accepts a transport the daemon cannot use.**
  The transport follows from the interface name (CUxD speaks BIN-RPC,
  everything else XML-RPC) and nothing consulted the per-interface
  value, so `rpc_type: binrpc` on BidCos-RF was accepted, saved, shown
  as saved, and ignored. A value that contradicts the derived transport
  is now a config error that names both; a value that agrees still
  loads.

- **A password reset now ends the sessions it is meant to end.** User
  names are stored lower-cased, but a login reported back whatever
  casing the person typed, and that spelling went into the session. An
  admin can only address the account by its stored spelling, so
  resetting the password of someone who had signed in as `Markus`
  evicted nothing: the handler answered 204 while the old cookie kept
  full access for the rest of the session lifetime. Deleting the account
  behaved the same way, and left the subject's bearer tokens alive too.
  A login now reports the stored name, and the session and token purges
  match case-insensitively regardless.

- **A CCU login is now the same principal however it is typed.** Logins
  validated against a CCU's own user database reported the subject with
  whatever casing the person entered, so signing in as `Markus` and as
  `markus` produced two identities out of one CCU account: separate
  per-user preferences, separate privately owned diagrams, two actors in
  the audit trail, and an API token — stored canonically — that belonged
  to neither. The CCU is still asked about the name as typed, since it
  owns that namespace, but the identity the daemon files everything
  under is the canonical one every other login path already reported.

- **A channel-configuration edit can no longer drop the WebSocket
  connection.** Staging a parameter whose value was a JSON array or
  object — anything but the scalar a paramset actually holds — crashed
  the command that stages it as soon as the same value was sent twice:
  the two values were compared with an equality that only works on
  scalars. The connection died mid-frame without being closed, so the
  browser lost every live update until it reconnected and the daemon
  leaked the writer goroutine behind it. Comparing two staged values now
  copes with any shape, and a non-scalar value is refused up front with
  a clear error instead of being staged and failing later against the
  CCU.

- **Creating a user can no longer overwrite one.** `POST /api/v1/users`
  upserted: re-submitting an existing name silently rewrote that
  account's password and role and answered 201 — without the session
  revocation the update path performs, so an attacker's cookie kept the
  old admin role after the "reset". The route is create-only now and
  answers 409 for a name that exists (compared case-insensitively);
  changes go through `PATCH /api/v1/users/{subject}`, which revokes.
  (REST API 5.23.0)

- **The last admin can no longer be demoted into a lockout.** Deleting
  the only admin was refused, but changing its role to operator or
  viewer was not — and a daemon with zero admins answers 403 on every
  admin route, including the one that would create a new admin.
  Recovery meant editing the database by hand. The user store now
  refuses the demotion in the same transaction that counts the admins,
  and both the API and the Users view report it.

- **Deleting one entry from a map-valued setting sticks.** Trimming a
  single key out of `north.rest.auth.ccu.role_mapping` (or
  `north.ui.profiles`) was accepted with a success toast and then
  quietly restored: the saved section was assembled by decoding the
  request over the stored value, and decoding a JSON object into an
  existing map only ever adds to it. So a CCU user level the operator
  had just stopped mapping to admin kept mapping to admin. A key the
  request carries is now authoritative; a key it omits still keeps its
  stored value.

- **The OIDC login start is no longer an unmetered allocator.**
  `GET /api/v1/auth/oidc/start` needs no credentials and parks a PKCE
  verifier plus nonce in memory for five minutes per call, with nothing
  bounding how many. It now carries the same per-IP speed bump as the
  login POST, and the in-flight flow table has a hard ceiling — which
  also caps the scan every new flow performs while holding the lock, the
  part that slowed down genuine logins.

### Fixed

- **The device page follows the device in the address bar.** Opening a
  second device while a device page was already open — a pasted
  `#/devices/<address>`, a deep link, browser back/forward between two
  device pages — kept rendering the first device's data under the second
  device's URL and breadcrumb. Every write issued from that page (rename,
  delete, MASTER save, room or function assignment, link edit) went to
  the device the operator was no longer looking at. The view now reloads
  on the address it is given, and a response for a device that has since
  been left is discarded instead of repainting the page.

- **"Away" on an HmIP thermostat tile no longer switches to manual
  heating.** The away segment of the mode row was sent as a mode, and
  away is not one: the daemon's mode write only knows auto, heat, cool
  and off, so the button silently wrote manual mode at the current
  setpoint. It now triggers the away operation, is offered only for
  devices that support away, and leaving away clears the away window
  first, which a plain mode write does not.

- **System-variable values update live again.** The daemon has been
  pushing every system-variable change over the event stream, but the
  SPA translated the frame to a shape nothing consumed, so the list only
  ever showed what the last reload fetched.

- **Charts, pickers and the device page discard superseded responses.**
  The history chart, the multi-series diagram chart, the diagram series
  picker and the system-variable channel picker each let a slower earlier
  request overwrite the answer to a newer one — showing another range's
  history, another device's channels, or a parameter list belonging to a
  device the series no longer points at. Each fetch is now tied to the
  selection it was started for. The history chart also stopped issuing
  its initial request twice.

- **Favorites no longer survive a user switch.** After a logout and a
  login as somebody else in the same tab, the pinned devices, channels
  and programs of the previous operator stayed on screen, and the first
  star toggled wrote them into the new operator's own favorites. Pinned
  items are now scoped to the identity they were loaded for, and so is
  the per-user start page.

- **The group editor dialog now closes on Escape and keeps keyboard focus
  inside it.** Escape only ever reached the dialog's own root element,
  so a keyboard user whose focus was still on the button that opened the
  dialog — nothing inside it was ever focused — could not close it with
  Escape, and Tab could cycle out to the page behind the modal. Opening
  the dialog now moves focus in and restores it to the trigger on close,
  mirroring the confirm dialog's existing pattern.

- **The audit log and the device history tab discard superseded
  responses.** Changing the audit log's date filters, or switching
  channels on a device's History tab, before the previous request
  returned could let the older, slower response overwrite the newer
  one — an audit trail that silently disagreed with the visible filter,
  or a history chart showing the previous channel's parameters under the
  channel the picker still shows selected. Both are now tied to the
  selection that started them.

- **A CCU firmware-update poll no longer outlives the panel that started
  it.** Navigating away from Settings → System while an install was
  still in progress and a poll tick's fetch was in flight left that
  fetch to reschedule itself after the component was gone, so the daemon
  kept polling `GET /system/update` every five seconds with nothing left
  to stop it.

- **Two more missing translation keys.** The Rooms & Functions area save
  confirmation and the Favorites program run button each referenced a
  key neither catalogue defined, so both showed the raw key
  (`areas.toast.rooms_saved`, `programs.execute`) instead of a
  translated message.

- **Hour-axis labels and the sunset/sunrise glyphs in the weekly
  schedule strip now invert in dark mode**, matching every other
  colour-bearing element in the same chart.

- **A Matter controller can no longer take over another controller's
  session slot.** The bridge reserves one secure-session id per CASE
  exchange, and Apple Home opens a second session on an exchange it has
  already used. The second session was registered under the id the first
  one still held: the first controller's session disappeared from the
  table with its keys live and its subscriptions still registered, so
  the bridge kept serving them — encrypted for the wrong controller.
  Each handshake now takes its own id, and a session that would displace
  a live one is torn down properly first.

- **Aborted Matter handshakes give their session id back.** A CASE
  exchange that sent Sigma1 and never finished reserved one of the
  65534 session ids for the lifetime of the daemon. Enough of them —
  which a device on the same network can produce on its own — and every
  further handshake was refused, including legitimate reconnects from an
  already-paired controller, until a restart. Reserved ids now expire,
  and an exhausted id space reclaims the oldest reservation instead of
  failing.

- **Matter announces the port it actually listens on.** With
  `north.matter.listen` set to port `0` — the way to let the operating
  system pick a free port, e.g. next to a second Matter bridge on the
  same host — every mDNS record still named 5540. Commissioners resolved
  the announcement and sent their first packet to a port nothing was
  listening on, so pairing timed out against a perfectly valid QR code
  while the log reported the announcement as successful, and a bridge
  paired by direct addressing became unreachable after a restart.

- **The Matter bridge no longer reports one bridged device too many.**
  The endpoint count shown in the Matter view, published on the event
  stream after every topology rebuild and logged at startup counted the
  aggregator endpoint — bridge scaffolding, not a device. With nothing
  exposed the SPA showed one bridged device; every other topology was
  one too high. (REST API 5.22.1)

- **An abandoned Matter timed write no longer leaks.** A controller that
  announces a timed write or command and never sends it — a dropped
  packet, a backgrounded app, a controller reboot — left the deadline
  behind, and closing the session did not reclaim it either. The
  bookkeeping only ever grew, for as long as the daemon ran, and a peer
  could drive it there deliberately. Deadlines now expire and are
  dropped with the session that registered them.

- **A Matter controller that already holds events no longer gets them
  all again.** A read or subscribe can carry "the lowest event number I
  still want", which controllers send on every reconnect so they receive
  only what they missed. The bridge read that number out of the wrong
  field of the request, so it always came out as zero and the filter had
  no effect: every reconnect replayed the whole buffered event history —
  up to 112 records — and lock operations or switch presses the
  controller had already seen arrived a second time.

- **"Identify" on a bridged device does something again.** Identifying an
  accessory from Apple Home, Google Home or chip-tool was acknowledged
  with success and then had no effect whatsoever: the remaining identify
  time read back as zero on the very next request, no controller ever saw
  a countdown, and each attempt left a timer running in the background for
  as long as the requested duration — up to eighteen hours, once per
  attempt. The cluster is now bound to the endpoint, so it keeps the state
  it was given across requests and across a topology rebuild, and it stops
  when the device leaves the bridge.

- **A removed device's Matter endpoint number is not handed to the next
  device.** Endpoint numbers were reissued as soon as they were free, so
  the first device bridged after a device was unpaired — or after a
  channel was un-exposed — took over the number the old one had. Apple
  Home and Google Home cache an accessory by its endpoint number and are
  told nothing when the bridge's endpoint list has the same numbers as
  before, so the new device showed up under the removed device's name,
  icon and room until the bridge was removed and re-added by hand.
  Numbers now advance and a released one stays retired.

- **A vendor-specific Matter message is no longer mistaken for an
  Interaction Model one.** The protocol header put the protocol id where
  the wire carries the vendor id and vice versa. Both fields are absent
  from the traffic the bridge normally sees, so nothing showed until a
  controller sent a vendor-qualified message: its vendor id was then read
  as the protocol id, and a payload the bridge does not implement was fed
  into the Interaction Model dispatcher instead of being rejected. The
  header now follows the field order every other Matter stack uses.

### Security

- **A Matter write is authorized where it lands.** A write whose path
  left out the attribute or the cluster was checked once, against the
  default privilege for the un-expanded path, and then applied to every
  attribute of the cluster it resolved to. A controller holding only
  Operate — a guest ecosystem, a shared-home account — could target the
  Access Control cluster this way and rewrite the access rules
  themselves, which requires Administer. Such a write is now refused
  outright, as the Matter specification requires; a write that names
  every endpoint is authorized separately at each endpoint it reaches,
  so it can no longer touch endpoints the controller's own access rules
  do not cover.

- **A Matter controller is bound to the fabric its certificate names.**
  Verifying a controller's operational certificate proved it chained to
  the fabric's root, but never that the certificate was issued for THAT
  fabric. Where two fabrics are provisioned from one certificate
  authority, a controller could present its own fabric's certificate,
  complete the handshake against another fabric, and read and write that
  fabric's data. The check existed but could never run, because the
  daemon's certificate verifier did not expose the fabric id it needed.

- **A Matter session no longer inherits the previous controller's
  authenticated tags.** CASE Authenticated Tags identify the
  administrator group a controller belongs to, and access rules can be
  written against them. When a second controller ran a handshake on an
  exchange a first one had used, and its own certificate carried no
  tags, the previous controller's tags stayed attached — so the new
  session matched every rule written for the previous controller's
  group.

- **The frozen health-score, latency and last-event-age topics of a
  renamed CCU subtree are cleaned up.** The three central-wide metric
  sensors used to lower-case the CCU name into their topic while every
  other topic on the plane escapes it; correcting that left the values on
  the old topics behind, retained on the broker and never updated again,
  so a dashboard or automation subscribing them kept reading a health
  score from the previous build. The opt-in retained-cleanup pass now
  clears them — but only where the two spellings actually differ. For a
  CCU whose name needs no escaping the old and the new topic are the same
  string, and for those deployments (and for any topic another configured
  CCU publishes to) the pass does nothing at all, so no live value is
  ever blanked.

## [0.59.0]

### Added

- **Matter bridge diagnostics.** Three further views into what the
  bridge is doing, reachable under **Matter → Diagnostics**:

  *Discovery.* What the bridge actually announces over mDNS, and what
  would keep a controller from finding it: missing service subtypes
  (Apple and Google browse through those, so without them the bridge is
  invisible to both while chip-tool finds it), addresses only reachable
  inside a container, a commissioning port other than 5540 (Alexa uses
  no other), an announcement without IPv6. Each of these leaves the
  daemon looking correct — advertising succeeds and the log says so.

  *Endpoints.* The assembled topology as a controller sees it, with
  device types and clusters. Until now the only way to look at it was to
  commission a controller and browse with chip-tool, so every "why is
  this device missing" question started with a pairing step.

  *Ecosystem compatibility.* Each paired fabric is classified by
  controller vendor, and the exposed device types are checked against
  what that ecosystem accepts — a valve Google and Alexa will not show,
  a leak detector that makes Alexa drop the entire bridge, an endpoint
  count past where Alexa becomes unreliable. The bridge cannot observe
  any of this: it exposes the endpoint correctly and the ecosystem
  silently omits it. (REST API 5.22.0)

- **The bridge can say whether a Matter controller is still talking to
  it.** `GET /api/v1/matter/sessions` lists every open secure session
  with two separate ages: when the session last carried traffic in any
  direction, and when the controller last sent something. The difference
  is the point. A controller that goes away without closing its session
  leaves it open and simply stops sending, so the bridge keeps reporting
  into it — from every other angle that looks healthy, and in the
  ecosystem it shows up only as entities that quietly stop updating.
  Each session also reports how many subscriptions ride on it: a
  commissioned controller holding none is connected but receiving
  nothing.

  The daemon has tracked all of this internally for the idle-session
  reaper; none of it could be seen from outside. No key material is
  exposed. (REST API 5.21.0)


## [0.58.6]

### Security

- **Go toolchain 1.26.6.** The 1.26.5 standard library carries seven
  advisories on code paths this daemon actually calls, across the XML
  decoder the XML-RPC transport and SSDP discovery use, the ASN.1 decoder
  behind Matter CSR generation, and `net/http`. All are fixed in 1.26.6;
  `go.mod`, the builder image, and every workflow move together.

### Fixed

- **A siren stop now actually beats the queue it is supposed to beat.**
  Stop commands are marked critical so they skip the command throttle
  and are still attempted while the circuit breaker for a struggling CCU
  is open — the difference between a siren that can be silenced and one
  that cannot. The mark never arrived: every layer between the alarm
  engine and the wire declared a priority, and the last one dropped it,
  because the object that talks to the transport is built once per
  interface and carries a single fixed priority for every call it ever
  makes. Measured through the real write path: a critical write produced
  no wire attempt at all past an open breaker, exactly as an ordinary one
  does. The priority now travels with the command. Bounded activations
  are covered too — those leave as one `put_paramset`, which carried no
  priority at all.

- **Direct-link operations record telemetry again.** The link
  coordinator's observability recorder was never handed over, so every
  add and remove wrote into the no-op default while its neighbours on
  the same central were wired.

- **A siren that fails to fire no longer does so quietly.** An output
  command that fails — an activation during an incident, a stop, an
  operator's test — now records a journal entry *and* a health signal
  naming the output. Two paths were silent before. A failed test fire
  reported HTTP 502 to whoever pressed the button and left nothing
  behind, while successful tests were journalled, so the record of a
  siren sweep listed only the outputs that worked. And a failed
  activation during a real incident was journalled but never touched
  health, so `/api/v1/health` kept reporting the alarm domain healthy
  while a siren had not gone off. A test fire the daemon refuses by
  design — a smoke-detector sounder, where each activation costs
  irreplaceable battery life — is deliberately not reported as a
  degradation.

- **A successful siren test is no longer filed as a fault.** Every test
  fire was journalled under the `fault` class, so an operator filtering
  for faults saw one per test that worked, burying the failures the
  filter exists to surface. They are filed under `test`, which the
  journal has always had.

- **A siren still sounding after a restart is dealt with again.** The
  alarm engine reads the live state of every enrolled siren when it
  starts and reconciles: one whose zone is armed is adopted as a
  triggered incident — journalled, notified, kept sounding within its
  bound — because it is evidence of a trigger during the window the
  daemon was down; one whose zone is disarmed is switched off. That pass
  ran at the end of the alarm service's start, which is the same second
  the daemon starts and long before the CCU has answered with a single
  device, so it read an empty model and found nothing. It never ran
  again: the two other entry points are a central adopted at runtime and
  a connection returning after a total loss, and an ordinary restart is
  neither. A siren left sounding across one was therefore neither
  adopted nor stopped, on every path, with nothing logged. The pass now
  also runs when a CCU reports its device model complete. Confirmed
  against a real CCU: before, a switched siren burned for 153 s after a
  restart into a disarmed zone until it was switched off by hand; after,
  it stops 21 s in, and an armed zone opens an adopted incident instead.
- **A timed switch-on reaches the device as one radio message again.**
  Switching something on for a bounded time writes two things: how long
  it may stay on, and that it is on now. Both belong in the same
  message — the device then applies its own auto-off even if the daemon
  dies in the next second, and the pair costs one transmission instead
  of two out of a duty-cycle budget a following stop command competes
  for. The daemon has a collector that exists to bundle exactly this,
  but the duration was written straight past it, so the collector only
  ever saw the switch-on and the pair left as two separate calls.
  Verified on the wire against a real HMIP-PS before the fix, where it
  showed as two `setValue` calls five milliseconds apart. Ramp and
  on-time durations on dimmers took the same detour and are bundled
  now too. Separately, the writer every data point is handed could not
  write a parameter set at all, which left the atomic path unreachable
  even where nothing opened a collector; it can now.
- **The Refresh button on the Matter diagnostics view showed its
  translation key.** `common.refresh` had no entry in either catalogue,
  so the button rendered the literal string `common.refresh`.

- **A second CCU no longer loses its virtual-remote entities.** A few
  address classes are identical on every CCU — the virtual remotes, the
  internal devices, the hub itself — so what separates their entities is
  the CCU's serial. The serial is resolved by the hub bring-up, on a
  different path than the devices take, and the device snapshot did not
  wait for it: entities were declared with the serial slot left blank,
  which made two CCUs announce the very same entity id. Home Assistant
  keeps whichever it saw first, so the second CCU's virtual remotes were
  missing — and stayed missing, because the announcement is retained on
  the broker.

  The snapshot now reads the serial straight from the CCU it belongs to
  before writing anything, and an entity that would need a serial it does
  not have is not announced at all rather than announced ambiguously. It
  appears on the next snapshot, once the serial is known. Single-CCU
  setups were never affected.

  Announcements already on the broker are cleaned up on the next start:
  the daemon withdraws the ambiguous ones before re-announcing, so the
  duplicate that would otherwise appear beside each corrected entity
  never shows up. Nothing has to be deleted by hand, and other
  integrations sharing the broker are left untouched.

- **Tunable-white dimmers report and accept a colour temperature.** The
  RF families (HM-LC-DW-WM, HM-DW-WM) have no colour-temperature
  parameter at all: they express the white point as a second dimmer
  channel. The daemon only ever read the parameter the HmIP devices use,
  so on these lamps the colour temperature was absent in both
  directions — nothing to read, nothing to set. The white-point channel
  is now converted through mireds, the same arithmetic the reference
  implementation uses, so a value set comes back as the value read.

## [0.58.5]

### Fixed

- **Dimmers and covers report their group brightness again, and colour
  dimmers their effects.** A dimmer's group level comes from a different
  parameter per family — the HmIP devices report it on the group's state
  channel, the RF ones on the light's own channel — and both are
  read-only there, while the daemon asked for the writable form on the
  light's channel alone. Neither family ever filled the slot, so the
  group brightness a north-bound consumer reads stayed absent. The same
  applied to covers. The effect list of an RF colour dimmer was empty for
  a related reason: its effects parameter sits two channels above the
  light.

- **An RGBW light knows which mode it is in.** HmIP-RGBW, HmIP-LSC and
  HmIP-DRDI3 report their operating mode on the device's channel 0, and
  the light read it on its own channel — where it does not exist. Three
  things had to be wrong at once for it to stay unnoticed: the lookup
  channel, the value shape (the mode is an enum, whose wire value is an
  index rather than a word), and the two mode names, which carry a
  channel-count prefix the daemon did not expect. The light therefore
  fell back to "assume plain brightness" on every device, so a lamp in
  RGB or tunable-white mode advertised the wrong capabilities and its
  secondary channels surfaced as entities of their own.

- **A key-matic reports which way it turned.** The lock's direction
  slot was filled only for HmIP door locks, which do not have it, while
  the HM key-matic family — which does — took the branch that skipped
  it.

- **The sound player reads its selected sound file, and the display its
  burst-limit warning.** The sound file is writable on the player's own
  channel, so it never matched the read-only shape the code looked for;
  the burst-limit warning sits on the device's channel 0, not on the
  display channel.

- **A siren can be silenced again, and sounds a defined alarm.** The two
  alarm-selection parameters are write-only ENUMs on the wire, so the
  daemon builds a write-only selection for them — but the siren looked
  them up as readable text, which matches nothing on any device. Both
  slots were therefore empty, and with them the "disable" value the CCU
  declares: switching a siren off wrote an empty selection, which the CCU
  rejects, and switching it on without naming a tone sent no selection at
  all, leaving the device to repeat whatever was set last — including the
  disable tone a preceding off-command had left behind. Both commands now
  name the value they mean. The optical channel also gets its own disable
  value; it was being sent the acoustic one.

  Five further data points were absent for the same reason and are now
  found: the smoke detector's command, the garage door's command, the
  sound player's repetitions, and the sound-LED's repetitions and
  on-time list. Their state changes reach the north-bound planes again.

## [0.58.4]

### Fixed

- **A parameter no longer arrives in Home Assistant under two different
  names.** The same data point was called "Frostschutz" over the REST
  drop-in and "Frostschutz ch1" over MQTT discovery. The daemon composes
  entity names in one place, and that composer appends the multi-channel
  marker `chN` only when the channel name alone cannot identify the
  channel. The MQTT path had its own copy of that decision which tested
  two of the three conditions, so it marked up channels the authority had
  already found unambiguous — visible on any device that carries a
  parameter on several named channels, such as FROST_PROTECTION on an
  HmIP-BWTH (channels 1 and 8). The marker is now decided once.

- **Enum values are shown in the operator's language over MQTT.** A
  select or enum sensor published its options as the CCU's raw tokens
  (`auto_mode`, `manu_mode`) and relied on Home Assistant translating
  them. A discovered MQTT entity has no translation table behind it, so
  the tokens stayed on screen. The options now carry the same localised
  labels the REST and UI surfaces show ("Automatik", "Manuell"), and a
  value the translation archive does not cover is humanised the same way
  it is everywhere else (`AUTO_MODE` → "Auto Mode").

  Writing still reaches the device: the discovery payload maps the chosen
  label back to the CCU's own token. Automations that compare an entity's
  state against a raw token (`auto_mode`) need to compare against the
  label instead — the state is what an operator sees. Where labels would
  be ambiguous (two values sharing one label), the raw tokens are kept.

### Changed

- **The `/config` companion topic of an `ENUM` parameter carries
  `value_labels`.** The localised display strings sit next to the
  unchanged `value_list` tokens, index-aligned, so a consumer can render
  the label and still address the value. Existing fields keep their shape.

## [0.58.3]

### Fixed

- **Saving a schedule no longer shortens its switching durations.** The
  CCU stores a duration as a time base plus a factor, and the schedule
  editor rendered that pair by magnitude: a slot set to 65 seconds came
  back as "1min", one set to 1.2 seconds as "1s". The displayed string is
  what gets written on the next save, so opening a schedule and saving it
  unchanged handed the device a shorter duration than it had — 65s became
  60s, 70 minutes became an hour. Durations are now rendered exactly, so
  the value survives the round trip.

- **Door locks and long switching durations reappear in week profiles.**
  The week-profile surface — MQTT and the `/week_profile` endpoints —
  silently discarded any entry it could not re-validate on the way in.
  That caught more than it was meant to: every door-lock slot the CCU
  encodes as "until further notice" (an unlock, an auto-relock end, any
  standing user permission) and every switching entry whose duration sits
  on a coarse time base, such as 12 minutes. Those schedules existed on
  the device and were simply absent here. What the CCU holds is now
  reported as it stands.

- **A fixed-time entry no longer claims a sunrise.** The week-profile
  path read the CCU's ASTRO_TYPE field on every group, including groups
  that switch at a fixed time and ignore it, and reported them as tied to
  sunrise.

### Changed

- **Schedule durations are reported in the unit the device stores them
  in.** `duration` and `ramp_time` on `SimpleScheduleEntry` now carry the
  exact value — a slot the CCU holds as 13 × 5 seconds reads "65s", and
  one it holds as 24 × 5 seconds reads "120s" where it previously read
  "2min". Both forms were always accepted on input and still are; this
  changes what the API reports, and it is what makes the round trip
  lossless. (REST API 5.20.0)

- **Six of the eight schedule conditions were named after a different
  rule than the device applies.** The `<NN>_WP_CONDITION` integer is
  translated in two places, and they disagreed. Condition 2 was called
  "astro before fixed" where the CCU selects the *fixed* time when it
  falls before the astro one — the two roles swapped — and 6 and 7 were
  called "between" and "or" where the device picks the earlier or the
  later of the two times.

  Reading a schedule through the week-profile path therefore reported a
  rule the device does not implement. Writing was worse: the reverse
  lookup did not recognise the correct names at all and fell through to
  0, so a caller asking for "earliest of fixed and astro" silently got a
  plain fixed time. The REST schedules domain had the vocabulary right;
  it now comes from the CCU editor's own option list, and a guard
  compares both translations against it.

- **Week profiles keep every entry past the 24th.** A switching,
  dimming, blind or servo channel carries 75 schedule groups (69 on the
  models the CCU's web UI special-cases), and the CCU's own editor uses
  all of them. This daemon stopped at 24 — a number describing its own
  storage, not any device. A schedule built past that point on the CCU
  was truncated on read, and the write path rejected the slots outright,
  so such a schedule could be opened here but never saved back.

  Confirmed against a real CCU, which stored and returned a group-25
  entry written over XML-RPC. The four places that each repeated the
  limit now share one named constant.

  The sweep that clears deleted entries is bounded by what the target
  channel actually declares, read from the device: naming a group a
  channel does not have fails the whole paramset with a `-5` fault, so
  a fixed upper bound would have broken every 69-group model.

- **The MQTT motion-reset button is findable and translated.** It
  carried `entity_category: "config"`, which files an entity into a
  collapsed section of the Home Assistant device page and keeps it out
  of dashboards — right for a setting, wrong for a control an operator
  presses during an incident. The panel entity beside it never had a
  category either. Its name was also assembled from an English literal
  (`"… — reset motion"`) instead of the translation catalogues, so a
  German installation read half-English entity names. Both the button
  and the latched-detector count are now named through i18n; the count
  stays `diagnostic`, since that one really is a readout.

- **Sunday works in device week profiles again.** The
  `<NN>_WP_WEEKDAY` bitmask was read with Sunday on bit 7. The CCU puts
  it on bit 0 — the mask runs Sunday=1, Monday=2 … Saturday=64, so all
  seven days are 127, not 254.

  The consequence ran both ways and was silent in each. A schedule set
  on the CCU for Sunday arrived here with an empty weekday list, so the
  entry looked like it applied to no day at all. A schedule saved from
  here for Sunday set a bit the device does not evaluate, so it simply
  never fired — the entry looked correct everywhere it was displayed
  and did nothing. Every other day was unaffected, which is why this
  survived: a Monday-to-Saturday schedule round-tripped perfectly.

  The layout is taken from the checkbox values the CCU's own editor
  emits (`_getWeekDay` in `HmIPWeeklyProgram.js`) and was confirmed
  against a real CCU: written `WEEKDAY=1`, stored and returned as `1`,
  while this daemon reported the entry as having no weekdays. Both
  tables that encode the mask — they had drifted apart from each other
  as well — now assert their values against that source.

## [0.58.2]

API 5.19.0 — additive: a hidden-parameter candidate gains
`reason_detail`.

### Fixed

- **Week-profile cells above group 24 are filed as week profiles
  again.** The hidden-parameters screen sorted two thirds of them —
  657 of 969 on a real installation — under "MASTER setting" instead,
  which put them in the open list rather than the collapsed
  week-profile category they belong to, defeating the tidying the
  screen exists for. The predicate that recognises a cell stopped at
  group 24, a limit that describes this project's own schedule storage
  and says nothing about the parameter.

  The CCU declares 75 such groups on a dimmer, switch, blind or servo
  channel and 69 on the models its web UI special-cases, and it edits
  every one of them (`_getMaxEntries` in the CCU's own
  `HmIPWeeklyProgram.js`). Confirmed against a real CCU, which stored
  and returned a group-25 entry written over XML-RPC. Recognition no
  longer caps; the storage limit stays where it belongs and is now
  named rather than repeated as a literal.

### Changed

- **A suppressed-name badge names the rule instead of its category.**
  "Name prefix" left an operator to work out which of seven prefixes
  applied; the badge now reads "Prefix `STATUS_FLAG_`" or
  "Suffix `_STATUS`". Reasons whose rule is a membership list rather
  than a pattern are unchanged — there the parameter name already is
  the entry.

## [0.58.1]

API 5.18.0 — additive: the parameter enumeration gains
`PRESENCE_DETECTION_STATE` and `RESET_PRESENCE`.

### Fixed

- **Resetting triggered motion detectors actually reaches the
  device.** The feature shipped in 0.58.0 was inert on real hardware:
  no reset button ever appeared, `GET /alarm/triggered-motion` reported
  an empty set however many detectors were latched, and the pre-arm
  reset pass wrote nothing. `RESET_MOTION` is classified as a button
  action, so the model holds it as a `Button`, while the reset looked
  for the `Action` shape. The type assertion was false for every real
  detector — which is a silent runtime miss, not a compile error, and
  the same lookup decides both what gets written and what gets counted,
  so the operator saw a consistent and consistently empty answer.

  The lookup now depends on the capability (fire this parameter) rather
  than on one concrete shape. Verified against a live CCU whose zone
  readiness reported three blocking detectors while the endpoint
  returned none.

- **Presence detectors are covered too.** An HmIP-SPI latches
  `PRESENCE_DETECTION_STATE` and clears it with `RESET_PRESENCE`, not
  `MOTION` / `RESET_MOTION`, so it was skipped even once the shape
  mismatch above was out of the way. The reset parameter is now derived
  from the enrolled state parameter, which keys it to what the device
  actually exposes instead of to the sensor type both families share.

### Changed

- **The Security & Safety and alarm panels link to each other the same
  way.** The cross-link into the alarm panel sat below the zone list as
  a borderless button while its counterpart sat in the header toolbar
  with a border and an icon. It now occupies the mirrored toolbar slot
  with the same variant and the navigation's alarm icon, and no longer
  appears twice on one screen.

## [0.58.0]

### Added

- **Triggered motion detectors can be reset — per zone or all at
  once.** A motion detector holds its `MOTION` flag until the device's
  own blocking time expires or the parameter is written. While it does,
  the sensor reads as open, which blocks an arm or forces an
  auto-bypass — and until now there was no way to clear it short of
  waiting. The alarm overview offers a reset beside each affected zone
  and one in the toolbar. Both appear only while a detector is actually
  latched, and both say how many they would clear; a detector that is
  not triggered is never written to.

  The set that gets reset and the count that is displayed come from one
  predicate — currently active *and* the channel exposes a writable
  `RESET_MOTION` — so the number an operator sees can never name a
  detector the button would skip. Door contacts fall out of it by
  construction.

- **Arming clears them automatically.** `beginArm` writes the reset to
  the zone's latched detectors before the exit delay starts, so the
  clearing has the whole delay to take effect. It deliberately does not
  feed into the arm decision: the reset is asynchronous, and letting it
  pre-empt the blocker check would treat a detector that is latched
  *because somebody is moving in the room* as clear. The existing
  blocker and auto-bypass rules stay in charge.

- **Reachable from every north-bound bridge.** REST gains
  `POST /alarm/zones/{id}/reset-motion`, `POST /alarm/reset-motion` and
  `GET /alarm/triggered-motion` (API 5.17.0). MQTT accepts
  `RESET_MOTION` on the alarm command topic and publishes a reset button
  plus a `triggered motion detectors` counter per zone and for the
  master aggregate. MCP gains `list_triggered_motion` and `reset_motion`.
  Every reset is recorded in the alarm journal under the new
  `maintenance` class.

- **MCP catches up with the rest of the daemon.** The adapter had not
  gained a tool since it landed, while eighteen REST domains were built
  around it — the alarm system alone had grown to 35 routes while MCP
  could read incidents and nothing else. New tools:
  `list_alarm_zones`, `list_triggered_motion`, `get_security_status`,
  and, behind `AllowWrites`, `arm_alarm_zone`, `disarm_alarm_zone`,
  `reset_motion`. The arm and disarm tools take no code argument on
  purpose: a code is a human authorization factor, so zones that
  require one refuse an assistant-driven arm.

  A contract test now compares the tool catalogue against the REST
  router in the direction that actually drifted — a new capability with
  no tool. Domains that are deliberately not projected (account
  administration, daemon configuration, the first-run wizard) are
  declared with the reason; domains that are merely still pending are
  declared separately, so "decided against" and "not done yet" cannot
  wear the same face. A domain in neither list fails the build.


- **`GET /api/v1/visibility/unignore/candidates` returns grouped
  candidates** — a new `groups` array carries one entry per (parameter,
  paramset) with its models, channels, per-scope patterns and the
  suppression reason, plus a `reasons` vocabulary for building filters.
  The reason is recomputed from the same rule sets the suppression
  passes consult, so the categories cannot drift from the rules; an
  integration test over the whole embedded fleet fails on any candidate
  that no rule explains. The flat `candidates` field and the
  `include_master` query parameter are unchanged. API 5.16.0.

### Changed

- **Hidden parameters are a workable screen again** — the picker listed
  every un-ignore pattern the fleet could produce, flat and
  alphabetical. On a 399-device fleet that is 2800 rows, built as the
  cross-product of parameter × device model × channel in three pattern
  formats out of only 45 distinct parameters. Enabling *Include MASTER
  parameters* added 943 more and the screen crawled, because each row
  asked whether its pattern was a candidate by scanning the full
  candidate array — an O(n) lookup inside an O(n) render, re-run on
  every keystroke.

  The screen is now parameter-first: one row per hidden parameter, which
  expands to the device models and channels it occurs on. The same fleet
  yields 45 rows instead of 2800. Each row carries the localized
  parameter name, a badge naming the rule that hid it, and a three-state
  toggle that distinguishes "enabled everywhere" from "enabled for some
  models". Membership lookups are set-based, so MASTER no longer slows
  the list down — it is now always loaded and offered as a filter chip
  rather than a checkbox that triggers a reload.

  Category chips filter the list, and the ones that describe internal
  plumbing — diagnostic bits, `FLAGS=INTERNAL` service values, the
  `_STATUS`/`_RESULT`/`_SUBMIT` name families, and week-profile cells —
  start collapsed, with a visible "hidden by the category filter: N —
  show all" so nothing disappears quietly. Week-profile cells matter
  most in practice: one climate device carries up to 6 profiles × 7
  weekdays × 13 slots × 2 fields, and they already have a schedule
  editor.

  Patterns that were saved earlier but match no current candidate are
  listed separately instead of vanishing, so a save cannot silently drop
  them.

## [0.57.2]

### Fixed

- **The mDNS TXT record advertises the CCU serials again** — every
  installation that runs without a YAML config file (the shape any setup
  that keeps its configuration in the database has) hit a nil-pointer
  panic the moment a central reported southbound-ready. The composition
  root reached into the reload bag's fields from a closure instead of
  going through one of its nil-guarded accessors, and `daemonServe`
  passes a nil bag on exactly that path. The recover in the hub-ready
  pipeline turned it into a single `restart_on_ready.panic` log line, so
  the daemon carried on with the ADR 0058 serial re-announce silently
  dead — discovery clients never received the `ccus=` list and
  `centrals=` stayed stale after a live adopt. Present since 0.50.0.

  A contract test now fails the build on any direct field access to that
  bag from outside its own file.

- **A busy WebSocket connection is no longer severed** — when a client
  fell behind, the daemon closed its socket. The boot snapshot fans out
  one frame per data point, which on a large installation is far past
  any per-client queue, so every open Config-UI session was cut during a
  daemon restart. An overflow now drops the oldest queued events and
  sends the client a `replay_lost` frame so it resyncs, keeping the
  session alive.

- **One overflowing connection no longer floods the log** — a closed
  client stayed in the hub's fan-out set until its read loop noticed, so
  the publisher kept selecting it and every attempt logged again. One
  session produced 413 `ws.backpressure` warnings in two seconds. The
  client now leaves the set immediately and the condition is logged once
  per episode.

### Changed

- **The boot snapshot no longer replays the model into the WebSocket
  stream** — it writes MQTT's retained topics as before and sends
  subscribers a single resync signal instead of tens of thousands of
  individual frames. The Config UI reloads the affected views on that
  signal; a channel editor with unsaved edits is left alone. The two
  north-bound planes also apply the same visibility rules now: the
  WebSocket side had none, so the ~780 MASTER week-program slots of a
  single channel were broadcast on every boot while MQTT correctly
  refused them.

- **An unset user preference reads as `null` instead of 404** — `GET
  /me/preferences/{key}` answers 200 with a null value. Every key starts
  unset and the Config UI asks for `favorites` and `start_route` on its
  first page load, so the old answer put a warn-level line in the log
  for ordinary use. API version 5.15.0; clients that already treat 404
  as "not set" keep working.

- **A co-starting CCU no longer reports errors that resolve themselves**
  — the first `listDevices` against a CCU whose per-interface RPC
  service still trails ReGaHss answers `http 503`, and the bring-up
  retries across a ~33 s window. That attempt is now logged at warn with
  `retried: true` rather than error, and slow calls against a booting
  CCU at info rather than warn. The final attempt keeps full severity,
  and an interface that never comes up is now an error rather than a
  warning.

## [0.57.1]

### Fixed

- **Restarting the daemon no longer replays keypresses** — a button that
  had been pressed once fired its Home Assistant automations again on
  every restart.

  A `PRESS_*` parameter reports an edge, not a level, but two boot paths
  treated it as restorable state: the persistent VALUES cache kept the
  last press across restarts, and the `fetch_all_device_data` seed
  re-read it from the CCU (the script emits every data point with a valid
  timestamp, which a button acquires on its first press and keeps). The
  boot-time snapshot then found the value observed and pushed it through
  the same per-channel `/event` topic a live keypress uses — same
  `event_type`, fresh timestamp, indistinguishable downstream.

  Edge-trigger parameters (`PRESS_*`, `CODE_ID`, `CODE_STATE`) now stay
  out of the values cache on both the write and the read side, the ReGa
  seed skips them, and the keypress pulse is gated on a live value change
  so neither the boot snapshot nor a cache-to-live source flip can emit
  one. Existing installations are covered without a database migration —
  the restore side rejects rows an older build already wrote.

- **The BidCos radio-utilisation poll reached the CCU again** — the
  `hub.bidcos_interfaces_refresh` job passed the daemon-internal
  interface handle (`<central>-BidCos-RF`) where
  `Interface.listBidcosInterfaces` expects the name the CCU knows
  (`BidCos-RF`), so every run failed with `unknown interface` and the
  duty-cycle / carrier-sense fields stayed empty on the interface list.

- **A panic during the hub-discovery re-wire now logs its stack** —
  `mqtt.hub_discovery.restart_on_ready.panic` recorded the panic value
  alone. The re-wire runs on its own goroutine and the recover keeps the
  daemon alive, so that line was the only trace of a hub plane that had
  been torn down and not rebuilt, and it carried no way to locate the
  fault.

## [0.57.0]

### Added

- **`GET /api/v1/info` now reports `config_ui_url`** — the
  externally-reachable address of this daemon's Config UI, derived from
  `north.rest.public_url` with the SPA mount appended. Empty when no
  public URL is configured.

  It answers a question a client cannot answer for itself. The address a
  client uses to *talk* to the daemon — a container network, a LAN
  address behind a reverse proxy — is not necessarily one a browser can
  follow. Only the operator knows that, and `public_url` is where they
  already record it; until now the value only reached the CCU add-on's
  `config.cgi` through a hint file.

  The first consumer is the Home Assistant integration, which will point
  its device pages at Loom's own device view instead of a config panel it
  no longer registers for loom-backed entries.

  The mount path is appended by the daemon on purpose: a client that had
  to know where the SPA lives would break on the next mount change. Empty
  stays empty rather than becoming a guess — the client's fallback is its
  own connection address, which it knows and the daemon does not (API
  version 5.14.0).

## [0.56.0]

### Added

- **The embedded mode now applies only where Home Assistant actually
  shows the UI** — new setting `north.ui.embedded_scope`, in the editor
  as "Where does the embedded mode apply?".

  The reason for hiding a view is specific: Home Assistant already shows
  the same editor, and two doors to one paramset confuse. That holds
  inside the HA panel. It does not hold for someone who opened this
  daemon's own address — they chose Loom over the panel, and until now
  they got the trimmed UI anyway, with a daemon-wide switch as the only
  way out.

  `inside_ha` (the **new default**) applies the embedded profile to
  requests that arrive through Home Assistant, including the remote proxy
  add-on; a direct visit keeps the full UI. `always` restores the
  daemon-wide behaviour of 0.55.x. Also available as
  `OPENCCU_LOOM_UI_EMBEDDED_SCOPE`.

  **This changes behaviour on upgrade**: a daemon with `embedded: true`
  that you open directly now shows the full navigation. If you want the
  reduced UI on every path, set the scope to `always`.

  The signal is the Supervisor's `X-Ingress-Path` header, not the auth
  scheme — the scheme cannot answer the question, since signing in to
  Loom from inside the panel is indistinguishable from a direct visit,
  and the bearer scheme covers both the remote panel and the integration.
  It is deliberately not an authorization boundary: the header is
  forgeable on the direct port, and all a forger gains is a shorter menu.
  That is only sound because hiding is navigation and nothing else, which
  is why the write enforcement had to go first (API version 5.13.0).

## [0.55.3]

### Fixed

- **The two fleet-wide overviews no longer deep-link into a tab the
  surface profile hides.** `/schedules` and `/links` are catalogues that
  hand off to a per-device editor. With "Configure → Schedule" (or
  "→ Links") switched off in **Settings → Navigation & views**, every row
  still offered the jump and landed on a device where that tab was gone.

  The listings stay — they answer "*which* devices run a program", which
  the device detail cannot answer at all — but their rows render
  un-linked, with a line above the list naming the reason and where to
  change it. Hiding the catalogue instead was rejected: it would make the
  editor's own table lie, showing a view as visible that nobody finds.

  The dependency is now declared in the registry (`opens`) rather than
  living only inside a view, so the editor states it from both sides:
  the catalogue's row reports that its editor is hidden, and the editor's
  row reports that hiding it drops the jump but keeps the list. Both ends
  are guarded — a declared target that is not in the registry, or a
  declared relation no view consults, fails the build (API version
  5.12.0).

- **Removed the last traces of the write enforcement dropped in 0.55.2.**
  The editor's banner still told operators that rows marked `⇄` decide
  whether Home Assistant may write to a surface. There are no such rows
  and no such rule — the sentence described a mechanism that no longer
  exists, in both locales. Same for the `write_gated` field named in the
  `/ui/surfaces` API description and four stale references in the design
  notes.

## [0.55.2]

### Added

- **A fleet-wide schedule overview at `/schedules`.** Until now the only
  way to learn whether a device has a week program was to open it: the
  Config UI had a per-device editor and no list. The new view answers
  "which devices have a schedule at all" across every CCU, and each row
  opens that device's editor — the counterpart to the existing
  direct-links overview.

  The list is derived from channel **types** — a `*_WEEK_PROFILE`
  channel, or one of the climate channel types that carry the profile in
  their MASTER paramset. It deliberately does not confirm against
  MASTER: confirming costs one CCU round-trip per thermostat, so opening
  the overview on a fleet of forty would pay radio budget for a question
  the detail view answers exactly once, on click. The badge on each row
  says which of the two paths matched.

  New endpoint `GET /api/v1/schedules` (API version 5.11.0).

### Changed

- Dependency refresh: `kin-openapi` 0.146.0, `go-internal` 1.16.0,
  `modernc.org/sqlite` 1.56.0, and the SPA's in-range npm updates
  (`vite` 8.2.1, `@lucide/svelte` 1.30.0, `svelte-check` 4.7.5,
  `happy-dom`, `@internationalized/date`, `@sveltejs/vite-plugin-svelte`).
  The icon set moved a few pixels, so every screenshot baseline is
  regenerated. TypeScript stays on 6.x and Playwright on 1.62.0: the
  first is a major with a known svelte-check interaction, the second is
  pinned to the container that renders the committed Linux baselines —
  both are their own change, not a side effect of this one.

### Removed

- **The write enforcement behind the embedded profile — it defended
  nothing.** A hidden surface used to refuse its writes for the Home
  Assistant Ingress passthrough identity, on the reasoning that hiding
  should not be merely cosmetic and that it would stop two editors
  writing the same CCU. Neither held: `openccu-loom-client`
  authenticates with a bearer token, and the passthrough resolver bails
  out as soon as a request carries any identity of its own — so the Home
  Assistant integration was never subject to it, and the duplication it
  claimed to prevent was between the HA panel and this UI, with the HA
  side exempt by construction. As a security boundary it was empty too:
  whoever reaches Ingress is an HA admin who can sign in here and write
  anyway.

  What it did reach was a browser opening this daemon's own UI through
  the add-on panel. Gone, with its middleware, its route table and two
  contract guards. **Settings → Navigation & views is navigation now,
  and says so**: the banner no longer claims a permission effect, the
  ⇄ badge and its confirmation are removed, and the mode dialog no
  longer promises read-only editors. Authorization stays where it
  belongs — roles, tokens and ADR 0051 — and nothing about what Home
  Assistant may do has ever depended on this setting.

  `GET /api/v1/ui/surfaces` drops `write_gated` (API version 5.10.0).

### Fixed

- **Settings → Navigation & views called some views by a different name
  than the navigation does.** The editor carried its own label
  catalogue, and it had drifted in 26 of 48 rows: the fleet view was
  "Fleet" in the editor and "CCUs" in the German sidebar, "Variablen"
  appeared as "Systemvariablen", "Meldungen" as "Servicemeldungen", and
  ten more in each locale. A row that names a view differently than the
  view names itself is worse than useless in an editor whose whole job is
  deciding which views to keep.

  The editor now resolves the navigation's own keys — `nav.*`,
  `settings.tab.*`, `device.{top,sub}tab.*` — instead of a copy, and the
  98 duplicated label strings are gone. Only the per-row description
  remains editor-owned, because nothing else needs it.
  `TestSurfaceCopyIsComplete` now fails when a surface's *navigation*
  label is missing, so the two cannot drift apart again. Sub-tabs are
  indented in the list rather than prefixed with "·", which is where the
  hierarchy used to live.

## [0.55.1]

### Fixed

- **On a daemon serving several CCUs, embedded mode no longer hides the
  editors Home Assistant cannot replace.** `north.ui.embedded` is
  daemon-wide, but a Home Assistant config entry addresses exactly one
  CCU. Binding one of three CCUs into Home Assistant and turning the
  switch on therefore hid the paramset editor for the other two in the
  only UI that offers one — Home Assistant has no config entry for them,
  so its panel shows nothing, and in the add-on the Ingress identity's
  writes were refused on top. CCU administration was in the same
  position: the two unbound CCUs could not be edited anywhere.

  `settings → ccus` and the device Configure tab (with its sub-tabs) now
  default to **visible** in the embedded profile whenever the daemon
  serves more than one CCU. This moves the default, not the ceiling: an
  operator who wants them hidden still sets it. The count is read live,
  so a CCU adopted while the daemon runs widens the default on the next
  request rather than at the next config save, and the write boundary
  follows it — the re-shown editor works instead of failing on save.

  `GET /api/v1/ui/surfaces` reports `centrals` and marks the affected
  rows `multi_central_visible`; Settings → Navigation & views prints the
  reason under each one (API version 5.8.0).

## [0.55.0]

### Added

- **The Config UI's navigation is configurable, per operating mode.** A new
  admin-only editor under **Settings → Navigation & views** decides which
  views, settings tabs and device-detail tabs this daemon serves. It carries
  two profiles — `standalone` and `embedded` — and a master toggle
  (`north.ui.embedded`, add-on option `ui_embedded`, env
  `OPENCCU_LOOM_UI_EMBEDDED`) that selects which one is live.

  Turn the master toggle on when Home Assistant owns this daemon's config
  surface, i.e. the Homematic(IP) Local integration runs against *this*
  daemon: the UI then hides what HA already provides (login and OIDC, user
  and token administration, CCU credentials, the paramset/link/schedule
  editors, Matter, the device tiles and the aggregated analytics) instead of
  offering a second, competing copy of it. It is deliberately **not** derived
  from running behind HA Ingress — the add-on is also used without the
  integration, and the remote-proxy add-on forwards the same Ingress signal
  while serving the full UI on purpose.

  Each profile stores only its deviations from the shipped default, so views
  added by a later release arrive with the default their own code assigns
  rather than being invisible because they were missing from a frozen
  snapshot. Every row shows the default it deviates from, and reset is
  available per row and per profile.

  A small set of surfaces can never be hidden, enforced on the server rather
  than by a disabled switch: the device list, Settings, this editor, the
  About page, and — in the standalone profile — user and token
  administration, which is the only place to rotate a credential when Loom
  owns identity.

  In the **embedded** profile a hidden surface additionally refuses its
  writes for the Home Assistant Ingress passthrough identity, and showing it
  again hands the write back — so an operator who prefers Loom's paramset
  editor gets a working one instead of an editor that fails on save. That
  scoping applies to the passthrough identity only: an API token, a Loom
  account and MQTT are untouched, reads are never refused, and outside the
  embedded profile the navigation setting is exactly that. Profile changes
  take effect immediately, without a restart, and are audited.

  New REST endpoints `GET`/`PUT /api/v1/ui/surfaces` (API version 5.7.0).
  Design: `notes/concepts/ui-surface-profiles.md`.

### Changed

- **The published documentation is separated from the working documents.**
  Both used to live in `docs/`, told apart only by an exclude list in
  `mkdocs.yml`. That list rotted the way allowlists do: pages were added
  without being listed, and published pages accumulated links into excluded
  trees — links that resolve in the repository and 404 on the site. Anyone
  browsing the documentation could land on a dead link; anyone adding a
  document had to guess which category it fell into.

  The boundary is now the directory. Everything under `docs/` is published,
  and the engineering working set — audits, design concepts, contributor
  procedures, parity fixtures, plans, references — lives under `notes/`,
  which the site never sees. `notes/README.md` states the rule and says
  which tree a new document belongs in.

  The **architecture decision records are now on the site**, under
  *Developer → Architecture Decisions*. They were previously excluded, which
  left fifteen dead links pointing at them from pages that are published,
  and their index — now complete through ADR 0061, having stopped at 0054 —
  linked out to GitHub instead of navigating the site.

  Nine superseded or fully executed documents were removed. Their still-open
  remnants moved into the roadmap first: the two competing pagination
  envelopes, the deliberately reverted path-naming rename, and five deferred
  feature ideas.

  Documentation that had drifted from the code was corrected — the roadmap
  still described the `openccu-data` embed that ADR 0053 replaced, the
  specification pointed at a June scorecard as the "recent" audit, the Matter
  conformance page pinned a spec version the schema snapshot has moved past,
  and the readme pinned a release and API version that had both moved on.

  API version 5.8.0: seventeen OpenAPI descriptions pointed at internal
  documents that are deliberately not published, so a reader of the public
  contract could not follow them. The pointers are gone; the sentences around
  them were self-contained. No endpoint, schema or field changed.

## [0.54.7]

### Fixed

- **The Config-UI change-history page rendered nothing but its loading
  state.** The audit table keyed its rows on a tuple of timestamp,
  action, device address and user. A single operator action can emit
  several entries that agree on all four — creating an alarm area writes
  `area_create`, `sensors_replace` and `outputs_replace` within the same
  second, none of them carrying a device address — so the keyed `{#each}`
  aborted with `each_key_duplicate` and left the view frozen mid-render:
  the header already showed the fetched entry count while the table below
  it still displayed "loading" and `0 / 0`. The request and its response
  were correct throughout. Audit entries now carry the identity they
  always had in the database: `AuditEntry.id` is served on both read
  paths (the durable store's primary key, or the in-memory buffer's own
  sequence when no database is wired) and the table keys on it. This also
  fixes the latent second half of the bug — colliding entries shared one
  expand/collapse state.

## [0.54.6]

### Fixed

- **Virtual-remote buttons pressed in Home Assistant now reach the CCU.**
  The MQTT topic path upper-cases every device address (mirroring the
  reference path rule), and the command subscriber fed that upper-cased
  address straight back into the case-sensitive XML-RPC `setValue` — the
  virtual remote (`HmIP-RCV-1`) is the one mixed-case address in a CCU's
  inventory, so every HA button press on it faulted with
  `-2 Invalid device` while every other device kept working. The MQTT
  command sink now canonicalizes topic-derived addresses against the
  model registry (values, MASTER, custom-DP invokes, schedule switches
  and combined-timer writes alike); unknown addresses pass through
  unchanged. Verified end to end against a live CCU: the press lands,
  the CCU fires the key event, and the button's state topic carries the
  echo.

## [0.54.5]

### Fixed

- **The boot snapshot no longer races the CCU bring-up — and no longer
  floods the MQTT broker with MASTER paramsets.** The boot-time
  `PublishInitialSnapshot` ran on the daemon's main path while the
  readiness-gated bring-up hydrated devices in the background. A device
  that was already hydrated but whose visibility passes had not run yet
  published its **entire** MASTER paramset — retained state + `/config`
  companions + HA-Discovery configs. On HmIP-PSM devices with recent
  firmware that meant ~1,100 parameters each (a 75-slot week-program
  table on channel 8 plus the mesh-router tables on channel 0), and it
  also leaked suppressed VALUES parameters (`BOOTED`, `INSTALL_TEST`,
  `*_STATUS`, `UNREACH`, …). Home Assistant then showed thousands of
  phantom config entities. Four changes close it for good:
  - the snapshot (boot, broker-reconnect reseed, runtime MQTT swap)
    skips every central whose southbound bring-up has not latched
    ready; the `CentralSouthboundReadyEvent` path publishes it after
    the visibility marks are in place,
  - the unobserved-DP branch of the snapshot honours the same
    visibility rule as the observed path instead of publishing raw
    slot + `/config` unconditionally,
  - every MQTT visibility gate now queries the registry with the
    event's real paramset key — MASTER is a default-deny whitelist,
    and asking with `VALUES` (the previous hard-coding) reported every
    MASTER parameter as visible,
  - a new raw-plane orphan sweep (plus the existing HA-Discovery
    orphan sweep, now correctly ordered AFTER the central's snapshot)
    evicts the retained leftovers earlier builds parked on the broker,
    so existing installations come clean on the first restart.

### Changed

- **User and API-token administration exists once, in Settings.** The
  same users and the same tokens were managed by two independent
  implementations: the `#/access` menu entry and the Settings tabs
  "Users" and "Token". They called the same endpoints but each carried
  its own markup, forms, state and error paths — so the same defect
  (raw role names `viewer` / `operator` / `admin` instead of translated
  labels) sat in both and had to be found and fixed twice. Settings
  keeps the surface; the implementation that survives is the one from
  the deleted view, because it was the better of the two: shared
  `LoadingState` / `ErrorState` / `Input` primitives instead of bare
  `<p>` and raw `<input>`, a handled 404 from the user store instead of
  a hard error, a clipboard failure that says so instead of a token
  silently lost, and a role label that falls back to the raw value
  instead of rendering a bare translation key. Two behaviours from the
  Settings copy are folded in: the last-admin delete conflict is
  reported in words, and roles keep their badge colours. The role
  vocabulary now lives in one module, so the next role change is one
  edit. The tabs carry the admin gate the standalone view had.

- **"Hidden parameters" is a Settings tab, not a sibling of Settings.**
  It sat in the "System" navigation cluster next to Settings while
  being, in substance, a setting. It is now the "Hidden parameters" tab
  in the Settings group "Advanced".

- **"Signal quality" is now "Radio & battery" / "Funk & Batterie".** The
  old name described a third of the view: it also reports battery level
  and reachability. The route `#/signal` is unchanged, so existing
  bookmarks keep working, and the view now sets a document title.

- **Settings tabs are linkable.** `#/settings?tab=<id>` opens a tab
  directly, and the active tab is mirrored into the address bar. Both
  retired routes resolve rather than 404: `#/access` rewrites to
  `#/settings?tab=users` and `#/visibility` to
  `#/settings?tab=visibility`, for bookmarks, shared links and a stored
  start page alike.

## [0.54.4]

### Fixed

- **A motion detector on a disarmed system no longer reports "Alarm".**
  Three separate defects made the Security overview claim a break-in
  where a window had merely been tilted. The severity of an active
  `intrusion` class was fixed at `alarm`, so a single detection folded
  the whole domain onto "Alarm" whatever the alarm engine was doing —
  which contradicts the principle the class rename established: the
  class entities report a **detection**, never a verdict, because only
  the engine knows the arm state. The severity is now derived per class
  from the arm state of the zone each active source sits in
  (`internal/security/severity.go`). An armed zone — `armed`, `arming`,
  `pending` or `triggered`, i.e. anything but `disarmed` — still
  escalates to `alarm`; a disarmed one grades `info`; an installation
  without an alarm engine has no arm state at all and therefore stays
  `info`; an arm state that cannot be resolved grades `warning` rather
  than inventing a reassuring "disarmed". Smoke, gas, CO, water and
  panic escalate unconditionally as before, tamper stays `warning`,
  technical and battery stay `info`. **The class stays *active* on a
  disarmed system — that is deliberate and unchanged; only what it
  contributes to the folded severity changed.**

- **The Config UI shows the class names the daemon uses.** The rename
  from nouns to a verb pattern in 0.54.2 reached both north-bound paths
  but not the SPA, which carries its own catalogue under its own keys —
  so the overview kept rendering "Einbruch" ("Intrusion") over the same
  data, the exact wording the rename existed to remove. All nine names
  now match the daemon catalogues word for word in English and German,
  and `TestSPASecurityClassLabelsMatchDaemonCatalogues` fails the build
  on any future drift, per locale. The concept document claimed a
  rename happens "in exactly one place"; it is two, and it now says so.

- **A class tile no longer paints every detection red.** The overview
  coloured any active class with the alarm variant and worded it
  "1 active", so "Battery low" looked like a fire. Tiles now take their
  colour from the severity the daemon delivers, and an active class that
  is not escalating gets neutral wording ("Reporting: 1") instead of the
  alarm phrasing.

### Added

- `SecurityClassState.severity` on `GET /security` and
  `GET /security/classes/{class}`, plus a `severity` attribute on the
  MQTT `security/class/<class>` topic. An automation can branch on the
  graded verdict instead of re-deriving the arm state the daemon has
  already resolved. REST API version `5.5.0`.

## [0.54.3]

### Added

- **Every program run the daemon triggers is now visible in the daemon
  log, with the surface that asked.** The audit database has recorded
  daemon-triggered executions since 0.52.x, but nothing reached the log
  operators actually read during an incident, and the record said only
  `trigger=api` for every route. Each ingress now stamps its operation
  (`mqtt:program-trigger`, `rest:program-execute`, `ws:program-execute`,
  `mcp:program-trigger`, `service:program-trigger`) into the request
  context; the execute event carries it as `Source`, the audit note
  gains a `source=` tag, and an INFO line (`program.execute` with
  central, program, source, success) lands in the daemon log. The #497
  investigation would have taken minutes instead of days with this line
  present.

- **Comment claims and ratchet rot now fail the build.** Three guards
  institutionalize the audit that found the defects above. A
  declared-consumerless event whose catalogue doc still claims
  consumers fails `tests/contract` (the two truths contradicted each
  other for three events). A ratchet justification that defers — "no
  surface consumes it yet" — instead of deciding fails the build, which
  the ratchet headers demanded in prose but nothing enforced. And the
  WS-delivery-without-MQTT table pins that the optional MQTT plane
  never gates a WebSocket emission.

- **The migration `Down` path is now documented as unsupported, not
  silently risky.** An audit of every `goose` migration found that most
  `-- +goose Down` blocks drop the table or column their `Up` added,
  destroying data with no other copy — bcrypt password/token hashes,
  Matter NOC private keys, the append-only alarm journal, argon2id PIN
  hashes, and the frozen zone `slug` that Home Assistant entity ids and
  MQTT topics depend on. Nothing in this project ever exposed `goose
  down` to an operator, so the risk was latent, not exploited. Every
  destructive `Down` block now carries a factual note above the marker
  naming what is lost; `TestMigrationDownDropsHaveLossNotes`
  (`tests/contract/`) enforces the note on every future migration; and
  [ADR 0061](./docs/adr/0061-migration-down-path-unsupported.md) records
  the decision that `goose down` is a development/test tool only, never
  an operator rollback path.


### Fixed

- **A keypad or lock access code was stored in cleartext in the audit
  log.** Every paramset write persisted its raw before/after values, and
  `CODE_ID` carries the access code of a keypad or lock channel — so
  setting one put the code itself into the append-only log, readable for
  the full 90-day retention by anything that reads an audit row. The
  sibling data-point write path had recorded parameter names only, for
  exactly this reason, since it was written. Credential-bearing
  parameters now record the name and withhold the value; ordinary
  settings keep theirs, because "the heating curve went from 21 to 24"
  is what makes an audit row useful. A first write still reads as "had
  no value before" rather than as a withheld one.

- **A panic while reading an event's type would have stopped every event
  in the daemon, silently.** The bus resolved an event's identity
  without recovery, and its dispatch lock is released by the queue drain
  rather than by a deferred unlock — so the panic unwound past the
  release and left the lock held for the life of the process. The daemon
  kept running, every later publish queued into a backlog nothing would
  drain, and every unsubscribe blocked forever. No event can trigger
  this today (they return constants), but they are methods on ordinary
  structs, one nil dereference away, and the failure is total and
  produces no error. The identity is now read before any lock is taken
  and under recovery.

- **One slow broker could freeze every CCU's event delivery.** The
  internal event bus dispatches synchronously on the publishing
  goroutine, so anything a handler does happens on a goroutine shared by
  every central. The live value plane had been moved off it, but the hub
  plane had not: connectivity changes, install-mode countdowns, sysvar
  and program updates, alarm and service messages, the inbox, the
  firmware-update entity and the device-link re-announcement all
  published to the broker inline — as did the whole-device and
  whole-central snapshots that a hot-plugged device or a completed CCU
  bring-up triggers. A broker that stopped acknowledging (a half-open
  connection waits out the transport's ack timeout) therefore stalled
  dispatch for every central at once, and a removed device's retraction
  could stall it too. All of these now hand their broker work to a
  single fan-out worker per publisher, which keeps them serialised — so
  discovery still precedes state and per-entity ordering is unchanged —
  while the dispatch goroutine returns immediately. Shutdown cancels the
  worker's in-flight publish instead of waiting it out.

- **A flood of value changes could silently delete entities from Home
  Assistant.** The fan-out queue drops its oldest entry when it
  overflows, which is right for a state sample — the next one overwrites
  the same retained topic — and wrong for anything declarative. Now that
  snapshots and hub-plane payloads share that queue, discovery configs,
  device snapshots and aggregate replacements are marked durable and are
  never dropped; the queue grows past its soft bound instead, and only
  self-healing state publishes are evicted. Losing one of the former
  leaves an entity missing or frozen until the operator restarts the
  daemon, because nothing re-sends it.

- **Every energy day and month total was shifted by the timezone
  offset.** Day and month buckets were folded on the UTC calendar while
  the SPA labelled each one with a local date. In CEST a "day" therefore
  ran from 02:00 to 02:00 local time under the label of the day before,
  so a consumption at 00:30 on the 6th was counted — and shown — against
  the 5th, and both edges of every day and month were off by the
  offset's slice. Day and month buckets are now local calendar buckets
  in the daemon's own timezone, folded with calendar arithmetic so a
  23-hour and a 25-hour daylight-saving day each stay exactly one
  bucket. The rollup, the un-rolled tail and the history-chart tiers all
  derive the boundary the same way, so a bucket never moves when a
  rollup runs. Hour buckets are unchanged. Existing daily rows cannot be
  re-cut into local days, so a migration empties the daily tier and
  rewinds its watermark; the next rollup rebuilds it from the untouched
  hourly tier. Operators running the daemon in a container should make
  sure it carries the household's timezone (`TZ` / `/etc/localtime`).
  API 5.3.0 documents the bucket boundary on `GET /api/v1/energy`.

- **Every CCU program ran by itself — twice on every daemon start, once
  more whenever a program was created or edited.** With the MQTT raw
  plane enabled, the bridge mirrored each program's active flag onto the
  program's `…/hub/programs/<id>/trigger` topic. That topic is a
  *command* topic: the daemon itself subscribes to it and answers every
  live message with `Program.execute` on the CCU. A broker routes a
  client's own publishes back to its established subscriptions (the
  daemon does not use the MQTT 5.0 No-Local option), so each state
  publish came back as a command and stumped the program's first "then"
  branch — deactivated programs included, conditions never evaluated,
  exactly like the WebUI's manual-execute button. Two publish rounds per
  boot (initial hub load plus the CCU-ready re-publish) produced the
  double execution; the hub scan discovering a WebUI edit's temporary
  copy produced the ghost-copy executions. Reported with a
  screencast-grade repro in discussion #497; reproduced and verified
  fixed against a live CCU. The bridge now publishes program state only
  to `…/state`; two new guards pin the invariant that no state-plane
  topic may match any of the daemon's own command subscriptions.

- **Retained state mirrors parked on the trigger topics are now evicted,
  and an empty trigger payload is no longer a command.** Brokers that
  ever saw a pre-0.54.3 daemon still hold retained `true`/`false`
  payloads on every program's trigger topic. The boot-time retain
  cleanup now clears them; the command handler additionally ignores
  empty payloads (the shape of a retained-topic eviction), so the
  cleanup can never re-trigger what it cleans. Triggering a program via
  MQTT keeps working with any non-empty payload (Home Assistant's
  discovery button publishes `true`).

- **The boot-time MQTT retain-cleanup passes never ran in production.**
  Both `RunRetainCleanupOnce` and the HA-Discovery orphan sweep need a
  subscribe-capable broker client to snapshot the retained store, but
  the bridge only held the publish-only circuit-breaker decorator — the
  capability check failed on every boot with a WARN line and nothing was
  ever cleaned. The composition root now wires the raw client into the
  bridge for the cleanup passes; a composition-root test pins it.

- **The retain cleanup treated hub state as legacy device state.** The
  legacy-topic matchers saw `…/hub/programs/<id>/state` (and a
  numeric-named `…/hub/sysvars/<n>/state`) as a retired
  `<iface>/<addr>/<channel>/state` shape and would have evicted live,
  retained hub state on every boot. The reserved `hub` subtree is now
  excluded. Latent until this release because of the wiring defect
  above.

- **Without MQTT configured, the SPA never heard that a value's
  freshness flipped.** A wire data point moving between cache / live /
  stale re-publishes its current value with a `refresh` envelope so
  consumers can tell a confirmed live reading from a restored one. The
  handler behind it returned early when no MQTT wiring was present —
  before the WebSocket dispatch, although the dispatch path gates only
  its MQTT arm on the wiring being there. In an MQTT-less deployment
  every freshness signal was silently dropped for the SPA and every
  other WS consumer. The guard moved to where it belongs, and a new
  delivery table drives every WS-emitting bridge subscription against a
  bridge constructed without MQTT so the next optional plane cannot
  gate a WebSocket emission either.

- **Recovery progress was invisible while it happened.** The
  connection-recovery pipeline published a per-stage and a per-attempt
  event that nothing consumed — an operator tapping the diagnostics
  event stream saw a recovery start and finish with a silent gap in
  between, and the events' own documentation claimed consumers that
  never existed. Both now stream through the diagnostics event-bus tap
  (`GET /diagnostics/eventbus/tap`) as `RecoveryStageChanged` and
  `RecoveryAttempted`, carrying the stage, the attempt count and the
  last error.

- **A CCU added while the daemon was running never cached its device
  descriptions.** The persistent device-description and paramset caches
  were attached at the boot call site only, so a central adopted at
  runtime ran without them: nothing it learned reached SQLite, and every
  later daemon start re-inventoried the whole CCU over the radio as if
  it had never been seen. The wiring moved into the bring-up path both
  entry points share, so an adopted central hydrates from disk and
  mirrors back exactly like a configured one. Pinned through the real
  manager by asserting the effect on both sides — hydration and
  persistence — rather than the call.

- **Switching the measurement history off froze `history.db` at its
  full size.** Retention hung off the recorder, the recorder off the
  store, and the store off the enabled flag — so an operator disabling
  history to reclaim disk got the one outcome the switch was meant to
  prevent: nothing was recorded, and nothing was ever evicted either.
  The rollup and retention pass now runs without a recorder whenever a
  history database exists, so the file drains to the configured
  retention and keeps draining as the cutoff moves. Recording stays off,
  the `/history` REST surface and the `history` capability stay hidden,
  and no database is created for a feature that is disabled.

- **A chart of a freshly learned device collapsed to a single point.**
  A history query whose window reaches back before the series' oldest
  raw sample was promoted to the hourly tier — right for a series whose
  older rows the recorder has already purged, wrong for one that simply
  did not exist yet. A device learned ten minutes ago, charted over the
  last half hour in 30-second buckets, had its raw tail folded onto hour
  buckets and drew one point. The tier choice now weighs both tiers at
  the *requested* bucket width, so a narrow-bucket window keeps its raw
  resolution unless the hourly rollup genuinely reaches further back
  than the raw table does.

- **The daily history tier's retention purge scanned the whole table.**
  `measurements_daily` is keyed by central name first, so
  `DELETE … WHERE bucket_ts < ?` could not use the primary key and
  full-scanned on every retention tick — a scan that grows with every
  day retained. The raw and hourly tiers have had a time-axis index
  since the bounded rollup landed; the daily tier now has one too.


- **A backup error claimed a missing feature when the daemon meant a
  missing configuration.** Backup triggers with no central registered,
  and downloads with no backup storage configured, both answered
  "adapter: not implemented in MVP". The errors now name what is
  actually absent — "backup: no central registered" respectively
  "backup: no storage configured" — so an operator debugs the
  configuration instead of hunting a feature gap that is not there.

- **The base temperature of a climate week profile could flip between
  daemon runs.** When two temperatures occupied the same total minutes
  in a weekday schedule, the winner came out of Go's randomized map
  iteration — the same profile could show 18 °C today and 22 °C after a
  restart. Ties now deterministically favour the temperature whose
  period starts earliest, matching the reference implementation's
  accumulation-order semantics, and the previously ineffective sort is
  now load-bearing and pinned by a repeat-run test.

- **A dozen comments described consumers, stubs and wirings that do not
  exist.** An audit of comment claims against the code found: the event
  catalogue asserting MQTT subscribers, audit loggers and refresh
  indicators for events nothing consumes; an MQTT install-command topic
  documented as "subscribed" that no command wildcard matches; WS
  device triggers claimed to reach Home Assistant when they reach WS
  clients only; a WS command file header still listing five "stubs"
  that have long been wired to real handlers; a restore path documented
  as "not wired yet" that has been wired per central for releases; and
  a validation-reader parameter on the configuration coordinator's
  paramset write that no code path ever read. Every claim now states
  what the code does, and the dead parameter is gone.

- **Turning off the Home Assistant Ingress auth passthrough did not turn
  it off.** The `north.rest` config row was written from the whole REST
  struct, which contains the three nested auth sections
  (`north.rest.auth.oidc`, `.ccu`, `.ha_ingress`) — so every value was
  stored twice, and the copy in `north.rest` was applied first at boot.
  Resetting `north.rest.auth.ha_ingress.enabled`, or deleting the whole
  section, removed it only from the section's own row; the next start read
  the passthrough back out of `north.rest` and re-enabled it, while the
  Config UI attributed the field to "default". The passthrough is a
  deliberate auth bypass, so a reset that does not stick is a security
  defect. A section row now never carries a nested section's sub-tree —
  on save, on load, and when the editor reads a row written earlier.

- **Saving one field of a config section cleared the rest of it.** A
  section save replaces the stored row, but the daemon validated the
  request merged onto the running config while persisting only the
  request itself. A `PUT` of `{"enabled": true}` on `north.mqtt`
  therefore passed validation against a config that still had a broker
  URL and left behind a row describing an enabled MQTT bridge with no
  broker. The row now stores the same configuration that was validated,
  so it describes the whole section.

- **Changes that need a restart reported that they did not.** The
  restart-required marker in the Config UI and the pending-restart banner
  were driven by two hand-maintained lists that disagreed: the whole
  `alarm` section and the Basic/Bearer auth switches were marked in the
  editor but never compared, so saving one answered "no restart needed"
  and the banner stayed silent while the change sat inert until the next
  start. Both are now derived from one table, and the Matter identity and
  commissioning parameters (`vendor_id`, `product_id`, `discriminator`,
  `commissioning.*`) — which are baked into the bridge at start-up — were
  added to it.

- **`hmcli`'s interactive password prompt echoed the password to the
  terminal**, where it stayed readable in the scrollback for as long as
  the session's history was kept. The prompt now suppresses echo via
  `golang.org/x/term` (already resolved transitively through
  `golang.org/x/crypto`/`golang.org/x/net`, so this only promotes an
  existing dependency to direct rather than adding a new one) when reading
  from a real terminal; piped or redirected input is unaffected.

- **`hmcli --insecure` disabled TLS certificate verification silently.**
  The existing plaintext-credential warning only fires for `http://`; an
  `https://` connection with verification turned off is exposed to the
  same interception risk (any certificate is accepted) but said nothing.
  `--insecure` now prints an explicit stderr warning everywhere it can be
  set (`devices`/`sysvar`/`program`/`paramset`/`alarm`, `export-def`,
  `cache clear`, `events tail`).

- **A password embedded in `hmcli --host` (`https://user:pass@ccu/`)
  leaked into every error message the command printed** — the client
  built its target URL, and therefore every wrapped request/response
  error, straight from the raw `--host` value. Go's HTTP client never
  actually authenticates with a destination URL's userinfo (only a proxy
  URL's is used), so the credential did nothing but sit there waiting to
  be printed. The userinfo is now stripped before the base URL is stored.

- **A channel action's result (success or failure) rendered in the
  header banner instead of a toast**, contrary to the SPA's own
  operating concept — and unlike every other action in the same panel,
  a failure there had no distinguishable error styling. `ChannelPanel`'s
  action buttons now report through `toastStore`, like save, import and
  the profile-take-over flow next to it.

- **The channel-config profile picker kept showing the previous
  channel's manually selected profile after switching channels.**
  `ChannelPanel` reuses the same `ProfileSelector` instance across a
  channel switch (it updates props rather than remounting), so once a
  user picked a profile from the dropdown, the "don't override my pick"
  guard latched permanently and never re-synced to the new channel's
  detected profile. The selector is now keyed on the channel (and peer,
  for LINK), so a genuine channel switch gets a fresh instance while an
  in-place reload (e.g. after Save) still preserves an in-progress pick.

- **The sidebar offered a "Backups" entry to every operator, including
  ones the server rejects.** `GET/POST /api/v1/backups*` has been
  admin-gated for a while; the navigation entry was not. It is now gated
  the same way as the other admin-only entries (Logs, Access).

- **Three i18n / locale gaps in the SPA:** the Energy view's four
  time-range preset buttons (24h/7d/30d/12mo) were a plain `const`
  evaluated once at component init, so they did not follow a runtime
  locale switch like the rest of the toolbar; the energy table's kWh/W
  columns used `toFixed()` (always a `.` separator) next to the
  locale-aware cost column, producing mixed decimal separators in the
  same row for German operators; and the access-control page's
  viewer/operator/admin role labels (dropdown options and the three role
  badges) rendered the raw English role token untranslated in both
  locales instead of a localized label.

- **A CCU that rebooted again while an interface was still activating
  could end up registered twice.** The one-time boot readiness gate
  (`gatedCentralBringUp`) waits for `checkrega.cgi` before southbound
  bring-up starts, but the per-interface ingest retry that follows
  (`wireInterface`'s `activate()`, up to six attempts over roughly 33
  seconds to absorb residual per-interface RPC lag) never re-checked it —
  so a CCU that dropped again inside that window could hit `Deinit`/`Init`
  mid-boot, the same "deinit fails while init succeeds" race already
  guarded against on the reconnect path. Every `activate()` attempt now
  re-probes CCU readiness with a short, bounded timeout immediately before
  touching `Deinit`/`Init`, reusing the existing retry/backoff on a miss
  instead of registering against a CCU that is not actually ready yet.

- **A forced value reload after a failed live-event coercion silently gave
  up on BidCos-RF and VirtualDevices data points.** When the daemon
  receives a push event it cannot coerce inline, it schedules a direct
  `GetParamset` reload to resolve the type mismatch from the canonical
  wire shape. That reload shared its skip with the unrelated
  cost-saving guard that avoids speculative `VALUES` probes on
  interfaces that can only return a placeholder (BidCos-RF's passive
  devices, VirtualDevices' aggregated channels) — so the reload wrote a
  sentinel instead of fetching the value the CCU had just sent, leaving
  the data point permanently unobserved. The forced path is now exempt
  from that skip: it only ever fires right after a live push, when the
  CCU already has a fresh value to serve.

- **A damaged backup archive was uploaded to the CCU without being
  looked at.** An uploaded archive was inspected at import, but a
  restore read the stored file and pushed it straight at
  `cp_security.cgi`, which starts the restore and reboots the CCU. An
  archive that a proxy had truncated, or that was never a system backup
  at all, therefore took the CCU down with it. Every restore now runs
  the same structural inspection first and refuses with `422` and the
  reason (not a readable tar / missing a required member) before a
  single byte reaches the CCU. API 5.4.0 documents the widened `422` on
  `POST /api/v1/backups/{id}/restore`.

- **A failed backup download saved as an ordinary `.sbk` file.** The
  download response committed its `200 OK` and its attachment headers
  before the archive was read, so any failure afterwards appended a
  problem document to a half-written file and the browser stored the
  result as a normal backup — one a later restore would push at a CCU.
  The status is now committed by the first payload byte: a failure
  before it arrives is answered as an error, and a failure part-way
  through aborts the transfer so the client reports a broken download
  instead of writing out a short file.

- **Two backups of the same CCU in the same second became one.** Backup
  ids carry a one-second timestamp and the storage overwrites an
  existing id, so a manual backup that landed in the same second as the
  scheduled one silently replaced it — and the rotation then pruned
  against a set one archive shorter than it appeared. Ids are now minted
  strictly increasing per CCU; the id format is unchanged.

- **The Config UI reported settings that only exist in `config.yaml` as
  unset — and asked for a restart that no action could clear.** The
  effective configuration behind `GET /api/v1/config` was assembled from
  the built-in defaults instead of the file the daemon booted from, so
  every value set in YAML and never edited in the UI came back as its
  default. Because the pending-restart banner compares the running
  configuration against that assembly, a YAML-only `backup.schedule` —
  restart-required, and carried by no editable section — kept the banner
  lit permanently. Both assemblies now start from the same base, and a
  value that came from the file is labelled as such instead of as a
  default.

- **A configuration section was saved even when it could not be
  validated.** If the effective configuration was momentarily
  unavailable, the section `PUT` skipped the masked-secret restore, the
  semantic validation and the restart-required answer, then persisted
  the request body anyway and reported success — so a form the UI had
  re-sent with its `***` placeholders could overwrite a real credential
  silently. The save is now refused with `503` and the stored section is
  left untouched.

- **`callback.port_range` was accepted and then ignored.** The range was
  only consulted when `callback.port` was `0`, but that field is filled
  with `8120` on every load, so no installation could reach it: an
  operator behind a firewall that only opens `30000-30099` got port 8120
  and no indication the setting had been dropped. A configured range now
  takes precedence over `callback.port`, and a malformed range is
  rejected at the save instead of at the next boot.

- **Configuration values outside their documented range were accepted
  and then quietly replaced.** `locale`, `north.webhook.url` and
  `timeout_ms`, `north.mcp.path`, `north.matter.listen` and
  `discriminator`, `north.matter.commissioning.iterations`, the REST
  rate-limit and WebSocket replay sizes, and every duration setting are
  now validated when they are saved, with a message naming the field and
  the accepted range. Previously each of them fell back to a default at
  the point of use, so the UI answered "saved" for a value the daemon
  never used.

- **Switching the per-CCU connection check off had no effect.** A
  negative `centrals[].check_connection_interval` is the documented way
  to disable the poll, but the daemon only copied the value when it was
  positive, so the job kept running at its default cadence.

## [0.54.2]

### Fixed

- **An intrusion alarm was reported while the alarm system was
  disarmed.** The Security & Safety domain rendered a `triggered` report
  for every hazard-class source going active — and the intrusion class is
  fed by every enrolled door, window and motion sensor, which report all
  day on a disarmed system. An operator saw:

  > In Zone  wurde um 15:43 Uhr ein Einbruchalarm ausgelöst (Modus ):
  > Bewegungsmelder WZ, Bewegungsmelder HAR, Bewegungsmelder FL.

  Both halves of the defect are visible in that sentence: it claims a
  break-in that did not happen, and the zone and mode placeholders are
  empty because the source path carries neither.

  Whether an active motion sensor means a break-in is the alarm engine's
  verdict — it is the only party that knows the arm state — and the engine
  already reports it, with the zone and mode filled in, on both the
  triggered and the cleared side. The source path no longer reports the
  intrusion class at all.

  The class entity keeps flipping: it reports a detection, is named for
  one ("Öffnung oder Bewegung erkannt"), and answers the "is anything
  open?" question the arming flow needs. Panic is deliberately not
  excluded — a hold-up trigger must alert precisely when nothing is armed.

- **An alarm zone reported no arm state at all until it first changed
  one.** The Security & Safety domain seeds a zone from the zone store and
  from the panel projection, and both paths set identity only — id, slug,
  name. The state writers fire on a trigger or a transition. A daemon
  restarted next to a quiet alarm system therefore held every zone with an
  empty state, and the Config UI rendered the bare translation key
  `alarm.state.` where the arm state belongs. The zone now adopts the
  engine's state from the panel projection; an unrecognised token leaves
  the previous state standing rather than inventing one, because on a
  security surface a made-up "disarmed" is worse than an admitted gap. The
  overview additionally says "unknown" instead of interpolating an empty
  value into a key.

### Changed

- **The Security & Safety class entities say what they observed, not what
  it means.** They report a detection; the verdict — "someone is breaking
  in" — belongs to the alarm control panel, which is the only surface that
  knows the arm state. `intrusion` was the sharp case: named "Einbruch" it
  claimed a burglary, while it actually stands ON as soon as an enrolled
  door, window or motion sensor reports — a tilted window on a disarmed
  system is enough. It is now "Öffnung oder Bewegung erkannt" / "Opening or
  motion detected", and the other classes follow the same verb pattern
  (`Rauch erkannt`, `Batterie schwach`, `Panikruf ausgelöst`, …).

  Names only: no behaviour changed, and no entity id, topic or unique_id
  moved. They live in `internal/i18n/catalogs/{de,en}.json` and therefore
  reach the MQTT discovery plane and the REST entity-name catalogue in one
  step. `notes/concepts/security-safety-concept.md` §4.2.1 documents what each class
  asserts, §4.2.2 the `sources` attribute that names the detector behind
  it.

## [0.54.1]

### Fixed

- **A disabled MQTT broker could take the daemon down, and did silence the
  whole hub plane.** `Wiring.Bridge()` returns nil by design: switching
  `north.mqtt.enabled` off at runtime keeps the Wiring alive and points its
  bridge nowhere, so an in-flight publish becomes a no-op. Every method on
  Wiring itself checks for that. Four call sites that reach *through* Wiring
  for the bridge — to get at the discovery builder, the per-central HubInfo
  stamp, the per-data-point discovery on the value-change path, and the
  system-status publish — did not.

  The failure was asymmetric, which is why it went unnoticed. The
  hub-discovery re-Start runs under panic isolation, so it logged
  `mqtt.hub_discovery.restart_on_ready.panic` and carried on — while the
  entire hub plane (sysvars, programs, the named central device,
  alarm/service messages, connectivity, install mode) silently never
  published, because the pass that wires it died before publishing
  anything. The other three have no recover at all: the HubInfo stamp runs
  during per-central bring-up and the other two from event-bus handlers, so
  each would have crashed the daemon outright.

  All four now check, and a contract test fails the build on any new
  reach-through that dereferences the bridge without one.

- **CUxD delivered every event twice after an upgrade, and the second copy
  was rejected forever.** The `loom-` interface-id rename in 0.54.0 assumed
  the old registration would be cleared on the next start, which holds for
  the CCU's own components: they key deregistration on the callback URL.
  CUxD does not — a registration written by a pre-0.54.0 release survives
  the upgrade, so CUxD announces each event to both ids. The current one is
  handled; the orphan had no route and produced a
  `binrpc callback: multicall sub-call failed` warning per event, for the
  life of the daemon. An operator log showed 26 of them in two minutes.

  The BIN-RPC callback server now also answers under the pre-prefix id, so
  the orphan's copy becomes a duplicate the ingest pipeline collapses
  instead of a rejected callback. No events were lost — both copies carry
  the same payload — but the log noise was permanent and read like a
  transport fault.

## [0.54.0]

### Added

- **The daemon's own entity names are now readable by any consumer —
  `GET /i18n/entities`.** The daemon has been the single naming authority
  since 0.45.0, and it already names its hub singletons and Security &
  Safety surfaces in both locales — but only on the MQTT discovery plane,
  where the names were resolved at publish time and never left. Every
  other consumer kept a second copy of the same words, and the copies
  drift the moment either side is edited alone: "Alarmmeldungen" lived in
  this catalogue *and* in the Home Assistant integration's `strings.json`,
  with nothing comparing them.

  The endpoint serves the naming projection — the `discovery.*` and
  `security.entity.*` namespaces — resolved for a requested `locale`,
  falling back per key to the daemon's default and echoing the locale
  that actually answered so a consumer can tell a translation from a
  fallback. Values stay templates: `Connectivity {iface}` carries a
  placeholder only the caller can fill, because the daemon does not know
  which interface is being named. The Config UI's own strings
  (`nav.`, `login.`, `setup.`) are deliberately outside the projection —
  they are not a naming contract. `APIVersion` bumped to `5.2.0`.


- **The Security & Safety domain now pushes.** Its five events — the
  folded severity, a hazard or fault class going active, a zone's
  security view, a fault opening or clearing, and a rendered report —
  reached MQTT, the webhook plane and the metrics collector since the
  domain shipped, but no WebSocket consumer received any of them. Every
  REST/WebSocket client therefore had to re-read `GET /security` on its
  own schedule to learn that a smoke detector had fired, and the Config
  UI's Security views showed whatever the state had been when the page
  was opened: an `ok` badge could sit on the screen right through a
  running alarm.

  Five broadcasts close that: `security.state_changed`,
  `security.class_changed` and `security.zone_changed` on topic
  `security.state`, `security.fault_changed` on `security.faults`, and
  `security.notification` on `security.notifications`. Three topics
  rather than one flat family, so a messenger integration can take the
  prose reports alone while a dashboard takes the state and never sees a
  fault it does not render; `security.*` subscribes to all of them. The
  broadcast names are the domain's own event tags — one vocabulary on
  both sides, unlike the alarm family, where the internal tags read
  `alarm_panel.` and the wire reads `alarm.`.

  The Config UI's Security overview and fault ledger consume them and
  re-read on the push, so both surfaces now follow the installation
  instead of the page load. `APIVersion` bumped to `5.1.0`.

  **A covert report stays off this plane.** A duress code or a silent
  panic trigger reaches `security.notification` only under
  `alarm.duress_visibility: full`. The WebSocket feeds the SPA and every
  dashboard a browser has open — a hallway tablet showing "duress code
  entered" while the attacker stands next to you defeats the trigger the
  feature exists for. The domain already folds that policy into the
  report's retainability, and this plane honours the flag rather than
  re-deriving the rule; the webhook and the raw MQTT event topic are
  unaffected and keep delivering under every level.

## [0.53.1]

### Changed

- **BREAKING: alarm messages dropped the device fields they never
  legitimately had, and gained real timestamps.** An alarm entry is backed
  by an alarm system variable that a program raises, not by a device — the
  CCU reports its trigger data point as the 65535 "unknown" sentinel — so
  `device_name`, `address`, `state_value`, `last_trigger` and `rooms` on
  `AlarmMessage` (REST `GET /alarm-messages`, the `alarm_messages.list` WS
  command, and the MCP `list_alarm_messages` tool) never carried real data
  and are removed. In their place, `timestamp` and `last_timestamp` now
  carry the CCU's actual Unix-second occurrence data (previously present in
  the `get_alarm_messages.fn` ReGa script output but never read); both are
  omitted from the response instead of the CCU's `0` becoming the
  1970 epoch. The Config UI's alarm list drops its device column and shows
  the new "last changed" timestamp instead. `APIVersion` bumped to `4.0.0`.
- **BREAKING: service messages gained real timestamps, real room/function
  arrays, and dropped `description` / `priority`.** `timestamp` and the
  new `last_timestamp` on `ServiceMessage` (REST `GET /service-messages`,
  the `service_messages.list` WS command, and the MCP
  `list_service_messages` tool) are now the CCU's Unix-second occurrence
  data instead of a local-time string with no zone offset; both are
  omitted from the response instead of the CCU's `0` becoming the 1970
  epoch. `rooms` and `functions` — present in the model since `0.1.0` but
  never populated by the loader — now reach every surface as proper
  string arrays. `description` and `priority` are removed: the
  `get_service_messages.fn` ReGa script never emitted either, so both
  were always empty. The Config UI's service-message list shows rooms,
  functions and a "last changed" column, and its "quittable only" filter
  — previously a no-op because `quittable` was never populated — now
  actually filters.

  `APIVersion` is `5.0.0` — a major bump, because `rooms` and `functions`
  change from string to array, `description` and `priority` disappear, and
  `timestamp` leaves the `required` set. Together with the alarm-message
  change above, this release moves the REST contract from `3.20.0` to
  `5.0.0` in two breaking steps.

### Fixed

- **"Which programs use this system variable?" returned nothing on CCUs
  whose usage index is empty.** `usage_by_sysvar.fn` asked the variable
  object's own `DPEnumUsagePrograms()`. Measured against two CCUs running
  the same firmware: one reported every reference, the other reported none
  of 29 references — 21 in program activities, 8 in trigger conditions —
  that a walk over the program rules finds. The index reports an empty list
  rather than an error, so REST `GET /sysvars/{name}/usage`, the WS command
  and the SPA's delete-confirmation warning were silently blank on such an
  installation. The script now resolves the references out of each
  program's root rule instead. References inside else-if sub-rules or
  inside script-type activities remain invisible — the root rule is the
  only one reachable, and a script body is opaque; both limits are stated
  in the script header and in the OpenAPI description. `APIVersion` bumped
  to `5.0.1`.

- **A service-message channel with two or more functions (Gewerke) made
  the whole service-messages list unparsable.** `get_service_messages.fn`
  joined `rooms` and `functions` with a raw tab character inside a JSON
  string — a control character that is illegal there — so
  `json.Unmarshal` failed with `invalid character '\t' in string
  literal` and the daemon received zero service messages instead of the
  CCU's actual count. `rooms` and `functions` are now real JSON arrays.

### Changed

- **The interface identifiers the daemon registers with a CCU now read
  `loom-<central>-<interface>`.** They previously carried the daemon's
  instance name and the central's name separately — both derived from the
  host, so running as the CCU's own add-on repeated the same name twice
  (`RM-Test-VM-96-RM-Test-VM-96-BidCos-RF`) while nothing in the string
  said the registration belonged to Loom. The CCU prints this identifier
  in its own logs, where that attribution is the whole point. The instance
  name is kept whenever it differs from the central name, so two daemons
  against one CCU stay distinct. See ADR 0060.

  Internal identifiers are unaffected: MQTT topics, REST/WebSocket
  payloads and the values cache continue to use `<central>-<interface>`.

  A registration left behind by an earlier version is cleared on the next
  start — see the deregistration fix below.

### Fixed

- **Every HTTP client the daemon makes now owns its connection pool.**
  Sixteen call sites left `http.Client.Transport` nil, which falls back to
  the process-wide default transport — so the CCU readiness probe, SSDP
  discovery, firmware downloads, the JSON-RPC client, webhook delivery and
  the metric exporters all shared one pool, and whatever closed idle
  connections on it could tear down a request another had in flight.

  Two of them additionally built a transport from scratch when a custom
  TLS configuration was supplied, which silently dropped proxy handling,
  the dial and TLS timeouts and HTTP/2; those now clone the defaults and
  adjust only the TLS field. An operator behind an HTTP proxy gets proxy
  support back on the `hmcli` export and cache paths and on device-icon
  fetches.

  The insight was already in the tree — `hmcli`'s daemon client had cloned
  its transport for exactly this reason, naming the flaky parallel test
  that exposed it — but nothing carried it to the other callers. A
  contract test now fails the build on any `http.Client` literal without
  an explicit transport.

- **No CUxD device has ever pushed an event.** CUxD does not call `event`
  directly the way the other interfaces do — it wraps every callback,
  a single value change included, in a `system.multicall` envelope. The
  BIN-RPC callback listener had no case for that method: it read the first
  argument of each call as the interface id, which is a string for a bare
  call and an array for an envelope, so every CUxD push was rejected as
  malformed and discarded. The daemon still read CUxD values, because
  reading is a poll the daemon initiates, so a CUxD device showed a value
  that only ever refreshed on a restart or a re-seed.

  The listener now unwraps the envelope and dispatches each sub-call, and
  reports one result per sub-call so a single unroutable member cannot
  discard a whole batch.

  Two things kept this invisible for as long as it existed. The rejection
  was logged below the default level, so a CUxD that pushed constantly and
  one that pushed nothing produced identical logs; those rejections are now
  logged as warnings. And the interface was then declared dead for the right
  reason by the wrong route: with no inbound callback ever arriving, the
  keepalive verdict turned negative after three minutes and reported
  `connection lost: timeout`, which read as a network problem rather than a
  parser one.

- **A CUxD interface reported `connection lost: timeout` on a healthy
  connection, and could not recover from it.** Two defects behind the same
  symptom. The keepalive skipped CUxD on the assumption that it cannot
  answer `ping` — it can, over BIN-RPC, and that PONG is exactly what keeps
  an interface's liveness fresh while its devices are quiet — so the
  liveness verdict was guaranteed to go negative and stay there. And no
  recovery pipeline was ever registered for CUxD, so the connection-loss
  event that followed was discarded by the recovery coordinator: CUxD was
  the one interface that could never reconnect itself. Each false loss also
  marked every CUxD data point stale on REST and MQTT.

- **Deregistering a callback registered an unreachable one instead.** The
  daemon sent `init("", interface_id)` where the CCU expects `init(url)`
  with the second parameter omitted — the CCU keys deregistration on the URL
  alone. The old registration therefore survived every deinit, and the empty
  URL was accepted as a *new* registration that the CCU then tried to
  deliver to on every keepalive, logging
  `XmlRpcClient error calling event(...) on uds://:/RPC2` in the CCU's own
  log until it was restarted. Affected XML-RPC and BIN-RPC alike.

  This also removes the upgrade caveat noted under *Changed* above: because
  the pre-init deinit now names the callback URL, which does not change
  across an upgrade, a registration left by an earlier version is cleared on
  the next start without restarting the CCU.

- **A device fault reported by a CCU reached nothing at all.** A device-error
  event (`ERROR_CODE`, `ERROR_OVERHEAT`, `SENSOR_ERROR…`) produced no
  WebSocket `device.*.trigger` broadcast, no diagnostics entry and no record
  on the channel's event group — the same for an impulse (`SEQUENCE_OK`). A
  smoke detector reporting a fault was, to every north-bound consumer,
  indistinguishable from one reporting nothing.

  Both families deliberately have no data point: they are events, not state,
  so the device pipeline creates none for them. The callback path then
  dropped any event whose parameter had no data point, which is exactly and
  only those two. A keypress was unaffected and hid the gap — a `PRESS_*`
  parameter is writable, so it does get a data point and travelled the
  ordinary path. The callback now forwards a data-point-less event when the
  parameter classifies as one; anything unclassified stays dropped as before.

  The event-group documentation already listed `device_error` as a kind a
  channel can report, so the promise was in place and only the delivery was
  missing.

- **A channel's event group never recorded the trigger that fired.**
  `GET /devices/{addr}/channels/{no}/event-groups` reports which triggers a
  channel offers and, per group, the last one that fired. The second half was
  always absent: delivery of a keypress reaches the WebSocket and the
  diagnostics stream directly off the event bus, but nothing fed it back into
  the model, so `last_triggered_event` stayed null however often a button was
  pressed. A client could enumerate its event entities and never learn that
  one had been used. The model's sources are now fed from the same
  device-trigger events the north-bound surfaces receive, including for a CCU
  adopted while the daemon runs.

- **The integration suite had been failing on `main` since 0.52.9.** Its MQTT
  availability guard still required an unobserved data point to publish
  `{"value":null,"available":true}` — the convention that gating availability
  on the full validity chain deliberately reversed in 0.5.x. No daemon
  behaviour changed here; the guard now pins the decided contract and states
  which side it holds, so the next reversal has to touch it on purpose.

  The guard had not merely gone stale. It stayed green for six weeks after
  the reversal because the calculated plane was not gated yet and its
  unobserved points still carried the old shape — so it passed on a plane it
  never named, measuring something other than what it claimed. It went red
  only when calculated validity was gated on source validity in 0.52.9. The
  assertion now names the VALUES plane.

- **`GET /devices/{addr}/channels/{no}/event-groups` answered with an empty
  list for every channel of every device.** The route reports which trigger
  kinds a channel offers — keypress, impulse, device error — so a client can
  build its event entities from it. Both ends of the model were complete: a
  channel could hold event sources and grouped them by kind once hydrated.
  Nothing in between ever attached one, so the answer was empty by
  construction rather than because a device lacked buttons. Device ingestion
  now attaches an event source for every VALUES parameter that carries the
  EVENT operation and classifies as a trigger, which is where the reference
  stack creates them too.

  This affects the bootstrap shape only. Trigger delivery itself was never
  broken — a keypress reaches subscribers as a device-trigger event on its
  own path — and `last_triggered_event` in the response stays null until the
  model sources are fed from that path, which is separate work.

- **After a CCU restart the daemon could end up registered twice, so every
  event arrived twice.** A rebooting CCU serves XML-RPC before it has
  finished starting. A reconnect landing in that window fails the `deinit`
  that precedes the re-registration but lands the `init`, so the CCU keeps
  the previous registration alongside the new one — it appends the second
  under a suffixed interface id (`…-BidCos-RF#2`). It then pushes every
  event once per registration, and anything reacting to those events runs
  twice, which is how CCU programs came to be executed twice after a
  restart.

  The reconnect now waits for the same boot marker (`/ise/checkrega.cgi`)
  that already gated the initial bring-up before it re-registers, bounded
  at 30 s so the client's own backoff keeps driving. A restart of the CCU
  clears an already-duplicated registration.

### Added

- **Program runs are recorded in the audit log** (`program_execute`, with
  central, program, trigger and outcome). When a program is reported as
  running twice, the CCU executes it either way and its own log does not
  say who asked — without this entry there was no way to tell a run the
  daemon triggered from one the CCU produced on its own, and those point
  at different causes. The record hangs on the event bus, so every route
  (REST, WebSocket, MQTT) is covered.

### Fixed

- **Restoring an uploaded backup always failed.** An archive uploaded
  through the web UI is stored under an id that names no CCU, so the
  restore path could not work out where to send it and fell through to a
  restorer nothing in the daemon ever installs. Every attempt answered
  "Backup restore failed" — an error that blames the CCU for a request it
  never received.

  With one CCU configured, which is the ordinary installation, an
  uploaded backup now restores to it. With several, the restore is
  refused with the reason rather than guessed: writing a backup onto the
  wrong CCU overwrites a live installation and cannot be undone.

  The two failures are also told apart now. "No restore path configured"
  answers 501 and says nothing was sent; only a CCU that was actually
  asked and refused answers 502. Both were 502 before, which is why a
  dead restore path looked like an unhappy CCU.

- **Saving a config section wiped every secret the operator did not
  retype.** Editing any MQTT field and pressing Save dropped the broker
  password: the daemon then sent a CONNECT with no password flag at all
  and the broker answered `Not authorized (0x87)` — which reads like a
  wrong credential, so the search went to the broker instead of the save
  path. The first save after typing the password worked, every later one
  destroyed it, and the UI kept showing `***` either way.

  The two sides of the masked-secret round-trip had drifted apart. The
  editor never receives a secret's cleartext, so it echoed a placeholder
  for a field it had not changed; the handler only reconciled the string
  forms of that placeholder and read the one the editor actually sent as
  a deliberate value. The contract is now explicit and pinned by a
  contract test: the editor omits an untouched secret entirely, an absent
  key (and the legacy `null` / `***` forms) means "keep the stored value",
  and an empty string on a string secret still clears the credential —
  so a secret remains deletable.

- **"Reload MQTT" reloaded the configuration the daemon had booted
  with.** A section saved in the Config UI is written to the database, an
  event no file-watcher follows, but the reload read only the snapshot
  that boot and the YAML watcher maintain. The rebuilt broker link
  therefore kept the previous settings while the action reported success,
  and only a full restart applied the change. The reload now re-derives
  the effective configuration the way boot does — the YAML base with the
  current database sections overlaid — and says so in the log when it has
  to fall back to the stale snapshot.

- **An unset secret was masked to `***` like a configured one.** That
  made a credential that had been dropped indistinguishable from one that
  was set, which is what kept the wiped password invisible. Empty secrets
  are now passed through unmasked and the editor marks them "not set".

- **The alarm system's system-variable mirror never wrote anything, and
  said nothing about it.** An operator who configured a zone output of
  class `sysvar_mirror` — so that arming, disarming and triggering show up
  in a CCU system variable — got the variable created and its value never
  written. A CCU program reading it waited for a trigger that could not
  arrive.

  Two faults met. The write path was never handed to the hub coordinator,
  on the line next to the one that hands it over for programs. And the
  coordinator answered a write attempt without a write path with success,
  so the mirror's own error branch never ran and no log line appeared.
  Both backends now wire the path, a missing one returns an error, and the
  central reports it at start-up.

- **Every screenshot baseline of the web UI showed a version of the UI that
  no longer existed, and the test suite called that a match.** The
  comparison allowed 2 % of the viewport to differ — 20 480 pixels, more
  than a navigation sidebar costs when it gains an entry. Measured against
  the code, all 35 committed baselines had drifted, by 2 600 to 12 100
  pixels each, and every run was green.

  The refresh command could not repair it either: `--update-snapshots`
  without an explicit mode means `changed`, and `changed` rewrites a
  baseline only when the comparison failed. Refreshing after a small edit
  therefore reported success and kept the old image — which is
  indistinguishable from "nothing changed".

  Screenshots are now compared exactly (`maxDiffPixels: 0`), which the
  container's rendering supports: two runs of all 37 screenshot tests
  reported zero differing pixels. The refresh command passes
  `--update-snapshots=all`, the wall clock is frozen so a rendered
  timestamp cannot force tolerance back in, and every baseline — plus the
  four screenshots in the user guide, stale for the same reason — has been
  regenerated from the current UI.

- **Three browser tests asserted a surface they never rendered.** The
  system-variable list gained `page`/`per_page` query parameters, which the
  mock's `**/api/v1/sysvars` pattern stopped matching (`*` does not cross a
  slash). The request then reached the dev-server proxy and the page
  rendered "API 502 Bad Gateway" — for both the empty-state and the
  error-state test, whose baselines were byte-identical as a result. The
  error-state guard had been asserting the empty state for as long as it
  existed. Five further endpoints (`/areas`, and four device
  sub-resources) were never mocked at all.

### Added

- **`mqtt.connect.failed`** — a rejected broker CONNECT now logs the
  broker, client ID, protocol version and whether a username and password
  were actually sent (presence and length, never the value). A broker
  answers a missing credential and a wrong one with the same
  `Not authorized (0x87)`, so those flags are what separate "typed it
  wrong" from "sent none".

- **The integration suite runs on every pull request.** It previously ran on
  pushes to `main` and on pull requests only when labelled, so it reported a
  defect after the change was already in. That is how one of its assertions
  came to be wrong for six weeks and red for 27 consecutive pushes without
  blocking anything.

- **A browser test now fails when one of its requests escapes the mock
  layer.** The suite is meant to be hermetic, but an unmatched route did
  not fail loudly — it fell through to the dev-server proxy, and the SPA
  rendered its error surface instead of the state under test. A catch-all
  registered ahead of every specific route records what escaped and fails
  the test with the list. It is what would have named the system-variable
  pagination break on the day it landed.

- **`TestScreenshotComparisonBudgetIsTightEnoughToSeeDrift` and
  `TestBaselineRefreshScriptRewritesEveryBaseline`** — contract tests that
  pin the two settings deciding whether the visual suite can see drift at
  all: an exact-match pixel budget, and a refresh command that rewrites
  every baseline rather than only the ones that already failed. Both are
  pinned outside the suite on purpose, because a test inside it would be
  scored by the same budget it is meant to constrain — which is why a
  green run could not report either fault.

- **`TestEveryWiringSetterHasAProductionCaller`** — a contract test that
  resolves every method injecting a collaborator and fails on those
  production never calls. It is the mechanical half of the wiring rule:
  in 0.52.12 the hub notifiers were dead because `SetHubModel` had no
  production caller, and the coordinator tests called it themselves, so
  they stayed green while every hub push was lost. Removing that call
  today names the seam and fails the build.

  The first clean run found 23 such seams. Most are dead surfaces or have
  a second path that carries the same duty; one has a user-visible
  consequence, recorded in the ratchet: nothing ever attaches a generic
  event to a channel, so `GET /devices/{addr}/channels/{no}/event-groups`
  can only ever answer with an empty list.

- **A boot-order guard that runs against the real daemon.** It starts the
  built binary against a CCU that is still warming up, flips the CCU to
  ready, and only then asserts that the security inventory, the hazard
  classes, the hub model and the Home Assistant discovery plane hold
  state.

  The order is the test. Against a CCU that answers instantly the daemon
  finishes loading before the domain services start, so every subsystem
  reads a populated model however broken the wiring is — measured, not
  assumed: the first version of this guard stayed green with the
  0.53.0 defect put back. Gating the CCU restores the order a real
  installation has.

- **`TestEveryEventTypeHasASubscriber`** — a contract test that resolves
  every `events.Subscribe` call through the type checker and fails on any
  event type nothing consumes, unless the silence is declared with a
  reason. The bus has no wildcard subscription, so an event with no
  subscriber reaches nothing while every test around it passes: the
  producer asserts it published, and the would-be consumer publishes onto
  its own bus. The first run found nine such events and two code comments
  naming consumers that were never written.

- **A topic round-trip test per MQTT plane** — alarm, hub, add-on update
  and the device plane join the security plane. Each collects every topic
  its discovery payloads declare and every topic the plane actually
  publishes or subscribes, and fails when a declaration has no
  counterpart. That comparison is what found the dead firmware-install
  button: the declaration side and the publish side each had passing
  tests, and nothing had ever compared them.

### Fixed

- **Every device's firmware "Install" button in Home Assistant did
  nothing.** The per-device update entity declared a `command_topic` and
  `payload_install`, so Home Assistant rendered an install control — but
  no subscription has ever matched that topic shape, and the press went to
  the broker and stopped there. The entity is now declared read-only,
  matching the CCU-level update entity, which is read-only by design for
  the reason that also applies here: flashing firmware from an
  unconfirmed broker payload that a reconnect may replay is unsafe. The
  install path is `POST /api/v1/devices/{addr}/firmware/update`.

- **The duress help text promised a delivery path that does not exist.**
  It told operators that visibility `hidden` "keeps it on the webhook and
  the raw alarm topic". The alarm MQTT publisher does not subscribe to the
  duress event at all — a duress disarm appears on that plane as an
  ordinary disarm, which is correct for a screen an attacker can see, but
  means `hidden` reaches the webhook and nothing else. An operator who
  read that sentence and skipped the webhook was told nothing at all when
  someone entered a code under coercion. Corrected in both locales, and
  the concept's visibility matrix along with it.


## [0.53.0]

### Added

- **The Security & Safety views in the config UI.** An overview that
  leads with the last report — subject and message, the thing an
  operator reads first — plus a tile per hazard and fault class, the
  open faults and the engine health. A source inventory that shows what
  the classifier believes about every data point and lets an operator
  override it, including removing an override again. A fault list with
  acknowledgement, which records that someone has seen the problem
  without pretending it went away.

  The inventory is deliberately listable unfiltered: a source the
  classifier got wrong is invisible in every aggregate, so listing
  everything is the only way to find it.

  New: `GET /api/v1/security/sources` and
  `PUT /api/v1/security/sources/{ref}`.

- **Security & Safety reaches Home Assistant.** The domain now publishes
  its own MQTT plane and its own device card: a folded state, an "is
  something wrong right now" flag, a fault flag, one entity per hazard
  and fault class the installation actually has, one per zone, and two
  event streams — hazards and faults kept apart so they can be routed to
  different destinations without inspecting the class.

  A class with no source is not published at all. An installation
  without gas detectors should not carry a permanently-off gas alarm.

  The event topics are deliberately **not** retained: a consumer ignores
  retained payloads on an event topic, and a retained alarm event would
  re-fire every automation on every broker restart. `last_alarm` and
  `last_fault` are retained precisely because of that — after a consumer
  restart they are the only record of what happened.

  Topic reference: `docs/mqtt-topic-schema.md`; rationale: ADR 0059.

- **`alarm.duress_visibility` now actually does something.** The setting
  shipped earlier in this release but nothing produced a duress report
  yet. The domain subscribes the duress event and applies the policy at
  the single point where the report is created, so MQTT, the webhook and
  the API cannot disagree about it.

- **Security & Safety: the domain that says what is wrong and tells someone.**
  A new daemon-level plane aggregates every classified data point into
  hazard classes (smoke, water, gas, CO, intrusion, panic) and fault
  classes (tamper, battery, technical), per system and per zone, and
  renders a report a person can read: a one-line subject and a full
  sentence naming cause, place and time — in German and English.

  It runs **independently of the alarm engine**. A household with smoke
  and water detectors but no burglar alarm gets the hazard classes, the
  fault plane and the notifications; only the zone half stays empty.

  Reports carry the rendered text *and* the machine facets — class,
  severity, verb, sources, and the catalogue key with its arguments.
  The text makes a three-line automation possible; the key lets a
  consumer render in its own locale instead of translating prose.

  Faults are persistent, because `since` is the interesting part:
  "unreachable for three days" is a different fact from "unreachable",
  and a restart must not reset that clock. They can be acknowledged
  without being cleared — the condition stands, the operator has merely
  stopped needing to be told.

  New: `GET /api/v1/security`, `/security/classes/{class}`,
  `/security/faults`, `POST /security/faults/{id}/acknowledge`.

- **`alarm.duress_visibility` decides where a covert trigger may
  appear.** `hidden` keeps a duress-code use or silent panic on the
  webhook only, `notify_only` (the default) additionally sends the
  notification so a phone is reached but never writes it to retained
  state or a local screen, `full` treats it like any other alarm. The
  threat is not an insecure Home Assistant — it is that whoever stands
  next to you sees the same screen.

- **A smoke detector can no longer be triggered by the alarm system's own
  siren.** `SMOKE_DETECTOR_ALARM_STATUS` has the value list `[IDLE_OFF,
  PRIMARY_ALARM, INTRUSION_ALARM, SECONDARY_ALARM]`, and the engine's
  rule was "anything but index 0 counts as an activation" — so
  `INTRUSION_ALARM`, which means the installation drove that detector as
  a siren for a burglary, counted as a smoke detection. A new
  `active_values` list on a sensor enrollment names exactly which values
  activate. **Leaving it empty keeps the previous behaviour**, so no
  existing enrollment changes meaning.

  The live event path and the restore path now reach that verdict
  through one shared function. They carried separate copies of the rule,
  and a divergence would have meant a sensor reading active while
  running and inactive after a restart.

- **`GET /api/v1/alarm/sensor-candidates`.** Outputs and remote keys had
  candidate lists; sensors did not, so enrollment was unvalidated free
  text over central, interface, channel address and parameter — a typo
  produced a sensor that silently never fired. Each candidate carries
  the suggested role, the hazard class, the parameter's value list, and
  the recommended `active_values`. The config UI now pre-selects the
  derived `SMOKE_ALARM` boolean instead of the raw status enumeration.

- **Every alarm trigger is recorded, not just the first.** The engine's
  trigger path returns early once a zone is already triggered, so a
  second detector going off while the siren sounds left no trace beyond
  a journal line carrying an opaque row id. A new per-incident source
  ledger records every contributing data point with its full identity —
  central, interface, channel address, parameter, sensor role — and the
  trigger event re-publishes with the grown list. "Which detectors
  fired?" is now answerable; before it was not.

- **`GET /api/v1/alarm/incidents` and `/alarm/incidents/{id}`.** An
  alarm's history was reachable only through the journal, which records
  events rather than episodes and holds no source identity. The incident
  store had carried a list query since it existed, with no caller.

- **Notifications say what set the alarm off.**
  `alarm_panel.notification` — the event an operator enrols for a
  messenger — carried no sensor identity at all. It now carries the
  cause and the full source list, on both the webhook and MQTT planes.

- **Security & Safety taxonomy — the classification layer.** A new
  package classifies device data points into a hazard/fault taxonomy
  (smoke, water, gas, CO, tamper, battery, technical, intrusion, panic)
  keyed on the triple (model, channel type, parameter) rather than on the
  parameter alone, because the same parameter means different things on
  different channels. This is the foundation of the Security & Safety
  domain described in `notes/concepts/security-safety-concept.md`; on its own it
  changes no north-bound surface.

  The classifier refuses to read back anything the alarm engine writes.
  Most consequential case: `SMOKE_DETECTOR_ALARM_STATUS` carries
  `INTRUSION_ALARM` at index 2 of its value list, so the established
  "active means index != 0" rule counts it as a smoke detection — while it
  actually means the installation drove that smoke detector as a *siren*
  for a burglary. A domain built on that rule would report its own siren
  command as the cause of a fire.

### Fixed

- **A fault that arose while the daemon was down never entered the
  ledger.** The index seeded its in-memory activation from the device
  model at boot, so a device that went unreachable during a restart
  showed as active in the class view — but no fault row was written, so
  it had no `since`, produced no report, appeared in no fault list, and
  could not clear afterwards either, because there was no row to close.
  The ledger is now reconciled against the model whenever the index is
  rebuilt, in both directions.

- **The fault ledger grew without bound.** Each raise/clear cycle writes
  a new permanent row and nothing ever reads a cleared one. Cleared
  faults are now pruned daily on the alarm journal's retention window —
  the two answer the same question about the same installation, and a
  second knob would only be a second thing to get wrong.

- **A deleted alarm zone kept its Security & Safety entity forever.**
  Zones entered the aggregate but never left it, so the snapshot still
  carried a deleted zone, so the retraction pass never saw it disappear
  and its retained state and consumer entity survived the deletion
  indefinitely. A zone created after boot also settled on a derived
  fallback slug until the next restart; it now takes its real slug
  immediately.

- **The fault event topic had two producers writing two different payload
  shapes.** Every fault transition arrived twice — once as a ledger
  record with an id and a count and no text, once as a rendered report
  with a subject and a message and no id — and a consumer's event entity
  parses one shape per topic. Every automation reading either field got
  it on half the messages. The rendered report is now the sole producer;
  the ledger facts live in the retained fault-flag attributes, which
  carry the full standing list with ids, counts and acknowledgements.

  An acknowledgement also announced itself as `raised`, which is what an
  automation would act on a second time, and reported a standing count of
  zero on the one transition that proves a fault stands.

- **An operator's class override made the fault it described unclearable.**
  Overriding a data point's class dropped the classifier's reason facet,
  and the clear path keys on that reason: the fault could open, dedupe
  onto itself on every later raise, and never close. The same held for a
  tamper-typed alarm enrollment.

- **A smoke detector's two self-diagnosis faults had no data point.** A
  soiled smoke chamber and a failed self-test are the conditions that
  make a detector stop protecting without announcing it, and both were
  suppressed by the visibility rules — so neither could appear in any
  fault list. Un-ignored on HmIP-SWSD.

- **Two parameter constants named things that are not parameters.** The
  published enum schema loses two entries as a result; the API version
  moves by a minor step rather than a major one because neither value
  could ever appear on the wire — they described data points no device
  has, so no consumer can have received one.
  `ERROR_CODE` and `ERROR_NON_FLAT_POSITIONING` were added to the
  taxonomy as data points and mapped by the classifier, but neither
  exists: the CCU firmware string table carries the real smoke-detector
  error parameters as `SMOKE_DETECTOR|<PARAM>=<VALUE>` and these two not
  at all, the reference library lists the second as an error *value*,
  and neither appears on any device in the simulated fleet. Removed
  along with their classifier entries, which could never have matched.

- **Water leaks now reach the Matter bridge.** The binary classifier
  said in its own comment that leak parameters were "not classified
  yet"; `MOISTURE_DETECTED` and `WATERLEVEL_DETECTED` now map to the
  Leak class. `ALARMSTATE` deliberately does not: it is a device-wide
  alarm flag, and on a siren the same name means actuator feedback.

- **Retained discovery of the two daemon-level planes could never be
  cleaned up.** The orphan sweep filtered on the `<central>_` node
  prefix, which neither the alarm plane nor the security plane carries —
  so a retracted zone panel kept a retained discovery config alive in
  every consumer indefinitely, unreachable by any cleanup pass. Both node
  ids are now named explicitly.

- **The CCU add-on registered no control-panel tile.** The install hook
  called `/bin/update_hm_addons.tcl` — a helper that exists on no CCU
  firmware. Guarded by an `-x` probe, the call was skipped silently, so
  nothing was ever written to `/etc/config/hm_addons.cfg` and the WebUI's
  "Systemsteuerung" had no OpenCCU-Loom button to render. Both the
  install and the uninstall path now use the real platform helper,
  `/bin/updateAddonConfig.tcl`, fall back to the HomeMatic Tcl API
  (`AddConfigPage` / `RemoveConfigPage`) on firmware that predates it,
  and print a warning when neither is available instead of leaving the
  operator with a silently tile-less install. The tile appears on the
  next install or update of the add-on.

- **A fresh CCU add-on install was locked out completely.** CCU-delegated
  login is on by default there, which suppressed the first-run onboarding
  wizard — but it cannot authenticate anyone until a central is
  configured, and configuring one requires an authenticated session. The
  result was no wizard *and* no working CCU login. CCU-delegated login
  now only counts as an available authentication source once at least one
  central exists, so onboarding stays reachable until it can genuinely
  take over.

- **CCU login ignored centrals configured in `config.yaml`.** The auth
  resolver consulted only the SQLite `centrals` table and failed closed
  when it was empty, instead of deferring to the YAML tier the way the
  rest of the daemon does. A deployment that declares its centrals in
  `config.yaml` — never adopting one through the SPA — could therefore
  never sign in with a CCU account. The table now wins whenever it holds
  any row; an empty table hands authority back to `config.yaml`. Unknown
  and disabled centrals still fail closed.

- **The onboarding wizard's CCU stayed dark until the next restart.** The
  wizard wrote its optional central straight to the store, bypassing the
  live-adopt path that `POST /admin/centrals` uses. The freshly onboarded
  CCU is now brought up immediately, so devices appear — and CCU-delegated
  login starts working — without restarting the daemon.

- **A hazard sensor could be configured never to fire.** A sensor typed
  `hazard` but not marked `always_on` falls into the arm-state machine,
  so it only fires while the zone is armed in one of its listed modes —
  and with the empty mode list that is normal for a smoke detector, it
  never fires at all. The API now couples the two on write, so the
  failure cannot be configured. Existing enrollments in that state are
  reported at startup.

- **`FAILED_TO_ARM` reported opaque sensor ids where `TRIGGER` reported
  names.** The two alarm events disagreed on what `open_sensors` even
  contained, so a consumer could not treat the field uniformly. Both now
  carry display names, plus a structured `sources` array. The arm-failure
  hook forwards the blocking reason with each sensor, so "why can I not
  arm?" is answerable — readiness previously deduplicated the reason away
  and a sensor blocking for two reasons collapsed into one entry.

- **Closed alarm incidents were never purged.** The journal retention
  chain ran daily; incidents were kept forever, and the store's purge
  method had no caller. Retention now applies the same window to closed
  incidents and sweeps source rows their incidents left behind.

- **Water and rain sensors get their sensor tile back.** The config UI
  looked up the channel types `WATER_DETECTOR` and `RAIN_DETECTOR`, which
  no CCU emits — the real names are `WATER_DETECTION_TRANSMITTER`,
  `WATERDETECTIONSENSOR`, `RAIN_DETECTION_TRANSMITTER` and `RAINDETECTOR`.
  Both the primary-value lookup and the quick-control status list missed
  every leak and rain sensor as a result. The leak icon and colour rule
  matched an equally non-existent parameter and now match the real
  `MOISTURE_DETECTED` / `WATERLEVEL_DETECTED`.

### Removed

- `HmIP-SWD` no longer maps `STATE` onto `device_class: window`. HmIP-SWD
  is the water sensor and has no `STATE` parameter, so the rule was
  unreachable — and had it ever matched, it would have labelled a leak
  detector a window contact. Window contacts (`HmIP-SWDO`, `HmIP-SWDM`,
  `HM-Sec-SC` and their variants) are unaffected. Recorded as a deliberate
  divergence in `notes/parity/by_design.md`.

## [0.52.12]

### Fixed

- **Hub push events reach the bus again — the boot wiring was missing.**
  0.52.10 and 0.52.11 taught the model to announce a program's activity
  flip, a program's execution and a system variable's value change, and
  `HubCoordinator.SetHubModel` to wire those announcements onto the event
  bus — but nothing in a running daemon ever called `SetHubModel`. The
  coordinator-level tests attach the model themselves, so they stayed
  green while every real daemon ran with nil notifier hooks: the REST
  surface answered correctly from the model, and `hub.program_changed`,
  `hub.program_executed` and `hub.sysvar_changed` never fired. Verified
  live: toggling a program returned 202 and flipped the model while zero
  frames crossed the WebSocket.

  Consequence for Home Assistant: switching a program off snapped back to
  "on" in the UI (the confirming push never came) and the paired "run now"
  button never went unavailable; system variables froze on their
  bootstrap value over REST/WebSocket.

  `central.New` now attaches the hub model to the hub coordinator at
  construction, exactly as the `SetHubModel` contract ("call once at
  daemon boot") always intended. The regression test drives the
  production constructor alone — registering a program and a system
  variable the way the hub scan does and asserting the bus events arrive —
  so the wiring can no longer silently detach from the tests.

## [0.52.11]

### Fixed

- **A button press never reached a REST/WebSocket client.** The CCU reports
  a keypress as a callback like any other, but a keypress is not a value:
  `PRESS_SHORT` arriving twice is two presses, and no consumer can recover
  "a button was pressed" from a value-changed message without re-deriving
  the classification and the repeat semantics the daemon already owns. That
  is what the `device.trigger` broadcast is for — and its only publisher had
  no caller in a running daemon, so the raw callback path emitted the value
  change and nothing else.

  Consequence for Home Assistant: device triggers and keypress event
  entities never fired through this daemon. Remotes and wall switches
  produced no automation trigger.

  The callback path now emits the trigger alongside the value.
  `event.Classify` decides — click parameters become keypresses,
  `SEQUENCE_OK` an impulse, the `ERROR` / `SENSOR_ERROR` prefixes a device
  error, everything else nothing — so the rule lives in one place. Repeated
  identical presses each surface, matching the exemption the value path
  already makes for edge-trigger parameters.

  Found by walking all 36 declared broadcasts against their publishers after
  the same defect turned up in 0.52.10 and in the system-variable fix below.
  This was the last one: every other broadcast has a live publisher.

- **A system variable's value never reached a REST/WebSocket client after
  start-up.** The daemon polls the CCU and pushes what changed — that is the
  design, and it worked over MQTT, whose publisher subscribes to the hub
  model directly. The WebSocket plane listens for the internal
  `SysvarChangedEvent` instead, and nothing in a running daemon ever
  published it: the only publisher was a coordinator method with no caller
  (the hub scan writes to the model, not through it). So
  `hub.sysvar_changed` was declared, bridged, contract-tested — and silent.
  A client's variables froze at whatever its bootstrap read.

  The model now notifies on an actual value change, and attaching the hub
  model wires that notifier for every system variable — the ones already
  present and everything the scan registers later. Re-observing the same
  value on a scan cycle stays silent, as before.

  Same defect as the program notifiers fixed in 0.52.10, and the same blind
  spot: the contract guard checks that a declared broadcast has a WebSocket
  emitter, not that anything upstream feeds it. Both are now pinned by tests
  that drive the model and assert the broadcast arrives.

## [0.52.10]

### Fixed

- **A program's activity flag was a change nobody was told about.** A CCU
  program is two controls: the activity flag decides whether it reacts at
  all, and the execution runs it once — and a deactivated program refuses
  the execution. 0.52.7 made that rule explicit in the model and on the REST
  surface, but the transition itself never left the daemon. `Program.OnActive`
  recorded the flag silently, so the only thing that ever notified a
  subscriber was an execution: over MQTT a program's execute-availability
  stayed stale until the program next ran, and over WebSocket there was no
  program-changed message at all. A client could ask for the right answer,
  but never learn that it had changed.

  The activity flag now notifies on every transition, which is what the MQTT
  availability topic was already listening for, and a new
  `hub.program_changed` broadcast carries `active` + `execute_available` to
  WebSocket clients (API 3.14.0). Re-observing the same flag on the periodic
  hub scan stays silent.

- **`hub.program_executed` never fired.** Its wiring lived in a method with
  no caller: the hub scan registers programs directly on the hub model, so
  every program a running daemon had carried an unwired notifier. Attaching
  the hub model now wires both notifiers — for the programs already present
  and for everything the scan registers later.

- **The WebSocket value-changed push now says whether the value is a
  confirmed reading.** `available` (the same verdict the MQTT slot state
  carries, added for calculated data points in 0.52.9) rides on every
  `datapoint.value_changed` message. A consumer could not derive it:
  `observed` stays true through a measurement fault, and the transition
  *into* a fault usually arrives as a value change — so a client reading
  availability only when it refreshes its catalogue rendered the faulted
  value as confirmed. MASTER-paramset entries are always reported available;
  configuration is not a runtime reading.

## [0.52.9]

### Fixed

- **A calculated sensor no longer reports a value its own sources have
  disowned.** Dew point, frost point, enthalpy, apparent temperature, vapour
  concentration and the derived battery level are computed from a channel's
  ordinary readings. Those readings can be *read but unusable* — the CCU
  flags a measurement fault through the paired `…_STATUS` parameter, or the
  value falls outside the bounds the device itself declares. The reading then
  reports unavailable, exactly as intended, but the derived sensor kept
  reporting available and kept recomputing from it: a temperature stuck at
  `OVERFLOW` still produced a confident dew point, and a 999 °C reading
  produced a dew point of 4124 °C.

  A derived value is only as good as its inputs, so every calculated data
  point is now available only while all the sources it derives from are
  usable. It recovers on its own as soon as they are, without waiting for a
  fresh value. Configuration inputs stay exempt: `LOW_BAT_LIMIT` is read from
  the device's MASTER paramset, and a sleeping battery device may never
  deliver a fresh one — that must not take the battery level down.

  Over MQTT the calculated topics were the one plane that published
  `available: true` unconditionally, so this is where the effect is visible:
  an affected Home Assistant entity now goes unavailable instead of showing a
  wrong number. `GET /api/v1/devices/{addr}/channels/{no}/calc-dps` and the
  `calc_dp.*` WebSocket records gain the same `available` flag (API 3.13.0);
  the existing `observed` field cannot answer this — it stays true right
  through a source fault.

## [0.52.8]

### Fixed

- **The `INTERNAL` marker explains itself per list.** Both the
  system-variable and the program picker showed one shared text, and that
  text described programs — a variable list telling you that "the CCU flags
  most ordinary user programs as internal" answers a question nobody asked
  there. Each list now carries its own wording; the program one keeps the
  warning that matters, since without the marker its list stays nearly
  empty.

## [0.52.7]

### Changed

- **The `MQTT` marker is no longer offered for system variables or
  programs.** It steers an MQTT hand-off the reference stack needs and this
  daemon does not: its own bridge publishes every hub entity regardless, so
  the marker only ever acted as a second free-text tag beside `HX`. The CCU
  editor drops a stored value when it opens, so what is shown is what gets
  saved. The token is still stripped from the CCU description, so a variable
  whose description still carries it does not show it in its name.
- **System variables and programs are picked up within 30 seconds instead
  of 5 minutes.** Both hub scans ran on a 5-minute cadence, so a variable or
  program added on the CCU could take that long to appear. The compiled-in
  default is now 30 seconds, matching the reference stack's shared
  `sys_scan_interval`. The cost is modest: each cycle is one JSON-RPC call
  that already carries the values, plus one script run for the metadata.
- **An operator-set scan interval below 3 seconds is rejected.** Each cycle
  costs the CCU a script run on a single-threaded interpreter, so a cadence
  short enough for cycles to overlap starves the CCU's own automations
  rather than delivering fresher data. Configuration validation names the
  offending central, and the scheduler floors the value independently in
  case it arrives another way. `0` still selects the default.

- **The profile filesystem now satisfies the `fs.FS` contract.** The adapter
  added in 0.52.6 refused to open its root and reported a stale size after a
  read, because `bytes.Reader.Len()` counts the bytes still unread. Neither
  reached an operator — the profile stores open archives by name — but any
  generic traversal (`fs.WalkDir`) would have failed. Found by running the
  standard library's own conformance suite over it, which now guards it.

- **A CCU program is two controls, and the daemon now says so.** A program
  has an activity flag that decides whether it reacts at all, and an
  execution that runs it once — and a deactivated program refuses the
  execution. That rule was nowhere in the daemon: consumers each had to
  rediscover it, and the MQTT plane collapsed both into a single switch
  whose `turn_on` *executed* the program instead of activating it.

  `GET /api/v1/programs` and the WS payload gain `execute_available`
  (API 3.12.0), and the program model declares its two controls through
  the generalised `MQTTAddressable` surface, so the bridge transcribes
  them rather than knowing them (ADR 0011). Over MQTT a program now
  surfaces as a switch on `…/programs/<id>/set` (activity) and a button on
  `…/programs/<id>/trigger` (execution) whose availability follows the
  flag.

  **Breaking for MQTT:** publishing to `…/trigger` still executes the
  program, but the switch entity no longer triggers — it toggles activity.
  An automation that used the switch to run a program has to use the
  button, or publish to the trigger topic directly.


## [0.52.6]

### Fixed

- **The profile dropdown shows umlauts.** 0.52.5 decoded the CCU WebUI's
  HTML references in the two profile stores, but the link editor does not
  read them from there: it takes the profile document straight from the
  embedded archive and hands it to the SPA verbatim, precisely so the UI
  consumes the archive's exact shape without a Go schema mirror that would
  drift on every upstream refresh. That path kept its `&auml;`. The
  references are now decoded where the archive is loaded, which is the one
  point every consumer passes — 164 display strings across 66 receiver
  types.
- **Every embedded-archive load is guarded against this class of defect.**
  A standing test re-serialises what each loader hands the daemon and fails
  on any HTML reference in it. It works on the loader's result, not the
  archive, so a reference in a field nothing reads stays green — while an
  archive refresh, or a new struct field that pulls such text in, fails the
  build instead of surfacing in an operator's dropdown.

- **The profile archives are now clean at the source** (go-openccu-data
  0.1.3): the extractor decodes the references itself, so the daemon's own
  decoding is a no-op against current data and stays only as cover for an
  older module. That release also catches the archive up with OCCU 3.89.5,
  where eQ-3 narrowed `LONG_PROFILE_ACTION_TYPE` from the two-element list
  `{1 5}` to the scalar `1` on 26 profiles. **This shifts profile
  detection**: a direct link whose `LONG_PROFILE_ACTION_TYPE` reads 5 no
  longer matches those profiles and shows as *Expert*, and because a fixed
  constraint scores as more specific than a list, an affected profile can
  now win a match it previously lost. The archive agrees with the CCU's own
  WebUI again.

- **The profile archives exist once instead of three times.** Two stores
  each embedded their own copy of the same 65 eQ-3 archives, beside the
  shared module's. Nothing kept them in step, so the same profile answered
  differently depending on which code path read it: the copies still carried
  the CCU's HTML references *and* the pre-3.89.5 constraint set, and were
  missing 45 range bounds the module has. Both stores now read through the
  module, 130 duplicated files are gone, and a contract test fails the build
  if a copy reappears.


## [0.52.5]

### Fixed

- **Profile names show umlauts instead of `&auml;`.** The profile archives
  are lifted from the CCU WebUI, whose display strings are HTML fragments —
  a profile named *Bewässerungsaktor* is stored as `Bew&auml;sserungsaktor`,
  and `on/off &amp; louder` for the ampersand. Every north-bound surface
  renders them as plain text (correctly — escaping them again is what stops
  a device name from injecting markup), so the reference itself was shown to
  the operator. Both profile stores now decode the references once on load:
  127 affected display strings across link and master profiles.

## [0.52.4]

### Fixed

- **The valve-opening level of an HmIP-eTRV / HmIP-HEATING channel is
  delivered once again, as a sensor.** The parameter is forced to a read-only
  sensor, and the reference disambiguates that surface with a `_sensor`
  suffix on the identifier. The daemon applied the suffix internally but
  dropped it from the external key it publishes over REST, WS and MQTT — so
  a consumer keying its registry on that string orphaned the entity it had
  under the suffixed key and created a duplicate beside it. All three planes
  now spell the identity the same way. Home Assistant users see the
  duplicate `…_ventil_offnungsgrad_2` entity go stale; it can be deleted.
- **The marker checkboxes in the CCU editor now say what each marker does.**
  They rendered as four bare codes with no explanation, and the descriptions
  written for them landed on a config-schema field that is never shown —
  the `centrals` section is edited in the CCUs tab, not in the generic
  section editor. Each checkbox now carries its effect inline, above a note
  that markers steer how an entry arrives rather than whether it is
  imported.
- **`HAHM` is no longer offered as a program marker.** It makes a *system
  variable* writable, and a program has no value to write — offering it
  promised an effect that does not exist. A stored value is dropped when the
  editor opens, so what is shown is what gets saved.

## [0.52.3]

### Fixed

- **The `INTERNAL` marker now surfaces the CCU's internal entries.** It was
  only honoured for the enabled-by-default decision, while whether internal
  system variables and programs were delivered at all still hung on
  `include_internal_sysvars` / `include_internal_programs` alone. The
  reference gives the marker its own meaning — *"includes CCU-internal
  variables/programs"* — so configuring it is itself the request. The gap
  was invisible for system variables (whose boolean was on) and hid 38 of
  40 programs, because the CCU flags most ordinary user programs as
  internal. Either the marker or the boolean now suffices.

## [0.52.2]

### Fixed

- **System variables and programs are no longer hidden by markers.**
  Description markers (`HAHM`, `HX`, `INTERNAL`, `MQTT`) were treated as
  an import filter: an entry whose CCU description carried none of the
  configured markers never entered the model at all. The reference stack
  documents the opposite — everything is imported, and markers decide
  only whether an entry arrives *enabled*. The difference is not
  cosmetic: an entity that is never created cannot be switched on by the
  operator afterwards. On a real installation this left 23 of 83 system
  variables and 2 of 40 programs visible. Markers now feed
  enabled-by-default only; `include_internal_sysvars` /
  `include_internal_programs` remain the switches that genuinely gate
  import.

- **Calculated data points carry the `calculated` family marker in their
  `unique_id`.** They were keyed like a plain VALUES parameter
  (`loom_<device>_<channel>_<parameter>`), omitting the marker the
  reference scheme puts ahead of the address. Consumers that key their
  entity registry on it migrate by exact string match, so every
  calculated data point orphaned the entity the consumer had just
  migrated — the one holding history, area and customisations — and got
  an empty duplicate beside it. On a real installation that affected all
  159 of them. REST, WebSocket and MQTT discovery now share one builder
  so the three planes cannot drift apart, and the shape is pinned by a
  contract test.

## [0.52.1]

### Fixed

- **The fleet page loads again, and its CCU-interface list is populated.**
  `Interface.listInterfaces` reports `name`, `port` and `info` — not the
  `type`, `address` and `url` the decoder read — so every entry came back
  with empty strings. That blanked each interface badge, marked every
  interface as unmanaged, and gave Svelte one duplicate key per
  interface, which throws and took the whole `#/fleet` route down. The
  decoder now reads `name` as the interface identifier (the same token
  `configured_interfaces` is keyed on) while still preferring an explicit
  `type`/`address` where a firmware supplies one, and the badge list no
  longer keys on a field that can repeat — a display list rebuilt on
  every load must not be able to break its route.

- **Actuator channels can be pinned to favourites.** The pin star only
  ever appeared on the generic fallback tile, so exactly the channels
  worth quick access to — switches, dimmers, covers, thermostats, every
  channel backed by a custom data point — were the ones that could not be
  pinned. The star now sits on those tiles too, and a pinned channel
  renders its real control tile in the favourites view instead of the
  generic fallback, so pinning a switch no longer costs it its toggle.

## [0.52.0]

### Added

- **Optional backup before a CCU firmware update (SY06).** The system
  update card gains a "back up first" checkbox: with it,
  `POST /api/v1/system/update/install` takes a full CCU backup and starts
  the update only once that backup is durably stored. A failed backup
  aborts and the update does not run — the whole point of asking for one
  is to have something to return to. The call blocks while the backup
  runs (minutes on a large configuration), because its response is what
  tells the operator whether the safety net exists; the checkbox is off
  by default so that wait is never a surprise (API 3.11.0, additive).
  No changelog link is offered: the CCU's firmware check reports only
  version numbers and no release-notes source, and a constructed URL
  would be a guess that breaks differently on every firmware variant.

- **Import an externally-supplied CCU backup (SY02).** `POST
  /api/v1/backups/upload` takes in a `.sbk` archive produced elsewhere —
  another CCU, an older daemon, the CCU WebUI — and stores it so it can
  be restored like a locally-taken backup. The archive is inspected
  before it is stored, so picking the wrong file fails immediately rather
  than at restore time, when the CCU is already being wiped: it must be a
  readable tar carrying the configuration archive and its signature. The
  signature itself is not verified — that needs the CCU's key material,
  and claiming otherwise would be dishonest — but the firmware version
  the archive came from is read and reported, which is the same fact the
  CCU's own restore consults. Admin-gated and audited; the Backups view
  gains an Import button (API 3.10.0, additive).

- **CCU host control: shutdown, safe mode and recovery (SY07, SY15,
  SY16).** The CCU maintenance card in Settings → System gains three
  actions next to the existing reboot: shut the CCU down, restart it into
  safe mode (logic layer held down, so a configuration that breaks normal
  startup can be repaired), and restart it into its recovery system. All
  three are admin-gated, audited and confirmed before dispatch
  (`POST /api/v1/system/ccu/{central}/{poweroff,safe-mode,recovery-mode}`;
  API 3.9.0, additive). Recovery is an OpenCCU / RaspberryMatic feature,
  so `GET /api/v1/system/ccu` reports `recovery_mode_supported` and the
  button is hidden — not disabled — where it cannot work: there is
  nothing an operator could do to enable it on a stock CCU3. The
  shutdown confirmation says plainly that nothing brings the CCU back
  remotely.

- **The CCU's astro position is visible and editable (SY05).** Every
  sunrise/sunset time a CCU computes — for its own programs and for the
  weekly profiles this daemon edits — derives from a latitude/longitude
  pair stored on the CCU. It was invisible here, so a wrong location
  skewed every astro schedule with no error anywhere. `GET
  /api/v1/system/ccu` now reports `longitude`, `latitude` and the CCU's
  `timezone`, and the CCU maintenance card in Settings → System shows
  them and lets an admin correct them (`PUT
  /api/v1/system/ccu/{central}/position`, audited; API 3.8.0, additive).
  The write is confirmed rather than assumed: the ReGa script reads the
  values back and the daemon compares them, so a success response means
  the CCU holds exactly what was sent. Coordinates are range-checked
  before substitution — a ReGa script takes its parameters textually, so
  an out-of-range value would otherwise be written verbatim. An
  unresolved position is reported as absent, never as 0/0, which is a
  real place in the Atlantic. Verified against real firmware: write,
  independent read-back, and restoration of the original coordinates.

- **Favourites are operable, and channels and programs can be pinned
  (O01).** Pinned entries stop being bookmarks: a pinned device now
  renders its live tile set right on the favourites page — the same
  CDP-tile and AutoTile pipeline the overview uses, not a second
  implementation — a pinned channel renders its own tile, and a pinned
  program gets a run button. Channels are pinned from the star on their
  tile in the device's channel list, programs from the program list.
  Pinned system variables were already editable inline and are
  unchanged. The pin store is per-user server-side preferences holding
  opaque JSON, so the two new kinds need no migration: an older client
  ignores a kind it does not know, and this one tolerates entries it
  cannot resolve — a device that has since disappeared degrades to the
  plain link it was before rather than blanking the page.
- **Electricity costs in the energy view (SY18).** A tariff
  (`persistence.history.energy_price_per_kwh`, plus an
  `energy_currency` label) makes the energy view show what the recorded
  consumption cost — under the consumption total and as an extra,
  sortable column per device. Without a configured tariff nothing
  changes: no cost figure appears anywhere, because a tariff of zero
  would render every amount as `0.00`, which reads as "free" rather than
  "not configured". The daemon echoes the tariff on
  `GET /api/v1/energy` (`price_per_kwh`, `currency`; API 3.7.0,
  additive) rather than computing the amounts itself — rounding and
  money formatting are locale decisions the UI already owns.
- **A configurable start page per user (O03).** Settings → Interface gains
  a "Start page" selector; the chosen view is what opens after logging in
  or reloading. The preference is stored server-side per user, so it
  follows the operator to another browser or device — unlike theme and
  language, which stay device-local because they are device-shaped. A
  direct link always wins: an explicit `#/…` in the URL is never
  overridden. The selector offers exactly the views the operator can
  currently reach (the Matter, history and admin views appear only when
  their gate is open), because it is built from the same navigation table
  the sidebar renders — previously that table lived inside the sidebar
  component, and any second list would have drifted from it. A stored
  route whose view no longer exists falls back to the default.
- **The sidebar shows how many messages are waiting (D01).** The
  navigation's Messages entry carries a badge with the number of pending
  service and alarm messages — a count in the expanded sidebar, a dot in
  the collapsed one. Until now an operator only learned about a pending
  message by opening the list: the daemon has always broadcast
  `hub.service_message` / `hub.alarm_message` on every change, but nothing
  in the UI consumed them. The badge seeds from the per-central hub
  snapshot and is then kept live by those broadcasts, so a message
  acknowledged anywhere — this tab, another tab, the CCU WebUI, a rule —
  moves the count without a reload. Counts are tracked per central and
  summed, so one CCU's broadcast cannot overwrite another's. The message
  list itself now refreshes on the same broadcasts, silently (the table
  stays in place instead of flashing back to its loading state) and
  debounced, so an acknowledge-all across several centrals triggers one
  refetch rather than one per central.

- **A guide for placing OpenCCU-Loom next to Home Assistant**
  (`docs/user/home-assistant.md`). The daemon and HA overlap, and there
  are three ways devices can travel from the daemon into HA — MQTT
  Discovery, the *Homematic(IP) Local* loom backend (preview), and the
  Matter bridge — each of which creates its **own** HA entities. The
  page states that constraint ("exactly one device path per device"),
  maps nine scenarios from "HA only, no daemon" to "daemon as the logic
  host", and adds a combination matrix, the anti-patterns (duplicate
  entity sets, disabling MQTT Discovery without retracting its retained
  configs, the UDP-5540 collision with HA's Matter Server, the lost
  alarm panel), migration paths, and a per-path feature comparison. The
  deployment-topology section carries an explicit recommendation — run
  the daemon **on the CCU** via the CCU / RaspberryMatic add-on, with the
  four cases that argue for a separate host. Linked from the README, the
  docs landing page and Getting Started.
- **The CCU's own security posture and interface list in the fleet view.**
  Each central now reports whether the CCU requires authentication
  (`auth_enabled`) and whether it redirects plain HTTP to HTTPS
  (`https_redirect_enabled`), plus the interface adapters the CCU reports
  for itself (`ccu_interfaces`, with type / address / XML-RPC port / URL)
  — all three on `GET /api/v1/system/ccu` (API 3.5.0, additive). The
  Fleet cards render the two flags as labelled chips and list the
  CCU-reported interfaces, highlighting any the daemon is *not*
  configured for. All three facts are best-effort at bring-up: a firmware
  that does not answer leaves the zero value, which reads as "no / not
  discovered" instead of failing the central. The flags stay out of the
  MQTT-Discovery hub block by design (pinned by a test) so no published
  payload changes.

### Fixed

- **Backups work on a stock CCU3 again (SY01).** Creating a backup drove
  `/bin/createBackup.sh`, which only OpenCCU and RaspberryMatic ship, and
  failed outright when the script was absent. It turns out the download
  step already did the whole job on its own: it posts to
  `cp_security.cgi?action=create_backup`, and that CGI builds the archive
  itself rather than reading what the script would have produced. A
  failed start is therefore no longer fatal — it now means "this firmware
  has no background-backup helper" and the synchronous CGI path is used
  instead.

- **Charts beyond the raw retention are no longer empty (SV04).** The
  recorder folds raw samples into an hourly and a daily rollup and keeps
  those for 13 months, then purges the raw rows after their much shorter
  retention — but `GET /api/v1/history` only ever read the raw table, so
  any range reaching further back rendered as an empty chart on data that
  was still there. History now picks its source the way the energy
  endpoint already did: a bucket at least a day wide is served from the
  daily rollup, at least an hour wide from the hourly rollup, anything
  finer from raw samples, and each tier is completed by the tail that is
  not folded up yet so the running hour and day are never missing. A
  narrow-bucket query over a range older than the raw data is promoted to
  the hourly tier instead of returning nothing. Rollup rows carry sum and
  count, so the average stays exact across the re-fold rather than
  becoming an average of averages, and min/max keep the peak contract.
  The chosen resolution is reported in a new `X-History-Tier` response
  header (`raw` / `hour` / `day`) — a header rather than a body field
  because the response is a bare array and the OpenAPI contract is
  additive-only. API version → 3.6.0.

- **The per-package coverage gate passes again.** `internal/client/transport/jsonrpc`
  had drifted to 95.9 % against its 96 % floor, failing every `integration`
  workflow run since 0.46.0 — the gate averages per-function percentages,
  so the small `bidcos*` coercion helpers added back then pulled the mean
  under the floor. Three long-untested client methods
  (`GetAuthEnabled`, `GetHTTPSRedirectEnabled`, `ListInterfaces` — all
  three now wired into the system info above) and the coercion helpers
  gained direct tests, putting the package at 99.7 %.

### Changed

- **Dependencies refreshed.** Go: `modelcontextprotocol/go-sdk` 1.6.1 →
  1.7.0, `modernc.org/sqlite` 1.54.0 → 1.55.0 (plus its `libc`). SPA:
  Playwright 1.61.1 → 1.62.0, `@testing-library/jest-dom` 6 → 7,
  `@lucide/svelte` 1.25 → 1.27, Svelte 5.56.6 → 5.56.8, `svelte-check`
  4.7.3 → 4.7.4. The Playwright container the e2e workflow pins moved to
  `v1.62.0-noble` in lockstep — it had drifted behind the package version
  — and every visual baseline was regenerated on both platforms against
  the new renderer. TypeScript stays on 6.x: `svelte-check` supports
  TypeScript 7 only through a dual install behind an
  `--tsgo-experimental-api` flag, which is not a basis for a blocking CI
  gate.

## [0.51.0]

### Added

- **WS verbs for the add-on self-updater.** `addon_update.check` and
  `addon_update.install` (operator role) join the existing
  `addon_update.state_changed` broadcast, so WebSocket clients can
  drive the self-update without a REST round trip (API 3.4.0,
  additive). Both are fire-and-forget: progress and outcome arrive on
  the broadcast; on platforms without the self-update capability the
  commands stay unregistered.

## [0.50.1]

### Fixed

- **Released CCU add-on installs identify as add-on again.** The release
  pipeline packages the prebuilt standalone binaries into the CCU add-on
  tarball, so the build-time add-on stamp was never set — every released
  add-on install reported "Standalone", which suppressed the new
  self-update capability (ADR 0057) **and** the CCU-delegated
  authentication default (ADR 0043) that add-on installs were meant to
  get. `build.IsAddon()` now also detects the add-on at runtime from the
  executable's install path (`/usr/local/addons/openccu-loom/`), healing
  existing installs on their next update — one more manual WebUI install
  of this version, then the self-update card and the HA update entity
  appear.

## [0.50.0]

### Added

- **The CCU add-on can update itself** — on OpenCCU / RaspberryMatic,
  where the firmware ships `/bin/install_addon` (ADR 0057). The whole
  mechanism is capability-gated (`addon_self_update`): on other
  platforms no button, no endpoint, no entity exists. A "check for
  updates" button, a boot-delayed check, and a periodic check (default
  every 24 h with random jitter; interval via
  `addon_update.check_interval`, background checking off via
  `addon_update.enabled=false`) watch the project's GitHub releases; installing downloads
  the add-on package, verifies its SHA256 against the release
  checksums, stages it and drives the firmware installer — the daemon
  restarts, no CCU reboot. Surfaces: REST
  `GET /api/v1/system/addon-update` + `POST …/check|install`
  (API 3.3.0), WS broadcast `addon_update.state_changed`, an MQTT
  Home Assistant `update` entity, and a card in the system settings.
  The add-on tarball now embeds `.sha256` files so the firmware
  verifies the archive a second time.
- **mDNS announces the configured CCUs.** The `_openccu-loom._tcp` TXT
  record gains `ccus=<sn1>,<sn2>,…` — the 10-character short serials of
  the configured CCUs, sorted, re-announced at runtime as serials
  resolve or centrals are adopted/removed (the `centrals` count hint is
  refreshed on the same trigger; it was silently stale after live adopt
  before). Deliberate reversal of ADR 0021's no-serials TXT decision —
  see ADR 0058; `GET /api/v1/system/ccu` stays the authoritative
  post-auth source.

## [0.49.3]

### Added

- **Areas: room groupings above CCU rooms.** Rooms (per central) can be
  assigned to operator-defined areas — a floor, a shed, a terrace roof —
  managed in the rooms/functions administration (create, rename, delete,
  assign; one area per room, reassigning moves it). The device list,
  overview, alarm sensor/output pickers (tabs and wizard), and the group
  editor gain an area filter. New REST surface `GET/POST /api/v1/areas`,
  `PUT/DELETE /api/v1/areas/{id}`, `PUT /api/v1/areas/{id}/rooms`
  (API 3.2.0, additive); assignments persist in the daemon's database —
  the CCU itself knows nothing of areas.

### Changed

- The Matter schema snapshot now pins matter.js v0.17.7 (was a v0.17.5-alpha
  commit). No cluster IDs, cluster revisions, attribute IDs or device-type
  revisions changed between the two — the regenerated
  `internal/north/matter/schema/` output is byte-identical, only the
  provenance stamp moves.

### Fixed

- **Matter secure sessions now reject replayed message counters below the
  first counter observed.** The MRP duplicate-detection window anchors on
  the first counter a session receives; for an encrypted session every
  counter below that anchor must count as already-received (Matter Core
  Spec §4.5.4.1 replay protection), but the window started empty and
  accepted up to 32 of them once each. Unsecured / PASE traffic keeps the
  empty seed — there duplicate detection is not a security control and a
  legitimately reordered message just below the first one seen has to stay
  acceptable. Mirrors the per-variant `initialBitmap` in matter.js
  `MessageReceptionState.ts`.
- **A commissioner that restarts its message counter is no longer locked
  out.** The unsecured / PASE reception window measured how far back a
  counter sat using the rule for encrypted sessions, so a peer that
  rebooted and resumed counting from a low value looked like a
  four-billion-message replay: every packet was dropped until it climbed
  back past the pre-restart maximum, and because these windows are kept
  per source node across sessions, the lockout outlived the handshake that
  triggered it. The window now folds distances the way matter.js
  `MessageReceptionStateUnencryptedWithRollover` does — only the 32
  counters directly below the maximum count as "behind", anything further
  back rolls the window forward onto the restarted trajectory. Secure
  sessions keep the strict reading.

## [0.49.2]

### Changed

- **The alarm system's armable unit is now called a "zone" everywhere** —
  formerly "area", which is being freed up for the upcoming room-grouping
  concept above CCU rooms. This is a deliberate breaking rename across the
  whole surface (no external API consumers yet): REST paths
  (`/api/v1/alarm/zones…`), request/response fields (`zone_id`,
  `zone_name`), WebSocket commands/broadcasts, MQTT alarm topics
  (`<base>/alarm/<zone>/…`, pseudo-zone `master`), the `hmcli` output, the
  SPA (UI texts DE "Zone"/EN "zone"), and the SQLite schema — a
  data-preserving migration renames the tables/columns in place,
  including the ids inside stored code bindings.

### Added

- **The alarm setup wizard's sensor and output candidate lists are now
  searchable, filterable, and sortable.** The outputs step gains the
  free-text search the sensors step already had; both steps add room and
  function ("Gewerk") filters plus a name/room/model sort, and each row
  shows the device's model label and room/function assignments (output
  rows resolve the model label from the live device inventory). The
  Sensors and Outputs tabs' add-drawers gained the same enrichment. The
  output-candidates REST DTO now carries the channel's `rooms` and
  `functions` (API 3.1.0, additive).

### Fixed

- **The same siren (or any output/sensor) can now be enrolled in more than
  one alarm area.** Enrolling a channel in a second area failed with an
  opaque 500: clients derived the row id from the channel key, so the
  second area's row collided with the first area's PRIMARY KEY and the
  whole outputs/sensors replace aborted — the area was created but its
  sirens never attached. Row ids are now an intra-area concern: the
  server keeps a client id only when it round-trips one of the SAME
  area's rows and mints a fresh UUID otherwise (cross-area collisions and
  in-payload duplicates included), and the wizard no longer sends
  channel-derived ids at all.
- **Shared outputs stop only when the last area releases them.** With one
  siren enrolled in two areas, silencing or finishing area A used to
  switch the device off even while area B was still alarming. The output
  manager now tracks per-channel demands: a stop for a channel another
  area still claims is deferred (logged as
  `alarm.output_stop_deferred_shared`), and the device turns off with the
  last demanding area. Demands are in-memory — after a daemon restart
  every stop proceeds, which is the safe direction.
- **The alarm setup wizard configures sensors and outputs inline.** Steps 2
  and 3 used to link to the sensors/outputs tabs — which require an existing
  alarm area, while the wizard only creates the area on Finish. First-run
  users following the wizard's own links landed on an empty page whose only
  action led back to a freshly reset wizard. The wizard now embeds real
  pickers: security-device sensor candidates (search + show-all) in step 2,
  output candidates in step 3; Finish applies everything in order (create
  area → sensors → outputs) and a partial failure keeps the created area id
  so a retry updates instead of duplicating the area.
- **Wizard progress survives navigation.** Step, area name, delays, and
  selections moved into a store — leaving the wizard and coming back no
  longer silently restarts it at step 1.
- **Honest wizard steps.** Step 5 no longer claims PIN codes ship "in a
  later release" (the Codes tab exists); the trigger-time input now caps at
  the engine's 600 s ceiling instead of accepting values the engine silently
  clamps at runtime.
- **Alarm tabs distinguish "loading" from "no areas".** The sensors,
  outputs, policies, and codes tabs showed the "no alarm areas yet" empty
  state while the area list was still loading — and permanently if the
  fetch failed. They now show the shared loading and error (with retry)
  surfaces and only report "no areas" after a successful, genuinely empty
  load.

## [0.49.0]

### Changed

- **Sysvar/program markers now steer Home Assistant's enabled-by-default.**
  The marker-derived `enabled_default` flag (config `sysvar_markers` /
  `program_markers`) was computed, surfaced on REST and the raw MQTT config
  plane — but never reached the HA discovery payloads, so it had no effect in
  Home Assistant. Sysvar and program discovery now carries
  `enabled_by_default`, matching the reference stack's entity-registry
  default: with markers configured, entries whose CCU description matches a
  marker arrive enabled (internal entries via the `INTERNAL` marker); without
  markers every sysvar/program entity arrives disabled and the operator
  enables the ones they want per entity. HA applies the hint only when an
  entity is first added to its registry, so already-discovered entities on
  existing installs keep their current enabled/disabled state.

## [0.48.9]

### Fixed

- **Custom-DP `unique_id`s are channel-level again.** The REST summary
  (`GET …/cdps`) and the WS `custom_data_point.state_changed` payload stamped
  the parameter-level routing key (`…_state`, `…_level`,
  `…_set_point_temperature`), but aiohomematic keys custom data points by
  their primary channel alone. A consumer seeding its entity registry from the
  summary (the HA drop-in) therefore minted keys that no longer matched the
  aiohomematic twin — switching backends would re-create every custom entity
  (climate, switch, cover, lock, siren) instead of migrating it. Both surfaces
  now stamp the channel-level key; calculated data points keep their
  parameter-level keys.

### E2E

- **`godevccu-e2e` mirrors virtual-receiver writes onto the state channel.**
  Real HmIP actuator firmware aggregates the virtual-receiver group onto the
  `…_TRANSMITTER` channel; without the mirroring, consumers that read state
  there (aiohomematic's custom data points) never see a command take effect.
  The driver now replicates successful `<FAMILY>_VIRTUAL_RECEIVER` writes onto
  the sibling `<FAMILY>_TRANSMITTER` channel via the `OnSetValue` hook.

## [0.48.8]

### Fixed

- **The group editor shows members by name again.** 0.48.7 added daemon-resolved
  member names, but the editor built its picker from the type's suitable-members
  list and, for a member not in that list, kept only the address — so a group's
  current members that the CCU reports as already-grouped bare device addresses
  (no channel suffix) rendered as raw addresses in the picker and the selected
  tray. The editor now carries the daemon-resolved `device_name` / `channel_name`
  / `rooms` from the member row into that fallback, so those members (including
  the wall thermostat that never resolved) show their names. SPA-only.

## [0.48.7]

### Added

- **The heating-group overview shows member device names.** Each member in the
  overview now resolves to its device/channel name and room from the live device
  model instead of a bare address; a member the model does not know still falls
  back to its address. Members addressed by a bare device address (no channel
  suffix, as some CCU heating-group members are — e.g. a wall thermostat) now
  resolve too, via a device-address fallback shared with the member picker. The
  `GroupMemberEntry` gains `device_name` / `device_model` / `channel_name` /
  `rooms`; API version → 2.56.0.
- **The group editor's "operate only via group" toggle carries an inline help
  text** explaining that the group's devices can then only be operated together
  through the group, not individually.

### Fixed

- **Heating groups no longer leak into the pairing inbox.** The CCU's inbox
  query returns every not-yet-configured object, which includes the virtual
  backing device of a heating group (an `INT`-prefixed address on the
  VirtualDevices interface). Those are managed through the group flow and can
  never be accepted as pairing candidates, so a group would appear in the inbox
  and then fail to accept. The daemon now filters CCU-internal virtual devices
  out of the inbox, so they never surface as pairing candidates.
- **Accepting a stale inbox entry returns 404 instead of 502.** When the
  targeted device is no longer waiting in any central's inbox (it settled or was
  removed on the CCU), `POST /devices/{addr}/accept` now answers `404 Not Found`
  and drops the stale entry from the inbox view, rather than surfacing an
  upstream-failure `502`. API version → 2.56.0.

## [0.48.6]

### Security

- **Complete the remote-proxy open-redirect hardening** (CodeQL
  `go/bad-redirect-check`). 0.48.5 gated the final `Location` write, but three
  helper functions in the rewrite path (`rewriteLocation`, `stripPathPrefix`,
  `rebase`) still split their input on a bare leading-slash test, which the
  scanner flags because such a check alone does not exclude the `//host` /
  `/\host` open-redirect forms. The leading-slash decision now lives solely in
  the complete `isLocalPath` gate: `rewriteLocation` forwards only genuine
  local paths, `stripPathPrefix` collapses an unsafe `//…` / `/\…` remainder to
  the base root, and `rebase` composes the browser base without any leading-slash
  test on the (possibly upstream-controlled) reference. No user-visible change.

## [0.48.5]

### Security

- **Harden the remote-proxy redirect rewriting against open redirects**
  (CodeQL `go/bad-redirect-check`). The proxy now emits a rewritten `Location`
  only when the computed target is a genuine local path — a single leading `/`
  not followed by another `/` or a `\` — so a value such as `//host` or `/\host`
  can never be turned into a protocol-relative redirect off-site.

### Changed

- **Dependency refresh.** Go and SPA dependencies bumped to their latest
  compatible releases.

## [0.48.4]

### Fixed

- **Heating groups created from the UI are no longer empty.** Saving a group
  with members created it with **zero members** — HMServer's `group/save`
  handler expects `assignedDevicesIds` as a JSON-encoded *string*, but the
  daemon sent a native JSON array, which HMServer silently drops. The daemon
  now sends the stringified form, matching the CCU WebUI. Root cause captured
  from the WebUI and live-confirmed both ways (native array → 0 members,
  stringified → members assigned).
- **Live WebSocket 403 through the remote-proxy add-on.** When the SPA was
  reached through a chained proxy (e.g. Traefik → remote-proxy → daemon), the
  `/api/v1/events` handshake still failed the WebSocket same-origin check
  because the browser's external Origin cannot be reconciled with the daemon's
  internal Host across the chain — leaving the live indicator flickering. The
  origin check now skips any handshake that carries an `Authorization` header
  (Bearer/Basic), mirroring the CSRF middleware: such a request is not a CSRF
  vector (CSRF rides ambient cookie auth, and a browser cannot set an
  Authorization header on a WebSocket handshake), and the remote-proxy injects
  a Bearer token while stripping the cookie. Cookie-authenticated handshakes
  keep the origin protection.

## [0.48.3]

### Fixed

- **Live connection no longer reconnects every minute.** The SPA never answered
  the server's WebSocket heartbeat (`{op:"ping"}`), so on an idle page the
  daemon's 60 s read deadline expired and tore the socket down — the live
  indicator flickered once a minute. The client now replies with a pong and
  stays connected.
- **Live WebSocket behind a chained proxy.** The remote-proxy add-on
  overwrote `X-Forwarded-Host` with its own upstream host, so a browser reaching
  the daemon through Traefik → remote-proxy still failed the daemon's WebSocket
  same-origin check. The proxy now preserves the browser-facing
  `X-Forwarded-Host` (and `X-Forwarded-Proto`) a trusted upstream already set.

### Changed

- **Group member picker shows config-pending devices.** A device that still has
  a pending configuration (`CONFIG_PENDING`) cannot be assigned to a group yet;
  instead of hiding it, the picker now lists it greyed out with a "config
  pending" hint so it is obvious why it is not selectable. Current members stay
  selectable regardless. `GET /groups/suitable-members` /
  `groups.suitable_members` carry a new `config_pending` flag (API 2.55.0).

## [0.48.2]

### Security

- **Bump `github.com/getkin/kin-openapi` to v0.144.0** (GHSA-r277-6w6q-xmqw,
  CRITICAL). The OpenAPI request-validation middleware embedded in the daemon
  used a version whose `ValidationHandler.Load()` could fail open; the release
  image scan flagged it. No API or behaviour change — dependency bump only.

## [0.48.1]

### Fixed

- **Live WebSocket no longer disconnects behind a reverse proxy.** The
  `/api/v1/events` handshake rejected the SPA with `403 "websocket origin not
  allowed"` in a reconnect loop ("live disconnected", log spam) whenever a
  proxy rewrote the request Host to the internal upstream: the same-origin
  check compared the browser's Origin only against that internal Host. It now
  also accepts the proxy-forwarded external host from `X-Forwarded-Host`, which
  a browser cannot forge on a WebSocket handshake, so the SPA reconnects
  without weakening the cross-site protection.

### Changed

- **Heating-group member picker redesigned for scale.** The group editor's
  member list — previously a flat, unfiltered list of raw channel addresses —
  now groups candidates by device, offers a search box (name / room / type /
  serial) and room / only-selected filters, a tri-state per-device checkbox
  (a multi-channel actuator is one expandable row), and a live selection tray
  showing exactly which members are chosen. The daemon enriches each candidate
  with device/channel name, model, room and function from the live model
  (`GET /groups/suitable-members`, `groups.suitable_members`; API 2.54.0), so
  the picker stays usable across hundreds of channels.

## [0.48.0]

### Added

- **Heating-group administration (GR02).** Create, edit, and delete Homematic
  heating groups from the SPA — the "Heizungsgruppen" view gains New / Edit /
  Delete controls and a group editor (type picker → member picker → name +
  "operate only via group" toggle). Groups are managed through the CCU's own
  HMServer jpages endpoints, authenticated with Loom's live JSON-RPC session
  (ADR 0055), so the group-wiring matrix is computed by the CCU and never
  re-derived in Go. A new group runs the CCU's two-step create → save flow;
  because the save's HTTP response is slow and can time out even on success,
  the daemon confirms completion by polling `getHeatingGroupList` rather than
  the save reply. The write path was verified live against a real HmIP heating
  group (create → save → delete round-trip).
  - REST (admin-gated, audited, API 2.53.0): `POST /groups`,
    `PUT /groups/{id}`, `DELETE /groups/{id}`, plus read helpers
    `GET /groups/types` and `GET /groups/suitable-members`. The same commands
    are available over WebSocket (`groups.create/update/delete/types/
    suitable_members`).
  - The group's virtual device now carries a clean CCU label (the bare group
    name), from which the CCU derives its channel names as `<name>:<n>` — the
    save no longer writes a bogus `INT0000000` serial suffix (GR03). Creating or
    editing a group also applies the "operate only via group" flag to each
    member device on the CCU (GR04), and a device can be assigned to a heating
    group straight from the inbox accept dialog (GR05). The clean label and the
    operate-only write were both verified live on the real CCU.
- **Central links now show their live active state.** The central click-event
  panel (device detail → Direct links) reads the CCU's report-value-usage
  metadata (`getMetadata(<channel>, "reportValueUsageData")`) and marks each
  eligible channel active / inactive, plus a device-wide active count — so an
  operator can tell at a glance which button channels currently forward their
  press events to the central, without guessing from the enable / disable
  buttons. The state is authoritative (it reflects changes made in the CCU
  WebUI or after a reboot), not a daemon-side approximation. When the backend
  has no metadata read path the panel falls back to eligibility only
  (`active_state_known: false`).
  - REST/WS: `GET /devices/{addr}/central-links` gains `active_state_known`,
    `active_channels`, and a per-channel `active` flag (API 2.52.0).

## [0.47.4]

### Changed

- **Room / function assignment is now a searchable combobox with inline
  create.** The device- and channel-level room / function editors in the
  device detail view and the inbox accept dialog no longer take a
  comma-separated free-text string. Instead each shows the current
  assignment as removable chips and a search field that filters the CCU's
  existing rooms / functions in an inline dropdown; when the typed text
  matches no existing entry, a "+ create" action adds a brand-new room /
  function on the spot (`POST /rooms` / `POST /functions`) and assigns it.
  The dropdown is a plain inline list (not a portaled popover) and flips
  above the field when the viewport has more room there — so it stays
  reliable on touch devices and inside the scrolling accept dialog. Each
  add / remove persists immediately with an optimistic update that rolls
  back on a CCU error.

### Fixed

- **Diagram series channel picker did not work on iPad.** The channel and
  value steps used a custom portaled dropdown whose taps iOS Safari did not
  register; they are now native selects (the reliable iOS wheel picker),
  matching the device step's touch-friendly behaviour.

## [0.47.3]

### Changed

- **Guided diagram series editor.** Composing a diagram series is now a
  searchable device → channel → value picker instead of four free-text fields:
  the central and interface are derived from the picked device and the label is
  auto-suggested, so operators no longer type raw addresses / parameter strings.
  The value dropdown lists only numeric (plottable) data points and shows their
  unit.
- **Energy view is hidden when history recording is off.** The Energy nav item
  now follows the same opt-in-history gate as Diagrams; the page already showed
  a "history required" state on direct navigation.
- **"Edit on device" from the direct-links overview opens the links tab.** The
  action now deep-links to the device's direct-links sub-tab
  (`#/devices/{addr}?tab=links`) instead of just opening the device on its
  default channels view.
- **Signal-quality list links the device.** The device name links to the device
  detail, matching the firmware list.

### Fixed

- **Umlauts in program condition/activity summaries rendered as `�`.** A
  program's summary lists the device and channel names it references (e.g.
  `Wassersensor Sp�le`, `L�ftung Aus`). Those names come from the ReGa script
  layer, which UriEncodes ISO-8859-1 object names; after `url.QueryUnescape` the
  high byte was raw Latin-1 (invalid UTF-8) and rendered as U+FFFD. The ReGa
  field decoder (`decodeRegaField`) now transcodes a non-UTF-8 result from
  ISO-8859-1 to UTF-8, so `Sp�le` becomes `Spüle` (an already-valid UTF-8 value
  passes through untouched). Sysvar descriptions and channel addresses from the
  same ReGa path share the fix. The JSON-RPC client gained the same defensive
  transcode for any method that returns a Latin-1 body.

## [0.47.2]

### Added

- **Per-channel visibility and operation lock (G12).** An operator can now hide
  a channel from the operation surfaces (data-point list, MQTT, Matter) and
  lock it against control writes, per channel, from the device-detail channel
  editor. `GET`/`PUT /api/v1/devices/{addr}/channels/{no}/flags` back the two
  toggles (`hidden`, `locked`); a locked channel rejects VALUES writes with
  `423`, while reads and MASTER/config edits are unaffected. The overrides are
  daemon-owned (SQLite `channel_flags`, no CCU write) and re-applied across
  reconnects; `ChannelSummary` carries `hidden`/`locked`. API version 2.51.0.

### Fixed

- **Heating groups now show up in the UI even when the CCU returns boolean
  group properties.** A real CCU serialises `FORBID_SINGLE_OPERATION` in
  `groups.gson` as a JSON boolean, not the string the reconstructed schema
  assumed. The group parser typed the whole property map as `map[string]string`,
  so the boolean failed to unmarshal and the entire heating-group list came back
  empty — the "Heizungsgruppen" view showed nothing. The parser now decodes each
  property lazily and tolerates both the boolean and the string form.
- **Toggling Basic/Bearer auth now flags a required restart.** The
  `north.rest.auth.basic_enabled` / `north.rest.auth.bearer_enabled` gates are
  wired into the auth middleware once at boot, so a live change silently did
  not take effect — an operator who enabled Bearer auth saw injected tokens
  still rejected with no hint that a restart was needed. Both paths are now in
  `restartRequiredPaths`, so the config editor shows the restart badge.

## [0.47.1]

### Fixed

- **Brand logo missing behind Home Assistant Ingress / the remote app.** The
  `BrandMark` component built its SVG `src` as a root-absolute `/app/…` path,
  which resolves against the Home Assistant origin instead of the Ingress proxy
  prefix, so the wordmark 404'd and did not render under Ingress or the remote
  proxy. The path now carries `ingressBase()` like every other SPA asset. The
  unused `mark` variant also pointed at a non-existent file and now references
  the shipped `mark-loom.svg`.

## [0.47.0] — unreleased

### Added

- **Per-channel room and function assignment.** `PATCH
  /api/v1/devices/{addr}/channels/{no}` now accepts `rooms` and
  `functions` (alongside `name`), and the new WebSocket commands
  `device.set_channel_rooms` / `device.set_channel_functions` mirror it.
  The CCU assigns rooms and Gewerke per channel, not just per device;
  the device-detail view gains per-channel editors, and
  `ChannelSummary` now exposes the full `rooms` array. The assignment
  ReGa scripts resolve their target rename-proof (name lookup, then an
  address scan), so a renamed device or channel no longer silently
  fails to update.
- **HmIP teach-in without internet (SGTIN + key).** The HmIP install
  mode gains the keyserver-less LOCAL flavour: entering a device's SGTIN
  and key from its label opens a pairing window restricted to exactly
  that device. `POST /install-mode/interfaces` and the
  `install_mode.enable` WebSocket command accept `sgtin` + `key`; the
  values are normalised server-side (including the Base32 label-form key
  conversion). The endpoint additionally accepts `central` (multi-CCU
  disambiguation) and `device_address`.
- **Virtual-remote key simulation.** The CCU's virtual remotes
  (HM-RCV-50 / HMW-RCV-50 / HmIP-RCV-50) render as a key grid with short
  and long press buttons in the device detail; writable press slots on
  other devices become interactive too. A press is a single boolean
  write of `PRESS_SHORT` / `PRESS_LONG`, and a cell flashes on the CCU's
  echoed press event.
- **Restore stored device configuration.** A new `POST
  /api/v1/devices/{addr}/config/restore` endpoint and
  `device.restore_config` WebSocket command re-transmit the centrally
  stored configuration (every channel's MASTER paramset plus link
  peerings) to a device after a factory reset. Admin-gated,
  audit-logged, and surfaced as a device-detail action for devices whose
  interface supports it (`config_restore_supported`); HmIP-RF and
  BidCos-RF only.
- **Guided device replace.** `GET
  /api/v1/devices/{addr}/replace-candidates` lists the paired devices a
  new (inbox) device may replace, and `POST /api/v1/devices/{addr}/replace`
  performs the swap (matching WebSocket commands
  `device.replace_candidates` / `device.replace`). The CCU migrates
  direct links, teams and ReGa references; the old device is unpaired.
  Offered from the inbox for BidCos devices only (HmIP does not support
  it); admin-gated and audit-logged.
- **Wired-bus device search.** `POST /api/v1/install-mode/search` and the
  `install_mode.search` WebSocket command trigger the BidCos-Wired bus
  scan (`searchDevices`) and return the count found; the found devices
  join the inbox for acceptance. Offered from the inbox for a BidCos-Wired
  interface.
- **Per-device communication test.** `POST /api/v1/devices/{addr}/test`
  and the `device.test` WebSocket command run the CCU's per-device
  communication / function test (a radio test frame + ACK, the same test
  the CCU inbox runs) and report pass / fail. Surfaced as a "Test" action
  in the device detail; radio interfaces only
  (`communication_test_supported`).
- **Channel team assignment.** `GET
  /api/v1/devices/{addr}/channels/{no}/team-candidates` lists the team
  channels a channel may join, and `PUT
  /api/v1/devices/{addr}/channels/{no}/team` assigns it (or resets to the
  default team); matching WebSocket commands `device.team_candidates` /
  `device.set_team`. Backed by `setTeam` / `listTeams`; BidCos-RF and
  HmIP-RF only (`team_supported`). A per-channel team picker appears in
  the device detail.
- **Named multi-series diagrams.** A new Diagrams view (`#/diagrams`)
  lets operators compose and save charts that overlay several
  measurement-history data points — across devices and CCUs — as private
  or shared definitions. Backed by CRUD REST routes
  (`GET/POST/PUT/DELETE /api/v1/diagrams`) over a new `diagram_configs`
  table (owner + visibility, series document validated for a non-empty
  central); each chart's data comes from the existing history feature.
  The whole surface (nav + page) is gated on the opt-in history-recording
  feature via a new `history.v1` info capability, so it stays hidden when
  recording is off.
- **Test a direct link at the device.** A new `POST
  /api/v1/devices/{addr}/links/test` endpoint (and the operator WebSocket
  command `links.activate_paramset`) triggers the receiver's LINK
  paramset for a sender — the CCU config dialog's "test link" /
  simulate-keypress probe (short or long press). It maps to XML-RPC
  `activateLinkParamset` and **physically actuates the receiver**, so the
  schedule/link profile editor's new "Test (short/long press)" buttons
  confirm before firing, and the endpoint is operator-gated. CUxD /
  Homegear interfaces report `501`. Read-only `links.test_profile` (the
  embedded profile preview) is unchanged.
- **Universal-light weekly-program colour preserved.** Editing a
  universal light's (HmIP-RGBW / DRG-DALI / LSC) or HmIP-BSL weekly
  program no longer discards the per-switch-point colour / effect. The
  `HUE_SATURATION_COLOR_TEMPERATURE_EFFECT_TYPE` / `_VALUE` and
  `OUTPUT_BEHAVIOUR` fields are carried through the schedule DTO as opaque
  values, glued to their switch point's slot so they survive reorder /
  insert / delete deterministically (previously they could be orphaned or
  inherited by an unrelated slot). The schedule editor shows a read-only
  colour badge per switch point on colour-capable devices
  (`color_capable`). Editing the colour value is a follow-up (its packed
  layout needs live-device validation).
- **System-variable usage overview + delete warning.** A new `GET
  /api/v1/sysvars/{name}/usage` endpoint (and the read-only
  `sysvars.usage` WebSocket command) lists the CCU programs that
  reference a system variable, via the variable's native
  `DPEnumUsagePrograms()` — the same call the CCU WebUI uses — enriched
  from the hub's program registry (localized name, canonical id, internal
  flag, active state). The SPA's delete-confirmation now warns which
  programs will be affected before removing a variable; the lookup is
  best-effort and never blocks the delete.
- **Per-datapoint recording toggle.** When measurement history is
  enabled, the device-detail history tab gains a "Record" switch that
  forces recording on or off for one specific data point, overriding the
  parameter-name glob policy; "reset to default" clears the override. New
  `GET`/`PUT /api/v1/history/recording` endpoints back it, a sparse
  override table lives in the history database, and the recorder consults
  an in-memory overlay on its hot path (no per-event disk read). The
  numeric and live-provenance guards still apply — a force-on cannot
  record a non-numeric or non-live value. Overrides are purged on
  device-remove / central-remove alongside the measurements.
- **Global direct-links overview.** A new `GET /api/v1/links` endpoint
  (and the read-only `links.list_all` WebSocket command) aggregates
  every direct link across all centrals into one flat list; each link
  now carries its owning `central_name` and `interface_id`. The daemon
  reads the interface-wide roster with one empty-address `getLinks` per
  (central, interface) — the same call the CCU WebUI uses — rather than
  a per-channel scan. A `?central=<name>` query scopes to one CCU. A new
  "Direct links" SPA view (`#/links`) renders the roster with search and
  a per-CCU filter, deep-linking each row to its device for editing.

### Fixed

- **Intermittent `403 insufficient role` behind the remote-ingress proxy.**
  A request reaching the daemon with both an injected admin Bearer token and a
  browser session cookie was silently downgraded to the session's (lower) role:
  the session resolver overwrote the already-resolved Bearer identity instead of
  deferring to it. Admin/operator actions (`/diagnostics/*`, `/admin/*`,
  `/auth/tokens/*`, switching a device) then failed with "insufficient role"
  whenever a stale lower-role session cookie was present, while reads still
  worked — and it came and went as the session expired or the `SameSite=Lax`
  cookie rode along inside the Home Assistant ingress iframe. The session
  resolver now yields to any Bearer/Basic identity resolved earlier (matching
  the ingress-passthrough precedence), so a deliberate token always wins. As
  defence in depth the remote-ingress proxy also drops the competing daemon
  session cookie on requests where it injects an instance token (no-token
  login mode keeps the cookie). Fixes the WebSocket handshake too, which pinned
  the downgraded role for the connection's lifetime.
- **Role matching when creating a direct link.** The linkable-channels
  picker ignored the requested direction and offered every link-capable
  channel for both roles. It now intersects the raw CCU
  `LINK_SOURCE_ROLES` / `LINK_TARGET_ROLES` tokens — exactly like the CCU
  WebUI — so a `sender` source only lists candidates that can receive
  and a `receiver` source only lists candidates that can send. The roles
  are carried onto the channel model during ingest, so the filter needs
  no CCU roundtrip (it removes one `getLinkPeers` call per candidate).
  Response shape unchanged; the `receiver` candidate set is now
  correctly narrower.
- **Heating schedule for classic BidCos thermostats.** HM-CC-RT-DN and
  HM-CC-RT-DN-BoM store their single week profile as prefix-less
  `ENDTIME_*` / `TEMPERATURE_*` keys directly in the device-level MASTER
  paramset — with no `P<n>_` prefix and no dedicated schedule channel —
  which the schedule resolver, parser and writer previously did not
  recognise, so the schedule tab reported "not supported". The daemon
  now resolves such devices to their device-root paramset, reads the
  bare schema as the single profile P1, and writes it back with
  prefix-less keys (a prefixed write would have silently no-op'd on the
  CCU). No API contract change — a previously `404` schedule read now
  returns `200`.

## [0.46.0] — 2026-07-22

### Added

- **Heating-group listing (read-only).** A new `GET /api/v1/groups`
  endpoint and `groups.list` WebSocket command surface the Homematic
  heating groups (HmIP / BidCos) configured on each CCU, grouped by
  central. The listing is read from each CCU's `groups.gson` via the
  `CCU.getHeatingGroupList` JSON-RPC method and joined into a typed
  shape (id, name, type, "operate only via group" flag, and member
  addresses). Omitting the `central` query aggregates over all
  centrals; a non-CCU or offline central contributes an empty roster
  rather than failing the request. This is the first slice of the
  group-administration work; creating, editing, and deleting groups
  will run through the CCU jpages proxy (see the new
  [ADR 0055](docs/adr/0055-groups-jpages-proxy.md)) and lands
  separately. API version 2.42.0.
  The device-detail Links tab now carries an expandable info hint that
  explains why an HmIP button can look dead — without press-event
  forwarding many buttons never send their events to the CCU or
  OpenCCU-Loom — and that enabling forwarding raises the device's radio
  duty cycle and battery consumption. Mirrors the CCU channel-config
  "info" dialog. Fully localized (de + en); no API change.
- **Central-link (press-event forwarding) toggle can now target a single
  channel.** `POST` / `DELETE /devices/{addr}/central-links` (and the
  WebSocket commands `central.create_links` / `central.remove_links`)
  accept an optional `channel` (a channel address such as `ABC0000001:4`);
  without it the whole device is switched as before, with it only that one
  channel is touched — mirroring the CCU channel-config dialog, which
  scopes the switch to the opened channel. `GET /devices/{addr}/central-links`
  (and `central.links_status`) now also return a per-channel `channels`
  list (address, number, eligibility). The device-detail Links tab keeps
  the device-wide switch and adds a per-channel switch for each eligible
  channel. REST `APIVersion` 2.36.0.
- **"Determine" button in the channel MASTER editor.** Determine-capable
  MASTER parameters — the ones the firmware spells out as
  `operations="read,write,determine"`, i.e. OPERATIONS bit `0x08` — now
  render a "Determine" button that reads the parameter's current value
  straight from the device and stages it into the editor, dirty-tracked
  and undoable exactly like a manual edit (an error surfaces as a toast; a
  spinner shows while the read is in flight). The channel ui-schema now
  exposes the capability per parameter (`operations.determine`), and a new
  additive endpoint `POST
  /devices/{addr}/channels/{no}/paramsets/{key}/determine` backs the
  button — a read, so it carries no edit-lock token. The REST route shares
  the registry-resolved backend path with the existing WS
  `paramset.determine` command; the SPA uses REST because its WebSocket
  channel is event-only. Mirrors the CCU WebUI's per-parameter "Determine"
  link (`config/ic_ifacecmd.cgi`).
- **Secured-transmission (AES) toggle per channel.** The channel MASTER
  configuration panel now shows a dedicated "Secured transmission" row
  with a switch for every channel whose MASTER paramset carries
  `AES_ACTIVE` (the per-channel AES signing flag). The switch reads its
  state straight from the raw paramset — independent of the visibility /
  un-ignore store, since `AES_ACTIVE` carries the `internal` ui-flag and
  is filtered out of the normal schema — and writes through the existing
  edit-locked `PUT /devices/{addr}/paramsets/MASTER` path. Enabling asks
  for confirmation first, warning that secured transmission raises the
  channel's radio load and battery drain; disabling applies immediately.
  No REST or ReGa change: writing `AES_ACTIVE` on the interface is the
  authoritative mechanism (the CCU WebUI's ReGa `setTransMode` only adds
  a WebUI-cache refresh the daemon neither uses nor needs).
- **Firmware update duty-cycle warning + CCU firmware download.** The
  device firmware-update endpoint (`POST
  /devices/{addr}/firmware/update`) now checks the device's radio
  interface against the per-interface duty-cycle poll and, when it is
  saturated (≥ 80 %), returns an advisory `duty_cycle_warning` in the
  202 body — it never blocks the update, mirroring the CCU WebUI's
  non-blocking warning. The firmware overview surfaces the same warning
  in the update-confirm dialog. A new admin-only endpoint `POST
  /api/v1/system/firmware/download` (audited) tells a CCU to fetch a
  firmware image from a URL onto the central so it can be staged for
  installation; the CCU system-update panel gains a download field for
  it. REST `APIVersion` 2.40.0.
- **Per-radio-interface duty cycle and carrier sense on the Diagnostics
  page.** BidCos radio interfaces now surface their transmit duty cycle
  and receive carrier-sense load directly in the interface table, so
  pure-BidCos installations and radio-LAN gateways — which have no
  device that exposes `DUTY_CYCLE` — finally show their radio budget.
  A new per-central poll (60 s, pure JSON-RPC, no radio traffic) reads
  the CCU's `Interface.listBidcosInterfaces` and caches the result;
  `GET /api/v1/interfaces` gains optional `duty_cycle` and
  `carrier_sense` fields (percent, absent when the CCU does not report
  them, e.g. for HmIP-RF, which the device-level data points still
  cover). The SPA renders each as a threshold badge — green, yellow
  from 60 %, red from 80 %. REST `APIVersion` 2.39.0.
- **First-time configuration when accepting an inbox device.**
  `POST /devices/{addr}/accept` (and the WebSocket command
  `inbox.accept`) now take an optional body — `name`,
  `include_channels`, `rooms`, `functions` — applied to the device right
  after it is accepted out of the inbox: the name is persisted to the
  CCU (optionally cascading to every channel), and the room / function
  (Gewerk) assignments go through the ReGa hub-writer. An empty or
  omitted body keeps the plain accept-only behaviour, so the change is
  backward compatible. The follow-up steps are best-effort but never
  swallow errors: if the accept succeeds and a follow-up step fails the
  response is a 502 whose title states the device was already accepted,
  so only the configuration needs re-applying. The SPA's "Accept" action
  now opens a dialog with a name field, room and function multi-selects
  (populated from `GET /rooms` and `GET /functions`) and a
  "rename channels" toggle; leaving everything blank just accepts. REST
  `APIVersion` 2.32.0.
- **Channel rename over REST + WebSocket.** A new
  `PATCH /devices/{addr}/channels/{no}` endpoint (and WebSocket command
  `device.rename_channel`) renames a single channel; the SPA exposes it
  via a pencil affordance on each channel in the device detail view.
- **Delete device with factory-reset / force options and a dependency
  warning.** `DELETE /devices/{addr}` gains optional `reset` and `force`
  query flags that map onto the CCU `deleteDevice` delete bitmask —
  `reset` also factory-resets the device during removal, `force` removes
  an unreachable device even when the CCU cannot complete the handshake
  (both default to off, preserving the plain-unpair behaviour). The SPA
  remove action becomes a small options dialog (unregister-only vs.
  factory-reset radio plus a force checkbox) that warns up front when
  direct links or CCU programs still reference the device. A backend
  without a pairing concept (CUxD) now answers 422 instead of 502. REST
  `APIVersion` 2.31.0.

### Changed

- **Toggling press-event forwarding now asks for confirmation and reports
  through a toast.** The device-detail Links tab used to enable/disable the
  central-link forwarding immediately and show the result in an inline
  banner. Every toggle (device-wide and per-channel, enable and disable)
  now runs through the shared confirm dialog first — disable uses the
  destructive variant and warns that CCU-side programs may consume the
  press events and that after disabling neither CCU programs nor
  OpenCCU-Loom will receive them — matching the CCU WebUI's yes/no safety
  question in both directions. The touched/skipped/failed result is now a
  toast (a warning toast when any channel failed), and the inline banner is
  gone. SPA-only; no REST surface change.

### Fixed

- **Deactivating a central link now clears PRESS_LONG too.** Removing the
  press-event forwarding for a channel (`DELETE /devices/{addr}/central-links`
  and the `central.remove_links` WebSocket command) used to zero only the
  `PRESS_SHORT` usage counter, leaving a lingering `PRESS_LONG` counter that
  could keep the device forwarding long-press events to the CCU after the
  user switched the link off. Teardown now issues a second
  `Interface.reportValueUsage` for `PRESS_LONG` (ref-counter 0) per channel,
  matching the CCU WebUI's own deactivate behaviour so the device-internal
  direct link is fully removed. Activation is unchanged (it still raises only
  `PRESS_SHORT`). No REST surface change.
- **Device and channel renames now persist to the CCU.** A rename used
  to mutate only the in-memory model and was silently lost on the next
  device reload. `PATCH /devices/{addr}` and the WebSocket
  `device.rename` command now dispatch to the CCU's `Device.setName` /
  `Channel.setName` JSON-RPC methods (resolving the ReGa ISE-ID first),
  and propagate the CCU error instead of swallowing it. A backend
  without JSON-RPC (Homegear, CUxD) answers 422 rather than pretending
  success. `PATCH /devices/{addr}` and `device.rename` gain an optional
  `include_channels` flag that also renames every channel with the
  `"<name>:<channelNo>"` pattern (the CCU WebUI convention); the SPA
  rename dialog offers it as a toggle, on by default. REST `APIVersion`
  2.30.0.
- **Reboot a CCU from the daemon.** A new admin-only endpoint
  `POST /api/v1/system/ccu/{central}/reboot` reboots one CCU host: it runs
  a ReGa script (`reboot_ccu`) that persists the CCU's state
  (`system.Save()`) and then triggers `/sbin/reboot`. The southbound
  connection to that central drops for the duration of the reboot and
  recovers automatically once the CCU is back (the readiness gate re-runs
  the bring-up). The SPA surfaces this as a new **CCU maintenance** card
  under Settings → System with a per-central reboot button behind the
  shared destructive-confirm dialog and a toast result. This reboots the
  CCU hardware — distinct from `POST /system/restart`, which restarts the
  OpenCCU-Loom daemon itself. REST `APIVersion` 2.33.0.
- **Permanently suppress service messages.** "Disable" now durably
  suppresses a service message's channel parameter on the CCU via
  `Interface.suppressServiceMessages` instead of merely acknowledging it
  once — the device stops raising the message until the suppression is
  cleared. `POST /api/v1/service-messages/{id}/disable` resolves the
  message's channel + service parameter and suppresses it; the new
  `GET /api/v1/service-messages/suppressed` lists the active suppressions
  (reconciled against each CCU's live
  `Interface.getSuppressedServiceMessages`); and `POST
  /api/v1/service-messages/unsuppress` (body `channel` + optional
  `parameter` / `interface`, optional `central` query) clears one. The
  matching `service_messages.suppressed` / `service_messages.unsuppress`
  WebSocket commands mirror the REST surface. This closes a long-standing
  gap: the client-layer suppression path
  (`InterfaceClient.SuppressServiceMessage` /
  `GetSuppressedServiceMessages`) and the `HubCoordinator` seam existed
  but were never wired — central bring-up now installs the suppressor so
  the calls reach the CCU. The Config UI Messages view gains a "Hide
  permanently" action per service message (confirm dialog + toast) and a
  new "Suppressed" tab listing active suppressions with a "Restore"
  action. REST `APIVersion` 2.32.0.
- **Bulk acknowledge for service and alarm messages ("Acknowledge
  all").** New `POST /api/v1/service-messages/ack-all` and
  `POST /api/v1/alarm-messages/ack-all` endpoints (and the matching
  `service_messages.ack_all` / `alarm_messages.ack_all` WebSocket
  commands) clear every quittable message of a class in a single CCU
  pass and return the number acknowledged (`{"acknowledged": n}`). Both
  accept an optional `central` query parameter to scope the operation to
  one CCU; when omitted every registered central is acknowledged. Two
  new ReGa scripts drive the wire pass — service messages honour the
  per-message writability gate, alarm messages are acknowledged
  unconditionally, mirroring the CCU WebUI's "acknowledge all" loop. The
  Config UI Messages view gains an "Acknowledge all" button per tab,
  shown only when acknowledgeable messages exist, guarded by a confirm
  dialog and reporting the acknowledged count via a toast. REST
  `APIVersion` 2.32.0.
- **Rename a direct link after it has been created.** A link's name
  and description were previously settable only at creation time. The
  daemon now exposes the CCU's `Interface.setLinkInfo` call end to end:
  a new `LinksDomain.SetLinkInfo` (interface resolved from the sender
  device, like `ListLinks`/`AddLink`, with an audit `link_update`
  entry), the REST endpoint `PATCH /api/v1/devices/{addr}/links`
  (body `{sender_address, receiver_address, name, description}`), the
  WebSocket command `links.set_info`, and a per-row rename action
  (pencil) in the SPA device-links view with a small name/description
  editor. Name and description are written verbatim, so either field
  can be cleared with an empty string. REST `APIVersion` 2.35.0.
- **Take the sender's current brightness into a motion-detector link
  threshold.** When a direct link's sender channel reports a brightness
  or illuminance reading (`BRIGHTNESS`, `ILLUMINATION`, …), the LINK
  paramset editor now shows a one-click helper on the
  `SHORT_COND_VALUE_LO`/`_HI` (and `LONG_` variant) condition-threshold
  fields that fills them with the sender's live value — so the operator
  no longer has to read the brightness off elsewhere and type it in.
  The value follows the sender's live pushes, and the edit is tracked
  and undoable like any manual change. SPA-only, mirroring the CCU
  WebUI's `config/ic_md.cgi` "Aktuelle Helligkeit übernehmen" helper.
- **"Pending wakeup" hint after link operations on battery devices.**
  A battery-powered device only applies a new/removed direct link or a
  written LINK paramset the next time it wakes up (a button press, a
  cyclic wake interval); mains devices apply it immediately. The device
  detail DTO now decodes the CCU `RX_MODE` bitmask into a new `rx_mode`
  object (`always`/`burst`/`config`/`wakeup`/`lazy_config` flags), and
  after a successful add-link, remove-link, or LINK-paramset save the
  SPA checks the affected device(s) and — when one carries a
  `wakeup`/`lazy_config` rx mode — replaces the plain success toast with
  an info toast reminding the operator the change transfers only on the
  next wakeup. Mirrors the CCU WebUI's
  `config/ic_ifacecmd.cgi` `cmd_ShowConfigPendingMsg`. REST `APIVersion`
  2.35.0.
- **Delete a CCU program.** `DELETE /api/v1/programs/{id}` removes a
  program from the CCU (`dom.DeleteObject`, via a new `delete_program`
  ReGa script) and drops the local mirror once the call lands. It is
  admin-gated and irreversible — parity with `DELETE /devices/{addr}` —
  returns 204 on success, 404 for an unknown id, and records an audit
  entry (`program_delete`). The optional `central` query parameter scopes
  the target when several CCUs are configured. The WS `programs.delete`
  command (admin role) exposes the same operation, and the Config UI's
  program table gains a Delete action guarded by the shared destructive
  confirm dialog with a result toast. REST `APIVersion` 2.34.0.

- **Run a program only when its condition is met.**
  `POST /api/v1/programs/{id}/execute` gains an optional body field
  `check_conditions` (boolean, default false). When true the CCU
  evaluates the program's "if" condition — via a new
  `execute_program_conditional` ReGa script — and runs the program only
  when the condition is currently satisfied; the response now reports
  `executed` (always true for an unconditional run, false for a
  condition-checked run whose condition was not met). When false (the
  default, and when the body is omitted) the program runs unconditionally,
  preserving existing behaviour. The WS `programs.execute` command gains
  the same `check_conditions` argument and returns `executed`. The Config
  UI's execute-confirmation dialog adds an "Only run when the condition is
  met" toggle and the result toast now distinguishes executed from
  not-executed. Mirrors OpenCCU's program-execution-with-condition-check
  WebUI extension. REST `APIVersion` 2.34.0.

- **Program list shows the rule at a glance: condition + activity
  summary and last execution.** `GET /api/v1/programs` (and the single
  `GET /api/v1/programs/{id}`) gain two nullable fields,
  `condition_summary` and `activity_summary` — a compact,
  language-neutral rendering of each program's root rule. Object names
  come from the CCU (channel and system-variable names); comparison and
  logical operators render as symbols (`==`, `>=`, `<=`, `>`, `<`,
  `&&`, `||`) and activities as `name := value`, so the strings need no
  translation. They are built by extending the
  `get_program_descriptions` ReGa script with a bounded root-rule
  traversal (one extra ReGa round-trip, capped at ~200 characters with
  an ellipsis). The Config UI program table adds Condition and Activity
  columns (collapsible on narrow viewports) plus a Last-executed column.
  REST `APIVersion` 2.34.0.
- **Reveal system-internal programs at runtime — no config change
  needed.** The daemon now always loads internal programs (`Tmp_*`,
  `prgEnergyCounter_*`) into the hub and filters them at delivery, so
  they can be shown on demand. `GET /api/v1/programs` and the WS
  `programs.list` command gain an optional `include_internal` override;
  when omitted the central's `include_internal_programs` config remains
  the default (hidden), preserving existing behaviour for MQTT and other
  clients. The Config UI program table adds a "Show system programs"
  toggle (off by default, persisted locally), mirroring the CCU WebUI's
  footer button. REST `APIVersion` 2.34.0.
- **A system variable's channel assignment is writable.** `POST /sysvars`
  and `PATCH /sysvars/{name}` now accept an optional `channel_address`
  ("ADDR:idx", the CCU "Kanalzuordnung"). The address is resolved to the
  channel's ReGa ise id via `Interface.getIseIDByAddress` before it reaches
  the CCU; `CreateSysvar` no longer hard-codes `chn_id: -1`, and the
  `update_system_variable` Rega script gained `oSv.Channel()`. On PATCH the
  field is tri-state (omit = leave untouched, empty string = clear the
  assignment, an address = assign it); an address the CCU cannot resolve is
  rejected with 422. The system-variable create and edit dialogs offer a
  searchable device/channel picker to set or clear the assignment. REST
  `APIVersion` 2.31.0.

- **Alarm system variables can be created.** `POST /sysvars` now accepts
  `value_type: "ALARM"`, provisioning a binary, acknowledgeable alarm
  line on the CCU. The Rega `create_system_variable` script backs it
  with an `OT_ALARMDP` object (not the `OT_VARDP` every other type uses)
  and wires up the binary alarm condition, so the new variable reads,
  writes and acknowledges like any hand-created CCU alarm. The
  system-variable create form offers ALARM in its type dropdown, and the
  handler now validates `value_type` against the known create set
  (rejecting read-side wire codes such as `LOGIC`/`NUMBER`/`LIST`).

- **System variables can be renamed, and carry a description from
  creation.** `PATCH /sysvars/{name}` now accepts an optional `name`
  field that renames the variable in place (the CCU-side rename runs
  through the `update_system_variable` Rega script; the local cache is
  re-keyed the moment the call lands so the new name shows before the
  next periodic refresh). `POST /sysvars` gained an optional
  `description` field, so a variable's help text can be set at creation
  instead of only via a follow-up patch. The system-variable editor
  surfaces both: the edit dialog has a rename field, the create form a
  description field.

- **System variables expose their value labels and visibility / logging
  flags.** The sysvar catalogue read from the CCU (`SysVar.getAll`) now
  parses the fields it already ships — the binary `valueName0`/
  `valueName1` state labels (for `LOGIC`/`ALARM` variables) and the
  `isVisible` / `isLogged` flags. They surface as `value_name_0`,
  `value_name_1`, `is_visible` and `is_logged` on `SysvarSummary` (REST
  `GET /sysvars` and the WS `sysvars.list`). `POST /sysvars` accepts the
  two value labels (empty adopts the CCU's own `false`/`true` defaults; a
  custom label routes creation through the Rega script since the native
  `SysVar.createBool` has no label parameter), and `PATCH /sysvars/{name}`
  accepts the two labels plus tri-state `is_visible` / `is_logged`
  toggles (backed by the CCU-side `Visible()` / `DPArchive()` settings).
  In the SPA, a boolean sysvar's switch now shows the operator-visible
  state label instead of a bare toggle, and the edit and create dialogs
  offer the value-label fields plus the visibility and logging switches.

### Fixed

- **The system-variable edit dialog now patches the value list of real
  CCU list variables.** The dialog gated its value-list field on
  `value_type === "ENUM"`, but the daemon delivers the CCU wire type
  `LIST` — so editing the options of an existing list variable silently
  did nothing. The dialog now keys off the real wire types
  (`LOGIC`/`LIST`/`FLOAT`/`INTEGER`/`STRING`/`ALARM`), showing the
  value-list field for `LIST` and the min/max fields for the numeric
  types.

- **Logic and alarm system variables are now flipped with a switch.**
  The system-variable list and the favorites view rendered a switch
  only for the `BOOL` alias, so the CCU's real boolean sysvar types —
  `LOGIC` (a plain logic value) and `ALARM` (an alarm flag), by far the
  most common — fell through to the free-text field and could only be
  changed by typing `true`/`false`. Both views now derive the inline
  control from one shared dispatch: `BOOL`/`LOGIC`/`ALARM` render a
  switch (a two-entry label list on an alarm variable no longer hides
  the toggle), a labelled `LIST` renders a dropdown, and a label-less
  `LIST` renders a numeric-index field. Read/write path only — the
  edit dialog is unchanged. REST `APIVersion` 2.31.0.

## [0.45.0] — 2026-07-20

### Changed

- **REST is the single naming authority: `translated_name` now also
  carries the collapsed name for label-omitted data points.** When
  `label_omitted` is true (the "primary parameter" marker), the
  data-point summary's `translated_name` no longer arrives empty — it
  holds the channel-level collapsed name (channel name plus
  multi-channel `chN` marker, device prefix stripped; empty only when
  the collapse reduces to the device name alone). REST consumers such
  as `openccu-loom-client` no longer need any client-side entity-name
  composition. The MQTT discovery plane is unchanged (`name: null` on
  omitted labels). REST `APIVersion` 2.29.0 (2.28.0 + 2.29.0 across the two naming PRs).
- **Custom data points ship their entity names too.** The CDP summary
  gains `translated_name` (the fully composed channel-level display
  name: custom channel names verbatim, `ch<no>`/`vch<no>` group
  markers for derived names, locale-aware postfix labels for button
  locks; empty on the device-name collapse) and `parameter_name` (the
  untranslated marker/postfix portion). Composition lives in the new
  `device.BuildCustomDataPointName`, mirroring the reference's
  `get_custom_data_point_name` — including the marker digits following
  the channel-name suffix and the single-primary collapse applying
  only on the primary channel.

## [0.44.3] — 2026-07-20

### Fixed

- **Multi-channel postfix no longer overrides unique custom channel
  names.** The `ch<no>` postfix for parameters that exist on multiple
  channels of a device is now only appended when the channel name alone
  does not identify the channel — i.e. for device-derived names, names
  following the `<name>:<no>` scheme, or when several channels providing
  the same parameter share the same custom name. A channel with a unique
  custom name (e.g. a status channel named `<sub device> Status`) keeps
  its clean data point name. Mirrors aiohomematic 2026.7.10.

## [0.44.2] — 2026-07-19

### Fixed

- **OpenCCU-Loom Remote: instance names accept mixed case.** The
  add-on schema and the proxy rejected names like `OttoLoom`
  (lowercase-only slug); the constraint is relaxed to
  `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$` — the name stays a single URL
  path segment, but capital letters are fine everywhere it is used
  (mount path, tile label, DOM id, cookie scope). Surrounding
  whitespace (a classic paste mistake) is trimmed instead of rejected,
  by the schema and the proxy alike.

## [0.44.1] — 2026-07-19

### Added

- **New HA add-on: OpenCCU-Loom Remote** (ADR 0054) — an ingress proxy
  that brings the Config UI of one or more **remote** OpenCCU-Loom
  instances into the Home Assistant sidebar, without running a local
  daemon. Multiple instances mount under one panel (overview page with
  live status tiles — health, version — when more than one is
  configured); an optional per-instance API token logs HA admins into
  the remote UI without a second credential, and without a token the
  remote login page is proxied through (session cookies are scoped per
  instance). Upstreams may be `http://` or `https://` (per-instance
  `tls_insecure` for self-signed certificates); WebSockets and the
  HA theme bridge work through the proxy. Ships as
  `ghcr.io/sukramj/openccu-loom-remote-ha-{arch}` from
  `packaging/ha-addon/openccu-loom-remote/`; the
  `openccu-loom-remote` binary also rides in the release image and
  archives.

## [0.44.0] — 2026-07-19

### Changed

- **CCU metadata now ships as the versioned go-openccu-data module**
  (ADR 0053) — the extracts (translations, easymodes, profiles,
  curated overlays, device semantics) come from
  `github.com/SukramJ/go-openccu-data` instead of a hand-synced
  `internal/ccudata/embedded/` copy. Regeneration is automated on
  every upstream release (repository_dispatch → module PR → dependabot
  bump); `make bump-ccudata` is the manual fallback, the old
  `update-ccu-data`/`refresh-ccudata` targets, `MANIFEST.json` and the
  drift-check script are gone. The migration surfaced (and upstreamed)
  loom-only curation: the 0.42.9 BWTH parameter labels now live in the
  shared source of truth.

- **Doorbells ring — with the shared curated model list** — press
  events of the doorbell devices now follow Home Assistant's doorbell
  contract: the discovered `event` entity announces (and the event
  topic fires) HA's standard `ring` type instead of `press_short`
  (mandatory for doorbell event entities from HA 2027.4; mirrors the
  reference stack's ring-event fix). Other press types are unchanged.
  The doorbell classification now comes from the upstream data
  package's new curated `device_semantics` extract — one source of
  truth shared with the reference stack — and gains the classic
  `HM-Sen-DB-PCB` alongside `HmIP-DBB` and `HmIP-DSD-PCB`.
  **Breaking:** consumers of the raw channel `/event` topic of these
  three models receive `{"event_type":"ring"}` for the short press
  now; HA automations triggering on the entity's `press_short` event
  type must switch to `ring`.

## [0.43.4] — 2026-07-19

### Fixed

- **MCP write tools rejected every real write** — `set_datapoint` and
  `write_paramset` checked device ownership by passing the raw target
  address to the per-device lookup; real writes always target a
  channel (`ADDR:n`), so the lookup missed and every write failed with
  `device … belongs to central ""`. The guard now strips the channel
  suffix before the ownership lookup — and still rejects a
  `central_name` that does not own the device. Found live on the first
  agent-driven switch attempt; the previous test only covered a
  device-level address.

## [0.43.3] — 2026-07-18

### Fixed

- **The MCP endpoint rejected every credential** — the `/mcp` mount
  wrapped its handler only in the auth chain's `Require` step, which
  checks the identity the `Resolve` middleware places in the request
  context; `Resolve` never ran on that mount (it sits outside the REST
  router's middleware stack), so every request — valid Bearer token,
  Basic auth, or none — got `401 no valid credentials`. The mount now
  resolves credentials before requiring them, restoring the documented
  "same auth chain as REST" behaviour. Pinned by a mount-level
  regression test.
- **Claude Desktop connection example fixed** — the documented
  `claude_desktop_config.json` snippet put `"Authorization: Bearer …"`
  into `args`, which Claude Desktop mangles (spaces in args); mcp-remote
  then fell into an OAuth discovery flow and failed with `/register`
  404s. The example now passes the full header value through an
  environment variable (`Authorization:${AUTH_HEADER}`), which
  mcp-remote expands itself.

## [0.43.2] — 2026-07-18

### Added

- **Localised siren tone / pattern / soundfile pickers** — the
  candidate extras now carry parallel `*_labels` lists translated
  through the curated CCU value translations (APIVersion 2.27.0), so
  the ASIR tone and pattern dropdowns show "Frequenz steigend" instead
  of `FREQUENCY_RISING`; raw wire values remain what is stored.
- **Optical pattern on the acoustic-siren card** — the acoustic
  activation writes the optical selection in the same atomic device
  paramset, so the acoustic card now exposes the optical dropdown too
  (previously only optical-siren and alarm-light cards had it).
- **Stale enrollments are visible and repairable** — pre-0.43
  enrollments could point at a non-siren channel (the old dialog
  defaulted to `:1`); such rows never fired and, since 0.43.0's save
  validation, block the whole output set with 422 — invisibly. The
  output card now flags an ineligible channel/class pair with an
  explanation and, when the device has exactly one eligible channel,
  offers a one-click channel repair.

## [0.43.1] — 2026-07-18

### Added

- **Sysvar-mirror outputs became configurable — and can target existing
  alarm variables** — the add-output dialog no longer demands a device
  for the sysvar-mirror class: it asks for the central and the variable
  target instead. Either a managed value-list variable (created on the
  CCU automatically, as before — but the name was previously not
  settable from the SPA at all, leaving the enrollment a silent no-op)
  or, new, an operator-owned ALARM-type variable: the mirror then
  writes true while triggered and false otherwise, never creates or
  retypes the variable, and accepts no inbound intents through it. The
  output card now edits the variable name and the allow-disarm opt-in,
  and saving without a variable name is rejected with 422.
- **Notification outputs actually notify now** — the class previously
  had no consumer. A notification output now emits a deliberate,
  per-area, mode-filtered `alarm_panel.notification` event at fire time
  (one-shot, never cancelled by silence): published as a NOTIFICATION
  entry on the area's MQTT alarm event topic, forwarded to outbound
  webhook receivers, and broadcast on the WebSocket alarm_panel topic
  (`alarm.notification`, APIVersion 2.26.0). Each plane can be toggled
  per output (`notify_mqtt` / `notify_webhook`, both default on) from
  the output card; the add dialog no longer demands a device for this
  class.
- **Alarm keyfobs surface first in the remote-key picker** — security
  remotes (HmIP-KRCA and peers) sort to the top of the guided binding
  picker and the KRCA carries an "alarm keyfob" badge; generic wall
  buttons and remotes follow.

### Fixed

- **Remote-key picker found no keys** — the candidate enumeration
  looked for press parameters in the channel's generic-event set,
  which the device pipeline never populates; every remote and
  wall-button key was invisible ("no remote or wall-button keys
  found"). Press parameters are ordinary VALUES data points, and the
  enumeration now checks exactly that. Pinned by a godevccu
  integration test that asserts the HmIP-KRCA surfaces with both
  press parameters.
- **Tone / pattern / soundfile dropdowns could miss their device
  lists** — the output card matched candidates strictly by
  central + channel address; enrollments whose stored central differs
  (older rows may carry an empty one) silently fell back to free-text
  fields. An unambiguous address-only fallback now covers those rows,
  and the ASIR/MP3P ENUM lists themselves are pinned end-to-end by a
  godevccu integration test.

## [0.43.0] — 2026-07-17

### Added

- **Capability-aware alarm output enrollment** — new REST endpoint
  `GET /api/v1/alarm/output-candidates` (APIVersion 2.25.0) derives,
  from the live domain model, which channels can back each
  device-backed output class (acoustic/optical siren, switched siren,
  smoke sounder, alarm light, chirp — including ON_TIME-gated
  switched-siren eligibility) plus the device's real ENUM label lists
  (siren tones, optical patterns, MP3 soundfiles). The SPA add-output
  dialog now lists these ground-truth candidates per class with a real
  channel picker (expert mode keeps the unfiltered device list), the
  tone / optical-pattern / chirp-tone editors offer the device's ENUM
  values instead of free text (e.g. HmIP-ASIR), and MP3-player chirp
  outputs (HmIP-MP3P) get a soundfile picker. Saving an output set now
  soft-validates enrollments: a resolvable channel that cannot carry
  its class is rejected with 422 instead of failing at fire time;
  unresolvable channels (CCU down) still save and remain guarded by
  the fault journal.
- **Guided remote-key bindings (keyfob arming, e.g. HmIP-KRCA)** —
  remote-key alarm codes no longer require a hand-written JSON binding:
  `GET /api/v1/alarm/remote-key-candidates` enumerates every physical
  remote/wall-button key channel (PRESS_SHORT / PRESS_LONG from the
  live model; virtual remotes excluded), and the codes editor gained a
  key picker plus trigger / action / area selects that assemble the
  binding document. Raw JSON remains available as an expert fallback
  (also the path for virtual remote channels).

## [0.42.9] — 2026-07-17

### Fixed

- **Untranslated climate parameters on HmIP-BWTH** — the parameter
  editor showed raw identifiers for `TEMPERATURE_COMFORT_COOLING`
  (the cooling-mode comfort temperature on climate transceivers) and
  `SUPPORTING_WIRED_OPERATION_MODE` (MAINTENANCE channel): the CCU's
  own stringtable never carried them, and the curated translation
  overlay had no gap-fill. Both now resolve in German and English
  ("Komfort-Temperatur (Kühl-Modus)" / "Comfort temperature (cooling
  mode)"), pinned by an overlay regression test.

## [0.42.8] — 2026-07-17

### Fixed

- **Firmware overview froze on the state from daemon start** — the
  daemon read each device's firmware data (installed / available
  version, update lifecycle state) exactly once, at device
  materialisation, possibly straight from the SQLite description cache.
  An update performed at the CCU never surfaced: the overview kept
  offering "Update" for firmware installed long ago, and the RPI-RF-MOD
  placeholder row persisted. Three gaps closed:
  - the periodic firmware polling jobs (`central.firmware_check` hourly,
    plus fast delivery/updating polls while an update transaction runs)
    existed as scheduler slots but were never wired — they run now, and
    the refresh propagates the fresh description fields onto the live
    device models (previously even the WS `firmware.refresh` command
    only updated an internal registry no surface reads).
  - new REST endpoint `POST /api/v1/devices/firmware/refresh`
    (APIVersion 2.24.0) forces the same sweep on demand.
  - the firmware overview's "Reload" button now actually reloads: it
    triggers the daemon-side refresh and re-fetches the per-device
    firmware details, which were previously served from a never-
    invalidated page cache — the button visibly did nothing.
- **Reload/interval audit follow-ups** — a sweep over every reload
  button and periodic-refresh job after the firmware finding:
  - the system-variables page's "Reload" now forces a CCU re-pull
    (`POST /sysvars/fetch`, which existed but was never called by the
    SPA) before reading the list — previously a value just changed at
    the CCU stayed invisible for up to one sysvar-scan interval.
  - the overview's error-retry drops its per-device tile caches and
    re-fetches expanded groups instead of serving the session's first
    detail/CDP snapshots forever.
  - the `hub.metrics_refresh` job is no longer scheduled: its inner
    hook was never wired anywhere, so it fired every 5 minutes as a
    permanent no-op (both existing hub metrics are produced by other
    jobs). All other reload buttons and refresh jobs verified working.

## [0.42.7] — 2026-07-17

### Fixed

- **LIST sysvar published its raw index instead of the label** — the
  CCU delivers every sysvar value as a string, and the scan kept LIST
  (and numeric) sysvar values string-typed instead of parsing them to
  the declared type. The MQTT state topic therefore carried the raw
  index (`0`) while the discovery advertised the labels as enum
  options, so Home Assistant rejected every update ("got '0', allowed:
  Aus, …") and the sensor stayed unknown. LIST values now parse to the
  integer index (resolved to its label on publish), INTEGER to int,
  NUMBER/FLOAT to float; REST/WS mirror the correctly typed value. A
  non-numeric payload still degrades to the string fallback.

## [0.42.6] — 2026-07-17

### Added

- **`alarm.v1` capability token** (#357) — `GET /api/v1/info` now
  advertises `alarm.v1` in `capabilities` whenever the daemon-level
  alarm service is mounted (the same condition that mounts the
  `/alarm` routes). External clients gate their alarm surface on the
  token instead of probing `GET /alarm/panels` for a 404, which cannot
  distinguish a disabled subsystem from an old daemon or a
  reverse-proxy misroute.
- **Per-area code policy on the panel entity** (#358) — the
  alarm-control-panel entity (`GET /alarm/panels`, WS
  `alarm_panel.panels`, and the `alarm.panel_changed` broadcast) now
  carries `code_arm_required` / `code_disarm_required`: the same
  effective per-verb code requirement the MQTT discovery already
  advertises (area code policy AND an applicable enabled pin code
  exists), so REST/WS consumers can prompt for a code upfront instead
  of surfacing the daemon's 403 after the fact. The master aggregate
  carries the any-area-requires union; live code CRUD re-derives the
  flags and pushes changed panels over the broadcast. APIVersion
  2.23.0.

## [0.42.5] — 2026-07-16

### Added

- **Hot-plug: newly paired devices appear without a restart** — a device
  taught in at the CCU while the daemon is running is now materialised
  live from the `newDevices` callback: the device pipeline hydrates
  exactly the new device (paramset descriptions, data points, custom
  data points, initial values, CCU-assigned name via a forced
  device-details refresh), MQTT publishes its discovery + state as soon
  as the model announces it, the Matter bridge reassembles its bridged
  endpoints (debounced, with the PartsList change notified to
  commissioners), and the WebSocket device-lifecycle broadcast continues
  to fire. The ingest dedups against the model and is serialised with
  the interface bring-up, so the CCU's full-inventory re-announcement
  after every reconnect stays a no-op instead of re-reading the whole
  interface. Previously the callback only updated internal registries —
  the device stayed invisible in REST/SPA/MQTT/Matter until the daemon
  restarted. Applies to the XML-RPC interfaces and CUxD (BIN-RPC) alike.

### Fixed

- **All-zero firmware placeholder rendered as an update** — devices the
  CCU has no OTA image for (e.g. the RPI-RF-MOD gateway module, which
  is updated through the CCU firmware itself) report the placeholder
  available version `0.0.0`. The firmware overview treated any version
  difference as a pending update and showed "Update available" /
  "Awaiting transfer to the device" next to 4.4.22 → 0.0.0. The
  placeholder now counts as "no available version": the row reads "Up
  to date", the available column shows "—", and the updates filter +
  summary no longer count the device. The domain gate
  (`GatedLatestFirmware`) applies the same rule, so a `0.0.0` can never
  surface as an installable target on the BidCos path or in the MQTT
  update entity either.

## [0.42.4] — 2026-07-16

### Fixed

- **Firmware overview contradicted itself** — a device whose CCU knows
  a newer firmware that is not yet delivered to the device (HmIP
  `NEW_FIRMWARE_AVAILABLE`, e.g. 1.2.2 installed / 1.4.10 available)
  showed "Up to date" twice, because the status column let the gated
  `update_available` flag (which only says an install can start *now*)
  overrule the real CCU lifecycle state. The status column now renders
  the CCU state ("Update available"), the action column explains
  "Awaiting transfer to the device" instead of claiming currency, and
  the updates filter + summary count include such devices.

## [0.42.3] — 2026-07-16

### Added

- **SPA↔contract path guard** — a new vitest contract test parses every
  URL the REST client constructs and matches it (method + path shape)
  against the OpenAPI contract via the generated types. A client call
  against a path or method the daemon never serves now fails CI instead
  of failing the user at runtime — the class behind the 0.42.2
  link-paramset 404. A full audit of all 184 client calls against the
  217 contract operations found no further mismatches; the existing
  router↔spec walk test already pins the other edge in both directions.

### Fixed

- **Install mode never opened** — `POST /api/v1/install-mode/interfaces`
  (and the per-device teach-in route) failed on every attempt: the
  install-mode writer looked its backend up under the bare interface
  type (`HmIP-RF`) while the registry keys backends by the canonical
  central-prefixed wire ID. The writer now translates to the wire ID;
  the unit-test fixtures register backends exactly like production
  wiring so this class of key mismatch can no longer hide.
- **Log viewer showed `"error": {}`** — the SPA log viewer and
  diagnostic captures passed error attributes through as raw values,
  which marshal to an empty JSON object, hiding the failure reason for
  every logged error (the install-mode failure above was undiagnosable
  from the UI). Error attrs now render as their message string,
  matching the stdout log.

## [0.42.2] — 2026-07-16

### Fixed

- **Link paramset saves from the SPA failed with 404** — the channel
  editor called `PUT /devices/{addr}/link-paramsets/{peer}`, a path the
  API never served (the contract route is
  `/devices/{addr}/link-ps/{peer}`), so every direct-link configuration
  save failed with "resource not found". The client now uses the
  contract path.
- **Percent-encoded path IDs are now decoded centrally** — the REST
  router routes on the percent-decoded path, so every `chi.URLParam`
  yields decoded values. Previously chi handed handlers the raw
  segment whenever a client percent-encoded a path ID (as the SPA does
  for every ID), which broke any endpoint whose IDs carry `:`, `|`,
  `@`, spaces, or non-ASCII — the class behind the custom-DP invoke
  fix, the 0.42.1 alarm test-fire fix, and the link-paramset lookup,
  and still latent in sysvar/room/function/program routes. The two
  per-handler workarounds are retired in favour of the router-level
  guarantee, pinned by a routing contract test.

## [0.42.1] — 2026-07-16

### Added

- **Alarm sensor hold time** — `hold_time` on a sensor now works: an
  activation must persist that many seconds before it counts; clearing
  earlier discards it. Filters twitchy PIRs and doors rattling in wind.
  Never applied to always-on hazard/panic sensors, so a smoke or panic
  alarm is never delayed.
- **Alarm cross-zoning groups** — the sensor `group` field now works:
  sensors sharing a group name only trigger when a second distinct
  member activates within 60 seconds; a lone activation is suppressed
  but journaled (`cross_zone_first_hit`). Kills single-PIR false
  alarms. Both windows are deliberately not restart-persisted
  (seconds-short).
- **Silent-panic flag in the sensor drawer** — panic-class sensors can
  now be marked as silent panic (duress) from the UI; the engine
  support existed but had no editor surface.

- **Alarm SPA help texts** — every alarm tab now opens with a short
  orientation line (what the view controls, how it relates to the other
  tabs), and the complex editors grew inline explanations: the Policies
  page explains every switch (code requirements incl. the automatic
  disarm-code rule, hazard/panic always-on semantics, pre-alarm,
  post-trigger/auto re-arm, schedules), the sensor detail drawer explains
  every behaviour flag, the output cards explain duration/tone/outdoor/
  shared-with-CCU, and the add-output drawer describes the selected
  output class. All texts in both locales. The previously missing
  `alarm.flag.chime` label (the door-chime switch rendered its raw i18n
  key) is fixed.
- **Alarm operator guide** — new `docs/alarm-user-guide.md`: an
  operator-facing walkthrough of the whole alarm system (concepts,
  safety promise, wizard, every tab, every policy and sensor flag,
  integrations), linked from the user guide.

### Fixed

- **Alarm output test fire 404** — `POST /api/v1/alarm/outputs/{id}/test`
  returned `404 Unknown alarm resource` for every output when the client
  percent-encoded the ID's `|`/`:` separators (as the SPA does), because
  the handler compared the still-encoded path segment against the
  enrolled output IDs. The ID is now URL-decoded before the lookup, so
  the SPA's siren/optical test buttons work again.
- **Alarm output editor wrote fields the engine never read** — the
  siren tone field saved `tone` while the driver reads `acoustic_tone`
  (the configured tone silently never played), the chirp card offered a
  single `chirp_chime_tone` the driver does not know (it reads
  `chirp_arm_tone` / `chirp_disarm_tone` / `chirp_tick_tone` — the card
  now exposes exactly those three), and the dimmer-level input accepted
  0–100 while the wire expects 0–1 (now 0–1 with the add-default fixed
  from 100 to 1). Legacy values saved under the old keys are read as
  fallbacks and migrated on the next save.
- **Per-output loud/silent toggle removed** — it wrote a `policy` field
  no engine path reads. Loud vs. silent is a property of the mode /
  hazard / panic output policies (Policies tab), where it already
  works; the dead toggle only suggested a control that did nothing.

## [0.42.0] — 2026-07-15

### Added

- **Native alarm system ("Alarmanlage")** — a complete, local-first
  intrusion-alarm engine inside the daemon, spanning six increments
  (#344–#349) on the concept in `notes/concepts/alarm-concept.md` (#343):
  - **Engine & safety core**: per-area arm-state machines (perimeter /
    full / night / vacation / custom) with real entry delays,
    force/bypass arming, bounded re-trigger cycles, and the full
    restart-restore semantics incl. a restart-loop breaker and a
    clock-plausibility rule. The seven hard safety invariants (S1–S7)
    are contract-tested: every siren activation is finitely bounded and
    budgeted per incident, every stop is verified with critical
    priority (and may probe an open circuit breaker), silence is
    persisted incident-scoped across restarts, reconciliation adopts
    sounding sirens before stopping them, and every degradation is
    journaled instead of swallowed.
  - **Output drivers** for HmIP sirens (ASIR family), plug-in sirens on
    switch/dimmer actuators (device-side auto-off travels with the
    switch-on), smoke-detector sounders (engine-watchdog bounded, with
    battery/group-fan-out caveats), alarm lights, chirps/countdown
    ticks, and an optional CCU sysvar mirror (inbound intents arm-only
    by default). Researched device assumptions in
    `notes/reference/alarm-assumptions.md`.
  - **Surfaces**: REST namespace `/api/v1/alarm` and WebSocket category
    `alarm_panel` (APIVersion 2.22.0), a full SPA section (panel with
    single-tap silence, sensor picker with mode matrix, output
    management, journal, walk test, setup wizard — de/en, all theme
    combinations), Home Assistant MQTT discovery as
    `alarm_control_panel` entities (per area + aggregate master panel,
    daemon-level topics per ADR 0052), and an `hmcli alarm` break-glass
    group. The panel is a first-class model entity so REST, WS, and
    MQTT can never diverge.
  - **Codes & identities**: argon2id-hashed PIN codes with permissions,
    area restrictions, validity windows, rate limiting and lockout;
    duress codes with silent fan-out; per-area code policies reflected
    in the HA discovery (`REMOTE_CODE` command template); keypad
    (HmIP-WKP) and remote (KRCA/KRC4) intents with on-device PIN slots
    kept independent by design.
  - **Always-on hazard & panic classes**, pre-alarm stage, auto-rearm
    after quiet period, door chime, arm schedules with reminders (or
    explicit opt-in auto-arm), and alarm events on the webhook plane
    for user-land escalation chains.

### Changed

- Critical-priority commands may now probe an OPEN circuit breaker once
  (alarm stop path); non-critical traffic keeps the fail-fast shed.
- Repeated keypad press events (`PRESS_*`, `CODE_ID`) are no longer
  suppressed as unchanged values — edge-trigger parameters always
  publish.

## [0.41.0] — 2026-07-14

### Added

- **OIDC role mapping now works with your identity provider's real roles.** The
  `role_claim` setting is finally honored: it reads the configured claim from
  the ID token — a plain string, a string array, or a dotted path into a nested
  object such as Keycloak's `realm_access.roles` — and grants the highest of
  `admin` / `operator` / `viewer` it finds. Previously the field was ignored and
  only a single hardcoded top-level `role` string mapped, so Keycloak realm-role
  and group users all fell back to `viewer`. A new
  [Keycloak setup guide](docs/admin/keycloak-oidc.md) walks through the client,
  redirect URI, and role mapping.

### Security

- **OIDC no longer runs over cleartext.** The identity-provider issuer and every
  endpoint discovered from it must use https (plain http is allowed only on
  localhost). A misconfigured `http://` issuer used to run the whole login —
  including the code exchange and ID-token retrieval — in the clear; such a
  deployment is now refused. Move the issuer to https.
- **Stricter ID-token validation.** The discovery document's own `issuer` must
  equal the configured issuer (RFC 8414 §3.3), and the `azp` (authorized-party)
  claim is now checked — a present `azp` must name this client, and a
  multi-audience token must carry it (OIDC Core §3.1.3.7).

## [0.40.0] — 2026-07-13

### Changed

- **The HA-native visual skin (0.39.0) now covers the whole Config UI, not
  just the primitives and highest-traffic views.** `html[data-skin="ha"]`
  remaps Tailwind's `--color-*` palette scale (the CSS variables every plain
  `bg-slate-500`-style utility resolves through), so every remaining view
  follows the active skin automatically — no per-file sweep needed. Opacity-
  modified utilities (`bg-slate-900/50`) were rewritten to `color-mix()`
  against the same variables since Tailwind inlines those as literals. The
  HA-skin default values were also refreshed to the latest
  `home-assistant/frontend` design tokens (primary `#009ac7`, flat
  shadow-less cards, Roboto body font, refreshed neutral/semantic ramps).
  `data-skin="loom"` (the standalone default) is unaffected. See
  [`notes/concepts/ha-theme-bridge.md`](notes/concepts/ha-theme-bridge.md) §
  "Complete theme coverage via palette remap".

### Fixed

- **OpenCCU-Loom no longer exhausts the CCU's login-session pool.** The daemon
  discarded its CCU session roughly every 90 seconds and opened a fresh one,
  without releasing the old one — on the order of 40 abandoned sessions per
  hour per CCU. Because the CCU's session pool is shared with its WebUI, the
  leak eventually locked operators out of their own CCU with "invalid
  credentials or too many sessions". The cause was a wire-contract mismatch:
  the CCU answers `Session.renew` with the boolean `true` (it extends the
  session in place and does *not* issue a new ID), but the client decoded the
  reply as a session-ID string. Every renewal therefore failed to parse, was
  treated as a dead session, and triggered a re-login. A long-running daemon
  now holds exactly **one** CCU session for its entire life, pinned by a
  contract test.

- **Backup and firmware downloads no longer displace the CCU session.** Both
  authenticate against `cp_security.cgi` by session ID and asked the shared
  JSON-RPC client for a *forced fresh login* first, abandoning the session the
  rest of the central was working with — one burned CCU session slot per
  download. They now renew the existing session instead, which is what the
  code's own comment always said it did.

- **A session the daemon abandons is now handed back to the CCU.** When a
  session really does have to be replaced (CCU reboot, expired session,
  privilege error), the client issues `Session.logout` for the old one instead
  of leaving it to idle out of the pool.

- **A rejected login now engages the existing backoff.** The CCU reports both
  wrong credentials and an exhausted session pool as a JSON-RPC error rather
  than an empty result, which slipped past the backoff and made the daemon
  retry at full speed — turning an exhausted pool into a self-sustaining retry
  storm exactly when it needed to back off.

## [0.39.0] — 2026-07-12

### Added

- **The Config UI has a Home-Assistant-native visual skin.** Settings →
  Appearance gains a "Design" control offering the default OpenCCU-Loom
  teal/slate look or a Home Assistant look. Opened standalone (browser tab,
  not inside HA), the operator's choice is remembered and applied on every
  load. Opened inside Home Assistant via Ingress, the HA skin is applied
  automatically and mirrors the operator's real, live HA theme — including
  any custom theme, not just the built-in ones — via a same-origin bridge
  that reads Home Assistant's own color and light/dark settings and re-syncs
  whenever they change. The sidebar, header, and navigation are identical in
  every context; only the color palette adapts, and the browser tab title is
  left to Home Assistant while embedded. See
  [`notes/concepts/ha-theme-bridge.md`](notes/concepts/ha-theme-bridge.md) for the
  design. The shared UI primitives and the highest-traffic views carry the
  new skin now; a handful of less-visited views are a documented follow-up.

- **A CCU's operational readiness is now visible in the UI.** Many actions
  depend on a CCU having finished its readiness-gated southbound bring-up
  (device list fully loaded, Matter coupling available), but that state was
  invisible — an initializing CCU looked "offline" and its half-loaded device
  list looked "empty". Each central now exposes an explicit readiness phase
  (`waiting_for_ccu` → `loading_hub` → `loading_devices` → `ready`, with a
  per-interface `x/y` progress count) as a new `readiness` object on
  `GET /api/v1/system/ccu` and live over the WebSocket topic
  `central.{name}.readiness`. Readiness is tracked **per central**, so a mixed
  fleet (one CCU ready, another still initializing) is represented faithfully.
- **The SPA surfaces readiness everywhere it matters.** A shared
  `CentralStatusBadge` renders the full state set (Ready / Waiting for CCU /
  Initializing *names* / Initializing *devices x/y* / Offline), so an
  initializing CCU is no longer indistinguishable from an offline one on the
  Fleet view. The device overview shows a "devices are still loading" state
  (plus a per-CCU banner in a mixed fleet) instead of a bare "no devices", and
  auto-refreshes the moment a CCU flips to ready. Matter pairing now explains
  when it is waiting for a CCU (a 503 is no longer collapsed into "disabled")
  and — per the "allow + hint" policy — stays available as soon as at least one
  CCU is ready, so a single stuck CCU never blocks pairing of a healthy one.

## [0.38.0] — 2026-07-12

### Fixed

- **English Home Assistant users saw German MQTT entity names.** Four
  HA-discovery entity names were authored (hardcoded) in the central event
  bridge rather than the MQTT layer, two of them German-only
  (`Zeitplan Kanal N`, `Zeitdauer`), so they leaked German to every locale.
  They now resolve from the i18n catalogues in the daemon's `locale`
  (schedule-switch, combined-level, HS-colour and combined-timer labels).

### Changed

- **All remaining daemon-authored translatable strings now go through the i18n
  catalogues (`internal/i18n/catalogs/<locale>.json`).** Adding a language is a
  single new catalogue file — no Go change. Migrated in this release:
  - The server-rendered `/health` and `/about` pages (headings, table headers,
    overall-status label, version/license/repository labels). The health status
    token stays the raw value for the CSS class; only the visible text is
    localized.
  - The Matter bridged-endpoint NodeLabel channel-number fallback
    (`Kanal N` → localized "Channel N" / "Kanal N").
  - The SPA's two inline-bilingual helper tables (daylight-saving headers and
    time-preset labels) now use the SPA `t()` catalogue instead of hand-rolled
    EN/DE branching.
  What is intentionally NOT localized (documented): REST/WS API contract
  messages (clients localize off the stable code), matter.js-mirrored spec
  names, and log messages (English by convention).
- **Health/diagnostics component notes are now localized.** `health.Sample`
  gains an additive `NoteKey` (i18n key) alongside the existing English `Note` —
  which stays the stable sentinel the health-scoring logic matches on, so
  scoring is unaffected. Static notes (client connected, breaker states,
  recovery, initial-sync) render localized on the SPA diagnostics view, the
  connectivity tooltip and the `/health` page; interpolated notes keep their
  English text. `GET /api/v1/health` components carry a new optional `note_key`
  field. REST API version bumps to **2.18.0** (additive).
- **MQTT-discovery entity names are now localized through the i18n catalogues
  instead of being hard-coded.** Every daemon-synthesised HA entity name — the
  hub entities (system health, connection latency, last-event age, alarm /
  service messages, inbox, system update, per-interface install-mode and
  connectivity) and the per-device schedule / firmware / week-profile
  entities — is resolved from `internal/i18n/catalogs/<locale>.json` in the
  daemon's configured `locale`. Previously these names were a mix of hard-coded
  English and German. Adding another language is now purely a new catalogue
  file; no code change. (Side effect: names follow `locale` now, so e.g. the
  schedule sensor reads "Zeitplan" only when `locale: de`.)
- **Auto-generated CCU counter system variables get a friendly, localized name
  in Home Assistant.** The CCU synthesises per-channel counter variables with
  machine-token names (`svEnergyCounter_<ise_id>_<addr>:<ch>`,
  `svHmIPRainCounter…`, `svHmIPSunshineCounter…` and their FeedIn / Today /
  Yesterday variants); MQTT discovery previously surfaced that raw token as both
  the entity name and the entity_id (e.g.
  `sensor.…_svenergycounter_14007_0001dbe9915be4_6`). They now render with the
  reference integration's friendly name — e.g. "Energiezähler Gesamt" /
  "Energy Counter Total" — plus the matching HA `device_class`, unit
  (Wh / mm / min) and a cumulative `total_increasing` state_class so energy
  counters feed long-term statistics. The stable `unique_id` is unchanged;
  entities discovered before this release keep their old entity_id until removed
  and rediscovered.

## [0.37.0] — 2026-07-12

### Fixed

- **Home Assistant showed every CCU device's parent as an "unknown device" and
  never surfaced system variables (nor their device assignments).** The entire
  hub-discovery plane (system variables, programs, the named central "hub"
  device, alarm/service messages, connectivity, install-mode) is gated on the
  CCU serial. Because the readiness-gated central bring-up resolves that serial
  asynchronously — after the composition root had already stamped the discovery
  builder with a still-empty serial — every hub discovery payload was silently
  skipped, including the only payloads that give the synthetic central device a
  name. Raw state topics (`…/hub/sysvars/<name>/state`, …) were unaffected,
  which is why the plane looked half-alive. The hub publisher now reads the
  serial live from the central's `SystemInformation` and re-publishes hub
  discovery on each central's `CentralSouthboundReadyEvent`, so the central
  device is named (no more "unknown device" parent) and system-variable /
  program entities — including their device links — appear once the CCU's
  serial resolves. No API surface change.

## [0.36.0] — 2026-07-12

### Added

- **System variables honour the CCU's explicit channel assignment and expose
  their device link over REST/WS.** The sysvar scan now reads the channel the
  operator assigned to a variable in the CCU WebUI ("Kanalzuordnung") and uses
  it as the primary device-link source — the existing name-based matching
  (channel address, channel/device `ise_id` in the variable name) remains the
  fallback, so e.g. the CCU's auto-generated
  `svEnergyCounter_<ise_id>_<ADDRESS>:<ch>` variables keep resolving. An
  assignment referencing a device the daemon does not serve falls back to name
  matching. Linked sysvars/programs appear under their physical device in Home
  Assistant both via MQTT discovery (as before) **and now via the REST/WS
  API**: `GET /api/v1/sysvars` / `GET /api/v1/programs` (and the WS
  `sysvars.list` / `programs.list` commands plus the `hub.sysvar_changed` /
  `hub.program_executed` broadcasts) carry new optional `channel` and
  `device_address` fields — absent fields mean the entity belongs on the
  central hub card. Assignment changes are logged
  (`hub.sysvar.channel_assigned`, source `explicit`/`name`/`none`) for field
  diagnosability. REST API version bumps to 2.17.0 (additive).

## [0.35.0] — 2026-07-11

Ingress and dark-mode fixes for the Home Assistant add-on deployment (device
icons were placeholder-only behind Ingress and glaring white in dark mode;
the "Login with OIDC" button 404'd behind Ingress), plus CCU system variables
and programs now linked to their physical device. No REST/WS API surface
changes.

### Added

- **System variables and programs are now linked to their device.** A CCU
  system variable (or program) whose name carries a device or channel
  identifier — the channel address, a channel `ise_id`, or the device
  `ise_id` — is associated with that device, mirroring aiohomematic's
  `channel_lookup.identify_channel`. The identifier must appear as a
  standalone token (bounded by non-word characters), so an `ise_id` of `123`
  no longer matches inside a larger number such as `41234`. In Home Assistant
  MQTT discovery the linked sysvar/program entity now renders on the physical
  device's card instead of the synthetic central hub card. The association is
  a device-population-driven pass: it is (re)established after every interface
  ingest and after each periodic sysvar/program refresh, and is cleared again
  when the referenced device disappears.

### Fixed

- **CCU device icons now load behind Home Assistant Ingress.** The
  device-list card built its icon URL as a hard-coded `/api/v1/…` path,
  which bypasses the Ingress proxy prefix and hits the Home Assistant
  origin — so every icon 404'd and fell back to the generic glyph when the
  daemon ran as an HA add-on (it worked untouched as a CCU add-on, where no
  prefix applies). The URL now carries the Ingress prefix (`apiBase()`),
  like every other REST call. The icon proxy additionally sends the
  central's credentials, so icons also resolve against a CCU with
  authentication enabled when reached off-box.
- **The "Login with OIDC" button works behind Home Assistant Ingress.** The
  login page linked the OIDC start endpoint with a hard-coded `/api/v1/…`
  path, which bypasses the Ingress proxy prefix and hits the Home Assistant
  origin (404) when the daemon runs as an HA add-on. The link now carries the
  Ingress prefix via `apiBase()`, like every other REST URL, so the SSO flow
  is reachable. (Completing the round-trip still requires the operator's
  registered OIDC `redirect_uri` to point at a daemon URL the browser can
  reach — an inherent Ingress/OIDC deployment constraint the SPA cannot
  derive, since Ingress strips its prefix server-side.)
- **Device icons are no longer glaring white tiles in dark mode.** The CCU
  model artwork is monochrome line-art (some ships transparent, some with a
  baked-in white background) and sat on a permanent white plate. In dark
  mode the grayscale art is now inverted and the plate goes transparent, so
  icons sit cleanly on the dark card instead of as bright white squares.

## [0.34.0] — 2026-07-11

SPA design & UX overhaul driven by a structured UI review, plus one backend
data-quality fix. No REST/WS API surface changes.

### Fixed

- **RSSI no-signal sentinels no longer surface as measurements.** The
  ReGa-script boot seed decodes JSON numbers as floats, which bypassed the
  RSSI normalisation (it only handled integer wire types) — devices could
  show "RSSI 128 dBm" until the first radio event. The SQLite value-cache
  restore path had the same gap. All numeric wire types now funnel through
  the sentinel filter, and the SPA additionally masks out-of-range RSSI
  values defensively.
- **Dark mode no longer flashes/strands a white top bar.** The `.dark` class
  was applied after first paint; a pre-paint script in `index.html` now sets
  it synchronously, the header carries an explicit token background, and
  `html`/`body` base colours track the theme tokens.
- **Seven previously undefined `brand-*` colour rungs.** The Tailwind theme
  declared only 4 of the 11 brand shades used in markup; utilities like
  `dark:text-brand-100` silently produced no CSS (unreadable active states
  in the settings navigation, washed-out accents). The scale is now complete.
- **Disabled buttons are recognisably disabled in dark mode** (explicit
  disabled fills instead of opacity-only dimming).

### Changed

- **Brand palette switched from generic blue to the logo's Loom teal** —
  full `brand-50…950` scale derived from Tailwind teal, contrast-shifted so
  white button labels meet WCAG AA (4.5:1); info tones stay blue on purpose.
- **Login, loading and empty surfaces carry a subtle woven signature**
  (reusable `WeavePattern` SVG, loom-thread loader; `prefers-reduced-motion`
  respected), and page titles use a tighter display treatment.
- **Overview tiles**: grid rows no longer stretch short tiles to the tallest
  neighbour; widgets without observed state and without operable controls
  collapse to a compact single-row tile ("No state received yet").
- **Matter exposure table**: rows are grouped per device with a header
  (name + address + row count), platform-dependent emoji state glyphs are
  replaced by registry icons with a legend, disabled checkboxes explain why,
  and the redundant selection column header is gone.
- **Device views**: product photos sit in a neutral tile (no more white
  boxes in dark mode), "Remove device" is a red outline action instead of
  the loudest button in the header, and "CCU Refresh" became a secondary
  "Reload from CCU" action.
- **Channel configurator**: the sub-navigation is a quiet segmented control
  (the active top tab is the only brand-marked nav level), channel tabs are
  visually subordinated, duplicate parameter labels within a group are
  disambiguated (upper/lower threshold qualifiers), and long labels wrap.
- **Settings editor**: value-source dots have tooltips + screen-reader
  labels (bootstrap source is violet now), Go type badges only show in
  expert mode, booleans use the design-system switch, and section panels
  keep a single primary action ("Save").
- **Connection badge**: the disconnected state is amber (red stays reserved
  for real errors) and all states explain themselves via tooltip, including
  the mobile dot-only variant.
- **Empty states** across messages, audit log, signal quality and fleet
  views gained explanatory descriptions; "Un-Ignore" is now called
  "Hidden parameters" / "Ausgeblendete Parameter".
- **Theme can be set in Settings → Interface** (light/dark/system, same
  preference the sidebar toggle cycles).

## [0.33.0] — 2026-07-11

Matter interop hardening. The changes below adopt verified findings from a
systematic comparison against the
[home-assistant-matter-hub](https://github.com/RiDDiX/home-assistant-matter-hub)
project's field experience, with every mechanism mirrored from matter.js HEAD
(not from the compared project).

### Fixed

- **Matter endpoint numbers now genuinely survive daemon restarts.** The
  boot-time topology assembly ran before the readiness-gated CCU device load and
  garbage-collected every persisted endpoint-ID row of a still-loading central —
  on every boot. Endpoint numbers stayed stable only by accidental re-allocation
  order, so any fleet change across a restart renumbered endpoints and broke
  controller-side groups/automations. Vanished-device GC is now gated on the
  per-central `ModelComplete` readiness latch; endpoint numbers stay reserved
  until a device is genuinely removed (mirroring matter.js
  `ServerEndpointStores` number reservation, Matter §9.12.4).
- **One GenericSwitch endpoint per physical button.** All press parameters of a
  button channel (PRESS_SHORT / PRESS_LONG / PRESS_CONT / PRESS_LONG_RELEASE)
  now share a single endpoint with a spec-conformant press-cycle state machine
  (InitialPress → LongPress → LongRelease). Previously each press parameter
  materialised its own endpoint, so long-press releases landed on a different
  cluster than the long press (orphaned by construction) and a held BidCos
  button emitted a LongPress event every ~300 ms. Also fixes press events never
  reaching commissioners from real devices (a dead wiring type-assertion) and
  duplicate events after topology reassembly. **Button devices receive new
  Matter endpoint numbers; Apple/Google re-learn button accessories once after
  the update.**
- **WindowCovering `EndProductType` reports enum-correct values** per cover
  profile — Curtain→CentralCurtain(16), Shutter/Window→RollerShutter(17),
  Awning→AwningTerracePatio(19), Blind→InteriorBlind(10), Garage→RollerShade(0)
  — instead of wrongly reusing the `Type` enum code (which read as
  PleatedShade/LayeredShade/CellularShade/SheerShade) or a fabricated
  GarageDoor value. Controllers cache Type/EndProductType at commissioning, so
  already-paired bridges show the corrected values after a re-sync or re-pair.
- **WindowCovering target/direction for externally started movement.** When a
  cover moves via wall button or CCU program, TargetPosition is now inferred
  from the CCU-reported motion instead of staying stale, a device-side stop
  snaps the target back to the current position, Target attributes are
  proactively reported, and the motion parameter (DIRECTION / ACTIVITY_STATE,
  including the previously unwired HmIP fallback) fires Matter reports — Apple
  Home now shows the correct direction arrow for wall-button movement.
- **Thermostat SystemMode/RunningMode conformance.** HM AUTO (week-program)
  mode no longer reads back as `SystemMode=Auto(1)` on a FeatureMap without the
  AUTO feature — controllers echoing that read value on state sync received
  ConstraintError. Reads clamp to Heat/Cool per the active HEATING_COOLING
  direction, RunningMode uses the Auto-less enum, and cooling-capable profiles
  advertise COOL without AUTO (Matter AutoMode requires dual setpoints +
  MinSetpointDeadBand, which HM single-setpoint devices cannot provide).
- **Controllers no longer see the bridge as unresponsive for minutes after a
  restart.** The bridge now sends a Secure-Channel CloseSession StatusReport
  before dropping a session (stale same-peer CASE eviction, idle-session reap
  — operational sessions without traffic for 5 minutes are evicted on a 60 s
  sweep; controller acks on subscription heartbeats keep live sessions marked
  active — and daemon shutdown, capped at 2.5 s), closes a session immediately
  when the peer sends CloseSession (with subscription cleanup), and resumes
  mDNS broadcast once a peer has no remaining session. Mirrors matter.js
  ExchangeManager / SecureChannelProtocol behaviour.
- **`BasicInformation.SoftwareVersion` is derived from the build version**
  (`major*1_000_000 + minor*1_000 + patch`) instead of a hard-coded `1`,
  keeping it consistent with `SoftwareVersionString` — a divergent pair crashed
  some ecosystem hubs (e.g. Aqara) during bridge synchronisation.
- **`ElectricalEnergyMeasurement.CumulativeEnergyImported` is wire-valid.**
  The attribute was emitted as a bare int64 instead of the spec's
  `EnergyMeasurementStruct` (Matter §2.14.5.2) — typed decoders (chip-tool and
  potentially ecosystem controllers) rejected a plain read with "Wrong TLV
  type"; only the untyped report dump masked it. The value now encodes as a
  struct carrying the mandatory `Energy` field.
- **Matter endpoint readiness is race-free and covers runtime-adopted CCUs.**
  The per-central readiness latch is now queryable and seeded at subscribe
  time (a CCU whose device load finished before the Matter bridge subscribed
  was previously never latched), and centrals adopted at runtime get the same
  readiness + reassemble-on-ready wiring as boot-time centrals.

### Changed

- **Matter schema refreshed to matter.js HEAD (spec 1.5.1).** Cluster
  revisions bumped (AccessControl 3, BasicInformation 6,
  BridgedDeviceBasicInformation 6, GeneralDiagnostics 3, GroupKeyManagement 3,
  BooleanState 3, SmokeCoAlarm 2, Thermostat 11, OccupancySensing 7, and the
  measurement/concentration clusters +1 each), device-type revisions updated
  (Thermostat 6; WaterLeakDetector, WaterFreezeDetector and RainSensor 2), and
  the new spec-1.5.1 read-only attributes were added to the write gate.

- **Leak-class sensors will materialise as ContactSensor (0x0015), not
  WaterLeakDetector (0x0043).** Amazon Alexa is pinned below the Matter-1.3
  detector device types; field evidence shows a single 0x0043 endpoint renders
  an entire Alexa bridge unresponsive. The wire surface is identical (mandatory
  BooleanState), polarity stays non-inverted (leak detected → StateValue=true).
  No classifier emits the leak class yet, so this lands before any moisture
  parameter is wired.
- **WindowCovering slider gestures are debounced.** `GoToLiftPercentage` /
  `GoToTiltPercentage` commands debounce per axis (400 ms gesture-start /
  150 ms active-drag; commands within 1 % of the current position are
  acknowledged without a radio write), so Apple/Google slider drags send one
  settled position to duty-cycle-limited HmIP actuators instead of 5–10
  stuttering intermediate writes. The commanded TargetPosition updates
  immediately; StopMotion / UpOrOpen / DownOrClose cancel pending writes.

### Added

- **Dimmer transitions.** LevelControl `MoveToLevel` / `MoveToLevelWithOnOff`
  now honour `TransitionTime`: a positive value is delegated to the device as
  `RAMP_TIME` in one atomic paramset write (a WithOnOff min-level target ramps
  off via ramp-time off). Null/0 keep the instant path; devices without
  `RAMP_TIME` support fall back to instant.
- **Regression guard for Google Home's LevelControl wire shape.** Google omits
  the `transitionTime` field entirely (not TLV-null); the lenient decode of the
  absent field is now pinned by tests so a future strict-validation pass cannot
  silently break Google Home dimming.
- **Controller-ecosystem caveats documentation** (`docs/user/matter.md`): Alexa
  commissions only on UDP port 5540 (also noted in the `north.matter.listen`
  config help, en+de), Alexa's ~80–100 device cap per bridge, detector device
  types breaking Alexa bridges, and WaterValve being unsupported on Google
  Home/Alexa.

## [0.32.0] — 2026-07-10

### Fixed

- **Power/energy meters on switching devices now push live readings to Matter.**
  A HmIP-BSM (and similar metering switches) reports its POWER / ENERGY_COUNTER
  from a sibling meter channel that the bridge attaches onto the switch's Matter
  endpoint. The endpoint's change-notifier only covered the OnOff cluster, so a
  power/energy change was never pushed to a subscribed controller — the value was
  correct on a read but updated only on demand. The ElectricalPowerMeasurement /
  ElectricalEnergyMeasurement clusters now forward their source's notifier, so a
  meter reading change reaches Apple/Google Home as a proactive report.
- **Matter event subscriptions now establish and deliver events.** A subscription
  naming only events (e.g. a GenericSwitch button press — a momentary control with
  no persisted attribute) was rejected when no matching event had been recorded
  yet. Since a button subscription is necessarily placed before the press, it
  could never receive one. Event-only subscriptions now establish on the requested
  event paths and stay up to deliver future events, matching the reference Matter
  stack.

## [0.31.0] — 2026-07-10

### Added

- **Matter now bridges `valve.Irrigation` as an on/off endpoint.** The
  irrigation valve's primary channel projects its `STATE` onto OnOff /
  OnOffPlugInUnit like any other switch — resolving the ADR 0012 "stays
  MQTT-only" gap. Its redundant group-STATE transmitter and sibling actor
  channels stay folded so the device presents as one Matter endpoint by
  default. See [ADR 0049](docs/adr/0049-matter-one-endpoint-per-device.md).
- **Expert flag `north.matter.expose_secondary_channels`** (default off). When
  enabled, the Matter projection additionally surfaces a custom entity's
  secondary actor channels and its group-STATE channel as their own endpoints,
  for power users who want per-channel control. Matter-only — MQTT,
  HA-Discovery and REST are unaffected.

### Changed

- **The Matter exposure candidate list only shows real entities now.** The
  `GET /api/v1/matter/exposable` collector now applies the same visibility gate
  MQTT / HA / REST use: service, status-validity and overflow parameters
  (`INSTALL_TEST`, `*_STATUS`, `*_OVERFLOW`, `PROCESS`, `CONFIG_PENDING`, …,
  all usage `ignored`) and the raw constituents an aggregating custom entity
  consumes (usage `no_create`) no longer clutter the exposure UI as
  "unmappable" rows. A candidate is now a data point that would be a standalone
  entity elsewhere; un-ignoring a parameter makes it reappear automatically.

### Fixed

- **Matter commissioning no longer spins after CommissioningComplete.** When a
  commissioner (Apple Home, Google Home, chip-tool) finished pairing, the
  commissioning-window auto-close routed through `RevokeWindow`, which disarmed
  the fail-safe via `ArmFailSafeFor(…, 0, …)`. That path treated a zero-second
  arm as "arm for 0 seconds" — immediately expired — so it spawned an expiry
  watcher that fired the expiry hook, which itself calls `RevokeWindow`: an
  unbounded loop that pegged a CPU core and flooded the log. The bridge stopped
  answering the commissioner's post-commissioning reads, so the pairing aborted
  ("could not add accessory"). A zero-second `ArmFailSafeFor` is now a pure
  disarm (no watcher, no expiry hook), matching the cluster-wire disarm
  semantics. Verified end-to-end against a real Apple Home commissioner.
- **Matter thermostat setpoint / mode writes from a controller now take
  effect.** The Climate Thermostat cluster asserted `value.(int16)` /
  `value.(uint8)` on writes, but the bridge's TLV decoder delivers write values
  as `int64` / `uint64`, so every Apple/Google `OccupiedHeatingSetpoint` or
  `SystemMode` write failed with IM status `Failure`. The handler now coerces via
  `cluster.AsInt16` / `cluster.AsUint8` (matching every other cluster), so the
  setpoint and mode actually reach the CCU.
- **External CCU changes now propagate to Matter controllers for every bridged
  device class.** Only generic sensors/switches implemented the
  `MatterChangeNotifier`, so a change made at the wall switch or by a CCU program
  never reached a controller's Subscribe for custom-DP-backed accessories
  (dimmers, thermostats, covers, locks, sirens) — they reflected only the
  commands the controller itself sent. `generic.Float` and every custom endpoint
  class now implement `OnMatterValueChanged`, and a source-walking contract test
  guarantees no future device type reopens the gap. Verified end-to-end against
  Apple Home.
- **Read-only ENUM state now reaches Matter controllers for every affected
  device class.** A custom data point that read a read-only ENUM wire parameter
  through a string-sensor accessor got `nil`: the resolver projects read-only
  ENUM parameters onto a raw-index integer sensor, which the `*Sensor[string]`
  cast never matched, so the projected value stayed unobserved and the Matter
  attribute reported TLV-null forever. This silently disabled DoorLock
  `LockState` (`LOCK_STATE`) and SmokeCOAlarm `SmokeState` / `ExpressedState`
  (`SMOKE_DETECTOR_ALARM_STATUS`) in Apple / Google Home, plus the door lock's
  `DIRECTION`, the RF lock's `ERROR`, the siren sound player's `SOUNDFILE` /
  `DIRECTION`, the fixed-colour light's active `CHANNEL_COLOR` slot, and the
  garage door's `DOOR_STATE` (so a garage never reported a position). The custom
  DPs now read the index sensor and resolve the VALUE_LIST label on demand, and a
  resolver-boundary contract test pins the "read-only ENUM → index sensor"
  invariant so the accessor mismatch cannot recur.
- **Docker image builds again.** The builder stage pinned `golang:1.26.4-alpine`
  while `go.mod` requires Go 1.26.5, so with `GOTOOLCHAIN=local` the image build
  failed at `go mod download` (`requires go >= 1.26.5`). The builder base image is
  bumped to `golang:1.26.5-alpine`, matching `go.mod` and the CI `GO_VERSION`.
- **Matter bridge survives boot-time database contention with a large device
  fleet.** Endpoint-ID assignment runs a read-then-write SQLite transaction;
  under WAL a concurrent boot writer (the device-load pipeline) could fail the
  upgrade with `SQLITE_BUSY` — a case `busy_timeout` does not cover — so the
  bridge logged `matter.bridge.start … database is locked` and never brought up
  its Matter listener. The assignment now retries on a BUSY/locked error with a
  bounded backoff, so bring-up no longer fails when many devices are assembled
  at once.
- **A structurally-incomplete device no longer crashes the Matter exposable
  list.** Enumerating exposure candidates calls promoted accessors on each
  device; a custom device with a nil embedded data point (e.g. a colour light
  whose LEVEL DP never materialised) panicked `GET /api/v1/matter/exposable`
  with a `500`, which in turn left the Matter bridge with no bridged endpoints.
  Candidate collection now isolates each device — a panicking one is logged and
  skipped — so every healthy device still surfaces on the exposable list and in
  the bridge.

## [0.30.0] — 2026-07-09

### Fixed

- **HmIP climate away/party mode now writes the reference wire parameters.**
  `SetAway` on an IP thermostat wrote `PARTY_TEMPERATURE` and a `dd.mm.yy hh:mm`
  time format that the CCU did not honour; it now writes `SET_POINT_MODE=2`,
  `SET_POINT_TEMPERATURE`, and the `PARTY_TIME_START/END` window in the CCU's
  `yyyy_mm_dd hh:mm` format, matching aiohomematic — so the away setpoint and end
  time actually take effect.
- **`PUT /devices/.../value` reports client mistakes as `400`, not `502`.** A
  value rejected by validation (read-only parameter, out-of-range, wrong type,
  string too long) never reaches the CCU, but was being surfaced as a `502`
  upstream failure. Such rejections now return `400` (validation); genuine
  upstream failures still return `502`.
- **SPECIAL values bypass MIN/MAX consistently.** A parameter value equal to a
  declared SPECIAL sentinel is now accepted on the REST write-coerce path, the
  validation path, and the runtime validity check alike (previously the write
  path rejected a special-below-MIN that the runtime accepted). The rule handles
  both the object (`{"NOT_USED":0.0}`) and list (`[{"ID":…,"VALUE":…}]`) wire
  encodings, mirroring the CCU's own clamp behaviour.

### Changed

- **Duration/ramp editor values match the CCU.** The time base/factor encoder now
  selects the same natural base as the reference (e.g. 2 min → `MIN_1 × 2` rather
  than `SEC_5 × 24`), so the value the CCU editor displays matches what was set.

### Internal

- **Cross-stack model-parity gate is no longer vacuous and now guards pull
  requests.** The datasource wire-identity step provisions the pydevccu/godevccu
  data roots and fails a zero-key run instead of passing silently; a scoped
  model-parity check runs on PRs; and `model_snapshot_diff.py` now counts missing
  channels and device-field mismatches toward the drift total.
- **Broad test hardening across the model core and every north-bound surface:**
  a runtime value-coercion parity golden against the reference; a chi-router ↔
  OpenAPI route walk; a WebSocket broadcast-emitter binding contract and
  model→WS fan-out assertion; an MQTT command write-roundtrip and a committed
  broker-discovery reference; a matter.js schema-staleness contract and per-
  category (Thermostat/DoorLock/WindowCovering/ColorControl/SmokeCoAlarm) bridge
  smoke; custom-profile constructor-resolution and wire-reference guards; and
  Svelte channel-editor undo/redo, cross-validation, edit-lock, schedule,
  preset, and login/OIDC coverage.

## [0.29.0] — 2026-07-08

### Added

- **HmIP-LSC lights now expose colour temperature alongside full colour.** The
  HmIP-LSC uses RGBW hardware but, unlike the HmIP-RGBW, has no
  `DEVICE_OPERATION_MODE` — it runs hue/saturation and colour temperature at the
  same time and reports whichever axis is currently inactive as an empty value.
  It was previously treated as a plain colour light, so its white/colour-
  temperature control never surfaced. It is now modelled as a combined
  colour + colour-temperature light: MQTT discovery advertises both
  `color_temp` and `hs`, the state stream reports the active `color_mode`
  (derived from which value is set), and the Matter Color Control cluster
  reflects the active mode. Ports aiohomematic's `CustomDpIpRGBWColorTempLight`.
- **Doorbell devices are announced as doorbells, not generic buttons.** The ring
  event of the HmIP-DBB and HmIP-DSD-PCB now carries the `doorbell` event
  device class in Home-Assistant MQTT discovery instead of the generic `button`,
  matching the reference stack (homematicip_local #3276).

## [0.28.2] — 2026-07-08

### Fixed

- **Config-UI control tiles: several Custom-DP actions were silently sending the
  wrong parameter or operation name, so the click either returned HTTP 422 or did
  the wrong thing.** The Svelte CDP widgets and the Go custom-DP dispatcher had
  drifted apart on the parameter/operation contract, and the only test that
  looked like it covered the path (`spa_e2e_switch`) exercised the backend keys
  rather than the payload the SPA actually emits — so the divergence went
  unnoticed. Fixed across the board:
  - **Switch → "on for" and Valve → "open for":** the widgets sent
    `{seconds: …}` / `{duration: 600}` while the dispatcher required `duration`
    (and interpreted a bare number as *milliseconds*), so the switch button
    returned `422 missing required param "duration"` and the valve's 10-minute
    preset actually opened for 0.6 s. The dispatcher now accepts a canonical
    `seconds` key (a number of seconds) for `turn_on_for` / `set_on_time` /
    timed valve `open`, keeping the `duration` string form as a
    backward-compatible alias for API/MQTT clients.
  - **Light → brightness slider** sent `{level}` but the dispatcher reads
    `brightness`; **fixed-colour palette** sent the colour *name* while the
    dispatcher expects the numeric `slot`; **effect dropdown** sent `{effect}`
    instead of the accepted `{label}`; and the **HSV saturation** was sent on a
    0..1 scale while the dispatcher treats it as 0..100 (so saturation landed
    ~100× too low). All corrected in the widget.
  - **Climate → "away for duration"** invoked a non-existent operation
    (`set_away_for_duration`); it now calls the real `enable_away_by_duration`.
  - The Switch/Valve timed actions are now driven by a shared input-plus-presets
    control (30 s / 1 min / 5 min, or a free value) instead of a single fixed
    preset button. A new contract test pins every SPA CDP widget's emitted
    operation and parameter keys against the dispatcher's accepted set so this
    class of drift fails the build.

## [0.28.1] — 2026-07-08

### Fixed

- **JSON-RPC cold-start session storm: concurrent callers no longer each open
  a separate CCU session.** `loginOrRenew` read the cached session ID under a
  short lock but released it before the actual `Session.login` round-trip, so a
  burst of concurrent calls at start-up (or after a session expiry) all saw an
  empty session and fired parallel logins — opening several CCU sessions at once
  and tripping the CCU's "too many sessions" limit. Login/renew is now serialized
  through a dedicated lock with a lock-free fast path for a valid, recently
  refreshed session and a re-check under the lock, so a cold-start burst performs
  exactly one login; the auth-failure retry path dedupes the same way. Ports the
  hardening from the aiohomematic reference client (login-storm serialization).

### Security

- **The two unauthenticated callback listeners now cap concurrent
  connections and no longer eager-allocate an attacker-declared frame
  size.** The XML-RPC (`:8120`) and BIN-RPC (`:8129`) callback listeners
  bind on the LAN without authentication. Neither had a concurrent-
  connection limit, so a host on the same segment could open thousands of
  sockets and pin one goroutine (plus its read buffers) per connection.
  BIN-RPC additionally allocated the payload size declared in the 8-byte
  frame header up front (`make([]byte, size)`, up to 10 MiB) before any
  body byte arrived, so a flood of stalled headers amplified memory use.
  Both listeners now honour `callback.max_connections` (default 64) via a
  connection cap, and BIN-RPC grows the payload buffer with the bytes that
  actually arrive instead of the declared size. The BIN-RPC source-IP
  allowlist is now enforced in the accept loop (before a handler goroutine
  is spawned), and a new opt-in `callback.restrict_source_ips` extends the
  same allowlist — resolved from the configured CCU hosts plus loopback —
  to the XML-RPC listener as well. Defaults preserve existing open-LAN
  behaviour except for the new connection cap.

## [0.28.0] — 2026-07-07

### Fixed

- **Disabling or editing an already-live CCU in the Config UI no longer
  silently leaves the old connection running.** `PUT /admin/centrals/{name}`
  persisted the row but, for a central already registered at runtime,
  returned success without touching the live `central.Unit` — disabling a
  CCU left it fully connected and polling until the next daemon restart,
  with no log line, while the Config UI showed a plain "CCU disabled"
  toast. Disabling a currently-registered central now tears the live
  connection down (mirroring the existing Delete order) and logs it. A
  config edit to an already-live CCU still cannot be hot-applied, so it now
  logs a `central.edit.restart_required` warning, and the Config UI shows
  an explicit "a daemon restart is required" toast instead of an
  unqualified "CCU updated" success when a southbound-relevant field
  (host, ports, TLS, credentials, interfaces) actually changed.
- **MQTT: command and HA-birth handlers no longer block the client's
  read loop, which could self-deadlock or stall unrelated commands.**
  `go-mqtt` runs every inbound `MessageHandler` synchronously on the
  single goroutine that also processes PUBACK/PINGRESP for the same
  connection. `BirthSync.handle` called `Bridge.RepublishDiscovery`
  inline — a blocking QoS1 `Publish` per declared discovery topic,
  each waiting on a PUBACK only that same (now-busy) goroutine could
  ever deliver — a guaranteed self-deadlock on every Home Assistant
  restart once more than a handful of entities were declared.
  `CommandSubscriber`'s handlers (`SetValue`/`SetMasterValue`/
  `InvokeChannelService`/…) called the sink inline too, so a CCU
  write stuck for seconds behind the circuit breaker/retry stack
  stalled every other in-flight MQTT message on the same connection.
  Both now dispatch the actual downstream call onto a small bounded
  worker pool (`boundedDispatcher`) and return immediately; per-worker
  routing is keyed by the inbound MQTT topic so writes to the same
  data point never reorder, and a full queue blocks briefly with a
  logged warning rather than silently dropping the command. Both
  `BirthSync` and `CommandSubscriber` now expose `Close()` for a clean
  goroutine drain on teardown.
- **MQTT: removed devices no longer keep stale retained raw-plane
  topics.** `onDeviceRemoved` only retracted the device's
  HA-Discovery `/config` entries; the retained raw-plane
  `values`/`master`/`calculated`/custom-DP state topics plus the
  device-scoped `availability`/`info`/`diagnostics` topics survived
  forever, so non-HA MQTT consumers kept seeing a removed device as
  permanently `available:true` with a stale last value. Device
  removal now also calls the new `Bridge.RetractRawStateForDevice`,
  which publishes an empty retained payload to every raw-plane topic
  the bridge declared for that device address.
- **MQTT: `QoSProfile.Commands` is no longer dead configuration.**
  `command_subscriber.go` hardcoded QoS 1 on every inbound `/set` /
  `/trigger` / `/invoke` subscription regardless of the configured
  `QoS.Commands` value. `CommandSubscriber` now subscribes at a
  configurable QoS (default QoS 1, unchanged) via `WithQoS` /
  `Bridge.CommandQoS()`; `docs/mqtt-topic-schema.md` corrected — it
  previously and incorrectly described command topics as QoS 0.
- **Backup docs no longer claim `secret.key` is bundled in the CLI
  archive.** `docs/admin/backup.md` said the archive was
  self-contained for decryption; `backup create` has always skipped
  `secret.key` while sealing the archive with it instead. The doc now
  states plainly that `secret.key` (or `OPENCCU_LOOM_SECRET_KEY`) must
  be preserved out-of-band, and `backup create` prints a one-line
  reminder of this to stderr after every successful run.
- **Cache metrics: command-tracker/ping-pong sizes now populated.**
  `metrics.Aggregator.Cache()` looped over connected interface clients
  but discarded each one, so `CacheMetricsSnapshot.CommandTracker` and
  `.PingPongTracker` (and therefore `TotalEntries`/`OverallHitRate`)
  always reported zero. The client adapter now exposes
  `CommandTrackerSize`/`PingPongSize` and the aggregator sums them
  across every connected client.
- **Manual backup/restore is now multi-CCU-correct.** `POST
  /backups` triggered a create-and-download backup for only the
  first registered central, and — worse — the CCU backup restorer
  was wired once, globally, so restoring *any* stored `.sbk`
  uploaded it to whichever central had come up first, regardless of
  which CCU actually produced it. `BackupAdapter` now resolves a
  backup's owning central from its id and holds one restorer per
  central; `Restore` targets strictly that central's restorer and
  never falls back to a different one. `POST /backups` accepts an
  optional `{"central_name": "..."}` body to trigger a specific
  central explicitly (the bare/empty-body call keeps backing up the
  first registered central, unchanged); a new admin-only WS command
  `backups.trigger` exposes the same central-scoped trigger. The
  Config UI's Backups page shows a target-CCU picker once more than
  one central is registered.
- **Config UI: Programs and Sysvars lists no longer silently cap at
  one page.** `listPrograms()`/`listSysvars()` fired a single
  unparameterized request; the client now pages through `/programs`
  and `/sysvars` (`page`/`per_page`) until a short page signals the
  end, mirroring the loop `devices.svelte.ts` already used for
  `/devices`. `Favorites.svelte`'s pinned-sysvar lookup benefits for
  free since it shares the same client call.
- **Config UI: Settings now matches the shared operating concept.**
  The MQTT-reload result was an inline coloured `<span>`; it is now a
  `toastStore.success`/`.error` call like every other action result.
  The schema-load failure was an ad-hoc `<Card>` with red text; it is
  now the shared `ErrorState` component with a working retry button.
- **Config UI: the shared `ConfirmDialog` now traps focus.** Opening
  the dialog moves focus to the Cancel button and restores it to the
  triggering element on close; Tab/Shift+Tab now cycle only between
  the dialog's two buttons instead of leaking focus to the page
  behind the modal backdrop. Every destructive-action flow in the SPA
  reuses this one component, so the fix applies everywhere at once.

### Added

- **Config UI: reliability + values-cache admin panel on the
  Diagnostics page.** New `client.ts` wrappers (`getReliability`,
  `getValuesCacheStats`, `resetValuesCache`) surface the existing
  `GET /diagnostics/reliability` and `/admin/values-cache/*` /
  `/devices/{addr}/values-cache/reset` endpoints, which previously had
  no Config UI surface at all. The Diagnostics page now shows a
  per-`(central, interface)` circuit-breaker/connection-state table
  next to the interfaces table, plus a values-cache stats card with a
  confirm-gated reset action.

- **`GET /incidents` gained `central`/`since`/`until`/`limit` filtering.**
  Previously an unbounded, unfiltered `SELECT` across every registered
  central (unlike the equivalent `/audit` endpoint). The bounds are now
  pushed down to SQL via a new `IncidentStore.GetIncidentsFiltered`;
  `?limit=` defaults to 500 (max 5000).
- **Master profiles, bulk incident clear, and service-message disable are
  now reachable over REST**, not just the WS command protocol:
  `GET /devices/{addr}/channels/{no}/master-profiles[/{id}]`,
  `POST .../master-profiles/match`, `DELETE /incidents` (operator),
  and `POST /service-messages/{id}/disable` (operator). All four share
  their domain call with the equivalent WS command
  (`master_profiles.list/get/match`, `incidents.clear`,
  `service_messages.disable`) rather than re-implementing the logic.
- **"Before you upgrade" guidance in `docs/admin/backup.md`.**
  Recommends `openccu-loom backup create` ahead of any upgrade and
  states the rollback reality: migrations carry `Down` blocks but the
  daemon only runs `goose.UpContext` and exposes no rollback
  subcommand, so recovering from a bad upgrade means restoring a
  pre-upgrade backup.
- **Values cache: periodic dead-row garbage collection.**
  `ValuesCacheStore.GCDeadRows` used to be exercised only by tests —
  nothing in the daemon ever called it, so a row whose channel or
  parameter permanently disappeared from a device's model (firmware
  update, profile change) stayed in `values_cache` forever. The
  background flusher goroutine now also drives a much lower-frequency
  GC ticker (derived from the flush interval; 30 min under the
  default 60 s flush cadence) that rebuilds the alive-key set from
  every central's current device model and deletes rows that no
  longer map to a live parameter.
- **Test coverage: MQTT combined-DP/schedule discovery, SSDP
  discoverer lifecycle, the DeviceDetail/Diagnostics SPA routes, and
  the CCU add-on `update_script` exit-code contract.** Closes four
  previously-untested surfaces: table-driven `Build*`/`Publish*`
  discovery + state tests for `internal/north/mqtt/discovery_combined.go`
  and `discovery_schedule.go`, plus an eventbridge-level check proving a
  broker-publish failure during schedule/combined-DP discovery still
  increments the bridge's `publish_errors` counter even though the call
  sites discard the error value; `internal/north/discovery/ssdp`'s
  `New`/`fetch`/`List`/stale-eviction paths via `httptest` and an
  injectable clock; `DeviceDetail.test.ts` (loading/error/happy-path) and
  a `Diagnostics.test.ts` page-load case, plus a new
  `tests/e2e/device-detail.spec.ts` Playwright spec driving a real MASTER
  paramset write and asserting the success toast; and a hermetic
  `tests/contract/ccu_addon_update_script_test.go` that runs the real
  `packaging/ccu-addon/ccu/update_script` against stubbed system commands
  and locks its 0=no-reboot / 10=reboot exit-code contract per platform
  identifier — the exact contract that regressed in 0.27.1.

### Changed

- **Values cache: dirty tracking is now per-`(channel, parameter)`
  key, not per-central.** The periodic flusher used to mark an
  entire central dirty on any single data-point change and then
  re-serialise every live/stale data point of that central on the
  next tick — on a ~1000-DP install, one hot data point forced a
  full-fleet SQLite UPSERT every tick. The flusher now tracks the
  exact set of changed `(channel, parameter)` keys per central and
  persists only those, falling back to a full walk only for the
  initial post-boot tick (covering values that changed before the
  flusher's event subscriptions were installed) and the final
  shutdown flush.
- **The v1 token-admin API (`GET`/`POST /auth/tokens`) is now marked
  `deprecated: true`** in the OpenAPI spec; it remains served (existing
  external API consumers keep working, and `DELETE /auth/tokens/{id}`
  still revokes v1 tokens) but new integrations should use
  `/auth/tokens/v2`, which the Config UI already exclusively uses.
  `docs/admin/auth.md` now documents v2 as the primary surface. Removed
  the dead `listTokens()` v1 client wrapper (unused by the SPA).
- **Config UI: hand-rolled native `<select>` filter dropdowns migrated
  to the shared `lib/components/ui/Select.svelte` primitive.** Covers
  the static central/action/role/interface pickers in `AuditLog`,
  `MessageList`, `ProgramList`, `SignalQualityList`, `UnIgnoreList`,
  `FirmwareList`, `Inbox`, `DeviceDetail` (history channel/parameter
  pickers), and the `UsersAdmin`/`TokensAdmin`/`CentralsAdmin`/
  `RoomsFunctionsAdmin` settings panels — all now match the rest of
  the SPA's themed dropdown look instead of the browser's native
  control chrome. `DeviceList`, `Overview`, `Diagnostics`, and the
  `Settings` general-tab language picker keep their native `<select>`
  deliberately: each sits on a route with a committed Playwright
  visual-regression baseline that this branch cannot regenerate, so
  swapping the control there risked an unverified layout diff.
  `Logs.svelte`'s two selects also stay native — they share a toolbar
  styled entirely off `--ha-*` CSS custom properties, and the shared
  `Select` primitive uses fixed Tailwind slate colors, so migrating
  only those two controls would have made that one toolbar row look
  inconsistent with its neighbors.

## [0.27.2] — 2026-07-07

### Added

- **Config UI: About page (`#/about`).** New "About" entry at the
  bottom of the sidebar's System cluster, plus a version line in the
  sidebar footer that links there. The page collects the support
  answers in one place: daemon version / commit (linked to GitHub) /
  build date / build variant (standalone vs CCU add-on) / start time
  and uptime / API version / active capabilities, a card per central
  (model, CCU firmware, serial, pending CCU update), and the
  license + project links mirrored from the no-JS `/about` page.
- **REST: `GET /api/v1/info` now reports `addon_build`** — `true`
  when the binary was built as the CCU/RaspberryMatic add-on, so
  clients can tell where the daemon runs. API version 2.15.0
  (backwards-compatible addition).

## [0.27.1] — 2026-07-07

### Fixed

- **CCU add-on: installing or updating no longer reboots the CCU on
  RaspberryMatic / OpenCCU.** The add-on's `update_script` returned the
  exit code that explicitly requests a reboot, although nothing in the
  add-on needs a boot-time hook (no ReGa patch, no interface
  registration). It now starts the daemon in place and reloads monit,
  returning the no-reboot exit code; RaspberryMatic / OpenCCU install
  and update the add-on without restarting the CCU. The stock CCU3
  firmware is unchanged — its WebUI reboots unconditionally and
  performs the installation during that boot, which an add-on cannot
  influence.

## [0.27.0] — 2026-07-07

### Security

- **REST: idempotency replay cache no longer leaks responses across
  users.** The cache key now includes the authenticated subject (the
  middleware runs after auth resolution), so two users sharing an
  `Idempotency-Key` value can no longer receive each other's cached
  responses. Concurrent requests with the same key are serialized: the
  second in-flight duplicate is rejected with `409 Conflict` instead of
  executing the mutation twice.
- **REST: request bodies are size-capped everywhere.** The OpenAPI
  validator and the token-create endpoint read bodies through
  `http.MaxBytesReader` (1 MiB) instead of unbounded buffering;
  oversized bodies now return `413` (previously `400` or unbounded
  memory use). Firmware/backup downloads from the CCU are capped as
  well.
- **OIDC: the login flow now mints a per-flow `nonce`** (OIDC Core
  §3.1.2.1), binds it to the pending state server-side, and rejects ID
  tokens whose `nonce` claim is missing or mismatching — a captured ID
  token can no longer be replayed into a different session.
- **REST: 5xx responses no longer echo internal error strings.**
  Server-side failures log the real error and return a generic detail;
  attachment filenames in `Content-Disposition` headers are escaped via
  `mime.FormatMediaType`.
- **REST: `GET /config/effective` no longer leaks array-nested secrets.**
  The secret-masking walk now descends into arrays, so per-central CCU
  passwords (`centrals[].password`) are masked to `***` like every other
  secret — previously they were returned in cleartext to any
  authenticated reader.
- **BIN-RPC decoder hardened against malformed length fields.** A struct
  member / array element count is now validated against the minimum wire
  size per element before allocating, so a crafted count on the callback
  listener (`:8129`) can no longer panic `makeslice` (a single packet
  crashed 32-bit/armv7 builds) or pre-allocate many times the payload
  size.
- **Resource caps against unauthenticated floods.** The Matter UDP
  receive loop bounds concurrent per-datagram dispatch goroutines; the
  Matter unsecured-PASE duplicate-detection map is bounded per
  commissioning window (spoofable `SourceNodeID`); each WebSocket
  connection's subscription set is capped. The CCU JSON-RPC client now
  size-limits response bodies (configurable; default 128 MiB, generous
  for bulk `getAllDeviceData` / `listAllDetail` fetches) and the XML-RPC
  callback HTTP server gained read/write/idle timeouts.

### Fixed

- Data races: `InterfaceClient.GetVersion` vs `SetVersion`; the Matter
  secure-session `closed` flag (now atomic); the Matter fabric-index
  read in the session resolver (now lock-guarded); the command-retry
  jitter source (a shared `*rand.Rand` used concurrently by per-key
  retry chains, now mutex-guarded).
- Connection-recovery coordinator: concurrent recovery triggers for the
  same interface are now genuinely serialized (the wait re-checks in a
  loop instead of once, and the active-slot cleanup is ownership-checked)
  — previously a single completion could release several waiters that
  then ran duplicate recovery pipelines against one CCU interface.
- Device coordinator: `DeviceCreated` events are published outside the
  coordinator lock (a subscriber calling back into the coordinator
  could deadlock); the paramset-consistency check goroutine now has
  panic recovery and a stop path that `Stop()` waits for.
- XML-RPC decoder: nested payloads are depth-limited (mirrors the
  BIN-RPC decoder), closing a stack-exhaustion vector on the callback
  listener.
- Config: invalid numeric values in `OPENCCU_LOOM_*` environment
  overrides now fail the boot with the variable name instead of being
  silently ignored.
- Payload parameter parsing rejects trailing garbage (`"42xyz"` was
  accepted as `42` via `fmt.Sscanf`; now `strconv`).
- Background firmware-data refresh after a device update surfaces its
  error instead of discarding it silently; the async audit sink's
  closer now waits for the worker to drain.
- WebSocket: every registered command must be classified read-only or
  mutating (contract-enforced), so a forgotten role entry can no longer
  expose a mutating command to viewers; writes go through a single
  writer goroutine per connection.

### Changed

- Internal deduplication: shared XML-RPC backend helpers (CCU / CUxD /
  Homegear), one SQLite handle passed through the daemon wiring instead
  of four independent opens, shared REST rate-limiter store, shared
  MQTT topic builder; repo-wide cleanup of truncated provenance
  comments.
- Dependency maintenance: refreshed direct Go modules (`go-mqtt`
  1.1.0 → 1.2.0, `go-chi/chi/v5` 5.3.0 → 5.3.1, `pressly/goose/v3`
  3.27.1 → 3.27.2, `golang.org/x/text` 0.38.0 → 0.39.0) and updated the
  Svelte SPA toolchain within its declared version ranges (Svelte, Vite,
  Vitest, Tailwind, svelte-check, and related dev dependencies).

## [0.26.6] — 2026-07-05

### Fixed

- **Matter pairing: subtype query replies were dropped by strict mDNS
  stacks — commissioners across an mDNS reflector never got the
  answer.** The subtype responder's reply to a
  `_L<disc>._sub._matterc._udp` PTR query echoed the query's question
  section and ID (miekg/dns `SetReply` semantics). RFC 6762 §6 forbids
  questions in multicast responses (§18.1 requires ID 0); Avahi-class
  stacks — including the reflectors that bridge mDNS between subnets —
  validate and silently drop such packets. Field capture showed the
  full chain: query received (`query_seen`), reply sent (`write4_ok`),
  nothing arriving in the querier's subnet, while grandcat/zeroconf's
  primary-type replies (which null the question section) passed the
  same reflector. The reply now clears the question section and forces
  ID 0, mirroring grandcat/zeroconf `server.go` and the RFC. With
  0.26.5's announcements this completes both cache-fill paths a
  commissioner can take: unsolicited announcement AND live
  query/response.

## [0.26.5] — 2026-07-05

### Fixed

- **Matter pairing: commissioners never saw the discriminator-filter
  record — "device not found" despite a valid, resolvable bridge.**
  Apple's commissioning browse filters by the QR code's discriminator
  via the `_L<disc>._sub._matterc._udp` mDNS subtype and satisfies that
  browse from the peer's mDNS cache. The primary instance record lands
  in caches through register-time announcements, but the side-car
  subtype responder was purely reactive: it never announced its PTRs
  and thus relied on a live subtype query reaching its socket — which
  the field capture showed does not happen for commissioner browses.
  The responder now multicasts every registered subtype PTR as an
  unsolicited RFC 6762 §8.3 announcement (twice, one second apart, on
  every multicast-capable interface, both address families) when a
  commissioning window opens, refreshes them on the periodic
  re-announce, and emits TTL=0 goodbyes when the window closes.
  Verified end-to-end: a subtype-filtered browse
  (`dns-sd -B _matterc._udp,_L3840`) that returned nothing now
  surfaces the bridge instance immediately.
- The subtype PTR TTL now matches the primary record's 3200 s (was
  120 s) so the filter record no longer expires from commissioner
  caches while the instance it points at is still valid.
- Inbound subtype queries are traced at debug level
  (`matter.mdns.subtype.query_seen`) so field diagnosis can tell
  "query never arrived" from "query arrived but was not answered".

## [0.26.4] — 2026-07-05

### Fixed

- **Matter pairing failed out of the box: the bridge never advertised
  itself.** `north.matter.mdns_advertise` defaulted to the in-memory
  `noop` advertiser, so an enabled bridge with a configured passcode
  published **no** `_matterc._udp` records — commissioners (iPhone /
  Home Assistant app, Apple Home, Google Home) reported "device not
  found" after scanning a perfectly valid QR code. The default is now
  `zeroconf`; `noop` remains available as an explicit opt-out for
  hermetic tests and out-of-band discovery. Switching the value still
  requires a daemon restart, which the config-change surface now
  reports.
- **Commissioner-visible surfaces used the raw (un-defaulted)
  discriminator.** The commissioning-window opener, the mDNS
  advertisement, and `GET /api/v1/matter/setup-payload` read
  `north.matter.discriminator` directly, so an unset value produced QR /
  manual codes and mDNS TXT records carrying discriminator 0 while the
  bridge core applied the documented 0xF00 default. All Matter config
  consumers now share one defaulting point
  (`config.NorthMatter.WithDefaults`), covering vendor/product ID, node
  label, discriminator, advertiser selection, and PBKDF iterations (the
  startup log also showed the raw `iterations: 0` instead of the
  effective 1000).
- **Matter mDNS records no longer advertise container-bridge
  addresses.** The commissionable/operational A/AAAA records included
  IPs from `docker0` / `hassio` / `br-<hex>` / `veth*` interfaces on
  host-network deployments (e.g. the Home Assistant add-on); iOS
  iterates the advertised address list and times out on unroutable
  addresses during resolve. The Matter advertiser now applies the same
  virtual-interface filter as the client-discovery mDNS.
- **Misleading "commissioning_published" log under the noop
  advertiser.** With `mdns_advertise: noop` the bridge logged
  `matter.mdns.commissioning_published` although nothing left the
  process; it now logs an explicit
  `matter.mdns.commissioning_not_advertised` warning (and the
  operational-record counterpart `matter.mdns.fabric_not_advertised`)
  naming the config remedy.

## [0.26.3] — 2026-07-05

### Changed

- **CI: the Windows test leg no longer gates PRs.** Windows is not a
  shipped target (goreleaser builds linux only) and the runner was the
  slowest, flakiest matrix leg; the run moved to the nightly workflow
  as a non-gating canary for a potential Windows release comeback.

### Added

- **`centrals[].host` is validated syntactically.** The host must be a
  bare hostname or IP literal (IPv4, bare or bracketed IPv6, FQDN with
  optional trailing dot; underscores tolerated for nonstandard LAN
  names). Values carrying a scheme, path, query, fragment, credentials,
  or an embedded port are rejected at config load — the value is
  interpolated into every south-bound URL (XML-RPC / JSON-RPC
  endpoints, the CCU readiness probe), so malformed shapes would
  silently reshape those URLs. Also closes the CodeQL
  `go/request-forgery` findings at that trust boundary. The TCP port
  keeps its own config field.

## [0.26.2] — 2026-07-05

### Fixed

- **Non-string sysvar writes were silently dropped.** Every sysvar
  write went through the `set_system_variable` Rega script, whose own
  guard writes string-typed variables ONLY and emits nothing when it
  declines — so writes to LIST/ENUM (e.g. `Aus;Niedrig;Normal;Hoch`),
  FLOAT, INTEGER, and BOOL variables reported success without changing
  anything on the CCU (reads were unaffected). Writes now dispatch by
  type like the reference client: bool → `SysVar.setBool`, numeric
  values including enum/list indices → `SysVar.setFloat`, and only
  strings use the Rega script — whose decline (missing or non-string
  sysvar) now surfaces as an error instead of a silent no-op.
- **Sysvar writes normalise the payload to the declared type.** The
  wire value now derives from the sysvar's descriptor type instead of
  the caller's payload shape (mirrors the reference `parse_sys_var`):
  LOGIC/ALARM accept bool, 0/1, and the usual string forms
  (`true/on/yes/1`, `false/off/no/0`); FLOAT/NUMBER and INTEGER accept
  numeric strings; STRING stringifies scalars; LIST resolves labels to
  their zero-based index. Mismatches (unknown enum label, fractional
  value for INTEGER, non-numeric string) are rejected with a clear
  error instead of silently picking the wrong wire method. The full
  type × payload matrix is locked by table tests.

## [0.26.1] — 2026-07-05

### Added

- **chip-tool CI workflow.** `.github/workflows/chiptool.yml` runs the
  chip-tool capability suite (`make chiptool-test`) and the compose
  PASE smoke (`make matter-smoke`) on Linux runners — nightly, on the
  `needs-chiptool` PR label, and via manual dispatch. The chip-tool
  binary is extracted from the `connectedhomeip/chip-cert-bins` image
  pinned in `compose/matter-smoke.yml` and cached keyed on that pin,
  so both layers always exercise the same chip-tool build. This closes
  the gap that chip-tool cannot run on macOS developer hosts (Docker
  Desktop does not support host networking).

### Fixed

- **Expired CCU JSON-RPC sessions broke sysvar/program writes until
  restart.** The CCU reports an invalid or expired session as HTTP 200
  + JSON-RPC error 400 ("access denied"), not as an HTTP auth status —
  but the client only re-logged-in on HTTP 401/403, and no production
  path called `Renew`, so after a session lapse (ReGa restart, CCU
  reboot, inactivity timeout) every JSON-RPC operation failed
  permanently with `access denied ("ADMIN" needed 0)` — surfaced e.g.
  as REST 502 on `PUT /sysvars/{name}` — even though the configured
  CCU account had admin rights. The client now maintains the session
  on both layers: proactively (login-or-renew ahead of every call,
  bounded by the existing 90 s freshness guard) and reactively (on
  error 400 it invalidates the session, re-logs-in, and retries once).
  A genuine privilege mismatch still fails with 400 on the fresh
  session and propagates as before.
- **Matter commissioning deadlock — new pairings have been broken
  since 0.23.0 (critical).** `PaseAdapter.ProcessPake3` invoked the
  session-pickup callback while holding the adapter mutex; the
  daemon's pickup callback calls back into the adapter
  (`PeerMRPParams`, added in the 0.23.0 behaviour-parity wave), so
  every successful PASE handshake self-deadlocked the receive
  goroutine after Pake3. The commissioner never received the closing
  `SESSION_ESTABLISHMENT_SUCCESS` StatusReport and timed out —
  observed uniformly with chip-tool, and affecting every controller
  (Apple Home / Google Home / Home Assistant) attempting a NEW
  commissioning on 0.23.0–0.26.0; already-commissioned fabrics
  resuming over CASE were unaffected. The callback now runs outside
  the lock (verifier state is torn down under the lock first),
  mirroring matter.js `PaseServer.ts`, which reads initiator session
  params post-verify with no lock held. Found by the new chip-tool CI
  workflow on its first real run — the exact gap it was built to
  close (the suite cannot run on macOS developer hosts). Regression
  test: `TestPaseAdapter_OnEstablishedRunsOutsideAdapterLock`.
- **From-source Docker build.** The SPA build stage in `Dockerfile`
  now copies `assets/ui/.npmrc` (`legacy-peer-deps=true`) next to the
  package manifests — without it npm 11 fails `npm ci` on the
  `typescript@^6` vs `openapi-typescript` peer range, breaking
  `docker build .` (and with it the compose matter-smoke stack) even
  though every non-container `npm ci` path worked.
- **`make matter-smoke` chip-tool invocation.** The compose exec now
  calls `/root/chip-tool` by absolute path — the `chip-cert-bins`
  image ships its binaries as `/root` symlinks without putting them on
  `PATH`, so the previous bare `chip-tool` invocation could never
  resolve. The target also waits for the daemon's `matter.bridge.up`
  log marker before pairing instead of racing the container
  healthcheck (which only proves the binary execs, not that the Matter
  UDP listener is bound).

## [0.26.0] — 2026-07-04

### Changed

- **Warm-boot descriptor cache now covers the whole fleet.** Hydration
  stores every fetched paramset description in the registry (and, via
  the persistence sink, in SQLite) instead of only the per-channel
  reload path — after one boot against a reachable CCU, all VALUES and
  MASTER descriptors survive restarts. Entity naming is unaffected:
  verified by a byte-identical model snapshot (the naming source
  counts materialised sibling data points and never consulted the
  registry index, which has no production reader).

### Removed

- **Masked `loom:reachable` annotations resolved.** The whitelist audit
  (`script/reachability/whitelist_audit.go`) now counts same-file
  production call sites (outside the symbol's own declaration), which
  removes its false MASKED positives (`TypeOfValue`, `RecalcUnit`,
  `NewParamsetRegistry`, `NewAlarmMessagesWithCentral`,
  `NewConnectionRecoveryCoordinatorWithLimit`). Genuinely dead exports
  whose annotation reasons claimed call sites that never existed were
  deleted: `adapter.StaticCallbackBaseURL`, `metrics.Round2`,
  `device.CheckChannelIsOnlyPrimaryChannel`, `naming.NewHubPathData`
  (plus the orphaned `HubSetPathRoot`/`HubStatePathRoot` constants),
  `payload.ForKinds`, `hmlog.BuildStack`, `hmlog.DefaultSensitiveKeys`,
  and `hmlog.RequestContextFilter` (an unwired duplicate of the
  production `reqctx.ContextHandler` chain). The nine deliberately
  retained seams (functional options, test constructors, schema
  introspection helpers, the values-cache GC key encoder) carry
  truthful annotation reasons now — no fabricated call-site claims.
  Production-unreachable baseline: 3093 → 3091.

### Changed

- **MQTT publishes are circuit-protected.** The bridge now publishes
  through go-mqtt v1.1.0's `Breaker`: during a degraded-broker phase
  (link up, acks missing) publishes fail fast with `ErrCircuitOpen`
  instead of each stalling on the 20-second AckTimeout, with bounded
  half-open probing once the recovery window elapses. Open-transitions
  increment the previously never-incremented `CircuitBreakerOpened`
  counter and are logged. The unwired local circuit-breaker copy is
  gone — breaker semantics live in the shared transport module now.

### Changed

- **MQTT transport upgraded to go-mqtt v1.0.0 (MQTT 5.0 by default).**
  The bridge now speaks MQTT 5.0 on the wire; brokers without v5
  support are pinned via the new expert setting
  `north.mqtt.protocol_version: "3.1.1"` (there is no silent
  downgrade — a v5 connect against a v3-only broker fails with a
  named error). Subscribes now block until the broker's SUBACK, so a
  rejected subscription surfaces as an error instead of being
  silently logged, and the last-will message is configured through
  the new `Will` shape.

- **Dead-code guard is enforced, not advisory.** A new CI ratchet
  (`.github/workflows/reachability.yml`) regenerates the reachability
  inventory on every PR and fails when the number of
  production-unreachable exports grows past the checked-in baseline.
  `loom:reachable` whitelist claims are now audited mechanically
  (`script/reachability/whitelist_audit.go` →
  `notes/parity/loom-reachable-audit.md`) — several annotations claimed
  wiring that never existed. The generic SQLite `PersistentCache`
  wrapper (superseded by the direct descriptor-persistence sinks) was
  removed, and the historical dead-code classification carries an
  addendum marking which of its verdicts the cleanup executed or
  disproved.

- **Superseded duplicate subsystems removed.** The unwired
  aiohomematic-config form-schema port (configui generator/grouping/
  labels/widget half plus the easymode use-case tree — the live UI
  schema comes from the central adapter's UISchema service), the
  duplicate core-package PowerSource cluster (production uses the
  measurement package's server), the schedule facade layer, the event
  bus batching helper, the untyped CentralRegistry and the model-less
  query-facade constructor are gone. Rationale and pointers to the
  live twins are catalogued in `notes/parity/by_design.md` ("Removed
  Unwired Subsystems").

### Fixed

- **Client lifecycle state has a single source of truth.** The interface
  client kept its state twice: a raw field (feeding `ClientState()`,
  `WaitForState` and all predicates) plus the validated state machine —
  and `Close()` only updated the raw field, leaving the machine claiming
  CONNECTED on a stopped client. The machine is now authoritative: reads
  and predicates consult it, `Close()`/`SetState` route through it, and
  its transition listener wakes `WaitForState` waiters (armed before the
  state check, closing a lost-wakeup window). Graceful STOPPING is now a
  valid transition from every non-terminal state, so shutdown paths no
  longer depend on forced transitions. A second, never-instantiated
  client state machine (with a silently diverged transition table) and
  the unused ConnectionState tracker were removed.

- **Warm boot: device and paramset descriptions are actually persisted.**
  `docs/caching.md` promised that descriptors survive a restart, the
  `devices`/`paramsets` tables and their stores existed, and the boot path
  (`CheckAndCreateDevicesFromCache`) was written to consume the cache — but
  the glue between the in-memory registries and SQLite was never wired, so
  the tables stayed empty forever and every boot re-pulled all descriptions
  from the CCU. Each central's registries are now hydrated from SQLite
  before its bring-up starts and mirror every later mutation back
  (normalised + patched, with content hashes for change detection). The
  ADR-0042 cache-clear now also clears the persisted descriptor rows —
  previously its `Devices`/`Paramsets` clearer slots were silently nil.

- **Reliability observability is actually wired.** The hooks connecting
  the per-client reliability primitives to the bus and the incident
  store existed but were never installed: circuit-breaker transitions
  now publish `CircuitBreakerStateChangedEvent` (the signal connection
  recovery, health tracking and the diagnostics event tap already
  subscribed to, but which never fired) and record incidents; ping/pong
  mismatches and exhausted retry chains are recorded as incidents;
  coalesced requests surface in the diagnostics event stream. Six event
  types that had neither publisher nor consumer were removed
  (`FirmwareStateChanged`, `HealthRecorded`, `DataPointsCreated`,
  `ConnectionStageChanged` and the internal `AlarmMessage`/
  `ServiceMessage` duplicates of the hub WS frames), along with a
  diagnostics subscriber waiting on the never-published
  `DataPointStatusChangedEvent` and the redundant scheduler event
  wrapper that duplicated the job instrumentation in `jobs.go`.

- **Metrics: the diagnostics snapshot's model section is populated.** The
  aggregator's device and hub providers existed but were never passed in
  `daemon` wiring, so `model.devices_total`, channel/data-point counts and
  program/sysvar totals silently stayed at zero. Both providers are now
  wired per central. The parallel bus-event metric funnel
  (`EmitLatency`/`EmitCounter`/`EmitGauge`/`EmitHealth` plus the four
  metric event types and `SubscribeObserver`) had subscribers but no
  publisher anywhere in production and was removed — the direct provider
  path is the single metrics pipeline.

- **Auth: `north.rest.auth.basic_enabled` / `bearer_enabled` now actually
  gate their schemes.** Both flags existed in the config (and the SPA
  editor) but were never evaluated — Basic auth was active whenever users
  were configured, Bearer whenever tokens existed. They are now tri-state
  gates (omit → enabled, explicit `false` rejects the scheme even with
  credentials configured), so what the config claims is what the daemon
  does. The never-evaluated `session_enabled` flag was removed: session
  cookies are the SPA's core login mechanism and are always on.
- **CCU data: `ccu_data.easymode_path` override is honored.** The
  documented file override (ADR 0003) was silently ignored — the easymode
  archive always loaded from the embedded bundle. It now follows the same
  file-first/embedded-fallback contract as `translations_path`.

## [0.25.0] — 2026-07-04

### Fixed

- **Matter: bridge could not be commissioned after a restart even with devices
  exposed.** The bridge topology is assembled at daemon start, before the CCU
  device load finishes, so it was empty of bridged endpoints — and the
  commissioning window (correctly) refuses to open an empty bridge — until an
  operator toggled a device exposure to force a rebuild. The bridge now
  reassembles automatically once each central's initial device load completes
  (`CentralSouthboundReadyEvent`), so persisted exposures take effect on their
  own. The 503 error text no longer references a signal that was never emitted.

### Security

- **Northbound auth hardening from a code-vs-code security audit.** A deep audit
  of the REST/WebSocket/auth surface found and closed several issues:
  - **WebSocket commands now enforce role gating.** State-changing commands
    (`paramset.put`, `device.install_mode`, `master_profiles.apply`,
    `backup.trigger`, …) require the connection's identity to hold the same
    minimum role the equivalent REST route demands. Previously a read-only
    *viewer* could invoke operator/admin writes over the socket, because the
    command dispatch dropped the caller identity — that also collapsed the
    per-command rate-limiter and `system.user_permissions` to "anonymous". Both
    are fixed by threading the connection identity into the dispatch context.
  - **CCU passwords are no longer returned in the clear.** `GET
    /api/v1/admin/centrals` now masks `password_plain` to `***` (with a
    restore-on-save round-trip), mirroring `GET /config`.
  - **Credential changes revoke sessions immediately.** A password change,
    role change, or user deletion now revokes that subject's other sessions
    (and, on deletion, purges its API tokens) instead of letting a stolen or
    stale session live out the 12 h TTL.
  - **The at-rest encryption key is excluded from backups**, so a stolen backup
    tarball no longer carries both the key and the ciphertext. *Operator note:*
    restoring a backup onto a fresh host now requires the `OPENCCU_LOOM_SECRET_KEY`
    env key (or copying `secret.key` out of band); otherwise encrypted secrets
    must be re-entered.
  - **Daemon-state backups are now fully encrypted at rest and hardened on
    restore.** The `backup create` archive previously wrote the unencrypted
    SQLite DB world-readable (0644), leaking live session tokens, Matter PSKs,
    and CCU passwords. The whole archive is now sealed with AES-256-GCM using
    the data-dir master key (a versioned container; legacy plaintext archives
    are auto-detected and still restorable) and created `0600`; if no master
    key is available the tool warns loudly rather than silently writing
    plaintext. `backup restore` gained a Zip-Slip guard (tar entries that
    escape the data dir are rejected), decompression-bomb bounds
    (total/per-entry/entry-count caps, streamed to disk), a schema-compat check
    (a backup from a newer daemon is refused unless `--force`), and
    all-or-nothing atomic staging (a mid-restore failure rolls back instead of
    leaving half-applied live data). *Operator note:* as with the key
    exclusion above, restoring an encrypted archive onto a fresh host needs the
    original `OPENCCU_LOOM_SECRET_KEY` (or `secret.key`).
  - **Scheduled-backup rotation race fixed.** The per-central scheduled job now
    awaits backup creation before pruning, so rotation settles at exactly
    `KeepLast` instead of `KeepLast+1`; concurrent runs for one central are
    serialized.
  - **OIDC login-CSRF closed:** the `state` value is now bound to an HttpOnly
    cookie and verified on callback; abandoned-flow state entries are swept.
  - **Session cookies are marked `Secure`** whenever the deployment terminates
    TLS (directly, via `csrf_secure`, or an `https` `public_url`).
  - Hardened the unauthenticated device-icon proxy against unbounded cache
    growth / address enumeration (only known devices are cached); added the
    1 MiB body cap to channel-config import; a dummy bcrypt compare on the
    unknown-user login path (anti-enumeration timing); and a fail-fast guard
    that refuses to serve REST with no auth middleware wired.

- **`hmcli` admin-CLI hardening from a code-vs-code security audit.** Five
  issues found and closed across every command group (`devices`, `sysvar`,
  `program`, `paramset`, `events`, `export-def`, `cache`):
  - **Off-argv credentials.** A bearer token / basic-auth password no longer has
    to be passed via `--token` / `--password` (where it leaks into shell history
    and the process table). Both now fall back to the `OPENCCU_LOOM_TOKEN` /
    `OPENCCU_LOOM_PASSWORD` environment variables, and a missing basic-auth
    password is prompted for on an interactive terminal. The flags remain a
    last-resort override; the token is never logged.
  - **User arguments are URL-escaped.** Device addresses, sysvar/program IDs,
    paramset keys, and query values are now `url.PathEscape`/`url.QueryEscape`-d
    before being spliced into the REST path/query, so a `/` (or other special
    character) can no longer inject extra path segments.
  - **Terminal-output sanitisation.** Server-controlled strings (device
    name/model, sysvar name/value, event type/topic/payload, …) are stripped of
    C0/C1 control bytes and ANSI escape sequences before being printed to a
    human-readable table, closing a terminal-injection vector. JSON output is
    unchanged (already escaped).
  - **`events tail` distinguishes clean from abnormal stream ends.** A clean
    server close still exits 0; an abnormal drop (daemon death, network loss,
    abrupt close) now triggers a bounded auto-reconnect with exponential backoff
    and, only after exhausting the retry budget, exits non-zero — a lost stream
    is no longer silently reported as success.
  - **TLS trust controls.** New `--cacert` (trust a custom PEM CA bundle) and
    `--insecure` (explicit opt-out of verification; **off** by default —
    verification is never weakened implicitly) flags, plus a warning when
    credentials would be sent over plaintext `http://` to a non-loopback host.
- **Audit / observability hardening from a code-vs-code audit of the
  change-log surface.** A follow-up audit of the audit and telemetry paths
  found and closed several leaks:
  - **`GET /api/v1/audit` is now admin-only.** The change-log — which exposes
    subjects, device addresses and operator actions across every central — was
    readable by any authenticated user (including a viewer); it is now gated on
    the admin role like `/auth/users` and `/auth/tokens`.
  - **Custom-DP write audit notes no longer embed the raw written values.** The
    note records only the *names* of the written parameters, so a write payload
    that carries a secret (e.g. a lock PIN) never lands in the append-only audit
    log.
  - **Trace/span export no longer bypasses log redaction.** Span and event
    attributes keyed like a secret (`password`, `token`, `client_secret`, …) are
    now masked to `***REDACTED***` in the OTLP payload, matching the logging
    redactor instead of shipping cleartext to the collector.
  - **User-management audit notes are unspoofable.** New usernames carrying
    `=`, whitespace, or control characters are rejected at creation, so a
    username can no longer tamper with the `subject=<name> role=<role>` note
    shape or inject forged log lines.
  - Prometheus MQTT counters now **sanitize the central-name segment** (with a
    deterministic hash suffix to avoid collisions) so an unusual CCU name can no
    longer emit an invalid exposition line that makes Prometheus drop the whole
    scrape.
- **Server-side edit-lock enforcement for configuration writes.** The
  per-resource edit lock (`POST /sessions/edit`) is now enforced, not merely
  advisory: every MASTER and LINK paramset write must present a valid
  `X-Edit-Token` that currently holds the lock, else it is rejected `423
  Locked` before any CCU call. This applies to `PUT
  /devices/{addr}/paramsets/{MASTER,LINK}`, `PUT /devices/{addr}/link-ps/{peer}`,
  and the WebSocket `paramset.put` command (via a new optional `edit_token`
  arg, rejected with a `locked` command error). Real-time VALUES writes
  (device control) are **not** gated. The config UI already holds the lock and
  now sends its token automatically; non-interactive clients must open an edit
  session first — `hmcli paramset set … MASTER|LINK` does this transparently.
  API version 2.14.0.

### Added

- **Optional API-token expiry.** `POST /auth/tokens/v2` accepts
  `expires_in_days`; expired tokens are rejected at authentication and
  `expires_at` is surfaced in the token list. Omitted → never expires
  (unchanged). API version 2.13.0.

- **User access-permission switches for HmIP-DLD and HmIP-FWI.** The per-user
  access-receiver channels (HmIP-DLD channels 2–9, HmIP-FWI channels 1–8) are now
  exposed as switches: the value reflects the current permission (`STATE`), and
  turning the switch on/off grants/revokes access by writing
  `ACCESS_AUTHORIZATION` = ENABLE/DISABLE. Ported from aiohomematic (#3262) via a
  profile regeneration against aiohomematic 2026.7.1. Available over REST/WS.
  The switches are intentionally **not** exposed to the Matter bridge — a per-user
  access control is deliberately kept out of Matter ecosystems; MQTT
  Home-Assistant-Discovery surfacing is a planned follow-up.

### Fixed

- **Measurement-history rollup + energy engine, from a data-integrity audit.**
  Six confirmed correctness bugs in the history / rollup / energy subsystem are
  fixed with a coherent rollup redesign:
  - **Bounded, watermark-driven rollups.** The hourly and daily folds used to
    re-scan every source row below the lag cutoff and ON-CONFLICT-rewrite every
    historical bucket on every tick — an ever-growing write that held a long
    write lock and starved the recorder's `SaveBatch`, whose flush then silently
    dropped the batch (lost live samples). Each tier now tracks a per-tier
    high-water-mark and folds only the newly-eligible, bucket-aligned window
    `[watermark, cutoff)`; a new history migration adds the watermark table and
    the `measurements(ts)` / `measurements_hourly(bucket_ts)` fold-scan indexes.
    A failed flush now re-queues the batch (bounded, metered via a new
    `history.flush_errors` gauge) instead of silently dropping it.
  - **Energy endpoint shows the current period.** `GET /api/v1/energy` merged
    only the rolled-up tiers, so the un-rolled recent window ("energy today",
    the current hour) was missing. The query now merges the raw (and hourly)
    tail for the un-rolled window into every group (hour/day/month).
  - **Retention no longer corrupts finalized aggregates.** Purge cutoffs are
    bucket-aligned and floored by the fold watermark, so a purge can never
    split a boundary bucket or delete a source row before it has been folded.
  - **Device energy totals include inter-bucket consumption.** A device total
    was a sum of per-bucket `last-first` deltas, dropping everything consumed
    between buckets. It is now a reset-aware range total (positive counter
    segments across the whole range), so a meter reset is handled without a
    negative or under-counted total.
  - **`history.retention` is clamped** to at least the hourly-rollup lag (1 h)
    at config load, so it can never be set below the point where the purge would
    delete raw rows before they are folded.
  - **Chart bucketing off-by-one fixed.** `QueryBuckets` no longer emits a
    spurious extra tail bucket for samples adjacent to the range end.

- **Config pipeline hardening from a code-vs-code audit.** Four issues in the
  config + configstore pipeline were found and closed:
  - **`OPENCCU_LOOM_REST_LISTEN` / `north.rest.listen` now survives every boot.**
    The REST bind address is bootstrap-tier: it is no longer persisted into or
    read back from the `north.rest` config section, so the env/YAML value always
    wins instead of being pinned to a stale value seeded on first boot. The SPA's
    source pill reports it as `bootstrap`, and the field is no longer shown as an
    editable REST field.
  - **`restart_required` in the section-PUT response is now computed per changed
    field** (via `config.RestartRequiredDiff`), so a mixed section (e.g.
    `north.rest`, where CORS is hot-appliable but `public_url` needs a restart)
    and the fully-restart-required webhook section report correctly — consistent
    with `GET /system/config-changes`.
  - **Semantic validation now runs on the section-PUT path.** A well-typed but
    semantically-invalid section (empty `broker_url` with MQTT enabled, a callback
    port out of range, an `ftp://` `public_url`) is rejected with 400 and never
    persisted, instead of being saved and only warned about at the next boot.
  - **A REST PUT can no longer wipe basic-auth users or API tokens.** Credentials
    are managed exclusively by the SQLite user/token stores (the `/api/v1/users`
    and `/auth/tokens` CRUD) and no longer round-trip through the `north.rest`
    section. Config-file (YAML) users **and** API tokens are migrated into SQLite
    once on boot (idempotent, preserving each token's exact secret), so no
    operator loses a login on upgrade.
- **A slow or half-open MQTT broker can no longer stall event delivery.** The
  internal event bus dispatches handlers serially, and the value-change handler
  used to publish to MQTT inline — so a QoS1 publish waiting for a PUBACK (up to
  the broker AckTimeout) froze bus dispatch, and with the broker shared across
  every CCU that meant no central delivered events until the broker recovered.
  The north-bound MQTT fan-out for live value changes is now decoupled onto a
  bounded per-broker worker queue with drop-oldest backpressure (a dropped
  counter is exposed for monitoring); the bus dispatch goroutine never blocks on
  the broker again. The boot-time snapshot path stays synchronous.
- **Event-bus unsubscribe is now a barrier.** The closure returned by
  `Subscribe` waits for any in-flight dispatch that already captured the handler
  to finish before returning, and a handler detached mid-dispatch is skipped
  rather than invoked — so a consumer can free the resources a handler touches
  immediately after unsubscribing, without racing a late callback (previously a
  recovered-and-swallowed send-on-closed-channel / nil-map panic).
- **`EventBridge.Start` is now idempotent** — a second call detaches the previous
  run's subscriptions and fan-out worker before re-attaching instead of
  double-subscribing; the subscription list is mutex-guarded.
- **CCU value loading: skip the init getValue fallback for BidCos-RF.** Passive /
  battery BidCos-RF devices that have not reported since a CCU restart no longer
  have their readable VALUES seeded from the paramset default and marked as a
  valid observation, which masked an actually-uncertain state. The data point now
  stays unobserved until a real value arrives via event (as already done for
  VirtualDevices); the timestamp-gated bulk ReGa fetch remains the trustworthy
  init source. Mirrors aiohomematic #3260.

## [0.24.0] — 2026-07-03

### Security

- **Matter bridge: access-control and commissioning hardening from a
  code-vs-code re-audit against matter.js.** Event reads and subscriptions now
  enforce ACL and fabric-sensitive filtering, so a controller on one fabric can
  no longer read another fabric's `AccessControl` change events; partial-wildcard
  reads re-authorize per resolved endpoint; the PASE brute-force cap now counts
  wrong-passcode attempts (and Pake1 decode failures) and revokes the window at
  the limit; CASE Sigma3 verifies the peer NOC's FabricId against the selected
  fabric; and reserved-range Node IDs / version-0 CATs are rejected as ACL
  subjects. Writes to read-only Matter attributes (e.g. `OnOff`) are now rejected
  instead of being dispatched to the CCU.

### Fixed

- **Matter bridge: broad matter.js parity pass.** Tolerate 21-octet
  operational-certificate serials so LG-TV-class controllers can commission;
  keep a stable per-endpoint DataVersion across config edits so controllers stop
  re-downloading the whole bridge on every exposure toggle; require a Timed
  interaction for `DoorLock` lock/unlock/unbolt; stop heating-only thermostats
  from advertising cooling controls; correct `WindowCovering` ConfigStatus/Mode,
  `ColorControl` ExecuteIfOff gating, and `OnOff` GlobalSceneControl behaviour;
  return the correct Interaction-Model status codes for malformed
  `OperationalCredentials` commands; and set the mDNS default pairing hint to
  power-cycle + manual. Every change mirrors matter.js HEAD; intentional and
  deferred divergences are catalogued in `notes/parity/by_design.md`.

## [0.23.1]

### Changed

- **MQTT transport extracted to the shared `github.com/SukramJ/go-mqtt`
  module.** The wire codec, TCP/TLS adapter, and reconnecting lifecycle that
  previously lived under `internal/north/mqtt` now come from the standalone,
  dependency-free module shared with the `go-*2mqtt` bridges, so a transport
  fix lands once for every consumer instead of drifting across copies. Loom's
  MQTT surface and behaviour are unchanged; the HA Discovery, entity-description,
  topic-building, and command-handling code stays in `internal/north/mqtt`.

### Fixed

- **MQTT: fewer spurious reconnects on a healthy broker.** The keep-alive
  watchdog now tolerates a single delayed PINGRESP and only declares the socket
  dead after two consecutive missed heartbeats (≈ one full KeepAlive), so a
  momentary network blip or scheduler stall no longer forces a
  `mqtt.tcp.ping_timeout` + reconnect. A genuinely half-open socket is still
  detected, one keep-alive interval later.

## [0.23.0]

### Security

- **Matter bridge: UpdateNOC validates the new certificate before persisting
  it.** The handler previously stored whatever NOC bytes the commissioner sent
  — no chain verification against the fabric's trust root, no check that the
  certificate covers the pending CSR key, no FabricID pinning. A defective (or
  malicious) UpdateNOC could replace a fabric's identity with an unusable or
  foreign certificate. The handler now mirrors matter.js/chip: chain
  verification against the stored root, `InvalidPublicKey` when the NOC does
  not certify the pending CSR key, `InvalidNOC` on a FabricID change. A NodeID
  change now propagates to the fabric record, the CASE identity, and mDNS (old
  instance withdrawn, new instance announced), and the fabric's CASE
  resumption records are invalidated as the spec requires.
- **Matter bridge: AddNOC rejects duplicate fabrics and wrong-key NOCs.**
  Installing the same `(FabricID, root public key)` pair twice now returns
  `FabricConflict` before touching the store, and a NOC whose subject key does
  not match the pending CSR key returns `InvalidPublicKey` — both previously
  unreachable status codes that matter.js/chip emit.
- **Matter bridge: removing a fabric withdraws its operational mDNS record.**
  RemoveFabric only triggered a republish, which cannot retire a record the
  advertiser still holds — the removed fabric's `_matter._tcp` instance kept
  answering and commissioners resolved a dead identity. The instance is now
  explicitly withdrawn (and the same path retires the old-NodeID instance
  after an UpdateNOC).
- **Matter bridge: PASE sessions are cleared on CommissioningComplete and
  fail-safe expiry.** Matter §11.10.6.6 step 4 requires the server to drop any
  still-established PASE session once commissioning completes; the expiry path
  does the same, so a failed commissioner's channel no longer outlives its
  fail-safe.
- **Matter bridge: CASE session resumption (Sigma2_Resume) now establishes a
  live, correctly-scoped session.** The resume fast path derived fresh keys but
  never adopted the peer's identity: the resumed session registered with
  peer-node-id 0 (every inbound decrypt failed the AES-CCM nonce check),
  peer-session-id set to the bridge's own id (every reply was dropped by the
  controller), no fabric scoping, and no CASE Authenticated Tags — and the
  fresh resumption id was never persisted, so the controller's next resume
  attempt referenced a stale id. A controller reconnect that offered
  resumption (Apple Home after idle timeout or reboot) hit a dead session
  until it gave up and re-ran the full handshake. The resume path now mirrors
  matter.js `CaseServer.ts` — session identity (fabric, peer node id, peer
  session id, CATs) comes from the stored resumption record, the responder
  identity is resolved by the record's fabric, and the rotated resumption id
  is written back. Resumption records also persist the peer's CATs now;
  previously every persist wiped them, silently stripping CAT-scoped ACL
  privilege from resumed sessions.

- **Matter bridge: AccessControl.ACL rejects out-of-range Privilege / AuthMode
  values.** An ACL write with an invalid `Privilege` (outside View..Administer)
  or `AuthMode` (outside PASE/CASE/Group) enum value was persisted unchecked; it
  is now rejected with `ConstraintError`, matching matter.js schema validation.
- **Matter bridge: OperationalCredentials.NOCs requires Administer to read.**
  The NOC / ICAC certificate bytes were readable — and streamable via a
  wildcard subscribe — at View privilege. Reading them now requires Administer,
  matching matter.js (`operational-credentials.element.ts` access "R F A").
- **Matter bridge: PASE pairing failures are now capped.** An open
  commissioning window accepted unlimited passcode guesses for its whole
  duration (up to 15 minutes commissioned, or 48 hours for an uncommissioned
  bridge), letting a LAN attacker brute-force the setup passcode. The bridge
  now counts pairing failures and revokes the window after 20, mirroring
  matter.js `PaseServer`'s `PASE_COMMISSIONING_MAX_ERRORS`; the counter resets
  when a new window opens.
- **Matter bridge: writes and command invokes are now gated by the
  per-element ACL privilege, not a flat Operate.** Previously every write
  and command was authorised at Operate, so a subject holding an
  Operate-privilege CASE entry (as controllers issue to household members)
  could write `AccessControl.ACL` to grant itself Administer, invoke
  `AdministratorCommissioning.OpenCommissioningWindow` to admit a rogue
  administrator, or `OperationalCredentials.RemoveFabric` to evict another
  ecosystem. The IM layer now looks up the required privilege per attribute
  / command (AccessControl.ACL → Administer, RemoveFabric → Administer,
  BasicInformation.NodeLabel → Manage, …), mirroring matter.js. Commissioning
  is unaffected (PASE sessions bypass ACL as before).
- **Matter bridge: subscriptions now enforce ACL on every report.** The
  subscription read paths (initial and ongoing, attributes and events)
  previously called the dispatcher directly, bypassing the access check the
  one-shot Read path applies — so a View-only or ACE-less subject could
  subscribe wildcard and stream fabric-sensitive data (`AccessControl.ACL`,
  `OperationalCredentials.NOCs`). Every subscription result is now authorised
  against the subscribing subject and fabric-projected, matching matter.js.

### Fixed

- **Matter bridge: only one PASE commissioning handshake runs at a time.**
  A second commissioner's PBKDFParamRequest arriving mid-handshake silently
  replaced the first one's in-flight verifier state; the bridge now rejects the
  overlapping request (self-expiring after 60 s so a crashed commissioner
  cannot lock the window), matching matter.js's single-active-PASE rule.
- **Matter bridge: PASE honours and advertises MRP session parameters.** The
  commissioner's InitiatorMRPParams (PBKDFParamRequest tag 5) is now applied to
  the PASE session's retransmit timing, and the bridge advertises its own
  ResponderMRPParams in the response — previously both were ignored, so
  retransmissions used spec defaults regardless of what either side asked for.
- **Matter bridge: MRP message counters resist nonce reuse.** New session
  counters seed in the low 28 bits (so a fresh counter never starts near
  exhaustion) and secure-session counters refuse to roll over past
  0xFFFFFFFF (a wrapped counter would reuse an AES-CCM nonce under the live
  key); the session is retired instead, matching matter.js. Reliable-send and
  subscription bookkeeping are now keyed per session, closing a
  cross-session counter-collision.
- **Matter bridge: NodeLabel and Location survive a restart.** A commissioner
  write to either attribute (both non-volatile per spec) is now persisted and
  restored at boot, instead of reverting to the configured default.
- **Matter bridge: event numbers stay monotonic across restarts.** The
  EventNumber counter was in-memory and reset to 1 on every boot, so
  controllers filtering on the last number they saw silently dropped every
  fresh event; it now persists a counter ceiling and resumes past it.
- **Matter bridge: device-type ACL targets are honoured.** An AccessControl
  entry whose target names only a device type previously always denied; it now
  matches endpoints advertising that device type, mirroring matter.js/chip.
- **Matter bridge: the periodic mDNS re-announce no longer churns caches.** The
  30-minute operational re-announce re-registered every record, emitting TTL-0
  goodbye packets that made Apple flush and re-learn the bridge each interval;
  unchanged records are now left in place and only real changes re-register.
- **Matter bridge: bridged endpoints report a stable DataVersion.** Bridged
  cluster servers are rebuilt on every dispatch, and each rebuild installed a
  fresh random DataVersion — the same cluster reported a different version on
  every read, so controllers' DataVersionFilters never matched and Apple
  re-transferred all endpoints on every re-subscribe. The version now lives on
  the persistent endpoint keyed by cluster, is stable across reads, and is
  bumped on real state changes (value pushes from the CCU, reachability
  flips, successful writes), matching matter.js's once-per-lifetime /
  increment-per-change semantics.
- **Matter bridge: GroupKeyManagement enforces the per-fabric key-set
  budget.** KeySetWrite accepted unlimited new key sets; adding one beyond
  MaxGroupKeysPerFabric now returns ResourceExhausted (updates of an existing
  key set stay allowed), matching matter.js.
- **Matter bridge: duplicates are acknowledged immediately.** The standalone
  ACK for a retransmitted (duplicate) reliable message was queued behind the
  200 ms piggyback grace window, so a peer that had already waited out its
  retransmission timeout kept retransmitting; the ACK now goes out
  immediately, matching matter.js.
- **Matter bridge: a Subscribe matching nothing is rejected.** A Subscribe
  request naming no attribute/event paths, or whose (wildcard) paths matched
  zero attributes and events, was accepted and registered as a dead
  subscription burning engine ticks; both cases now return InvalidAction
  before registration, matching matter.js. Re-subscribes whose reports are
  fully suppressed by DataVersionFilters still establish.
- **Matter bridge: outbound retransmissions honour the peer's MRP session
  parameters.** The retransmit schedule used a fixed 300 ms base and a bare
  1.6× growth; the intervals a controller advertises during PASE/CASE session
  establishment were parsed and thrown away. The initiator's Sigma1 session
  parameters are now stored on the operational session, the base interval is
  selected per transmission from the peer's active/idle interval (active when
  the peer sent within its active threshold; spec defaults 300 ms / 500 ms /
  4 s otherwise), and the full spec backoff formula (margin, exponential
  growth past the threshold, jitter) applies — matching matter.js `MRP` and
  chip `GetMRPBaseTimeout`. Retransmits to a sleepy or slow controller no
  longer fire ~40 % too early, and active peers get snappier recovery.
- **Matter bridge: chunked WriteRequests are validated.** The
  MoreChunkedMessages flag was ignored entirely; a chunked write combined
  with SuppressResponse, or inside a timed interaction, is now rejected with
  InvalidAction per spec, and each valid chunk is answered with its own
  WriteResponse (matching matter.js, which responds per chunk).
- **Matter bridge: MRP ack bookkeeping is session-scoped.** ACK obligations,
  standalone-ack reply routes and the per-chunk StatusResponse rendezvous were
  keyed on the bare 16-bit exchange ID, which is only unique per session — two
  concurrent controllers (or an old and a new CASE session of one controller)
  sharing an exchange ID could discharge each other's pending ACKs, hijack the
  synthesised standalone-ack route, or release the wrong chunk-streaming
  waiter. All three maps now key on (session, exchange), matching matter.js's
  session-scoped exchange resolution.
- **Matter bridge: WindowCovering reports a real TargetPosition.** The
  TargetPositionLift/TiltPercent100ths attributes always mirrored the current
  position, so controllers never saw where a moving cover was heading (Apple
  Home shows "Opening…"/"Closing…" from the target-vs-current delta). The
  attributes now carry the commanded destination — set by UpOrOpen /
  DownOrClose / GoToLift- / GoToTiltPercentage, snapped back to the current
  position by StopMotion — matching matter.js `WindowCoveringServer` across
  the cover, blind (lift + tilt) and garage projections.
- **Matter bridge: OnOff implements the OnWithTimedOff engine and writable
  LT attributes.** OnWithTimedOff previously collapsed to a plain On — the
  timed-off countdown, the AcceptOnlyWhenOn gate and the delayed-off guard
  period were all dropped, and the OnTime / OffWaitTime / StartUpOnOff
  attributes were read-only constants. Lights now run the full matter.js
  `OnOffServer` state machine: OnTime counts down in 100 ms ticks and turns
  the device off at expiry, an Off during a timed-on phase enters the
  delayed-off guard, AcceptOnlyWhenOn is honoured while off, and all three
  LT attributes are writable (StartUpOnOff requiring Manage privilege;
  parking a countdown at 0/0xFFFF stops it).
- **Matter bridge: DoorLock emits the mandatory LockOperation event.** A
  successful remote LockDoor / UnlockDoor / UnboltDoor produced no event, so
  controllers tracking lock activity (Apple Home notifications, event
  subscribers) saw state flips without the spec-mandated operation record.
  The bridge now emits LockOperation (priority critical) with the operation
  type (UnboltDoor reports Unlatch), OperationSource=Remote and the invoking
  fabric + node, matching matter.js `DoorLockServer`. The DoorLock EventList
  now advertises the three conformance-mandatory events.
- **Matter bridge: LevelControl honours ExecuteIfOff and the MinLevel/MaxLevel
  bounds.** A plain `MoveToLevel` / `Step` (the non-WithOnOff variants) sent to
  a light that is off executed anyway and turned it on; per Matter they must be
  a silent no-op unless the command's options set ExecuteIfOff. The requested
  level is now also cropped to the spec range [1, 254] (a plain `Step` down can
  no longer switch the light off — it floors at MinLevel), the WithOnOff
  variants couple a MinLevel target to Off, and `Move` with a zero rate returns
  `InvalidCommand`, all matching matter.js `LevelControlServer`.
- **Matter bridge: device reachability updates reach attribute-subscribers.**
  When a bridged HomeMatic device went (un)reachable the bridge fired the
  `ReachableChanged` event but did not mark the `Reachable` attribute changed,
  so a controller tracking the attribute (Google Home) showed stale
  reachability until it re-subscribed. The attribute is now marked dirty too.
- **Matter bridge: vendor-specific protocol datagrams are rejected, not
  misrouted.** A datagram carrying a vendor id whose low protocol id collided
  with Interaction Model / Secure Channel was fed into those handlers; it is now
  rejected as an unknown protocol, matching matter.js's full 32-bit protocol
  dispatch.
- **Matter bridge: empty subscription keepalives set SuppressResponse.** A
  no-op max-interval heartbeat no longer asks the controller for an IM
  StatusResponse, matching matter.js.
- **Matter bridge: GroupKeyManagement.KeySetRead / KeySetRemove return
  NotFound.** Reading or removing a non-existent group key set returned a
  generic failure (KeySetRemove even succeeded silently); both now return the
  spec `NotFound` status, matching matter.js.
- **Matter bridge: colour lights advertise and accept their mandatory
  ColorControl commands.** The mounted CT / HS / RGBW colour servers advertised
  an empty `AcceptedCommandList` and rejected the mandatory Move / Step /
  StopMoveStep commands, so a conformance controller (and Alexa's hue-only
  changes on an RGBW light) failed. Each server now lists its
  feature-appropriate command set and accepts the continuous-rate commands as
  no-ops (HM lights have no rate sweep), matching matter.js.
- **Matter bridge: colour-temperature lights advertise the mandatory
  `StartUpColorTemperatureMireds` + `CoupleColorTempToLevelMinMireds`
  attributes.** They were missing from the ColorControl read surface, so a
  conformance/controller read of them returned UNSUPPORTED_ATTRIBUTE. Both are
  now served (CoupleColorTempToLevelMinMireds = PhysicalMinMireds;
  StartUpColorTemperatureMireds = null), matching matter.js.
- **Matter bridge: read-event timestamps are correct.** Out-of-band event reads
  stamped the EpochTimestamp in microseconds instead of the spec-mandated POSIX
  milliseconds, so a read event's time read ~1000× off (and inconsistent with
  the subscribe path, which was already correct). Both paths now emit
  milliseconds.
- **Matter bridge: GeneralDiagnostics.TestEventTrigger is now enumerated.** The
  mandatory command was missing from `AcceptedCommandList` and returned a
  generic failure; it is now listed and returns `ConstraintError` (the bridge
  configures no test-event enable key), matching matter.js.
- **Matter bridge: commissionable mDNS Session Idle Interval corrected to
  500 ms** (was 5000 ms, from a non-existent matter.js default). A too-large SII
  made commissioners space PASE retransmits ~10× too slowly on a lossy link.
- **Matter bridge: multi-admin "add to another ecosystem" now works.** An
  Enhanced Commissioning Window opened by a second controller
  (`AdministratorCommissioning.OpenCommissioningWindow`) supplies a PAKE
  verifier derived from a passcode that controller chose. The bridge validated
  the verifier and then discarded it, so PASE ran against the bridge's own
  passcode and every "add to Google/Alexa/…" attempt failed at the Pake2
  confirmation. The bridge now installs a PASE acceptor built from the supplied
  verifier for the window lifetime (restoring the configured acceptor on close)
  and advertises the Enhanced window over mDNS with `CM=2` + its discriminator
  so the second controller can discover it — mirroring matter.js.

- **Matter bridge: ArmFailSafe ownership is now enforced.** A commissioning
  fail-safe armed by one fabric could previously be re-armed or disarmed by a
  different fabric, and a CASE session could arm the fail-safe during another
  admin's open commissioning window — letting one controller roll back another
  controller's in-progress commissioning. The handler now rejects a re-arm or
  disarm from any fabric other than the one that armed it, and rejects a CASE
  arm while a window is open, both with `BusyWithOtherAdmin`, mirroring
  matter.js. PASE commissioning is unchanged.
- **Matter bridge: cancelling a commissioning window no longer Busy-locks the
  next one.** `RevokeCommissioning` (and the internal revoke path) now expires
  the fail-safe as Matter §11.19.7.3 step 1 requires, so cancelling a pairing
  in a controller and retrying immediately no longer fails with "busy" for the
  remainder of the original window (up to 15 minutes).

- **Matter bridge: blinds, colour-temperature, dimmer-step and thermostat
  setpoint commands now actually execute.** The bridge's command decoder
  delivers fields as a context-tag-keyed map, but the WindowCovering
  `GoToLift`/`GoToTiltPercentage`, LevelControl `Step`, ColorControl
  `MoveToColorTemperature` and Thermostat `SetpointRaiseLower` handlers only
  accepted a differently-shaped payload, so every real Apple Home / Google
  Home / chip-tool invocation of those commands was rejected — or, for the
  thermostat, silently ignored. The handlers now accept the real wire shape;
  `GoTo*Percentage` values are additionally clamped to their 100.00 % maximum.
- **Matter bridge: IM responses and CASE/PASE handshake replies are now
  MRP-reliable.** `WriteResponse`, `InvokeResponse`, the `TimedRequest`
  `StatusResponse`, and the PASE/CASE continuation replies (Pake2, Sigma2)
  were shipped best-effort, so a single dropped UDP datagram surfaced as
  "Not Responding" on a command or aborted commissioning outright. They are
  now retransmitted until the controller acknowledges them, matching
  matter.js. Unsecured (PASE) retransmits are also now detected as duplicates
  and acknowledged without re-running the handshake handler.
- **MQTT: detect half-open broker connections via a PINGRESP watchdog.**
  The keep-alive loop sent `PINGREQ` but never checked for the matching
  `PINGRESP`, and the read loop blocks in `ReadFrame` without a deadline.
  On a half-open socket (broker or network gone without a TCP FIN/RST) the
  read loop stayed blocked forever, `handleConnectionLost` never fired, and
  the lifecycle never reconnected — QoS-1 publishes (HA discovery, command
  acks) then timed out with "context deadline exceeded" on a dead socket
  until a manual restart. The keep-alive loop now arms an outstanding-ping
  flag after each `PINGREQ`; if the next tick still sees it unanswered, the
  connection is declared lost so the lifecycle re-dials.

## [0.22.0]

### Added

- **Persistent hourly/daily measurement-history rollup tiers.** Two new
  aggregate tables (`measurements_hourly`, `measurements_daily`) fold raw
  history rows into low-resolution buckets (sum/min/max/count plus
  first/last, needed for cumulative `ENERGY_COUNTER` deltas) so long-term
  history stays cheap. An hourly job rolls raw rows into the hourly tier
  and re-aggregates hourly→daily exactly (never average-of-averages)
  before the existing raw-row purge runs, so nothing is dropped before it
  is folded. Two new opt-in retention knobs bound each tier independently:
  `persistence.history.retention_hourly` (default 13 months) and
  `persistence.history.retention_daily` (`0` = keep forever). This is the
  backend foundation the `/api/v1/energy` endpoint below reads from.

- **`GET /api/v1/energy` — per-device power/energy breakdown.** Reads the
  hourly/daily measurement-history rollup tiers (this release, see below)
  and folds them into a per-device breakdown: `POWER` samples
  become `avg_power_w`/`peak_power_w`, `ENERGY_COUNTER`/
  `ENERGY_COUNTER_FEED_IN` become `consumed_wh`/`feed_in_wh` bucket deltas.
  A meter reset within a bucket (`last < first`) reports the delta as the
  counter's post-reset value and sets `reset` — never a negative delta.
  `?group=hour|day|month` selects the bucket granularity (month
  re-aggregates the daily tier); `?device=<address>` optionally scopes to
  one device. Requires the opt-in history feature
  (`persistence.history.enabled`); the route is absent (404) when
  disabled, same as `/history`. REST API version bumped to 2.12.0.

- **`hmcli` gains a `devices` command group + shared REST client.** The admin
  CLI moved onto the Cobra framework with a root carrying shared connection
  flags (`--host` default `http://localhost:8119`, `--token`, `--user`,
  `--password`, `--timeout`); the existing `version`/`config`/`cache`/
  `export-def` commands are unchanged. New: `hmcli devices list|get|get-value|
  set` — list the inventory, read a device / datapoint, and flip a value from
  the shell, each with `--json` for scripting. Pure REST client (no daemon
  change). The `cache clear` online default host was corrected to
  `http://localhost:8119` (was `:2121`). (More command groups — `sysvar`,
  `program`, `paramset`, `events tail` — are follow-ups.)

- **`hmcli` gains an `events tail` command.** Connects to the daemon's
  `/api/v1/events` WebSocket stream and prints live events until interrupted.
  `--topics` (comma-separated; default `*`) controls the subscribe filter;
  `--json` emits each event as a compact JSONL line for scripting; `--classify`
  requests inline `category`/`data_point_type` on datapoint value-changed
  payloads. `--timeout` bounds the handshake only — the stream itself runs
  indefinitely until the connection closes or Ctrl-C / SIGTERM. This completes
  the B1 `hmcli` command surface: `devices`, `sysvar`, `program`, `paramset`,
  `events`. No daemon change.

- **`hmcli` gains `sysvar`, `program`, and `paramset` command groups.** Three
  new command groups extend the admin CLI: `hmcli sysvar list|get|set|fetch`
  manages CCU system variables (list with type/value table, read, write a
  runtime value, force re-pull with optional `--central` scope); `hmcli program
  list|get|run|enable|disable` lists programs, reads one, triggers execution,
  and toggles the active flag; `hmcli paramset get|set` reads or writes a device
  paramset (VALUES, MASTER, LINK) as a sorted key=value table. All commands
  support `--json` for scripting and share the same `--host`/`--token`/`--user`/
  `--password`/`--timeout` connection flags. Pure REST client — no daemon change.

- **Scheduled / automatic CCU backups (`backup:`).** A new opt-in config
  section runs an automatic backup of each configured CCU on an interval
  (`backup.schedule`, e.g. `24h`; off by default) via a per-central
  `central.scheduled_backup` scheduler job. Old backups are rotated per CCU
  (`backup.keep_last`, keep the newest N; `0` keeps all). The archives appear
  in the same Backups view as manual backups; a failed backup surfaces on the
  `scheduler.failures` gauge and is retried on the next tick without crashing
  the daemon. Off-box targets (S3/WebDAV) remain out of scope. No API change.

- **Inbound webhook (`north.webhook.inbound`).** Two new REST endpoints let
  external systems drive the CCU: `POST /api/v1/webhook/value` sets a datapoint
  value and `POST /api/v1/webhook/program` runs a program. Disabled by default;
  the routes are mounted only when `north.webhook.inbound.enabled` is set
  (restart-required, 404 otherwise). Authorization requires an operator identity
  via the normal auth chain **or** the optional inbound bearer token
  (`north.webhook.inbound.token`, constant-time compared) — so a header-only
  caller (e.g. a doorbell) can POST without a session or user login. These are
  real device writes / program runs, reusing the same write/trigger paths as the
  equivalent REST endpoints. REST `APIVersion` → **2.11.0**.

- **`additional_information` on per-DP MQTT state and the REST datapoint DTO.**
  Data points that expose enriched model metadata (currently the calculated
  operating-voltage sensor's battery info — type, quantity, low-voltage limits)
  now publish it under an optional `additional_information` object on both their
  MQTT state topic and the REST `DataPointSummary` (`GET /devices/.../datapoints`).
  Strictly additive: the key is omitted for plain scalar DPs, so existing
  payloads are byte-identical. The REST datapoint schema gained the optional
  property (`assets/openapi.yaml`); REST `APIVersion` → **2.10.0**. Documented
  in `docs/mqtt-topic-schema.md`. (Exposing the same metadata on the hub
  service-/alarm-message aggregates remains a planned follow-up — see
  `notes/parity/by_design.md` §A1-BD01.)
- **MCP `list_incidents` tool.** The Model Context Protocol bridge gains a
  read-only `list_incidents` tool that projects the reliability incident
  journal (circuit-breaker trips, ping/pong mismatches, retry exhaustion) to
  LLM agents — the same enriched, source-tagged data `GET /incidents` serves
  REST clients. Newest-first, limit-clamped (default 50, max 1000), with an
  optional `central_name` filter. Read-only (not gated by `allow_writes`);
  available whenever the incident store is wired and `north.mcp.enabled`.
- **Outbound webhook bridge (`north.webhook`).** A new north-bound adapter
  that POSTs a signed JSON payload to an operator-configured URL on datapoint,
  system-status and incident events. Disabled by default. Each delivery carries
  an HMAC-SHA256 body signature (`X-OpenCCU-Signature: sha256=…`, GitHub-webhook
  convention) when a secret is set, plus `X-OpenCCU-Event` and a unique
  `X-OpenCCU-Delivery` header. Deliveries run off a bounded queue with jittered
  exponential-backoff retry, so a slow or unreachable endpoint never blocks the
  event bus. Filters: per-event-type allowlist, per-CCU allowlist, and an
  optional parameter glob on datapoint events. A single endpoint is supported;
  multi-endpoint fan-out is a planned follow-up. Webhook config changes are
  restart-required (the bridge is wired once at boot). A new `incident.recorded`
  event lets reliability incidents flow to the webhook alongside the existing
  diagnostics surface.

- **Energy view (`#/energy`).** A new SPA route renders the `/api/v1/energy`
  aggregation: a central + group (hour/day/month) selector with 24h/7d/30d/12mo
  range presets, total consumed/feed-in cards (Wh→kWh), an inline
  consumption-over-time chart with an all-devices/per-device toggle, and a
  per-device breakdown table (consumed/feed-in kWh, avg/peak power). Buckets
  where a meter reset occurred carry a reset badge. Shows a "history recording
  is off" state with a settings link when the history feature is disabled.

- **Cross-CCU fleet overview (`#/fleet`).** A new read-only SPA route lists
  every configured CCU at a glance: online/offline availability, host +
  CCU-reported hostname, model/version/serial, per-central device count, a
  chip per configured interface, and an "Open CCU WebUI" link. Reflects
  CCUs adopted live (see below) without a page reload. No REST/API change.

- **Access-control view (`#/access`).** A new admin-only SPA route manages
  Basic-auth users and API tokens from the browser instead of hand-editing
  `config.yaml`: add/edit a user (role `viewer`/`operator`/`admin` + optional
  password), delete via the shared confirm dialog, create an API token with a
  copy-once plaintext reveal, and delete a token by fingerprint. Frontend-only
  — the underlying REST CRUD routes already existed.

- **Whole-home device overview (`#/overview`).** A new top-of-nav SPA route
  renders the existing auto-tile dashboard across every device at once,
  grouped by room / function / CCU and filterable, with per-group lazy
  loading so the page starts interactive immediately. Rooms are never merged
  across CCUs. No new REST endpoint.

- **Live CCU adopt / remove — no daemon restart.** Adding or removing a CCU
  through the REST central-admin path (`POST /api/v1/centrals`, `DELETE
  /api/v1/centrals/{name}`) now brings the southbound connection, model,
  scheduler jobs, and CCU auth up or down live, instead of only taking effect
  after a restart. `centrals` no longer appears in the config
  restart-required diff for a pure add or remove — only an in-place edit of
  an existing central still requires a restart. Device-icon proxying and the
  cross-CCU fleet list (`GET /api/v1/system/ccu`) resolve a runtime-adopted
  central immediately as well.

- **Matter bridge enforces server-side timed-interaction conformance.** A
  command tagged "Timed Required" in the Matter model (currently
  `AdministratorCommissioning`'s `OpenCommissioningWindow` /
  `OpenBasicCommissioningWindow` / `RevokeCommissioning`, Matter §8.7) is now
  rejected with `NEEDS_TIMED_INTERACTION` when invoked outside a timed window,
  even if the controller left the request's own Timed flag clear. Previously
  the bridge only honoured the controller-asserted flag.

### Changed

- **North-bound bridges (MQTT, Matter, REST, MCP, webhook) migrated onto a
  shared, phased-start `bridge.Registry`.** Each surface now registers as a
  `Service` (`Name`/`Start`/`Stop`) instead of being hand-wired in
  `cmd/openccu-loom`; the registry starts `PhaseEarly` services (MQTT, so the
  boot-time retained-state snapshot still reaches the broker before
  southbound hydration) before `PhaseLate` ones (Matter, REST, webhook),
  rolls back already-started services on a mid-start error, and stops
  everything in reverse order on shutdown. Internal lifecycle unification —
  no user-facing behaviour change.

## [0.21.3]

### Changed

- **Default REST / Config-UI port is now `8119` (was `8080`) in all environments.**
  8080 is a busy, commonly-claimed port; the daemon now defaults
  `north.rest.listen` to `:8119` everywhere (binary, Docker, both add-ons).
  Override with `north.rest.listen` / `OPENCCU_LOOM_REST_LISTEN` as before.
- **HA add-on: all three ports are now operator-configurable options.**
  `rest_port` (8119), `xmlrpc_callback_port` (8120) and `binrpc_callback_port`
  (8129) are add-on options (passed through to the daemon's env overrides in
  `run.sh`), instead of a `ports:` mapping block that did nothing under
  `host_network: true`. The inert `ports:`/`ports_description:` block was
  removed. `ingress_port` stays static at 8119 and must equal `rest_port` for
  the Ingress panel — change `rest_port` only for direct-access setups.

## [0.21.2]

### Fixed

- **A CCU configured by host (e.g. `localhost`) is now recognised as already
  configured by discovery.** The discovery "already configured" check matches by
  serial, but centrals created before serial capture (or added manually) carry no
  stored serial, and a host match can never succeed for a `localhost`-configured
  central against the CCU's real discovered IP — so the same CCU showed up as a
  fresh discovery. The daemon now backfills a central's serial from its live ReGa
  connection at bring-up (the same canonical 10-character form discovery
  produces, resolved in `hub_wiring`), writing only the serial column and only
  when empty. After a central connects once, discovery recognises it by serial
  regardless of host. No wire-schema change (`APIVersion` stays 2.9.0).

## [0.21.1]

### Changed

- **CCU discovery now identifies a CCU by its canonical 10-character serial.**
  The serial captured from SSDP/UPnP (the UDN tail, which is longer than 10
  characters) is reduced to its last 10 characters — the same canonical form the
  runtime `system.GetSerial` reader produces — so a discovered CCU and a
  configured central are compared by an identical string. Both producers now
  funnel through one helper (`routingkey.CanonicalSerial`), guaranteeing the two
  serial worlds line up. The "already configured" check still falls back to a
  host match, and also canonicalises a stored full serial at compare time so a
  central adopted under 0.21.0 (which stored the untruncated serial) keeps
  matching. No wire-schema change (`APIVersion` stays 2.9.0).

## [0.21.0]

### Changed

- **CCU discovery now matches "already configured" by serial, not host**
  ([ADR 0046](docs/adr/0046-ssdp-ccu-discovery.md)). Adopting a discovered CCU
  persists its hardware serial (new `serial` column on the centrals store,
  migration 024); the discovery list then flags a CCU as already configured by
  that stable serial, so a CCU whose IP changed (DHCP lease, rotating docker
  address) is no longer offered as a duplicate. Centrals configured before this
  release carry no serial and continue to match by host.
- **Discovery suggests a stable adoption address.** Each discovered CCU now
  carries a `suggested_host` the SPA pre-fills instead of the raw SSDP IP:
  - `localhost` when the CCU is on the daemon's own host (its discovered IP is
    one of the daemon's interface addresses), and
  - on the supervised HA add-on, a reverse-DNS-resolved **container hostname**
    when the CCU is reachable only on a docker-range IP (172.16.0.0/12) — the
    docker IP rotates, the hostname does not.
  The raw host is still shown, and is used unchanged when no better address
  applies. `APIVersion` → 2.9.0 (added `suggested_host` / `serial` fields).

## [0.20.1]

### Fixed

- **HA Ingress users are no longer trapped in the first-run setup wizard.**
  After onboarding moved into the SPA ([ADR 0045](docs/adr/0045-login-and-onboarding-into-spa.md)),
  the boot probe decided wizard-vs-app purely from the persistent first-run
  state and ignored the request's identity. Behind Home Assistant Ingress the
  operator is already authenticated as admin via the Supervisor passthrough
  ([ADR 0044](docs/adr/0044-single-port-onboarding-and-ha-ingress-auth.md)),
  yet the SPA still showed the onboarding wizard — which could not be completed
  because the Ingress session lapsed mid-flow, then dead-ended on a login
  screen the Ingress-only operator has no credentials for. Now `GET
  /api/v1/setup/status` reports `required: false` whenever the caller already
  carries an authenticated identity, and the SPA only renders the wizard when
  no one is logged in. Guided CCU/MQTT configuration under Ingress happens in
  Settings → CCUs (with the SSDP auto-discovery added in 0.20.0).
- **A momentarily lapsed session no longer dead-ends the SPA on the login
  screen.** On a `401` the SPA now re-probes `/auth/me` before giving up: under
  Ingress the Supervisor re-authenticates the request and the session survives;
  a genuinely stale session still falls through to the login view as before.

## [0.20.0]

### Added

- **CCUs are now discovered automatically on the LAN via SSDP/UPnP**
  ([ADR 0046](docs/adr/0046-ssdp-ccu-discovery.md)). The daemon periodically
  multicasts an M-SEARCH, follows each Homematic/OpenCCU central's
  `basic_dev.cgi`, and surfaces the matches in the UI — in Settings → CCUs and
  in the first-run onboarding wizard — so a CCU can be added with its host
  pre-filled instead of typed by hand. Found CCUs already configured are
  flagged; unwanted ones can be **ignored** (persisted, so they stop
  reappearing) and un-ignored later. Discovery accepts OpenCCU **and** classic
  eQ-3 / HomeMatic / RaspberryMatic centrals.
- New REST endpoints: `GET /api/v1/centrals/discovered`,
  `GET /api/v1/centrals/discovered/ignored`, and admin-gated
  `POST|DELETE /api/v1/centrals/discovered/{serial}/ignore`. REST `APIVersion`
  → `2.8.0`.
- New config `north.discovery.ssdp` (`enabled`, default true; `interval`,
  default 60s). Discovery only reads the network — nothing about the daemon
  leaves the LAN — and finds nothing where multicast is unavailable.

## [0.19.0]

### Changed

- **Login, OIDC, and first-run onboarding now live entirely in the Svelte
  SPA** ([ADR 0045](docs/adr/0045-login-and-onboarding-into-spa.md)). The SPA
  already handled login and OIDC; the four-step first-run wizard (admin →
  language → CCU → MQTT) is now a SPA view (`Setup.svelte`) that finalizes
  through a single atomic `POST /api/v1/setup`. The previous server-rendered,
  no-JS login/setup forms have been removed. The server-rendered surface
  shrinks to `/health` and `/about`, kept only as a no-JS diagnostic anchor for
  when the SPA bundle cannot load.
- REST `APIVersion` → `2.7.0` (capability addition: the setup endpoints).

### Added

- **`GET /api/v1/setup/status`** and **`POST /api/v1/setup`** — the SPA probes
  status on boot to decide between the onboarding wizard and the login screen,
  and finalizes onboarding atomically. `POST /api/v1/setup` is unauthenticated
  by necessity (no admin exists yet) but hard-gated: it returns 409 once any
  authentication source exists, so a second admin can never be registered
  through it.
- A dedicated per-IP login brute-force speed-bump on `POST /api/v1/auth/login`
  (preserved from the removed server-rendered login, now a REST middleware).

### Removed

- Server-rendered `/login`, `/logout`, `/login/oidc/*`, and the `/setup*`
  wizard, plus their templates and the wizard session store.

## [0.18.5]

Bundles the work developed as 0.18.1–0.18.5 (none were tagged individually).

### Fixed

- **Matter colour and colour-temperature writes now work.** The ColorControl
  command handlers (`MoveToHue` / `MoveToSaturation` / `MoveToHueAndSaturation`
  for RGB lights, `MoveToColorTemperature` for tunable-white lights) decoded the
  command payload into the wrong shape, so every colour or CT change requested
  from a Matter controller (Apple Home / Google Home colour picker) was silently
  rejected and the bulb never changed. The bridge now decodes ColorControl
  commands into typed requests and the cluster servers consume them, with an
  end-to-end test that exercises the real TLV-payload → dispatcher → device path.
  Read-side attributes (current hue/saturation/CT) were already correct, so this
  is a write-path-only fix.

### Added

- **Scheduler health component.** The per-central health view now carries a
  `scheduler` liveness row: if a periodic job has failed since the last health
  heartbeat the component reads degraded for that interval and recovers once a
  quiet interval passes (a failure delta against the cumulative
  `scheduler.failures` metric). This completes the health-producer coverage
  (MQTT, Matter and SQLite already reported status; REST and WebSocket stay
  metric-only by design, since their liveness is implied by the daemon serving
  `/health`).

### Changed

- **"Reload device" now refetches just that device.** Reloading a single device
  from the UI previously pulled the descriptions for *every* device on the same
  CCU interface; it now fetches only the target device and its channels
  (`GetDeviceDescription` over the device plus its `CHILDREN`). One unreachable
  channel is logged and skipped rather than failing the whole reload. The
  operator-visible result is unchanged — only less work on the CCU, which
  matters on large installations.
- **API schema completeness.** Nine response shapes the web UI carried as
  hand-written types — backups, incidents, rooms, functions, linkable channels,
  RPC-recording status, live-log records, edit-session, and inbox devices — are
  now defined in `assets/openapi.yaml` and consumed by the SPA from the
  generated client, so the documented contract and the UI stay in sync (API
  contract 2.5.0 → 2.6.0, additive). Two latent UI/daemon mismatches were
  corrected in passing: the inbox device's `central` field is optional (matching
  what the daemon serves), and the install-mode interface entry now carries its
  `observed` flag.

### Removed

- **Internal only:** deleted unused scaffolding that never had a production
  caller — the standalone `health.Connection`/`ConnectionRegistry` model
  (superseded by the live health tracker), the `ClientCoordinator.PollClients` /
  `SubscribeToHealthEvents` / `RestartClients` methods, and the never-published
  `DeviceStateChangedEvent`. None were part of the REST/WebSocket contract, so
  there is no API change and downstream clients are unaffected; the concept each
  embodied is recorded in `notes/parity/by_design.md`.

## [0.18.0]

### Added

- **Combined data points are now published.** Cover blinds expose a combined
  level/slats position and HmIP RGB lights expose a combined hue/saturation as
  their own data points — materialised through the device pipeline and surfaced
  as Home Assistant discovery sensors over MQTT (plus REST/WebSocket). (Mapping
  HS colour onto the Matter ColorControl cluster is a follow-up.)
- **Service and alarm messages carry a human-readable `display_name`.** The
  message code (e.g. `LOW_BAT`, `UNREACH`) is now translated (de/en) and added
  as an additive `display_name` field on the messages REST/SPA surface
  (north-bound API contract 2.4.0 → 2.5.0). The raw MQTT attribute form is
  unchanged, so existing automations keep working.

### Removed

- **Dropped the speculative HmIP-COOK ("hood") custom data point.** It had no
  counterpart in any of the HomeMatic reference sources — a placeholder added in
  the very first commit — and was never registered, so it was dead code.

## [0.17.5]

### Fixed

- **Matter: string attribute writes are no longer dropped to empty.** A UTF-8
  attribute write (e.g. `NodeLabel`) was read from the wrong decoded field and
  always stored as the empty string; it now keeps the written value.
- **Matter conformance sweep against matter.js HEAD.** Mirrored five upstream
  fixes the schema snapshot does not track: truncate decoded character strings
  at Information Separator 1 / 0x1F (§7.19.2.40); reject a write that carries a
  DataVersion against a wildcard endpoint, and clamp the status when a
  cluster-specific status is returned (§8.9.2.8.1 / §7.10.7); reject illegal
  Read/Subscribe paths (wildcard cluster with a concrete non-global attribute
  or event) up front with InvalidAction (§8.4.3.2); strip `IsUrgent` from event
  report paths (§8.9.3.4); and reject a VendorID outside 0x0001-0xFFF4 as an
  invalid device identity.

### Changed

- **Matter schema snapshot now records its provenance.** The embedded
  `matter-schema-snapshot.json` captures the matter.js source commit and the
  Matter spec revision (1.5.1) it was extracted at, so the pinned reference is
  traceable to an exact upstream commit.

## [0.17.4]

### Fixed

- **Device-detail header no longer collapses on a narrow content area.** When
  the page was shown in a narrow container (most visibly inside the Home
  Assistant Ingress iframe, where the sidebar leaves little width), the action
  buttons stayed on one row and squeezed the title/metadata column to a sliver,
  wrapping the model and description character-by-character. The header now uses
  a container query: it stacks (title + metadata on their own full-width rows,
  actions below) until the content area is wide enough, then restores the
  side-by-side layout with the actions top-right.

### Changed

- **Unified the list-view page headers behind a shared `PageHeader` component.**
  Devices, firmware, signal quality, system variables, programs, messages,
  inbox, un-ignore, backups, and the audit log now render their title +
  actions through one component with a single, robust responsive rule (the
  title never shrinks, so a crowded header wraps its actions onto their own
  line instead of squeezing). Behaviour is unchanged; the device-list title
  drops a stray `tracking-tight` to match the other headers.

## [0.17.3]

### Changed

- **Refreshed the embedded openccu-data extracts to 2026.6.1** — updated device
  translations (`translation_extract`) and easymode definitions
  (`easymode_extract`), with the manifest hashes regenerated to match. 2026.6.1
  also restores the curated model labels for HmIP-DLP, HmIP-UDI-SMI55,
  HmIP-SMO230 and HmIP-SWDO-PL-2 (the extract ships those models only as icons).

## [0.17.2]

### Fixed

- **Removed devices now disappear from Home Assistant immediately.** When a
  device was deleted from the CCU while the daemon was running, its MQTT
  HA-Discovery configs lingered on the broker — so the entities stayed visible
  as permanently "unavailable" until the next daemon restart (when the boot
  orphan-cleanup pass evicted them). The device-removed handler now retracts
  the device's discovery configs right away (empty retained payload), scoped to
  the device's own node_id so unrelated entities are never touched.

## [0.17.1]

### Fixed

- **Battery values now appear in the Signal-quality table.** The battery
  level read the derived `OPERATING_VOLTAGE_LEVEL`, but that is a *calculated*
  data point and the lookup only searched the VALUES paramset — so the column
  was always empty (most visibly for HmIP). The maintenance-channel lookup now
  also searches calculated data points. Low-battery detection additionally
  recognises HmIP's `LOWBAT` parameter (previously only BidCos `LOW_BAT`).

### Changed

- **Every sensible table column is now sortable.** Across all DataTable views
  (devices, firmware, signal quality, system variables, programs, messages,
  inbox, un-ignore, backups, audit, Matter, the settings admin tables, and the
  Diagnostics sub-tables) every data column now sorts on header click;
  selection and action columns stay non-sortable. Timestamp columns sort on
  their raw value while still rendering the formatted date.

## [0.17.0]

### Added

- **Shared, sortable, searchable tables across the Config UI.** A new
  `DataTable` design-system component gives every list view one consistent
  interaction model: click a column header to sort (▲/▼, keyboard-operable),
  a built-in search field, a responsive layout that reflows to cards on
  phones, and persisted sort/search. The device, system-variable, program,
  firmware, and signal-quality views all render through it.
- **Signal quality view (own nav entry).** Per-device RF reception strength
  (RSSI_DEVICE / RSSI_PEER, colour-coded by quality), **battery** state
  (colour-coded ≤20 % red / ≤55 % amber, from `OPERATING_VOLTAGE_LEVEL`),
  and reachability — searchable and sortable. Moved out of the Diagnostics
  page into its own top-level entry. Backed by `battery_level` / `low_battery`
  added to `GET /diagnostics/rssi` (API contract 2.3.0 → 2.4.0).

### Changed

- **Device list: cards ↔ table toggle.** The former single-column "list"
  mode is now a real table (multi-select, interface grouping, bulk actions
  preserved); the card grid stays as the alternative view. The view mode,
  search, sort, and filters are all remembered across reloads.
- **Firmware, programs, and system variables are now tables** with click-to-
  sort headers and search, replacing the previous card grids. System
  variables keep their inline value editor (now in the value cell). Each
  view also remembers its search, sort, and central/mode filter.
- **The remaining list/table surfaces now share the same DataTable too**:
  messages, the device inbox, the un-ignore picker, backups, the audit log,
  the Matter exposure list (with multi-select) and fabrics, the Settings
  admin tables (users, API tokens, CCUs, rooms/functions), and the
  Diagnostics sub-tables (interfaces, client health, RPC recordings) — all
  sortable and consistently styled.

## [0.16.1]

### Fixed

- **RF reception (RSSI) overview now works for HmIP devices.** The 0.16.0
  feature read the BidCos-RF-only XML-RPC `rssiInfo` matrix, which HmIP-RF
  answers with a generic fault — so HmIP setups saw "no data" after a
  multi-second delay. It now reports **per-device** reception strength
  (`rssi_device` = RSSI_DEVICE, `rssi_peer` = RSSI_PEER, dBm) plus
  reachability, read from the device model's maintenance channel with no CCU
  round-trip, so it covers HmIP and BidCos alike. The Config-UI Diagnostics
  section became a per-device "Signal quality (RSSI)" table that loads with
  the page.

### Changed

- **`GET /diagnostics/rssi` / `ccu.get_rssi_info` response shape.** Each
  device entry now carries `rssi_device`, `rssi_peer`, and `reachable`
  directly instead of a nested `partners` list. North-bound API contract
  version 2.2.0 → 2.3.0 (the removed `partners` field is the only
  non-additive part; oasdiff classifies it as non-breaking).

## [0.16.0]

### Added

- **RF reception matrix — WS command, REST endpoint, and Config-UI view.**
  Exposes the CCU's pairwise RF reception matrix (device ↔ communication-
  partner RSSI pairs) read from `Interface.rssiInfo`, across every central
  and RF interface; the CCU's 65536 "no data" sentinel is normalised to
  `null`. Reachable via the `ccu.get_rssi_info` WebSocket command, the
  admin-only `GET /diagnostics/rssi` REST endpoint, and an on-demand
  "Signal / RSSI matrix" section on the Config-UI Diagnostics page.
  Complements `ccu.get_signal_quality` (a flat per-device list) with the
  link-level matrix the CCU reports directly. Bumps the north-bound API
  contract version to 2.2.0.

### Changed

- **JSON-RPC permission errors are now distinguishable from generic
  failures.** A CCU JSON-RPC `error.code 400` ("access denied" —
  authenticated but the session's privilege level is too low) maps to the
  new `ErrPermissionDenied` sentinel and short-circuits retry, instead of
  being retried as a generic client exception. Surfaces a mis-configured
  user level instead of silently hammering the CCU.
- **SPA list search accepts regular expressions.** The device, system-
  variable, and program list filters now match by regex when the term is a
  valid pattern (e.g. `BidCos-RF\.MEQ`, `MEQ|HEQ`), and fall back to a
  case-insensitive substring match otherwise.
- **SPA favorites became a quick-control surface.** Pinned system variables
  can now be changed inline (toggle / number / select) directly on the
  start page instead of only linking back to the sysvar list. Inactive
  programs are visually dimmed in the program list for faster scanning.

### Removed

- **`north.ui.listen` config setting + `OPENCCU_LOOM_UI_LISTEN` env var.** The
  bootstrap UI (login / setup / health / about) has shared the REST listener
  since 0.14.0 (ADR 0044), so the separate UI bind address was a deprecated
  no-op. It is now removed entirely — config struct, bootstrap tier, the
  startup deprecation warning, restart-required handling, and the SPA i18n
  entries. `north.ui.enabled` (the bootstrap-surface on/off toggle) stays.
  Delete `north.ui.listen` from existing config files; the loader already
  ignores unknown keys, so no migration is required.

## [0.15.0]

### Added

- **`firmware.refresh` WebSocket command is now wired.** It re-pulls device
  descriptions (and with them the firmware-version fields) from the CCU across
  every central and interface. Previously the command was registered only as a
  no-op stub (the `FirmwareRefresher` was left nil). Mirrors aiohomematic
  `ws_refresh_firmware_data`.
- **Optional Matter TimeSynchronization cluster (0x0038), operator opt-in.**
  New `north.matter.enable_time_sync` flag (tri-state, default off) mounts the
  already-implemented TimeSynchronization cluster on the Matter Root endpoint.
  Off by default because it is optional-only on a RootNode and some controllers
  (e.g. Apple Home) may reject the bridge at pairing when it appears; enable
  only when a controller needs a time-sync surface. ARL (0x002B) and Actions
  (0x0025) remain intentionally deferred (no current use-case) — see
  `notes/parity/by_design.md`.
- **Matter cluster-revision parity tests** added for previously-untested
  clusters, closing the documented behavioural-parity test gaps against the
  matter.js schema snapshot.

### Changed

- **A manual device reload now also refreshes link-peer addresses.**
  `config.reload_device_config` / `ccu.reload_device_config` re-pull the device's
  link peers as part of the (already RPC-bound) reload, keeping link data current
  on demand — without adding a boot-time per-device RPC sweep across the whole
  fleet.

### Fixed

- **`GET /api/v1/incidents` now returns the recorded incidents.** The REST
  incident reader was wired to an empty MVP stub, so the endpoint (and the
  diagnostics envelope) always returned an empty list even though incidents were
  being persisted to SQLite. It now reads from the incident store across every
  central. Dead stub code (`ParamsetsAdapter`, `IncidentsAdapter`) removed.
- **OpenAPI spec now matches the server's actual JSON, removing the hand-written
  type divergence in the SPA.** Seven response schemas drifted from what the Go
  handlers emit — `openapi.yaml` was missing fields the server always sends
  (`DeviceSummary.central`/`model_label`/`model_icon`/`update_available`/
  `functions`/`has_sub_devices`/`master_pushes_config_pending`; `central` on
  audit/alarm/service entries; alarm `address`/`state_value`; service
  `description`/`priority`) and over-constrained others. The spec is corrected to
  the Go json tags, the generated client types are regenerated, and the SPA's
  `TODO(openapi-typescript)` overrides are removed (it now re-exports the
  generated types). Composite-key call sites guard the optional `central`. REST
  API version bumped 2.0.0 → 2.1.0 (additive).

## [0.14.6]

### Fixed

- **CLI tools now honor `OPENCCU_LOOM_DATA_DIR` without `--config` too.**
  Completing the 0.14.5 daemon fix: `hmcli` subcommands (`backup`, `config`)
  resolved their data directory via the bootstrap default `./var` when run
  without `--config`, so a containerised CLI run could open the wrong (empty)
  database instead of the daemon's `/data` store. `BootstrapConfig` now applies
  the bootstrap-tier env overlay (`OverlayFromEnv`), and `loadBootstrapForCLI`
  calls it (and validates), so the CLI and daemon resolve the same data
  directory.

## [0.14.5]

### Fixed

- **Critical: configuration and database were lost on every restart / add-on
  update.** When the daemon started without a config file — the standard HA
  add-on case — it used `config.Default()`, which ignored the environment
  overlay and therefore `OPENCCU_LOOM_DATA_DIR`. The add-on sets that to `/data`
  (the only persistent location), but the daemon fell back to the default
  `./var` inside the ephemeral container, so the SQLite database lived there and
  every restart/update started on an empty database — losing configured CCUs,
  users and all SPA-edited config. The no-config-file path now applies the env
  overlay (`config.DefaultWithEnv`), so `OPENCCU_LOOM_DATA_DIR` is honoured and
  state persists under `/data`. **After updating, re-create your CCU and admin
  user once more; from then on they survive restarts and updates.**

## [0.14.4]

### Fixed

- **mDNS discovery no longer advertises container-internal addresses.** When the
  daemon runs as a Home Assistant add-on with host networking, the zeroconf
  advertiser put every host interface IP into the A-record — including the
  `hassio`/Docker bridge addresses (e.g. `172.30.232.1`). A discovering client
  (homematicip_local) resolved the daemon to that internal address and failed to
  connect (`[404] GET http://172.30.232.1:8080/api/v1/info`). The advertiser now
  publishes only routable LAN addresses (interfaces named like `docker*`, `br-*`,
  `veth*`, `hassio`, `virbr*`, … are excluded, as are loopback/link-local IPs)
  while still broadcasting on every interface so peers on a container bridge
  still receive the announcement. If no routable address survives the filter it
  falls back to the previous all-interfaces behaviour rather than going silent.
- **Config fields without a description / translation.** The REST `public_url`,
  `tls_cert_file` and `tls_key_file` fields rendered with a machine-humanised,
  untranslated label and no help text. They now have proper labels and inline
  help in English and German (the TLS help also explains that the cert/key paths
  are both the HTTPS on/off switch and the location an uploaded certificate is
  written to and hot-reloaded from).

### Added

- **Guard against undescribed config fields.** `TestConfigFieldsHaveLabelsAndHelp`
  (in `tests/contract/`) walks every `cfg`-tagged config field and fails the
  build unless each has a `config.field.<path>` label AND a `config.help.<path>`
  description in both the EN and DE catalogues of `assets/ui/src/lib/i18n.ts`. A
  matching Critical Rule was added to `CLAUDE.md`.

## [0.14.3]

### Changed

- **The HA Ingress auth passthrough is now ON by default in the HA add-on.**
  `north.rest.auth.ha_ingress.enabled` became tri-state: unset → on when the
  build is supervised (the HA add-on, where `panel_admin: true` restricts
  Ingress to HA admins), off in a plain build / Docker image; an explicit
  true/false still overrides. Opening the add-on through the Home Assistant
  panel now logs you straight in as admin — no setup, no login page — which
  also avoids the first-run `/setup` redirect. All the gates are unchanged
  (supervised + real RemoteAddr in `trusted_proxy_cidr` + `X-Ingress-Path`; a
  real token/session still wins), so it stays inert outside genuine Supervisor
  traffic (e.g. the CCU/RaspberryMatic add-on, which is not behind Ingress).
  Set `enabled: false` to opt out and use local/CCU login instead.

## [0.14.2]

### Fixed

- **Onboarding works behind HA Ingress.** Folding the server-rendered bootstrap
  surface (login / setup / about) onto the Ingress port in 0.14.0 exposed its
  absolute URLs to the Ingress prefix: form POSTs (e.g. `/setup/admin`) and
  links (`/app/`, `/login`) resolved against the Home Assistant origin instead
  of the add-on's Ingress path — so the setup wizard could not be submitted (you
  stayed stuck on it) and the footer `/app/` link reloaded the whole HA page
  inside the panel. The bootstrap pages now emit Ingress-prefix-aware URLs (a
  `<base href>` derived from `X-Ingress-Path` plus relative links), and every
  server-side redirect carries the prefix.

## [0.14.1]

### Fixed

- **First-run redirect no longer traps CCU/OIDC users on `/setup`.** 0.14.0
  redirected the SPA entrypoint to the setup wizard whenever no *local* admin
  existed — but in the HA add-on CCU-delegated login (ADR 0043) is on by
  default, so operators authenticate with their CCU account and never create a
  local admin. They landed on the "Create administrator account" wizard after
  updating. The redirect now fires only when there is genuinely no way to
  authenticate: no local user (YAML or DB) AND no CCU auth AND no OIDC.
- **Setup wizard step indicator showed `Step {current} of {total}` literally.**
  The server-rendered `t` template helper could not substitute placeholders. It
  now accepts `(name, value)` pairs and the wizard passes the step numbers, so
  it reads e.g. "Step 1 of 4".

## [0.14.0]

### Added

- **Opt-in Home Assistant Ingress auth passthrough (`north.rest.auth.ha_ingress`,
  ADR 0044).** When running as the supervised HA add-on, a request proxied by
  the HA Supervisor can be accepted as an authenticated admin without a local
  login. OFF by default; it only activates when all of these hold: the build is
  supervised, the request's real peer (`RemoteAddr`, never `X-Forwarded-For`)
  is inside `trusted_proxy_cidr` (default `172.30.32.0/23`), and the
  Supervisor's `X-Ingress-Path` header is present. A real Bearer token or
  session always wins (the passthrough is a fallback). Security contract: the
  add-on keeps `panel_admin: true`, so only HA admins reach Ingress. Audited as
  scheme `ingress`, subject `ha-ingress`.

### Changed

- **Single-port onboarding.** The server-rendered bootstrap surface (login,
  first-run `/setup` wizard, about, OIDC) is now served on the REST/SPA
  listener (`:8080`) instead of a separate `:8081` server, so the entire
  onboarding works through one port / HA Ingress. On first run (no admin user)
  the SPA entrypoint redirects to `/setup`. The HA add-on no longer exposes
  `8081/tcp` and pins `panel_admin: true`. **`north.rest.listen` is unchanged;
  `north.ui.listen` is deprecated** — it is ignored (with a startup warning);
  the UI shares the REST listener.

### Fixed

- **Missing config-field descriptions in the SPA.** The CCU-auth fields
  (`north.rest.auth.ccu.*`) rendered without a description because their
  `config.help.*` i18n keys were absent. Added labels + descriptions (en + de)
  for every CCU-auth and HA-Ingress field.

## [0.13.3]

### Fixed

- **The Home Assistant add-on image now publishes.** The `home-assistant/builder`
  action proved too fragile across the 0.13.0–0.13.2 attempts: it derives its
  builder-image tag from the action ref, so SHA pins and unpublished
  builder-image versions both 404'd, and once that was resolved the pinned
  version had dropped the `--all` flag the step relied on. Switched to building
  the add-on image with plain `docker/build-push-action` (buildx) — the same
  approach the working `go-daikin2mqtt` add-on uses. The add-on Dockerfile still
  COPYs the daemon binary out of the published release image; `provenance: false`
  keeps it a plain image the HA Supervisor can pull. amd64 only (matching the
  amd64-restricted daemon image).

## [0.13.2]

### Fixed

- **Attempted to publish the Home Assistant add-on image** (completed in 0.13.3).
  Pinned the `home-assistant/builder` action to `@2026.02.1` so its builder image
  resolves, but that version had removed the `--all` flag the step passed, so the
  build still aborted — superseded by dropping the builder action entirely in
  0.13.3. Daemon binaries, Docker image, and CCU/RaspberryMatic add-on were
  unaffected.

## [0.13.1]

### Fixed

- **Attempted to publish the Home Assistant add-on image** (completed in 0.13.2).
  The `home-assistant/builder` action was pinned by commit SHA; the action
  derives its builder-image tag from its own ref, so the SHA pin pulled a
  non-existent `amd64-builder:<sha>` ("manifest unknown") and the 0.13.0 release
  shipped without the HA add-on image. Re-pinned the action by version tag — but
  that version's builder image was itself unpublished, so the fix only landed in
  0.13.2. The 0.13.0 daemon binaries, Docker image, and CCU/RaspberryMatic
  add-on were unaffected.

## [0.13.0]

### Fixed

- **The SPA "CCU login" tab can now actually save.** The CCU-auth provider
  (`north.rest.auth.ccu`) was wired into the SPA — tab, editor, i18n — but the
  section was never registered in the backend's section registry, so a save
  was rejected at the allow-list gate with `API 400 …: north.rest.auth.ccu`
  ("Unknown section"). The section is now registered everywhere its OIDC
  sibling is (`configstore.AllSections`, `applySection`/`marshalSection`,
  `sectionTarget`, and the REST `validateSection`) and flagged
  restart-required. Registering it also lets the editor's longest-prefix guard
  attach the `north.rest.auth.ccu.*` fields to the "CCU login" tab only,
  instead of double-rendering them under the REST tab.
- **Tri-state config toggles no longer collapse their "unset" default.** The
  SPA section editor rendered every `*bool` field (e.g.
  `north.rest.auth.ccu.primary`, whose documented default is "CCU-primary")
  as a plain checkbox, so saving silently wrote `false` for a field the
  operator never touched — overriding the daemon's nil default. `*bool` fields
  now render as a "Default / On / Off" select that preserves the unset state
  (persisted as `null`, letting the daemon apply its own default). Concrete
  `true`/`false` values still round-trip unchanged.
- **Install mode (teach-in for new devices) now works.** Starting pairing from
  the SPA inbox failed with `API 502 /install-mode: adapter: not implemented in
  MVP`. Two gaps combined: the per-interface install-mode data points were
  never constructed in production (so `GET /install-mode/interfaces` was always
  empty and the SPA fell back to a CCU-wide toggle), and that CCU-wide toggle
  was an unimplemented stub. There is no CCU-wide install mode on a CCU — it is
  always per-interface — so one install-mode data point is now wired per
  pairing-capable radio (HmIP-RF, BidCos-RF, BidCos-Wired), each writing to its
  own interface backend. The SPA inbox now lists the radios and opens pairing on
  the selected one.

### Removed

- **The CCU-wide `GET`/`POST /install-mode` endpoints.** They modelled a
  CCU-wide pairing toggle that does not exist on the CCU (install mode is
  per-interface) and were never functional. Use `GET`/`POST
  /install-mode/interfaces` instead. API version bumped to `2.0.0`.

## [0.12.0] - 2026-06-24

### Added

- **CCU as an authentication provider (ADR 0043).** Logins are validated
  against the CCU's own user database (a transient `Session.login`) and the
  CCU `UserLevel` is mapped to a Loom role (8→admin, 2→operator, 1→viewer).
  The CCU is only contacted at login (the issued Loom session carries the
  rest). Configurable via `north.rest.auth.ccu` (also editable in the SPA's
  "CCU login" tab; restart-required) and surfaced as `auth.ccu.v1`:
  - **`enabled`** is tri-state — unset defaults to the build stamp, so the
    CCU add-on ships with CCU login **on** and a plain build keeps it off.
  - **`primary`** is tri-state — unset defaults to **true** (the CCU is the
    primary source, local users are the break-glass fallback); `false`
    flips to local-first. Break-glass holds in both orders because every CCU
    failure (wrong password **or** CCU down) falls through to the local
    store.
- **Config-UI parity push toward the legacy CCU WebUI.** Six gaps the SPA had
  against the CCU WebUI are now closed:
  - **Rooms & functions (Gewerke) management.** Create / rename / delete rooms
    and functions from a new Settings → "Groups" tab, on top of the existing
    device-assignment path. CCU-side via new ReGa scripts
    (`create_room`/`rename_room`/`delete_room` + function equivalents), exposed
    as `POST/PATCH/DELETE /rooms` and `/functions` (operator-gated, per-CCU).
  - **Favorites / start page.** Pin devices (and system variables) to a new
    Favorites view; persisted server-side per user via a generic
    `/me/preferences/{key}` store so they follow the operator across browsers.
  - **Direct-link sender side.** The link editor now renders the sender-side
    LINK paramset in addition to the receiver side, when the sender channel
    carries one.
  - **Self-service password change** (`PATCH /auth/me/password`) and a runtime
    **log-level override** UI (per-subsystem, admin-gated).
  - **Audit export.** The change-history view gains a date-range filter, server
    pagination over the full SQLite history, and CSV export
    (`GET /audit?format=csv`).
  - **Targeted teach-in.** Interface-selective install mode plus pairing a
    device by serial (`POST /devices/{addr}/install-mode`).
- **HTTPS for the daemon's own listener.** Set `north.rest.tls_cert_file` +
  `tls_key_file` to serve the REST API and SPA over TLS on the same port. The
  certificate hot-reloads on change or after an admin upload
  (`POST /admin/tls/certificate`) without a daemon restart.

## [0.11.3]

### Fixed

- **`HmIP-FWI` `CODE_ID` no longer drops the idle value `31` (#3238).** The CCU
  declares `MAX=21` for the fingerprint reader's `CODE_ID`, but the device
  reports `CODE_ID=31` in idle/standby (5-bit field, `31` = no active code). The
  too-low maximum dropped the idle value at ingestion, so the data point kept the
  last recognized code and never returned to `31`. A paramset patch now widens
  `CODE_ID` `MAX` to `31` for `HmIP-FWI` on channel 0 (`MIN` stays at `1`). This
  fixes the event-driven flow (device-reported codes); it is unrelated to the
  optimistic send/rollback path addressed in `0.11.2`. The paramset cache schema
  version was bumped to `2` so the corrected bounds are rebuilt from the CCU.

## [0.11.2]

### Fixed

- **Optimistic values now roll back immediately when a batched / timer write is
  rejected by the CCU, instead of lingering for the full 30 s optimistic-update
  timeout (#3238).** Two send paths staged an optimistic value but did not
  revert it when the wire call failed (e.g. an XML-RPC `RESPONSE_NAK` after all
  retries were exhausted): (1) the atomic `ON_TIME` + `STATE` switch turn-on
  (`turnOnWithTimer`) discarded the rollback closure returned by
  `ApplyOptimistic`, and (2) the `CallParameterCollector` rollback fired a no-op
  for any data point that had already been staged via `sendAndObserve` before
  being added to the collector (the re-entrant `ApplyOptimistic` burst-skipped
  and returned a no-op rollback). Both now roll back on send error with
  `reason=send_error`, matching the direct-send path and `Channel.Set`. The
  burst-skip still does not inflate `PendingSends`, so a single CCU echo settles
  the tracker without a spurious timeout rollback (#3049).

## [0.11.1]

### Fixed

- **`VirtualDevices` no longer report an implausible `0` after a CCU restart via
  the per-parameter VALUES fallback (#3228).** The `0.10.1` seeder rework (v2.5)
  fixed the bulk path, but on a cache-miss `runLoadValuesParamset` still issued a
  `GetParamset(VALUES)` fallback. A virtual heating group's `ACTUAL_TEMPERATURE`
  is aggregated by the CCU and has **no physical device** behind it, so that
  fallback can only ever return the CCU-internal default (`0`) — reported with
  `*_STATUS = NORMAL`, so the status cannot be used to reject it. Because
  `GetParamset`/`GetValue` cannot deliver a device-fresh value for an interface
  without a backing device, the VALUES fallback is now **skipped entirely for
  `VirtualDevices`**: the bulk seeder — which already gates these data points on a
  valid `LastTimestamp()` — is the single source of truth, and the data point
  stays unobserved (sentinel) until a real reading arrives via the event callback.
  Physical interfaces are unaffected: there `GetParamset` can read the device, so
  the fallback is retained.

## [0.11.0]

### Added

- **J5 — `unique_id` on the week-profile / schedule-channel-switch surfaces.**
  `WeekProfileResponse` now carries a top-level `unique_id` for the device-level
  week-profile sensor entity
  (`loom_week_profile_<device-addr>_week_profile`), and every
  `available_target_channels` (`TargetChannelSummary`) entry carries a
  `unique_id` for its schedule-channel-switch entity
  (`loom_schedule_channel_switch_<device-addr>_schedule_channel_lock_<channel_key>`).
  Both keys are built over the owning **device** address (not the channel
  address), `required`, and **bit-identical** to the keys a client otherwise
  synthesises — verified by two new routing-key golden-fixture cases that the Go
  golden test and the `make routing-key-parity` Go↔Python check both pin
  (21/21). This closes the last schedule-path `unique_id` gap from J3, so a
  client drops its own key recomputation (`canonical.py`) from the schedule
  path too. APIVersion → 1.21.0.

## [0.10.1]

Rework the `fetch_all_device_data.fn` ReGa bulk seeder (v2.5) to stop the
post-CCU-restart `0`/`0.0` placeholder at the source (reference issue #3228), and
revert the flawed `#149` interim fix.

### Fixed

- **Bulk seeder no longer drops legitimate zero readings; placeholders are kept
  out at the source.** The `#149` change skipped empty values via
  `if (vDPValue == "") { bHasValue = false }`. In ReGa an operation's type is
  determined by the left operand and an empty string coerces to `0`, so
  `vDPValue == ""` is also true for every numeric `0`/`0.0` — that skip therefore
  dropped *all* legitimate zero readings from the bulk result, not just
  not-yet-measured ones. The seeder (v2.5) now (a) gates `VirtualDevices` data
  points on a valid `LastTimestamp()` so heating groups that carry a `Timestamp()`
  but no real reading after a restart stay out of the bulk result entirely, and
  (b) coerces an empty value to `0` only when it is a genuine string script
  variable (`VarType() == 4`), preserving real numeric zeros. The north-bound
  `IsValid()` availability gating introduced in `#149` is unchanged and keeps
  placeholders out on the consumer side.

## [0.10.0]

Close the external-client backlog waves **J** (`unique_id`-ownership) and **K**
(CCU-domain derivation into the daemon, "dumber client") from
`notes/reference/external-client-asks.md`. Together they let the Loom path in
`py-openccu-loom-client` drop its `aiohomematic` runtime coupling. APIVersion →
1.20.0.

### Added

- **J1 — `unique_id` on every REST summary + the snapshot, guaranteed
  non-empty.** The canonical loom routing key (the `routingkey.CanonicalUniqueID`
  result, identical to the WS payloads) now rides `DataPointSummary`,
  `CustomDPSummary`, `ProgramSummary`, `SysvarSummary`, `CalculatedDPSummary`,
  `EventGroupSummary` and the nested snapshot data points. The owning central's
  serial suffix reaches the handlers through a new `SerialSuffix(central)` method
  on the `DeviceIndex` / `HubIndex` facades. The field is now `required` (REST +
  the two core WS payloads) and **guaranteed non-empty** by a serial-readiness
  gate: `WireHub`/`resolveCCUSerial` resolves the CCU serial — the central-id
  slot of every hub/internal/virtual-remote key — with a bounded retry before any
  device is loaded; if it cannot be resolved the bring-up gate re-waits, so a
  central never serves entities with an unresolved serial. A client can now drop
  its own key-recomputation fallback.
- **J2 — automatic Go↔Python routing-key parity check.** `script/routing_key_parity.py`
  (`make routing-key-parity`) runs aiohomematic's current `generate_unique_id` /
  `generate_channel_unique_id` over the shared golden-fixture inputs and fails on
  any mismatch. Combined with the existing Go golden test (Go == fixtures), this
  pins Python == fixtures ⇒ automatic cross-repo parity, closing the previous
  manually-synced-fixtures gap.
- **J3 — `unique_id` on calculated data points and event groups.** Calculated
  DPs carry the same canonical key as generic DPs; a new
  `event.Group.CanonicalUniqueID` keeps the event-group key convention in Go.
- **K3 — derived `update_status` on `DeviceSummary`.** A new
  `up_to_date | update_available | installing` field
  (`hmenum.DeriveDeviceUpdateStatus`) collapses the raw CCU firmware phases, so
  a client renders the update entity without carrying the phase-classification
  sets itself. The `DeviceUpdateStatus` enum is exported via `enums.json`.
- **K4 — hub pseudo-addresses as named constants + schema export.**
  `HubAddress` / `InstallModeAddress` / `ProgramAddress` / `SysvarAddress` are
  now named constants in `internal/routingkey` and ship in a `pseudo_addresses`
  block of `assets/schemas/enums.json`, so a wire client reads them from the
  daemon contract instead of `aiohomematic.const`.
- **K1 — primary-channel marker + climate vocabulary enum.**
  `ChannelSummary.is_custom_dp_primary` surfaces
  `device.Channel.IsCustomDPPrimaryChannel`, the daemon-derived "device primary
  channel" marker. New `ClimateMode` (`auto|heat|cool|off`) and `ClimateProfile`
  (`none|away|boost|comfort|eco|week_program_1..6`) enums in `pkg/hmenum`
  (exported into `enums.json`) publish the closed climate vocabulary for typed
  client dispatch; `climate.Mode` / `climate.Profile` are now aliases of them
  (single source). Custom-DP channel composition (`channels`) and the per-device
  available subset (`config.hvac_modes` / `preset_modes`) were already on the
  wire. The finer field→parameter composition map is a **deliberate non-goal**
  (`notes/parity/by_design.md` → `BD-North-CustomDPCompositionMap`): it would
  contradict the K2 state normalisation and leak the internal profile graph onto
  the wire without unblocking any client.

### Changed

- **WS `unique_id` is now always present.** `DataPointValueChangedPayload` and
  `CustomDataPointStateChangedPayload` dropped `omitempty` on `unique_id` — the
  daemon is the sole owner of the key, so clients consume it unconditionally (an
  empty string signals an unresolved central serial, not "field absent").

### Notes

- Asks **J2** (routing-key drift-guard contract test), **J4** (channel metadata
  in the nested snapshot) and **K2** (normalised typed Custom-DP state) were
  already satisfied in the codebase and are re-marked as such in `asks.md`; this
  release adds no code for them beyond `event.Group.CanonicalUniqueID` extending
  the J2 key-convention coverage.

## [0.9.1]

### Fixed

- **Complete the published contract for the 0.9.0 wire gaps (openapi.yaml
  drift).** D1–D3 shipped in the Go implementation and `wsapi.json` but their
  schemas were never added to `assets/openapi.yaml`, so generated client type
  packages (`openccu-loom-types`) could not see them — and the types
  regeneration broke outright on D1 (`gen_ws` could not resolve the broadcast
  payload). The daemon binary already emitted these fields at runtime; this
  release only corrects the published contract + schema digest.
  - **D1** — added the `HubSystemUpdateChangedPayload` schema (referenced by the
    `hub.system_update_changed` broadcast in `wsapi.json`).
  - **D2** — added `value_translations` (object) to `DataPointSummary`.
  - **D3** — added `functions` (array) to `ChannelSummary`.
  - Regenerated the contract `SchemaDigest`; APIVersion stays `1.19.0` (no
    surface change — these fields were already served, just undocumented).

## [0.9.0]

### Added

- **Close the post-`asks.md` north-bound wire gaps D1–D4** discovered during
  the `py-openccu-loom-client` integration. APIVersion → 1.19.0.
  - **D1 — `hub.<central>.system_update` WebSocket broadcast.** The sixth hub
    singleton now pushes on firmware-/system-update state changes, like the
    five existing hub topics (`alarm_messages`, `service_messages`, `inbox`,
    `metrics`, `connectivity`). Wired off the hub model's `Update.OnUpdate`
    hook with a `HubSystemUpdateChangedPayload{current_firmware,
    available_firmware, update_available, in_progress}`, so a client can drop
    its update-status poll loop.
  - **D2 — `value_translations` on data-point summaries.** ENUM data points in
    `GET .../data-points` now carry an optional `value_translations` map (raw
    `VALUE_LIST` entry → localised label, resolved in the request locale via
    the OCCU `parameter_values_<locale>` table). Only entries that actually
    translate are included; clients fall back to `value_list` for the rest.
    Mirrors aiohomematic's per-DP `value_translations`.
  - **D3 — `functions` on channel summaries.** `ChannelSummary` now serialises
    `functions[]` (the channel-level twin of `DeviceSummary.functions`), so
    clients can map "Gewerke" at channel granularity instead of folding up to
    the device.
  - **D4 — OpenAPI: document `SchemaField.default`.** The `GET /config/schema`
    handler already emitted a per-field `default`; the `SchemaField` schema now
    declares it (optional, `nullable`) so strict validators and generated types
    accept the real response.

### Changed

- **Light saturation is now HA-canonical 0..100 throughout `custom/light`
  (D5), matching aiohomematic and the documented `ColorHS` contract.**
  Previously `ColorLight` (and the `EffectLight` / `RGBW` paths that inherit
  its state) emitted the raw wire `0..1` SATURATION fraction into the
  HA-canonical `color.s` field, while `FixedColorLight` and the `combined`
  HS-colour DP already emitted `0..100` — an internal inconsistency that broke
  the nested `color:{h,s}` round-trip for external clients. `ColorLight.Color`
  / `SetColor`, `FixedColorToHS` / `HSToFixedColor`, the Matter
  saturation↔`CurrentSaturation` conversions and the north-bound `set_color`
  operation (saturation default now `100`) all speak `0..100`; the wire
  SATURATION DP is still written as the `0..1` fraction. **External clients
  that sent `set_color` saturation as `0..1` must switch to `0..100`.**

## [0.8.0]

### Added

- **Close the HA-client wire gaps G2/G4/G5/G6** (see
  `notes/parity/ha-client-wire-gaps.md`). These unblock the
  `openccu-loom-client` / Home-Assistant *Homematic(IP) Local* drop-in by
  serializing already-modelled data into the north-bound contract. APIVersion
  → 1.18.0.
  - **G2 — text-display option lists.** The text-display CDP state now carries
    `available_background_colors`, `available_text_colors`,
    `available_alignments`, `available_repetitions` and `available_intervals`
    alongside the existing icon/sound lists, so the notify entity can build its
    per-option pickers.
  - **G4 — hub singleton data points — `GET /api/v1/hub/data-points`.** A single
    aggregating endpoint returns the hub singletons (alarm/service messages,
    inbox, firmware update, metrics, per-interface connectivity and
    install-mode) per central, so a client hub coordinator can be built from one
    fetch (re-enabling its orphan-entity cleanup).
  - **G5 — per-device event groups —
    `GET /api/v1/devices/{addr}/channels/{no}/event-groups`.** Projects a
    channel's keypress/impulse/device-error event groups (`kind`, `event_types`,
    `parameters`, `available`, `last_triggered_event`) so the `event` platform
    gets its bootstrap entities.
  - **G6 — hub-singleton push topics.** New WebSocket broadcasts
    `hub.<central>.alarm_messages`, `.service_messages`, `.inbox`, `.metrics`
    and `.connectivity.<interface_id>` let the client drop its 30 s hub-refresh
    poll loop.

### Changed

- **The per-parameter `VALUES` fallback now loads the whole channel paramset in
  one `getParamset` call instead of one `getValue` per parameter.** The bulk
  `fetch_all_device_data` seed only ships data points that already carry a
  non-zero value, so the fallback runs for every not-yet-measured parameter;
  batching the channel's `VALUES` paramset warms every still-unloaded sibling at
  once. The singleflight key stays per-parameter and sibling fills are gated on
  not-yet-observed, so a bulk read never clobbers a restored / already-known
  value (restore-first / reference #3228). See `notes/parity/by_design.md`
  (BD-CCU-ValuesBulkParamsetLoad). No API change.

### Notes

- Verification of the original gap catalogue reclassified three items (now
  documented in `notes/parity/ha-client-wire-gaps.md`): **G1** is an HS-colour
  shape mismatch fixable client-side (colour-temp/effect read-back already
  works), **G3** (sysvar `extended` marker) is already implemented end-to-end,
  and **G7** (generic `set_on_time`) is reachable through the existing generic
  value route. No daemon change was required for these.

## [0.7.1]

### Added

- **REST endpoints for surgical config reload — `POST
  /api/v1/devices/{addr}/reload` and `POST
  /api/v1/devices/{addr}/channels/{channel}/reload`.** These expose the
  existing per-device and per-channel config reload over REST (previously the
  `config.reload_device_config` / `config.reload_channel_config` commands were
  WebSocket-only). Closes a parity gap for the REST-only consumers (the Python
  client and the Home-Assistant loom backend), which could otherwise only
  trigger the coarse global `POST /devices/refresh`. APIVersion → 1.17.0.

## [0.7.0]

### Added

- **Device-action services — parity with the Home-Assistant integration's
  service surface.** A batch of operator actions that previously had to be
  driven by hand via raw parameter writes are now first-class commands. The
  device-type actions are surfaced through the existing `cdp.invoke` WebSocket
  command (mapping new operation strings onto the already-present custom
  data-point domain methods):
  - **Climate away-mode** — `enable_away_by_calendar` (away until an end
    timestamp), `enable_away_by_duration` (away for N hours), and
    `disable_away`.
  - **On-time** — `set_on_time` for lights, switches and (irrigation) valves
    ("turn on for a duration", reverting automatically).
  - **Cover** — `set_combined` to set blind position and slat tilt atomically.
  - **Siren** — `turn_on` (with acoustic/optical selection and duration) and
    `stop`.
  - **Text display** — `send_text`, `clear_text` and `commit` for HmIP-WRCD.
- **Channel-level config reload — `config.reload_channel_config` /
  `ccu.reload_channel_config`.** Re-pulls the VALUES/MASTER/LINK paramset
  descriptions and MASTER values for a single channel and re-materialises its
  data points, the surgical counterpart to `reload_device_config`. WS-only,
  mirroring the reference.
- **Central links — `central.create_links` / `central.remove_links` /
  `central.links_status`** (WS), wrapping the existing central-links domain;
  the REST `/devices/{address}/central-links` surface already existed.
- **Session recording — `recording.start` / `recording.stop` /
  `recording.status`** (WS) to drive the built-in RPC session recorder across
  every central (multi-CCU-safe).
- **Schedule copy — `schedules.copy` / `schedules.climate.copy_profile`** (WS,
  plus `POST .../schedules/copy` and `POST .../week_profile/copy`): copy a whole
  device schedule between devices, or one climate profile between
  channels/profile slots.
- **Force a system-variable refresh — `sysvars.fetch`** (WS, plus
  `POST /sysvars/fetch`): re-pull all system variables from the CCU on demand.

### Notes

- These services bring OpenCCU-Loom's command surface in line with the
  homematicip_local HA-service set; the underlying behaviour is ported 1:1 from
  the reference. Services that are pure Home-Assistant glue, or that are already
  covered by the generic value/paramset surface, were intentionally not added.

## [0.6.0]

### Added

- **Device-definition export — produce pydevccu / godevccu fixtures straight
  from a live CCU.** A new `GET /api/v1/devices/{addr}/export-definition`
  endpoint (plus the `devices.export_definition` WS command and an
  `hmcli export-def` subcommand) fetches a device's raw description and the
  descriptions of all its channels and their non-LINK paramsets directly off
  the CCU, anonymises every address behind a single random `VCU` id, and
  returns a zip containing `device_descriptions/{model}.json` and
  `paramset_descriptions/{model}.json`. The JSON members are **byte-for-byte
  identical** to aiohomematic's `export_device_definition` (a new
  `internal/orderedjson` package reproduces orjson's member-order-preserving
  output, including its float repr), so the archive drops straight into
  pydevccu / godevccu as a device fixture. To preserve the CCU's wire member
  order the export reads descriptions over a new order-preserving RPC path
  (`InterfaceClient.CallOrdered`) on the XML-RPC and BIN-RPC transports —
  descriptions never travel over JSON-RPC, so that transport is intentionally
  not wired (see `notes/parity/by_design.md`).

- **Persistent login sessions — a daemon restart no longer logs everyone out.**
  Browser auth sessions are now SQLite-backed via a save-through cache: the
  in-memory store stays the hot path for speed, each issued/revoked session is
  best-effort mirrored to a new `auth_sessions` table, and the store hydrates
  active sessions from disk on boot. A background sweep (and lazy eviction on
  lookup) purges expired rows. A DB hiccup at login still lets the login
  succeed — only that one session's cross-restart durability is lost.
  Dynamically created API tokens were already SQLite-backed; the in-memory
  YAML-seeded Basic user store is unchanged. See ADR 0041.

- **Reverse-proxy support for the CCU add-on's "Open Config UI" button via
  `north.rest.public_url`.** Behind a TLS-terminating reverse proxy (Traefik,
  nginx, …) the add-on landing page previously linked the button at
  `http://<host>:8080/app/` — a direct host:port heuristic that the public
  side cannot reach (the proxy routes 443, not 8080, and forces `http`). Set
  `north.rest.public_url` (e.g. `https://loom.example.de`) and the daemon
  writes the resolved Config-UI URL to a hint file in its data dir that
  `config.cgi` links at instead (`<public_url>/app/`). Empty (the default)
  keeps the existing heuristic, which stays correct for a LAN-direct install.
  The field is editable in the SPA and is restart-required.

### Changed

- **Consistent operating concept across the whole Config UI (SPA).** A UX audit
  found the screens had drifted apart because there was no shared vocabulary for
  the recurring states, so every view hand-rolled its own. New shared
  `LoadingState` / `EmptyState` / `ErrorState` components (plus a `Spinner`) now
  back every list and detail screen, so loading, empty and error surfaces look
  and behave the same everywhere (the error surface always shows a localized
  "Error: …" with an optional retry). Action results (save / delete / create /
  run / restore) now consistently use toast notifications instead of a mix of
  toasts and easy-to-miss inline header banners; every destructive action is
  guarded by the shared confirm dialog (including the previously-unguarded
  program "Run"). Several real dark-mode bugs are fixed — `Input`/`Select`
  rendered typed text black-on-dark, and a few widgets used hardcoded colours
  that didn't invert — and raw `<button>`/`<select>`/`<input>` elements were
  replaced with the design-system primitives. Accessibility was tightened
  (tab strips expose `role="tab"`/`aria-selected`, a skip-to-content link, and
  per-route document titles), and two stray hardcoded German strings were
  localized. The daemon restart that previously could fire silently from the
  app-wide banner now surfaces its result.
- **Devices now appear with their names on a cold boot — the daemon waits for
  the CCU to be ready instead of racing it.** When OpenCCU-Loom co-boots with a
  (re)booting CCU, the backend answers `listDevices` / `Device.listAllDetail`
  with http 503 for ~a minute while ReGaHss warms up. Previously the device
  load and the name load (a separate JSON-RPC path) warmed up at different
  times, so devices could appear without their CCU-assigned names until a
  restart. Each central's southbound bring-up is now gated on the CCU's own
  readiness endpoint (`GET /ise/checkrega.cgi` returning `OK` — the marker the
  OCCU WebUI boot page itself polls): names load first, then devices, once,
  against a ready CCU, so devices are created already named. The daemon's
  north-bound surface (REST/SPA/health) comes up immediately and shows a
  "waiting for CCU" state per central while it waits (which never trips
  `/health` to 503). The wait is indefinite — a slow CCU is never abandoned
  into a half-loaded state. The same gate guards mid-life reconnects after a
  CCU reboot. This homogeneous gate replaces the previous partial-load
  background retry; only a thin retry for residual per-interface RPC lag
  remains.

### Fixed

- **Saving a config section that carries a secret (e.g. `north.rest` with
  its `public_url`) no longer fails.** Sections whose schema contains a
  *complex* secret field — `north.rest.auth.users` / `auth.tokens` are
  `map[string]string` — could not be saved. With no HTTP-basic users
  configured, the section load returns `north.rest` without `auth.users`, so
  the editor represented that absent map as the empty string `""`. Saving then
  either aborted silently (the pre-validation ran `JSON.parse("")`, which threw
  before the request was sent — Save did nothing and the typed value vanished
  on navigating away) or, once it reached the request, was rejected with
  `400 … cannot unmarshal string into … auth.users of type map[string]string`.
  MQTT was unaffected because it has no complex secret field. Fixed on both
  sides: the SPA no longer parses or round-trips a placeholder for a
  secret-class or empty complex field — it sends `null` instead of a mistyped
  string — and the API reconciles every secret-field placeholder (the empty
  `""` or the masked `***` sentinel) back to the operator's current real value
  before validating and persisting a section. So the edited field saves,
  existing secrets are preserved, a genuinely changed secret still persists,
  and a round-tripped placeholder can no longer 400 or overwrite a credential.
  A genuine malformed-JSON value now raises a toast instead of failing quietly.
- **Config-save behaviour is now homogeneous across every Settings section.**
  Three inconsistencies are resolved so that saving any field behaves the same
  way it already did for MQTT — persist to the DB, update the per-field source
  dot, and (for restart-required fields) surface in the app-wide restart-pending
  banner and the "Changed settings" overview, never an immediate restart:
  - **Source dots now reflect the real origin of each field.** The effective-
    config endpoint attributed the DB tier by section name only
    (`north.mqtt`), while the SPA's source pill keys on the full field path
    (`north.mqtt.enabled`), so every DB-backed field rendered as "default".
    The daemon now attributes every field path owned by a persisted section to
    the DB tier (honouring the longest-prefix rule so `north.rest.auth.oidc.*`
    is credited to the OIDC section, not REST), while bootstrap- and
    env-sourced fields keep their own attribution.
  - **Saving a Matter (or any restart-required) field no longer pops an
    immediate "restart daemon" modal.** The per-save restart prompt was driven
    section-wide, so saving an unrelated field (e.g. the Matter `node_label`)
    in a section that happens to contain a restart-required field forced the
    modal — unlike MQTT, which only updated the banner. The per-save modal is
    removed; a restart-required change is signalled solely by the persistent
    restart-pending banner and the Changed-settings overview, and the operator
    triggers a restart deliberately from that banner or the System tab. Saving
    never restarts the daemon.
- **Devices now appear even when the CCU backend is not yet ready at daemon
  start.** When OpenCCU-Loom starts alongside a (re)booting CCU — e.g. as a
  co-located add-on — the backend answers `listDevices` with http 503
  ("internal backend exception") for the first minute or so while its services
  warm up. The boot-time device-load previously gave up after a few quick
  retries and left the interface empty until an unrelated recovery cycle
  happened to fire — or indefinitely, if the CCU's ping stayed responsive while
  `listDevices` was still 503. The per-interface device-load (ingest + callback
  init) is now retried in the background with backoff until the CCU answers,
  mirroring the existing hub-side retry, so devices populate on their own
  without a daemon restart.
- **The "waiting for CCU to become ready" health entry no longer lingers after
  a successful boot.** The readiness-gated startup records a transient
  `startup.<central>` component while it waits for the CCU; it was never
  cleared once the central came up, so its last sample went stale and decayed
  to UNKNOWN, pinning the overall health verdict at "unknown" (e.g. 66 %) even
  though the central and its interfaces were healthy. The component is now
  removed as soon as the bring-up succeeds.
- **`/health` no longer returns a transient 503 while a slow CCU boots.**
  During the gated-startup wait a central has no interface clients registered
  yet, which the health heartbeat read as the critical `central` component
  being unhealthy → ServiceAvailability → 503 until the CCU finished booting.
  Zero registered clients is now treated as a "starting" state (the central
  stays healthy); a genuine outage still leaves clients registered-but-
  disconnected and reports unhealthy.

### Security

- **The per-section config API no longer hands cleartext secrets to the
  browser.** `GET /api/v1/config` already masks secret-class fields to `***`,
  but the per-section editor load (`GET /api/v1/config/sections/<section>`)
  returned the stored value with secrets *opened* (the section store decrypts
  on read) — so an admin opening, e.g., the REST section received the real MQTT
  password / OIDC client secret / basic-auth hashes in cleartext. The
  per-section GET now masks secrets the same way as the snapshot; the SPA
  round-trips the `***` sentinel and the save path restores the real value, so
  masking does not break saving. Additionally, the basic-auth credential maps
  `north.rest.auth.users` / `auth.tokens` — managed by their own Users/Tokens
  admin panels — are no longer rendered as (meaningless, single-password-input)
  fields in the generic section editor.

## [0.5.1]

### Fixed

- **Two OpenCCU-Loom daemons against the same CCU no longer drive each
  other's `/health` to `503`.** The XML-RPC ping embedded only the bare
  interface name in its `caller_id` (`HmIP-RF#<token>`), and the PONG-ingest
  hook correlated on that bare name. Because the CCU broadcasts every PONG to
  all registered clients, a co-located daemon's PONGs carried the same bare
  prefix, passed the correlation guard, matched no pending ping, and piled up
  as "unknown" mismatches — degrading, then (after the flap-damp escalation)
  failing the only interface, which tripped the "every interface down → 503"
  rule. The ping now keys its `caller_id` on the full wire-boundary triple
  `<instance>-<central>-<interface>` and the hook matches the echoed prefix
  against the client's own triple, so a foreign daemon's PONGs are rejected
  instead of counted. Mirrors the reference
  `caller_id = f"{interface_id}#{token}"` and its `v_interface_id ==
  interface_id` guard.
- **A ping/pong correlation mismatch alone can no longer make `/health`
  return `503`.** Ping/pong mismatches are now recorded on a separate
  `ping_pong/<interfaceID>` quality component instead of the interface's
  liveness entry, so correlation noise can at most degrade service
  availability (HTTP 200) — never escalate the interface to unhealthy and map
  to 503. The signal stays visible in diagnostics and no longer skews the
  primary-client-healthy verdict. Mirrors the reference's distinct
  `ping_pong_mismatch_{interface_id}` issue model.
- **Sensors no longer publish a spurious `0` after a CCU restart.** The
  `fetch_all_device_data.fn` ReGa bulk-load script coerced an **empty**
  (not-yet-measured) numeric value into `"0"`. After a restart a data point such
  as `ACTUAL_TEMPERATURE` can already carry a timestamp but no real reading yet;
  the script then emitted `0`, published as a confirmed value (e.g. `0 °C`, or
  `0`/closed for a cover's `LEVEL`). The script now **skips** empty values, so
  the data point stays unset until a real measurement arrives. This is the
  actual fix for upstream issue #3228 — the `#3228` DEBUG log confirmed the
  source is the seed coercion (the CCU pushes no `STATUS = UNKNOWN`; every
  `*_STATUS` event is `NORMAL` and a post-reconnect `getValue` returns
  `Fault -5`), not the measurement status.
- **North-bound `available` is now gated on the full data-point validity,
  mirroring the reference `is_valid`.** REST, WebSocket and the MQTT runtime
  (`VALUES`) plane now report a reading as available only when it is valid:
  refreshed (a value has been observed), its paired `<X>_STATUS` is acceptable,
  its value type matches, and it is within range. An `OVERFLOW`/`UNDERFLOW`
  status or an as-yet-unobserved data point therefore publishes as unavailable
  (with a `null` value) instead of as a confirmed reading. STATUS validity keeps
  reference parity — `NORMAL` and `UNKNOWN` are both valid for every parameter
  (no measured-vs-control discriminator), so a control actuator such as `LEVEL`
  reporting `UNKNOWN` during the init-phase grace period stays available
  (upstream #2630). MQTT device-level reachability and the `MASTER`/`CALCULATED`
  planes are unchanged. See `notes/parity/by_design.md`
  (BD-CCU-StatusUncertainViaTracker).

## [0.5.0]

### Added

- **Opt-in measurement history for SPA charts (no external stack
  required).** A daemon running without Home Assistant can now record a
  time-series of numeric sensor values and chart it in the SPA. The
  recorder subscribes to live wire value changes and persists them to a
  dedicated `history.db` (its own WAL, separate from the config/session
  store); only genuine live observations are recorded — boot-time
  pseudo-values, cache replays, and source-only flips are filtered out by
  provenance (`ValueSource`), so a real `0` is kept but a restart spike is
  not. A new `GET /api/v1/history` endpoint returns a server-side-bucketed
  (avg/min/max/count) series sized for charting. For users who already run
  Grafana/InfluxDB, an opt-in push exporter forwards each sample via
  InfluxDB line protocol (no client dependency; token sourced from the
  environment). Everything is off by default and configured under
  `persistence.history` (DB-tier, SPA-editable). See
  [ADR 0040](docs/adr/0040-measurement-history.md) and SPECIFICATION §4.6.
- **Nine new MCP read tools** project domain data that previously had no
  MCP surface: `list_programs`, `list_sysvars`, `list_service_messages`,
  `list_alarm_messages`, `list_inbox`, `get_system_info` (hub aggregates,
  each `central_name`-scoped), plus `list_rooms`, `list_functions`, and
  `list_channels` (device topology). `list_channels` closes a real gap —
  agents can now discover channel addresses before calling `read_paramset`
  instead of guessing `:n`. All are reads (no new write surface, no new
  config, no new capability token); the MCP catalogue grows from 9 to 18
  tools. Tool names follow a documented verb/vocabulary taxonomy now
  pinned by a contract test. See
  [ADR 0025](docs/adr/0025-mcp-northbound-adapter.md) and
  [the MCP guide](docs/external-clients/mcp.md).
- **`GET /programs/{id}` single-program fetch.** Returns one
  `ProgramSummary` by id (`404` when unknown), mirroring the existing
  `GET /sysvars/{name}` shape. Like that endpoint it resolves the central
  by id across CCUs and only requires `?central=` to disambiguate an id
  shared by multiple centrals. Clients that previously fetched the full
  `GET /programs` list and filtered locally can drop that workaround.
- **Device-type icons in the device list — real images proxied from the
  CCU.** Cards led with a bare reachability dot, leaving 140+ tiles
  visually identical. They now show the device's icon with the
  reachability state as a corner dot. A new `GET /devices/{addr}/icon`
  resolves the device to its central and proxies the real eQ-3 image the
  CCU serves under `/config/img/devices/250/<file>` (cached, since icons
  are static; unauthenticated like `/health`). When the CCU has no icon
  for a model or is offline, the card falls back to a representative type
  glyph, so it always shows something.
- **Persistent "restart required" banner + a changed-settings overview
  with per-field revert.** Saving a config change that needs a restart
  gave only a one-shot modal. A new `GET /system/restart-pending`
  (persisted vs. running boot config over the restart-required field set)
  drives an app-wide banner that stays until the change is reverted or the
  daemon restarts; it links to Settings and offers an inline restart where
  a supervisor is detected. A new "Changed settings" overview
  (`GET /system/config-changes`) lists exactly the fields that differ from
  the running boot config — what was edited this session, not what differs
  from the built-in default — so a clean start shows nothing and reverting
  an edit empties the list. Each entry is revertible on its own via
  `DELETE /config/fields/{path}` (removes just that leaf, pruning the
  section row when empty), the per-field counterpart to the whole-section
  reset. The overview is a standalone tab at the end of the settings nav,
  shown only while there are changes.
- **Grid/list toggle for the device list, with sticky search.** The list
  can switch between the multi-column card grid and a single-column list
  (durable preference); the search term, filters and sort now survive
  opening a device and navigating back instead of resetting each time.

### Changed

- **Clearer grouping across the config UIs.** As the number of settings
  grew, the editors had gone flat. The daemon Settings sidebar now buckets
  its tabs into five collapsible top-level categories (General & System,
  Bridges, CCUs & Connectivity, Security & Access, Advanced) instead of one
  long list. Within a section, fields are split into labelled subgroups
  (e.g. Authentication / Rate Limiting / WebSocket / Tracing under
  *API & WebSocket*; Commissioning / CASE / Attestation under *Matter*),
  derived from the config path with a count badge per group. The device
  channel-config editor (MASTER paramset) gets the same header treatment and
  its curated group titles (Temperature, Timing, Boost, …) are now localized
  (de/en) instead of hard-coded English; easymode-metadata groups keep their
  archive label. Frontend-only and additive — no API or config changes.
- **Clearer device value presentation in the SPA.** Enum status values
  now localize (a door contact reads "Geschlossen", not "Closed"; a dimmer
  "Unbekannt", not "Unknown"). Momentary event channels (a remote's
  PRESS_SHORT/LONG) render as events with when they last fired instead of a
  raw `false`. The measurement-history tab is fully localized and its
  states are distinct — "recording off" explains itself and links to the
  setting (rather than naming a YAML key), separate from "no data in this
  range". Long config pages keep Save reachable via sticky action bars, and
  the weekly-schedule fallback line names the base temperature so it no
  longer looks inconsistent with the period temperatures in the heatmap.
  A further round of UX-audit polish: generic data-point labels localize
  client-side (STATE → "Status"); the device-overview tile grid drops to
  two columns for one- or two-tile devices so it no longer wastes a third
  of the row; the top-bar connectivity badge is labelled as the live-update
  (WebSocket) stream rather than a bare red dot; and the maintenance stripe
  replaces double negatives ("Duty-Cycle blockiert: Nein") with status
  words (OK / Blockiert / Schwach). The device list also uses the full
  width.

### Fixed

- **Change-history view was always empty.** The audit recorder persisted
  every config change to SQLite, but the read path served only an
  in-memory ring buffer that starts empty on each daemon start — so the
  history showed nothing after a restart and never surfaced seeded config.
  The buffer is now hydrated from the persisted store on boot (preserving
  original ordering and timestamps).

## [0.4.0]

### Changed

- **Optional pagination on hub list endpoints.** `GET /programs`,
  `GET /sysvars`, `GET /alarm-messages`, and `GET /service-messages` now
  accept optional `page` / `per_page` query parameters (same semantics as
  `GET /devices`). The response body remains a flat JSON array in all cases
  so existing clients are unaffected; a new `X-Total-Count` response header
  carries the unfiltered item count for cursor-less pagination. The OpenAPI
  spec is updated with the optional parameters and the header.
- **Stricter JSON decoding for four diagnostic/visibility endpoints.**
  `POST /diagnostics/capture`, `PUT /system/startup-capture`,
  `PUT /diagnostics/log-levels/{path}`, and `PUT /visibility/unignore` now
  reject unknown fields in their request bodies (previously silently ignored).
  The shared `DecodeJSON` helper (which already enabled `DisallowUnknownFields`)
  is used at all four sites.

### Fixed

- **`GET /sysvars/{name}` no longer requires `?central=` on single-CCU
  deployments.** The handler now uses name-based lookup: when `?central=` is
  absent it scans all centrals and routes to the unique owner; only genuine
  ambiguity (same name on >1 central) requires the explicit parameter.

### Security

- **In-memory user passwords are bcrypt-hashed.** Users seeded from the YAML
  `auth.users` map and via the HTMX first-run setup page are now hashed with
  bcrypt (cost 12, matching the SQLite user store) before storage instead of
  being held verbatim; `MemoryUserStore` verifies hashed records with bcrypt
  and still accepts legacy plaintext records through a constant-time fallback.
  Operators may seed a pre-computed bcrypt hash and it is used as-is.
- **Brute-force speed-bump on the HTMX login.** The pre-auth `POST /login`
  form is now rate-limited per client IP (burst 5, ~1 request/second refill)
  on the UI listener — a surface the per-identity REST limiter cannot cover
  (it keys on a resolved identity and runs on the REST listener). Throttled
  requests receive a `Retry-After` header and the generic login error, so the
  limiter neither slows a legitimate operator nor reveals its presence.
- **Per-identity rate limit on the WebSocket command channel.** Once a WS
  connection is upgraded the REST per-request limiter no longer applies, so a
  single authenticated session could fan out paramset writes / ReGa executions
  unbounded. The command router now throttles each identity (burst 60, ~20/s
  refill, idle-evicted bucket map); a throttled command returns
  `code: rate_limited`.
- **Plaintext-secret fallback is now visible on `/health` and as a metric.**
  When no master key can be resolved the daemon stores config secrets in
  plaintext (the ADR 0027 resilient fallback) — previously surfaced only as a
  single boot warning. It now reports a degraded `config.secrets` component on
  `/health` (which collapses to "degraded", not a 503, so liveness stays green)
  and a `config_secrets_plaintext` gauge (1 = plaintext, 0 = encrypted) so an
  operator dashboard catches it without scraping logs.

### Fixed

- **Device list no longer truncates at 200 devices.** The SPA's device store
  now fetches all pages on refresh: it reads the `total` field from the first
  `GET /devices` response and issues additional requests (page size 200, capped
  at 100 pages) until every device is loaded. Installations with more than 200
  devices previously saw a silently incomplete list.

- **Clean shutdown of recovery and MQTT-command work.** The connection-recovery
  coordinator spawned its per-interface recovery runs on a detached background
  context with no tracking, so `Stop()` could return while a multi-minute
  recovery pipeline was still running against the central; the runs now execute
  under a cancellable context tracked by a `WaitGroup`, and `Stop()` cancels and
  drains them. MQTT command handlers built their per-command context from
  `context.Background()`; they now derive it from the daemon-lifetime context
  (wired so it survives a broker hot-swap), so an in-flight CCU write is
  cancelled on shutdown instead of lingering to its ack timeout.
- **Bounded change-history and values-cache growth.** The `audit_log` table had
  no retention and grew without bound; rows older than 90 days are now purged
  opportunistically (every 256 inserts), no scheduler required. Removing a
  device now also evicts its rows from the persistent values cache — previously
  an unpaired device left its cached rows behind indefinitely.
- **REST upstream failures no longer report `code: internal`.** 35 handler
  paths that return HTTP 502 for a failed CCU/upstream call tagged the
  problem+json body with `code: internal` (which signals a daemon bug). They
  now use `code: upstream_unavailable`, so API/SPA clients switching on `code`
  can distinguish a transient upstream outage from an internal error. The 502
  status and the specific error titles are unchanged.
- **Retry-cancellation metric no longer double-counts.** `CancelledRetries`
  was incremented twice per cancelled chain — once at the cancelling call site
  (supersede / `CancelKey` / `CancelDevice` / `CancelInterface`) and again when
  the chain observed its closed cancel channel. It is now counted once, so the
  metric matches the number of chains actually cancelled.
- **Circuit-recovery waiter no longer drops other waiters.** The retrier's
  recovery waiter wired its wake-up hook with the breaker's *replace* setter
  instead of the *append* one, so a second waiter on the same breaker silently
  evicted the first (leaving its blocked retries unwoken until their deadline).
  It now appends, matching the documented "piggy-back, never replace" intent.

### Added

- **MQTT MASTER-paramset writes.** The documented
  `<base>/<central>/<iface>/<addr>/<ch>/master/<param>/set` topic now writes the
  MASTER paramset via the same `Channel.Set` path as the REST paramset endpoint.
  Previously the `master` bucket was silently dropped. The `calculated` bucket
  remains read-only and is dropped with a debug log.
- **CCU system-update panel** (Settings → System). Shows each CCU's firmware
  state (installed → available) and, for admins, an **Install** button that
  triggers the CCU's own firmware update (`POST /system/update/install`) with
  a reboot confirmation and live progress polling. The REST/WS API already
  supported this; it is now reachable from the web UI.

### Changed

- **Configurable MQTT retain-cleanup window (`north.mqtt.retain_cleanup_window_ms`).**
  The snapshot window used by `RunRetainCleanupOnce` and
  `RunDiscoveryOrphanCleanupOnce` was hard-coded at 2 seconds. Operators on
  high-latency brokers or large retained-message stores can now raise this value
  (valid range: 500–30 000 ms). Zero or absent falls back to the existing 2 s
  default so behaviour is unchanged for deployments that do not set the key.

### Fixed

- **Panic-safe circuit-breaker state listeners.** State-change callbacks fired
  with a bare `go cb(from, to)` in `refreshLocked`; a panicking listener
  silently killed its goroutine. Callbacks are now wrapped in a
  `safeFire` helper that `recover()`s and logs the panic at error level, so the
  breaker continues transitioning normally and remaining listeners still run.
- **Bounded self-reload concurrency in callback handlers.** A coerce-failure
  flood from the CCU could spawn an unbounded number of concurrent
  `LoadValue` goroutines against the radio. Self-reloads are now gated by a
  buffered semaphore (capacity 16); excess reloads are dropped with a debug log
  instead of queueing unbounded work.
- **Bridge declared-map pruned on device removal.** When the CCU sends a
  `deleteDevices` callback the MQTT bridge now removes the corresponding entries
  from its internal declared map, so the orphan-cleanup dedup gate does not
  suppress subsequent evictions of those topics.

- **Responsive / iPhone pass across the config UI (Svelte SPA).** Every
  route and the heavy editor components were reworked so the content — not
  just the app shell — is usable on a phone:
  - Shared foundations: `viewport-fit=cover` + safe-area-inset helpers so
    the notch / home indicator never clip the sidebar, header, toasts or
    dialogs; a reusable table→cards reflow (`table-reflow` + `data-label`,
    table on desktop, stacked cards on phones); touch-sized primitives
    (`Button` / `Input` / `Select` / `Switch`), with `text-base sm:text-sm`
    on inputs to suppress iOS Safari focus auto-zoom.
  - Wide data tables (audit log, backups, firmware, users, API tokens,
    diagnostics recordings) reflow to cards below `sm`.
  - Non-wrapping toolbars and fixed-width inputs (device-list filter bar,
    device-detail rename, logs / inbox / sysvars / section editor, Matter
    exposures) now wrap or go full-width on phones.
  - Settings: the fixed vertical tab sidebar becomes a horizontal scroll
    strip on phones.
  - Schedule editors: the fixed-width timeline visualisations are now fluid,
    and the period / lock / astro rows regroup so they no longer overflow.
  - Device-control tiles: actuator buttons, colour chips, sliders and number
    steppers raised to ≥40–44px touch targets.
- **Full localisation (de/en) of the SPA.** Every remaining hardcoded
  string — DeviceList, Login, the device-control tiles (climate, cover,
  light, siren, valve, text-display), the schedule editor, the Matter
  screens, and assorted labels / placeholders / aria-labels — now resolves
  through the in-app de/en catalogue (~190 new keys). Technical enums
  (roles, CCU data types, log levels) and the language-picker names stay
  literal by design.
- More table→cards reflows (profile preview, Matter fabrics) and touch-
  target fixes (text-display tile, sidebar footer icons, channel picker);
  the keyboard-shortcut button is now hidden on touch devices
  (`pointer-coarse`).
- `theme-color` gained a dark-mode variant.

### Fixed

- Toast container no longer overflows the right edge on narrow (≤390px)
  viewports.
- Replaced the remaining native `confirm()` / `prompt()` dialogs in the SPA
  (device delete / rename / firmware, set-room, sysvar / token / user
  actions, daemon restart, link / import) with the app's styled confirm
  modal and inline editors.
- Removed the ineffective dynamic import of the audit-log route (it is also
  statically imported by the device-detail history tab), clearing the
  build-time `INEFFECTIVE_DYNAMIC_IMPORT` warning.
- Post-recovery hub-metadata reload (system-update, sysvars, programs — all
  over ReGa) is now **best-effort**: a transient ReGa failure (an overloaded
  CCU, or a firewall/IPS dropping bursty HTTP) no longer fails the whole
  `data_loading` recovery stage, so an interface's already-enumerated devices
  stay visible instead of vanishing until a manual restart. Each refresh is
  reattempted by the periodic hub jobs, so a miss self-heals.
- **CCU system-update progress is now monitored** — parity with aiohomematic
  `install()` / `_monitor_update_progress`. Triggering a CCU update via
  `POST /system/update/install` now snapshots the firmware version and spawns
  a bounded monitor (poll every 30 s, up to 30 min) that clears the
  `in_progress` flag once the CCU finishes installing and reboots. Previously
  `in_progress` was set on trigger but never auto-cleared (the ported
  `MonitorProgress` was unwired), so the status — and the new system-update
  panel — stayed stuck on "installing".

## [0.3.0] — 2026-06-14

### Added

- **Per-central behaviour toggles (`centrals[].behavior`).** Nine operator
  toggles mirroring the reference stack's config knobs, all per-central and
  runtime-editable:
  - `light_last_brightness` (default true) — restore last brightness on a
    plain light turn-on, or turn on at full.
  - `use_group_channel_for_cover_state` (default true) — report cover
    position from the group channel or the cover's own channel.
  - `enable_sysvar_scan` / `enable_program_scan` (default true) — gate the
    hub system-variable / program scan entirely.
  - `include_internal_sysvars` (default true) / `include_internal_programs`
    (default false) — daemon-side filter for CCU-internal hub entities, so
    MQTT and REST agree.
  - `sysvar_markers` / `program_markers` (default empty) — restrict the hub
    scan to entities whose CCU description starts with one of the
    `DescriptionMarker` tokens (HAHM, HX, INTERNAL, MQTT); program
    descriptions are now fetched via ReGa when program markers are set.
  - `sysvar_scan_interval` (default 0 = 5 min) — override the periodic
    sysvar-refresh cadence.
  - `enable_device_firmware_check` (default true) — gate the per-device
    firmware-update entity surface. Defaults true (a deliberate divergence
    from the reference stack's false default; see `notes/parity/by_design.md`)
    so 0.2.0's firmware-update entities are preserved on upgrade.
  - `delay_new_device_creation` (default false) — defer ingest of a
    newly-paired device until it is accepted from the inbox.

  The block is editable end-to-end: YAML, the SQLite-backed central store
  (`behavior_json`), the REST V2 central API (documented on the
  `CentralBehavior` schema in `assets/openapi.yaml`), and the SPA central
  editor. `api_version` 1.7.0 → 1.8.0 (additive).

### Changed

- **SPA is smartphone-friendly.** The navigation sidebar now behaves
  responsively: on `<md` (phones) it is an off-canvas drawer opened by a
  header burger and dismissed by a backdrop tap or a nav-item tap, and the
  content pane is full-width (the fixed-width left padding only applies from
  `md` upward, where the bar is permanently docked). The mobile drawer always
  renders the labelled (expanded) nav regardless of the desktop collapse
  preference. The CCU edit form's field pairs collapse from two columns to a
  single column on narrow screens.
- **mDNS advertisement enriched for client auto-discovery.** The
  `_openccu-loom._tcp` TXT bundle now also carries `instance=<label>`
  (the friendly daemon name for a client's daemon picker) and
  `centrals=<count>` (a pre-auth hint of how many CCUs the daemon
  serves). Host/IP and port already come from the A/AAAA + SRV
  records; CCU names/serials are read from `GET /api/v1/system/ccu`
  after auth (not advertised in TXT). Lets `homematicip_local` /
  `openccu-loom-client` discover and select a daemon without manual
  host/instance entry. See ADR 0021.

## [0.2.0] — 2026-06-14

### Added

- **Contract schema digest on `GET /api/v1/info`.** The new
  `schema_digest` field identifies the exact contract state
  (openapi.yaml, wsapi.json, enums/types schemas) the binary was built
  from; generated client type packages carry the same value, so clients
  can verify type/daemon parity at connect time. `api_version` is now
  guarded in CI: contract-asset changes without a version bump fail the
  PR (breaking OpenAPI diffs require a major bump), and releases
  dispatch a regeneration event to the openccu-loom-types repo.
  See ADR 0028. `api_version` bumped to 1.1.0 (additive).

- **Matter NodeLabel suffixes share the entity display-name resolution.**
  Measurement sub-endpoints previously embedded the raw parameter key in
  their `BridgedDeviceBasicInformation.NodeLabel`
  (`"Wohnzimmer Kanal 1 (TEMPERATURE)"`). The assembler now routes the
  suffix through the same primitives as the MQTT discovery builder and
  the REST data-point handler (`device.TranslatedParameterLabel` →
  `naming.EntityDisplayName`), bound to the daemon locale
  (`locale` config key): translated parameters render their OCCU label
  (`"… (Temperatur)"`), untranslated ones fall back to the title-cased
  parameter (`"… (Temperature)"`), and "primary" parameters drop the
  suffix entirely — matching how MQTT/REST collapse the entity name to
  the device name. All three north-bound surfaces now resolve
  per-parameter display names from the same source of truth.

- **Home Assistant add-on.** OpenCCU-Loom can now be installed as a Home
  Assistant add-on, a third distribution channel alongside the Docker image
  and the CCU/RaspberryMatic add-on. The repository itself doubles as a HA
  add-on repository (add `https://github.com/SukramJ/openccu-loom` under
  *Settings → Add-ons → Add-on Store → Repositories*). The add-on is built on
  the official HA base image (s6-overlay supervises the daemon, so the Config
  UI's **Restart** action works in-container; bashio maps the `log_level`
  option), runs with `host_network` (so per-central callbacks reach the
  daemon), persists state in `/data`, and exposes the Config UI both via
  **Ingress** (sidebar panel) and the direct port `:8080`. One image is
  published per arch (`ghcr.io/sukramj/openccu-loom-ha-<arch>`, amd64 /
  aarch64 / armv7). Sources live in `packaging/ha-addon/`; the release build
  is toggled by `BUILD_HA_ADDON`. Delivers the channel anticipated in
  [SPECIFICATION.md](SPECIFICATION.md) Q9.
- **CCU / RaspberryMatic add-on packaging.** OpenCCU-Loom can now ship as
  a native CCU add-on alongside the Docker image. The release attaches
  `openccu-loom-ccu-<version>.tar.gz` (installable via the CCU's
  *Additional software* page); a single tarball bundles the amd64, arm64,
  and armv7 builds and the `update_script` selects the right one per
  `uname -m`, covering CCU3 and every RaspberryMatic flavour (32-bit Pi,
  64-bit Pi, x86-64 OVA / generic). The add-on installs an `rc.d` service
  with monit supervision and wires *Settings* / *Update* entries into the
  CCU add-on page; the daemon stays UI-configured, with state under
  `/usr/local/addons/openccu-loom/var`. Sources live in
  `packaging/ccu-addon/`, packaged by `script/build_ccu_addon.sh`
  (`make ccu-addon`). Activates the CCU/RaspberryMatic channel anticipated
  in [ADR 0012](docs/adr/0012-matter-pure-go-implementation.md).
- **`OPENCCU_LOOM_CALLBACK_PUBLIC_HOST`** env override for
  `callback.public_host` — there was an env knob for the callback *bind*
  host but none for the *advertised* host.
- **REST data-point summaries carry `translated_name` + `label_omitted`.**
  `GET /api/v1/devices/{addr}/cdps` (and the snapshot / values-batch
  surfaces) now expose the same per-entity display name the MQTT discovery
  builder emits — both resolve through a single shared primitive
  (`naming.EntityDisplayName`), so a REST drop-in and the MQTT plane spawn
  Home Assistant entities with identical names. `label_omitted` mirrors the
  "primary parameter" marker (HA collapses the entity name to the device
  name alone; MQTT emits `name: null`).

- **Per-interface install-mode sensor + button on MQTT discovery.**
  Install/pairing mode now surfaces as one remaining-seconds `sensor`
  AND one activation `button` per interface (`install_mode_hmip`,
  `install_mode_bidcos`, plus their `-button` companions) — matching the
  reference stack — replacing the single central-wide aggregate sensor.
  The button publishes to
  `<base>/<central>/hub/install_mode/<iface>/set`; the command
  subscriber translates the HA press token into an install-mode
  activation on that interface (default 60s, or a numeric override).
  Per-interface countdown state rides
  `<base>/<central>/hub/install_mode/<iface>`.

- **Virtual-remote press buttons on MQTT discovery.** Virtual remotes
  (HM-RCV-50 / HMW-RCV-50 / HmIP-RCV-50) now expose two clickable HA
  `button` entities per channel (`press_short`, `press_long`, disabled
  by default) next to the per-channel keypress `event` entity —
  matching the reference stack's per-channel surface. The command
  subscriber maps HA's `payload_press` token ("PRESS") to the boolean
  `true` the write-only ACTION parameters expect, which also makes the
  existing RESET_MOTION / RESET_PRESENCE buttons actually trigger.

### Changed

- **REST `parameter_label` is now always ready to render.** The field
  carried the locale-aware channel-typed translation *or empty*, leaving
  the fallback to each client — and the SPA rendered raw parameter keys
  (`RSSI_DEVICE`) in tiles, readouts, and the channel status badge when
  no translation existed. The server now resolves the title-cased
  fallback itself (`Rssi Device`) via the shared naming primitive, on
  both `DataPointSummary.parameter_label` and the Matter exposure
  candidates' `parameter_label`; the SPA renders the field verbatim
  through a single `dpLabel()` helper (its client-side title-casing
  copy is gone) and its API types gained `translated_name` /
  `label_omitted`. `assets/openapi.yaml` documents the field contract.
- **Log download offers larger sizes.** The diagnostics log viewer's download
  selector now offers 2000 and 5000 lines in addition to 100 / 200 / 500 /
  1000. The backend already served any `limit` up to the live-log ring
  capacity (5000); only the UI choices were capped at 1000.
- **CCU add-on Settings page is branded.** Clicking *Settings* for the
  OpenCCU-Loom CCU / RaspberryMatic add-on now shows a small card with the
  OpenCCU-Loom logo and an *Open Config UI* button (into the SPA on port
  8080), mirroring how ccu-jack presents its logo — instead of an immediate
  blank redirect.
- **Reference config files renamed** `config.example.yaml` →
  `example.config.yaml` and `config.example.full.yaml` →
  `example.config.full.yaml`. Required because a Home Assistant add-on
  repository is scanned recursively for `config.{yaml,yml,json}`, and the
  old names matched that glob (the Supervisor would have tried to parse them
  as add-ons). Update any local references; the file contents are unchanged.
- **Dependency refresh.** `golang.org/x/tools` → v0.46.0 (and transitive
  `golang.org/x/mod` → v0.37.0); SPA `tailwindcss` / `@tailwindcss/vite`
  → 4.3.1 and `@lucide/svelte` → 1.18.0; docs `pymdown-extensions`
  floor → 10.21.3.

### Fixed

- **Multi-CCU: client health no longer collapses same-named interfaces.**
  The aggregated health view deduplicated components by bare name, so
  two CCUs both running e.g. `HmIP-RF` surfaced as a single entry and
  the diagnostics "client health" panel showed only one CCU's
  interfaces (which one depended on sample timing). Components from a
  central's tracker are now scoped as `<central>/<component>`;
  `ClientDetail`/`ClientScore` route scoped names to the owning
  central's tracker, bare names keep the legacy first-match lookup.

- **`GET /api/v1/devices/{addr}/cdps` no longer panics on a half-formed light
  channel.** `*light.Light` relied on the method promoted from its embedded
  `*generic.Float` for `Category()`. On a "half-formed" channel — one whose
  LEVEL parameter has not materialised yet, so `Float` is nil — the
  autogenerated forwarder dispatched to `(*DataPoint[float64]).Category` on a
  nil receiver and panicked, surfacing as a `500 Internal error` that aborted
  the Home Assistant integration's device bootstrap. `Light` now defines an
  explicit nil-safe `Category()` returning `Undefined` when `Float` is nil,
  mirroring the existing `cover.Cover.Category` guard. (Climate/siren use named
  rather than embedded `*generic.Float` fields, and the valve wrappers return
  nil from their constructors when the DP is absent, so they are not exposed to
  the same hazard.)

- **WebSocket Origin check no longer blocks non-browser clients.** With CSRF
  enabled (the default), the `/api/v1/events` handler rejected any handshake
  without an `Origin` header (`403 websocket origin required`) — which broke
  headless API-token clients such as the Home Assistant integration's
  `openccu-loom-client`, since non-browser clients legitimately omit `Origin`.
  CSRF is a browser-only attack vector and browsers always attach an `Origin`
  to WS handshakes, so a missing `Origin` cannot be a forged cross-site
  request. The handler now allows handshakes with no `Origin` through and only
  validates the value when one is actually present, preserving cross-site
  protection for genuine browser connections.

- **Hub wiring now recovers from a transient boot-time failure.** `WireHub`
  ran exactly once at boot; if the CCU's ReGa was not yet reachable during the
  daemon's startup window it failed, leaving that central's entire hub surface
  (programs / sysvars / inbox / service+alarm messages) **and** the
  `central.refresh_client_data` safety net dead until a manual restart —
  observed live as a central logging `refresh_client_data: LoadAndRefreshData­
  PointData not wired` every tick with zero hub activity. A failed boot-time
  WireHub now schedules a background retry (5 s→60 s backoff, bounded by the
  daemon lifecycle) that re-establishes the hub once the CCU answers and wires
  the refresh handler. The retry re-applies the hub mutators through new
  mutex-guarded setters (`Hub.SetMutator`, `Update.SetFirmwareUpdater`,
  `Reconciler.SetConnect`), so it does not race the running daemon.
- **Central trapped in FAILED after connectivity returns (permanent `/health`
  503).** The central state machine permitted `FAILED → RECOVERING` / `STOPPED`
  only. When every interface reconnected *outside* an active recovery pipeline
  (the clients' own reconnect path, `in_recovery=false`),
  `evaluate_central_state` computed `RUNNING`/`DEGRADED` and the transition was
  silently rejected — so the central stayed in `FAILED` indefinitely even
  though all interfaces were connected: `/health` returned 503 and every
  heartbeat logged a futile `failed→running`. `FAILED` is now recoverable
  (`→ RUNNING` / `→ DEGRADED` added; only `STOPPED` is terminal), mirroring the
  client state machine.
- **Lost event under concurrent publish (event bus dispatcher handoff race).**
  `Publish` released the `dispatch` lock via `defer` *after* `flushDeferred`
  observed the deferred queue empty. A concurrent `Publish` whose `TryLock`
  failed in that window enqueued its event to `deferred` but never re-checked,
  so the event sat undrained until some future publish — effectively dropped
  if none came. Surfaced as an intermittent macOS-CI failure
  (`HandlerStat.Matches=999, want 1000`). The dispatcher now releases
  `dispatch` inside `flushDeferred` while holding `mu`, and the slow path
  attempts the take-over under the same `mu`, making release and re-acquisition
  mutually exclusive — so a concurrently enqueued event is always drained.
- **Endless reconnect loop on quiet CCUs (~every 180 s).** Inbound CCU
  callbacks never refreshed the per-interface callback-liveness timestamp —
  it was stamped only on reconnect. On a CCU with little spontaneous device
  traffic, `IsCallbackAlive` therefore went stale exactly `callbackFreshness`
  (180 s) after each reconnect, the `check_connection` watchdog declared the
  channel dead, and a full recovery fired — re-stamping the timestamp and
  restarting the 180 s clock, forever. Affected every interface on every
  central (local and remote alike). `CallbackHandlers.Event` now stamps
  liveness for every inbound callback, before the device-existence guard.
- **PONG callbacks never correlated (ping/pong pending pile-up).** The CCU
  echoes a ping's caller_id back as a `PONG` event on the `CENTRAL`
  pseudo-address. Because that address is not a mirrored device,
  `CallbackHandlers.Event`'s device-existence guard dropped the PONG before
  it reached the ping-pong tracker, so pending PINGs grew unbounded to their
  per-interface cap (100) and health stayed permanently *degraded* (and
  `/health` returned 503). PONG is now routed to the tracker before the
  device guard, closing the round-trip.
- **Foreign / liveness PONGs filed as "unknown" mismatches.** The CCU
  broadcasts `PONG` events to *every* registered logic-layer client, so on a
  shared CCU the daemon also receives other instances' PONGs (e.g.
  `Otto-HmIP-RF#<ts>`) on its own interface, plus the bare-name liveness
  probe's tokenless PONGs. These were recorded as unmatched *unknown*
  mismatches, decaying interface health to degraded/unhealthy. (The reconnect
  loop above had masked this by clearing the tracker every ~180 s.) The
  PONG-ingest hook now correlates a PONG only when its caller_id carries a
  `#` token *and* the embedded prefix equals this client's own ping prefix
  (the bare interface name) — mirroring the reference
  `v_interface_id == interface_id` guard. Verified live against a CCU shared
  with other Homematic instances: pending and unknown both stay at 0.
- **Callback host is resolved per-central (multi-CCU).** The host the
  daemon advertised in `init()` for CCU push events was computed once
  globally — from `callback.public_host` or a UDP egress probe against
  the *first* central — and reused for every CCU. On a daemon serving a
  local CCU (reachable at `127.0.0.1`) and an external CCU (reachable at
  the daemon's LAN IP) one of them always got an unreachable callback
  address: no push events, "central heartbeat degraded". The advertised
  host (XML-RPC and BIN-RPC/CUxD) is now detected per central as the
  egress interface toward *that* CCU, so each gets a reachable address;
  `callback.public_host`, when set, still overrides all centrals for NAT
  setups.
- **Enabling HA discovery at runtime now takes effect without a daemon
  restart.** Toggling `north.mqtt.ha_discovery` (or any MQTT setting)
  triggered a hot MQTT swap that rebuilt the bridge from scratch — with
  an empty Discovery cache and slot state — but nothing re-seeded it:
  the supervisor's snapshot `OnConnect` hook fires *during* the swap's
  bridge build, before the new bridge is installed into the shared
  wiring, so it re-published onto the outgoing bridge. The new bridge
  stayed empty until a full daemon restart re-ran the boot-time snapshot.
  The reload handler now re-runs `PublishInitialSnapshot` *after* the
  swap completes (when the shared wiring already points at the new
  bridge), so discovery + availability + per-DP slot state are re-seeded
  exactly as a restart would, for every successful enable-swap.

- **Channel-group switch state now reaches WS subscribers.** Switching a
  channel-group switch CDP (HMIP-PS/PSM/PSMCO — `STATE@3`/`STATE@4`/`STATE@5`)
  left the HA switch entity snapping back to off: the daemon never delivered a
  matching `custom_data_point.state_changed`. Two defects compounded on the
  WS CDP-state path in `eventbridge.go`:
  1. `customDPStatePayload` matched a `State() map[string]any` shape that no
     shipping CDP implements (every CDP exposes the typed `payload.Source`
     `State()`), so the push silently never fired for any CDP. It now reads the
     canonical `payload.Source` contract and JSON-round-trips the typed state
     into the wire map (`{is_on: true}`), identical to the `GET …/cdps`
     snapshot.
  2. The event used the bare parameter (`STATE`) as its name, but the cdps
     REST/WS surface disambiguates channel-group CDPs to `PARAM@<channel>`
     (`STATE@3`). The push now carries `custom.WireName(...)`, so the client's
     `(address, name)` keyed CDP receives it. The reference stack re-renders
     each custom DP on its own member events; this keeps the state topic
     aligned with the catalogue entry.
- **CDP invoke/get accept percent-encoded wire names.** A conformant client
  that percent-encodes the `{name}` path segment (`STATE%403`) previously hit
  a 502 ("data point STATE%403 not found"); the handler now URL-decodes the
  segment via `url.PathUnescape` on both the invoke and get paths, while a
  literal `@` keeps working.

- **Connection-latency aggregated to a single hub sensor on MQTT
  discovery.** Latency previously published one `sensor` per interface
  (`latency_<central>_<iface>`); the reference stack exposes ONE
  central-wide `connection-latency` sensor fed from the aggregated
  ping/pong metric. The per-interface latency discovery and state are
  removed; a single `connection_latency` sensor now publishes on
  `<base>/<central>/system/latency`, sourced from the
  `connection_latency_ms` metric aggregate. Stale per-interface latency
  discovery configs are auto-evicted by the discovery-orphan cleanup
  pass on the next boot; the old retained per-interface state topics
  (`…/system/latency/<iface>`) are left empty/orphaned (no HA entity
  subscribes) and are not matched by the legacy retain-cleanup patterns.

- **Text-display (HmIP-WRCD) now publishes only a `notify` entity on MQTT
  discovery.** The aggregate path emitted a surplus `text` entity
  alongside the `notify` companion, which HA rendered with a colliding
  `_2` suffix. The reference stack maps a TEXT_DISPLAY custom-DP onto a
  `notify` entity ALONE; the aggregate `text` entity is now suppressed so
  the display surfaces as a single `notify` entity. The stale `text`
  discovery config is auto-evicted by the discovery-orphan cleanup pass.

- **Multi-central hub unique_id collision on MQTT discovery.** The hub
  publisher built its own discovery builder and never saw the
  per-central HubInfo registered on the bridge, so every central's hub
  entities (sysvars, programs, alarm/service messages, inbox,
  install-mode, connectivity, metrics, update) collided on serial-less
  unique_ids (`loom__alarm_messages`) and HA silently dropped all but
  one CCU's hub plane. The publisher now shares the bridge's builder,
  hub discovery skips publishing until the CCU serial is known (never
  an empty slot), and the daemon re-runs the publisher after stamping
  HubInfo post-hydration.

- **Sysvar HA typing keys on the extended-sysvar marker.** Component
  selection for sysvar discovery used writability, rendering nearly
  every ReGa variable as a writable switch/number/select. It now
  mirrors the reference stack: only extended sysvars (ReGa-description
  marker) surface as switch / select / number / text; everything else
  is a read-only sensor or binary_sensor (ALARM keeps the `problem`
  device class).

- **DataPointUsage verdict gates per-parameter MQTT discovery.**
  `no_create` / `ignored` data points and the `ce_primary` /
  `ce_secondary` constituents of a channel's custom-DP aggregate no
  longer spawn duplicate generic entities next to the aggregate
  (climate / switch / cover / light …). `ce_visible` extras (HmIP-BWTH
  HUMIDITY, ACTUAL_TEMPERATURE) still pass.

- **Action categories no longer surface as HA entities.**
  `action_number` (ON_TIME, RAMP_TIME, DURATION_VALUE) mirrors the
  reference stack's empty ActionNumber whitelist; plain `action`
  parameters (COMBINED_PARAMETER, RAMP_STOP) have no HA platform there
  either. Both stay writable through the per-DP command topics and
  custom-DP service methods. Write-only enum parameters
  (`action_select`) keep their select surface and are now relegated to
  HA's Configuration section.

- **ENUM tokens are lower-cased toward HA.** Enum sensor and select
  discovery now lower-cases `options` and pipes the state through
  `| lower` (the reference stack renders translatable lowercase tokens
  like `closed`, `auto_mode`); selects map the chosen option back to
  the uppercase CCU token via `command_template` on write. Hub sysvar
  enum labels stay verbatim, matching the reference.

- **Channel-group switch state now reaches WS subscribers.** Switching a
  channel-group switch CDP (HMIP-PS/PSM/PSMCO — `STATE@3`/`STATE@4`/`STATE@5`)
  left the HA switch entity snapping back to off: the daemon never delivered a
  matching `custom_data_point.state_changed`. Two defects compounded on the
  WS CDP-state path in `eventbridge.go`:
  1. `customDPStatePayload` matched a `State() map[string]any` shape that no
     shipping CDP implements (every CDP exposes the typed `payload.Source`
     `State()`), so the push silently never fired for any CDP. It now reads the
     canonical `payload.Source` contract and JSON-round-trips the typed state
     into the wire map (`{is_on: true}`), identical to the `GET …/cdps`
     snapshot.
  2. The event used the bare parameter (`STATE`) as its name, but the cdps
     REST/WS surface disambiguates channel-group CDPs to `PARAM@<channel>`
     (`STATE@3`). The push now carries `custom.WireName(...)`, so the client's
     `(address, name)` keyed CDP receives it. The reference stack re-renders
     each custom DP on its own member events; this keeps the state topic
     aligned with the catalogue entry.
- **CDP invoke/get accept percent-encoded wire names.** A conformant client
  that percent-encodes the `{name}` path segment (`STATE%403`) previously hit
  a 502 ("data point STATE%403 not found"); the handler now URL-decodes the
  segment via `url.PathUnescape` on both the invoke and get paths, while a
  literal `@` keeps working.

- **Connection-latency aggregated to a single hub sensor on MQTT
  discovery.** Latency previously published one `sensor` per interface
  (`latency_<central>_<iface>`); the reference stack exposes ONE
  central-wide `connection-latency` sensor fed from the aggregated
  ping/pong metric. The per-interface latency discovery and state are
  removed; a single `connection_latency` sensor now publishes on
  `<base>/<central>/system/latency`, sourced from the
  `connection_latency_ms` metric aggregate. Stale per-interface latency
  discovery configs are auto-evicted by the discovery-orphan cleanup
  pass on the next boot; the old retained per-interface state topics
  (`…/system/latency/<iface>`) are left empty/orphaned (no HA entity
  subscribes) and are not matched by the legacy retain-cleanup patterns.

- **Text-display (HmIP-WRCD) now publishes only a `notify` entity on MQTT
  discovery.** The aggregate path emitted a surplus `text` entity
  alongside the `notify` companion, which HA rendered with a colliding
  `_2` suffix. The reference stack maps a TEXT_DISPLAY custom-DP onto a
  `notify` entity ALONE; the aggregate `text` entity is now suppressed so
  the display surfaces as a single `notify` entity. The stale `text`
  discovery config is auto-evicted by the discovery-orphan cleanup pass.

- **Multi-central hub unique_id collision on MQTT discovery.** The hub
  publisher built its own discovery builder and never saw the
  per-central HubInfo registered on the bridge, so every central's hub
  entities (sysvars, programs, alarm/service messages, inbox,
  install-mode, connectivity, metrics, update) collided on serial-less
  unique_ids (`loom__alarm_messages`) and HA silently dropped all but
  one CCU's hub plane. The publisher now shares the bridge's builder,
  hub discovery skips publishing until the CCU serial is known (never
  an empty slot), and the daemon re-runs the publisher after stamping
  HubInfo post-hydration.

- **Sysvar HA typing keys on the extended-sysvar marker.** Component
  selection for sysvar discovery used writability, rendering nearly
  every ReGa variable as a writable switch/number/select. It now
  mirrors the reference stack: only extended sysvars (ReGa-description
  marker) surface as switch / select / number / text; everything else
  is a read-only sensor or binary_sensor (ALARM keeps the `problem`
  device class).

- **DataPointUsage verdict gates per-parameter MQTT discovery.**
  `no_create` / `ignored` data points and the `ce_primary` /
  `ce_secondary` constituents of a channel's custom-DP aggregate no
  longer spawn duplicate generic entities next to the aggregate
  (climate / switch / cover / light …). `ce_visible` extras (HmIP-BWTH
  HUMIDITY, ACTUAL_TEMPERATURE) still pass.

- **Action categories no longer surface as HA entities.**
  `action_number` (ON_TIME, RAMP_TIME, DURATION_VALUE) mirrors the
  reference stack's empty ActionNumber whitelist; plain `action`
  parameters (COMBINED_PARAMETER, RAMP_STOP) have no HA platform there
  either. Both stay writable through the per-DP command topics and
  custom-DP service methods. Write-only enum parameters
  (`action_select`) keep their select surface and are now relegated to
  HA's Configuration section.

- **ENUM tokens are lower-cased toward HA.** Enum sensor and select
  discovery now lower-cases `options` and pipes the state through
  `| lower` (the reference stack renders translatable lowercase tokens
  like `closed`, `auto_mode`); selects map the chosen option back to
  the uppercase CCU token via `command_template` on write. Hub sysvar
  enum labels stay verbatim, matching the reference.

## [0.1.0] — Initial Release

First public release of **OpenCCU-Loom**, a standalone Go daemon that
bridges Homematic CCUs to MQTT, a REST + WebSocket API, a web Config UI,
and a Matter bridge. A Go port of the `aiohomematic` family that adds the
standalone-daemon surface on top.

### Core

- **Multi-CCU from day one** — one daemon, many CCUs; every coordinator,
  adapter, and store is `central_name`-scoped (ADR 0002).
- **Hexagonal architecture** (ports & adapters) with an internal typed,
  priority-aware event bus for cross-domain communication.
- **Single static binary** (`CGO_ENABLED=0`, no CGo) + multi-arch Docker
  images (linux/amd64, arm64, armv7).
- **Pure-Go SQLite** (`modernc.org/sqlite`) + filesystem persistence;
  goose-managed migrations.

### South-bound (CCU)

- All three transports: **XML-RPC, BIN-RPC, JSON-RPC**, plus HTTP and raw
  TCP callback servers (shared across all centrals, dynamic-port aware).
- Every MVP interface — HmIP-RF, BidCos-RF, BidCos-Wired, HmIP-Wired,
  VirtualDevices, and CUxD (BIN-RPC) — supports **push callbacks**; no
  polling-only code path.
- Reliability layer per `(central, interface)`: circuit breaker, retry,
  throttle, coalescer, ping/pong.
- 139 generated device profiles with hand-written custom data-point types;
  ReGa script runner.
- **Homegear backend support** — system variables load and periodically
  refresh over the XML-RPC `getAllSystemVariables` method (each variable's
  type inferred from its value, since Homegear ships only name + value) and
  write back via `setSystemVariable`, bringing Homegear to system-variable
  parity with the reference stack. Programs, rooms, and functions stay
  empty on Homegear by design (no ReGa engine / metadata RPC).

### North-bound (bridges)

- **MQTT** — Home Assistant Discovery **and** raw topic planes in parallel;
  MQTT config applies without a daemon restart. Discovery topics are scoped
  to each device's own CCU, so on a multi-CCU daemon every device's state,
  availability, command, and `json_attributes` topics route to the central
  the device actually lives on. Availability tracks device *reachability*
  (`UNREACH` / `STICKY_UNREACH` via `Device.Available()`): a reachable
  device is published `online` at boot even before its data points report,
  and every registered data point — including not-yet-observed ones —
  publishes an explicit `{"value":null,"available":true}` slot state (HA
  value templates render an unobserved point as `unknown` rather than
  `"None"`). The full boot snapshot — per-device availability plus every
  data point's slot state — is republished on every broker (re)connect, so
  a broker restart or transient TCP reset never leaves entities stuck
  `unavailable`.
- **REST + WebSocket API** — ~80 REST endpoints (`assets/openapi.yaml`) and
  85 WebSocket commands. Value-bearing WebSocket push payloads
  (`datapoint.value_changed`, `custom_data_point.state_changed`,
  `hub.sysvar_changed`, `hub.program_executed`,
  `datapoint.optimistic_rolled_back`, `device.trigger`) carry the canonical
  loom-namespaced `unique_id` (`loom_<routing-key>`) that external clients
  use as the Home Assistant entity key.
- **MCP server (Model Context Protocol)** — a north-bound adapter
  (`internal/north/mcp/`) exposing the daemon to LLM agents as tools over a
  Streamable-HTTP transport, mounted on the REST listener behind the same
  auth chain. Disabled by default (`North.MCP.Enabled`) and read-only even
  when enabled — write tools are registered only when `North.MCP.AllowWrites`
  is also set, and a write tool that touches a device refuses to act on one
  the named central does not own. Read tools: `list_centrals`,
  `list_devices`, `get_device`, `read_paramset`, `get_health`,
  `list_audit`; write tools: `set_datapoint`, `write_paramset`,
  `trigger_program`. Each tool also gates on its own dependency, so a
  partial wiring never exposes a half-functional tool. The `mcp.v1` /
  `mcp.write.v1` capability tokens surface the posture via `GET /info`.
  Built on the official `modelcontextprotocol/go-sdk` (ADR 0025).
- **Config UI** — a Svelte 5 SPA (Tailwind 4, embedded via `go:embed`) as
  the primary surface, with a minimal HTMX bootstrap surface for pre-auth
  flows (login, first-run setup, OIDC callback) and SPA-down diagnosis. The
  SPA includes an **MCP** tab (wired through the config schema and the
  section store) to toggle `enabled` / `allow_writes` and set the mount
  `path` — flagged restart-required — without editing YAML or env.
- **Matter bridge** — native-Go, default off, operator opt-in; a semantic
  port of matter.js HEAD.

### Auth & security

- Basic / Session / OIDC / API-Token authentication with role gating
  (admin-only mutations); CSRF protection for cookie-session flows.
- Audit ledger for sensitive operations.

### Diagnostics & observability

- Unified health tracker (per-central + daemon-global), Prometheus
  metrics, tracing helpers, incident journal.
- **Live log viewer** (`#/logs`, admin-only): always-on ring buffer with an
  SSE tail (`tail -f`, resume via `Last-Event-ID`/`?since=`), aggregated
  (≥ warn, deduplicated) vs. detail views, level dropdown, and download of
  the last N records.
- **Diagnose & Aufzeichnen hub**: RAM-buffered debug-log capture and an
  **RPC session recorder** (XML / JSON / BIN-RPC traffic for deterministic
  golden replay) with per-CCU scope, duration limit + safety cap,
  optional anonymisation, restart-survival, and `map`/`golden` export.
- Composite diagnostics dump and runtime per-subsystem log-level overrides.

### Internationalisation

- German + English catalogues across UI and server-rendered surfaces.
- A curated translation overlay supplies device-model labels the upstream
  catalogue omits — e.g. HmIP-DLP ("Türschlossantrieb - pro" / "Door Lock
  Drive - pro") and HmIP-UDI-SMI55 ("Universal Dimmeraufsatz -
  Bewegungsmelder" / "Universal Dimming Control Element - motion
  detector") — so their MQTT discovery payload carries a readable
  `model_id` instead of falling back to the raw wire type.

### Quality & parity

- Cross-stack model-snapshot drift gate
  (`script/model_snapshot_drift_check.py`) with an explicit env-override
  table (`OPENCCU_LOOM_DRIFT_GENERIC` / `_CHANNEL` / `_CALC`), a printed
  TOTAL line, and fail-closed behaviour on any drift bucket without a
  baseline.
