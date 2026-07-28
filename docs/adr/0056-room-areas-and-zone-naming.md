# ADR 0056 — Room areas, and the zone/area naming split

- Status: accepted
- Date: 2026-07-28

## Context

Operators want to group CCU rooms one level higher — a floor, a shed, a
terrace roof — and filter device/alarm/group views by that grouping. The
CCU itself has only flat rooms and functions (Gewerke), both per
central.

At the same time the alarm system's armable partition was called an
"area" (DE „Bereich"), which is exactly the word operators reach for
when naming the room grouping. Keeping both meanings under one word
would have made every alarm mask ambiguous ("Bereich" the room group vs
"Bereich" the armable partition).

## Decision

1. **The alarm partition is a *zone*** (DE „Zone", EN "zone") —
   renamed across the whole surface in one deliberate breaking change
   (REST `/api/v1/alarm/zones`, `zone_id`, WS, MQTT
   `<base>/alarm/<zone>/…`, SQLite schema via data-preserving
   migration 031, SPA, docs). API 3.0.0. There were no external API
   consumers at the time; the Node-RED contrib syncs after release.
2. **The room grouping is an *area*** (DE „Bereich", EN "area") —
   matching Home Assistant's convention, where "areas" (DE „Bereiche")
   are exactly this kind of spatial grouping.
3. **Areas are daemon-owned.** The CCU knows nothing of them:
   `areas` + `room_areas` tables in the daemon's SQLite (migration
   032), REST CRUD under `/api/v1/areas` (API 3.2.0). A room is
   identified as the `(central_name, room_name)` pair — multi-CCU-safe;
   the same room name on two centrals is two assignable rooms.
4. **One area per room.** The `(central, room)` pair is the primary
   key of the assignment; assigning a room to another area moves it.
   This keeps filters unambiguous and the admin UI honest.
5. **Filtering is client-side.** Device/candidate rows already carry
   their rooms; the SPA loads the area list once and derives membership
   (`any room of the item is assigned to the selected area on its
   central`). Device summaries are not enriched server-side — the
   mapping is one small table and one lookup, and keeping it out of the
   hot device-list path avoids invalidation complexity.

## Consequences

- The word "Bereich" in the German UI now always means the room
  grouping; alarm masks consistently say „Zone"/„Alarmzone".
- Area assignments survive CCU re-pairings only as far as room names
  and central names stay stable — a renamed CCU room drops out of its
  area (the assignment row keys on the old name). Acceptable: room
  renames are rare and the admin view makes re-assignment cheap.
- External API consumers get areas read-only cheaply (`GET
  /api/v1/areas`); write operations are operator-gated like the
  rooms/functions admin.
