# openccu-lite — live verification against a real box

Status: **open — no box available yet** (decided 2026-09-27: documentation
first; this list waits until a box is provided). Phases A–D and the docs
(F1) are merged; everything below was only exercised against the MIT test
double `tests/harness/litefake`.

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
| L-10 | `GET /service-messages`, `GET /system-update`, `GET /groups`, `/upnp/basic_dev.cgi` serial = SSDP serial | — |
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

## Results

_None yet._
