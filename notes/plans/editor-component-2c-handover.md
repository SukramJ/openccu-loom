# #1067 step 2c — hand-over issue to `homematicip_local` (draft)

Opened 2026-11-18 at the owner's request, split in two:
SukramJ/homematicip_local#1377 (relay, module view) and
SukramJ/homematicip-local-frontend#128 (panel). Follow-up here: #1119;
#1067 closed. The text below is the combined draft both were cut from.

Pins: openccu-loom `043afeeb` (main, PR #1118 merged, unreleased, API
16.5.0), homematicip_local `fcd79f5`, homematicip-local-frontend `ebbc6bb`,
openccu-loom-client `a41be8a`.

---

**Title:** Panel: load the openccu-loom device editor as a web component on the loom backend

## Why

On the `loom` backend the configuration panel is switched off: the daemon ships its own
editor, and duplicating it in Lit would mean a second implementation that follows every
change of the daemon's API by hand. That leaves a loom entry with no device configuration
inside Home Assistant at all.

openccu-loom now ships its device editor (channel parameters, direct links, schedules,
change log) as a web component, `<loom-device-editor>`, built from the same sources as its
own UI. The design is openccu-loom ADR 0089
(`docs/adr/0089-editor-component-transport.md`): the component makes **no network call of
its own**. The page that hosts it hands it a transport, and in Home Assistant that
transport is this integration, relaying to the daemon with the entry's stored credential.
The browser never reaches the daemon; no daemon address or token reaches the browser; it
works on every deployment the integration supports (add-on, remote proxy add-on, other
host, openccu-lite box).

## Where it stands today

- `custom_components/homematicip_local/__init__.py:126-153`
  (`_any_entry_has_panel_enabled`) skips every `BACKEND_LOOM` entry (line 142).
- `homematicip-local-frontend` `packages/config-panel/src/homematic-config.ts:200-219`
  drops loom entries from the entry picker (line 213).
- `panel.py:19-22, 76, 80-101` serves the Lit panel from HA's origin under a hashed URL;
  the component is served the same way.

## What the daemon provides (openccu-loom, API 16.5.0)

**Manifest** — `GET /api/v1/ui/editor-component`, authenticated like every other route
(the entry's token works), 404 on a build without the bundle:

```json
{ "module_url": "/app/component/editor-<sha256>.js", "hash": "<sha256 hex>", "routes": ["/audit", "/auth/me", "/devices", "..."] }
```

- `module_url` is served **without a session**, `Cache-Control: immutable`; a stale hash
  answers 404, never HTML.
- `routes` are path prefixes below `/api/v1`; a prefix covers itself and every path below
  it (`/devices` covers `/devices/A:1/paramsets/MASTER`). Today: `/audit`, `/auth/me`,
  `/devices`, `/functions`, `/info`, `/rooms`, `/schedules`, `/sessions/edit`,
  `/system/ccu`, `/taxonomy`. **Read them from the manifest, do not hard-code them** —
  the component enforces the same list and the daemon publishes it from one file, so the
  two cannot differ.

**Element** — the module defines `<loom-device-editor>` on import. Properties:

| Property | Value |
|---|---|
| `transport` | required before the first render, see below |
| `address` | device address |
| `channel`, `sub` | optional: channel number, sub-page |
| `locale` | `"de"` or `"en"` — from `hass.locale.language` |
| `dark` | boolean — from `hass.themes.darkMode` |
| `skin` | `"ha"` (default) or `"loom"` |

A link inside the editor (to a direct link, a schedule, another device) is not followed;
the element dispatches `loom-navigate` (`bubbles`, `composed`) with `detail.route` = the
daemon UI's hash route (`#/devices/…`, `#/links/…`, `#/schedules/…`). The panel decides
where it leads. **One element per page** — the editor's state is module-global.

**Transport** — the object the panel hands over:

```ts
type EditorTransport = {
  request(req: {
    method: string;
    path: string;                    // below /api/v1, no query string
    query: Record<string, string>;
    headers: Record<string, string>; // must reach the daemon
    body?: unknown;                  // decoded JSON
  }): Promise<{ status: number; body?: unknown }>;
  subscribe?(onFrame: (raw: unknown) => void): () => void;
};
```

- `request` resolves for **every HTTP status** with the daemon's status and decoded body
  (a 423, 409 or 422 `problem+json` is something the editor shows); it rejects only when
  the daemon was not reached. Note: `openccu_loom_client` `HttpTransport.request`
  (`transport/http.py:420`) raises on non-2xx, so the relay converts `LoomHttpError` back
  to status + body.
- `headers` must be forwarded. A MASTER write needs the edit lock (`X-Edit-Token`, taken
  through `/sessions/edit`); without it the daemon answers 423.
- `subscribe` is optional. Without it the editor shows what it loaded and what its own
  writes return. With it, each frame is the daemon's WebSocket frame **as it arrives on
  the wire** (the component parses pings, resync signals and events itself) — the
  client's decoded `WsEnvelope` (`transport/ws.py:363`) is not that.

## Scope here

1. **Two admin-only WebSocket commands**, both taking `entry_id`, loom entries only:
   - `homematicip_local/loom/editor/request` → forwards to the entry's daemon. Refuses a
     path outside the manifest's `routes`. Adds the entry's central to the call itself
     and refuses a call that names another one.
   - `homematicip_local/loom/editor/subscribe` (optional for the first version) → relays
     raw frames.
   It is a relay for this component, not a general proxy to the daemon.
2. **A view for the module**: fetch `module_url` from the entry's daemon, serve it from
   HA's origin under a URL carrying `hash`, cached immutably. Re-read the manifest when
   the daemon reconnects (a daemon update changes the hash).
3. **Stop skipping loom entries** in `_any_entry_has_panel_enabled` and in the frontend's
   entry picker.
4. **Panel**: for a loom entry, render `<loom-device-editor>` in place of the Lit device
   views; wire `transport` to `hass.callWS` / `hass.connection.subscribeMessage`,
   `locale` and `dark` from `hass`, and map `loom-navigate` to the panel's own
   navigation. The Lit views and their per-operation commands stay for the CCU backend.
5. **Afterwards**: an inventory of the WebSocket commands only the Lit panel used on the
   loom backend.

## Open questions (decide before the relay relies on them)

- **How the relay names the central.** The entry's instance name is handed to the client
  as `central_name` (`control_unit.py:1592`, `1602`, `1658`). Whether it equals the
  daemon's central name on every installation is not shown; the scoping in point 1 needs
  that shown from the client first. The element has no `central` property — the editor
  takes its central from the daemon's answers.
- **Device paths without `central`.** `/devices/<address>/…` names a device by address.
  Whether the relay's central check covers these calls, or the daemon resolves an address
  of another central, is not yet established.
- **Who the audit log names.** Today the daemon's change log shows the entry's token as
  the actor, not the HA user. Whether the relay passes the HA user and whether the daemon
  may trust it from this credential is open (openccu-loom ADR 0089, "Still open").
- **Route prefix width.** `/devices` also covers lifecycle routes the component hides;
  narrowing to method + prefix would be a contract change on the daemon side.
- **Theme tokens.** The `--ha-*` tokens stay empty inside the shadow root until their
  `:host` forms ship in go-fabric-ui (SukramJ/go-fabric-ui#72); until then the component
  renders with its fallback colours.

## Acceptance

- On a loom entry the panel shows the device editor; it renders a device and **saves a
  MASTER paramset end to end against a running daemon with the simulated CCU
  (godevccu)** — HA, integration, daemon, simulator, in a real browser. Where that run
  lives (this repo's CI or openccu-loom's) is part of this issue.
- The relay refuses a path outside the manifest's routes and a call naming another
  central (tests).
- A CCU-backend entry is unchanged.
- Minimum daemon version: the first openccu-loom release carrying API 16.5.0; an older
  daemon (manifest 404) keeps the panel off for that entry.

## Not here

- No change to the daemon's API; a need for one goes back to openccu-loom as an issue.
- No general proxy to the daemon.
