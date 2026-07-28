-- Areas: operator-defined room groupings above CCU rooms (a floor, a
-- shed, a terrace roof). CCU rooms are flat and per-central; an area
-- collects (central, room) pairs so views can filter one level higher.
-- Distinct from alarm zones (the armable alarm partitions).

-- +goose Up
CREATE TABLE areas (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    position      INTEGER NOT NULL DEFAULT 0,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL
);

-- One area per room: the (central, room) pair is the key, so assigning
-- a room to another area moves it. No FK on area_id — consistent with
-- the alarm tables; the store cascades deletes.
CREATE TABLE room_areas (
    central_name TEXT NOT NULL,
    room_name    TEXT NOT NULL,
    area_id      TEXT NOT NULL,
    PRIMARY KEY (central_name, room_name)
);

CREATE INDEX room_areas_by_area ON room_areas(area_id);

-- +goose Down
DROP INDEX room_areas_by_area;
DROP TABLE room_areas;
DROP TABLE areas;
