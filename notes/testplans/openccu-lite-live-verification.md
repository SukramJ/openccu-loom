# openccu-lite — live verification against a real box

Status: **all checks done 2026-09-28** against a real box (occulited
`0307f63ce04f`, release `1.0.0-dev.30`, base `3.89.11.20260919`); the writes
ran with the maintainer's approval on devices the maintainer named. The open items of the whole
openccu-lite support are listed [below](#open-items-of-the-openccu-lite-support) (decided 2026-09-27: documentation
first; this list waits until a box is provided). Phases A–D and the docs
(F1) are merged; everything below was only exercised against the MIT test
double `tests/harness/litefake` (since 2026-09-29: godevccu's `pkg/litefake`).

Source of the checks: [implementation plan](../plans/openccu-lite-backend.md)
§9 Phase F / F2.

## Rules

- **Reads are free.** Every check not marked *write* only reads.
- **Every write needs explicit approval AND a user-named target** (CLAUDE.md,
  "Live-CCU writes need explicit user approval"). The table names the check
  type, never the device.
- Record each result with its **negative control** — a check whose control
  produces the same answer measured nothing.
- Needed from the maintainer: the box's address and a token with at least
  `rpc:read`, `meta:read`, `system:read` for L-1 … L-11.

## Checks

| # | Check (read-only unless marked) | Negative control |
|---|---|---|
| L-1 | `GET /api/meta/v1/version`: `api:"meta"`, `capabilities.apis` majors = 1, limits as Appendix A | same call against the CCU (172.18.4.29) is not classified lite |
| L-2 | SSE through lighttpd: `: ping` every 15 s ± 2 s arrives unbuffered, `hello` first | stop reading for 60 s → server-side `resync overflow` or drop observed |
| L-3 | XML-RPC via proxy: `listDevices`, `getParamsetDescription` for one device, with the token | same call without token → 401 JSON |
| L-4 | `ping("<loom initID>#1")` → `CENTRAL`/`PONG` event with that value on the stream within 5 s (resolves §4.5) | a second stream filtered `key=STATE` does not show it |
| L-5 | meta snapshot shape: `objects`, `enums` with `room`/`function` trees | — |
| L-6 | cold lite bring-up: read `DUTY_CYCLE` via `listBidcosInterfaces` before and 5 min after (resolves §4.9) | same measurement with `getParamset` seeding disabled |
| L-7 | `GET /api/rpc/v1/state` paging and `event_id` | — |
| L-8 | `GET /api/system/v1/backup/targets` shape matches Appendix A §A.7 (`state.state`, `last_backup`) | — |
| L-9 | `/api/meta/v1/version` `hmip` fields; `GET /radio/health` | — |
| L-10 | `GET /service-messages`, `GET /system-update`, `GET /groups`, `GET /groups/types`, `GET /groups/{id}` (the member fields, the form of a member id, the group id type, what `devices_to_configure` asks of the operator), `/upnp/basic_dev.cgi` serial = SSDP serial | — |
| L-11 | `GET /ise/checkrega.cgi` answers `200 text/html` (confirms H13) | `GET /api/system/v1/health` is JSON |
| L-12 (**write, approval + named device**) | `setValue` on the user-named actor via Loom REST; event returns on the stream; leave it in its original state | — |
| L-13 (**write, approval**) | rename one user-named device via Loom; the box's meta store shows it; rename back | — |
| L-14 (**write, approval**) | pairing request from Loom; box admin approves; Loom stores the token; then the user deletes the token on the box | — |
| L-15 (**write, approval**) | backup download (runs `createBackup.sh` on the box) | — |

Reboot, restore, system update and halt are not part of this list; the fake
covers them.

## What each open check settles

| Check | Settles | Where the assumption lives today |
|---|---|---|
| L-2 | SSE through the box's web server is not buffered | the stream's heartbeat timeout (`internal/client/transport/occulited/supervisor.go`) |
| L-4 | the interface processes relay `PONG` for a caller they did not register | `backends.Capabilities` of `KindOpenCCULite` keeps `PingPong: true`; if L-4 fails, set it false and rely on the heartbeat (ADR 0072) |
| L-6 | whether `getParamset(VALUES)` seeding costs radio airtime | the lite value seeder's fallback read (`internal/central/adapter/lite_values.go`) |
| L-10 | the heating-groups member/id format of the box's groups API | lite heating-group create/update stay refused until known (`internal/central/adapter/lite_groups.go`) |

## Open items of the openccu-lite support

Everything else from the implementation plan is merged (phases A–D, F1).
Resolved 2026-09-30 (Kearney, real box, loom 0.82.0): the **radio cost of
`getParamset(VALUES)` seeding** is bounded by dose amplification — where
L-6's single sweep stayed below the whole-percent duty-cycle resolution,
three consecutive full sweeps over all 56 HmIP-RF channels (168 reads
through the proxy, three times a boot seed) left `listBidcosInterfaces`
`DUTY_CYCLE` unchanged at 6 %. The interface process answers VALUES reads
from its device mirror; a single boot seed therefore costs well under one
percent of duty cycle. HmIP-RF only; BidCos-RF not measured separately.
Recorded in `docs/caching.md`.

What is not, and why:

| Item | Why open | Unblocked by | Documented in |
|---|---|---|---|
| Heating groups on openccu-lite: create, update, members | Group ids are JSON numbers and a candidate is `{id: <channel address>, serial, type}` (L-10). Writing members fails **on the box itself**: `POST`/`PUT /groups` answer `502 hmipserver … context deadline exceeded` after 30 s (occulited's call to the HMIPServer group save times out), and the create still leaves an empty group behind (G-1). So the shape of a group's `members` list and the meaning of `devices_to_configure` stay unobserved. Kept refused. | a box whose group save succeeds (an openccu-lite fix, to be reported upstream) | `docs/admin/openccu-lite.md`; `internal/central/adapter/lite_groups.go` |
| Encrypted backup restore (`.sbk.age`) | The daemon does not hold the box's recovery key; refused by design | — (design) | `docs/admin/backup.md` |
| Areas keyed by room name | On a box a name several rooms share is one area assignment | — (documented limitation) | ADR 0073 |
| Heating-group candidates show room names only | The candidates DTO carries no taxonomy; showing the path needs an API addition | API change | ADR 0073 |
| Interface pre-selection from the probe | Planned in the plan's §7.6; the probe answer carries no interface list, so the onboarding does not pre-select | API addition (interfaces on the probe answer) | here |
| Visual baselines for editing an openccu-lite central | The CCU form's lite edit is covered by vitest and a functional Playwright case, not by screenshots | — | here |

Done since the list was written: the release (0.79.0, tag `v0.79.0`), the
downstream repositories (openccu-loom-client 2026.9.5 with the new DTOs,
the exception and the event; node-red-contrib-openccu-loom 0.7.0 on API
major 12, published to npm), and — found on the box after the migration —
the CCU add-on's supervision and pidfile under openccu-lite's confined
oneshot unit (#859, shipped in 0.80.0).

## Results

Read-only, 2026-09-28, box `172.18.4.39`, a token with `*` (only reads
were sent). Every result with the control that would have come out
differently had the claim been false.

| # | Result | Negative control |
|---|---|---|
| L-1 | `GET /api/meta/v1/version` answers `api:"meta"`, `apis` all major 1, limits `streams_per_token 2`, `streams_total 16`, `buffer 5000 / 300 s`, `hmip` present; same over HTTP and HTTPS | the CCU (`172.18.4.29`) answers `404 text/html` — not classified lite |
| L-2 | `: connected`, then `hello` with id first; `: ping` at 15.00 s, 30.00 s, 45.00 s, 60.01 s; events arrive at once; `X-Accel-Buffering: no` | — (timestamps would have bunched on a buffering proxy) |
| L-3 | `listDevices` via the proxy with the token: HmIP-RF 114, BidCos-RF 66, VirtualDevices 12 entries; `text/xml; charset=utf-8` | without the token: `401 {"error":"unauthenticated"}` |
| L-4 | `ping("loomlive-bidcos#1")` / `ping("loomlive-hmip#1")` → `CENTRAL`/`PONG` with exactly that value on the stream within 1 s. HmIP answers on address `CENTRAL:0`, BidCos on `CENTRAL`; BidCos answers `ping` with an array `[true]` — both handled (the correlator ignores the address, the backend ignores the answer value) | a second stream filtered `key=STATE` showed no PONG |
| L-5 | snapshot `{format, revision, objects (172), enums: favorite, function, room}`; all trees one level deep on this box | — |
| L-6 | duty cycle 7 % before, 7 % after 3 min idle, 7 % right after and 3 min after 158 `getParamset(VALUES)` calls — **not decidable** at whole-percent resolution; 4 faults (`-1 Failure`) all on one BidCos device's channels | the idle phase (stable at 7 %) — validates the baseline, not a cost below 1 % |
| L-7 | `/state` keys `entries, total, unconfirmed, next, event_id, sweeps, datapoints`; entries carry `confirmed`, `confirmed_at`, `source` | — |
| L-8 | `/backup/targets` `{nightly, container, kinds, encryption, hostname, needed_bytes, targets: []}` | — |
| L-9 | `/radio/health` `{interfaces [duty_cycle …], history, feed, answering, busy, errors}`; `hmip` in `/version`: `KEYSERVER_LOCAL`, 0 device keys, offline pairing | — |
| L-10 | `/groups`: **group ids are JSON numbers** (`"id": 4`) — the client decoded them as strings, so the heating-group list failed on a real box (fixed alongside this record); `/groups/types` candidates `{id: <channel address>, serial, type}`; `/service-messages`, `/system-update` as the contract; UPnP serial `3014F711A0001F5A4993D993` = the HmIP radio address in `/radio/health` | — |
| L-11 | `/ise/checkrega.cgi` → `200 text/html` (the SPA shell) | `/api/system/v1/health` → JSON |

Writes, 2026-09-28, through a locally started OpenCCU-Loom daemon (built from
`main` after #854) with the box as an `openccu-lite` central. The central was
ready ~2 s after start, loaded all 13 devices with the box's names, reported
25 of 33 features (the eight absent: no sysvars, programs, inbox, alarm
messages, service-message acknowledge, communication test, position, safe
mode) and logged no error.

| # | Result | Control |
|---|---|---|
| L-12 | `PUT …/KEQ0843929/channels/1/data-points/STATE/value` true, then false (HM-LC-Sw4-DR, named by the maintainer): both `202`; the device's confirmed `STATE` event reached the box's stream ~150 ms after each write, and Loom reported the new value from it; left **off**, as found | the value read before the first write was `false` |
| L-13 | device `00109709B1381B` renamed through Loom: the box's metadata object read back the new name at revision 4, Loom showed it; renamed back to `test234` (revision 5) | the object read before the write (revision 3, `test234`) |
| L-14 | pairing through Loom (`access: read`): code shown, approved on the box by the maintainer; granted `rpc:read`, `meta:read`, `system:read`, `logs:read`; a central created with the `pairing_id` came up ~4 s later with 7 of 33 features, every absent one naming its scope; no answer carried the token (the stored row reads `***`); the test central was deleted afterwards. The box names the token `openccu-loom-ubuntu-remote-d` (app plus instance, cut to 28 characters); deleted afterwards with the maintainer's approval through `DELETE /api/auth/v1/tokens/<name>` (`auth:admin`) → `200 {"ok":true}`, gone from `GET /api/auth/v1/tokens` | — |
| G-1 | heating-group write probe (approved, member `00109709B1381B:1` named by the maintainer): `POST /groups {Loom-Test, hmip.heating.group, [00109709B1381B:1]}` → `502 hmipserver` after 30 s, yet group 6 existed afterwards with `members: []`; `PUT /groups/6 {members:[…]}` → the same 502, members still empty; `DELETE /groups/6` → `200 {"deleted":6,"former_members":[]}` — **`deleted` is the id, not a flag** (the client decoded a boolean; fixed alongside this record); the group list is back to its two groups and the member device shows no `CONFIG_PENDING` | the group list before the probe (groups 4 and 5) |
| L-15 | `POST /backups` for the central: the box's archive `Kearney-3.89.11.20260919-2026-09-28-1205.sbk` (960 KiB, plain), a CCU-compatible tar (`usr_local.tar.gz`, `signature`, `signature.sha256`, `key_index`, `firmware_version`), stored under the box's own file name | — |
