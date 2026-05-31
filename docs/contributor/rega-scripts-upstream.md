# OpenCCU-Loom — four Go-specific ReGa scripts

This note documents the four `.fn` (ReGa / HomeMatic Script) files
that ship in `internal/client/rega/scripts/` but **do not exist** in
[`aiohomematic`](https://github.com/SukramJ/aiohomematic). They
support REST endpoints that aiohomematic's reference architecture
delegates to Home Assistant (room/function assignment, sysvar
lifecycle).

The scripts are written so they can be lifted, unchanged, into
aiohomematic's `aiohomematic/rega_scripts/` directory if upstream
ever wants the same standalone capability. This document is meant as
the PR description for that contribution.

---

## Why these are OpenCCU-Loom-only today

aiohomematic is a Home-Assistant integration first; it relies on
HA's Areas / Labels for room and function classification, and HA's
own helpers for sysvar lifecycle management. OpenCCU-Loom is a
**standalone daemon** — there is no host integration to delegate to.
Sysvar create / update / delete and room / function assignment have
to round-trip to the CCU directly, and the CCU only accepts those
mutations through ReGa script execution.

The five scripts below are therefore **net-new capability** for an
aiohomematic operator running the daemon outside HA, and **risk-free
additions** for HA users (none of them are wired by default in the
HA integration).

---

## Script catalogue

Each script lives in `internal/client/rega/scripts/<name>.fn`. The
`!#` header lines are not metadata — they are valid HomeMatic-Script
comments. The `##placeholder##` tokens are substituted by the Go
caller before the script is sent to `ReGaScript.exe`.

### 1. `create_system_variable.fn`

| | |
|---|---|
| **Verb / Endpoint** | `POST /api/v1/sysvars` |
| **Inputs**          | `name`, `type` (`BOOL` \| `INTEGER` \| `FLOAT` \| `STRING` \| `ENUM`), `unit`, `min`, `max`, `values` |
| **Output**          | new sysvar id (or empty string on failure) |
| **Idempotency**     | yes — if a sysvar with the same name already exists, the script returns the existing id and does not modify metadata |

Creates a new system variable. Type-specific branches set the right
combination of `ValueType` / `ValueSubType` / `ValueName0` /
`ValueList`. ENUMs accept a semicolon-separated list of names.

### 2. `update_system_variable.fn`

| | |
|---|---|
| **Verb / Endpoint** | `PATCH /api/v1/sysvars/{name}` |
| **Inputs**          | `name`, `unit`, `min`, `max`, `values`, `description` |
| **Output**          | `"ok"` on success, empty string otherwise |
| **Idempotency**     | yes — empty string for a field leaves the existing value untouched |

Patches a sysvar's metadata in place. Type changes are intentionally
**not supported** — the CCU does not handle them safely. Callers
delete (JSON-RPC `SysVar.deleteSysVarByName`) and recreate (JSON-RPC
`SysVar.createBool/createFloat/createEnum` or the
`create_system_variable` Rega script for INTEGER/STRING/Unit) instead.

### 3. `set_device_functions.fn`

| | |
|---|---|
| **Verb / Endpoint** | `PUT /api/v1/devices/{address}/functions` |
| **Inputs**          | `address` (channel or device address), `functions` (`\n`-separated list, empty string clears) |
| **Output**          | count of assignments after the update |
| **Idempotency**     | yes — full replacement: every existing assignment is dropped, then the listed functions are added |

Replaces the function (HomeMatic *Gewerk*) assignments for one channel/device.
Functions not registered on the CCU are silently skipped — the
caller calls `getDevices` afterwards to see the canonical state.

### 4. `set_device_rooms.fn`

| | |
|---|---|
| **Verb / Endpoint** | `PUT /api/v1/devices/{address}/rooms` |
| **Inputs**          | `address` (channel or device address), `rooms` (`\n`-separated list, empty string clears) |
| **Output**          | count of assignments after the update |
| **Idempotency**     | yes — same full-replace semantics as `set_device_functions` |

Mirrors `set_device_functions.fn` for rooms. Rooms not registered on
the CCU are silently skipped.

---

## Suggested upstream PR shape

If aiohomematic decides to take these on, the smallest reasonable PR
is:

1. Drop the four files into `aiohomematic/rega_scripts/`.
2. Add a thin `RegaScriptName` entry per script (the existing enum
   already names every other script).
3. Add a `Client.set_room_assignment(...)`-style coroutine per
   script. The Python wrappers map directly to the Go callers in
   `internal/client/rega/client.go` — feel free to pattern-match.
4. No unit tests are required at the script level (they have no
   logic that is amenable to mocking); coverage comes from the
   integration suite (`pydevccu` / `godevccu`).

The scripts follow aiohomematic's existing `!#` header convention —
the only stylistic decision is whether to drop the explicit
`openccu-loom —` prefix.

---

## Tracking

Source of truth for these scripts is **OpenCCU-Loom** —
`internal/client/rega/scripts/`. If aiohomematic adopts them, this
document stays as the historical record. If the scripts diverge
between the two projects, the divergence must be written up here
under a *Divergence* section before merging.
