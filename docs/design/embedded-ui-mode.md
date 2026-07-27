# Embedded UI Mode — Config-surface split when Loom is a Home Assistant backend

- **Status**: Proposed
- **Date**: 2026-07-12, revised 2026-07-27
- **Scope**: `openccu-loom` (this repo) + `homematicip_local` integration + `homematicip-local-frontend` config-panel
- **Related**:
  [ADR 0044 — single-port onboarding and HA Ingress auth passthrough](../adr/0044-single-port-onboarding-and-ha-ingress-auth.md),
  [ADR 0045 — login and onboarding into SPA](../adr/0045-login-and-onboarding-into-spa.md),
  [ADR 0051 — northbound authorization model](../adr/0051-northbound-authorization-model.md),
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
>    system variables, programs, the alarm system, fleet, and daemon settings.
> 3. **Hide, at the server, everything that duplicates HA** — Loom's own login/OIDC,
>    setup wizard, user/token admin, CCU-connection admin, room/function admin,
>    device-control tiles, aggregated analytics, Matter, and the paramset/link/schedule
>    *editors* that the HA panel owns.
>
> The slimmed mode is the load-bearing piece: without it you either import Loom's second
> login and confusing daemon-admin (embed-whole) or leave two overlapping panels
> (embed-nothing). **The embedded mode is offered for *any* Loom backend**, but a true
> single sign-on is only guaranteed in the **HA add-on** deployment (Ingress passthrough,
> ADR 0044); a manually-run daemon behind the integration still shows Loom's own login in
> the iframe — an accepted, documented trade-off.
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
| **Login / identity** | HA user (`connection.user`) | `Login.svelte` / OIDC + own user DB (`AccessControl.svelte`) | Two credential stores, no SSO |
| **Matter bridge** | HA-native Matter integration | Loom `/matter/*` | Same devices double-exposed |
| **CCU admin** (pairing / firmware / signal / inbox / service messages) | panel CCU dashboard | Loom `/inbox`, `/firmware`, `/signal`, `/messages`, `/backups` | Two surfaces for one device class — **currently live on both** (see §1.1) |
| **Device control / rooms** | HA dashboard / areas | `Overview`, `Favorites`, `RoomsFunctionsAdmin` | Loom side fully redundant inside HA |
| **Paramset / link / schedule editors** | panel Devices tab (native, undo/redo) | `DeviceDetail → Configure`, `links` / `schedule` sub-tabs | Same editor built twice |
| **Aggregated analytics** | HA Energy dashboard + History | Loom `/energy`, `/diagrams` | Same question answered from two datasets |

No reduced/embedded UI mode exists today. `north.ui` only gates the two no-JS
diagnostic pages (`/health`, `/about`) — `NorthUI` carries a single `Enabled *bool`
(`internal/config/config.go:1056-1069`). `Sidebar.svelte` builds its full five-cluster nav
unconditionally except for the existing `admin`-role, expert-mode and `matterEnabled`
gates (`assets/ui/src/lib/components/ui/Sidebar.svelte:129-341`).

### 1.1 What changed since the first revision (2026-07-12)

Three developments in the surrounding repos invalidate parts of the original text:

1. **The CCU dashboard was un-gated for loom.** `homematicip-local-frontend` #74
   (`4c9e37e`, 2026-07-13) replaced the `backend === "CCU"` gate with
   `CCU_DASHBOARD_BACKENDS = ["CCU", "openccu-loom"]`
   (`packages/config-panel/src/homematic-config.ts:32`, applied at `:278`), because the
   literal gate hid the whole dashboard from loom-backed entries. That dashboard
   (`views/ccu-dashboard.ts`, sub-tabs `general | pairing | messages | signal |
   firmware`) covers exactly the capability block this document assigns exclusively to
   the embedded Loom UI. **Decision: this is reverted** — see §5.5. The dashboard's
   value for loom-backed entries is delivered by the embedded Loom ops UI, which is the
   surface that actually owns daemon-side state; running both is the duplication this
   design exists to remove.
2. **The dual-backend adapter matured.** `homematicip_local` #1220 landed a backend
   surface contract (AST-inventoried `central.*` call shapes checked against both backend
   classes), behavioural e2e parity probes across ccu/loom/mqtt, a parity ratchet and a CI
   gate — with four documented loom call-shape exemptions. The Loom duck-type is no longer
   the open-ended cost centre the first revision described, though it is still incomplete
   (orphan entity cleanup remains skipped for loom, `control_unit.py:506-513`) and
   `LOOM_BACKEND_SELECTABLE` is still `False` (`const.py:65`).
3. **An embedding signal already exists in the SPA.** `isEmbedded()`
   (`assets/ui/src/lib/theme/ha-bridge.ts:52`) detects the iframe situation and forces the
   HA skin, mirroring the live HA theme — see [HA-native theme bridge](./ha-theme-bridge.md).
   It is **cosmetic only** and stays that way; §4.1 defines the authoritative signal.

Additionally the SPA grew routes the first revision never classified — `/groups`,
`/links`, `/diagrams` are new since then, and `/sysvars`, `/programs`, `/messages`,
`/energy`, `/alarm` were never assigned. §3 and §4.2 now cover the complete route table
(`assets/ui/src/App.svelte:204-229`).

## 2. Decision — concern-split hybrid + a server-side `embedded` mode

Assign every capability class to exactly one authoritative surface, chosen by *where the
data lives and which editor is already better*:

- **Per-device *editing*** (paramsets MASTER/VALUES with edit sessions + undo/redo,
  direct links, climate/device schedules) → **HA config-panel**. It already works over
  both backends via the `isawaitable` branch in `websocket_api.py` and offers native
  session/undo that the SPA cannot match at that granularity. **No change required here.**
- **Daemon & CCU *operations*** (firmware, signal/RSSI, inbox/install-mode, service
  messages, visibility, backups, diagnostics/RPC-recorder, logs, audit, groups, system
  variables, programs, alarm system, fleet, and daemon settings: MQTT/MCP/REST/mDNS/
  callback/reliability/persistence/system) → **embedded Loom SPA**. Only Loom holds this
  state (throttle, circuit-breaker, recorder); HA has no equivalent editor.
- **Identity, CCU-connection, first-run, user/token admin, rooms, device tiles,
  aggregated analytics, Matter** → **owned by HA (or Loom-standalone only)**; **hidden in
  embedded mode** so there is exactly one place for each.

This is realised in this repo as a first-class **`embedded` UI mode**: a server-resolved
posture (not a client-side guess) that strips redundant nav clusters, refuses the
corresponding client routes, and — the actual boundary — rejects the matching write APIs.

## 3. Ownership matrix (all three topologies)

| Capability class | Loom standalone | **Loom as HA backend** | Classic (no Loom) |
|---|---|---|---|
| Paramsets (MASTER/VALUES, session/undo, export/import/copy) | Loom SPA | **HA panel (Devices)** | HA panel (Devices) |
| Direct-link *editing* / peering | Loom SPA (`DeviceDetail → Configure → links`) | **HA panel** | HA panel |
| Direct-link *fleet overview* (`/links`) | Loom SPA | **embedded Loom UI** (read-only, no editor deep-link) | — (does not exist) |
| Schedules (climate / device) | Loom SPA | **HA panel** | HA panel |
| Firmware / signal / inbox / visibility | Loom SPA | **embedded Loom UI** | HA panel (CCU dashboard) |
| Service messages (`/messages`) | Loom SPA | **embedded Loom UI** | HA panel (CCU dashboard) |
| Backups (CCU + daemon) | Loom SPA | **embedded Loom UI** | HA panel (CCU dashboard) |
| Install-mode / pairing | Loom SPA | **embedded Loom UI** | HA panel (CCU dashboard) |
| System info | Loom SPA | **embedded Loom UI** | HA panel (CCU dashboard) |
| System variables (`/sysvars`) incl. channel assignment | Loom SPA | **embedded Loom UI** | — (HA exposes entities, no editor) |
| CCU programs (`/programs`) | Loom SPA | **embedded Loom UI** | — (HA exposes entities, no editor) |
| HmIP groups (`/groups`) | Loom SPA | **embedded Loom UI** | — (does not exist) |
| Alarm system / security zones (`/alarm`) | Loom SPA | **embedded Loom UI** | — (does not exist) |
| Logs / diagnostics / audit / RPC-recorder / RSSI | Loom SPA | **embedded Loom UI** | — (does not exist) |
| Per-device measurement history (`DeviceDetail → History`) | Loom SPA | **embedded Loom UI** | — (does not exist) |
| Aggregated analytics (`/energy`, `/diagrams`) | Loom SPA | **HA Energy dashboard / History** (Loom side hidden) | HA core |
| MQTT / MCP / REST / mDNS / callback / north-bound | Loom SPA | **embedded Loom UI** (settings) | — |
| Daemon lifecycle (restart / update / reliability / persistence) | Loom SPA | **embedded Loom UI** | — |
| Matter bridge | Loom SPA | **HA-native Matter** (Loom side hidden by default, §6) | — |
| CCU connection / credentials | Loom SPA (`CentralsAdmin`) | **HA `config_flow` only** (Loom side hidden) | HA `config_flow` |
| Login / users / groups / tokens / OIDC | Loom SPA (`AccessControl`) | **HA auth only** (Loom login hidden; add-on: Ingress passthrough) | HA auth |
| Setup wizard / first-run | Loom SPA (`Setup`) | **hidden** (HA `config_flow` is the entry point) | — |
| HA runtime: health / throttle / incidents / cache | — | **HA panel (Integration tab)** | HA panel (Integration tab) |
| HA entities / areas / permissions / dashboards | — | **HA core** | HA core |
| Device control tiles / overview / favorites | Loom SPA | **HA dashboard** (Loom tiles hidden) | HA dashboard |
| Rooms / functions | Loom SPA (`RoomsFunctionsAdmin`) | **HA areas** (Loom side hidden) | HA areas |
| Fleet (multi-CCU) | Loom SPA | **embedded Loom UI** (no HA single-entry equivalent) | — |

Two rows deserve their rationale in prose, because they look inconsistent at a glance:

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

## 4. `embedded` mode specification (this repo)

### 4.1 Trigger resolution — server-authoritative, auto-derived, explicitly overridable

The mode resolves **on the server**:

```
embedded := north.ui.embedded            // explicit override, tri-state *bool
            ?? (ha_ingress active         // HAIngressConfig resolved ON (ADR 0044)
                && request carries X-Ingress-Path)
```

- **Add-on deployment** → zero-config: `ha_ingress` resolves ON via the supervised stamp
  (`HAIngressConfig.Enabled` nil-default, `internal/config/config.go`), Supervisor sends
  `X-Ingress-Path` (read by the ingress-prefix helper at
  `internal/north/rest/router.go:1157-1162`) → embedded ON automatically.
- **Manual daemon behind the HA integration** → the operator sets `north.ui.embedded: true`
  explicitly (there is no Ingress signal to derive from). This is the case that answers
  "embedded for *any* backend, not only the add-on."
- **Standalone Loom** → both signals absent → embedded OFF → full SPA, unchanged.

Add a pointer-bool `Embedded *bool` to the `north.ui` config section (`NorthUI`,
`internal/config/config.go:1056-1069`), documented in `example.config.full.yaml` next to
`enabled`, with `config.field.north.ui.embedded` + `config.help.north.ui.embedded` in both
i18n catalogues (see CLAUDE.md, Critical Rules). Because the effective value can depend on
a per-request header, resolve a **base** value from config at composition and let the
request-scoped middleware promote it when `X-Ingress-Path` is present (mirrors how the
Ingress prefix is already threaded).

**The client's `isEmbedded()` stays cosmetic.** `assets/ui/src/lib/theme/ha-bridge.ts:52`
detects the iframe to force the HA skin and mirror the live HA theme. It must **not** feed
nav or route gating: an iframe is not proof of an HA-owned deployment, and a second
truth channel invites divergence between what is hidden and what is refused. The SPA learns
its posture from the server only (§4.3).

### 4.2 Nav + route taxonomy

**Hidden in embedded mode** (owned by HA, or a second identity/duplicate):

- Pre-auth: `Setup.svelte`, `Login.svelte` / OIDC
- `/overview`, `/favorites`, `RoomsFunctionsAdmin`, theme/locale chrome, `/about`
  branding, mobile-nav chrome
- `/access` (`AccessControl`), `settings → ccus` / `oidc` / `ccu_auth` / `users` /
  `groups` / `tokens`
- `/energy`, `/diagrams` (HA Energy + History own aggregation)
- `/matter` and `settings → matter` (HA owns Matter — see §6)
- **`DeviceDetail → Configure`** (paramset / link / schedule editors) — the HA panel owns
  these; the `/links` row deep-link into that tab is suppressed with it

**Visible in embedded mode** (no HA equivalent, or HA has no editor):

- `/inbox`, `/firmware`, `/signal`, `/messages`, `/visibility`, `/backups`,
  `/diagnostics`, `/logs`, `/audit`, `/fleet`
- `/sysvars`, `/programs`, `/groups`, `/alarm`
- `/links` (read-only fleet overview)
- `/devices` + `DeviceDetail → Overview` / `History`
- `settings → general` / `system` / `changes` / `mqtt` / `mcp` / `rest` / `discovery` /
  `callback` / `reliability` / `persistence`

Two nav clusters survive intact (`automation`, `system`), `overview` loses its device
tiles but keeps `alarm`/`inbox`/`fleet`, `diagnose` loses only the analytics pair, and
`bridges` disappears with Matter (`Sidebar.svelte:129-341`). Within `settings → rest` the
listener/TLS/CORS fields stay editable while the auth sub-fields follow the same
read-only rule as `users`/`tokens` — they configure the identity system HA owns.

The `DeviceDetail → Overview`/`History` tabs stay; only the `Configure` tab is gated off
(`DeviceDetail.svelte:587-606`), so the same device is browsable in both panels but only
*editable* in one.

### 4.3 Enforcement — server-resolved, API-enforced

The first revision proposed blocking hidden routes "at the router". That is not
implementable as written: the SPA is **hash-routed** (`location.hash`,
`assets/ui/src/App.svelte:67-112`), and fragments are never transmitted to the server, so
no HTTP handler can ever see `#/access`. The enforcement therefore splits into three
layers, with the real boundary at the API:

1. **Posture transport**: surface the resolved `embedded` flag in the bootstrap payload the
   SPA already fetches — `GET /api/v1/info` carries a `capabilities []string`
   (`internal/north/rest/handlers/info.go:79`, filled at `:121`), which is the natural
   carrier; a dedicated boolean field is equally acceptable. Either way the value is
   **server-computed**, never inferred client-side.
2. **Nav + client route guard**: extend the existing cluster gating in
   `Sidebar.svelte:129-341` (already gates on `admin` / expert / `matterEnabled`) and the
   tab list in `Settings.svelte:195-228` with an `embedded` gate, and make the route
   resolver in `App.svelte:204-229` map every hidden path to a redirect onto an allowed
   route instead of rendering it. This closes deep links *within* the SPA; it is a UX
   guarantee, not a security boundary.
3. **API enforcement (the actual boundary)**: the hidden surfaces all have REST/WS
   counterparts (users, tokens, centrals, OIDC, paramset writes, link create/delete,
   schedule writes, Matter). Under embedded mode these reject writes for the passthrough
   identity per the northbound authorization model (ADR 0051), so a hidden UI cannot be
   bypassed by hand-editing the hash or calling the API directly. Reads stay open — the
   HA panel needs them.

### 4.4 Auth — SSO guaranteed only in the add-on

- **Add-on**: Ingress passthrough (ADR 0044) means HA's admin gate *is* the auth boundary;
  no second login. Loom's `Login`/`Setup` are hidden because they are genuinely unneeded.
- **Manual daemon (embedded via explicit key)**: there is **no SSO**. The iframe will hit
  Loom's own auth. Mitigation, in order of preference: (a) document that supervised add-on
  is the supported single-login path; (b) allow a long-lived token / the integration's
  `CONF_LOOM_TOKEN` to seed an SPA session so the human login is skipped; (c) accept the
  second login. **This trade-off must be stated in user docs, not hidden.**

## 5. Cross-repo implementation plan

**Prerequisite (integration, `homematicip_local`):** the Loom backend is still gated off
(`LOOM_BACKEND_SELECTABLE = False`, `const.py:65`). The dual-backend surface contract,
behavioural parity probes and the parity ratchet from #1220 now guard the adapter, but the
duck-type is still partial — orphan entity cleanup is skipped for loom
(`control_unit.py:506-513`) and four call-shape gaps are tracked as exemptions. Nothing
here ships before those close.

1. **This repo — build `embedded` mode** (largest new work):
   - `internal/config`: add `north.ui.embedded` (`*bool`), document it in
     `example.config.full.yaml`, add `config.field.*` + `config.help.*` in en + de.
   - Resolve the effective `embedded` (config ?? ingress signal) and surface it in the
     `GET /api/v1/info` bootstrap payload.
   - `Sidebar.svelte` + `Settings.svelte`: `embedded` gate on clusters and tabs.
   - `App.svelte`: redirect hidden hash routes; suppress the `/links` → `Configure`
     deep-link.
   - ADR 0051 alignment: the passthrough identity is read-only on the hidden admin and
     editor APIs.
2. **Integration — second panel registration** (`homematicip_local/panel.py`): for
   Loom-backed entries, register a sidebar panel that iframes the embedded Loom Ingress
   URL. `panel.py` currently registers a single backend-agnostic panel; the new one
   branches on `CONF_BACKEND`.
3. **Integration — leave `websocket_api.py` untouched.** The Devices/Integration tabs
   already run on Loom via the `isawaitable` branches; that is the documented dual-backend
   contract, now guarded by the #1220 surface tests.
4. **Integration — document CCU-credential ownership.** `config_flow` is the single source
   for the CCU connection when on Loom; `CentralsAdmin` is hidden in embedded mode. No code
   change, but state the ownership.
5. **Frontend — revert #74 and re-gate the CCU dashboard on `backend === "CCU"`**
   (`homematic-config.ts:32`, applied at `:278`). Un-gating it for loom made the panel and
   the Loom SPA both claim pairing, inbox, firmware, signal and service messages — the
   exact duplication this design removes. The loom-backed replacement is the embedded ops
   UI, so the revert must land **together with** step 2, never before it: between the two
   commits a loom-backed entry would have no CCU dashboard at all.

## 6. Consequences, risks, open questions

- **Sequencing risk from the #74 revert.** Step 5 removes a surface users can see today.
  Ship it in the same release train as the embedded panel, and note it in the integration
  changelog as a move, not a removal.
- **Auth/SSO is the hardest edge.** Clean single-login exists only under the add-on
  (supervised + `trusted_proxy_cidr` + `X-Ingress-Path`). The manual-daemon path has no SSO
  by construction — a real regression vs. a single native panel. **Open:** is
  "Loom-as-HA-backend" officially "Loom-as-add-on," with manual-daemon embedded marked
  best-effort? Decide and document.
- **Three credentials still coexist:** HA user, Loom SPA user, and the machine
  `CONF_LOOM_TOKEN`. Embedded mode *hides* the Loom user login; it does not remove the user
  DB. Token rotation/expiry needs a defined flow.
- **CSP / iframe:** Loom deliberately sets no `X-Frame-Options` / `frame-ancestors`
  (`internal/north/rest/middleware/middleware.go`), so iframing works. Under Ingress it is
  same-origin. **Open:** HA frontend CSP for a non-add-on (foreign-origin) embed.
- **Latent duplicate data source:** paramset/link/schedule editors exist in code twice
  (HA Lit + Loom Svelte for standalone). Ownership is assigned (HA wins inside HA) but the
  Loom copies keep drifting. The `isawaitable` branch is the visible seam; #1220's call-shape
  inventory is the new drift detector.
- **New Loom surfaces default to hidden-until-classified.** `/groups`, `/links`,
  `/diagrams` all appeared after the first revision of this document and had no owner.
  Every future SPA route must be assigned a row in §3 as part of its PR, otherwise embedded
  mode silently either leaks a duplicate or hides a unique capability.
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
  re-enable the route via the explicit config key — but that is an expert deviation, not
  the default.

## 7. Alternatives considered

- **A — HA panel as the single canonical surface** (rebuild all CCU-ops as HA WS commands,
  reduce Loom UI to logs/diagnostics only). Rejected: requires finishing the partial Loom
  hub-coordinator *and* re-implementing thousands of lines of working Svelte (pairing,
  firmware, backup, groups, alarm) as WS commands — high cost, no UX gain, and fleet
  doesn't fit HA at all.
- **B — Embed the whole Loom SPA, delete the HA Devices tab.** Rejected: discards a working,
  HA-authenticated, native paramset editor (with undo/redo) for a seamed iframe, imports
  Loom's *second identity system* (separate user DB, OIDC, no SSO — the actual user-facing
  harm), and strands the classic aiohomematic majority, whose Devices tab must keep existing
  anyway. A duplicated widget is cheaper to maintain than a duplicated login.
- **C — Keep both CCU surfaces (accept #74 as the end state)**, framing the panel dashboard
  as a read-only quick view and the embedded Loom UI as the full ops surface. Rejected: the
  two are not actually read-only-vs-full — the panel dashboard triggers install mode and
  firmware updates, so both would issue writes against the same CCU with different
  concurrency assumptions, and users would have no way to know which one is authoritative.
  "Two panels both claim to own the same thing" is the problem statement, not a compromise.

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
