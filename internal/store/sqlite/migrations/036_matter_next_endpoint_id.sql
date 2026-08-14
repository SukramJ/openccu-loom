-- +goose Up
-- +goose StatementBegin

-- next_endpoint_id is the monotonically-advancing high-water mark for
-- bridged Matter endpoint numbers. Without it the allocator walked the
-- existing rows and handed out the smallest unused number, so a number
-- freed by an unpaired device (or by a revoked exposure) was reissued to
-- the next unrelated device. Controllers cache their accessory list by
-- endpoint number and see no structural change when the number set stays
-- the same, so the new device arrives under the removed device's identity.
--
-- Mirrors matter.js packages/node/src/storage/server/ServerEndpointStores.ts,
-- which allocates from the persisted #nextNumber (NEXT_NUMBER_KEY) and never
-- rewinds it when an endpoint store is erased.
--
-- Seed above every number the table already holds so an upgraded database
-- keeps its current assignments. COALESCE covers the empty table: bridged
-- endpoints start at 2 (0 = RootNode, 1 = Aggregator).
-- OR IGNORE keeps the migration idempotent without an ON CONFLICT clause,
-- which SQLite cannot parse unambiguously after an INSERT ... SELECT.
INSERT OR IGNORE INTO matter_metadata (key, value)
SELECT 'next_endpoint_id', COALESCE(MAX(endpoint_id), 1) + 1 FROM matter_endpoints;

-- +goose StatementEnd

-- Down deletes the next_endpoint_id counter row. The next Up reseeds it from
-- the endpoint table, so nothing is lost while the rows survive; running Down
-- together with a matter_endpoints reset would restart the sequence at 2 and
-- reissue numbers that commissioned controllers still have cached.
-- +goose Down
-- +goose StatementBegin

DELETE FROM matter_metadata WHERE key = 'next_endpoint_id';

-- +goose StatementEnd
