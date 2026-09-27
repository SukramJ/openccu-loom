# openccu-lite — live verification against a real box

Status: **open — no box available yet**; the open items of the whole
openccu-lite support are listed [below](#open-items-of-the-openccu-lite-support) (decided 2026-09-27: documentation
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
What is not, and why:

| Item | Why open | Unblocked by | Documented in |
|---|---|---|---|
| Heating groups on openccu-lite: create, update, members, member candidates | The member format, the member id, the group id type and `devices_to_configure` are not in the wire contract; guessing could put devices into the wrong group. Listing and deleting work. Kept refused by decision (2026-09-27). | L-10 reads | `docs/admin/openccu-lite.md`; `internal/central/adapter/lite_groups.go` |
| PONG relay on the event stream | Unconfirmed that the interface processes relay a PONG for a caller they did not register | L-4 | ADR 0072 |
| Radio cost of `getParamset(VALUES)` seeding | Not measured | L-6 | `docs/caching.md` |
| SSE unbuffered through the box's web server | Not measured | L-2 | — |
| Encrypted backup restore (`.sbk.age`) | The daemon does not hold the box's recovery key; refused by design | — (design) | `docs/admin/backup.md` |
| Areas keyed by room name | On a box a name several rooms share is one area assignment | — (documented limitation) | ADR 0073 |
| Heating-group candidates show room names only | The candidates DTO carries no taxonomy; showing the path needs an API addition | API change | ADR 0073 |
| Interface pre-selection from the probe | Planned in the plan's §7.6; the probe answer carries no interface list, so the onboarding does not pre-select | API addition (interfaces on the probe answer) | here |
| Visual baselines for editing an openccu-lite central | The CCU form's lite edit is covered by vitest and a functional Playwright case, not by screenshots | — | here |
| Downstream repositories | `openccu-loom-client`/`-types` and the Node-RED contrib need the new DTOs and the REST API 12.0.0 pin | their own releases | `CHANGELOG.md` |
| Release | Version bump, both add-on changelogs and the pre-release comment-claims sweep are not done yet | the release | CLAUDE.md, implementation policy |

## Results

_None yet._
