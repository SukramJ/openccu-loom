# ADR 0055 — Heating-group administration via the CCU jpages proxy

Date: 2026-07-22
Status: accepted
Related:
[ADR 0002 — multi-CCU first-class](./0002-multi-ccu-first-class.md)

## Context

Homematic heating groups (HmIP and BidCos "Heizungsgruppen") are the
last large CCU-WebUI capability OpenCCU-Loom does not expose. A group is
a virtual device on the `VirtualDevices` interface whose members are
wired to it through a type-specific direct-link matrix; the CCU keeps
the member roster in `/etc/config/groups.gson` and drives a
`CONFIG_PENDING` settle after every change. Today Loom surfaces the
virtual group device as an ordinary device with no member context, and
offers no way to create, edit, or delete a group.

The CCU implements group orchestration in **two** places, and the
distinction decides our approach:

- **Reading the roster** is a plain JSON-RPC call. `CCU.getHeatingGroupList`
  (`occu/WebUI/www/api/methods/ccu/getheatinggrouplist.tcl`) does nothing
  but read `/etc/config/groups.gson` and return it. Any JSON-RPC client
  with a valid session can call it.
- **Mutating a group** (create / save / delete / member selection) runs
  entirely inside HMServer, exposed over the HTTP endpoints
  `/pages/jpages/group/{list,create,save,delete,suitableGroupMembers,
  configureDevices,assignedGroupMembers}`. HMServer builds the virtual
  device, computes the per-type direct-link matrix, maintains
  `groups.gson`, and sequences the `CONFIG_PENDING` follow-up. The shipped,
  readable page templates are `occu/HMserver/opt/HMServer/pages/GroupListPage.ftl`
  and `GroupEditPage.ftl`; lighttpd proxies `^/pages/jpages` to HMServer on
  `127.0.0.1:9292` (`WebUI/etc/lighttpd/conf.d/proxy.conf`).

Reproducing that matrix natively in Go would mean re-deriving, per group
type and per device generation, which link roles wire to which — logic
eQ-3 evolves with every firmware. Drift there is silent and only shows
up as a mis-wired group on real hardware.

### The load-bearing question: which session does jpages accept?

The one fact that had to be verified before committing to a proxy was
whether `/pages/jpages` authenticates with the JSON-RPC session Loom
already holds, or with a separate WebUI session that Loom would have to
establish on its own.

It is the JSON-RPC session. HMServer validates the request's `sid` by
POSTing it to ReGa's JSON-RPC endpoint `/api/homematic.cgi` — the same
endpoint and the same session token that `Session.login` returns. The WebUI passes exactly this token to
jpages as a query parameter (`.../group/...?sid=<SessionId>`, e.g.
`occu/WebUI/www/config/easymodes/js/Group.js`). There is no second login
and no separate WebUI cookie in the path.

Loom already holds that token: the JSON-RPC client keeps it in
`jsonrpc.Client.SessionID()` and renews it on its own cadence. And Loom
already has the precedent for session-authenticated raw HTTP to the CCU:
`CcuBackend.SetDownloadFirmwareTransport(baseURL, hc, sessionIDFn)` posts
to `/config/cp_maintenance.cgi` and `/config/cp_security.cgi` with
`sid := sessionIDFn()`. The jpages proxy is that same mechanism aimed at
a different path.

## Decision

Expose heating-group administration by **proxying the CCU's own jpages
endpoints with Loom's live JSON-RPC session**, not by re-implementing the
group-wiring matrix in Go.

1. **Reads go through JSON-RPC, not the proxy.** The group list
   (GR01) is served by calling `CCU.getHeatingGroupList` through the
   existing `CcuBackend` / `jsonrpc.Client` and joining the roster
   against Loom's device model. This keeps the read path on the same
   typed, retried, session-managed transport as every other CCU read and
   needs no HTTP-proxy machinery.

2. **Mutations go through the jpages proxy.** Create / save / delete /
   member-selection (GR02–GR05) call
   `/pages/jpages/group/{create,save,delete,suitableGroupMembers}` over
   HTTP, authenticated with `?sid=<sessionIDFn()>`, following the
   `SetDownloadFirmwareTransport` pattern: a per-central southbound HTTP
   transport wired with the CCU base URL, a bounded `http.Client`, and a
   `sessionIDFn func() string` that returns the live JSON-RPC session. If
   the session is empty (never logged in / logged out), the call fails
   with the same `ErrUnsupported`-class error the firmware path uses.

3. **The write path is asynchronous with progress broadcasts.** A group
   save triggers a `CONFIG_PENDING` settle on the virtual device and its
   members; the REST/WS surface models this as a job with progress
   broadcasts (GR02 is sized XL for exactly this reason), mirroring the
   settle handling already used elsewhere in the wave work.

4. **Native rebuild is the documented fallback, ADR-gated.** If a future
   CCU firmware ever drops or firewalls the jpages endpoints (e.g. a
   fully sealed-CCU deployment, cf. the open A2 JSON-RPC-compat item), a
   native reconstruction of the direct-link matrix becomes necessary.
   That is a separate, large decision and gets its own ADR; it is
   explicitly out of scope here.

## Consequences

- The group **read** surface (GR01) is available immediately and cheaply
  and does not depend on the proxy transport landing.
- The group **write** surface reuses the CCU's certified orchestration
  verbatim, so it cannot drift from eQ-3's link-matrix logic — the whole
  point of choosing the proxy.
- Loom takes a dependency on the jpages endpoints and their request /
  response shapes. These are internal CCU surfaces, not a public
  contract, so the proxy tolerates unknown fields and treats non-2xx
  responses as opaque errors surfaced to the operator, rather than
  parsing HMServer internals.
- Group administration requires a CCU that actually runs HMServer with
  jpages reachable (the standard RaspberryMatic / OpenCCU / CCU3 case).
  Homegear and any sealed-CCU setup do not offer it; those backends
  report the capability as unavailable rather than failing mid-write.
- The proxy inherits the JSON-RPC session's lifecycle for free: renewal,
  backoff, and logout are already handled by `jsonrpc.Client`, so the
  group transport never manages credentials itself — it only reads the
  current session id at call time.
