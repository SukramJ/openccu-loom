# ADR 0089 — The editor component reaches the daemon through the integration

- Status: accepted
- Date: 2026-11-12
- Related: [ADR 0088 — the Config UI is the bridge's console](./0088-config-ui-is-the-bridges-console.md)
  (decision 3 and "Not decided here"),
  [ADR 0044 — one port and the ingress passthrough](./0044-single-port-onboarding-and-ha-ingress-auth.md),
  [ADR 0054 — the remote proxy add-on](./0054-remote-ingress-proxy-addon.md),
  [ADR 0079 — box-shell single sign-on](./0079-lite-shell-session-sso.md)

## Context

ADR 0088 decides that there is one implementation of the device-configuration editor,
that the daemon delivers it as a web component, and that the Home Assistant
integration `homematicip_local` loads that component in its panel for a CCU served by
this daemon. It leaves open how the component reaches the daemon's API from inside
Home Assistant, and names two candidates: the browser calls the daemon through Home
Assistant's ingress, or the component gets its transport injected and the integration
relays the calls.

The findings below were read from the three repositories on 2026-11-12:
this repository at `c90e4a47`, `homematicip_local` at `d4ee9f7`,
`homematicip-local-frontend` at `635db83`. Paths in the integration are relative to
`custom_components/homematicip_local/`.

### What the integration holds

- An entry on this backend stores how to reach the daemon and with which credential:
  host, TLS flags, port and a daemon token (`control_unit.py:1510-1511`), or a box
  token for a daemon behind an openccu-lite box's web server (`const.py:90-98`,
  `control_unit.py:1778`). That covers a daemon that is an add-on, a daemon behind the
  remote proxy add-on, a daemon on another host and a daemon on a box.
- The entry carries an instance name and hands it to the client as the name of its
  central (`control_unit.py:1739`). Whether that name equals the daemon's central
  name on every installation was not verified; see "Not decided here".
- The panel is one custom panel for all entries, admin only, loaded from a module URL
  the integration serves itself with a content hash in the file name
  (`panel.py:19-22`, `panel.py:91-99`).
- **The panel does not serve this backend today.** `__init__.py:139` skips these
  entries when it decides whether to register the panel, and the panel's entry picker
  drops them (`packages/config-panel/src/homematic-config.ts:208-217` in the frontend
  repository). Decision 3 of ADR 0088 therefore describes a target, and the
  hand-over reverses both lines.
- The integration knows nothing about ingress for a daemon: no add-on slug, no
  ingress URL, no daemon URL is handed to the frontend.

### What the panel speaks

Every call of the Lit panel is `hass.callWS` with a `homematicip_local/...` type and
an `entry_id`; there is no `fetch` and no injected transport
(`packages/panel-api/src`). Language comes from `hass.config.language`, dark mode
from `hass.themes.darkMode`.

### What ingress gives

- Ingress exists where an add-on exists: the daemon's own add-on and the remote proxy
  add-on (`packaging/ha-addon/*/config.yaml`, `ingress: true`). A daemon on another
  host without the proxy add-on, and a daemon on a box, have none.
- The passthrough identity is one subject, `ha-ingress`, with one configured role,
  for every request that carries `X-Ingress-Path` from the trusted proxy range
  (`internal/auth/ingress.go:40-92`). It has no relation to a config entry or to the
  CCU of one entry.

### What the SPA is built on

- Every REST call goes through one function, `request()` in
  `assets/ui/src/lib/api/client.ts`, which builds its URL from `apiBase()`
  (`assets/ui/src/lib/api/base.ts`, derived from `location.pathname`) and sends the
  session cookie. A central is named per call with `?central=`.
- The editor is `routes/DeviceDetail.svelte` with `DeviceParameters`, `DeviceLinks`,
  `ScheduleTab` and `AuditLog`. It imports the API client, the event subscription
  (`stores/events.svelte`), and the stores for centrals, devices, preferences, auth,
  info, taxonomy, maintenance, surfaces, toasts and confirmations.
- The SPA has no library build and no custom element.

### A build probe

A throwaway build wrapped `DeviceParameters` in a plain custom element class that
attaches a shadow root, adopts the application stylesheet and calls Svelte's
`mount()`. It was built as a Vite library target with the SPA's own plugins.

- It builds without the `customElement` compiler option: one ES module of 1.06 MB
  (270 kB gzip) for the parameter editor alone.
- One component's scoped CSS was emitted as a separate file and is not part of the
  adopted sheet.
- The bundle writes to `document.body` (the scroll lock and pointer-event guard of
  the dialog primitives), reads `location.pathname` twice, touches `localStorage`
  four times and opens a `WebSocket`. Inside Home Assistant each of these acts on
  Home Assistant's page, not on the component.
- `ui/Select.svelte` renders through a portal. A portal that targets the document
  leaves the shadow root and its styles.

**Not probed:** rendering. The bundle was built, not loaded in a page.

## Decision

1. **The component's transport is injected, and the integration relays.** The
   component makes no network call of its own. Its host hands it a transport with two
   operations: a request (method, API path, query, body → status, body) and an event
   subscription. In the SPA the host is the SPA shell and the transport is the
   existing `fetch` and WebSocket path. In Home Assistant the host is the
   integration's panel and the transport is `hass.callWS` and
   `hass.connection.subscribeMessage` on two new commands of the integration, which
   forward to the daemon with the entry's stored credential.

2. **The relay is narrow.** The request command takes an `entry_id`. The integration
   resolves the entry, adds the entry's central to the call itself and refuses a call
   that names another one. It forwards only the routes the editor uses, by an
   allow-list of path prefixes that ships with the integration, and it is admin only
   like the panel. It is a relay for this component, not a general proxy to the
   daemon.

3. **The daemon is the source of the module, and the integration serves it.** The
   daemon serves the component bundle next to the SPA and reports its content hash.
   The integration fetches the bundle from the daemon of the entry and serves it from
   Home Assistant's origin under a URL that carries that hash, the way it serves its
   own panel today. The browser never needs to reach the daemon, and the editor
   always matches the API of the daemon it edits. The bundle is public code; serving
   it without a session is no disclosure.

4. **Authentication is the entry's credential.** The daemon sees the token the entry
   holds, on every deployment the integration supports. No daemon credential and no
   daemon address reaches the browser, and the daemon needs no CORS rule for Home
   Assistant.

5. **Language and theme are properties.** The host sets the locale and the dark flag
   on the element; the panel takes them from `hass`. Colours follow Home Assistant
   through the CSS custom properties that cross a shadow boundary.

6. **The component is a wrapper class around `mount()`**, in a shadow root with the
   stylesheet adopted. The `customElement` compiler option is not used: the editor
   stays ordinary Svelte components, and the SPA route and the element mount the same
   component.

7. **Everything the editor takes from the page becomes a seam of the host.** The
   request function, the event subscription, the central, the persisted preferences,
   the navigation between device, link and schedule, and the portal target for
   dialogs and selects. The probe's list above is the starting inventory for the
   build issue.

## Is this the best architecture?

**Better than the comparison targets?** The Lit panel carries a second implementation
of the editor and a WebSocket command per operation, and it has to follow every
change of the daemon's API by hand. One relay command and one module from the daemon
replace that for this backend. The CCU WebUI has no counterpart: its editor cannot be
embedded anywhere.

**Better than the state before?** Before, a CCU served by this daemon had no editor
inside Home Assistant at all, and the daemon's editor was reachable only as a whole
SPA through an add-on's ingress panel. Afterwards the editor appears where the
integration's users look for it, on every deployment, with the entry's scope.

**Is the cut right?** The daemon owns the editor and its API, so they cannot drift.
The integration owns what only it has: the entry, its credential, its CCU and Home
Assistant's session. The component owns no network and no storage. The alternative
through ingress puts reachability and identity into the browser, works only where an
add-on runs, and gives one shared admin identity with no relation to an entry; a
mixed design would carry two transports and two authentication paths for the same
editor. A single path that works everywhere is the smaller design.

The cost is real: a relay in the integration with an allow-list that has to follow
the editor's routes, an event relay, and a payload that crosses Home Assistant's
WebSocket instead of going direct.

## Consequences

- `homematicip_local` gains two WebSocket commands and a view for the module, stops
  skipping these entries for the panel, and loads the component in place of its Lit
  views on this backend. Its existing per-operation commands stay for the CCU
  backend.
- The SPA's API client gets its request function and its event source from the host
  instead of from the page. The SPA itself is the first host.
- The allow-list is a contract between the two repositories. The daemon publishes the
  set of routes the component uses with the bundle, and a test on the daemon's side
  fails when the editor calls a route outside it.
- The daemon's audit log shows the entry's token as the actor. Which Home Assistant
  user the relay acted for is recorded beside it as an assertion (settled below,
  2026-11-26).

## Not decided here

- **How the relay names the central.** The scoping in point 2 needs the daemon's
  central name for an entry. The entry's instance name is the candidate; that the
  two always agree has to be shown from the client before the relay relies on it.
- **Who the audit log names.** Whether the relay passes the Home Assistant user to
  the daemon, and whether the daemon may trust that from this credential.
- **How the hash is reported and the bundle addressed** on the daemon (a route of its
  own or a field of an existing answer). It is a contract change and is written with
  the build issue.
- **Dialogs and selects inside a shadow root.** The portal primitives have to render
  into the component. Whether they can be pointed there or have to be replaced is
  established by a rendering probe before the build.
- **Bundle size.** 270 kB gzip for one of four editor parts is a first number, not a
  budget.
- **Whether the event relay is needed for the first version**, or the editor reloads
  after its own writes.

## Settled by the build (2026-11-17)

The component build (#1067 step 2b) closed four of the open points above.

- **How the hash is reported and the bundle addressed.** By a route of its own,
  decided by the owner: `GET /api/v1/ui/editor-component` answers
  `{module_url, hash, routes}`. The module is served without a session under
  `/app/component/editor-<hash>.js`, cached immutably; a stale hash is a 404,
  never the SPA's `index.html`.
- **Dialogs and selects inside a shadow root.** The primitives can be pointed
  there; none had to be replaced. The element sets bits-ui's
  `BitsConfig.defaultPortalTo` to a node in its shadow root, registers the
  stylesheet's `@property` rules on the document (Tailwind's border utilities
  read a variable only those rules give a value), and the hand-rolled dialogs
  resolve focus through the shadow roots instead of `document.activeElement`.
  The `--ha-*` tokens stayed empty inside the root until their `:host` forms
  shipped in go-fabric-ui (SukramJ/go-fabric-ui#72, released in v0.10.0); the
  expected failure in the component's spec was removed with that release.
- **Whether the event relay is needed for the first version.** It is optional:
  without an event source the editor shows what it loaded and what its own
  writes return.
- **Bundle size.** The whole editor (the device page's configuration part and
  the change log, one module) is 1.6 MB, 383 kB gzip.

Two contract details the decision above did not spell out: a request carries
its headers, because a MASTER write needs the edit lock (`X-Edit-Token`) and
the daemon answers 423 without it; and the route list is the file
`assets/ui/src/component/editor-routes.json`, which the component enforces and
the daemon publishes, so the two cannot differ.

## Settled by #1119 (2026-11-26)

The owner decided the two points left open, on the issue (2026-11-20) and in
a decision round on 2026-11-26.

- **How the relay names the central.** The relay maps its entry to the
  daemon's central (by the CCU's serial) and sends `?central=<name>` with every
  call. Every `/devices/{addr}` route honours it: a guard in front of the
  handler answers 404 when the central does not exist or does not hold the
  address, and otherwise binds the request to that central, so every address
  lookup on the request's path resolves there and nowhere else. Without the
  parameter an address resolves on the first central that holds it, as before.
  The binding travels in the request context as a value of its own
  (`hmreqctx.WithCentralScope`), separate from the log enrichment's central
  name, and the registry's `Scoped(ctx)` is the one walk the adapters use. An
  explicit `…On(central, …)` method on every port was the alternative; it
  doubles the port surface for some 40 routes and still leaves every route it
  misses answering from the first central.
- **Who the audit log names.** The principal stays the entry's credential.
  The relay names the Home Assistant user in `X-On-Behalf-Of` (an opaque
  label of at most 128 bytes, e.g. `Alex (ha:<id>)`), and the daemon records
  it in `on_behalf_of` beside the principal. It is accepted only from an API
  token or a box API token, the credentials an integration holds for its
  users, ignored from any other principal, and never used for authorization:
  the daemon cannot check it. Every row a request writes carries both, the
  rows written deep in the adapters included, because the actor travels in
  the request context and every producer records through one function.
- **The route list names the method.** `routes` is a list of
  `{method, prefix}`; a `*` segment matches one segment. A path prefix alone
  could not tell the editor's `POST /devices/{addr}/paramsets/…` from a
  lifecycle call such as `POST /devices/{addr}/release`, so a relay that
  enforced `/devices` forwarded both. The retype is API 19.0.0.
- **The change log in the editor.** `GET /audit` stays admin-only, and a
  paired token is never admin (an integration pairs as operator). The editor's
  history reads `GET /devices/{addr}/audit` instead: the device and its
  channels, matched exactly, for every principal with the operator role. The
  device filter of `GET /audit` matches a prefix and could not be reused for a
  scoped read without returning neighbouring devices.

**Better than the comparison targets?** The CCU's WebUI and the aiohomematic
family serve one CCU per connection and never had to name one; the HA
integration's own Lit editors record every change as the integration. Here
one daemon serves several CCUs, and the change log can still say which person
asked for a change without trusting the relay for anything but the label.

**Better than the state before?** A colliding address (a virtual remote, an
internal device, a CUxD device) was edited on whichever CCU sorted first; it is
now edited on the CCU the entry belongs to, or refused. The relay could
forward any `/devices` call, including unpairing; it now forwards only the
editor's calls.

**The right cut?** The daemon owns binding, authorization and the change log;
the relay owns the mapping of its entry to a central, the allow-list it
enforces and the Home Assistant user; the component owns no network access.

An audit row records the central it was made on (the request's binding, or the
central the daemon resolved the device on), so the bound history of an address
that exists on two CCUs lists only that CCU's rows; rows recorded before the
column existed stay in every bound history (#1182).

