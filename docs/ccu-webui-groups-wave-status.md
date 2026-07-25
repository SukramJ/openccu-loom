# CCU-WebUI Groups Wave (Welle 2) — Status & Handoff

Last updated: 2026-07-24

Status of the **Gruppenverwaltung** wave (heating groups, GR01–GR05)
from [`ccu-webui-gap-analysis.md`](./ccu-webui-gap-analysis.md) §4.3 / §7.
**The whole wave has shipped (0.48.0): GR01 (read-only listing), GR02
(create/edit/delete via the jpages proxy), GR03 (clean virtual-device label),
GR04 (per-member "operate only via group"), GR05 (inbox group assignment).**
This document preserves the CCU wire knowledge gathered by observing the
WebUI's own HTTP traffic, reading the shipped OCCU templates, and probing the
live CCU, so the behaviour can be re-derived without re-doing the reconnaissance.

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

The schema below is confirmed against the JSON payloads a live CCU
(`172.18.4.39`) returns for `getHeatingGroupList` (Gson serialisation with
default field names; the group model is plain data classes, not enums).
**`groupType.version` is an integer and `FORBID_SINGLE_OPERATION` is a JSON
boolean, not a string** — the property map therefore holds mixed value types,
so a parser must not type it as `map[string]string` (that regressed GR01: the
whole list failed to unmarshal and the view showed no groups — fixed in 0.47.2
by decoding each property lazily). GR02 (which sends `forbidSingleOperation`
in the save body) must mirror this typing:

```json
{
  "groups": [
    {
      "id": 4711,
      "groupType":  { "id": "…", "label": "…", "version": 1 },
      "groupProperties": {
        "NAME": "Wohnzimmer",
        "GROUP_DEVICE_NAME": "…",
        "FORBID_SINGLE_OPERATION": false
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

### Live re-verification (2026-07-24)

Re-checked before committing to the GR02 build, against the current
firmware on `172.18.4.39` (reads only, no mutation) and the present code:

- **Transport is already wired per central.** `CcuBackend` carries
  `baseURL` + `sessionIDFn` (`internal/client/backends/ccu.go`), populated
  at start-up by `ccuBackend.SetDownloadFirmwareTransport(...)` in
  `internal/central/adapter/ccu_wiring.go`. GR02 needs **no new transport
  wiring** — it POSTs to the jpages paths on the same base URL + session.
- **The save/delete request contract matches the firmware template
  verbatim** (`GroupEditPage.ftl` lines ~529–542: `groupName=escape(...)`,
  `groupTypeId`, `forbidSingleOperation`, `assignedDevicesIds`,
  `isNewGroup`, `groupDeviceName`, `groupId` for edit; `GroupListPage.ftl`
  ~153–155: `delete` with `{ groupId }`).
- **HMServer (port 9292) is up and every CRUD endpoint exists and is
  session-gated.** A `POST …?sid=<dummy>` to `group/{list,save,create,
  delete,suitableGroupMembers,configureDevices,assignedGroupMembers}` all
  return HTTP 200 with a **session-invalid** JSON body — confirming ADR
  0055's load-bearing assumption that a valid JSON-RPC `sid` is all that is
  required. (`getAllAssignableGroupTypes` returned **404**; see the
  corrections below.)

**Two corrections to the reconstructed shapes** (the live firmware differs
from the earlier reconstruction):

1. **Response shape.** The reply is
   `{ "isSuccessful": true|false, "errorCode": "…", "content": "…" }` — not
   `{ "valid": … }`. GR02 must key on `isSuccessful`. An invalid/expired
   session returns `errorCode:"42"` with an HTML login-redirect in
   `content`; the backend should map that to `ErrAuthFailure` and trigger a
   JSON-RPC re-login + retry rather than surfacing it as a group error.
2. **Group-type list source is unconfirmed.**
   `group/getAllAssignableGroupTypes` 404s on this firmware, so the
   create-form type list comes from somewhere else (likely a field of the
   `suitableGroupMembers` / `list` response, or a ReGa/JSON-RPC call). This
   is the one shape that still needs a **valid-session** live read before
   the GR02 form is fixed (login → `list` / `suitableGroupMembers` with a
   real `sid`).

### Valid-session reads (2026-07-24, against 172.18.4.29)

Confirmed with a real JSON-RPC session on a CCU that has live HmIP heating
groups (roster: 2 groups, `groupType.id = "hmip.heating.group"`):

- **Data endpoints return the bare data object on success — no wrapper.**
  `suitableGroupMembers` with `{ "groupTypeId": "hmip.heating.group" }`
  returns HTTP 200 and
  `{ "assignableGroupMembers": [ { "id", "serialNumber", "type" } … ],
  "leftoverGroupMembers": [ … ] }`. `id` is the channel address (e.g.
  `00109709B1381B:1`), `type` is the member kind (`SENSOR_WINDOW`,
  `SWITCH_ACTUATOR`, …). So the backend must branch on the body: an object
  carrying `isSuccessful:false` is the **session/error** wrapper (re-login);
  otherwise it is the plain data payload — parse it directly.
- **`group/list` renders the HTML page** (`GroupListPage.ftl`), not a JSON
  API — keep sourcing the roster from `CCU.getHeatingGroupList`.
- **`getAllAssignableGroupTypes` 404s even with a valid session** — it is not
  on this firmware. The one type id GR02 needs for HmIP heating
  (`hmip.heating.group`) is known from the roster; a full assignable-type
  enumeration source is still open but low-priority (HmIP heating is the
  primary case; BidCos heating-group type ids, if needed, come from a
  roster/`suitableGroupMembers` sample).
### `save` contract — authoritative from `GroupEditPage.ftl` `_SaveGroup()`

Two approved throwaway writes on `172.18.4.29` (empty create, and a create
with one confirmed window-sensor member) **both hung for the full client
timeout** (120 s / 90 s) with **no** group appearing (roster stayed clean).
Reading the shipped `_SaveGroup()` (lines ~524–615) explains why and pins the
real contract — this supersedes the earlier "save is long-running / empty is
rejected" guess:

1. **Transport / content-type (the likely hang cause).** The WebUI sends the
   save via Prototype `new Ajax.Request(url, {postBody: JSON.stringify(data),
   onComplete})`. Prototype's default `Content-Type` is
   **`application/x-www-form-urlencoded`** (charset UTF-8) with the JSON
   string as the *raw* body. Our probes sent `application/json`; a save
   handler reading a form-encoded body can block on that mismatch. **GR02
   must POST the JSON body with `Content-Type:
   application/x-www-form-urlencoded`, not `application/json`.** (Reads like
   `suitableGroupMembers` tolerated `application/json`; `save` apparently
   does not — treat the form content-type as required for all jpages POSTs.)
2. **Pre-save metadata preamble.** Before the POST, the WebUI issues, per
   device, JSON-RPC `Interface.setMetadata({objectId: device.id, dataId:
   "inHeatingGroup", value: "true"|"false"})` — `true` for assigned members,
   `false` for every other assignable device. GR02's save flow must replicate
   this preamble.
3. **Success response shape (confirmed from the code path).** `onComplete`
   parses `response = JSON.parse(t.responseText)` and branches on
   `response.isSuccessful`. On success of a **new** group it derives the
   virtual device serial via `createVirtualDeviceSerialNumber(response.content)`
   — so success is `{ isSuccessful: true, content: <virtual-device info> }`.
   On failure, `{ isSuccessful: false, errorCode, content }`;
   `errorCode == sessionTimeoutErrorCode` ("42") means the session expired.
4. **`save` returns before the settle finishes.** After a successful reply the
   WebUI collects `devicesInConfigPending` and tracks the `CONFIG_PENDING`
   settle **separately** — the HTTP response is not gated on the settle. So
   GR02 does not need to hold the request open for the settle; it POSTs
   (with a sane timeout), then, like the WebUI, watches the members'
   `CONFIG_PENDING` / re-reads `getHeatingGroupList` for completion + the new
   `groupId`.
5. **Post-save follow-up (what the WebUI does).** A new-group success then runs
   JSON-RPC `Device.setName` / `Channel.setName` (`Gruppenname:Kanalnr`),
   `iseDevices.setReadyConfig(regaId)`, and `system.saveObjectModel`. Loom does
   **not** replicate this — see §3 GR03: the naming is instead handled by the
   bare `groupDeviceName` sent in the save, and the synchronous JSON-RPC rename
   is impossible on loom's side anyway (virtual device unresolvable + settle lag).

### New-group flow + live write CONFIRMED (2026-07-24, 172.18.4.29)

The real reason the earlier saves hung: **creating a group is a two-step
jpages flow**, and the first step was missing.

1. **`GET /pages/jpages/group/create?sid=…`** allocates the draft group and
   returns `{ isSuccessful:true, content:<GroupEditPage HTML> }`. The HTML
   carries the placeholder id (0 for a fresh draft); the real id is assigned
   on save. (`GroupListPage.ftl` `NewGroup()`.) The virtual-device serial is
   `"INT" + zeroPad(id, 7)` (`viewmodels.js` `createVirtualDeviceSerialNumber`
   / `virtualDevicePrefix`).
2. **`POST /pages/jpages/group/save?sid=…`** then commits it. A `save` with
   `isNewGroup:true` **without** the preceding `GET group/create` hangs and
   creates nothing — that was every earlier failure.

**Confirmed end-to-end** with a throwaway `zz_loom_verify` group + one
window-sensor member (`00109709B1381B:1`): GET create → `isSuccessful:true`;
POST save → **the group was created server-side (appeared in the roster as
id 2)** even though the save's HTTP response **did not return within 90 s**;
DELETE → **`{ "isSuccessful": true, "errorCode": "", "content": "[]" }`** (HTTP
200, prompt); roster clean afterwards. So a sensor-only HmIP heating group is
accepted, and create + delete both work.

**The load-bearing implementation fact:** `save` commits server-side but its
**HTTP response is slow / may never return within a sane timeout** — the
create succeeded while our client timed out. GR02 therefore **must fire the
save and treat the new group appearing in `getHeatingGroupList` (with its
server-assigned id) as the completion signal**, NOT the save HTTP response.
This is exactly the async design in ADR 0055 §3, now empirically required, not
just preferred. `delete` (and the reads) return promptly, so only the
`save`/create leg needs the fire-and-poll treatment. The new group's id for a
later edit/delete comes from the roster, not from the save reply.

### jpages endpoints

All are `POST … ?sid=<JSON-RPC session>` with a `JSON.stringify` body.
On an **invalid** session the reply is the wrapper
`{ "isSuccessful": false, "errorCode": "42", "content": <login html> }`
(live-verified 2026-07-24; the earlier `{ "valid": … }` reconstruction is
superseded). On a **valid** session, data endpoints return their bare data
object (see above). `delete` returns `{ isSuccessful:true, errorCode:"",
content:"[]" }`; `save` commits but its response is slow / may time out — see
the new-group flow above.

| Endpoint | Method + body | Purpose |
|---|---|---|
| `/pages/jpages/group/create` | **GET** (no body) | **precursor for a new group** — allocates the draft, returns the edit-page HTML with the id; a `save` create hangs without it |
| `/pages/jpages/group/save` | POST `{ groupName, groupTypeId, forbidSingleOperation, assignedDevicesIds: [memberId…], isNewGroup, groupDeviceName, groupId? }` | commit — create (`isNewGroup:true`, after `group/create`) **and** edit (`groupId` set). Response is slow: fire-and-poll `getHeatingGroupList` |
| `/pages/jpages/group/delete` | POST `{ groupId }` | delete — returns `{ isSuccessful:true, content:"[]" }` promptly |
| `/pages/jpages/group/suitableGroupMembers` | `{ groupTypeId }` | assignable + leftover members for a type |
| `/pages/jpages/group/getAllAssignableGroupTypes` | — | group-type list for the create form — **404 on the 2026-07-24 live check**; the type list source is unconfirmed (see corrections above) |
| `/pages/jpages/group/list` | filter object | HMServer's *structured* group list (richer than `getHeatingGroupList`; not needed for GR01) |

Field notes from `GroupEditPage.ftl` (the `save()` view-model):

- `groupName = escape(viewModel.groupName())` — JS `escape()`, i.e.
  **Latin-1 percent-encoding**; the server side decodes **ISO-8859-1**.
  Loom must mirror this, not send raw UTF-8, or umlauts corrupt
  (e.g. "Wohnzimmer Süd").
- `groupDeviceName` = the virtual-device label. The WebUI sends
  `<group_name> + " " + virtualDeviceSerialNumber`, but that serial is built
  from the always-zero draft id, so loom sends the **bare group name** instead
  (see §3 GR03 for the live rationale and the resulting `<name>:<n>` channels).
- `assignedDevicesIds` = member `id`s (device/channel addresses) as a
  **JSON-encoded STRING**, NOT a native JSON array. HMServer's save handler
  re-parses this field; a native array is silently dropped and the group
  commits with **zero members**. Captured from the WebUI's `GroupEditPage`
  save() (it sends the stringified form) and live-confirmed both ways: native
  array → 0 members, `"[\"id1\",\"id2\"]"` string → members assigned. The
  Go-`json.Marshal` form (no spaces) works.
- After a successful save the WebUI additionally issues JSON-RPC
  `Device.setName` + `Channel.setName` to apply the `Gruppenname:Kanalnr`
  naming scheme, and the CCU runs a `CONFIG_PENDING` settle on the virtual
  device + members. Loom does not replicate this JSON-RPC pass (see §3 GR03).

The `suitableGroupMembers` and `getAllAssignableGroupTypes` replies are plain
JSON objects (assignable/leftover member lists, group-type entries); their
field names are taken straight from the observed responses.

### GR02 implementation plan

1. **Backend (no new transport wiring).** Add
   `CreateGroupDraft / SaveGroup / DeleteGroup / SuitableGroupMembers /
   GroupTypes` to `CcuBackend`, calling the jpages paths using the
   **existing** `b.baseURL` + `b.sessionIDFn()` already set for the firmware
   download path — the ADR 0055 session finding (re-verified 2026-07-24) is
   what makes this free. Critical details, all live-confirmed 2026-07-24:
   - **New group = two steps.** `GET group/create` first (allocates the
     draft, returns the edit HTML), **then** `POST group/save`. A `save`
     create without the `GET group/create` precursor hangs and creates
     nothing.
   - **`save` is fire-and-poll.** Its HTTP response is slow / may time out
     even though the group *is* committed server-side. Do **not** treat the
     save response as the completion signal — fire it (generous timeout) and
     poll `getHeatingGroupList` until the group appears with its
     server-assigned id. `delete` and the reads return promptly.
   - Match the WebUI on the wire: `POST` bodies use `Content-Type:
     application/x-www-form-urlencoded` with the JSON string (Prototype
     default); run the per-device
     `Interface.setMetadata(inHeatingGroup=true/false)` preamble before
     `save`; `groupName`/`groupDeviceName` via Latin-1 `escape()`;
     `groupDeviceName = "<name> INT<zeroPad(id,7)>"`.
   - Response: `delete` → `{ isSuccessful:true, errorCode:"", content:"[]" }`;
     `errorCode:"42"` == expired session (`ErrAuthFailure`) → re-login + retry.
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
2. **Sync vs. async — RESOLVED: async/fire-and-poll for `save`.** The live
   test showed `save` commits server-side but its HTTP response can time out,
   so a synchronous "save returns → done" model is not viable for create.
   `save` (create) must be fired and completion detected by polling
   `getHeatingGroupList`; `delete` and reads are prompt and can stay
   synchronous. This is ADR 0055 §3, now empirically required.
3. **Live validation — DONE (2026-07-24).** A throwaway `zz_loom_verify`
   group with a window-sensor member was created and deleted on
   `172.18.4.29` (GET create → POST save → group appeared as id 2 → delete →
   roster clean). The write path is confirmed against real HmIP. GR02 still
   ships with hermetic mocked-HTTP tests; this live round-trip is the
   one-time human-in-the-loop confirmation the rule requires.

---

## 3. Wave items — final status (all shipped, 0.48.0)

Dependency order (gap analysis §7, Welle 2): **A3 ✓ → GR01 ✓ → GR02 ✓ →
GR03 ✓, GR04 ✓, GR05 ✓.**

- **GR03 — clean virtual-device label** ✓. The originally-planned
  `Device.setName` / `Channel.setName` (`Gruppenname:Kanalnr`) post-save pass
  proved both **impossible synchronously** and **unnecessary**, established by
  live probing on 2026-07-24:
  - The `group/create` draft always carries `self.groupId = 0`, so at save
    time the real group id is unknown; any serial we build is `INT0000000`.
    HMServer stores the sent `groupDeviceName` **verbatim** (roster
    `GROUP_DEVICE_NAME` = the exact string), so the earlier `save` that sent
    `"<name> INT0000000"` wrote a wrong label onto every loom-created group.
  - `Device.getReGaIDByAddress` / `Interface.getIseIDByAddress` return
    `noDeviceFound` for `INT*` **virtual-device** addresses, even though the
    ReGa ids exist (only `Device.listAllDetail` maps them) — so a
    getReGaIDByAddress-based device rename is a silent no-op.
  - A freshly created group's virtual device does **not** appear in
    `Device.listAllDetail` for **>120 s** after the roster shows the group
    (the ReGa settle lags), so naming it synchronously right after create is
    impossible; the WebUI names it deferred, after the settle.
  - **Resolution:** `SaveHeatingGroup` sends the **bare group name** as
    `groupDeviceName` (no serial suffix). Live-confirmed: the roster
    `GROUP_DEVICE_NAME` then equals the group name exactly, and the CCU derives
    the virtual device's channel names as `<name>:<n>` itself — cleaner than
    the WebUI's `<name> INT<id>:<n>`, and no deferred machinery. The
    ineffective post-save device-rename was removed.
- **GR04 — "operate only via group"** ✓ (`Device.setOperateGroupOnly`). A
  per-member flag applied from the group's `forbid_single_operation`. Note the
  CCU reports the flag back as the **string** `"true"`/`"false"`; the write
  itself was verified live (false→true→restore) on a real member device.
- **GR05 — group assignment on pairing** ✓. The inbox accept dialog offers a
  target heating group for a group-capable device and assigns the accepted
  device's channels via `updateGroup`.

The Programmeditor items PR01–PR06 remain **explicitly out of scope** by
prior decision.

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
