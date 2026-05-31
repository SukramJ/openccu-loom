-- +goose Up
-- +goose StatementBegin

-- matter_metadata is a key-value store for per-daemon Matter scalars.
-- Currently holds the monotonically-advancing next_fabric_index counter
-- so AddFabric allocates indices that do not re-use freshly-removed
-- slots — mirroring matter.js FabricManager.ts #nextFabricIndex
-- (persisted in FabricManager.ts:38,71,163-164,186-188) and chip
-- FabricTable.h:1149-1152 kNextAvailableFabricIndexTag. Drift L9-D8.
CREATE TABLE matter_metadata (
    key   TEXT    PRIMARY KEY,
    value INTEGER NOT NULL
);

-- Seed the counter at 1 so the first AddFabric allocates index 1.
-- The counter is incremented (and wrapped 255→1) after each successful
-- AddFabric.
INSERT INTO matter_metadata (key, value) VALUES ('next_fabric_index', 1);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS matter_metadata;

-- +goose StatementEnd
