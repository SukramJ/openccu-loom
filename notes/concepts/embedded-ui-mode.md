# Embedded UI Mode — Config-surface split when Loom is a Home Assistant backend

- **Status**: Partly implemented — the `embedded` switch and its surface profile ship in 0.55.0 (see [UI surface profiles](./ui-surface-profiles.md)); the cross-repo steps in §5 are open
- **Date**: 2026-07-12, revised 2026-07-27 and 2026-08-08
- **Scope**: `openccu-loom` (this repo) + `homematicip_local` integration + `homematicip-local-frontend` config-panel
- **Related**:
  [ADR 0044 — single-port onboarding and HA Ingress auth passthrough](../../docs/adr/0044-single-port-onboarding-and-ha-ingress-auth.md),
  [ADR 0045 — login and onboarding into SPA](../../docs/adr/0045-login-and-onboarding-into-spa.md),
  [ADR 0051 — northbound authorization model](../../docs/adr/0051-northbound-authorization-model.md),
  [ADR 0054 — remote ingress proxy add-on](../../docs/adr/0054-remote-ingress-proxy-addon.md),
  [UI surface profiles](./ui-surface-profiles.md),
  [HA-native theme bridge](./ha-theme-bridge.md)

---

## ⭐ Recommendation (TL;DR)

> **Adopt a "concern-split hybrid", not "one UI".** When Loom runs as the backend for
> the Home Assistant integration, do **not** show the full Loom SPA and do **not** rebuild
> Loom's ops screens inside HA. Instead:
>
> 1. **Keep the HA config-panel canonical** for everything the user *edits* per device —
>    paramsets, direct links, schedules. Those already run backend-agnostically over
>    `websocket_api.py` and need no change.
> 2. **Embed a slimmed Loom SPA** (`embedded` mode) as one HA sidebar panel, exposing
>    **only** the daemon/CCU-ops screens HA has no equivalent for — firmware, signal,
>    inbox, service messages, backups, visibility, diagnostics, logs, audit, groups,
>    system variables, programs, the alarm system, Security & Safety, fleet, and daemon
>    settings.
> 3. **Hide, at the server, everything that duplicates HA** — Loom's own login/OIDC,
>    setup wizard, user/token admin, CCU-connection admin, room/function admin,
>    device-control tiles, aggregated analytics, Matter, and the paramset/link/schedule
>    *editors* that the HA panel owns.
> 4. **Make the mode an explicit operator opt-in** (`north.ui.embedded`). It is **not**
>    derived from the Ingress signal: since ADR 0054 the same signal also reaches the
>    Remote proxy add-on, which deliberately serves the *full* UI, and an add-on operator
>    who never configures the HA integration would lose surfaces HA cannot replace. Only
>    the operator knows whether HA actually owns the config surface for *this* daemon.
>
> The slimmed mode is the load-bearing piece: without it you either import Loom's second
> login and confusing daemon-admin (embed-whole) or leave two overlapping panels
> (embed-nothing).
>
> **Single sign-on is solved in both add-ons** — the daemon add-on through Ingress
> passthrough (ADR 0044), the Remote add-on through per-instance token injection
> (ADR 0054). Only a bare daemon iframed directly by the integration, with neither add-on
> in front of it, still shows Loom's own login — an accepted, documented trade-off.
>
> **Guiding principle: one capability → one authoritative editor → living where its data
> lives.** The harm today is not "two panels exist" — it is "two panels both claim to own
> the same thing."

---

## 1. Context — the double-UI problem

Two independent config surfaces exist once Loom backs the HA integration:

- **HA config-panel** (`homematicip-local-frontend`, registered by the integration's
  `panel.py`) — an HA sidebar panel, authenticated as the HA user, driven by the
  integration's `websocket_api.py` over `control.central.*`.
- **Loom's own Svelte SPA** (this repo, `assets/ui/`) — served on the REST listener
  (`:8119`), with its own first-run `Setup`, its own `Login`/OIDC, and its own user
  database (ADR 0045). In the **HA add-on** deployment it *also* appears as a second HA
  sidebar panel via Ingress (`packaging/ha-addon/openccu-loom/config.yaml`, `panel_admin: true`).

In the add-on topology the user can literally see **two HA sidebar panels for overlapping
device/CCU concerns**. The concrete overlaps, and why each is harmful:

| Duplicated capability | HA side | Loom side | Assessment |
|---|---|---|---|
| **CCU connection / credentials** | integration `config_flow` (`CONF_LOOM_TOKEN`, host/port/TLS) | `settings → ccus` (`CentralsAdmin.svelte`, add/edit/discover) | Two sources of truth for the same CCU — real footgun |
| **Login / identity** | HA user (`connection.user`) | `Login.svelte` / OIDC + own user DB (`settings → users` / `groups` / `tokens`) | Two credential stores, no SSO |
| **Matter bridge** | HA-native Matter integration | Loom `/matter/*` | Same devices double-exposed |
| **CCU admin** (pairing / firmware / signal / inbox / service messages) | panel CCU dashboard | Loom `/inbox`, `/firmware`, `/signal`, `/messages`, `/backups` | Two surfaces for one device class — **currently live on both** (see §1.1) |
| **Device control / rooms** | HA dashboard / areas | `Overview`, `Favorites`, `RoomsFunctionsAdmin` | Loom side fully redundant inside HA |
| **Paramset / link / schedule editors** | panel Devices tab (native, undo/redo) | `DeviceDetail → Configure`, `links` / `schedule` sub-tabs | Same editor built twice |
| **Aggregated analytics** | HA Energy dashboard + History | Loom `/energy`, `/diagrams` | Same question answered from two datasets |

Since 0.55.0 this repo carries the reduced mode: `north.ui.embedded` selects the
`embedded` surface profile, the navigation gates on it through `NavGates.surfaceVisible`,
and hidden write-gated surfaces refuse the Ingress passthrough identity. What remains open
is everything on the Home Assistant side, which #87 / 2.9.1 now covers (§5). See [UI surface profiles](./ui-surface-profiles.md) for the mechanism; this
document stays the record of *which* capability belongs to which surface, which is the
input that mechanism configures.

### 1.1 Where the surrounding repos stand (2026-08-08)

1. **The loom backend has shipped.** `LOOM_BACKEND_SELECTABLE` is gone: #1227 replaced the
   master switch with a discovery-based gate, after #1163 declared full parity with the
   direct-CCU backend, #1222 added backend-aware flow titles plus an in-place backend
   switch, and #1225/#1229 finished the mDNS browse. `const.py:59-62` now carries plain
   `BACKEND_CCU` / `BACKEND_LOOM` constants with CCU as the default, and the integration is
   at 2.9.0. **The prerequisite the previous revision placed in front of this whole design
   is therefore gone** — loom-backed entries exist in the field, which makes the
   duplication below live rather than hypothetical. One documented gap remains: orphan
   entity-registry cleanup is still skipped for loom
   (`control_unit.py:507-512`), because the loom adapter exposes only a partial
   hub-coordinator.
2. **Frontend #74 is still not reverted.** `CCU_DASHBOARD_BACKENDS` remains
   `["CCU", "openccu-loom"]` (`packages/config-panel/src/homematic-config.ts:32`, applied
   at `:278`), so the panel's CCU dashboard (`views/ccu-dashboard.ts`, sub-tabs
   `general | pairing | messages | signal | firmware`) is live for loom-backed entries and
   has been for about a month. The revert ships in #87 (§5, step 3) — but it
   removes a surface with an installed base, not a fresh change. The dashboard covers
   exactly the capability block this document assigns exclusively to the embedded Loom UI;
   the embedded ops UI is the surface that actually owns daemon-side state, and running
   both is the duplication this design exists to remove.
3. **No second panel is registered.** `homematicip_local/panel.py` still registers exactly
   one backend-agnostic panel — there is no `CONF_BACKEND` branch anywhere in it. Step 2 of
   the plan is unstarted.
4. **A third topology appeared: the Remote proxy add-on** (ADR 0054, 2026-07-19).
   `openccu-loom-remote` iframes the Config UI of one or more *remote* daemons into the HA
   sidebar, injecting a per-instance bearer token so HA admins land in the UI without a
   second login, and forwarding `X-Ingress-Path` with the instance prefix appended. It
   deliberately serves the **full** standalone UI — HA owns nothing there. This is what
   invalidates the auto-derived trigger of the previous revision (§4.1).
5. **The client-side embedding signal is unchanged and stays cosmetic.** `isEmbedded()`
   (`assets/ui/src/lib/theme/ha-bridge.ts:52`) is a bare `window.self !== window.top`
   check driving the HA skin and the live theme mirror — see
   [HA-native theme bridge](./ha-theme-bridge.md). The Remote add-on is the proof that it
   must never gate capability: it renders the same SPA inside an HA iframe while HA owns
   none of it.

### 1.2 What the SPA refactor changed (#503)

The config-UI refactor landed after the previous revision and moves three things this
design depends on:

- **`/access` and `/visibility` no longer exist as routes.** They were folded into
  `settings?tab=users` and `settings?tab=visibility` (`nav.ts:267-270`), and the router
  rewrites the old paths on load, on hash change and from stored preferences
  (`App.svelte:78`, `:104`, `:153`). That rewrite table is the mechanism a hidden-route
  redirect should reuse rather than reinvent.
- **The navigation lives in one place.** `navClusters(gates)` in `assets/ui/src/lib/nav.ts`
  is the single table feeding both the sidebar (`Sidebar.svelte:90-101`) and the start-route
  preference (`landingTargets:292`, `isValidLandingRoute:303`,
  `lib/stores/startRoute.svelte.ts`). Adding `embedded` to `NavGates` therefore gates the
  landing-page selector too — without that, an operator's stored start route can point at a
  view embedded mode hides, and the SPA opens on a redirect.
- **Settings is one home with eighteen tabs in five groups** — `general`, `bridges`,
  `ccus`, `security`, `advanced` (`Settings.svelte:243-261` and `:276-282`), with `changes`
  rendered separately. The tab-level gating this design needs plugs into the existing
  `isAvailable()` filter, which already handles `expertOnly` and `adminOnly`.

Two further facts matter:

- **The posture-transport pattern already exists.** `/energy` and `/diagrams` are gated on
  the `history.v1` capability read from `GET /api/v1/info`
  (`Sidebar.svelte:91-95`, applied in `nav.ts:170-185`; served from
  `internal/north/rest/handlers/info.go:87` and `:139`). §4.3 does not propose a new
  mechanism — it proposes a second value on a working one.
- **`/security` is new and unclassified.** The Security & Safety views
  (`assets/ui/src/routes/security/`, route kind `security` in `App.svelte:274-276`) arrived
  after the previous revision. §3 assigns them.

## 2. Decision — concern-split hybrid + a server-side `embedded` mode

Assign every capability class to exactly one authoritative surface, chosen by *where the
data lives and which editor is already better*:

- **Per-device *editing*** (paramsets MASTER/VALUES with edit sessions + undo/redo,
  direct links, climate/device schedules) → **HA config-panel**. It already works over
  both backends via the `isawaitable` branch in `websocket_api.py` and offers native
  session/undo that the SPA cannot match at that granularity. **No change required here.**
- **Daemon & CCU *operations*** (firmware, signal/RSSI, inbox/install-mode, service
  messages, visibility, backups, diagnostics/RPC-recorder, logs, audit, groups, system
  variables, programs, alarm system, Security & Safety, fleet, and daemon settings:
  MQTT/MCP/REST/mDNS/callback/reliability/persistence/system) → **embedded Loom SPA**. Only
  Loom holds this state (throttle, circuit-breaker, recorder, zone model); HA has no
  equivalent editor.
- **Identity, CCU-connection, first-run, user/token admin, rooms, device tiles,
  aggregated analytics, Matter** → **owned by HA (or Loom-standalone only)**; **hidden in
  embedded mode** so there is exactly one place for each.

This is realised in this repo as a first-class **`embedded` UI mode**: an
operator-declared, server-resolved posture (not a client-side guess) that strips redundant
nav clusters, refuses the corresponding client routes, and — the actual boundary — rejects
the matching write APIs.

## 3. Ownership matrix (all topologies)

The first column covers both standalone deployments: a bare daemon and one reached through
the Remote proxy add-on (ADR 0054), which serves the same full UI.

| Capability class | Loom standalone (incl. Remote add-on) | **Loom as HA backend** | Classic (no Loom) |
|---|---|---|---|
| Paramsets (MASTER/VALUES, session/undo, export/import/copy) | Loom SPA | **HA panel (Devices)** | HA panel (Devices) |
| Direct-link *editing* / peering | Loom SPA (`DeviceDetail → Configure → links`) | **HA panel** | HA panel |
| Direct-link *fleet overview* (`/links`) | Loom SPA | **embedded Loom UI** (read-only, no editor deep-link) | — (does not exist) |
| Schedules (climate / device) | Loom SPA | **HA panel** | HA panel |
| Firmware / signal / inbox | Loom SPA | **embedded Loom UI** | HA panel (CCU dashboard) |
| Visibility (`settings → visibility`) | Loom SPA | **embedded Loom UI** | — (does not exist) |
| Service messages (`/messages`) | Loom SPA | **embedded Loom UI** | HA panel (CCU dashboard) |
| Backups (CCU + daemon) | Loom SPA | **embedded Loom UI** | HA panel (CCU dashboard) |
| Install-mode / pairing | Loom SPA | **embedded Loom UI** | HA panel (CCU dashboard) |
| System info | Loom SPA | **embedded Loom UI** | HA panel (CCU dashboard) |
| System variables (`/sysvars`) incl. channel assignment | Loom SPA | **embedded Loom UI** | — (HA exposes entities, no editor) |
| CCU programs (`/programs`) | Loom SPA | **embedded Loom UI** | — (HA exposes entities, no editor) |
| HmIP groups (`/groups`) | Loom SPA | **embedded Loom UI** | — (does not exist) |
| Alarm system / security zones (`/alarm`) | Loom SPA | **embedded Loom UI** | — (does not exist) |
| Security & Safety classes / sources / faults (`/security`) | Loom SPA | **embedded Loom UI** | — (does not exist) |
| Logs / diagnostics / audit / RPC-recorder / RSSI | Loom SPA | **embedded Loom UI** | — (does not exist) |
| Per-device measurement history (`DeviceDetail → History`) | Loom SPA | **embedded Loom UI** | — (does not exist) |
| Aggregated analytics (`/energy`, `/diagrams`, already `history.v1`-gated) | Loom SPA | **HA Energy dashboard / History** (Loom side hidden) | HA core |
| MQTT / MCP / REST / mDNS / callback / north-bound | Loom SPA | **embedded Loom UI** (settings) | — |
| Daemon lifecycle (restart / update / reliability / persistence) | Loom SPA | **embedded Loom UI** | — |
| Matter bridge | Loom SPA | **HA-native Matter** (Loom side hidden by default, §6) | — |
| CCU connection / credentials | Loom SPA (`settings → ccus`) | **HA `config_flow` only** (Loom side hidden) | HA `config_flow` |
| Login / users / groups / tokens / OIDC | Loom SPA (`settings → security` group) | **HA auth only** (Loom login hidden; add-ons: passthrough / token injection) | HA auth |
| Setup wizard / first-run | Loom SPA (`Setup`) | **hidden** (HA `config_flow` is the entry point) | — |
| HA runtime: entity/device statistics of a config entry, radio levels of its entities | — | **HA panel (Integration tab)** | HA panel (Integration tab) |
| Health / throttle / incidents / cache | Loom SPA (`/diagnostics`) | **embedded Loom UI** — on loom these read `central.health` / `client.command_throttle` through the adapter, so the HA panel was showing daemon state at second hand, for one CCU instead of the fleet (corrected 2026-08-08, shipped in 2.9.1) | HA panel (Integration tab) |
| HA entities / areas / permissions / dashboards | — | **HA core** | HA core |
| Device control tiles / overview / favorites | Loom SPA | **HA dashboard** (Loom tiles hidden) | HA dashboard |
| Rooms / functions | Loom SPA (`RoomsFunctionsAdmin`) | **HA areas** (Loom side hidden) | HA areas |
| Fleet (multi-CCU) | Loom SPA | **embedded Loom UI** (no HA single-entry equivalent) | — |

Three rows deserve their rationale in prose, because they look inconsistent at a glance:

- **`/links` visible, link editing hidden.** Loom's `/links` view is a read-only fleet-wide
  listing (`LinkList.svelte` calls `api.listAllLinks` and nothing else); the actual editor
  lives in `DeviceDetail → Configure → links`, which embedded mode hides. The panel has no
  cross-device link overview, so the two do not collide — but the row deep-links into the
  device editor today (`DeviceDetail.svelte:205-212`), and that jump must be suppressed in
  embedded mode or it lands on a gated tab.
- **Per-device history visible, `/energy` + `/diagrams` hidden.** HA's Energy dashboard and
  History own aggregation, and duplicating them adds a second dataset answering the same
  question. The per-device history tab survives because it is the only way to see the
  recorded curve of a *CCU parameter that is not an HA entity at all* — a debugging
  surface, not an analytics one.
- **`/security` visible although HA has alarm panels.** The Security & Safety domain models
  daemon-side state — class membership, source health, fault latching — that no HA entity
  carries; HA only receives its results (ADR 0059's MQTT plane, plus REST/WS). Editing that
  model has exactly one home, and it is Loom.

## 4. `embedded` mode specification (this repo)

### 4.1 Trigger resolution — explicit, server-resolved, never derived

```
embedded := north.ui.embedded          // *bool, nil → false
```

That is the whole rule. The switch is the master toggle of the profile mechanism in
[UI surface profiles](./ui-surface-profiles.md): what follows in §4.2 is the shipped
**default** of the `embedded` profile, which an admin may adjust per surface — within a
floor that keeps the device list, Settings, the profile editor and the About page
permanently reachable. The previous revision derived the mode from
`ha_ingress active && request carries X-Ingress-Path`; that derivation is now wrong in both
directions:

- **Ingress does not imply HA owns the config surface.** An operator can run the daemon
  add-on for MQTT, REST or Matter and never configure the `homematicip_local` integration
  at all. Auto-enabling embedded mode there would hide paramset editing, CCU credentials
  and identity admin behind an HA panel that offers *no* replacement for any of them.
- **The Ingress signal now also reaches a surface that must stay full.** The Remote proxy
  add-on forwards `X-Ingress-Path` with the instance prefix appended (ADR 0054 §4) —
  deliberately, so remote daemons keep generating correct ingress-aware redirects. Deriving
  posture from that header would silently amputate the UI of every remote instance.

Only the operator knows whether HA owns the config surface for *this* daemon, so the
posture is declared, not detected. The cost is one switch; the benefit is that the mode
cannot mis-fire in either topology, and that the resolution is **static**: no
request-scoped promotion, no per-request divergence, and `Info` stays the
request-independent handler it is today (`internal/north/rest/handlers/info.go:121-134`
ignores its `*http.Request`).

Implementation notes:

- Add `Embedded *bool` to `NorthUI` (`internal/config/config.go:1116-1129`). Unlike
  `Enabled` (nil → true) this defaults **off**: a daemon that was never told HA owns its
  config surface must serve everything.
- Document it in `example.config.full.yaml` next to `enabled` (`:270-274`), and add
  `config.field.north.ui.embedded` + `config.help.north.ui.embedded` in **both** i18n
  catalogues — enforced by `TestConfigFieldsHaveLabelsAndHelp` (CLAUDE.md, Critical Rules).
- Add the curated env overlay `OPENCCU_LOOM_UI_EMBEDDED` (`internal/config/overlay.go:60`
  is the pattern) so the daemon add-on can expose it as a plain HA add-on option through
  `rootfs/usr/bin/run.sh`, which maps `bashio::config` values onto `OPENCCU_LOOM_*`. That
  keeps the switch reachable from the HA UI for the deployment where it is most often
  wanted.

**The client's `isEmbedded()` stays cosmetic.** `assets/ui/src/lib/theme/ha-bridge.ts:52`
detects the iframe to force the HA skin and mirror the live HA theme. It must **not** feed
nav or route gating: an iframe is not proof of an HA-owned deployment — the Remote add-on
is the standing counter-example — and a second truth channel invites divergence between
what is hidden and what is refused. The SPA learns its posture from the server only (§4.3).

### 4.2 Nav + route taxonomy

**Hidden in embedded mode** (owned by HA, or a second identity/duplicate):

- Pre-auth: `Setup.svelte`, `Login.svelte` / OIDC
- `/overview`, `/favorites`, `RoomsFunctionsAdmin`, theme/locale chrome, mobile-nav chrome
  (`/about` itself stays reachable — see the floor in
  [UI surface profiles](./ui-surface-profiles.md) §2.5 — and merely drops its marketing
  header)
- `settings → security` group in full (`oidc`, `ccu_auth`, `users`, `groups`, `tokens`)
  and `settings → ccus`
- `/energy`, `/diagrams` (HA Energy + History own aggregation; already `history.v1`-gated,
  so embedded adds a second gate rather than replacing one)
- `/matter` and `settings → matter` (HA owns Matter — see §6)
- **`DeviceDetail → Configure`** (paramset / link / schedule editors) — the HA panel owns
  these; the `/links` row deep-link into that tab is suppressed with it

**Visible in embedded mode** (no HA equivalent, or HA has no editor):

- `/inbox`, `/firmware`, `/signal`, `/messages`, `/backups`, `/diagnostics`, `/logs`,
  `/audit`, `/fleet`
- `/sysvars`, `/programs`, `/groups`, `/alarm`, `/security`
- `/links` (read-only fleet overview)
- `/devices` + `DeviceDetail → Overview` / `History`
- `settings → general` (`general`, `system`), `changes`, and the remaining `bridges`
  members (`mqtt`, `mcp`, `rest`, `discovery`), plus `callback`, `visibility`,
  `reliability`, `persistence`

Against the cluster table in `nav.ts:75-259` that means: `automation` survives intact,
`overview` loses its device tiles and favorites but keeps `alarm`, `security`, `inbox` and
`fleet`, `diagnose` loses only the analytics pair, `bridges` disappears with Matter, and
`system` keeps firmware, backups, settings and about. Within `settings → rest` the
listener/TLS/CORS fields stay editable while the auth sub-fields follow the same read-only
rule as `users`/`tokens` — they configure the identity system HA owns.

The `DeviceDetail` top tabs are `overview | configure | history`
(`DeviceDetail.svelte:53-54`, rendered at `:586-590`); only `configure` is gated off, so
the same device is browsable in both panels but only *editable* in one.

### 4.3 Enforcement — server-resolved, API-enforced

The first revision proposed blocking hidden routes "at the router". That is not
implementable as written: the SPA is **hash-routed** (`location.hash`,
`assets/ui/src/App.svelte:65-160`), and fragments are never transmitted to the server, so
no HTTP handler can ever see `#/settings?tab=users`. The enforcement therefore splits into
three layers, with the real boundary at the API:

1. **Posture transport**: surface the resolved `embedded` flag in the bootstrap payload the
   SPA already fetches — `GET /api/v1/info` carries `capabilities []string`
   (`internal/north/rest/handlers/info.go:87`, composed at `:139`), which already gates
   `/energy` and `/diagrams` through `history.v1`. A second token (or a dedicated boolean
   field) rides the same path. Either way the value is **server-computed**, never inferred
   client-side.
2. **Nav + client route guard**: extend `NavGates` (`nav.ts:62-66`) with `embedded` and gate
   the affected clusters and items inside `navClusters`. Because that table also feeds the
   start-route preference (`landingTargets:292`, `isValidLandingRoute:303`), the landing
   selector then offers only reachable views for free. Mirror the gate in the
   `isAvailable()` filter of `Settings.svelte:289-293`, and route hidden paths through the
   existing rewrite mechanism (`foldedRouteTarget`, `nav.ts:267-284`, applied in
   `App.svelte:78`, `:104`, `:153`) onto an allowed route instead of rendering them.
   Suppress the `/links → DeviceDetail → Configure` deep link
   (`DeviceDetail.svelte:205-212`) with the same gate. This closes deep links *within* the
   SPA; it is a UX guarantee, not a security boundary.
   *Timing caveat*: honour the boot-race rule already documented at `nav.ts:307-326` — a
   stored start route is validated against the weaker "does this view exist" check, because
   the capability gates are still resolving during first paint. `embedded` arrives on the
   same payload as `history.v1` and belongs in the same class: apply it once resolved,
   never against an unresolved store.
3. **API enforcement (the actual boundary)**: the hidden surfaces all have REST/WS
   counterparts (users, tokens, centrals, OIDC, paramset writes, link create/delete,
   schedule writes, Matter). These reject writes for the passthrough identity per the
   northbound authorization model (ADR 0051), so a hidden UI cannot be bypassed by
   hand-editing the hash or calling the API directly. Reads stay open — the HA panel needs
   them.
   **This is the one part of the design that did not survive contact with the code.**
   The integration authenticates with a bearer token, so it was never subject to the
   passthrough scoping — the enforcement only ever reached a browser opening this daemon's
   UI through the Ingress panel, defended nothing, and shipped removed. See
   [UI surface profiles](./ui-surface-profiles.md) §2.8. Hiding is navigation; ADR 0051
   remains the authorization model, unchanged by any profile.

### 4.4 Auth — SSO in both add-ons, not for a bare daemon

- **Daemon add-on**: Ingress passthrough (ADR 0044) means HA's admin gate *is* the auth
  boundary; no second login. Loom's `Login`/`Setup` are hidden because they are genuinely
  unneeded.
- **Remote add-on** (ADR 0054): the proxy injects `Authorization: Bearer <token>` per
  instance, so HA admins reach the UI without a second login across the network boundary.
  Option (b) of the previous revision — "seed the SPA session from a long-lived token" —
  therefore exists in production, just as a proxy concern rather than a daemon one. Note
  the combination is only *correct* when the remote daemon is also the integration's
  backend: the proxy itself owns no posture and serves whatever the daemon declares.
- **Bare daemon iframed directly by the integration**: there is **no SSO**. The iframe hits
  Loom's own auth. Preference order: (a) point operators at one of the two add-ons, which
  is now a complete answer for both local and remote daemons; (b) accept the second login.
  **This trade-off must be stated in user docs, not hidden.**

## 5. Cross-repo implementation plan

**Status of the former prerequisite:** the loom backend is no longer gated off — the master
switch was replaced by a discovery-based gate (#1227) after the parity work in #1163, and
the dual-backend surface contract, behavioural parity probes and parity ratchet from #1220
guard the adapter. The duck-type remains incomplete in one documented place — orphan entity
cleanup is skipped for loom (`control_unit.py:507-512`) — but that no longer blocks this
design. Step 1 shipped in 0.55.0 and the duplicated tabs went in 2.9.1.

1. **This repo — build `embedded` mode** (largest new work):
   - `internal/config`: add `north.ui.embedded` (`*bool`, nil → false), document it in
     `example.config.full.yaml`, add `config.field.*` + `config.help.*` in en + de, and add
     the `OPENCCU_LOOM_UI_EMBEDDED` overlay plus the add-on option that feeds it.
   - Surface the resolved flag in the `GET /api/v1/info` capability payload.
   - `lib/nav.ts`: `embedded` in `NavGates`, applied in `navClusters` — the sidebar and the
     start-route selector inherit it. `Settings.svelte`: same gate in `isAvailable()`.
   - `App.svelte`: redirect hidden hash routes through the existing rewrite table; suppress
     the `/links` → `Configure` deep link.
   - ADR 0051 alignment: the passthrough identity is read-only on the hidden admin and
     editor APIs.
2. **~~Integration — second panel registration~~ — dropped, it was never needed.**
   The original plan had `homematicip_local/panel.py` register a second sidebar panel
   iframing the Loom Ingress URL. Both add-ons already register one themselves
   (`panel_admin: true` in each `config.yaml`), so an integration-registered panel would be
   a **third** sidebar entry for the same surface. The only deployment without a Loom panel
   is a bare daemon behind neither add-on — the same case §4.4 already marks as
   best-effort. Removing the duplication where it exists (steps 3 and 4) is the smaller and
   more honest change.
3. **Frontend — remove the duplicated tabs for loom-backed entries.** Shipped in
   `homematicip-local-frontend` #87 / `homematicip_local` 2.9.1:
   - The **CCU dashboard** is re-gated to `backend === "CCU"` (the #74 revert). Its
     capability block — inbox, firmware, signal quality, service messages, backups — is
     exactly what the Loom UI owns, and the Loom UI covers **every** CCU the daemon serves
     while the panel dashboard only ever showed the one behind the selected config entry.
   - The **Integration tab** keeps only what HA itself knows: the config entry's device
     statistics and the radio levels of its own entities. Health, command throttling and
     incidents read `central.health` / `client.command_throttle`, which on loom is daemon
     state fetched through the adapter — the Loom UI shows it under Diagnostics for the
     whole fleet. A note in the tab names where the cards went.
4. **Integration — document CCU-credential ownership.** `config_flow` is the single source
   for the CCU connection when on Loom; `settings → ccus` is hidden in embedded mode.
   Recorded in `docs/architecture-comparison-aiohomematic-vs-loom.md` (2.9.1).

## 6. Consequences, risks, open questions

- **The #74 revert had an installed base.** It was live from 2026-07-13, and the loom
  backend shipped to users in between, so step 3 removes a surface people use.
  Ship it in the same release train as the embedded panel, and note it in the integration
  changelog as a move, not a removal.
- **Explicit opt-in costs discoverability.** Nothing turns embedded mode on by itself, so an
  operator who wires HA to a Loom daemon and never finds the switch keeps both panels — the
  status quo, which is at least not worse than today. Mitigations: expose it as an add-on
  option (§4.1), and have the integration's loom setup step name it. Detection is
  deliberately not the answer; §4.1 records why.
- **Embedded is a property of the daemon, not of the viewer.** A daemon in embedded mode
  serves the reduced UI to *everyone* — the standalone browser tab and the Remote proxy
  alike. That is the correct trade for the "HA owns this CCU" case, but it means embedded
  must never be set on a daemon whose config surface HA does not own.
- **Three credentials still coexist:** HA user, Loom SPA user, and the machine
  `CONF_LOOM_TOKEN` (plus the Remote add-on's per-instance token, which is usually the
  same class of secret). Embedded mode *hides* the Loom user login; it does not remove the
  user DB. Token rotation/expiry needs a defined flow.
- **CSP / iframe:** Loom deliberately sets no `X-Frame-Options` / `frame-ancestors`
  (`internal/north/rest/middleware/middleware.go`), so iframing works. Under Ingress it is
  same-origin. **Open:** HA frontend CSP for a non-add-on (foreign-origin) embed.
- **Latent duplicate data source:** paramset/link/schedule editors exist in code twice
  (HA Lit + Loom Svelte for standalone). Ownership is assigned (HA wins inside HA) but the
  Loom copies keep drifting. The `isawaitable` branch is the visible seam; #1220's
  call-shape inventory is the drift detector.
- **New Loom surfaces default to hidden-until-classified.** `/security` is the latest
  example: it appeared after the previous revision with no owner, exactly as `/groups`,
  `/links` and `/diagrams` did before it. Every future SPA route must be assigned a row in
  §3 as part of its PR, otherwise embedded mode silently either leaks a duplicate or hides
  a unique capability.
- **Versioning:** embedded mode couples the HA release cadence to the Loom SPA version. A
  Loom UI regression would break the HA config experience. Needs a compatibility/minimum-
  version gate across `homematicip_local`, `openccu-loom-client`, and the daemon — the same
  discipline already applied to the aiohomematic pin.
- **Fleet does not fit HA's single-entry model.** Loom's multi-CCU `/fleet` view stays in
  the embedded ops UI; the conceptual tension (HA thinks per config-entry/CCU, Loom thinks
  fleet) remains unresolved.
- **Matter ownership** is settled as *hidden by default* in embedded mode: HA owns Matter
  natively and double-exposing the same devices to a controller is worse than losing a
  configuration surface. An operator who deliberately runs Loom's bridge behind HA can
  re-enable the route via an explicit config key — but that is an expert deviation, not
  the default.

## 7. Alternatives considered

- **A — HA panel as the single canonical surface** (rebuild all CCU-ops as HA WS commands,
  reduce Loom UI to logs/diagnostics only). Rejected: requires finishing the partial Loom
  hub-coordinator *and* re-implementing thousands of lines of working Svelte (pairing,
  firmware, backup, groups, alarm, Security & Safety) as WS commands — high cost, no UX
  gain, and fleet doesn't fit HA at all.
- **B — Embed the whole Loom SPA, delete the HA Devices tab.** Rejected: discards a working,
  HA-authenticated, native paramset editor (with undo/redo) for a seamed iframe, imports
  Loom's *second identity system* (separate user DB, OIDC, no SSO — the actual user-facing
  harm), and strands the classic aiohomematic majority, whose Devices tab must keep existing
  anyway. A duplicated widget is cheaper to maintain than a duplicated login.
- **C — Keep both CCU surfaces (accept #74 as the end state)**, framing the panel dashboard
  as a read-only quick view and the embedded Loom UI as the full ops surface. Rejected, and
  re-examined on 2026-08-08 now that it is the de-facto state: the two are not actually
  read-only-vs-full — the panel dashboard triggers install mode and firmware updates, so
  both issue writes against the same CCU with different concurrency assumptions, and users
  have no way to know which one is authoritative. "Two panels both claim to own the same
  thing" is the problem statement, not a compromise.
- **D — Derive the mode from the Ingress signal** (`ha_ingress` resolved ON plus
  `X-Ingress-Path` on the request), the trigger the 2026-07-27 revision specified.
  Rejected on 2026-08-08: it answers "am I behind HA Ingress?", while the question is "does
  HA own my config surface?" — two propositions that came apart when ADR 0054 put a proxy
  in front of remote daemons and when add-on operators ran the daemon without the
  integration. It also forced a request-scoped posture, which a static config value avoids
  entirely.

## Revision history

- **2026-07-12** — first version.
- **2026-07-27** — decided to revert `homematicip-local-frontend` #74 rather than absorb it
  (§1.1, §5.5, alternative C); classified the eight previously unassigned routes and the
  three added since (§3, §4.2), keeping `/sysvars`, `/programs`, `/groups`, `/alarm`,
  `/links`, `/messages` in embedded mode and dropping `/energy`, `/diagrams` to HA; fixed
  §4.3, whose server-side hash-route guard was not implementable (hash fragments never
  reach the server) — the boundary is now the API, with the SPA posture served from
  `GET /api/v1/info`; scoped the client `isEmbedded()` to theming only (§4.1); refreshed
  all source references and the integration status after `homematicip_local` #1220.
- **2026-08-08** — replaced the auto-derived trigger with an explicit `north.ui.embedded`
  opt-in (§4.1, alternative D), because ADR 0054's Remote proxy add-on forwards the same
  Ingress signal while deliberately serving the full UI, and because the daemon add-on runs
  in deployments that never configure the HA integration; recorded that the integration's
  loom backend has shipped (#1227 replaced `LOOM_BACKEND_SELECTABLE` with a discovery gate,
  after #1163), so §5's blocking prerequisite is gone while #74's duplication is live and
  its revert now has an installed base (§1.1, §6); re-based §4.2/§4.3 on the config-UI
  refactor — navigation now lives in `lib/nav.ts` with `NavGates`, `/access` and
  `/visibility` are folded into settings tabs, and the start-route preference reads the
  same table, so the gate must cover it (§1.2); classified the new `/security` route (§3);
  noted that `history.v1` already proves the capability-transport pattern (§1.2, §4.3);
  refreshed every source reference.
