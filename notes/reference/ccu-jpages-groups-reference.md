# Heating groups: the CCU jpages wire contract

Reference for the CCU-side interface behind OpenCCU-Loom's heating-group
administration. **Everything described here ships** — the read path since
0.46.0, the write path since 0.48.0 (gap-analysis items GR01–GR05).

This is not a status document. It preserves the wire knowledge that was
gathered by observing the CCU WebUI's own HTTP traffic, reading the shipped
OCCU templates, and probing a live CCU — none of it is documented by eQ-3,
and **godevccu implements neither `CCU.getHeatingGroupList` nor any jpages
endpoint**, so there is no simulator to re-derive it from. Changing the group
code without this page means repeating the reconnaissance against real
hardware.

Related:
[ADR 0055 — heating groups via CCU jpages proxy](../../docs/adr/0055-groups-jpages-proxy.md)
(the decision),
[gap analysis](./ccu-webui-gap-analysis.md) §4.3 (the GR01–GR05 items).

---

## 1. Session model and roster

### Why a proxy (ADR 0055)

Group **mutations** are exposed by **proxying the CCU's own
`/pages/jpages/group/*` HMServer endpoints** with Loom's live JSON-RPC
session, rather than re-implementing the direct-link matrix in Go (which
would drift against eQ-3's firmware).

The load-bearing question — *does `/pages/jpages` accept Loom's JSON-RPC
session, or does it need a separate WebUI login?* — was answered by watching
the WebUI's own requests:

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

### The roster read (GR01)

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
by decoding each property lazily). The write path, which sends
`forbidSingleOperation` in the save body, mirrors this typing:

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

## 2. The write path — jpages wire contract

Create / edit / delete groups plus member selection run through the jpages
proxy. The wire shapes below were reconstructed from the shipped, readable
jpages page template `GroupEditPage.ftl` and the request/response bodies
observed on live `/pages/jpages/group/*` calls.

### Transport and endpoint availability

Verified against the firmware on `172.18.4.39` (reads only, no mutation):

- **The transport is the one already wired per central.** `CcuBackend`
  carries `baseURL` + `sessionIDFn` (`internal/client/backends/ccu.go`),
  populated at start-up by `ccuBackend.SetDownloadFirmwareTransport(...)` in
  `internal/central/adapter/ccu_wiring.go`. The group calls needed **no new
  transport wiring** — they POST to the jpages paths on the same base URL +
  session.
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

**Two shapes that differ from what the WebUI code alone suggests:**

1. **Response shape.** The reply is
   `{ "isSuccessful": true|false, "errorCode": "…", "content": "…" }` — not
   `{ "valid": … }`. Key on `isSuccessful`. An invalid/expired session
   returns `errorCode:"42"` with an HTML login-redirect in `content`; the
   backend maps that to `ErrAuthFailure` and triggers a JSON-RPC re-login +
   retry rather than surfacing it as a group error.
2. **There is no group-type enumeration endpoint.**
   `group/getAllAssignableGroupTypes` 404s on this firmware even with a
   valid session — it is simply not there. The type id the HmIP heating case
   needs (`hmip.heating.group`) is known from the roster; a general
   assignable-type source remains unknown (and unneeded so far).

### Data reads with a valid session (against 172.18.4.29)

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
  on this firmware. The one type id needed for HmIP heating
  (`hmip.heating.group`) is known from the roster; a full assignable-type
  enumeration source is still unknown but low-priority (HmIP heating is the
  primary case; BidCos heating-group type ids, if needed, come from a
  roster/`suitableGroupMembers` sample).

### `save` contract — authoritative from `GroupEditPage.ftl` `_SaveGroup()`

Two approved throwaway writes on `172.18.4.29` (empty create, and a create
with one confirmed window-sensor member) **both hung for the full client
timeout** (120 s / 90 s) with **no** group appearing (roster stayed clean).
Reading the shipped `_SaveGroup()` (lines ~524–615) explains why and pins the
real contract:

1. **Transport / content-type (the likely hang cause).** The WebUI sends the
   save via Prototype `new Ajax.Request(url, {postBody: JSON.stringify(data),
   onComplete})`. Prototype's default `Content-Type` is
   **`application/x-www-form-urlencoded`** (charset UTF-8) with the JSON
   string as the *raw* body. Probes that sent `application/json` blocked; a
   save handler reading a form-encoded body can hang on that mismatch. The
   JSON body is therefore POSTed with `Content-Type:
   application/x-www-form-urlencoded`, not `application/json`. (Reads like
   `suitableGroupMembers` tolerated `application/json`; `save` apparently
   does not — treat the form content-type as required for all jpages POSTs.)
2. **Pre-save metadata preamble.** Before the POST, the WebUI issues, per
   device, JSON-RPC `Interface.setMetadata({objectId: device.id, dataId:
   "inHeatingGroup", value: "true"|"false"})` — `true` for assigned members,
   `false` for every other assignable device. The save flow replicates this
   preamble.
3. **Success response shape (confirmed from the code path).** `onComplete`
   parses `response = JSON.parse(t.responseText)` and branches on
   `response.isSuccessful`. On success of a **new** group it derives the
   virtual device serial via `createVirtualDeviceSerialNumber(response.content)`
   — so success is `{ isSuccessful: true, content: <virtual-device info> }`.
   On failure, `{ isSuccessful: false, errorCode, content }`;
   `errorCode == sessionTimeoutErrorCode` ("42") means the session expired.
4. **`save` returns before the settle finishes.** After a successful reply the
   WebUI collects `devicesInConfigPending` and tracks the `CONFIG_PENDING`
   settle **separately** — the HTTP response is not gated on the settle. The
   request is therefore not held open for the settle; it POSTs (with a sane
   timeout), then, like the WebUI, watches the members' `CONFIG_PENDING` /
   re-reads `getHeatingGroupList` for completion + the new `groupId`.
5. **Post-save follow-up (what the WebUI does).** A new-group success then runs
   JSON-RPC `Device.setName` / `Channel.setName` (`Gruppenname:Kanalnr`),
   `iseDevices.setReadyConfig(regaId)`, and `system.saveObjectModel`. Loom does
   **not** replicate this — see §3: the naming is instead handled by the bare
   `groupDeviceName` sent in the save, and the synchronous JSON-RPC rename is
   impossible on loom's side anyway (virtual device unresolvable + settle lag).

### New-group flow (live-confirmed on 172.18.4.29)

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
create succeeded while the client timed out. The save is therefore **fired,
and the new group appearing in `getHeatingGroupList` (with its
server-assigned id) is the completion signal**, NOT the save HTTP response.
This is the async design in ADR 0055 §3, empirically required rather than
merely preferred. `delete` (and the reads) return promptly, so only the
`save`/create leg needs the fire-and-poll treatment. The new group's id for a
later edit/delete comes from the roster, not from the save reply.

### jpages endpoints

All are `POST … ?sid=<JSON-RPC session>` with a `JSON.stringify` body.
On an **invalid** session the reply is the wrapper
`{ "isSuccessful": false, "errorCode": "42", "content": <login html> }`
(live-verified). On a **valid** session, data endpoints return their bare
data object (see above). `delete` returns `{ isSuccessful:true,
errorCode:"", content:"[]" }`; `save` commits but its response is slow / may
time out — see the new-group flow above.

| Endpoint | Method + body | Purpose |
|---|---|---|
| `/pages/jpages/group/create` | **GET** (no body) | **precursor for a new group** — allocates the draft, returns the edit-page HTML with the id; a `save` create hangs without it |
| `/pages/jpages/group/save` | POST `{ groupName, groupTypeId, forbidSingleOperation, assignedDevicesIds: [memberId…], isNewGroup, groupDeviceName, groupId? }` | commit — create (`isNewGroup:true`, after `group/create`) **and** edit (`groupId` set). Response is slow: fire-and-poll `getHeatingGroupList` |
| `/pages/jpages/group/delete` | POST `{ groupId }` | delete — returns `{ isSuccessful:true, content:"[]" }` promptly |
| `/pages/jpages/group/suitableGroupMembers` | `{ groupTypeId }` | assignable + leftover members for a type |
| `/pages/jpages/group/getAllAssignableGroupTypes` | — | group-type list for the create form — **404 on the live firmware**, no replacement found (see above) |
| `/pages/jpages/group/list` | filter object | renders the HTML group page, not a JSON API — the roster comes from `CCU.getHeatingGroupList` instead |

Field notes from `GroupEditPage.ftl` (the `save()` view-model):

- `groupName = escape(viewModel.groupName())` — JS `escape()`, i.e.
  **Latin-1 percent-encoding**; the server side decodes **ISO-8859-1**.
  Loom mirrors this rather than sending raw UTF-8, or umlauts corrupt
  (e.g. "Wohnzimmer Süd").
- `groupDeviceName` = the virtual-device label. The WebUI sends
  `<group_name> + " " + virtualDeviceSerialNumber`, but that serial is built
  from the always-zero draft id, so loom sends the **bare group name** instead
  (see §3 for the live rationale and the resulting `<name>:<n>` channels).
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
  device + members. Loom does not replicate this JSON-RPC pass (see §3).

The `suitableGroupMembers` replies are plain JSON objects
(assignable/leftover member lists); their field names are taken straight from
the observed responses.

### Where this lives in the code

- **Backend.** `CcuBackend` (`internal/client/backends/`) holds the jpages
  calls; they reuse the `baseURL` + `sessionIDFn()` that
  `SetDownloadFirmwareTransport` already installs — that reuse is what ADR
  0055's session finding buys.
- **Adapter.** `GroupsDomain` in `internal/central/adapter` resolves the
  primary backend per central and owns the fire-and-poll completion logic.
- **North.** REST `POST /groups`, `PUT /groups/{id}`, `DELETE /groups/{id}`,
  `GET /groups/types`, `GET /groups/suitable-members` (admin-gated,
  audited) and the WS twins `groups.create / update / delete / types /
  suitable_members`.
- **Tests are hermetic** (mocked `httptest` for the jpages bodies and the
  session-invalid wrapper, plus adapter/handler/WS/SPA tests). Since godevccu
  has no jpages surface, the e2e suite skips the group commands
  (`tests/e2e/wsapi_skip.txt`) and the live CCU is the only real validation
  — which is why the observations on this page matter.

---

## 3. Behaviour Loom deliberately does differently

- **The virtual-device label is the bare group name.** The originally-planned
  `Device.setName` / `Channel.setName` (`Gruppenname:Kanalnr`) post-save pass
  proved both **impossible synchronously** and **unnecessary**, established by
  live probing:
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
- **"Operate only via group" is written per member** (`Device.setOperateGroupOnly`),
  derived from the group's `forbid_single_operation`. The CCU reports the flag
  back as the **string** `"true"`/`"false"`, not a boolean — mind the typing.
  The write was verified live (false→true→restore) on a real member device.
- **A group's virtual backing device is filtered out of the pairing inbox.**
  The CCU's inbox query returns every not-yet-configured object, including the
  `INT`-prefixed virtual device of a fresh heating group; accepting one can
  never succeed, so the daemon drops CCU-internal virtual devices from the
  inbox (0.48.7).

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
  or any jpages surface, hence the group e2e skips and the mocked-HTTP test
  strategy.
- Live-CCU rule: reads are free, **writes need explicit approval and a named
  target** (`CLAUDE.md` → Critical Rules). Every write observation on this
  page came from an approved throwaway group that was deleted afterwards.
