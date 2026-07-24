# CCU-WebUI Groups Wave (Welle 2) — Status & Handoff

Last updated: 2026-07-24

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
5. **Post-save follow-up (GR03 scope, but wired here).** A new-group success
   then runs JSON-RPC `Device.setName` / `Channel.setName`
   (`Gruppenname:Kanalnr`), `iseDevices.setReadyConfig(regaId)`, and
   `system.saveObjectModel`.

The `delete` success body is still unobserved but is a *minor* unknown: GR02
confirms deletion by re-reading `getHeatingGroupList`. The session-invalid
wrapper `{ isSuccessful:false, errorCode:"42", content:<login html> }` is the
only literal response body captured live so far; the shapes above come from
the authoritative firmware template.

### jpages endpoints

All are `POST … ?sid=<JSON-RPC session>` with a `JSON.stringify` body.
On an **invalid** session the reply is the wrapper
`{ "isSuccessful": false, "errorCode": "42", "content": <login html> }`
(live-verified 2026-07-24; the earlier `{ "valid": … }` reconstruction is
superseded). On a **valid** session, data endpoints return their bare data
object (see above); the mutation success wrapper is still to be confirmed.

| Endpoint | Request body | Purpose |
|---|---|---|
| `/pages/jpages/group/save` | `{ groupName, groupTypeId, forbidSingleOperation, assignedDevicesIds: [memberId…], isNewGroup, groupDeviceName, groupId? }` | create (`isNewGroup:true`) **and** edit (`groupId` set) |
| `/pages/jpages/group/delete` | `{ groupId }` | delete |
| `/pages/jpages/group/suitableGroupMembers` | `{ groupTypeId }` | assignable + leftover members for a type |
| `/pages/jpages/group/getAllAssignableGroupTypes` | — | group-type list for the create form — **404 on the 2026-07-24 live check**; the type list source is unconfirmed (see corrections above) |
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
   `SaveGroup / DeleteGroup / SuitableGroupMembers / GroupTypes`
   to `CcuBackend`, POSTing to the jpages paths using the **existing**
   `b.baseURL` + `b.sessionIDFn()` already set for the firmware download
   path — the ADR 0055 session finding (re-verified 2026-07-24) is what
   makes this free. Critical details from the `_SaveGroup()` contract above:
   send the JSON body with **`Content-Type: application/x-www-form-urlencoded`**
   (Prototype default — `application/json` hangs the save handler); run the
   per-device `Interface.setMetadata(inHeatingGroup=true/false)` preamble
   before `save`; parse the reply on `isSuccessful` (success carries
   `content` → virtual-device serial); treat `errorCode:"42"` as an expired
   session (`ErrAuthFailure`) → session-managed transport re-login + retry;
   do **not** hold the request for the settle — confirm completion by
   re-reading `getHeatingGroupList`.
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

Update 2026-07-23: gap-analysis §7 Wellen **3, 5 and 6 have since shipped
with 0.47.0** (device workflows, direct-link overview/role-match/test,
diagram definitions, sysvar-usage, recording toggle, weekly-profile
gaps), so **GR02 is now the single largest remaining decided (`umsetzen`)
item** — see the plan in §2 above. The Programmeditor items PR01–PR06
remain **explicitly out of scope** by prior decision.

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
