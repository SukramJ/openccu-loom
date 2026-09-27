# ADR 0073 — A backend-neutral taxonomy for rooms, functions and other enums

- Status: accepted
- Date: 2026-09-27

## Context

On a CCU, rooms and functions ("Gewerke") are flat lists of ReGa objects,
keyed by an integer id and shown by name. The whole north-bound surface —
`DeviceSummary.rooms`, the channel summaries, MQTT `suggested_area`, the
overview grouping, alarm and group pickers, areas (ADR 0056) — speaks in
those names.

openccu-lite's metadata store organises devices in **enums** whose nodes
**nest**: `room/eg/wohnzimmer` is the living room on the ground floor, and a
box may carry enums beyond rooms and functions. Node ids are path segments,
not integers; two nodes may share a display name ("Küche" on two floors);
and the store has a revision counter and a change stream.

Flattening the tree into names loses the tree and merges the two kitchens.
Flattening ancestors into `rooms` would give every device on a floor two
rooms and break every consumer that expects a device's own room.

## Decision

A backend-neutral **taxonomy** model, `internal/model/taxonomy`, holds a
central's enums as trees of nodes. It is pure data and is not persisted:
both sources are authoritative and cheap to re-read.

- **Identity.** A node is named by a reference `<enum>/<path>` where the path
  joins node ids with `/` (`room/eg/kueche`). On a CCU every enum is one level
  deep and a node's path is the ReGa id of the room or function; the CCU's
  ISE-id joins stay inside the CCU adapter.
- **Mirror.** The per-central device-details cache carries the taxonomy and,
  per address, the directly assigned references. The CCU adapter fills it
  from the same JSON-RPC reads as before; the lite adapter from one metadata
  snapshot before the devices load, then from the metadata change stream.
- **Names stay names.** `rooms`, `functions` and every name-based surface keep
  the display names of the *directly* assigned nodes. Ancestors are not
  flattened in. MQTT `suggested_area` keeps its rule (the room, when exactly
  one is assigned). Nothing changes for a CCU.
- **Paths are additive.** Devices and channels on REST, WebSocket and the
  MQTT info payload gain a `taxonomy` array (`enum`, `path`, `name`,
  `parent_path`); `GET /rooms` and `GET /functions` name the nodes behind
  each name (`refs`); `GET /taxonomy` serves every tree, empty nodes
  included. Writes can name a node (`room_paths`, `function_paths`), which is
  how one of two same-named rooms is chosen. Nodes are created, renamed,
  moved and deleted through `/taxonomy/{central}/{enum}/nodes`; on a CCU only
  flat root nodes of rooms and functions, nesting and moving are refused with
  the `taxonomy.tree` feature (ADR 0074).
- **An ambiguous name is refused**, not guessed: assigning by a name several
  nodes share answers `409` with the candidate paths.
- **Areas stay keyed by (central, room name)** (ADR 0056). On a box, a name
  several rooms share is one area assignment; the tree itself is the lite
  grouping. Documented limitation.
- **The room/function create answer is corrected.** `POST /rooms` and
  `POST /functions` always sent `id` as an integer while the specification
  said string; the specification now says integer, `id` is optional (absent
  on a box, whose nodes have no integer id) and `path` names the created
  node. The contract guard classifies the correction as breaking, so it
  carries REST API 12.0.0.

## Alternatives considered

- **Flatten ancestors into `rooms`.** Every device on a floor would be in two
  rooms; `suggested_area` and every "the device's room" consumer break.
- **Replace names by paths on the existing fields.** A breaking change for
  every client, for a structure a CCU does not have.
- **Persist the taxonomy.** A second truth to invalidate; the sources are
  cheap and authoritative.
- **Key areas by node reference.** Correct for boxes, a migration for every
  existing area on a CCU; deferred until a need is shown.

## Consequences

- Clients that only know names keep working; clients that care about trees
  read `taxonomy` and `GET /taxonomy`.
- The SPA edits nested taxonomies as trees, picks assignments by path, and
  groups and filters the overview and alarm pickers by the path of a nested
  node. Heating-group candidates still carry names only.
- The CCU's ReGa ids no longer leak as a separate concept: they are a node's
  path on a CCU.
