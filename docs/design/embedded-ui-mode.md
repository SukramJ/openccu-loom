# Embedded UI Mode — Config-surface split when Loom is a Home Assistant backend

- **Status**: Proposed
- **Date**: 2026-07-12
- **Scope**: `openccu-loom` (this repo) + `homematicip_local` integration + `homematicip-local-frontend` config-panel
- **Related**:
  [ADR 0044 — single-port onboarding and HA Ingress auth passthrough](../adr/0044-single-port-onboarding-and-ha-ingress-auth.md),
  [ADR 0045 — login and onboarding into SPA](../adr/0045-login-and-onboarding-into-spa.md),
  [ADR 0051 — northbound authorization model](../adr/0051-northbound-authorization-model.md)

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
>    inbox, backups, visibility, diagnostics, logs, audit, and daemon settings.
> 3. **Hide, at the server, everything that duplicates HA** — Loom's own login/OIDC,
>    setup wizard, user/token admin, CCU-connection admin, room/function admin,
>    device-control tiles, Matter, and the paramset/link/schedule editors that the HA
>    panel owns.
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
| **CCU admin** (pairing / firmware / signal / inbox) | panel CCU tab (only `backend === "CCU"`) | Loom `/inbox`, `/firmware`, `/signal`, `/backups` | Two surfaces for one device class |
| **Device control / rooms** | HA dashboard / areas | `Overview`, `Favorites`, `RoomsFunctionsAdmin` | Loom side fully redundant inside HA |
| **Paramset / link / schedule editors** | panel Devices tab (native, undo/redo) | `DeviceDetail → Configure`, `links/*`, `schedule/*` | Same editor built twice |

No reduced/embedded UI mode exists today. `north.ui` only gates the two no-JS
diagnostic pages (`/health`, `/about`); `Sidebar.svelte` builds its full nav
unconditionally except for the existing `admin`-role and `matterEnabled` gates
(`assets/ui/src/lib/components/ui/Sidebar.svelte:120-278`).

## 2. Decision — concern-split hybrid + a server-side `embedded` mode

Assign every capability class to exactly one authoritative surface, chosen by *where the
data lives and which editor is already better*:

- **Per-device *editing*** (paramsets MASTER/VALUES with edit sessions + undo/redo,
  direct links, climate/device schedules) → **HA config-panel**. It already works over
  both backends via the `isawaitable` branch in `websocket_api.py` and offers native
  session/undo that the SPA cannot match at that granularity. **No change required here.**
- **Daemon & CCU *operations*** (firmware, signal/RSSI, inbox/install-mode, visibility,
  backups, diagnostics/RPC-recorder, logs, audit, and daemon settings: MQTT/Matter/MCP/
  reliability/persistence/system) → **embedded Loom SPA**. Only Loom holds this state
  (throttle, circuit-breaker, recorder); HA has no equivalent.
- **Identity, CCU-connection, first-run, user/token admin, rooms, device tiles, Matter**
  → **owned by HA (or Loom-standalone only)**; **hidden in embedded mode** so there is
  exactly one place for each.

This is realised in this repo as a first-class **`embedded` UI mode**: a server-side
posture (not merely a client nav-hide) that strips redundant nav clusters *and blocks the
corresponding routes at the router*, so hidden surfaces are unreachable even by deep link.

## 3. Ownership matrix (all three topologies)

| Capability class | Loom standalone | **Loom as HA backend** | Classic (no Loom) |
|---|---|---|---|
| Paramsets (MASTER/VALUES, session/undo, export/import/copy) | Loom SPA | **HA panel (Devices)** | HA panel (Devices) |
| Direct links / peering | Loom SPA | **HA panel** | HA panel |
| Schedules (climate / device) | Loom SPA | **HA panel** | HA panel |
| Firmware / signal / inbox / visibility | Loom SPA | **embedded Loom UI** | HA panel (CCU tab) |
| Backups (CCU + daemon) | Loom SPA | **embedded Loom UI** | HA panel (CCU tab) |
| Install-mode / pairing | Loom SPA | **embedded Loom UI** | HA panel (CCU tab) |
| System info / service messages | Loom SPA | **embedded Loom UI** | HA panel (CCU tab) |
| Logs / diagnostics / audit / RPC-recorder / RSSI | Loom SPA | **embedded Loom UI** | — (does not exist) |
| MQTT / Matter / MCP / REST / mDNS / north-bound | Loom SPA | **embedded Loom UI** (settings) | — |
| Daemon lifecycle (restart / update / reliability / persistence) | Loom SPA | **embedded Loom UI** | — |
| CCU connection / credentials | Loom SPA (`CentralsAdmin`) | **HA `config_flow` only** (Loom side hidden) | HA `config_flow` |
| Login / users / tokens / OIDC | Loom SPA (`AccessControl`) | **HA auth only** (Loom login hidden; add-on: Ingress passthrough) | HA auth |
| Setup wizard / first-run | Loom SPA (`Setup`) | **hidden** (HA `config_flow` is the entry point) | — |
| HA runtime: health / throttle / incidents / cache | — | **HA panel (Integration tab)** | HA panel (Integration tab) |
| HA entities / areas / permissions / dashboards | — | **HA core** | HA core |
| Device control tiles / overview / favorites | Loom SPA | **HA dashboard** (Loom tiles hidden) | HA dashboard |
| Rooms / functions | Loom SPA (`RoomsFunctionsAdmin`) | **HA areas** (Loom side hidden) | HA areas |
| Fleet (multi-CCU) | Loom SPA | **embedded Loom UI** (no HA single-entry equivalent) | — |

## 4. `embedded` mode specification (this repo)

### 4.1 Trigger resolution — auto-derive, with explicit override

The mode resolves from an **explicit config key that falls back to the Ingress signal**:

```
embedded := north.ui.embedded            // explicit override, tri-state *bool
            ?? (ha_ingress active         // HAIngressConfig resolved ON (ADR 0044)
                && request carries X-Ingress-Path)
```

- **Add-on deployment** → zero-config: `ha_ingress` resolves ON via the supervised stamp
  (`HAIngressConfig.Enabled` nil-default, `internal/config/config.go`), Supervisor sends
  `X-Ingress-Path` (read at `internal/north/rest/router.go:988-993`) → embedded ON
  automatically.
- **Manual daemon behind the HA integration** → the operator sets `north.ui.embedded: true`
  explicitly (there is no Ingress signal to derive from). This is the case that answers
  "embedded for *any* backend, not only the add-on."
- **Standalone Loom** → both signals absent → embedded OFF → full SPA, unchanged.

Add a pointer-bool `Embedded *bool` to the `north.ui` config section (`NorthUI`,
`internal/config/config.go:392`), documented in `example.config.full.yaml` next to
`enabled`. Because the effective value can depend on a per-request header, resolve a
**base** value from config at composition and let the request-scoped middleware promote it
when `X-Ingress-Path` is present (mirrors how the Ingress prefix is already threaded).

### 4.2 Nav + route taxonomy

**Hidden in embedded mode** (owned by HA, or a second identity/duplicate):

- Pre-auth: `Setup.svelte`, `Login.svelte` / OIDC
- `/access` (`AccessControl`), `settings → ccus` (`CentralsAdmin`),
  `settings → oidc` / `ccu_auth` / `users` / `groups` / `tokens`
- `/overview`, `/favorites`, `RoomsFunctionsAdmin`, `/matter` (+ `settings → matter` if HA
  owns Matter — see §6), theme/locale chrome, `/about` branding, mobile-nav chrome
- **`DeviceDetail → Configure`** (paramset/link/schedule editors) — the HA panel owns these

**Visible in embedded mode** (no HA equivalent):

- `/backups`, `/firmware`, `/signal`, `/inbox`, `/visibility`, `/diagnostics`, `/logs`,
  `/audit`, `/fleet`
- `settings → mqtt` / `mcp` / `reliability` / `persistence` / `system`
- `DeviceDetail → History` (recorded measurements — HA has no equivalent)

The `DeviceDetail → Overview`/`History` tabs stay; only the `Configure` tab is gated off,
so the same device is browsable in both panels but only *editable* in one.

### 4.3 Enforcement — server-side, not just nav-hide

1. **Nav**: extend the existing cluster gating in
   `assets/ui/src/lib/components/ui/Sidebar.svelte:120-278` (already gates on
   `admin` / `matterEnabled`) with an `embedded` gate that drops the redundant clusters and
   `Settings` sub-tabs. Expose `embedded` to the SPA the same way `matterEnabled` /
   `identity.role` are exposed (a store fed from a `/api/v1/*` capability/bootstrap payload).
2. **Routes**: block the hidden route families **at the router** so `#/access`,
   `#/settings/ccus`, etc. cannot be reached by deep link. Add the guard where routes mount
   / where the SPA index is served (`internal/north/rest/router.go`), returning 404 or a
   redirect to an allowed route. Nav-hide alone is insufficient — the SPA is hash-routed and
   every route is otherwise reachable directly (`assets/ui/src/App.svelte:156-212`).
3. **API (defence in depth)**: the hidden admin routes have REST counterparts (users,
   tokens, ccus, oidc). Under embedded mode these should reject writes for the passthrough
   identity per the northbound authorization model (ADR 0051), so a hidden UI cannot be
   bypassed via the API.

### 4.4 Auth — SSO guaranteed only in the add-on

- **Add-on**: Ingress passthrough (ADR 0044) means HA's admin gate *is* the auth boundary;
  no second login. Loom's `Login`/`Setup` are hidden because they are genuinely unneeded.
- **Manual daemon (embedded via explicit key)**: there is **no SSO**. The iframe will hit
  Loom's own auth. Mitigation, in order of preference: (a) document that supervised add-on
  is the supported single-login path; (b) allow a long-lived token / the integration's
  `CONF_LOOM_TOKEN` to seed an SPA session so the human login is skipped; (c) accept the
  second login. **This trade-off must be stated in user docs, not hidden.**

## 5. Cross-repo implementation plan

**Prerequisite (integration, `homematicip_local`):** Loom backend is still gated off
(`LOOM_BACKEND_SELECTABLE = False`, `const.py`). The Loom `CentralUnit` duck-type is
partial — orphan-cleanup is skipped (`control_unit.py:506-512`). Nothing here ships before
that adapter is complete; it is the real cost centre and blocks productive Loom generally.

1. **This repo — build `embedded` mode** (largest new work):
   - `internal/config`: add `north.ui.embedded` (`*bool`), document in `example.config.full.yaml`.
   - Resolve effective `embedded` (config ?? ingress-signal) and surface it in the SPA
     bootstrap/capability payload.
   - `assets/ui/.../Sidebar.svelte` + `Settings.svelte`: `embedded` gate on clusters/tabs.
   - `internal/north/rest/router.go`: block hidden route families (404/redirect).
   - ADR 0051 alignment: passthrough identity is read-only on hidden admin APIs.
2. **Integration — second panel registration** (`homematicip_local/panel.py`): for
   Loom-backed entries, register a sidebar panel that iframes the embedded Loom Ingress URL.
   Fix the stale comment/behaviour mismatch in `__init__.py:110-116` (claims "CCU only" but
   performs no backend check) and branch on `CONF_BACKEND`.
3. **Integration — leave `websocket_api.py` untouched.** Devices/Integration tabs already
   run on Loom via the `isawaitable` branch (`websocket_api.py:263-281`); this is the
   documented dual-backend contract.
4. **Integration — document CCU-credential ownership.** `config_flow` is the single source
   for CCU connection when on Loom; `CentralsAdmin` is hidden in embedded mode. No code
   change, but state the ownership.
5. **Frontend — keep the CCU tab gated on `backend === "CCU"`** (`homematic-config.ts:262-267`),
   deliberately. Its function is delivered by the embedded Loom ops UI, not by un-gating the tab.

## 6. Consequences, risks, open questions

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
  Loom copies keep drifting — the compat-shim drift risk noted in the loom-client
  `architecture-review.md`. The `isawaitable` branch is the visible seam.
- **Versioning:** embedded mode couples the HA release cadence to the Loom SPA version. A
  Loom UI regression would break the HA config experience. Needs a compatibility/minimum-
  version gate across `homematicip_local`, `openccu-loom-client`, and the daemon — the same
  discipline already applied to the aiohomematic pin.
- **Fleet does not fit HA's single-entry model.** Loom's multi-CCU `/fleet` view stays in
  the embedded ops UI; the conceptual tension (HA thinks per config-entry/CCU, Loom thinks
  fleet) remains unresolved.
- **Matter ownership** is a policy call: if HA owns Matter natively, hide Loom's Matter in
  embedded mode to avoid double-exposing devices; if Loom's Matter bridge is intended to run
  even behind HA, keep it visible. Recommend **hidden by default** in embedded mode.

## 7. Alternatives considered

- **A — HA panel as the single canonical surface** (rebuild all CCU-ops as HA WS commands,
  reduce Loom UI to logs/diagnostics only). Rejected: requires finishing the partial Loom
  hub-coordinator *and* re-implementing thousands of lines of working Svelte (pairing,
  firmware, backup) as WS commands — high cost, no UX gain, and fleet doesn't fit HA at all.
- **B — Embed the whole Loom SPA, delete the HA Devices tab.** Rejected: discards a working,
  HA-authenticated, native paramset editor (with undo/redo) for a seamed iframe, imports
  Loom's *second identity system* (separate user DB, OIDC, no SSO — the actual user-facing
  harm), and strands the classic aiohomematic majority, whose Devices tab must keep existing
  anyway. A duplicated widget is cheaper to maintain than a duplicated login.

The chosen concern-split keeps the one surface each capability is already best at, and is
the only option where most of the solution already exists while the real footguns
(double login, double CCU credentials, double Matter bridge) disappear.
