# CCU-WebUI Groups Wave (Welle 2) — Status & Handoff

Last updated: 2026-07-22

Status of the **Gruppenverwaltung** wave (heating groups, GR01–GR05)
from [`ccu-webui-gap-analysis.md`](./ccu-webui-gap-analysis.md) §4.3 / §7.
Slice 1 (the A3 decision + GR01) has shipped; **GR02–GR05 are deferred**
by decision on 2026-07-22. This document preserves the CCU wire knowledge
gathered by observing the WebUI's own HTTP traffic and reading the shipped
OCCU templates, plus the GR02 plan, so the wave can resume without re-doing
the reconnaissance.

Related:
[ADR 0055 — heating groups via CCU jpages proxy](./adr/0055-groups-jpages-proxy.md),
[gap analysis](./ccu-webui-gap-analysis.md) (A3, GR01–GR05, §7 wave plan),
[wave runbook & continuation guide](./ccu-webui-wave-runbook.md) (how to
execute a wave, CI traps, resuming on another device).

---

## 1. What shipped — Slice 1 (PR #388, merged)

On `main` at build **0.46.0**, REST/WS API **2.42.0**.

### A3 — architecture decision (ADR 0055)

Group **mutations** will be exposed by **proxying the CCU's own
`/pages/jpages/group/*` HMServer endpoints** with Loom's live JSON-RPC
session, not by re-implementing the direct-link matrix in Go (which
would drift against eQ-3's firmware).

The load-bearing question — *does `/pages/jpages` accept Loom's JSON-RPC
session, or does it need a separate WebUI login?* — was answered by watching
the WebUI's own requests and is **resolved**:

- HMServer validates the incoming `sid` against ReGa's JSON-RPC endpoint
  `/api/homematic.cgi` (observable from the outbound request it makes on each
  jpages call). So the jpages `sid` **is** the ReGa/JSON-RPC session token
  that `Session.login` returns.
- The WebUI passes exactly that token as `?sid=<SessionId>` on every
  jpages call (visible in the shipped, readable templates, e.g.
  `occu/WebUI/www/config/easymodes/js/Group.js` and the jpages `.ftl`
  page templates).
- Loom already holds it: `jsonrpc.Client.SessionID()`, renewed on its own
  cadence.
- Precedent for the raw-HTTP-with-session mechanics already exists:
  `CcuBackend.SetDownloadFirmwareTransport(baseURL, hc, sessionIDFn)`
  posts to `/config/cp_maintenance.cgi` / `cp_security.cgi` with
  `sid := sessionIDFn()`. The jpages proxy is the same pattern pointed at
  a different path — **no separate WebUI login is required**.

lighttpd proxies `^/pages/jpages` to HMServer on `127.0.0.1:9292`
(`WebUI/etc/lighttpd/conf.d/proxy.conf`).

### GR01 — read-only heating-group listing (implemented)

- `GET /api/v1/groups` (+ `?central=`) and WS `groups.list`, both backed
  by one `groupsAdapter`. Empty `central` aggregates over all centrals,
  best-effort per central (a non-CCU or offline central contributes an
  empty roster instead of failing the request).
- `CcuBackend.GetHeatingGroupList` → JSON-RPC `CCU.getHeatingGroupList`;
  parsing in `internal/model/group`.
- SPA "Heizungsgruppen" view (read-only, de+en, theme-aware, nav entry in
  the automation cluster), with light+dark Playwright visual baselines.

#### `groups.gson` schema (pinned from live `getHeatingGroupList` responses)

`CCU.getHeatingGroupList` reads `/etc/config/groups.gson`
(`occu/WebUI/www/api/methods/ccu/getheatinggrouplist.tcl`) and returns
its **contents as a JSON string** (via `json_toString`), so the payload
needs a *second* unmarshal. A missing file yields the sentinel `"-1"`.

The schema below was reconstructed from the JSON payloads
`getHeatingGroupList` actually returns on the CCU (Gson serialisation with
default field names; the group model is plain data classes, not enums):

```json
{
  "groups": [
    {
      "id": 4711,
      "groupType":  { "id": "…", "label": "…", "version": 1 },
      "groupProperties": {
        "NAME": "Wohnzimmer",
        "GROUP_DEVICE_NAME": "…",
        "FORBID_SINGLE_OPERATION": "false"
      },
      "groupDefinition": {
        "groupType": { "id": "…", "label": "…", "version": 1 },
        "allowedGroupMemberTypeDefinitions": [
          { "groupMemberType": { "id": "…" }, "maxCount": 8 }
        ]
      },
      "groupMembers": [
        { "id": "000ABC0123456789:1", "memberType": { "id": "…" } }
      ]
    }
  ]
}
```

The top-level payload is a single `{ "groups": [ … ] }` object.
Property-map keys are the literal strings `NAME`, `GROUP_DEVICE_NAME`,
`FORBID_SINGLE_OPERATION`.

---

## 2. Deferred — GR02 configurator (reconnaissance complete)

GR02 is the **XL** slice: create / edit / delete groups + member
selection via the jpages proxy. The wire shapes below were reconstructed
from the shipped, readable jpages page template `GroupEditPage.ftl` and the
request/response bodies observed on the live `/pages/jpages/group/*` calls.

### jpages endpoints

All are `POST … ?sid=<JSON-RPC session>` with a `JSON.stringify` body and
a `JsonResponse` reply (`{ "valid": true|false }`;
`getValidResponse` / `getInvalidResponse` / `getSessionInvalidResponse`).

| Endpoint | Request body | Purpose |
|---|---|---|
| `/pages/jpages/group/save` | `{ groupName, groupTypeId, forbidSingleOperation, assignedDevicesIds: [memberId…], isNewGroup, groupDeviceName, groupId? }` | create (`isNewGroup:true`) **and** edit (`groupId` set) |
| `/pages/jpages/group/delete` | `{ groupId }` | delete |
| `/pages/jpages/group/suitableGroupMembers` | `{ groupTypeId }` | assignable + leftover members for a type |
| `/pages/jpages/group/getAllAssignableGroupTypes` | — | group-type list for the create form |
| `/pages/jpages/group/list` | filter object | HMServer's *structured* group list (richer than `getHeatingGroupList`; not needed for GR01) |

Field notes from `GroupEditPage.ftl` (the `save()` view-model):

- `groupName = escape(viewModel.groupName())` — JS `escape()`, i.e.
  **Latin-1 percent-encoding**; the server side decodes **ISO-8859-1**.
  Loom must mirror this, not send raw UTF-8, or umlauts corrupt
  (e.g. "Wohnzimmer Süd").
- `groupDeviceName = <group_name> + " " + virtualDeviceSerialNumber`.
- `assignedDevicesIds` = array of member `id`s (device/channel addresses).
- After a successful save the WebUI additionally issues JSON-RPC
  `Device.setName` + `Channel.setName` to apply the `Gruppenname:Kanalnr`
  naming scheme (**this is GR03's scope**), and the CCU runs a
  `CONFIG_PENDING` settle on the virtual device + members.

The `suitableGroupMembers` and `getAllAssignableGroupTypes` replies are plain
JSON objects (assignable/leftover member lists, group-type entries); their
field names are taken straight from the observed responses.

### GR02 implementation plan

1. **Backend (no new transport wiring).** Add
   `SaveGroup / DeleteGroup / SuitableGroupMembers / AssignableGroupTypes`
   to `CcuBackend`, POSTing to the jpages paths using the **existing**
   `b.baseURL` + `b.sessionIDFn()` already set for the firmware download
   path — the ADR 0055 session finding is what makes this free.
2. **Adapter.** Extend `internal/central/adapter` `GroupsDomain` with the
   write methods (resolve primary backend → jpages call).
3. **REST.** `POST /api/v1/groups`, `PUT /api/v1/groups/{id}`,
   `DELETE /api/v1/groups/{id}`, `GET /api/v1/groups/suitable-members`,
   `GET /api/v1/groups/types` — **admin-gated**, audit-logged.
4. **WS.** `groups.create / groups.save / groups.delete /
   groups.suitable_members / groups.types`.
5. **SPA.** Create/edit form (type picker → member picker → name +
   "operate only via group" toggle) from the existing groups view; delete
   via the shared confirm dialog; full de+en + theme + light/dark
   baselines.
6. **Tests.** Mocked-`httptest` backend tests (jpages request bodies +
   `{valid}` / `{valid:false}` / session-invalid), adapter/handler/WS
   tests, SPA vitest — all hermetic.

### Open decisions (to settle when GR02 resumes)

1. **String encoding** — mirror `escape()` + ISO-8859-1 for
   `groupName` / `groupDeviceName` (recommended; raw UTF-8 corrupts
   umlauts).
2. **Sync vs. async** — ship GR02 **synchronous** (save/delete return
   `{valid}` + a `CONFIG_PENDING` *hint*, mirroring Welle 1's
   central-links pattern) and add the async progress-broadcast job as a
   small follow-up; **or** build the full async job up front. Recommended:
   synchronous first.
3. **Live validation is blocked on approval.** godevccu has **no** jpages
   surface, so the write path can only be validated against a real CCU.
   Per the live-CCU-write rule this needs explicit user approval **and** a
   named throwaway test group on `172.18.4.29` (create → add member →
   delete). GR02 ships with mocked-HTTP tests; the live round-trip is a
   separate, approved step.

---

## 3. Remaining wave items (deferred)

Dependency order (gap analysis §7, Welle 2): **A3 ✓ → GR01 ✓ → GR02 →
GR03, GR04, GR05.**

- **GR03 — rename incl. channel naming scheme** (P2, S). Via the
  `group/save` path + JSON-RPC `Device.setName` / `Channel.setName`
  (`Gruppenname:Kanalnr`), then a device reload. Depends on GR02.
- **GR04 — "operate only via group"** (`Device.setOperateGroupOnly`, P3,
  S). Small JSON-RPC pass, a per-member switch in the group detail view.
  Depends on GR02.
- **GR05 — group assignment on pairing** (P3, M). Offer a target group in
  the inbox accept-flow for group-capable device types. Depends on GR02.

After Welle 2: gap analysis §7 Wellen 3–6 (only the `umsetzen`-marked
items). The Programmeditor items PR01–PR06 remain **explicitly out of
scope** by prior decision.

---

## 4. Where the reference material lives

- CCU firmware source: `../occu/` and `../OpenCCU/` (local checkouts).
  - jpages templates: `../occu/HMserver/opt/HMServer/pages/GroupListPage.ftl`,
    `GroupEditPage.ftl`.
  - `getHeatingGroupList` Tcl:
    `../occu/WebUI/www/api/methods/ccu/getheatinggrouplist.tcl`.
  - lighttpd proxy rule: `../occu/**/lighttpd/conf.d/proxy.conf`.
  - the group wire shapes are otherwise taken from the observed
    `/pages/jpages/group/*` request/response bodies on a live CCU.
- aiohomematic does **not** model heating groups (it treats the virtual
  group device as an ordinary device) — no reference there.
- godevccu (`../godevccu/`) does **not** implement `CCU.getHeatingGroupList`
  or any jpages surface, hence the `groups.list` e2e skip and the
  mocked-HTTP test strategy for GR02.
