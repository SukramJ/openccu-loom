# ADR 0088 — The Config UI is the bridge's console, not a CCU WebUI

- Status: accepted
- Date: 2026-11-11
- Related: [ADR 0002 — multi-CCU first class](./0002-multi-ccu-first-class.md),
  [ADR 0040 — measurement history](./0040-measurement-history.md) (layer 1 superseded),
  [ADR 0067 — the north surface is a model API](./0067-north-surface-is-a-model-api.md),
  [ADR 0071 — south profiles](./0071-south-profiles.md),
  [ADR 0079 — box-shell single sign-on](./0079-lite-shell-session-sso.md)

## Context

The SPA grew by answering one question at a time: what does an operator without Home
Assistant still need the CCU WebUI for? `SPECIFICATION.md` §2.1 records the result as
two goals. Goal 5 says an operator "should not need the CCU WebUI for routine work".
Goal 6 adds measurement history as a daemon-level service, and §4.6 names "charts in
the Svelte SPA" as its driving use case. The surface registry
(`internal/north/ui/surface/surface.go`) lists 25 navigation items today, among them
an overview of control tiles, favourites, diagrams and an energy view.

The ecosystem around the daemon has moved since those goals were written. The state
below is as of the date of this ADR and is taken from the projects' own READMEs and
their maintainers' public statements.

- **The CCU WebUI has an owner with a plan.** The OpenCCU maintainer has announced a
  renewed WebUI as work for 2027 and described it as a configuration and
  administration UI that does not compete with Home Assistant or ioBroker dashboards.
  He asks for one coordinated path instead of many parallel UIs, and wants OpenCCU to
  offer MQTT, WebSockets and stable APIs for middleware
  ([OpenCCU discussion 4380](https://github.com/orgs/OpenCCU/discussions/4380),
  [forum thread](https://homematic-forum.de/forum/viewtopic.php?f=43&t=88794)).
- **Replacement WebUIs exist and multiply.**
  [ccu-addon-mui](https://github.com/firsttris/ccu-addon-mui) claims near-complete
  coverage of the WebUI's functions, plus charts and dashboards. A second project is
  presented in the forum thread above.
- **openccu-lite assigns each job to one tool.**
  [openccu-lite](https://github.com/SukramJ/openccu-lite) removes ReGaHSS and delegates
  everything beyond the core to add-ons. Its system service owns users, sessions,
  backups, updates and add-on management.
  [homematic-manager](https://github.com/hobbyquaker/homematic-manager) is named there
  as the tool for pairing, device configuration and direct links.
  [lite-addon-forwarder](https://github.com/hobbyquaker/lite-addon-forwarder) sends
  measurements to external stores; the system draws no charts itself.

Every job of "the WebUI for one CCU" therefore has an owner or a specialist. What none
of these projects offers is what this daemon is: one process for many CCUs of both
system types, with one typed device model projected onto MQTT, REST, WebSocket, Matter
and MCP.

Three findings from the code matter for the decision:

- The measurement recorder is "the single capture point" with two sinks (ADR 0040):
  the embedded SQLite store and the push exporter. The store is read by `GET /history`
  and `GET /energy` and by the MCP tools `get_measurements` and `get_energy`. The two
  REST handlers describe themselves as what the SPA's chart and energy view need.
  `/diagrams` holds the named chart definitions of the diagrams view. The
  per-data-point recording overrides (`/history/recording`) are kept in the same
  `history.db`.
- The surface profiles are two by a recorded decision. `internal/config/config.go`
  states that "who owns the config surface" has exactly two answers, standalone and
  embedded in Home Assistant. On an openccu-lite box the SPA already follows the
  shell's theme and language (`assets/ui/src/lib/theme/lite-shell.svelte.ts`) and
  accepts the shell's session (ADR 0079), but the profile table has no column for it.

- The daemon is also a backend of the Home Assistant integration `homematicip_local`,
  and there the two sides defer to each other. The integration does not register its
  own configuration panel for an entry on this backend, on the grounds that the daemon
  "ships its own Config UI covering everything this panel offers — paramsets, direct
  links, schedules, change history" (`_any_entry_has_panel_enabled` in the
  integration's `__init__.py`). The embedded profile in turn hides `device.configure`
  and its three sub-tabs as owned by Home Assistant. With `ui.embedded` switched on, a
  single CCU and the UI reached through Home Assistant, neither editor is offered.
  This is read from the two default tables, not observed on a running installation.
  The embedded profile is opt-in, and a daemon serving several CCUs shows the tabs.

## Decision

**The SPA is the console of the bridge.** It is not a replacement for the CCU WebUI
and not an operating UI.

1. **A surface must answer one of three questions.**
   - Is the bridge running? Centrals, readiness, messages, diagnostics, signal quality,
     audit, logs, the fleet.
   - What does the bridge see and export? Devices with their current values, and how
     they appear on MQTT, Matter, REST and MCP.
   - What has to be set so that a device is usable through the bridge? The daemon's own
     configuration, device onboarding, and device configuration within the limits of
     point 3.

   A surface that answers none of them is not built. An existing one is removed, not
   hidden.

2. **Four surfaces and one chart are removed from the SPA.**

   | Surface | Reason |
   |---|---|
   | `nav.diagrams`, `nav.energy`, the chart in `device.history` | Charting is the job of the store a measurement is exported to. |
   | `nav.overview` | A page of control tiles grouped by room and function is an operating UI. |
   | `nav.favorites` | The same, extended to running programs and writing system variables. |

   The device list and the device detail keep showing current values and keep the
   controls they have; a value shown there is a diagnostic of the bridge.

   Amended 2026-11-11: the `device.history` tab stays. Besides the chart it holds the
   device's change log and the per-data-point recording switch, both of which this ADR
   keeps (points 3 and 4). Only the chart goes, and the tab loses its capture gate
   because the change log needs no capture.

3. **Device configuration is the configuration surface of `homematicip_local` on this
   backend, and that sets its bar.** Paramsets, direct links, schedules and the change
   history cover what the integration's panel covers, for every CCU the daemon serves.
   That scope is a commitment. Parity with the CCU WebUI or with homematic-manager is
   not.

   **There is one implementation of that editor, and Home Assistant loads it.** The
   SPA's device configuration is built so that the daemon can also deliver it as a web
   component. On this backend the integration keeps its sidebar entry and its panel
   loads that component, scoped to the entry's CCU, in place of its own Lit views. The
   daemon serves the editor that matches its API, so the two cannot drift apart. The
   SPA uses no custom elements today; how the component reaches the daemon's API from
   inside Home Assistant is the open question listed below.

   **The rest of CCU administration is kept and frozen.** Programs, system variables,
   heating groups, firmware and backups stay in the SPA and stay maintained. A new
   capability there is justified by the API (point 5) or by the fleet: something one
   UI per CCU cannot do.

4. **Measurements are captured and forwarded, not stored.** The embedded store goes
   with its views: the measurement tables of `history.db`, retention and rollup, `GET
   /history`, `GET /energy`, `/diagrams`, `get_measurements` and `get_energy`. Capture
   stays, with its include and
   exclude filter, the per-data-point recording switch and the push exporter; the
   recording overrides move out of `history.db`. This supersedes layer 1 of ADR 0040
   and keeps its layer 2.

5. **A capability lands on the API first.** REST and WebSocket, and MCP where an
   assistant needs it, carry a capability before the SPA does. The SPA is one client
   of that contract and adds no capability of its own.

6. **Profiles describe hosts; they are not a way to keep out-of-scope views.** The
   profile axis gains a third host, the openccu-lite shell. This reopens the "two,
   fixed" decision in `internal/config/config.go`: on a lite box the system owns
   things Home Assistant does not, so reusing the Home Assistant column would hide the
   wrong surfaces. The per-surface defaults of the new column are set in the
   implementing change, each one checked against what the lite system service
   provides.

   In the Home Assistant column, `device.configure` and its sub-tabs stop being marked
   as owned by Home Assistant. Home Assistant owns that editor only while the
   integration talks to the CCU directly; on this backend the integration hands it to
   the daemon. Two editors in the first case cost less than none in the second.

   Amended 2026-11-11 with the implementing change. The third profile is `lite`. It is
   **selected by the request, not declared**: a request whose identity is a box-shell
   session the box confirmed ([ADR 0079](./0079-lite-shell-session-sso.md)) gets it.
   Home Assistant proxying a request does not say that it owns the config surface, so
   that ownership stays declared; a confirmed box session is itself the fact that the
   box holds the account and the login. Selecting on the identity instead of on the
   `X-Occulite-Session` header also means the profile cannot apply while single
   sign-on is off — the case in which the daemon's own accounts are the login and
   hiding their administration would be wrong. A box API token and a daemon login
   inside the shell frame keep the standalone profile; `embedded_scope: always`
   outranks the shell.

   The column, checked against occulited at `04ee7b6` (`docs/system-api.md`,
   `ui/src/App.svelte`):

   | Surface | lite default | Source |
   |---|---|---|
   | `settings.users` | hidden | `GET/POST /users`, shell page `/system/users` |
   | `settings.oidc`, `settings.ccu_auth` | hidden | the box's own login, passkeys and `/oidc` provider behind the session that selects the profile |
   | `settings.ccus` | hidden, visible on a multi-CCU daemon | the daemon onboards the box it runs on by itself ([ADR 0077](./0077-lite-addon-token-onboarding.md)) |
   | `nav.firmware` | hidden | `/firmware` routes, shell page `/system/updates` |
   | `nav.matter`, `settings.matter` | visible | no Matter in the box; hidden only in the Home Assistant column |
   | `settings.tokens` | visible | daemon tokens stay valid for clients on the direct port |
   | `settings.groups` (rooms and functions) | visible | the box keeps rooms and functions in its metadata store, but an editor equal to this one was not verified |
   | `nav.backups`, `nav.logs` | visible | the box has a backup and a log page; that its backup carries the daemon's data was not verified, and its log is the system journal, not this view |
   | `device.configure` and its sub-tabs | visible | the box has no paramset, link or schedule editor |

7. **The alarm system and the security view are the one named application.** They
   answer none of the three questions and stay nevertheless: they are a daemon-level
   service that has to work without Home Assistant (`SPECIFICATION.md` §2.1, goal 6),
   and they are built on the device model the daemon already holds. No further
   application of this kind is added without an ADR of its own.

8. **The projection view is where the SPA grows.** Per device it shows how the device
   appears on MQTT, Home Assistant discovery, Matter and REST, and why it does not
   appear where it is missing. The device detail has no such view today; the Matter
   side has an exposure API with a preview (`/node/exposure`). The view gets its own
   concept before it is built.

`SPECIFICATION.md` changes with this ADR: goal 5 keeps "the CCU is administrable from
the daemon" for the API and drops the aim that an operator never needs the CCU WebUI;
goal 6 and §4.6 describe measurement capture and export in place of an embedded
history.

### Rejected: becoming the WebUI of openccu-lite

A system without a WebUI of its own looks like an opening. It is not: the system
service and homematic-manager already cover it, and the role would bind the SPA's
roadmap to one system type while the daemon's own subject is the fleet.

### Rejected: hiding the removed surfaces by default

A hidden view is still built, translated, tested and pinned by visual baselines. It
keeps its cost and blurs the profile for every operator who switches it back on.

### Rejected: keeping the store as an API without a view

The store would stay for API clients alone. That keeps retention, rollup tiers and two
query endpoints in the daemon for a job a time-series store does better, and it leaves
point 1 with an exception nobody can explain from the rule. Compatibility is not a
reason to keep it: the removal is a documented breaking change.

### Rejected: showing the integration's own panel on this backend

The integration's WebSocket layer has paths for this backend, so the Lit panel could
be shown again. It would be a second rendering of a schema the daemon already
produces, reached through Home Assistant and the client library, and it sees the one
CCU of its config entry.

### Rejected: opening the SPA in a frame behind the sidebar entry

It needs no new build target and would work wherever the daemon is reachable through
Home Assistant's ingress. It puts a second application shell, with its own navigation
and login state, inside Home Assistant's. The web component gives the integration a
part it can place in its own panel.

### Rejected: removing device configuration

The integration has retired its own panel on this backend in favour of the SPA, so
removing it would leave Home Assistant users without an editor. It is also served
through REST and MCP in any case, where no other project in the ecosystem offers it
across several CCUs.

## Is this the best architecture?

- **Better than the comparison targets?** In its own field, yes. The WebUI
  replacements and homematic-manager each serve one CCU; aiohomematic has no UI of its
  own and leaves it to Home Assistant. None of them shows one device model across
  several CCUs and several north-bound planes. In the fields this ADR leaves (charts,
  dashboards, a complete WebUI) the daemon was the weaker offer and would have stayed
  it.
- **Better than before?** Yes for coherence and cost: the SPA's scope follows from one
  rule instead of from a list of WebUI features. It is worse for one user, the operator
  without Home Assistant who wanted charts from the binary alone, and for an assistant
  that read trends through MCP. Both are now pointed at the exporter and an external
  store.
- **The right cut?** The CCU's configuration belongs to the CCU's own UI or to a
  configuration specialist. Operating and visualisation belong to Home Assistant,
  ioBroker or a dashboard. Measurements belong to a time-series store. The daemon
  keeps the device model, its projections and the fleet; the SPA shows those. Matter
  pages stay with go-fabric-ui (ADR 0086).

## Consequences

- The routes `Diagrams`, `Energy`, `Overview` and `Favorites`, the chart of the device
  detail's history tab, their chart components, catalogue keys, registry entries and visual
  baselines are deleted. The start page becomes the device list or the fleet view,
  decided in the implementing change.
- `nav.energy` and `nav.diagrams` leave the registry together with `GateHistory`.
- The REST contract loses `/history`, `/energy` and `/diagrams`, and the MCP tool set
  loses `get_measurements` and `get_energy`: a new REST major version, followed by the clients that mirror
  the contract. The `favorites` user preference goes with its view.
- The `persistence.history` section becomes `north.measurements`: the forwarder is a
  north-bound plane, not a store. The recording route is `/measurements/recording`,
  the capability `measurements.v1`. The recording overrides live in the main database
  and are taken over once from an existing `history.db`, which is otherwise left
  untouched. A config file that still sets `persistence.history` is refused at start.
  Owner decisions of 2026-11-11.
- Operators who use the removed views lose them on upgrade, and recorded measurements
  are not migrated. The CHANGELOG entry names the replacement for each: the exporter
  for measurements, Home Assistant or another client for operating.
- `SPECIFICATION.md` §2.1 and §4.6 and the ADR index are updated when this ADR is
  accepted.
- The profile table gets a third column, and the guard that every surface is
  registered extends to it.

## Not decided here

- **How the web component reaches the daemon.** Two paths are candidates: the
  browser calls the daemon through Home Assistant's ingress (ADR 0044, ADR 0054), or
  the component gets its transport injected and the integration relays the calls over
  its WebSocket API. Authentication, the daemon that is not an add-on, and theming
  inside Home Assistant follow from that choice. It is investigated before the
  component is built, and decided together with `homematicip_local`.
  Decided on 2026-11-12 in
  [ADR 0089](./0089-editor-component-transport.md): the transport is injected and
  the integration relays.
- **The default column of the openccu-lite host**, as stated in point 6.
